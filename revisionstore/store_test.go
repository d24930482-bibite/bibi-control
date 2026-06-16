package revisionstore

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asemones/bibicontrol/blobstore"
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
