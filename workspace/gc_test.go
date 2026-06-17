package workspace

import (
	"context"
	"testing"

	"github.com/asemones/bibicontrol/revisionstore"
)

// nonHeadRevision returns the first revision in revs that is not the head.
func nonHeadRevision(t *testing.T, revs []revisionstore.Revision, headID int64) revisionstore.Revision {
	t.Helper()
	for _, r := range revs {
		if r.ID != headID {
			return r
		}
	}
	t.Fatal("no non-head revision found")
	return revisionstore.Revision{}
}

// TestGCUnreferencedBlobsReclaimsOrphan simulates the G2 crash window: the
// catalog is flipped to mirror_only but the bytes are left on disk. GC reclaims
// exactly those orphan bytes, never the head or a kept full revision's blob, and
// the mirror (history partition) survives.
func TestGCUnreferencedBlobsReclaimsOrphan(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)
	world, revs := evictWorldWithHistory(t, ctx, ws)
	headID := *world.HeadRevisionID

	orphan := nonHeadRevision(t, revs, headID)
	if orphan.BlobRef.IsInline() {
		t.Fatalf("orphan revision %d blob is inline; fixture must be non-inline for the delete proof", orphan.ID)
	}

	// G2 crash window: catalog flip only (bytes left on disk).
	if err := ws.store().EvictRevisionBlob(ctx, orphan.ID); err != nil {
		t.Fatalf("EvictRevisionBlob: %v", err)
	}
	if has, err := ws.blobs().Has(ctx, orphan.BlobRef); err != nil || !has {
		t.Fatalf("orphan bytes Has = (%v, %v), want present (crash left bytes on disk)", has, err)
	}
	histBefore := countBySaveID(t, ctx, ws, "save_archives", orphan.SHA256)
	if histBefore == 0 {
		t.Fatalf("history partition for orphan sha %s empty before GC", orphan.SHA256)
	}

	result, err := ws.GCUnreferencedBlobs(ctx)
	if err != nil {
		t.Fatalf("GCUnreferencedBlobs: %v", err)
	}
	if result.BytesDeleted != 1 {
		t.Errorf("BytesDeleted = %d, want 1", result.BytesDeleted)
	}
	if len(result.DeleteErrors) != 0 {
		t.Errorf("DeleteErrors = %v, want none", result.DeleteErrors)
	}

	// Orphan bytes are gone.
	if has, err := ws.blobs().Has(ctx, orphan.BlobRef); err != nil {
		t.Fatalf("Has(orphan) after GC error = %v", err)
	} else if has {
		t.Errorf("orphan bytes still present after GC; must be reclaimed")
	}

	// Head blob is STILL present (never delete a live blob).
	headRev, err := ws.store().RevisionByID(ctx, headID)
	if err != nil {
		t.Fatalf("RevisionByID(head): %v", err)
	}
	if has, err := ws.blobs().Has(ctx, headRev.BlobRef); err != nil || !has {
		t.Errorf("head blob Has = (%v, %v), want still present", has, err)
	}

	// The mirror survives: GC reclaims bytes, not the history partition.
	if got := countBySaveID(t, ctx, ws, "save_archives", orphan.SHA256); got != histBefore {
		t.Errorf("history rows for orphan sha %s = %d, want unchanged %d", orphan.SHA256, got, histBefore)
	}
}

// TestGCSkipsLiveBlob: a sha shared by a full + a mirror_only row must never be
// deleted. OrphanedBlobs must not list it and GC must leave the bytes present.
func TestGCSkipsLiveBlob(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)
	world, revs := evictWorldWithHistory(t, ctx, ws)
	headID := *world.HeadRevisionID

	rev := nonHeadRevision(t, revs, headID)

	// Seed a SECOND, still-full revision sharing rev's BlobRef (a legitimate
	// reference to the same bytes), then demote rev to mirror_only via the
	// reconcile-only MarkMirrorOnly (the G1 flip would be refused at refcount>1).
	run, err := ws.store().RecordScriptRun(ctx, revisionstore.ScriptRunInput{
		ScriptSHA256: rev.SHA256,
		Status:       "succeeded",
	})
	if err != nil {
		t.Fatalf("RecordScriptRun: %v", err)
	}
	if _, err := ws.store().RecordRevision(ctx, revisionstore.RevisionInput{
		BlobRef:     rev.BlobRef,
		ScriptRunID: run.ID,
	}); err != nil {
		t.Fatalf("RecordRevision(sharing sha): %v", err)
	}
	if err := ws.store().MarkMirrorOnly(ctx, rev.ID); err != nil {
		t.Fatalf("MarkMirrorOnly: %v", err)
	}

	// The shared sha must not be an orphan candidate.
	orphans, err := ws.store().OrphanedBlobs(ctx)
	if err != nil {
		t.Fatalf("OrphanedBlobs: %v", err)
	}
	for _, o := range orphans {
		if o.SHA256 == rev.SHA256 {
			t.Fatalf("OrphanedBlobs lists sha %s shared by a full row; must be excluded", rev.SHA256)
		}
	}

	result, err := ws.GCUnreferencedBlobs(ctx)
	if err != nil {
		t.Fatalf("GCUnreferencedBlobs: %v", err)
	}

	// The shared bytes must NOT be deleted; the full row's blob stays present.
	if has, err := ws.blobs().Has(ctx, rev.BlobRef); err != nil {
		t.Fatalf("Has(shared) error = %v", err)
	} else if !has {
		t.Fatalf("shared blob (full row still needs it) was deleted; never delete a live blob")
	}
	for _, e := range result.DeleteErrors {
		t.Errorf("unexpected DeleteError: %v", e)
	}
}

// TestGCIsIdempotent: running GC twice over the orphan reclaims it once; the
// second run reports BytesDeleted 0 and no DeleteErrors (Delete is NotFound→nil).
func TestGCIsIdempotent(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)
	world, revs := evictWorldWithHistory(t, ctx, ws)
	headID := *world.HeadRevisionID

	orphan := nonHeadRevision(t, revs, headID)
	if err := ws.store().EvictRevisionBlob(ctx, orphan.ID); err != nil {
		t.Fatalf("EvictRevisionBlob: %v", err)
	}

	first, err := ws.GCUnreferencedBlobs(ctx)
	if err != nil {
		t.Fatalf("GCUnreferencedBlobs #1: %v", err)
	}
	if first.BytesDeleted != 1 {
		t.Fatalf("first run BytesDeleted = %d, want 1", first.BytesDeleted)
	}

	second, err := ws.GCUnreferencedBlobs(ctx)
	if err != nil {
		t.Fatalf("GCUnreferencedBlobs #2: %v", err)
	}
	if second.BytesDeleted != 0 {
		t.Errorf("second run BytesDeleted = %d, want 0 (idempotent)", second.BytesDeleted)
	}
	if len(second.DeleteErrors) != 0 {
		t.Errorf("second run DeleteErrors = %v, want none", second.DeleteErrors)
	}
}

// TestGCSkipsInlineOrphan: an inline mirror_only orphan has no backing object and
// must not be counted as BytesDeleted (the IsInline branch).
func TestGCSkipsInlineOrphan(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	// A small (< 4KiB default inline threshold) blob is carried inline.
	inlineRef, err := ws.blobs().Put(ctx, []byte("tiny inline save"))
	if err != nil {
		t.Fatalf("Put(inline): %v", err)
	}
	if !inlineRef.IsInline() {
		t.Fatalf("ref is not inline; need a sub-threshold blob for the IsInline branch")
	}
	run, err := ws.store().RecordScriptRun(ctx, revisionstore.ScriptRunInput{
		ScriptSHA256: inlineRef.SHA256,
		Status:       "succeeded",
	})
	if err != nil {
		t.Fatalf("RecordScriptRun: %v", err)
	}
	rev, err := ws.store().RecordRevision(ctx, revisionstore.RevisionInput{
		BlobRef:     inlineRef,
		ScriptRunID: run.ID,
	})
	if err != nil {
		t.Fatalf("RecordRevision(inline): %v", err)
	}
	if err := ws.store().EvictRevisionBlob(ctx, rev.ID); err != nil {
		t.Fatalf("EvictRevisionBlob(inline): %v", err)
	}

	result, err := ws.GCUnreferencedBlobs(ctx)
	if err != nil {
		t.Fatalf("GCUnreferencedBlobs: %v", err)
	}
	if result.BytesDeleted != 0 {
		t.Errorf("BytesDeleted = %d, want 0 (inline orphan has no backing object)", result.BytesDeleted)
	}
	if result.Skipped < 1 {
		t.Errorf("Skipped = %d, want >= 1 (the inline orphan)", result.Skipped)
	}
}

// TestPromoteReappearedBlobRestoresMirror: a fully-evicted (mirror_only) revision
// whose bytes reappear is re-promoted to full/blob_present=1, exactly once, with
// refcount UNCHANGED (no double refcount).
func TestPromoteReappearedBlobRestoresMirror(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)
	world, revs := evictWorldWithHistory(t, ctx, ws)
	headID := *world.HeadRevisionID

	rev := nonHeadRevision(t, revs, headID)

	// Capture the original bytes while they are still present so we can re-Put them
	// to simulate a legitimate reappearance (re-ingest / re-import by hash).
	originalBytes, err := ws.blobs().Get(ctx, rev.BlobRef)
	if err != nil {
		t.Fatalf("Get(orphan bytes) before evict: %v", err)
	}

	// Fully evict: catalog flip + byte delete via the public wrapper.
	if err := ws.EvictRevisionBlob(ctx, rev.ID); err != nil {
		t.Fatalf("EvictRevisionBlob: %v", err)
	}
	if has, err := ws.blobs().Has(ctx, rev.BlobRef); err != nil {
		t.Fatalf("Has after evict error = %v", err)
	} else if has {
		t.Fatalf("bytes still present after full evict; want gone before re-put")
	}
	evicted, err := ws.store().RevisionByID(ctx, rev.ID)
	if err != nil {
		t.Fatalf("RevisionByID after evict: %v", err)
	}
	if evicted.Tier != "mirror_only" {
		t.Fatalf("evicted tier = %q, want mirror_only", evicted.Tier)
	}
	refBefore := evicted.Refcount

	// Bytes reappear by hash: re-Put the identical content.
	reput, err := ws.blobs().Put(ctx, originalBytes)
	if err != nil {
		t.Fatalf("Put(reappearing bytes): %v", err)
	}
	if reput.SHA256 != rev.BlobRef.SHA256 {
		t.Fatalf("re-put sha %s != original %s", reput.SHA256, rev.BlobRef.SHA256)
	}
	if has, err := ws.blobs().Has(ctx, rev.BlobRef); err != nil || !has {
		t.Fatalf("bytes Has after reput = (%v, %v), want present", has, err)
	}

	n, err := ws.PromoteReappearedBlob(ctx, rev.BlobRef)
	if err != nil {
		t.Fatalf("PromoteReappearedBlob: %v", err)
	}
	if n != 1 {
		t.Errorf("promoted count = %d, want 1", n)
	}
	got, err := ws.store().RevisionByID(ctx, rev.ID)
	if err != nil {
		t.Fatalf("RevisionByID after promote: %v", err)
	}
	if got.Tier != "full" || !got.BlobPresent {
		t.Errorf("promoted row = (tier=%q, present=%v), want (full, true)", got.Tier, got.BlobPresent)
	}
	if got.Refcount != refBefore {
		t.Errorf("refcount after promote = %d, want unchanged %d (no double refcount)", got.Refcount, refBefore)
	}
}

// TestPromoteReappearedBlobRefusesAbsentBytes: with bytes NOT present,
// PromoteReappearedBlob returns (0, nil) and leaves the row mirror_only.
func TestPromoteReappearedBlobRefusesAbsentBytes(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)
	world, revs := evictWorldWithHistory(t, ctx, ws)
	headID := *world.HeadRevisionID

	rev := nonHeadRevision(t, revs, headID)

	// Fully evict so the bytes are gone and the row is mirror_only.
	if err := ws.EvictRevisionBlob(ctx, rev.ID); err != nil {
		t.Fatalf("EvictRevisionBlob: %v", err)
	}
	if has, err := ws.blobs().Has(ctx, rev.BlobRef); err != nil {
		t.Fatalf("Has after evict error = %v", err)
	} else if has {
		t.Fatalf("bytes still present after full evict; want gone for the absent-bytes proof")
	}

	n, err := ws.PromoteReappearedBlob(ctx, rev.BlobRef)
	if err != nil {
		t.Fatalf("PromoteReappearedBlob: %v", err)
	}
	if n != 0 {
		t.Errorf("promoted count = %d, want 0 (bytes absent)", n)
	}
	got, err := ws.store().RevisionByID(ctx, rev.ID)
	if err != nil {
		t.Fatalf("RevisionByID: %v", err)
	}
	if got.Tier != "mirror_only" || got.BlobPresent {
		t.Errorf("row after refused promote = (tier=%q, present=%v), want (mirror_only, false)", got.Tier, got.BlobPresent)
	}
}
