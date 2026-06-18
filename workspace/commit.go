package workspace

import (
	"context"
	"fmt"
	"os"

	"github.com/asemones/bibicontrol/blobstore"
	"github.com/asemones/bibicontrol/duckdb"
	"github.com/asemones/bibicontrol/revisionstore"
	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
	"github.com/asemones/bibicontrol/script/thebibites"
)

// CommitWorld runs program against the world's already-loaded working copy
// (lazy-loading it via OpenWorld if absent), and — when the run staged a
// mutation, opted into commit, and is not dry-run — records a new revision that
// threads parent = the world's current head and advances worlds.head_revision_id
// atomically, then re-projects both DuckDB mirror partitions under the dual-key
// scheme:
//
//   - history partition (retained), keyed by the new revision sha256 — additive,
//     so prior revisions' history partitions survive (history accumulates), and
//   - working partition (re-seeded to the new head), keyed by the world id — so
//     the open world's analytics reflect the head.
//
// The whole commit body holds w.mu (the single DuckDB writer per workspace) so
// the read-current-head -> record-advancing-head -> dual-key-import sequence is
// atomic w.r.t. other mutators on this workspace: no concurrent commit can steal
// the parent between the head read and the record. OpenWorld is called BEFORE
// taking w.mu (it acquires its own lock; w.mu is non-reentrant — never nest it).
//
// A no-op run (pure analysis / dry-run / autocommit(False) / staged nothing)
// returns the zero Revision and a nil error: no head moved, no re-import.
func (w *Workspace) CommitWorld(ctx context.Context, worldID string, program []byte, opts thebibites.RunOptions) (revisionstore.Revision, error) {
	if w == nil {
		return revisionstore.Revision{}, fmt.Errorf("workspace: CommitWorld on nil workspace")
	}
	if worldID == "" {
		return revisionstore.Revision{}, fmt.Errorf("workspace: worldID is required")
	}
	if len(program) == 0 {
		return revisionstore.Revision{}, fmt.Errorf("workspace: program is empty")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// 1. Resolve the loaded working copy WITHOUT holding w.mu yet — OpenWorld
	// lazy-Loads under its own lock if absent; nesting w.mu would self-deadlock
	// (non-reentrant; working_set.go:162). The *LoadedSave pointer is stable in
	// w.worlds, and the commit serializes on w.mu next.
	ls, err := w.OpenWorld(ctx, worldID)
	if err != nil {
		return revisionstore.Revision{}, err
	}

	// 2. Take w.mu for the whole commit body: it is the single-writer DuckDB path
	// and the read-head -> record-advancing-head sequence must be atomic w.r.t.
	// other mutators on this workspace.
	w.mu.Lock()
	defer w.mu.Unlock()

	// 3. Read the world's CURRENT head as the parent, INSIDE the lock, immediately
	// before recording — the TOCTOU guard. A loadable world always has a head from
	// C1, so a nil head here is a defensive guard, not a normal path.
	world, err := w.store().GetWorld(ctx, worldID)
	if err != nil {
		if revisionstore.IsNotFound(err) {
			return revisionstore.Revision{}, fmt.Errorf("workspace: world %q not found", worldID)
		}
		return revisionstore.Revision{}, fmt.Errorf("workspace: get world %q: %w", worldID, err)
	}
	if world.HeadRevisionID == nil {
		return revisionstore.Revision{}, fmt.Errorf("workspace: world %q has no head to commit onto", worldID)
	}
	parentID := world.HeadRevisionID

	// 4. Run + commit via the script core (run -> blob -> advancing-head revision).
	// It already carries truthful run/commit status in any returned error.
	wc, err := thebibites.RunAndCommitWorld(ctx, ls, program, worldID, parentID, w.blobs(), w.store(), opts)
	if err != nil {
		return revisionstore.Revision{}, err
	}

	// 5-7. Apply the dual-key DuckDB re-import (shared with CommitWorldLoaded).
	return w.applyWorldCommit(ctx, worldID, wc)
}

// CommitWorldLoaded commits the mutations ALREADY staged on the world's cached
// working copy (ls.session) and advances the world head — the object-based
// counterpart of CommitWorld with NO script.Run. The automation world.open() ->
// s.commit() surface uses it: mutations are staged by direct method calls on the
// Save value (in the same automation thread) onto the cached ls, so re-running a
// program (CommitWorld) would stage onto a throwaway fresh load and lose them.
//
// It shares CommitWorld's exact lock discipline and dual-key re-import:
//   - resolve the cached working copy via OpenWorld BEFORE taking w.mu (OpenWorld
//     acquires its own lock; w.mu is non-reentrant). On the fast path OpenWorld
//     returns the SAME cached handle world.open() staged onto, so s.commit()
//     commits that staged session. (If an Unload/Load ran between open and commit,
//     the staged session is lost — within one hermetic automation cycle that does
//     not happen.)
//   - take w.mu for the whole body so read-current-head -> record-advancing-head
//     -> dual-key import is atomic w.r.t. other mutators (the TOCTOU guard).
//   - thebibites.CommitLoadedWorld performs the single WriteArchive + advancing-
//     head revision (no program), and applyWorldCommit runs the IDENTICAL
//     reparse + dual-key ReplaceExtractedSave.
//
// A no-op (nothing staged / dry-run / autocommit(False)) returns the zero
// Revision and a nil error: no head moved, no re-import — same contract as
// CommitWorld.
func (w *Workspace) CommitWorldLoaded(ctx context.Context, worldID string, opts thebibites.RunOptions) (revisionstore.Revision, error) {
	if w == nil {
		return revisionstore.Revision{}, fmt.Errorf("workspace: CommitWorldLoaded on nil workspace")
	}
	if worldID == "" {
		return revisionstore.Revision{}, fmt.Errorf("workspace: worldID is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// 1. Resolve the cached working copy WITHOUT holding w.mu yet (OpenWorld takes
	// its own lock; nesting w.mu self-deadlocks). On the fast path this is the SAME
	// *LoadedSave world.open() returned and staged the mutations onto.
	ls, err := w.OpenWorld(ctx, worldID)
	if err != nil {
		return revisionstore.Revision{}, err
	}

	// 2. Take w.mu for the whole commit body (single DuckDB writer; atomic
	// read-head -> record-advancing-head sequence).
	w.mu.Lock()
	defer w.mu.Unlock()

	// 3. Read the world's CURRENT head as the parent, INSIDE the lock (TOCTOU
	// guard). A loadable world always has a head from C1.
	world, err := w.store().GetWorld(ctx, worldID)
	if err != nil {
		if revisionstore.IsNotFound(err) {
			return revisionstore.Revision{}, fmt.Errorf("workspace: world %q not found", worldID)
		}
		return revisionstore.Revision{}, fmt.Errorf("workspace: get world %q: %w", worldID, err)
	}
	if world.HeadRevisionID == nil {
		return revisionstore.Revision{}, fmt.Errorf("workspace: world %q has no head to commit onto", worldID)
	}
	parentID := world.HeadRevisionID

	// 4. Commit the already-staged session via the shared script core (no program
	// run -> blob -> advancing-head revision). It carries truthful commit status in
	// any returned error.
	wc, err := thebibites.CommitLoadedWorld(ctx, ls, worldID, parentID, w.blobs(), w.store(), opts)
	if err != nil {
		return revisionstore.Revision{}, err
	}

	// 5-7. Apply the dual-key DuckDB re-import (shared with CommitWorld).
	return w.applyWorldCommit(ctx, worldID, wc)
}

// applyWorldCommit is the shared post-commit body for CommitWorld and
// CommitWorldLoaded: it reparses the committed blob once and re-imports both
// DuckDB mirror partitions under the dual-key scheme, then drops the consumed
// working copy from the working set. It MUST be called holding w.mu (the single
// DuckDB writer) — both callers take w.mu for the whole commit body before
// invoking it. Factoring this out keeps the dual-key re-import (and the subtle
// reparse-for-fresh-projection invariant) in ONE place.
func (w *Workspace) applyWorldCommit(ctx context.Context, worldID string, wc thebibites.WorldCommit) (revisionstore.Revision, error) {
	// No-op run: nothing committed, head unchanged, mirror untouched.
	if !wc.Committed {
		return revisionstore.Revision{}, nil
	}

	// Load-bearing dual-key invariant: the working partition is keyed by the world
	// id, never by the revision sha256. ls.saveID is set to worldID by LoadInto;
	// fail loudly if they ever diverge.
	if wc.SaveID != worldID {
		return revisionstore.Revision{}, fmt.Errorf(
			"workspace: working save_id %q != world id %q (dual-key desync)", wc.SaveID, worldID)
	}

	// Project the committed save for the dual-key DuckDB re-import.
	//
	// wc.Applied is the post-Apply archive: Session.Apply rewrites the mutated
	// entries' JSON/Raw bytes but does NOT re-derive the typed parser projections
	// (archive.Bibites/etc — the slices ExtractTables reads). Those stay at their
	// pre-mutation values until a reparse (session.go:21-23). So ExtractTables on
	// wc.Applied would mirror STALE rows — the subtle bug a value assertion catches.
	// Reparse the committed blob bytes exactly once here to get fresh, mutation-
	// accurate projections for both partitions.
	//
	// This reparse is the workspace mirror's, NOT the LoadedSave commit path's: it
	// leaves ls.reparseCount == 0 (the churn DoD counter, asserted at the script
	// level) untouched, and the blob is produced by a single WriteArchive. The DoD
	// "one DuckDB import per commit" still holds — one fresh projection feeds both
	// the additive history import and the working re-seed.
	projected, err := w.reparseCommitted(ctx, wc.Revision.BlobRef)
	if err != nil {
		return revisionstore.Revision{}, err
	}

	// The fresh projection feeds the DuckDB mirror ONCE: the working partition is
	// imported via the Appender, then the history partition is derived inside
	// DuckDB by a set-based save_id-rewriting copy of those working rows (no
	// second extract+append). Working MUST run first — it is the copy source.
	//
	// Working partition (re-seeded to head), keyed by the world id: the delete
	// drops the previous head's working rows and the insert seeds the new head.
	// This is the only place the working partition is replaced; the worldID key
	// never touches history. ReplaceExtractedSave (not ImportExtractedSave) so
	// migrations are not re-run; they ran on Open.
	working := tb.ExtractTables(worldID, projected)
	if err := duckdb.ReplaceExtractedSave(ctx, w.duck(), working); err != nil {
		// Known, non-corrupting drift: the working partition lags at the prior head
		// until a later successful commit re-seeds it (C2's Load does not re-import
		// the working partition). The head + blob remain durable — never data loss.
		return revisionstore.Revision{}, fmt.Errorf("workspace: re-seed working partition: %w", err)
	}

	// History partition (retained), keyed by the NEW revision sha256: derive it
	// from the just-seeded working rows via CopySavePartition, rewriting only
	// save_id. The delete is a no-op for a never-seen sha256, so the insert adds a
	// new history partition WITHOUT disturbing any prior revision's partition
	// (history accumulates — the immutability invariant), and the derived rows are
	// byte-identical to the working rows modulo save_id.
	if err := duckdb.CopySavePartition(ctx, w.duck(), worldID, wc.Revision.SHA256); err != nil {
		// The head + blob are durable in SQLite/blobstore; the working partition
		// still reflects the new head. Surface the error — the mirror is a
		// rebuildable projection, never rolled back against the committed head.
		return revisionstore.Revision{}, fmt.Errorf("workspace: derive history partition: %w", err)
	}

	// The cached working copy is consumed by this commit: prepareCommit Applied its
	// session (state StateApplied), so it can stage no further mutations ("cannot
	// stage after apply"). Drop it from the working set under w.mu so the next
	// OpenWorld/CommitWorld lazy-reloads a fresh, stageable copy from the now-current
	// head. The handle owns no closeable resource (ls.db is the shared workspace
	// DuckDB handle LoadInto never closes), so dropping it leaks nothing.
	delete(w.worlds, worldID)

	return wc.Revision, nil
}

// reparseCommitted fetches the just-committed blob bytes from the content-
// addressed store and parses them once into a fresh archive whose typed parser
// projections reflect the staged mutations. This is required because the
// in-memory post-Apply archive has stale projections (Apply rewrites entry
// bytes but not the derived row slices ExtractTables reads). ParseFile is
// path-only, so the bytes are staged to a temp file the parser reads; the parsed
// archive lives in memory afterward, so the temp file is removed on return.
//
// The blob's bytes were verified to round-trip to ref.SHA256 at write time (and,
// when opts.Verify is set, the commit path already reparse-asserted the hash), so
// this reparse is the trusted projection source for the dual-key mirror import.
func (w *Workspace) reparseCommitted(ctx context.Context, ref blobstore.Ref) (*tb.Archive, error) {
	data, err := w.blobs().Get(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("workspace: get committed blob %s: %w", ref.SHA256, err)
	}

	tmp, err := os.CreateTemp("", "bibicommit-*.zip")
	if err != nil {
		return nil, fmt.Errorf("workspace: create temp for committed blob: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("workspace: write temp for committed blob: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("workspace: close temp for committed blob: %w", err)
	}

	archive, err := tb.ParseFile(tmpPath, nil)
	if err != nil {
		return nil, fmt.Errorf("workspace: reparse committed blob %s for mirror projection: %w", ref.SHA256, err)
	}
	return archive, nil
}
