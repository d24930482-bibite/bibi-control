package workspace

import (
	"context"
	"fmt"

	"github.com/asemones/bibicontrol/duckdb"
	"github.com/asemones/bibicontrol/revisionstore"
	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

// ReprojectWorking re-seeds the world's WORKING partition (save_id = worldID) in
// the DuckDB mirror from the current head revision's blob. It is the on-demand
// repair lever for the one known, non-corrupting drift the commit/ingest path
// documents: a successful record-advancing-head followed by a failed
// ReplaceExtractedSave (commit.go:215-220, node_reload.go:375-377) leaves the
// working partition lagging the head with no rebuild path until the next
// successful commit. The head + blob remain durable; only the rebuildable mirror
// projection is stale. ReprojectWorking rebuilds it.
//
// WORKING-PARTITION ONLY. It deliberately does NOT touch the history partition
// (no CopySavePartition): the head revision's history partition (keyed by the
// head sha256) was written when the head was first recorded and is immutable.
// Reprojecting history would, at best, re-derive byte-identical rows for the
// already-seen head sha and, at worst, mutate frozen history — so it is never
// run here. This is the load-bearing difference from the commit/ingest path.
//
// Lock discipline: ReprojectWorking holds w.mu for the whole body (the single
// DuckDB writer per workspace), the same whole-body-lock shape Load /
// EvictWorldHistory use. reparseCommitted and the duckdb calls are lock-agnostic
// and never re-take w.mu (it is non-reentrant). The cached w.worlds[worldID]
// handle is dropped at the end so the next OpenWorld lazy-reloads from the
// now-consistent working partition.
func (w *Workspace) ReprojectWorking(ctx context.Context, worldID string) error {
	if w == nil {
		return fmt.Errorf("workspace: ReprojectWorking on nil workspace")
	}
	if worldID == "" {
		return fmt.Errorf("workspace: worldID is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// 1. Resolve the world row and its head revision id — the same head resolution
	// Load does (working_set.go:38-70), minus the temp-file materialize that Load
	// needs for LoadInto.
	world, err := w.store().GetWorld(ctx, worldID)
	if err != nil {
		if revisionstore.IsNotFound(err) {
			return fmt.Errorf("workspace: world %q not found", worldID)
		}
		return fmt.Errorf("workspace: get world %q: %w", worldID, err)
	}

	// 2. Head guard: a head-less world (e.g. a partial import) has no head blob to
	// reproject from — fail loudly rather than dereference a nil head.
	if world.HeadRevisionID == nil {
		return fmt.Errorf("workspace: world %q has no head revision to reproject from", worldID)
	}

	// 3. Fetch the head revision metadata.
	rev, err := w.store().RevisionByID(ctx, *world.HeadRevisionID)
	if err != nil {
		return fmt.Errorf("workspace: get head revision for world %q: %w", worldID, err)
	}

	// 4. Blob-present guard: a head whose bytes are gone is unrecoverable (there is
	// no mirror→blob path). Fail loud with the typed sentinel rather than an opaque
	// downstream blobstore miss.
	if !rev.BlobPresent {
		return notRematerializable(worldID, rev.ID, "reproject")
	}

	// 5. Reparse the head blob into a fresh archive with accurate typed
	// projections. Reuse reparseCommitted (commit.go:257-282) — it does the
	// temp-file + ParseFile and is lock-agnostic.
	archive, err := w.reparseCommitted(ctx, rev.BlobRef)
	if err != nil {
		return err
	}

	// 6. Re-seed ONLY the working partition (save_id = worldID). This is identical
	// to the commit/ingest working re-seed (commit.go:214-215, node_reload.go:374-375)
	// — ReplaceExtractedSave (not ImportExtractedSave) so the idempotent migrations
	// are not re-run; they ran on Open. NO CopySavePartition: history is untouched.
	working := tb.ExtractTables(worldID, archive)
	if err := duckdb.ReplaceExtractedSave(ctx, w.duck(), working); err != nil {
		return fmt.Errorf("workspace: reproject working partition for world %q: %w", worldID, err)
	}

	// 7. Drop the cached working copy so the next OpenWorld lazy-reloads from the
	// now-consistent working partition (mirrors applyWorldCommit:241). The handle
	// owns no closeable resource (ls.db is the shared workspace DuckDB handle
	// LoadInto never closes), so dropping it leaks nothing.
	delete(w.worlds, worldID)

	return nil
}
