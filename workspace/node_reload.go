package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/asemones/bibicontrol/duckdb"
	"github.com/asemones/bibicontrol/ipc"
	"github.com/asemones/bibicontrol/revisionstore"
	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
	"github.com/asemones/bibicontrol/simctl"
)

// Stabilize-before-parse settle parameters. The autosave file on disk may still
// be in mid-write by the game when IngestAutosave is called; we never parse a
// file the game is still writing (workspace_plan.md:530). The loop polls
// (Size, ModTime) and considers the file stable once two consecutive polls
// agree. The interval/attempts are named so tests can rely on a short settle.
const (
	// stableFilePollInterval is the delay between successive stat polls.
	stableFilePollInterval = 100 * time.Millisecond
	// stableFileMaxPolls bounds the settle loop so a never-stable (or
	// never-appearing) file fails loudly rather than hanging forever. The loop
	// is also bounded by ctx.
	stableFileMaxPolls = 50
)

// ReloadNode materializes the node's bound world head blob to the node's
// persisted drop_path crash-safely (write -> fsync -> atomic rename) and then
// sends simctl.RELOAD so the game loads the shipped save (workspace -> game).
//
// Ordering is load-bearing: the bytes are made durable and atomically swapped
// into place BEFORE the RELOAD command is sent, so the game never reads a
// half-written save and a crash mid-write leaves the prior drop file intact
// (no data loss; the head stays durable in the blobstore regardless).
//
// Lock discipline: w.Node and nodeRowByLogicalID each take w.mu themselves, and
// the IPC round-trip / file IO run WITHOUT w.mu held — w.mu is a non-reentrant
// sync.Mutex (workspace.go:45) so nesting it would self-deadlock. ReloadNode
// ships the persisted head bytes, NOT a possibly-staged working copy, so it does
// not go through Load/OpenWorld.
func (w *Workspace) ReloadNode(ctx context.Context, nodeID string) (ipc.ReloadResult, error) {
	if w == nil {
		return ipc.ReloadResult{}, fmt.Errorf("workspace: ReloadNode on nil workspace")
	}
	if nodeID == "" {
		return ipc.ReloadResult{}, fmt.Errorf("workspace: ReloadNode: nodeID is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// 1. Resolve the live runtime first: never write a drop file for a node we
	// cannot then command to reload. w.Node takes/releases w.mu for the peek.
	rt, ok := w.Node(nodeID)
	if !ok {
		return ipc.ReloadResult{}, fmt.Errorf("workspace: ReloadNode: unknown node %q", nodeID)
	}

	// 2. Resolve the persisted nodes row for the binding (world_id) and the
	// destination (drop_path). Both must be set or there is nothing to ship.
	node, err := w.nodeRowByLogicalID(ctx, nodeID)
	if err != nil {
		return ipc.ReloadResult{}, fmt.Errorf("workspace: ReloadNode %q: %w", nodeID, err)
	}
	if node.WorldID == "" {
		return ipc.ReloadResult{}, fmt.Errorf("workspace: ReloadNode: node %q has no bound world", nodeID)
	}
	if node.DropPath == "" {
		return ipc.ReloadResult{}, fmt.Errorf("workspace: ReloadNode: node %q has no drop_path", nodeID)
	}

	// 3. Resolve the bound world's head blob bytes (mirrors working_set.go:38-72).
	data, err := w.headBlobBytes(ctx, node.WorldID)
	if err != nil {
		return ipc.ReloadResult{}, fmt.Errorf("workspace: ReloadNode %q: %w", nodeID, err)
	}

	// 4. Write the drop file crash-safely: write -> fsync -> atomic rename. A
	// partial write must never be observed by the sim, and a crash mid-write
	// must not clobber the prior drop file.
	if err := writeDropFileAtomic(node.DropPath, data); err != nil {
		return ipc.ReloadResult{}, fmt.Errorf("workspace: ReloadNode %q: %w", nodeID, err)
	}

	// 5. Only after the bytes are durably in place do we tell the game to reload.
	// The IPC round-trip runs without w.mu (match D2 NodeInfo, node_control.go:50).
	res, err := simctl.New(rt).Reload(ctx)
	if err != nil {
		return ipc.ReloadResult{}, fmt.Errorf("workspace: ReloadNode %q: %w", nodeID, err)
	}
	return res, nil
}

// headBlobBytes resolves the head revision blob bytes for worldID. It takes w.mu
// for the registry/blob reads to stay consistent with Load (working_set.go), and
// does NOT load a working copy — reload ships the persisted head, not a staged
// edit. The caller must not already hold w.mu.
func (w *Workspace) headBlobBytes(ctx context.Context, worldID string) ([]byte, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	world, err := w.store().GetWorld(ctx, worldID)
	if err != nil {
		if revisionstore.IsNotFound(err) {
			return nil, fmt.Errorf("world %q not found", worldID)
		}
		return nil, fmt.Errorf("get world %q: %w", worldID, err)
	}
	if world.HeadRevisionID == nil {
		return nil, fmt.Errorf("world %q has no head to reload", worldID)
	}
	rev, err := w.store().RevisionByID(ctx, *world.HeadRevisionID)
	if err != nil {
		return nil, fmt.Errorf("get head revision for world %q: %w", worldID, err)
	}
	// Blob-present guard (forward-compat with G4 blob eviction). A well-formed
	// world's head is never evictable, but guard it anyway: this returns a plain
	// error today; G4 replaces it with the typed ErrNotRematerializable.
	// G4: replace with ErrNotRematerializable
	if !rev.BlobPresent {
		return nil, fmt.Errorf("head revision %d is mirror_only (blob evicted): cannot reload", rev.ID)
	}
	data, err := w.blobs().Get(ctx, rev.BlobRef)
	if err != nil {
		return nil, fmt.Errorf("get blob for world %q revision %d: %w", worldID, rev.ID, err)
	}
	return data, nil
}

// writeDropFileAtomic writes data to dropPath crash-safely: it materializes the
// bytes in a sibling temp file, fsyncs the file contents, closes it, and then
// atomically renames it over dropPath. The rename is the single point at which
// the new save becomes visible — a crash before the rename leaves the prior drop
// file untouched, and a crash after it leaves the complete new save in place.
func writeDropFileAtomic(dropPath string, data []byte) error {
	dir := filepath.Dir(dropPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create drop dir %q: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".bibidrop-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp drop file in %q: %w", dir, err)
	}
	tmpPath := tmp.Name()
	// On any error before the successful rename, remove the temp file so a
	// failed reload strands nothing.
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write drop file: %w", err)
	}
	// fsync the file contents so a crash after the rename cannot leave a
	// truncated/partial save visible under dropPath.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("fsync drop file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close drop file: %w", err)
	}

	// Atomic replace: the sim never observes a half-written save.
	if err := os.Rename(tmpPath, dropPath); err != nil {
		return fmt.Errorf("rename drop file into place: %w", err)
	}
	cleanup = false
	return nil
}

// IngestAutosave stabilizes and parses a game autosave file, dedups it by
// content hash against the bound world's current head (an unchanged autosave is
// a no-op skip), then stores the blob, records a new revision that threads
// parent = the world's current head and advances worlds.head_revision_id
// atomically, and re-projects both DuckDB mirror partitions under the dual-key
// scheme (game -> workspace).
//
// path == "" defaults to the node's live InfoResult.LastAutosave.Path.
//
// Return: (rev, ingested, err). ingested == false on a dedup-skip (mirroring
// CommitWorld's no-op contract, commit.go:87): no head moves, no mirror writes,
// the returned Revision is the zero value.
//
// Lock discipline: the runtime/row resolution, the INFO round-trip, the file
// stabilize/parse/read all run WITHOUT w.mu. Only the record-advancing-head +
// dual-key DuckDB import critical section holds w.mu (the single DuckDB writer),
// and the head is re-read INSIDE the lock as the parent (TOCTOU guard).
func (w *Workspace) IngestAutosave(ctx context.Context, nodeID, path string) (revisionstore.Revision, bool, error) {
	if w == nil {
		return revisionstore.Revision{}, false, fmt.Errorf("workspace: IngestAutosave on nil workspace")
	}
	if nodeID == "" {
		return revisionstore.Revision{}, false, fmt.Errorf("workspace: IngestAutosave: nodeID is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// 1. Resolve the live runtime (needed for the default-path INFO round-trip).
	rt, ok := w.Node(nodeID)
	if !ok {
		return revisionstore.Revision{}, false, fmt.Errorf("workspace: IngestAutosave: unknown node %q", nodeID)
	}

	// 2. Resolve the persisted nodes row for the binding (world_id).
	node, err := w.nodeRowByLogicalID(ctx, nodeID)
	if err != nil {
		return revisionstore.Revision{}, false, fmt.Errorf("workspace: IngestAutosave %q: %w", nodeID, err)
	}
	if node.WorldID == "" {
		return revisionstore.Revision{}, false, fmt.Errorf("workspace: IngestAutosave: node %q has no bound world", nodeID)
	}

	// 3. Default the path from the node's last autosave when not supplied. The
	// INFO round-trip runs without w.mu held.
	if path == "" {
		info, err := simctl.New(rt).Info(ctx)
		if err != nil {
			return revisionstore.Revision{}, false, fmt.Errorf("workspace: IngestAutosave %q: info: %w", nodeID, err)
		}
		if info.LastAutosave == nil || info.LastAutosave.Path == "" {
			return revisionstore.Revision{}, false, fmt.Errorf("workspace: IngestAutosave %q: node has no autosave to ingest", nodeID)
		}
		path = info.LastAutosave.Path
	}

	// 4. Stabilize the file before parsing: never parse a file the game is still
	// writing. Bounded by ctx and a max-poll cap.
	if err := waitForStableFile(ctx, path); err != nil {
		return revisionstore.Revision{}, false, fmt.Errorf("workspace: IngestAutosave %q: %w", nodeID, err)
	}

	// 5. Parse the autosave. archive.SHA256 is the whole-file content digest.
	archive, err := tb.ParseFile(path, nil)
	if err != nil {
		return revisionstore.Revision{}, false, fmt.Errorf("workspace: IngestAutosave %q: parse %q: %w", nodeID, path, err)
	}

	// 6. Dedup by content hash against the world's CURRENT head. An unchanged
	// autosave is a no-op skip (no head move, no mirror write) — mirroring
	// CommitWorld's no-op contract. (This is a fast outside-the-lock check; the
	// authoritative parent is re-read inside the lock in step 7 as the TOCTOU
	// guard.)
	world, err := w.store().GetWorld(ctx, node.WorldID)
	if err != nil {
		if revisionstore.IsNotFound(err) {
			return revisionstore.Revision{}, false, fmt.Errorf("workspace: IngestAutosave %q: world %q not found", nodeID, node.WorldID)
		}
		return revisionstore.Revision{}, false, fmt.Errorf("workspace: IngestAutosave %q: get world %q: %w", nodeID, node.WorldID, err)
	}
	if world.HeadRevisionID != nil {
		head, err := w.store().RevisionByID(ctx, *world.HeadRevisionID)
		if err != nil {
			return revisionstore.Revision{}, false, fmt.Errorf("workspace: IngestAutosave %q: get head revision: %w", nodeID, err)
		}
		if head.SHA256 == archive.SHA256 {
			return revisionstore.Revision{}, false, nil
		}
	}

	// 7. Read the file bytes for the blob. The parser is path-only, so re-read
	// the same file for the blob (matching AddWorld, world.go:34). The
	// sha256 divergence guard below catches the file changing between stabilize
	// and read.
	data, err := os.ReadFile(path)
	if err != nil {
		return revisionstore.Revision{}, false, fmt.Errorf("workspace: IngestAutosave %q: read %q: %w", nodeID, path, err)
	}

	rev, err := w.appendIngestedRevision(ctx, node.WorldID, archive, data, path)
	if err != nil {
		return revisionstore.Revision{}, false, err
	}
	return rev, true, nil
}

// appendIngestedRevision is the record-advancing-head + dual-key DuckDB import
// critical section. It holds w.mu for the whole body (the single DuckDB writer
// per workspace) so the read-current-head -> record-advancing-head ->
// dual-key-import sequence is atomic w.r.t. other mutators: no concurrent
// commit/ingest can steal the parent between the head read and the record.
//
// This is the importWorldFromArchive body (world.go:80-159) adapted for a
// non-first revision. IMPORTANT: ingest does NOT reparse. Unlike CommitWorld
// (which reparses because Session.Apply leaves the typed projections stale,
// commit.go:99-114), the archive here is fresh from ParseFile on disk, so its
// typed projections are accurate — ExtractTables is fed the parsed archive
// directly.
func (w *Workspace) appendIngestedRevision(ctx context.Context, worldID string, archive *tb.Archive, data []byte, sourcePath string) (revisionstore.Revision, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Store the blob. ref.SHA256 is the revision content hash and the
	// history-partition key; it must equal the whole-file digest the parser
	// computed. A divergence means the file changed between stabilize and read.
	ref, err := w.blobs().Put(ctx, data)
	if err != nil {
		return revisionstore.Revision{}, fmt.Errorf("workspace: IngestAutosave: store blob: %w", err)
	}
	if ref.SHA256 != archive.SHA256 {
		return revisionstore.Revision{}, fmt.Errorf(
			"workspace: IngestAutosave: blob sha256 %s != archive sha256 %s (file changed between parse and read)",
			ref.SHA256, archive.SHA256)
	}

	// Record the ingest script run so the revision's script_run_id FK is
	// satisfied (world.go:108 pattern). There is no script for an ingest, so
	// ScriptSHA256 reuses the content digest and Status is "ingested".
	now := time.Now().UTC()
	run, err := w.store().RecordScriptRun(ctx, revisionstore.ScriptRunInput{
		ScriptSHA256: ref.SHA256,
		StartedAt:    now,
		FinishedAt:   &now,
		Status:       "ingested",
		StagedOps:    0,
		DryRun:       false,
	})
	if err != nil {
		return revisionstore.Revision{}, fmt.Errorf("workspace: IngestAutosave: record ingest run: %w", err)
	}

	// Derive sim_time (nil when the save has no scene time).
	var simTime *float64
	if archive.Scene != nil && archive.Scene.HasTime {
		st := archive.Scene.SimulatedTime
		simTime = &st
	}

	// Re-read the world's CURRENT head INSIDE the lock as the parent — the TOCTOU
	// guard (commit.go:64-77). The insert-revision + SetWorldHead happen in one
	// SQLite tx inside RecordRevisionAdvancingHead, so threading the parent read
	// here keeps the head monotonic against concurrent mutators.
	world, err := w.store().GetWorld(ctx, worldID)
	if err != nil {
		return revisionstore.Revision{}, fmt.Errorf("workspace: IngestAutosave: get world %q: %w", worldID, err)
	}
	parentID := world.HeadRevisionID

	// Record the revision and advance the world head atomically. RecordRevision-
	// AdvancingHead self-refs the blob in-tx (store.go:337-384), so the recorded
	// revision is at refcount = 1 on its own — NO separate IncBlobRef (a second
	// increment would set refcount = 2 and strand the blob from G2 eviction).
	rev, err := w.store().RecordRevisionAdvancingHead(ctx, worldID, simTime, revisionstore.RevisionInput{
		ParentID:    parentID,
		WorldID:     worldID,
		SourcePath:  sourcePath,
		BlobRef:     ref,
		ScriptRunID: run.ID,
	})
	if err != nil {
		return revisionstore.Revision{}, fmt.Errorf("workspace: IngestAutosave: record revision: %w", err)
	}

	// Dual-key DuckDB import. Ingest needs no reparse: the parsed archive is
	// fresh from disk, so its typed projections are accurate — feed ExtractTables
	// the parsed archive directly (do NOT copy reparseCommitted from commit.go).
	//
	// History partition (immutable, keyed by the NEW revision sha256): the delete
	// is a no-op for a never-seen sha256, so this composes additively and never
	// disturbs any prior revision's partition (history accumulates).
	hist := tb.ExtractTables(ref.SHA256, archive)
	if err := duckdb.ReplaceExtractedSave(ctx, w.duck(), hist); err != nil {
		return revisionstore.Revision{}, fmt.Errorf("workspace: IngestAutosave: import history partition: %w", err)
	}
	// Working partition (re-seeded to the new head, keyed by the world id): the
	// delete drops the previous head's working rows and the insert seeds the new
	// head. The worldID key never touches history.
	working := tb.ExtractTables(worldID, archive)
	if err := duckdb.ReplaceExtractedSave(ctx, w.duck(), working); err != nil {
		return revisionstore.Revision{}, fmt.Errorf("workspace: IngestAutosave: re-seed working partition: %w", err)
	}

	// Drop the cached working copy so a later OpenWorld lazy-reloads from the
	// now-advanced head (mirrors commit.go:150). An already-loaded LoadedSave
	// would otherwise be stale at the prior head. The handle owns no closeable
	// resource (ls.db is the shared workspace DuckDB handle), so this leaks
	// nothing.
	delete(w.worlds, worldID)

	return rev, nil
}

// nodeRowByLogicalID resolves the persisted nodes row (world_id, drop_path, PK)
// from the logical node id. The active set (w.nodes) is keyed by logical node id
// but the binding and drop_path live only on the persisted row; there is no
// existing public getter keyed by logical id (GetNode keys on the PK). This
// reuses the resolution pattern at node.go:304-315 (setNodeStatusByLogicalID)
// and is kept self-contained in this file to avoid collision with parallel
// tickets. Do NOT hold w.mu across this call (ListNodes is consistent with the
// D2 passthrough methods; the caller resolves the row outside the lock).
func (w *Workspace) nodeRowByLogicalID(ctx context.Context, nodeID string) (revisionstore.Node, error) {
	persisted, err := w.store().ListNodes(ctx, w.id)
	if err != nil {
		return revisionstore.Node{}, fmt.Errorf("list nodes: %w", err)
	}
	for _, n := range persisted {
		if n.NodeID == nodeID {
			return n, nil
		}
	}
	return revisionstore.Node{}, fmt.Errorf("no persisted row for logical node %q", nodeID)
}

// waitForStableFile blocks until path's (Size, ModTime) is unchanged across two
// consecutive polls (the file has settled), or fails loudly on timeout, a
// vanishing/never-appearing file, or ctx cancellation. The architecture doc
// requires never parsing a file the game is still writing.
func waitForStableFile(ctx context.Context, path string) error {
	if path == "" {
		return fmt.Errorf("stabilize: path is required")
	}

	type sample struct {
		size    int64
		modTime time.Time
	}

	var prev sample
	havePrev := false
	for attempt := 0; attempt < stableFileMaxPolls; attempt++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("stabilize %q: %w", path, err)
		}

		fi, err := os.Stat(path)
		if err != nil {
			// A missing file fails loudly — the game never wrote it, or it was
			// removed under us.
			return fmt.Errorf("stabilize %q: %w", path, err)
		}
		cur := sample{size: fi.Size(), modTime: fi.ModTime()}
		if havePrev && cur == prev {
			return nil
		}
		prev = cur
		havePrev = true

		// Sleep before the next poll, but honor ctx cancellation.
		select {
		case <-ctx.Done():
			return fmt.Errorf("stabilize %q: %w", path, ctx.Err())
		case <-time.After(stableFilePollInterval):
		}
	}
	return fmt.Errorf("stabilize %q: file did not settle after %d polls", path, stableFileMaxPolls)
}
