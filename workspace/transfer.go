package workspace

import (
	"context"
	"fmt"

	"github.com/asemones/bibicontrol/revisionstore"
	"github.com/asemones/bibicontrol/script/thebibites"
)

// Transfer grafts the selected source entries (a where-collection's resolved set
// or a single entity) into the destination world and commits the result as a new
// dst revision — the Go method backing the workspace.transfer(...) automation
// builtin. It reuses the merged F1/F3 transfer engine (via thebibites.
// TransferEntries) for the graft and the unchanged CommitWorldLoaded path for the
// head-advancing commit; it writes NO new commit logic.
//
// Selection is the object DSL: srcLS is the SOURCE world's open working copy and
// entryNames are the entry_names that copy already resolved (scoped to the source
// world by construction). The user never writes SQL/JOINs.
//
// Lock discipline (mirrors CommitWorld/CommitWorldLoaded, commit.go:49-62):
// OpenWorld takes the non-reentrant w.mu itself, so Transfer must resolve the dst
// working copy via OpenWorld BEFORE any further lock and must NOT nest w.mu.
// TransferEntries takes NO lock — it only mutates the in-memory dst session, which
// is the SAME cached *LoadedSave handle CommitWorldLoaded re-resolves and commits
// within one hermetic automation cycle (no concurrent Unload/Load — the same
// assumption E2's s.commit relies on, commit.go:99-103). CommitWorldLoaded is the
// only DuckDB/SQLite writer here and owns w.mu for its whole body.
//
// All-or-nothing at the commit boundary: if any graft fails, TransferEntries
// returns an error and Transfer returns WITHOUT committing, so the partial stages
// die with the in-memory dst session and the dst head does not advance. An empty
// selection is a clean no-op: it returns the zero Revision (committed=false) and
// does NOT advance the head.
func (w *Workspace) Transfer(ctx context.Context, srcLS *thebibites.LoadedSave, entryNames []string, dstWorldID string) (revisionstore.Revision, error) {
	if w == nil {
		return revisionstore.Revision{}, fmt.Errorf("workspace: Transfer on nil workspace")
	}
	if srcLS == nil {
		return revisionstore.Revision{}, fmt.Errorf("workspace: Transfer source is nil")
	}
	if dstWorldID == "" {
		return revisionstore.Revision{}, fmt.Errorf("workspace: Transfer destination worldID is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// Empty selection: a clean no-op. Nothing is staged, so committing would be a
	// no-op anyway; return the zero Revision without touching the dst head.
	if len(entryNames) == 0 {
		return revisionstore.Revision{}, nil
	}

	// Resolve the dst working copy WITHOUT holding w.mu (OpenWorld takes its own
	// lock; w.mu is non-reentrant). This is the SAME cached handle CommitWorldLoaded
	// re-resolves and commits, so the grafts staged below are exactly what commits.
	dstLS, err := w.OpenWorld(ctx, dstWorldID)
	if err != nil {
		return revisionstore.Revision{}, fmt.Errorf("workspace: Transfer open destination %q: %w", dstWorldID, err)
	}

	// Reject a self-transfer: staging onto a session we also collect from would
	// double-stage and conflate the source/dest archives.
	if dstLS == srcLS {
		return revisionstore.Revision{}, fmt.Errorf("workspace: Transfer source and destination are the same world (%q)", dstWorldID)
	}

	// Stage the grafts onto the dst session (lock-free, in-memory). On any failure
	// nothing is committed: the partial stages die with the in-memory session.
	if _, err := thebibites.TransferEntries(srcLS, dstLS, entryNames); err != nil {
		return revisionstore.Revision{}, err
	}

	// Commit the staged grafts via the unchanged head-advancing path: it re-resolves
	// the SAME cached dst handle under w.mu, performs the single WriteArchive +
	// advancing-head revision, and re-seeds both dual-key DuckDB mirror partitions.
	return w.CommitWorldLoaded(ctx, dstWorldID, thebibites.RunOptions{})
}
