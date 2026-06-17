package revisionstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asemones/bibicontrol/blobstore"
	"github.com/google/uuid"
)

func TestStoreRecordsScriptRunAndRevision(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	store, err := Open(filepath.Join(dir, "metadata.sqlite"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	blobs, err := blobstore.NewFSStore(filepath.Join(dir, "blobs"), blobstore.WithInlineThreshold(0))
	if err != nil {
		t.Fatalf("NewFSStore() error = %v", err)
	}
	defer blobs.Close()

	startedAt := time.Date(2026, 6, 15, 11, 30, 0, 123456789, time.FixedZone("test", -6*60*60))
	finishedAt := startedAt.Add(2 * time.Second)
	run, err := store.RecordScriptRun(ctx, ScriptRunInput{
		ScriptSHA256: strings.Repeat("a", blobstore.SHA256HexLength),
		StartedAt:    startedAt,
		FinishedAt:   &finishedAt,
		Status:       "succeeded",
		Output:       "changed 2 saves\n",
		StagedOps:    2,
		DryRun:       true,
	})
	if err != nil {
		t.Fatalf("RecordScriptRun() error = %v", err)
	}
	if run.ID == 0 {
		t.Fatalf("script run id = 0, want assigned id")
	}
	if run.FinishedAt == nil || !sameInstant(*run.FinishedAt, finishedAt) {
		t.Fatalf("finished_at = %v, want %v", run.FinishedAt, finishedAt)
	}
	if !sameInstant(run.StartedAt, startedAt) || run.Status != "succeeded" || run.Output != "changed 2 saves\n" || run.StagedOps != 2 || !run.DryRun {
		t.Fatalf("script run = %#v", run)
	}

	parentRef, err := blobs.Put(ctx, []byte("parent save bytes"))
	if err != nil {
		t.Fatalf("Put(parent) error = %v", err)
	}
	parentCreatedAt := startedAt.Add(time.Second)
	parent, err := store.RecordRevision(ctx, RevisionInput{
		SourcePath:  "/saves/source.zip",
		BlobRef:     parentRef,
		ScriptRunID: run.ID,
		CreatedAt:   parentCreatedAt,
	})
	if err != nil {
		t.Fatalf("RecordRevision(parent) error = %v", err)
	}
	if parent.ID == 0 {
		t.Fatalf("parent revision id = 0, want assigned id")
	}

	childRef, err := blobs.Put(ctx, []byte("child save bytes"))
	if err != nil {
		t.Fatalf("Put(child) error = %v", err)
	}
	childCreatedAt := parentCreatedAt.Add(time.Second)
	child, err := store.RecordRevision(ctx, RevisionInput{
		ParentID:    &parent.ID,
		SourcePath:  "/saves/output.zip",
		BlobRef:     childRef,
		ScriptRunID: run.ID,
		CreatedAt:   childCreatedAt,
	})
	if err != nil {
		t.Fatalf("RecordRevision(child) error = %v", err)
	}

	gotChild, err := store.RevisionByID(ctx, child.ID)
	if err != nil {
		t.Fatalf("RevisionByID() error = %v", err)
	}
	if gotChild.ParentID == nil || *gotChild.ParentID != parent.ID {
		t.Fatalf("child parent_id = %v, want %d", gotChild.ParentID, parent.ID)
	}
	if gotChild.SourcePath != "/saves/output.zip" || gotChild.ScriptRunID != run.ID || !sameInstant(gotChild.CreatedAt, childCreatedAt) {
		t.Fatalf("child revision = %#v", gotChild)
	}
	assertRefEqual(t, gotChild.BlobRef, childRef)
	if gotChild.BlobRef.IsInline() {
		t.Fatalf("child BlobRef is inline, want non-inline")
	}

	bySHA, err := store.RevisionsBySHA256(ctx, childRef.SHA256)
	if err != nil {
		t.Fatalf("RevisionsBySHA256() error = %v", err)
	}
	if len(bySHA) != 1 || bySHA[0].ID != child.ID {
		t.Fatalf("RevisionsBySHA256() = %#v, want child revision", bySHA)
	}

	gotRun, err := store.ScriptRunByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("ScriptRunByID() error = %v", err)
	}
	if gotRun.ID != run.ID || gotRun.ScriptSHA256 != run.ScriptSHA256 || !gotRun.DryRun {
		t.Fatalf("ScriptRunByID() = %#v, want %#v", gotRun, run)
	}

	assertRawInlineBlob(t, ctx, store, child.ID, nil)
}

func TestStoreReloadsInlineRevisionBlob(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "metadata.sqlite")

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	blobs, err := blobstore.NewFSStore(filepath.Join(dir, "blobs"), blobstore.WithInlineThreshold(1024))
	if err != nil {
		t.Fatalf("NewFSStore() error = %v", err)
	}
	defer blobs.Close()

	run, err := store.RecordScriptRun(ctx, ScriptRunInput{
		ScriptSHA256: strings.Repeat("b", blobstore.SHA256HexLength),
		Status:       "succeeded",
	})
	if err != nil {
		t.Fatalf("RecordScriptRun() error = %v", err)
	}
	blob := []byte("tiny save")
	ref, err := blobs.Put(ctx, blob)
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if !ref.IsInline() {
		t.Fatalf("Put() returned non-inline ref for small blob")
	}
	revision, err := store.RecordRevision(ctx, RevisionInput{
		BlobRef:     ref,
		ScriptRunID: run.ID,
	})
	if err != nil {
		t.Fatalf("RecordRevision() error = %v", err)
	}
	assertRawInlineBlob(t, ctx, store, revision.ID, blob)
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := Open(dbPath)
	if err != nil {
		t.Fatalf("reopen Open() error = %v", err)
	}
	defer reopened.Close()

	got, err := reopened.RevisionByID(ctx, revision.ID)
	if err != nil {
		t.Fatalf("RevisionByID() after reopen error = %v", err)
	}
	assertRefEqual(t, got.BlobRef, ref)
	if !got.BlobRef.IsInline() {
		t.Fatalf("reloaded BlobRef is non-inline, want inline")
	}
	if !bytes.Equal(got.BlobRef.Inline, blob) {
		t.Fatalf("reloaded inline blob = %q, want %q", got.BlobRef.Inline, blob)
	}
}

func TestStoreLookupMissingRevision(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	_, err = store.RevisionByID(context.Background(), 999)
	if !IsNotFound(err) {
		t.Fatalf("RevisionByID() error = %v, want not found", err)
	}
}

func TestStoreRejectsRevisionWithMissingScriptRun(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	store, err := Open(filepath.Join(dir, "metadata.sqlite"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	blobs, err := blobstore.NewFSStore(filepath.Join(dir, "blobs"), blobstore.WithInlineThreshold(0))
	if err != nil {
		t.Fatalf("NewFSStore() error = %v", err)
	}
	defer blobs.Close()

	ref, err := blobs.Put(ctx, []byte("orphan save bytes"))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	_, err = store.RecordRevision(ctx, RevisionInput{
		SourcePath:  "/saves/orphan.zip",
		BlobRef:     ref,
		ScriptRunID: 999, // no such script run
	})
	if err == nil {
		t.Fatalf("RecordRevision() with missing script_run_id succeeded, want foreign-key rejection")
	}
}

func TestSchemaAppliesAndIsIdempotent(t *testing.T) {
	ctx := context.Background()

	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	// Re-applying migrations on the same DB must be a no-op. This is the
	// idempotency contract the inline CREATE ... IF NOT EXISTS must satisfy.
	if err := ApplyMigrations(ctx, store.db); err != nil {
		t.Fatalf("ApplyMigrations() second call error = %v", err)
	}

	// New registry tables must exist.
	wantTables := map[string]bool{"workspaces": false, "worlds": false, "nodes": false}
	tableRows, err := store.db.QueryContext(ctx, `
		SELECT name FROM sqlite_master
		WHERE type = 'table' AND name IN ('workspaces', 'worlds', 'nodes')
	`)
	if err != nil {
		t.Fatalf("query tables: %v", err)
	}
	defer tableRows.Close()
	for tableRows.Next() {
		var name string
		if err := tableRows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		wantTables[name] = true
	}
	if err := tableRows.Err(); err != nil {
		t.Fatalf("iterate tables: %v", err)
	}
	for name, found := range wantTables {
		if !found {
			t.Fatalf("table %q missing from schema", name)
		}
	}

	// New save_revisions columns must exist with the expected defaults.
	type colInfo struct {
		dflt sql.NullString
	}
	cols := map[string]colInfo{}
	colRows, err := store.db.QueryContext(ctx, `PRAGMA table_info(save_revisions)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info(save_revisions): %v", err)
	}
	defer colRows.Close()
	for colRows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notNull int
			dflt    sql.NullString
			pk      int
		)
		if err := colRows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan column info: %v", err)
		}
		cols[name] = colInfo{dflt: dflt}
	}
	if err := colRows.Err(); err != nil {
		t.Fatalf("iterate columns: %v", err)
	}
	for _, name := range []string{"world_id", "tier", "blob_present", "refcount", "mirror_schema_version"} {
		if _, ok := cols[name]; !ok {
			t.Fatalf("save_revisions column %q missing from schema", name)
		}
	}
	wantDefaults := map[string]string{
		"tier":         "'full'",
		"blob_present": "1",
		"refcount":     "0",
	}
	for name, want := range wantDefaults {
		got := cols[name].dflt
		if !got.Valid || got.String != want {
			t.Fatalf("save_revisions.%s default = %q (valid=%v), want %q", name, got.String, got.Valid, want)
		}
	}

	// New indexes must exist.
	wantIndexes := map[string]bool{
		"worlds_workspace_id_idx":     false,
		"nodes_workspace_id_idx":      false,
		"nodes_world_id_idx":          false,
		"save_revisions_world_id_idx": false,
	}
	idxRows, err := store.db.QueryContext(ctx, `
		SELECT name FROM sqlite_master
		WHERE type = 'index' AND name IN (
			'worlds_workspace_id_idx',
			'nodes_workspace_id_idx',
			'nodes_world_id_idx',
			'save_revisions_world_id_idx'
		)
	`)
	if err != nil {
		t.Fatalf("query indexes: %v", err)
	}
	defer idxRows.Close()
	for idxRows.Next() {
		var name string
		if err := idxRows.Scan(&name); err != nil {
			t.Fatalf("scan index name: %v", err)
		}
		wantIndexes[name] = true
	}
	if err := idxRows.Err(); err != nil {
		t.Fatalf("iterate indexes: %v", err)
	}
	for name, found := range wantIndexes {
		if !found {
			t.Fatalf("index %q missing from schema", name)
		}
	}

	// The registry schema must accept rows through the workspace -> world FK
	// chain, with uuid-allocated ids (the id allocation A2 will perform).
	createdAt := nowUTC().Format(time.RFC3339Nano)
	workspaceID := uuid.NewString()
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO workspaces (id, owner, name, created_at) VALUES (?, ?, ?, ?)
	`, workspaceID, "owner", "ws", createdAt); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	worldID := uuid.NewString()
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO worlds (id, workspace_id, name, created_at) VALUES (?, ?, ?, ?)
	`, worldID, workspaceID, "world", createdAt); err != nil {
		t.Fatalf("insert world: %v", err)
	}

	// A world_id referencing a non-existent world must be rejected by the FK.
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO worlds (id, workspace_id, name, created_at) VALUES (?, ?, ?, ?)
	`, uuid.NewString(), uuid.NewString(), "orphan", createdAt); err == nil {
		t.Fatalf("insert world with missing workspace_id succeeded, want FK rejection")
	}

	// Defaulted save_revisions columns must be observable on a fresh insert.
	runID := insertScriptRunForSchemaTest(t, ctx, store)
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO save_revisions (
			sha256, size, source_path, blob_ref, script_run_id, created_at, world_id
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`, strings.Repeat("c", blobstore.SHA256HexLength), 0, "", "{}", runID, createdAt, worldID); err != nil {
		t.Fatalf("insert save_revision with world_id: %v", err)
	}
	var (
		tier        string
		blobPresent int64
		refcount    int64
	)
	if err := store.db.QueryRowContext(ctx, `
		SELECT tier, blob_present, refcount FROM save_revisions WHERE world_id = ?
	`, worldID).Scan(&tier, &blobPresent, &refcount); err != nil {
		t.Fatalf("read defaulted save_revisions row: %v", err)
	}
	if tier != "full" || blobPresent != 1 || refcount != 0 {
		t.Fatalf("defaulted save_revisions row = (tier=%q, blob_present=%d, refcount=%d), want (full, 1, 0)", tier, blobPresent, refcount)
	}
}

func insertScriptRunForSchemaTest(t *testing.T, ctx context.Context, store *Store) int64 {
	t.Helper()
	run, err := store.RecordScriptRun(ctx, ScriptRunInput{
		ScriptSHA256: strings.Repeat("d", blobstore.SHA256HexLength),
		Status:       "succeeded",
	})
	if err != nil {
		t.Fatalf("RecordScriptRun() error = %v", err)
	}
	return run.ID
}

func assertRawInlineBlob(t *testing.T, ctx context.Context, store *Store, revisionID int64, want []byte) {
	t.Helper()

	var got []byte
	if err := store.db.QueryRowContext(ctx, `
		SELECT inline_blob
		FROM save_revisions
		WHERE id = ?
	`, revisionID).Scan(&got); err != nil {
		t.Fatalf("query inline_blob: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("inline_blob = %q, want %q", got, want)
	}
	if (got == nil) != (want == nil) {
		t.Fatalf("inline_blob nil = %v, want %v", got == nil, want == nil)
	}
}

func assertRefEqual(t *testing.T, got, want blobstore.Ref) {
	t.Helper()

	if got.SHA256 != want.SHA256 || got.Size != want.Size || !bytes.Equal(got.Inline, want.Inline) || got.IsInline() != want.IsInline() {
		t.Fatalf("BlobRef = %#v, want %#v", got, want)
	}
}

func sameInstant(a, b time.Time) bool {
	return a.Equal(b.UTC().Round(0))
}

func TestStoreWorkspaceWorldNodeCRUD(t *testing.T) {
	ctx := context.Background()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	ws, err := store.CreateWorkspace(ctx, WorkspaceInput{Owner: "owner", Name: "ws"})
	if err != nil {
		t.Fatalf("CreateWorkspace() error = %v", err)
	}
	if _, err := uuid.Parse(ws.ID); err != nil {
		t.Fatalf("workspace id %q is not a uuid: %v", ws.ID, err)
	}
	if ws.Owner != "owner" || ws.Name != "ws" || ws.CreatedAt.IsZero() {
		t.Fatalf("workspace = %#v", ws)
	}

	gotWS, err := store.GetWorkspace(ctx, ws.ID)
	if err != nil {
		t.Fatalf("GetWorkspace() error = %v", err)
	}
	if gotWS.ID != ws.ID || gotWS.Owner != ws.Owner {
		t.Fatalf("GetWorkspace() = %#v, want %#v", gotWS, ws)
	}

	listWS, err := store.ListWorkspaces(ctx)
	if err != nil {
		t.Fatalf("ListWorkspaces() error = %v", err)
	}
	if len(listWS) != 1 || listWS[0].ID != ws.ID {
		t.Fatalf("ListWorkspaces() = %#v, want one workspace", listWS)
	}

	world, err := store.CreateWorld(ctx, WorldInput{WorkspaceID: ws.ID, Name: "world"})
	if err != nil {
		t.Fatalf("CreateWorld() error = %v", err)
	}
	if _, err := uuid.Parse(world.ID); err != nil {
		t.Fatalf("world id %q is not a uuid: %v", world.ID, err)
	}
	if world.HeadRevisionID != nil || world.SimTime != nil {
		t.Fatalf("new world head/sim_time = (%v, %v), want both nil", world.HeadRevisionID, world.SimTime)
	}

	gotWorld, err := store.GetWorld(ctx, world.ID)
	if err != nil {
		t.Fatalf("GetWorld() error = %v", err)
	}
	if gotWorld.ID != world.ID || gotWorld.WorkspaceID != ws.ID || gotWorld.Name != "world" {
		t.Fatalf("GetWorld() = %#v", gotWorld)
	}

	listWorlds, err := store.ListWorlds(ctx, ws.ID)
	if err != nil {
		t.Fatalf("ListWorlds() error = %v", err)
	}
	if len(listWorlds) != 1 || listWorlds[0].ID != world.ID {
		t.Fatalf("ListWorlds() = %#v, want one world", listWorlds)
	}

	// A world with an unknown workspace_id must be rejected by the FK.
	if _, err := store.CreateWorld(ctx, WorldInput{WorkspaceID: uuid.NewString(), Name: "orphan"}); err == nil {
		t.Fatalf("CreateWorld() with unknown workspace succeeded, want FK rejection")
	}

	node, err := store.CreateNode(ctx, NodeInput{
		WorkspaceID: ws.ID,
		NodeID:      "node-1",
		RunID:       "run-1",
		Status:      "idle",
		CompatAddr:  "127.0.0.1:9000",
		DropPath:    "/drops/node-1",
	})
	if err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	if _, err := uuid.Parse(node.ID); err != nil {
		t.Fatalf("node id %q is not a uuid: %v", node.ID, err)
	}
	if node.WorldID != "" {
		t.Fatalf("new node world_id = %q, want empty (unbound)", node.WorldID)
	}

	if err := store.BindNode(ctx, node.ID, world.ID); err != nil {
		t.Fatalf("BindNode() error = %v", err)
	}
	if err := store.SetNodeStatus(ctx, node.ID, "running"); err != nil {
		t.Fatalf("SetNodeStatus() error = %v", err)
	}

	gotNode, err := store.GetNode(ctx, node.ID)
	if err != nil {
		t.Fatalf("GetNode() error = %v", err)
	}
	if gotNode.WorldID != world.ID || gotNode.Status != "running" {
		t.Fatalf("GetNode() = %#v, want world bound and status running", gotNode)
	}
	if gotNode.CompatAddr != "127.0.0.1:9000" || gotNode.DropPath != "/drops/node-1" || gotNode.RunID != "run-1" {
		t.Fatalf("GetNode() lost fields = %#v", gotNode)
	}

	listNodes, err := store.ListNodes(ctx, ws.ID)
	if err != nil {
		t.Fatalf("ListNodes() error = %v", err)
	}
	if len(listNodes) != 1 || listNodes[0].WorldID != world.ID || listNodes[0].Status != "running" {
		t.Fatalf("ListNodes() = %#v", listNodes)
	}

	// Unknown node mutations must error.
	if err := store.BindNode(ctx, uuid.NewString(), world.ID); err == nil {
		t.Fatalf("BindNode() on unknown node succeeded, want error")
	}
	if err := store.SetNodeStatus(ctx, uuid.NewString(), "x"); err == nil {
		t.Fatalf("SetNodeStatus() on unknown node succeeded, want error")
	}
}

func TestStoreRecordRevisionAdvancingHead(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	store, err := Open(filepath.Join(dir, "metadata.sqlite"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	blobs, err := blobstore.NewFSStore(filepath.Join(dir, "blobs"), blobstore.WithInlineThreshold(0))
	if err != nil {
		t.Fatalf("NewFSStore() error = %v", err)
	}
	defer blobs.Close()

	ws, err := store.CreateWorkspace(ctx, WorkspaceInput{Owner: "owner", Name: "ws"})
	if err != nil {
		t.Fatalf("CreateWorkspace() error = %v", err)
	}
	world, err := store.CreateWorld(ctx, WorldInput{WorkspaceID: ws.ID, Name: "world"})
	if err != nil {
		t.Fatalf("CreateWorld() error = %v", err)
	}

	run, err := store.RecordScriptRun(ctx, ScriptRunInput{
		ScriptSHA256: strings.Repeat("a", blobstore.SHA256HexLength),
		Status:       "succeeded",
	})
	if err != nil {
		t.Fatalf("RecordScriptRun() error = %v", err)
	}

	firstRef, err := blobs.Put(ctx, []byte("first world save"))
	if err != nil {
		t.Fatalf("Put(first) error = %v", err)
	}
	firstSim := 10.0
	first, err := store.RecordRevisionAdvancingHead(ctx, world.ID, &firstSim, RevisionInput{
		BlobRef:     firstRef,
		ScriptRunID: run.ID,
	})
	if err != nil {
		t.Fatalf("RecordRevisionAdvancingHead(first) error = %v", err)
	}
	if first.WorldID != world.ID {
		t.Fatalf("first revision world_id = %q, want %q", first.WorldID, world.ID)
	}
	if first.ParentID != nil {
		t.Fatalf("first revision parent_id = %v, want nil", first.ParentID)
	}
	if first.Tier != "full" || !first.BlobPresent || first.Refcount != 1 || first.MirrorSchemaVersion != nil {
		t.Fatalf("first revision tier defaults = (tier=%q, present=%v, refcount=%d (want 1, the self-ref), mirror=%v)", first.Tier, first.BlobPresent, first.Refcount, first.MirrorSchemaVersion)
	}

	afterFirst, err := store.GetWorld(ctx, world.ID)
	if err != nil {
		t.Fatalf("GetWorld() after first error = %v", err)
	}
	if afterFirst.HeadRevisionID == nil || *afterFirst.HeadRevisionID != first.ID {
		t.Fatalf("world head = %v, want %d", afterFirst.HeadRevisionID, first.ID)
	}
	if afterFirst.SimTime == nil || *afterFirst.SimTime != firstSim {
		t.Fatalf("world sim_time = %v, want %v", afterFirst.SimTime, firstSim)
	}

	secondRef, err := blobs.Put(ctx, []byte("second world save"))
	if err != nil {
		t.Fatalf("Put(second) error = %v", err)
	}
	secondSim := 20.0
	second, err := store.RecordRevisionAdvancingHead(ctx, world.ID, &secondSim, RevisionInput{
		ParentID:    &first.ID,
		BlobRef:     secondRef,
		ScriptRunID: run.ID,
	})
	if err != nil {
		t.Fatalf("RecordRevisionAdvancingHead(second) error = %v", err)
	}
	if second.ParentID == nil || *second.ParentID != first.ID {
		t.Fatalf("second revision parent_id = %v, want %d", second.ParentID, first.ID)
	}

	afterSecond, err := store.GetWorld(ctx, world.ID)
	if err != nil {
		t.Fatalf("GetWorld() after second error = %v", err)
	}
	if afterSecond.HeadRevisionID == nil || *afterSecond.HeadRevisionID != second.ID {
		t.Fatalf("world head = %v, want %d", afterSecond.HeadRevisionID, second.ID)
	}
	if afterSecond.SimTime == nil || *afterSecond.SimTime != secondSim {
		t.Fatalf("world sim_time = %v, want %v", afterSecond.SimTime, secondSim)
	}

	lineage, err := store.RevisionsForWorld(ctx, world.ID)
	if err != nil {
		t.Fatalf("RevisionsForWorld() error = %v", err)
	}
	if len(lineage) != 2 {
		t.Fatalf("RevisionsForWorld() len = %d, want 2", len(lineage))
	}
	if lineage[0].ID != first.ID || lineage[1].ID != second.ID {
		t.Fatalf("RevisionsForWorld() order = [%d, %d], want [%d, %d]", lineage[0].ID, lineage[1].ID, first.ID, second.ID)
	}
	if lineage[0].ParentID != nil {
		t.Fatalf("lineage[0].ParentID = %v, want nil", lineage[0].ParentID)
	}
	if lineage[1].ParentID == nil || *lineage[1].ParentID != first.ID {
		t.Fatalf("lineage[1].ParentID = %v, want %d", lineage[1].ParentID, first.ID)
	}
}

func TestStoreRecordRevisionAdvancingHeadAtomic(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	store, err := Open(filepath.Join(dir, "metadata.sqlite"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	blobs, err := blobstore.NewFSStore(filepath.Join(dir, "blobs"), blobstore.WithInlineThreshold(0))
	if err != nil {
		t.Fatalf("NewFSStore() error = %v", err)
	}
	defer blobs.Close()

	run, err := store.RecordScriptRun(ctx, ScriptRunInput{
		ScriptSHA256: strings.Repeat("b", blobstore.SHA256HexLength),
		Status:       "succeeded",
	})
	if err != nil {
		t.Fatalf("RecordScriptRun() error = %v", err)
	}
	ref, err := blobs.Put(ctx, []byte("doomed save"))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	// A world_id that does not exist: the INSERT's FK fails (or the head UPDATE
	// finds no world); either way nothing must be committed.
	badWorld := uuid.NewString()
	sim := 5.0
	if _, err := store.RecordRevisionAdvancingHead(ctx, badWorld, &sim, RevisionInput{
		BlobRef:     ref,
		ScriptRunID: run.ID,
	}); err == nil {
		t.Fatalf("RecordRevisionAdvancingHead() with unknown world succeeded, want error")
	}

	// No revision row may have leaked from the rolled-back tx.
	leaked, err := store.RevisionsForWorld(ctx, badWorld)
	if err != nil {
		t.Fatalf("RevisionsForWorld() error = %v", err)
	}
	if len(leaked) != 0 {
		t.Fatalf("RevisionsForWorld(badWorld) = %d rows, want 0 (rolled back)", len(leaked))
	}
	var total int64
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM save_revisions`).Scan(&total); err != nil {
		t.Fatalf("count save_revisions: %v", err)
	}
	if total != 0 {
		t.Fatalf("save_revisions count = %d, want 0 (no leaked insert)", total)
	}
}

func TestStoreBlobRefcountLifecycle(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	store, err := Open(filepath.Join(dir, "metadata.sqlite"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	blobs, err := blobstore.NewFSStore(filepath.Join(dir, "blobs"), blobstore.WithInlineThreshold(0))
	if err != nil {
		t.Fatalf("NewFSStore() error = %v", err)
	}
	defer blobs.Close()

	run, err := store.RecordScriptRun(ctx, ScriptRunInput{
		ScriptSHA256: strings.Repeat("c", blobstore.SHA256HexLength),
		Status:       "succeeded",
	})
	if err != nil {
		t.Fatalf("RecordScriptRun() error = %v", err)
	}
	ref, err := blobs.Put(ctx, []byte("refcounted save"))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	rev, err := store.RecordRevision(ctx, RevisionInput{
		BlobRef:     ref,
		ScriptRunID: run.ID,
	})
	if err != nil {
		t.Fatalf("RecordRevision() error = %v", err)
	}

	if _, err := store.IncBlobRef(ctx, ref.SHA256); err != nil {
		t.Fatalf("IncBlobRef() error = %v", err)
	}
	if _, err := store.IncBlobRef(ctx, ref.SHA256); err != nil {
		t.Fatalf("IncBlobRef() error = %v", err)
	}
	if _, err := store.DecBlobRef(ctx, ref.SHA256); err != nil {
		t.Fatalf("DecBlobRef() error = %v", err)
	}
	// RecordRevision establishes the revision's own self-ref (refcount 1), so
	// after self-ref(1) + inc x2(3) - dec(1) the count is 2.
	if got := rawRefcount(t, ctx, store, rev.ID); got != 2 {
		t.Fatalf("refcount after record self-ref + inc x2 / dec = %d, want 2", got)
	}

	// Dec down to 0 then again: the WHERE refcount > 0 guard keeps it at 0
	// (CHECK (refcount >= 0) must never be violated). Two more decs bring 2 -> 0.
	if _, err := store.DecBlobRef(ctx, ref.SHA256); err != nil {
		t.Fatalf("DecBlobRef() toward zero error = %v", err)
	}
	if _, err := store.DecBlobRef(ctx, ref.SHA256); err != nil {
		t.Fatalf("DecBlobRef() to zero error = %v", err)
	}
	if affected, err := store.DecBlobRef(ctx, ref.SHA256); err != nil {
		t.Fatalf("DecBlobRef() at zero error = %v", err)
	} else if affected != 0 {
		t.Fatalf("DecBlobRef() at zero affected = %d, want 0", affected)
	}
	if got := rawRefcount(t, ctx, store, rev.ID); got != 0 {
		t.Fatalf("refcount after dec to zero = %d, want 0", got)
	}

	// UnreferencedBlobs: a refcount=0 hash that is still blob_present=1 is NOT a
	// GC candidate (its bytes are still needed by a full revision).
	unref, err := store.UnreferencedBlobs(ctx)
	if err != nil {
		t.Fatalf("UnreferencedBlobs() error = %v", err)
	}
	if containsString(unref, ref.SHA256) {
		t.Fatalf("UnreferencedBlobs() = %v, must exclude still-present hash %s", unref, ref.SHA256)
	}

	// Evict the revision (non-head, single ref) so its blob_present flips to 0,
	// then the hash becomes a GC candidate.
	if err := store.EvictRevisionBlob(ctx, rev.ID); err != nil {
		t.Fatalf("EvictRevisionBlob() error = %v", err)
	}
	unref, err = store.UnreferencedBlobs(ctx)
	if err != nil {
		t.Fatalf("UnreferencedBlobs() after evict error = %v", err)
	}
	if !containsString(unref, ref.SHA256) {
		t.Fatalf("UnreferencedBlobs() = %v, want it to include evicted hash %s", unref, ref.SHA256)
	}
}

func TestStoreRecordEstablishesBlobRef(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	store, err := Open(filepath.Join(dir, "metadata.sqlite"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	blobs, err := blobstore.NewFSStore(filepath.Join(dir, "blobs"), blobstore.WithInlineThreshold(0))
	if err != nil {
		t.Fatalf("NewFSStore() error = %v", err)
	}
	defer blobs.Close()

	run, err := store.RecordScriptRun(ctx, ScriptRunInput{
		ScriptSHA256: strings.Repeat("d", blobstore.SHA256HexLength),
		Status:       "succeeded",
	})
	if err != nil {
		t.Fatalf("RecordScriptRun() error = %v", err)
	}

	// A freshly recorded revision establishes its own blob reference atomically
	// with the insert: refcount lands at 1, not the schema default 0.
	ref, err := blobs.Put(ctx, []byte("self-referenced save"))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	first, err := store.RecordRevision(ctx, RevisionInput{
		BlobRef:     ref,
		ScriptRunID: run.ID,
	})
	if err != nil {
		t.Fatalf("RecordRevision(first) error = %v", err)
	}
	if first.Refcount != 1 {
		t.Fatalf("first.Refcount = %d, want 1 (the revision's own self-ref)", first.Refcount)
	}
	if got := rawRefcount(t, ctx, store, first.ID); got != 1 {
		t.Fatalf("on-disk refcount after first record = %d, want 1", got)
	}

	// Recording a second revision with the SAME BlobRef (dedup) shares the hash;
	// the per-hash count tracks the number of referencing revisions, so both
	// rows now read refcount 2.
	second, err := store.RecordRevision(ctx, RevisionInput{
		BlobRef:     ref,
		ScriptRunID: run.ID,
	})
	if err != nil {
		t.Fatalf("RecordRevision(second, same hash) error = %v", err)
	}
	if got := rawRefcount(t, ctx, store, first.ID); got != 2 {
		t.Fatalf("first row refcount after dedup-share = %d, want 2", got)
	}
	if got := rawRefcount(t, ctx, store, second.ID); got != 2 {
		t.Fatalf("second row refcount after dedup-share = %d, want 2", got)
	}

	// A distinct-hash revision recorded into a world via the advancing-head path
	// also self-refs in the one atomic tx. The crash-equivalent check: the
	// freshly recorded, still-blob_present hash must NOT appear in
	// UnreferencedBlobs (its bytes are still needed), proving the +1 committed
	// with the insert rather than leaving a refcount-0 window.
	ws, err := store.CreateWorkspace(ctx, WorkspaceInput{Owner: "owner", Name: "ws"})
	if err != nil {
		t.Fatalf("CreateWorkspace() error = %v", err)
	}
	world, err := store.CreateWorld(ctx, WorldInput{WorkspaceID: ws.ID, Name: "world"})
	if err != nil {
		t.Fatalf("CreateWorld() error = %v", err)
	}
	headRef, err := blobs.Put(ctx, []byte("head save bytes"))
	if err != nil {
		t.Fatalf("Put(head) error = %v", err)
	}
	headSim := 5.0
	head, err := store.RecordRevisionAdvancingHead(ctx, world.ID, &headSim, RevisionInput{
		BlobRef:     headRef,
		ScriptRunID: run.ID,
	})
	if err != nil {
		t.Fatalf("RecordRevisionAdvancingHead(head) error = %v", err)
	}
	if head.Refcount != 1 {
		t.Fatalf("head.Refcount = %d, want 1 (the revision's own self-ref)", head.Refcount)
	}
	if got := rawRefcount(t, ctx, store, head.ID); got != 1 {
		t.Fatalf("on-disk refcount after head record = %d, want 1", got)
	}
	unref, err := store.UnreferencedBlobs(ctx)
	if err != nil {
		t.Fatalf("UnreferencedBlobs() error = %v", err)
	}
	if containsString(unref, headRef.SHA256) {
		t.Fatalf("UnreferencedBlobs() = %v, must not list freshly recorded hash %s", unref, headRef.SHA256)
	}
	if containsString(unref, ref.SHA256) {
		t.Fatalf("UnreferencedBlobs() = %v, must not list dedup-shared hash %s", unref, ref.SHA256)
	}
}

func TestStoreEvictRevisionBlob(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	store, err := Open(filepath.Join(dir, "metadata.sqlite"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	blobs, err := blobstore.NewFSStore(filepath.Join(dir, "blobs"), blobstore.WithInlineThreshold(0))
	if err != nil {
		t.Fatalf("NewFSStore() error = %v", err)
	}
	defer blobs.Close()

	ws, err := store.CreateWorkspace(ctx, WorkspaceInput{Owner: "owner", Name: "ws"})
	if err != nil {
		t.Fatalf("CreateWorkspace() error = %v", err)
	}
	world, err := store.CreateWorld(ctx, WorldInput{WorkspaceID: ws.ID, Name: "world"})
	if err != nil {
		t.Fatalf("CreateWorld() error = %v", err)
	}
	run, err := store.RecordScriptRun(ctx, ScriptRunInput{
		ScriptSHA256: strings.Repeat("e", blobstore.SHA256HexLength),
		Status:       "succeeded",
	})
	if err != nil {
		t.Fatalf("RecordScriptRun() error = %v", err)
	}

	olderRef, err := blobs.Put(ctx, []byte("older world save"))
	if err != nil {
		t.Fatalf("Put(older) error = %v", err)
	}
	older, err := store.RecordRevisionAdvancingHead(ctx, world.ID, nil, RevisionInput{
		BlobRef:     olderRef,
		ScriptRunID: run.ID,
	})
	if err != nil {
		t.Fatalf("RecordRevisionAdvancingHead(older) error = %v", err)
	}

	headRef, err := blobs.Put(ctx, []byte("head world save"))
	if err != nil {
		t.Fatalf("Put(head) error = %v", err)
	}
	head, err := store.RecordRevisionAdvancingHead(ctx, world.ID, nil, RevisionInput{
		ParentID:    &older.ID,
		BlobRef:     headRef,
		ScriptRunID: run.ID,
	})
	if err != nil {
		t.Fatalf("RecordRevisionAdvancingHead(head) error = %v", err)
	}

	// Evicting the non-head, single-ref older revision flips it to mirror_only.
	if err := store.EvictRevisionBlob(ctx, older.ID); err != nil {
		t.Fatalf("EvictRevisionBlob(older) error = %v", err)
	}
	gotOlder, err := store.RevisionByID(ctx, older.ID)
	if err != nil {
		t.Fatalf("RevisionByID(older) after evict error = %v", err)
	}
	if gotOlder.Tier != "mirror_only" || gotOlder.BlobPresent {
		t.Fatalf("evicted revision = (tier=%q, present=%v), want (mirror_only, false)", gotOlder.Tier, gotOlder.BlobPresent)
	}

	// The row must still be present (history retained) and the blobstore bytes
	// must be untouched (A2 never deletes them).
	if has, err := blobs.Has(ctx, olderRef); err != nil {
		t.Fatalf("blobs.Has(older) error = %v", err)
	} else if !has {
		t.Fatalf("blobstore lost evicted bytes; A2 must not delete blobstore bytes")
	}

	// Evicting the head is refused.
	if err := store.EvictRevisionBlob(ctx, head.ID); !errors.Is(err, ErrRevisionIsHead) {
		t.Fatalf("EvictRevisionBlob(head) error = %v, want ErrRevisionIsHead", err)
	}

	// A revision whose sha256 has refcount > 1 is refused. Use a fresh non-head
	// revision and bump its refcount above 1.
	sharedRef, err := blobs.Put(ctx, []byte("shared bytes save"))
	if err != nil {
		t.Fatalf("Put(shared) error = %v", err)
	}
	shared, err := store.RecordRevision(ctx, RevisionInput{
		BlobRef:     sharedRef,
		ScriptRunID: run.ID,
	})
	if err != nil {
		t.Fatalf("RecordRevision(shared) error = %v", err)
	}
	if _, err := store.IncBlobRef(ctx, sharedRef.SHA256); err != nil {
		t.Fatalf("IncBlobRef() error = %v", err)
	}
	if _, err := store.IncBlobRef(ctx, sharedRef.SHA256); err != nil {
		t.Fatalf("IncBlobRef() error = %v", err)
	}
	if err := store.EvictRevisionBlob(ctx, shared.ID); !errors.Is(err, ErrBlobStillReferenced) {
		t.Fatalf("EvictRevisionBlob(shared) error = %v, want ErrBlobStillReferenced", err)
	}

	// PromoteRevision restores the evicted older revision.
	if err := store.PromoteRevision(ctx, older.ID); err != nil {
		t.Fatalf("PromoteRevision(older) error = %v", err)
	}
	promoted, err := store.RevisionByID(ctx, older.ID)
	if err != nil {
		t.Fatalf("RevisionByID(older) after promote error = %v", err)
	}
	if promoted.Tier != "full" || !promoted.BlobPresent {
		t.Fatalf("promoted revision = (tier=%q, present=%v), want (full, true)", promoted.Tier, promoted.BlobPresent)
	}

	// Promoting an already-full revision is a no-op (no error).
	if err := store.PromoteRevision(ctx, older.ID); err != nil {
		t.Fatalf("PromoteRevision(already full) error = %v", err)
	}
	stillFull, err := store.RevisionByID(ctx, older.ID)
	if err != nil {
		t.Fatalf("RevisionByID(older) after second promote error = %v", err)
	}
	if stillFull.Tier != "full" || !stillFull.BlobPresent {
		t.Fatalf("re-promoted revision = (tier=%q, present=%v), want (full, true)", stillFull.Tier, stillFull.BlobPresent)
	}
}

func rawRefcount(t *testing.T, ctx context.Context, store *Store, revisionID int64) int64 {
	t.Helper()
	var refcount int64
	if err := store.db.QueryRowContext(ctx, `
		SELECT refcount FROM save_revisions WHERE id = ?
	`, revisionID).Scan(&refcount); err != nil {
		t.Fatalf("query refcount: %v", err)
	}
	return refcount
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// seedWorldRevision creates a workspace + world and records one full revision
// against it (advancing the head), returning the workspace id and the revision.
// Used by the C4b RevisionsForWorkspace / CatalogFingerprint tests.
func seedWorldRevision(t *testing.T, ctx context.Context, store *Store, blobs *blobstore.FSStore, payload string) (wsID string, rev Revision) {
	t.Helper()
	ws, err := store.CreateWorkspace(ctx, WorkspaceInput{Owner: "owner", Name: "ws"})
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	world, err := store.CreateWorld(ctx, WorldInput{WorkspaceID: ws.ID, Name: "world"})
	if err != nil {
		t.Fatalf("CreateWorld: %v", err)
	}
	run, err := store.RecordScriptRun(ctx, ScriptRunInput{
		ScriptSHA256: strings.Repeat("a", blobstore.SHA256HexLength),
		Status:       "succeeded",
	})
	if err != nil {
		t.Fatalf("RecordScriptRun: %v", err)
	}
	ref, err := blobs.Put(ctx, []byte(payload))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	sim := 1.0
	rev, err = store.RecordRevisionAdvancingHead(ctx, world.ID, &sim, RevisionInput{
		BlobRef:     ref,
		ScriptRunID: run.ID,
	})
	if err != nil {
		t.Fatalf("RecordRevisionAdvancingHead: %v", err)
	}
	return ws.ID, rev
}

// TestRevisionsForWorkspaceScopesByWorkspace proves the shared-registry scoping
// invariant: two workspaces in one registry, each with a world+revision, and
// RevisionsForWorkspace returns ONLY the requested workspace's revision, ordered
// by id. A leak here would attribute another workspace's history into this
// workspace's analytics mirror.
func TestRevisionsForWorkspaceScopesByWorkspace(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	store, err := Open(filepath.Join(dir, "metadata.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	blobs, err := blobstore.NewFSStore(filepath.Join(dir, "blobs"), blobstore.WithInlineThreshold(0))
	if err != nil {
		t.Fatalf("NewFSStore: %v", err)
	}
	defer blobs.Close()

	wsA, revA := seedWorldRevision(t, ctx, store, blobs, "workspace A save bytes")
	wsB, revB := seedWorldRevision(t, ctx, store, blobs, "workspace B save bytes")

	gotA, err := store.RevisionsForWorkspace(ctx, wsA)
	if err != nil {
		t.Fatalf("RevisionsForWorkspace(A): %v", err)
	}
	if len(gotA) != 1 || gotA[0].ID != revA.ID {
		t.Fatalf("RevisionsForWorkspace(A) = %v, want exactly revA id %d", gotA, revA.ID)
	}
	for _, r := range gotA {
		if r.ID == revB.ID {
			t.Fatalf("RevisionsForWorkspace(A) leaked workspace B revision %d", revB.ID)
		}
	}

	gotB, err := store.RevisionsForWorkspace(ctx, wsB)
	if err != nil {
		t.Fatalf("RevisionsForWorkspace(B): %v", err)
	}
	if len(gotB) != 1 || gotB[0].ID != revB.ID {
		t.Fatalf("RevisionsForWorkspace(B) = %v, want exactly revB id %d", gotB, revB.ID)
	}
}

// TestCatalogFingerprintMovesOnFlip proves the StateSum dimension catches an
// in-place tier/blob_present flip that Count and MaxID would MISS — the
// correctness invariant that keeps the analytics cache from going stale across a
// G2 eviction.
func TestCatalogFingerprintMovesOnFlip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	store, err := Open(filepath.Join(dir, "metadata.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	blobs, err := blobstore.NewFSStore(filepath.Join(dir, "blobs"), blobstore.WithInlineThreshold(0))
	if err != nil {
		t.Fatalf("NewFSStore: %v", err)
	}
	defer blobs.Close()

	wsID, rev := seedWorldRevision(t, ctx, store, blobs, "evictable save bytes")

	before, err := store.CatalogFingerprint(ctx, wsID)
	if err != nil {
		t.Fatalf("CatalogFingerprint(before): %v", err)
	}

	// MarkMirrorOnly flips tier='mirror_only', blob_present=0 in place without
	// adding or removing a revision (so Count and MaxID stay constant).
	if err := store.MarkMirrorOnly(ctx, rev.ID); err != nil {
		t.Fatalf("MarkMirrorOnly: %v", err)
	}

	after, err := store.CatalogFingerprint(ctx, wsID)
	if err != nil {
		t.Fatalf("CatalogFingerprint(after): %v", err)
	}

	if after == before {
		t.Fatalf("fingerprint unchanged after in-place flip: %+v", after)
	}
	if after.Count != before.Count {
		t.Errorf("Count changed (%d -> %d); flip should not move count", before.Count, after.Count)
	}
	if after.MaxID != before.MaxID {
		t.Errorf("MaxID changed (%d -> %d); flip should not move max id", before.MaxID, after.MaxID)
	}
	if after.StateSum == before.StateSum {
		t.Errorf("StateSum unchanged (%d) after flip; the in-place dimension is broken", after.StateSum)
	}
}

// orphanedSHAs collapses an OrphanedBlobs result to its sha256 set for assertion.
func orphanedSHAs(revs []Revision) []string {
	out := make([]string, 0, len(revs))
	for _, r := range revs {
		out = append(out, r.SHA256)
	}
	return out
}

// TestStoreOrphanedBlobs proves OrphanedBlobs catches the refcount-1 orphan that
// UnreferencedBlobs (refcount-gated) silently misses: G2's EvictRevisionBlob
// flips tier/blob_present only and leaves refcount=1. It also proves a still
// blob_present=1 sha and a dedup-shared (one full + one mirror) sha are excluded.
func TestStoreOrphanedBlobs(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	store, err := Open(filepath.Join(dir, "metadata.sqlite"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	blobs, err := blobstore.NewFSStore(filepath.Join(dir, "blobs"), blobstore.WithInlineThreshold(0))
	if err != nil {
		t.Fatalf("NewFSStore() error = %v", err)
	}
	defer blobs.Close()

	run, err := store.RecordScriptRun(ctx, ScriptRunInput{
		ScriptSHA256: strings.Repeat("f", blobstore.SHA256HexLength),
		Status:       "succeeded",
	})
	if err != nil {
		t.Fatalf("RecordScriptRun() error = %v", err)
	}

	// Orphan: a non-head, single-ref revision evicted to mirror_only. Its refcount
	// stays 1, so UnreferencedBlobs misses it but OrphanedBlobs must catch it.
	orphanRef, err := blobs.Put(ctx, []byte("orphan save bytes"))
	if err != nil {
		t.Fatalf("Put(orphan) error = %v", err)
	}
	orphan, err := store.RecordRevision(ctx, RevisionInput{
		BlobRef:     orphanRef,
		ScriptRunID: run.ID,
	})
	if err != nil {
		t.Fatalf("RecordRevision(orphan) error = %v", err)
	}
	if err := store.EvictRevisionBlob(ctx, orphan.ID); err != nil {
		t.Fatalf("EvictRevisionBlob(orphan) error = %v", err)
	}
	// Sanity: the evicted orphan keeps refcount 1, so UnreferencedBlobs misses it.
	if got := rawRefcount(t, ctx, store, orphan.ID); got != 1 {
		t.Fatalf("evicted orphan refcount = %d, want 1 (eviction flips tier only)", got)
	}
	if unref, err := store.UnreferencedBlobs(ctx); err != nil {
		t.Fatalf("UnreferencedBlobs() error = %v", err)
	} else if containsString(unref, orphanRef.SHA256) {
		t.Fatalf("UnreferencedBlobs() = %v, refcount-gated query must MISS the refcount-1 orphan", unref)
	}

	// Still-present: a full/blob_present=1 revision must NOT be an orphan.
	presentRef, err := blobs.Put(ctx, []byte("present save bytes"))
	if err != nil {
		t.Fatalf("Put(present) error = %v", err)
	}
	if _, err := store.RecordRevision(ctx, RevisionInput{
		BlobRef:     presentRef,
		ScriptRunID: run.ID,
	}); err != nil {
		t.Fatalf("RecordRevision(present) error = %v", err)
	}

	// Dedup-shared: one full + one mirror row of the same sha. The bytes are still
	// needed by the full row, so the sha must NOT be an orphan even though a
	// mirror_only row of it exists.
	sharedRef, err := blobs.Put(ctx, []byte("dedup shared save bytes"))
	if err != nil {
		t.Fatalf("Put(shared) error = %v", err)
	}
	sharedFull, err := store.RecordRevision(ctx, RevisionInput{
		BlobRef:     sharedRef,
		ScriptRunID: run.ID,
	})
	if err != nil {
		t.Fatalf("RecordRevision(shared full) error = %v", err)
	}
	sharedMirror, err := store.RecordRevision(ctx, RevisionInput{
		BlobRef:     sharedRef,
		ScriptRunID: run.ID,
	})
	if err != nil {
		t.Fatalf("RecordRevision(shared mirror) error = %v", err)
	}
	_ = sharedFull
	if err := store.MarkMirrorOnly(ctx, sharedMirror.ID); err != nil {
		t.Fatalf("MarkMirrorOnly(shared mirror) error = %v", err)
	}

	orphans, err := store.OrphanedBlobs(ctx)
	if err != nil {
		t.Fatalf("OrphanedBlobs() error = %v", err)
	}
	shas := orphanedSHAs(orphans)
	if !containsString(shas, orphanRef.SHA256) {
		t.Fatalf("OrphanedBlobs() = %v, must include the refcount-1 evicted orphan %s", shas, orphanRef.SHA256)
	}
	if containsString(shas, presentRef.SHA256) {
		t.Fatalf("OrphanedBlobs() = %v, must exclude still-present sha %s", shas, presentRef.SHA256)
	}
	if containsString(shas, sharedRef.SHA256) {
		t.Fatalf("OrphanedBlobs() = %v, must exclude dedup-shared sha %s (a full row still needs it)", shas, sharedRef.SHA256)
	}

	// One representative row per orphan sha (MIN(id) collapse): the orphan sha
	// appears exactly once.
	count := 0
	for _, sha := range shas {
		if sha == orphanRef.SHA256 {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("orphan sha %s appears %d times, want exactly one representative row", orphanRef.SHA256, count)
	}
}
