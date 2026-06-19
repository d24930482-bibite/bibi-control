package workspace

import (
	"context"
	"fmt"

	"github.com/asemones/bibicontrol/revisionstore"
	"github.com/asemones/bibicontrol/script/thebibites"
)

// TransferOptions carries the opt-in toggles for a cross-world transfer. Both
// default false: Move=false is a copy (source left intact), RemapIDs=false keeps
// today's loud body.id-collision guard.
type TransferOptions struct {
	// Move deletes the grafted entries from the SOURCE world after the destination
	// commit succeeds, so the entity ends up in exactly one world (no biomass
	// double-count). See Transfer's commit-ordering comment for the data-safety
	// rationale of the dst-first / source-second ordering.
	Move bool
	// RemapIDs mints a fresh non-colliding body.id for a grafted bibite instead of
	// aborting the batch on a body.id collision.
	RemapIDs bool
}

// TransferResult is the structured outcome of a Transfer. DstRevision is the
// committed destination revision (its ID is 0 for an empty-selection no-op).
// Moved echoes whether move semantics were requested AND the dst commit happened.
// SourceCommitted reports whether the source-side delete commit succeeded;
// SourceRevision is that commit's revision (ID 0 when no source commit happened).
type TransferResult struct {
	DstRevision     revisionstore.Revision
	Moved           bool
	SourceCommitted bool
	SourceRevision  revisionstore.Revision
}

// Transfer grafts the selected source entries (a where-collection's resolved set
// or a single entity) into the destination world and commits the result as a new
// dst revision — the Go method backing the workspace.transfer(...) automation
// builtin. It reuses the merged F1/F3/M6 transfer engine (via thebibites.
// TransferEntries) for the graft and the unchanged CommitWorldLoaded path for the
// head-advancing commit(s); it writes NO new commit logic.
//
// Selection is the object DSL: srcLS is the SOURCE world's open working copy,
// srcWorldID is its world id (threaded from automation.go so the move's
// source-delete commit can re-resolve the SAME cached source handle without
// reverse-deriving it), and entryNames are the entry_names that copy already
// resolved (scoped to the source world by construction). The user never writes
// SQL/JOINs.
//
// Lock discipline (mirrors CommitWorld/CommitWorldLoaded, commit.go:49-62):
// OpenWorld takes the non-reentrant w.mu itself, so Transfer must resolve working
// copies via OpenWorld BEFORE any further lock and must NOT nest w.mu.
// TransferEntries takes NO lock — it only mutates the in-memory dst (and, for move,
// src) session, which is the SAME cached *LoadedSave handle CommitWorldLoaded
// re-resolves and commits within one hermetic automation cycle (no concurrent
// Unload/Load — the same assumption E2's s.commit relies on, commit.go:99-103).
// CommitWorldLoaded is the only DuckDB/SQLite writer here and owns w.mu for its own
// body; Transfer must NOT hold w.mu across both commits (it is non-reentrant). The
// two commits are sequential and each self-contained.
//
// All-or-nothing at the STAGING boundary: if any graft fails, TransferEntries
// returns an error and Transfer returns WITHOUT committing either side, so the
// partial stages (dst and, for move, src) die with the in-memory sessions and no
// head advances. An empty selection is a clean no-op: it returns the zero result
// (DstRevision.ID == 0) and does NOT advance any head.
//
// CROSS-SAVE NON-ATOMICITY (the load-bearing data-safety decision). A move is TWO
// separate world commits with NO two-phase coordination. We commit the DST graft
// FIRST and only commit the SOURCE delete if the dst commit succeeded. Rationale:
// the only failure window (a crash/error AFTER the dst commit but BEFORE/DURING the
// source-delete commit) then leaves the entity in BOTH worlds — a recoverable
// DUPLICATE, identical to today's copy outcome — never in NEITHER world
// (unrecoverable data LOSS). Committing source-first, or coupling the two commits
// into one critical section, would re-open the loss window and is WRONG. If the dst
// commit succeeds but the source-delete commit fails, we return an error that NAMES
// the half-applied state so the user is never silently left with a duplicate they
// believe was moved.
func (w *Workspace) Transfer(ctx context.Context, srcLS *thebibites.LoadedSave, srcWorldID string, entryNames []string, dstWorldID string, opts TransferOptions) (TransferResult, error) {
	if w == nil {
		return TransferResult{}, fmt.Errorf("workspace: Transfer on nil workspace")
	}
	if srcLS == nil {
		return TransferResult{}, fmt.Errorf("workspace: Transfer source is nil")
	}
	if srcWorldID == "" {
		return TransferResult{}, fmt.Errorf("workspace: Transfer source worldID is required")
	}
	if dstWorldID == "" {
		return TransferResult{}, fmt.Errorf("workspace: Transfer destination worldID is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// Empty selection: a clean no-op. Nothing is staged, so committing would be a
	// no-op anyway; return the zero result without touching any head.
	if len(entryNames) == 0 {
		return TransferResult{}, nil
	}

	// Resolve the dst working copy WITHOUT holding w.mu (OpenWorld takes its own
	// lock; w.mu is non-reentrant). This is the SAME cached handle CommitWorldLoaded
	// re-resolves and commits, so the grafts staged below are exactly what commits.
	dstLS, err := w.OpenWorld(ctx, dstWorldID)
	if err != nil {
		return TransferResult{}, fmt.Errorf("workspace: Transfer open destination %q: %w", dstWorldID, err)
	}

	// Reject a self-transfer: staging onto a session we also collect from would
	// double-stage and conflate the source/dest archives.
	if dstLS == srcLS {
		return TransferResult{}, fmt.Errorf("workspace: Transfer source and destination are the same world (%q)", dstWorldID)
	}

	// Stage the grafts onto the dst session (and, for move, the source deletes onto
	// the src session) lock-free, in-memory. On any failure nothing is committed:
	// the partial stages die with the in-memory sessions.
	if _, err := thebibites.TransferEntries(srcLS, dstLS, entryNames, thebibites.TransferOptions{
		Move:     opts.Move,
		RemapIDs: opts.RemapIDs,
	}); err != nil {
		return TransferResult{}, err
	}

	// Commit #1: the dst graft via the unchanged head-advancing path. It re-resolves
	// the SAME cached dst handle under w.mu, performs the single WriteArchive +
	// advancing-head revision, and re-seeds both dual-key DuckDB mirror partitions.
	dstRev, err := w.CommitWorldLoaded(ctx, dstWorldID, thebibites.RunOptions{})
	if err != nil {
		return TransferResult{}, err
	}

	result := TransferResult{DstRevision: dstRev}

	// Copy (no move): done after the dst commit.
	if !opts.Move {
		return result, nil
	}

	// Move: the dst commit succeeded (rev.ID != 0 means the graft committed). ONLY
	// NOW do we commit the staged source deletes. This dst-first ordering is the
	// whole reason a single failure leaves a recoverable duplicate, never data loss.
	if dstRev.ID == 0 {
		// Defensive: a non-empty selection that produced no dst commit means nothing
		// was actually staged to commit (should not happen here). Skip the source
		// delete so we never delete from the source without a committed dst copy.
		return result, nil
	}

	result.Moved = true

	// Commit #2: the staged source deletes, on the SAME cached source handle (its
	// SaveID == srcWorldID) under its OWN w.mu critical section (sequential, not
	// nested with commit #1).
	srcRev, err := w.CommitWorldLoaded(ctx, srcWorldID, thebibites.RunOptions{})
	if err != nil {
		// Half-applied move: the graft is committed to the dst but the source delete
		// failed, so the entity REMAINS in the source. Name the state explicitly so
		// the user can recover (re-run the delete) instead of believing the move
		// completed.
		return result, fmt.Errorf("workspace: Transfer move half-applied: graft committed to destination %q rev %d, but source %q delete commit failed — the entity REMAINS in the source; re-run the source delete manually: %w", dstWorldID, dstRev.ID, srcWorldID, err)
	}

	result.SourceCommitted = true
	result.SourceRevision = srcRev
	return result, nil
}
