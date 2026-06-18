package workspace

import (
	"context"
	"errors"
	"testing"

	"github.com/asemones/bibicontrol/revisionstore"
	"github.com/asemones/bibicontrol/script/thebibites"
)

// commitWorldThrice imports fixtureSmall and commits it twice so the world has
// three revisions (import + 2 commits) with the head pointing at the last.
// Returns the world and its revisions in lineage order.
func evictWorldWithHistory(t *testing.T, ctx context.Context, ws *Workspace) (revisionstore.World, []revisionstore.Revision) {
	t.Helper()
	world, err := ws.AddWorld(ctx, fixturePath(t, fixtureSmall), "world-a")
	if err != nil {
		t.Fatalf("AddWorld: %v", err)
	}
	entry := firstBibiteEntryName(t, ctx, ws, world.ID)
	if _, err := ws.CommitWorld(ctx, world.ID, setBibiteEnergy(entry, 111.0), thebibites.RunOptions{}); err != nil {
		t.Fatalf("CommitWorld #1: %v", err)
	}
	if _, err := ws.CommitWorld(ctx, world.ID, setBibiteEnergy(entry, 222.0), thebibites.RunOptions{}); err != nil {
		t.Fatalf("CommitWorld #2: %v", err)
	}
	got, err := ws.store().GetWorld(ctx, world.ID)
	if err != nil {
		t.Fatalf("GetWorld: %v", err)
	}
	revs, err := ws.store().RevisionsForWorld(ctx, got.ID)
	if err != nil {
		t.Fatalf("RevisionsForWorld: %v", err)
	}
	if len(revs) != 3 {
		t.Fatalf("want 3 revisions (import + 2 commits), got %d", len(revs))
	}
	return got, revs
}

// TestEvictWorldHistoryKeepsMirrorDeletesBlob: the headline op. Evicting all but
// the last revision flips the older revisions to mirror_only/blob_present=0,
// deletes their blobstore objects, leaves the head untouched, and — critically —
// leaves their DuckDB history partitions fully queryable (keep the mirror).
func TestEvictWorldHistoryKeepsMirrorDeletesBlob(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)
	world, revs := evictWorldWithHistory(t, ctx, ws)

	headID := *world.HeadRevisionID
	// The non-inline assumption: real saves exceed the 4KiB inline threshold, so
	// they have deletable backing objects.
	for _, rev := range revs {
		if rev.BlobRef.IsInline() {
			t.Fatalf("revision %d blob is inline; fixture must be non-inline for the delete proof", rev.ID)
		}
	}

	// Record each evicted revision's history row count BEFORE eviction so we can
	// prove the mirror survives.
	histBefore := map[string]int64{}
	for _, rev := range revs {
		if rev.ID == headID {
			continue
		}
		histBefore[rev.SHA256] = countBySaveID(t, ctx, ws, "save_archives", rev.SHA256)
		if histBefore[rev.SHA256] == 0 {
			t.Fatalf("history partition for revision %d (sha %s) empty before evict", rev.ID, rev.SHA256)
		}
	}

	result, err := ws.EvictWorldHistory(ctx, world.ID, KeepLastN(1))
	if err != nil {
		t.Fatalf("EvictWorldHistory: %v", err)
	}
	if result.Candidates != 2 {
		t.Errorf("Candidates = %d, want 2 (3 revs, keep last 1)", result.Candidates)
	}
	if result.Demoted != 2 {
		t.Errorf("Demoted = %d, want 2", result.Demoted)
	}
	if result.BytesDeleted != 2 {
		t.Errorf("BytesDeleted = %d, want 2", result.BytesDeleted)
	}
	if len(result.DeleteErrors) != 0 {
		t.Errorf("DeleteErrors = %v, want none", result.DeleteErrors)
	}

	for _, rev := range revs {
		got, err := ws.store().RevisionByID(ctx, rev.ID)
		if err != nil {
			t.Fatalf("RevisionByID(%d): %v", rev.ID, err)
		}
		if rev.ID == headID {
			// Head untouched: still full, still present, blob loadable.
			if got.Tier != "full" || !got.BlobPresent {
				t.Errorf("head revision %d = (tier=%q, present=%v), want (full, true)", rev.ID, got.Tier, got.BlobPresent)
			}
			if has, err := ws.blobs().Has(ctx, got.BlobRef); err != nil || !has {
				t.Errorf("head blob Has = (%v, %v), want (true, nil)", has, err)
			}
			continue
		}
		// Evicted: mirror_only, blob_present=0, bytes gone.
		if got.Tier != "mirror_only" || got.BlobPresent {
			t.Errorf("evicted revision %d = (tier=%q, present=%v), want (mirror_only, false)", rev.ID, got.Tier, got.BlobPresent)
		}
		if has, err := ws.blobs().Has(ctx, got.BlobRef); err != nil {
			t.Errorf("evicted blob Has error = %v", err)
		} else if has {
			t.Errorf("evicted revision %d blob still present; bytes must be deleted", rev.ID)
		}
		// Keep the mirror: history partition still fully queryable.
		if got := countBySaveID(t, ctx, ws, "save_archives", rev.SHA256); got != histBefore[rev.SHA256] {
			t.Errorf("history rows for evicted revision %d (sha %s) = %d, want unchanged %d",
				rev.ID, rev.SHA256, got, histBefore[rev.SHA256])
		}
	}
}

// TestEvictRefusesHead: EvictWorldHistory never demotes the head; KeepLastN(0)
// makes every revision a candidate but the head is double-gated out. The head
// blob stays present and the world stays loadable.
func TestEvictRefusesHead(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)
	world, revs := evictWorldWithHistory(t, ctx, ws)
	headID := *world.HeadRevisionID

	result, err := ws.EvictWorldHistory(ctx, world.ID, KeepLastN(0))
	if err != nil {
		t.Fatalf("EvictWorldHistory: %v", err)
	}
	// KeepLastN(0) selects all non-head revisions (head excluded by the policy),
	// so the head is never even a candidate; the 2 non-head revisions evict.
	if result.Candidates != 2 {
		t.Errorf("Candidates = %d, want 2 (head excluded by policy)", result.Candidates)
	}
	if result.RefusedHead != 0 {
		t.Errorf("RefusedHead = %d via policy path; head should not be selected at all", result.RefusedHead)
	}

	headRev, err := ws.store().RevisionByID(ctx, headID)
	if err != nil {
		t.Fatalf("RevisionByID(head): %v", err)
	}
	if headRev.Tier != "full" || !headRev.BlobPresent {
		t.Errorf("head = (tier=%q, present=%v), want (full, true)", headRev.Tier, headRev.BlobPresent)
	}
	if has, err := ws.blobs().Has(ctx, headRev.BlobRef); err != nil || !has {
		t.Errorf("head blob Has = (%v, %v), want present", has, err)
	}
	// World still loadable (head blob intact).
	if _, err := ws.OpenWorld(ctx, world.ID); err != nil {
		t.Errorf("OpenWorld after evict: %v, want loadable head", err)
	}

	// Direct head-eviction must also be refused by the per-revision lever (G1
	// gate), proving the second gate.
	if err := ws.EvictRevisionBlob(ctx, headID); !errors.Is(err, revisionstore.ErrRevisionIsHead) {
		t.Errorf("EvictRevisionBlob(head) = %v, want ErrRevisionIsHead", err)
	}
	_ = revs
}

// TestEvictRefusesSharedBlob: a non-head revision whose sha256 has refcount > 1
// is refused; the blob is NEVER deleted. The load-bearing "never delete a live
// blob" proof.
func TestEvictRefusesSharedBlob(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)
	world, revs := evictWorldWithHistory(t, ctx, ws)
	headID := *world.HeadRevisionID

	// Pick a non-head revision and bump its blob refcount above 1 (simulating a
	// dedup/clone-shared blob another reference still needs).
	var sharedRev revisionstore.Revision
	for _, rev := range revs {
		if rev.ID != headID {
			sharedRev = rev
			break
		}
	}
	if sharedRev.ID == 0 {
		t.Fatal("no non-head revision found")
	}
	if _, err := ws.store().IncBlobRef(ctx, sharedRev.SHA256); err != nil {
		t.Fatalf("IncBlobRef: %v", err)
	}

	result, err := ws.EvictWorldHistory(ctx, world.ID, KeepLastN(1))
	if err != nil {
		t.Fatalf("EvictWorldHistory: %v", err)
	}
	if result.RefusedShared != 1 {
		t.Errorf("RefusedShared = %d, want 1", result.RefusedShared)
	}

	// The shared blob must NOT be deleted.
	if has, err := ws.blobs().Has(ctx, sharedRev.BlobRef); err != nil {
		t.Fatalf("Has(shared) error = %v", err)
	} else if !has {
		t.Fatalf("shared blob (refcount>1) was deleted; never delete a live blob")
	}
	// And its catalog row must still be full/present (not demoted).
	got, err := ws.store().RevisionByID(ctx, sharedRev.ID)
	if err != nil {
		t.Fatalf("RevisionByID(shared): %v", err)
	}
	if got.Tier != "full" || !got.BlobPresent {
		t.Errorf("refused shared revision = (tier=%q, present=%v), want (full, true)", got.Tier, got.BlobPresent)
	}

	// Direct per-revision eviction is also refused.
	if err := ws.EvictRevisionBlob(ctx, sharedRev.ID); !errors.Is(err, revisionstore.ErrBlobStillReferenced) {
		t.Errorf("EvictRevisionBlob(shared) = %v, want ErrBlobStillReferenced", err)
	}
}

// TestEvictThenLoadIsNotRematerializable: after evicting a non-head revision its
// bytes are gone with NO mirror→blob fallback. A direct Get on the evicted ref
// errors (the structural non-rematerialization property).
func TestEvictThenLoadIsNotRematerializable(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)
	world, revs := evictWorldWithHistory(t, ctx, ws)
	headID := *world.HeadRevisionID

	var evictRev revisionstore.Revision
	for _, rev := range revs {
		if rev.ID != headID {
			evictRev = rev
			break
		}
	}

	if err := ws.EvictRevisionBlob(ctx, evictRev.ID); err != nil {
		t.Fatalf("EvictRevisionBlob: %v", err)
	}

	// The bytes are gone: Get must fail (no rematerialization from the mirror).
	if _, err := ws.blobs().Get(ctx, evictRev.BlobRef); err == nil {
		t.Fatalf("Get(evicted ref) succeeded; expected the bytes to be gone (no mirror→blob path)")
	}
	if has, err := ws.blobs().Has(ctx, evictRev.BlobRef); err != nil || has {
		t.Fatalf("Has(evicted ref) = (%v, %v), want (false, nil)", has, err)
	}

	// Force the world head to the evicted revision and confirm Load refuses it
	// with the blob-present guard (working_set.go).
	if err := ws.store().SetWorldHead(ctx, world.ID, evictRev.ID, nil); err != nil {
		t.Fatalf("SetWorldHead(evicted): %v", err)
	}
	_ = ws.Unload(world.ID)
	_, err := ws.Load(ctx, world.ID)
	if !errors.Is(err, ErrNotRematerializable) {
		t.Fatalf("Load of mirror_only head: err = %v, want ErrNotRematerializable", err)
	}
}

// TestReconcileBlobsAfterCrashMidEvict: the crash-safety proof across the three
// windows.
func TestReconcileBlobsAfterCrashMidEvict(t *testing.T) {
	ctx := context.Background()

	// (a) Catalog-flip-committed-but-bytes-still-present: reconcile leaves the
	// row mirror_only, and the now-unreferenced bytes are reported by
	// UnreferencedBlobs (G3's to sweep) — and no live/head blob was deleted.
	t.Run("flipped_but_bytes_present", func(t *testing.T) {
		ws := newWorkspace(t, ctx)
		world, revs := evictWorldWithHistory(t, ctx, ws)
		headID := *world.HeadRevisionID

		var rev revisionstore.Revision
		for _, r := range revs {
			if r.ID != headID {
				rev = r
				break
			}
		}
		// Simulate the crash window: G1 catalog flip committed, bytes NOT deleted.
		if err := ws.store().EvictRevisionBlob(ctx, rev.ID); err != nil {
			t.Fatalf("EvictRevisionBlob (catalog flip only): %v", err)
		}
		// Bytes still present on disk.
		if has, err := ws.blobs().Has(ctx, rev.BlobRef); err != nil || !has {
			t.Fatalf("Has(flipped, not deleted) = (%v, %v), want present", has, err)
		}

		res, err := ws.ReconcileBlobs(ctx)
		if err != nil {
			t.Fatalf("ReconcileBlobs: %v", err)
		}
		if res.Promoted != 0 {
			t.Errorf("Promoted = %d, want 0 (must NOT resurrect an evicted-but-present orphan)", res.Promoted)
		}

		// Row stays mirror_only.
		got, err := ws.store().RevisionByID(ctx, rev.ID)
		if err != nil {
			t.Fatalf("RevisionByID: %v", err)
		}
		if got.Tier != "mirror_only" || got.BlobPresent {
			t.Errorf("after reconcile row = (tier=%q, present=%v), want (mirror_only, false)", got.Tier, got.BlobPresent)
		}

		// The bytes are now a reclaimable orphan for G3 to sweep: they remain on
		// disk, but NO revision of that sha256 is blob_present=1 anymore (the only
		// revision with those bytes was just demoted). That "present-on-disk but
		// no full revision needs it" state is exactly what G3/reconcile treat as
		// reclaimable — never as a live blob. (refcount stays 1 here: G1's evict
		// flips tier only, so this orphan is detected by the blob_present scan,
		// not by the refcount-0 UnreferencedBlobs list.)
		if has, err := ws.blobs().Has(ctx, rev.BlobRef); err != nil || !has {
			t.Errorf("orphan bytes Has = (%v, %v), want still present (G3 sweeps, reconcile must not)", has, err)
		}
		sameSha, err := ws.store().RevisionsBySHA256(ctx, rev.SHA256)
		if err != nil {
			t.Fatalf("RevisionsBySHA256: %v", err)
		}
		for _, r := range sameSha {
			if r.BlobPresent {
				t.Errorf("revision %d still blob_present for evicted sha %s; bytes are NOT a reclaimable orphan", r.ID, rev.SHA256)
			}
		}

		// No live/head blob deleted: head still present.
		headRev, err := ws.store().RevisionByID(ctx, headID)
		if err != nil {
			t.Fatalf("RevisionByID(head): %v", err)
		}
		if has, err := ws.blobs().Has(ctx, headRev.BlobRef); err != nil || !has {
			t.Errorf("head blob Has = (%v, %v), want present (reconcile must not touch live blobs)", has, err)
		}
	})

	// (b) Full-but-bytes-missing non-head row: reconcile demotes to mirror_only.
	t.Run("full_but_missing_nonhead", func(t *testing.T) {
		ws := newWorkspace(t, ctx)
		world, revs := evictWorldWithHistory(t, ctx, ws)
		headID := *world.HeadRevisionID

		var rev revisionstore.Revision
		for _, r := range revs {
			if r.ID != headID {
				rev = r
				break
			}
		}
		// Delete the object out-of-band while the catalog still says full/present.
		if err := ws.blobs().Delete(ctx, rev.BlobRef); err != nil {
			t.Fatalf("out-of-band Delete: %v", err)
		}

		res, err := ws.ReconcileBlobs(ctx)
		if err != nil {
			t.Fatalf("ReconcileBlobs: %v", err)
		}
		if res.Demoted != 1 {
			t.Errorf("Demoted = %d, want 1", res.Demoted)
		}
		got, err := ws.store().RevisionByID(ctx, rev.ID)
		if err != nil {
			t.Fatalf("RevisionByID: %v", err)
		}
		if got.Tier != "mirror_only" || got.BlobPresent {
			t.Errorf("after reconcile row = (tier=%q, present=%v), want (mirror_only, false)", got.Tier, got.BlobPresent)
		}
	})

	// (c) Full-but-bytes-missing HEAD: reconcile fails loud (unrecoverable).
	t.Run("full_but_missing_head", func(t *testing.T) {
		ws := newWorkspace(t, ctx)
		world, _ := evictWorldWithHistory(t, ctx, ws)
		headID := *world.HeadRevisionID
		headRev, err := ws.store().RevisionByID(ctx, headID)
		if err != nil {
			t.Fatalf("RevisionByID(head): %v", err)
		}
		// Delete the head's bytes out-of-band: corruption.
		if err := ws.blobs().Delete(ctx, headRev.BlobRef); err != nil {
			t.Fatalf("out-of-band Delete(head): %v", err)
		}

		if _, err := ws.ReconcileBlobs(ctx); !errors.Is(err, ErrHeadBlobMissing) {
			t.Fatalf("ReconcileBlobs = %v, want ErrHeadBlobMissing (fail loud, never demote a head)", err)
		}
		// The head row must NOT have been silently demoted.
		got, err := ws.store().RevisionByID(ctx, headID)
		if err != nil {
			t.Fatalf("RevisionByID(head) after reconcile: %v", err)
		}
		if got.Tier != "full" {
			t.Errorf("head tier = %q after failed reconcile, want unchanged 'full' (no silent demote)", got.Tier)
		}
	})
}

// TestReconcilePromotesReappearedReferencedBlob: a mirror_only row whose bytes
// reappear AND are still referenced by a full revision of the same sha256 is
// re-promoted (the narrow legitimate-reappearance repair).
func TestReconcilePromotesReappearedReferencedBlob(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)
	world, revs := evictWorldWithHistory(t, ctx, ws)
	headID := *world.HeadRevisionID

	var rev revisionstore.Revision
	for _, r := range revs {
		if r.ID != headID {
			rev = r
			break
		}
	}

	// Make a SECOND, still-full revision share rev's sha256 (a legitimate
	// reference to the same bytes). RecordRevision with the same BlobRef seeds a
	// new full/present row sharing the hash, so the bytes are genuinely needed.
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

	// Now demote rev to mirror_only via the G1 flip would be refused (refcount>1),
	// so use the reconcile-only MarkMirrorOnly to set up the drift: a mirror_only
	// row whose bytes are present and still referenced by the full sibling.
	if err := ws.store().MarkMirrorOnly(ctx, rev.ID); err != nil {
		t.Fatalf("MarkMirrorOnly: %v", err)
	}
	// Bytes are still present (the full sibling needs them; nobody deleted them).
	if has, err := ws.blobs().Has(ctx, rev.BlobRef); err != nil || !has {
		t.Fatalf("Has = (%v, %v), want present", has, err)
	}

	res, err := ws.ReconcileBlobs(ctx)
	if err != nil {
		t.Fatalf("ReconcileBlobs: %v", err)
	}
	if res.Promoted != 1 {
		t.Errorf("Promoted = %d, want 1 (reappeared+referenced row re-promoted)", res.Promoted)
	}
	got, err := ws.store().RevisionByID(ctx, rev.ID)
	if err != nil {
		t.Fatalf("RevisionByID: %v", err)
	}
	if got.Tier != "full" || !got.BlobPresent {
		t.Errorf("re-promoted row = (tier=%q, present=%v), want (full, true)", got.Tier, got.BlobPresent)
	}
}

// TestEvictWorldHistoryUnknownWorld errors loudly on a missing world.
func TestEvictWorldHistoryUnknownWorld(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)
	if _, err := ws.EvictWorldHistory(ctx, "no-such-world", KeepLastN(1)); err == nil {
		t.Fatalf("EvictWorldHistory(unknown world) = nil, want error")
	}
}
