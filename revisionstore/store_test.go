package revisionstore

import (
	"bytes"
	"context"
	"database/sql"
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
