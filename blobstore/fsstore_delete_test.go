package blobstore

import (
	"context"
	"testing"
)

// TestFSStoreDeleteIdempotent proves Delete is the idempotent, byte-destructive
// primitive G2's crash-safe re-run relies on: deleting a present object removes
// it, deleting an absent object is a no-op (nil), and deleting an inline ref
// (no backing object) is a no-op (nil).
func TestFSStoreDeleteIdempotent(t *testing.T) {
	ctx := context.Background()
	store, err := NewFSStore(t.TempDir(), WithInlineThreshold(0))
	if err != nil {
		t.Fatalf("NewFSStore() error = %v", err)
	}
	defer store.Close()

	blob := []byte("delete me")
	ref, err := store.Put(ctx, blob)
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if ref.IsInline() {
		t.Fatalf("Put() returned inline ref at threshold 0; want backing object")
	}
	if has, err := store.Has(ctx, ref); err != nil || !has {
		t.Fatalf("Has(before delete) = (%v, %v), want (true, nil)", has, err)
	}
	assertObjectFile(t, store, ref)

	// First delete removes the object.
	if err := store.Delete(ctx, ref); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if has, err := store.Has(ctx, ref); err != nil || has {
		t.Fatalf("Has(after delete) = (%v, %v), want (false, nil)", has, err)
	}
	assertNoObjectFile(t, store, ref)

	// Second delete on the now-absent object is a no-op (idempotent).
	if err := store.Delete(ctx, ref); err != nil {
		t.Fatalf("Delete() second call (idempotent) error = %v, want nil", err)
	}

	// Deleting an inline ref (no backing object) is a no-op.
	inlineStore, err := NewFSStore(t.TempDir(), WithInlineThreshold(64))
	if err != nil {
		t.Fatalf("NewFSStore(inline) error = %v", err)
	}
	defer inlineStore.Close()
	inlineRef, err := inlineStore.Put(ctx, []byte("tiny"))
	if err != nil {
		t.Fatalf("Put(inline) error = %v", err)
	}
	if !inlineRef.IsInline() {
		t.Fatalf("Put(tiny) returned non-inline ref at threshold 64")
	}
	if err := inlineStore.Delete(ctx, inlineRef); err != nil {
		t.Fatalf("Delete(inline) error = %v, want nil no-op", err)
	}
}

// TestFSStoreDeleteValidatesRef confirms Delete validates its ref like Get/Has.
func TestFSStoreDeleteValidatesRef(t *testing.T) {
	ctx := context.Background()
	store, err := NewFSStore(t.TempDir(), WithInlineThreshold(0))
	if err != nil {
		t.Fatalf("NewFSStore() error = %v", err)
	}
	defer store.Close()

	if err := store.Delete(ctx, Ref{SHA256: "not-a-valid-digest", Size: 1}); err == nil {
		t.Fatalf("Delete(invalid ref) error = nil, want validation error")
	}
}
