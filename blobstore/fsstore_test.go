package blobstore

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestFSStorePutGetRoundTrip(t *testing.T) {
	ctx := context.Background()
	store, err := NewFSStore(t.TempDir(), WithInlineThreshold(4))
	if err != nil {
		t.Fatalf("NewFSStore() error = %v", err)
	}
	defer store.Close()

	want := []byte("hello")
	ref, err := store.Put(ctx, want)
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if ref.IsInline() {
		t.Fatalf("Put() returned inline ref for %d-byte blob at threshold %d", len(want), store.InlineThreshold())
	}
	if ref.SHA256 != digestBytes(want) {
		t.Fatalf("ref sha256 = %q, want %q", ref.SHA256, digestBytes(want))
	}
	if ref.Size != int64(len(want)) {
		t.Fatalf("ref size = %d, want %d", ref.Size, len(want))
	}

	has, err := store.Has(ctx, ref)
	if err != nil {
		t.Fatalf("Has() error = %v", err)
	}
	if !has {
		t.Fatalf("Has() = false, want true")
	}

	got, err := store.Get(ctx, ref)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("Get() = %q, want %q", got, want)
	}
}

func TestFSStoreDedupesIdenticalContent(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := NewFSStore(root, WithInlineThreshold(0))
	if err != nil {
		t.Fatalf("NewFSStore() error = %v", err)
	}
	defer store.Close()

	blob := []byte("same bytes")
	ref1, err := store.Put(ctx, blob)
	if err != nil {
		t.Fatalf("first Put() error = %v", err)
	}
	ref2, err := store.Put(ctx, append([]byte(nil), blob...))
	if err != nil {
		t.Fatalf("second Put() error = %v", err)
	}
	if ref1.SHA256 != ref2.SHA256 || ref1.Size != ref2.Size || ref1.IsInline() || ref2.IsInline() {
		t.Fatalf("refs differ: %#v vs %#v", ref1, ref2)
	}

	if got := countObjectFiles(t, root); got != 1 {
		t.Fatalf("object file count = %d, want 1", got)
	}
}

func TestFSStoreInlineThresholdBoundary(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := NewFSStore(root, WithInlineThreshold(4))
	if err != nil {
		t.Fatalf("NewFSStore() error = %v", err)
	}
	defer store.Close()

	inlineBlob := []byte("abc")
	inlineRef, err := store.Put(ctx, inlineBlob)
	if err != nil {
		t.Fatalf("Put(inline) error = %v", err)
	}
	if !inlineRef.IsInline() {
		t.Fatalf("Put(%d-byte blob) returned non-inline ref at threshold %d", len(inlineBlob), store.InlineThreshold())
	}
	if !bytes.Equal(inlineRef.Inline, inlineBlob) {
		t.Fatalf("inline ref bytes = %q, want %q", inlineRef.Inline, inlineBlob)
	}
	gotInline, err := store.Get(ctx, inlineRef)
	if err != nil {
		t.Fatalf("Get(inline) error = %v", err)
	}
	if !bytes.Equal(gotInline, inlineBlob) {
		t.Fatalf("Get(inline) = %q, want %q", gotInline, inlineBlob)
	}
	assertNoObjectFile(t, store, inlineRef)

	boundaryBlob := []byte("wxyz")
	boundaryRef, err := store.Put(ctx, boundaryBlob)
	if err != nil {
		t.Fatalf("Put(boundary) error = %v", err)
	}
	if boundaryRef.IsInline() {
		t.Fatalf("Put(%d-byte blob) returned inline ref at threshold %d", len(boundaryBlob), store.InlineThreshold())
	}
	assertObjectFile(t, store, boundaryRef)
}

func countObjectFiles(t *testing.T, root string) int {
	t.Helper()

	count := 0
	err := filepath.WalkDir(filepath.Join(root, "objects"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk objects: %v", err)
	}
	return count
}

func assertObjectFile(t *testing.T, store *FSStore, ref Ref) {
	t.Helper()

	path, err := store.objectPath(ref.SHA256)
	if err != nil {
		t.Fatalf("objectPath() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat object %s: %v", path, err)
	}
	if info.IsDir() {
		t.Fatalf("object path %s is a directory", path)
	}
	if info.Size() != ref.Size {
		t.Fatalf("object size = %d, want %d", info.Size(), ref.Size)
	}
}

func assertNoObjectFile(t *testing.T, store *FSStore, ref Ref) {
	t.Helper()

	path, err := store.objectPath(ref.SHA256)
	if err != nil {
		t.Fatalf("objectPath() error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stat inline object err = %v, want not-exist", err)
	}
}
