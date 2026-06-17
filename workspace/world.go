package workspace

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/asemones/bibicontrol/duckdb"
	"github.com/asemones/bibicontrol/revisionstore"
	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

// AddWorld imports the save file at srcPath as a brand-new world: it parses the
// save once, creates the worlds registry row, stores the save bytes in the
// shared blobstore, records a first revision that advances the world head
// atomically (parent=nil), and projects the save's normalized mirror into the
// per-workspace DuckDB under both the working-partition key (the world id) and
// the immutable history-partition key (the revision sha256). It returns the
// created World with its head set.
func (w *Workspace) AddWorld(ctx context.Context, srcPath, name string) (revisionstore.World, error) {
	if srcPath == "" {
		return revisionstore.World{}, fmt.Errorf("workspace: src path is required")
	}

	archive, err := tb.ParseFile(srcPath, nil)
	if err != nil {
		return revisionstore.World{}, fmt.Errorf("workspace: parse save %q: %w", srcPath, err)
	}

	// The blob is the exact on-disk file the archive hash was computed over;
	// archive.SHA256 is the whole-file digest, so the blob ref sha256 must equal
	// it. importWorldFromArchive re-asserts this and fails loudly if it diverges.
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return revisionstore.World{}, fmt.Errorf("workspace: read save %q: %w", srcPath, err)
	}

	return w.importWorldFromArchive(ctx, archive, data, srcPath, name)
}

// AddWorldBytes imports an in-memory save as a brand-new world. ParseFile is
// path-only (there is no in-memory archive parser), so the bytes are staged to
// a temp file for the single parse and then passed straight through as the blob
// bytes (the temp file is not re-read).
func (w *Workspace) AddWorldBytes(ctx context.Context, data []byte, name string) (revisionstore.World, error) {
	if len(data) == 0 {
		return revisionstore.World{}, fmt.Errorf("workspace: save data is empty")
	}

	tmp, err := os.CreateTemp("", "bibiworld-*.zip")
	if err != nil {
		return revisionstore.World{}, fmt.Errorf("workspace: create temp save: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return revisionstore.World{}, fmt.Errorf("workspace: write temp save: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return revisionstore.World{}, fmt.Errorf("workspace: close temp save: %w", err)
	}

	archive, err := tb.ParseFile(tmpPath, nil)
	if err != nil {
		return revisionstore.World{}, fmt.Errorf("workspace: parse save bytes: %w", err)
	}

	return w.importWorldFromArchive(ctx, archive, data, tmpPath, name)
}

// importWorldFromArchive is the shared AddWorld/AddWorldBytes core: parse is
// already done. It holds the workspace mutex for the whole body because it is
// the single DuckDB writer per workspace, and records registry/blob state
// before the mirror import so a failed DuckDB import leaves the registry as the
// source of truth (an orphan blob or head-less world row is harmless and
// reclaimable). The two public wrappers do not lock — taking the lock here only
// keeps the non-reentrant mutex from self-deadlocking.
func (w *Workspace) importWorldFromArchive(ctx context.Context, archive *tb.Archive, data []byte, sourcePath, name string) (revisionstore.World, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// 1. Create the world row. world.ID is the freshly allocated UUID — the
	// stable working-partition key. Head/SimTime are NULL until step 5.
	world, err := w.store().CreateWorld(ctx, revisionstore.WorldInput{WorkspaceID: w.ID(), Name: name})
	if err != nil {
		return revisionstore.World{}, fmt.Errorf("workspace: create world: %w", err)
	}

	// 2. Store the blob. ref.SHA256 is the revision content hash and the
	// history-partition key. It must equal the whole-file digest the parser
	// computed; a divergence means the file changed under us.
	ref, err := w.blobs().Put(ctx, data)
	if err != nil {
		return revisionstore.World{}, fmt.Errorf("workspace: store blob: %w", err)
	}
	if ref.SHA256 != archive.SHA256 {
		return revisionstore.World{}, fmt.Errorf(
			"workspace: blob sha256 %s != archive sha256 %s (file changed between parse and read)",
			ref.SHA256, archive.SHA256)
	}

	// 3. Record the import script run so the revision's script_run_id FK is
	// satisfied. There is no script for an import, so ScriptSHA256 reuses the
	// content digest (a valid 64-hex digest) and Status is "imported".
	now := time.Now().UTC()
	run, err := w.store().RecordScriptRun(ctx, revisionstore.ScriptRunInput{
		ScriptSHA256: ref.SHA256,
		StartedAt:    now,
		FinishedAt:   &now,
		Status:       "imported",
		StagedOps:    0,
		DryRun:       false,
	})
	if err != nil {
		return revisionstore.World{}, fmt.Errorf("workspace: record import run: %w", err)
	}

	// 4. Derive sim_time (nil when the save has no scene time).
	var simTime *float64
	if archive.Scene != nil && archive.Scene.HasTime {
		st := archive.Scene.SimulatedTime
		simTime = &st
	}

	// 5. Record the first revision and advance the world head atomically.
	// ParentID is nil for the very first revision. G1's insertRevisionTx already
	// establishes the blob self-ref inside the same SQLite tx, so the recorded
	// revision is at refcount = 1 on its own — no separate IncBlobRef here.
	if _, err := w.store().RecordRevisionAdvancingHead(ctx, world.ID, simTime, revisionstore.RevisionInput{
		ParentID:    nil,
		WorldID:     world.ID,
		SourcePath:  sourcePath,
		BlobRef:     ref,
		ScriptRunID: run.ID,
	}); err != nil {
		return revisionstore.World{}, fmt.Errorf("workspace: record first revision: %w", err)
	}

	// 6. Project the mirror under both keys into the shared per-workspace DuckDB.
	// ExtractTables is a pure transform; extract once per key.
	//
	// Working partition (mutable, keyed by world id): ImportExtractedSave runs
	// the idempotent migrations then a delete-by-save_id + insert (the delete is
	// a no-op on a fresh world).
	working := tb.ExtractTables(world.ID, archive)
	if err := duckdb.ImportExtractedSave(ctx, w.duck(), working); err != nil {
		return revisionstore.World{}, fmt.Errorf("workspace: import working partition: %w", err)
	}
	// History partition (immutable, keyed by revision sha256): use
	// ReplaceExtractedSave directly so migrations are not re-run; the delete is a
	// no-op for a never-seen sha256, so this composes additively and never
	// disturbs another world's or revision's partition.
	hist := tb.ExtractTables(ref.SHA256, archive)
	if err := duckdb.ReplaceExtractedSave(ctx, w.duck(), hist); err != nil {
		return revisionstore.World{}, fmt.Errorf("workspace: import history partition: %w", err)
	}

	// 7. Return the world with its head set (re-read so the advanced
	// head_revision_id and sim_time are carried back to the caller).
	out, err := w.store().GetWorld(ctx, world.ID)
	if err != nil {
		return revisionstore.World{}, fmt.Errorf("workspace: reload world: %w", err)
	}
	return out, nil
}
