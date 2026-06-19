package workspace

import (
	"context"
	"testing"

	"github.com/asemones/bibicontrol/revisionstore"
)

// TestVerifyClean proves a freshly-imported world's store passes the doctor
// pass with zero anomalies and zero findings.
func TestVerifyClean(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	if _, err := ws.AddWorld(ctx, fixturePath(t, fixtureSmall), "world-a"); err != nil {
		t.Fatalf("AddWorld: %v", err)
	}

	report, err := ws.VerifyStorageConsistency(ctx)
	if err != nil {
		t.Fatalf("VerifyStorageConsistency: %v", err)
	}
	if report.BytesMissing != 0 {
		t.Errorf("BytesMissing = %d, want 0", report.BytesMissing)
	}
	if report.OrphanBytesPresent != 0 {
		t.Errorf("OrphanBytesPresent = %d, want 0", report.OrphanBytesPresent)
	}
	if report.RefcountAnomalies != 0 {
		t.Errorf("RefcountAnomalies = %d, want 0", report.RefcountAnomalies)
	}
	if len(report.Findings) != 0 {
		t.Errorf("Findings = %d, want 0: %+v", len(report.Findings), report.Findings)
	}
	if report.HasViolations() {
		t.Errorf("HasViolations() = true on a clean store, want false")
	}
}

// TestVerifyReportsOrphanBytesPresent proves the explicit form of "correctness
// rides on a GC pass running": after a non-head revision is evicted WITHOUT
// running GC (G2 leaves the bytes on disk), verify reports the orphan bytes with
// a NIL error (expected pre-GC state, reported not failed). After GC reclaims the
// bytes, verify is clean again.
func TestVerifyReportsOrphanBytesPresent(t *testing.T) {
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
	if rev.BlobRef.IsInline() {
		t.Fatalf("revision %d blob is inline; fixture must be non-inline for the orphan-bytes proof", rev.ID)
	}

	// Simulate the crash window: G1 catalog flip committed, bytes NOT deleted (no
	// GC). The row is now mirror_only but its bytes remain on disk — the orphan
	// pre-GC state.
	if err := ws.store().EvictRevisionBlob(ctx, rev.ID); err != nil {
		t.Fatalf("EvictRevisionBlob (catalog flip only): %v", err)
	}
	if has, err := ws.blobs().Has(ctx, rev.BlobRef); err != nil || !has {
		t.Fatalf("orphan bytes Has = (%v, %v), want present (pre-GC)", has, err)
	}

	report, err := ws.VerifyStorageConsistency(ctx)
	if err != nil {
		t.Fatalf("VerifyStorageConsistency returned error, want nil (orphan is reported, not failed): %v", err)
	}
	if report.OrphanBytesPresent < 1 {
		t.Errorf("OrphanBytesPresent = %d, want >= 1", report.OrphanBytesPresent)
	}
	if report.BytesMissing != 0 {
		t.Errorf("BytesMissing = %d, want 0 (orphan-present is not a missing-bytes violation)", report.BytesMissing)
	}
	if report.HasViolations() {
		t.Errorf("HasViolations() = true for a pre-GC orphan, want false (orphan is expected, not a violation)")
	}
	// The finding must be typed as an orphan, not a violation.
	var sawOrphan bool
	for _, f := range report.Findings {
		if f.Kind == FindingOrphanBytesPresent && f.SHA256 == rev.SHA256 {
			sawOrphan = true
		}
	}
	if !sawOrphan {
		t.Errorf("no FindingOrphanBytesPresent for sha %s in %+v", rev.SHA256, report.Findings)
	}

	// After GC reclaims the orphan bytes, verify is clean again.
	if _, err := ws.GCUnreferencedBlobs(ctx); err != nil {
		t.Fatalf("GCUnreferencedBlobs: %v", err)
	}
	clean, err := ws.VerifyStorageConsistency(ctx)
	if err != nil {
		t.Fatalf("VerifyStorageConsistency after GC: %v", err)
	}
	if clean.OrphanBytesPresent != 0 {
		t.Errorf("OrphanBytesPresent after GC = %d, want 0", clean.OrphanBytesPresent)
	}
	if clean.HasViolations() {
		t.Errorf("HasViolations() after GC = true, want false")
	}
}

// TestVerifyReportsFullButMissing proves verify flags the genuine invariant
// violation: a tier='full'/blob_present=1 revision whose bytes were deleted
// out-of-band (no reconcile) is recorded as a BytesMissing finding with a NIL
// error (the finding lives in the report; verify only errors on I/O failure).
func TestVerifyReportsFullButMissing(t *testing.T) {
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
	// Delete the object out-of-band while the catalog still says full/present — the
	// invariant-violating state reconcile would repair, but verify only reports.
	if err := ws.blobs().Delete(ctx, rev.BlobRef); err != nil {
		t.Fatalf("out-of-band Delete: %v", err)
	}

	report, err := ws.VerifyStorageConsistency(ctx)
	if err != nil {
		t.Fatalf("VerifyStorageConsistency returned error, want nil (finding lives in the report): %v", err)
	}
	if report.BytesMissing < 1 {
		t.Errorf("BytesMissing = %d, want >= 1", report.BytesMissing)
	}
	if !report.HasViolations() {
		t.Errorf("HasViolations() = false, want true (full-but-missing is a real violation)")
	}
	var sawMissing bool
	for _, f := range report.Findings {
		if f.Kind == FindingBytesMissing && f.RevisionID == rev.ID {
			sawMissing = true
			if f.IsHead {
				t.Errorf("non-head revision %d flagged as head", rev.ID)
			}
		}
	}
	if !sawMissing {
		t.Errorf("no FindingBytesMissing for revision %d in %+v", rev.ID, report.Findings)
	}

	// Verify is READ-ONLY: the row must still be full/present (no repair). Reconcile
	// — not verify — is what demotes it.
	got, err := ws.store().RevisionByID(ctx, rev.ID)
	if err != nil {
		t.Fatalf("RevisionByID: %v", err)
	}
	if got.Tier != "full" || !got.BlobPresent {
		t.Errorf("after verify row = (tier=%q, present=%v), want unchanged (full, true) — verify must not mutate", got.Tier, got.BlobPresent)
	}
}

// TestVerifyFlagsMissingHead proves verify distinguishes an unrecoverable head
// miss (IsHead=true) from a recoverable non-head miss, and still returns a nil
// error (it reports, never fails or repairs).
func TestVerifyFlagsMissingHead(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)
	world, _ := evictWorldWithHistory(t, ctx, ws)
	headID := *world.HeadRevisionID
	headRev, err := ws.store().RevisionByID(ctx, headID)
	if err != nil {
		t.Fatalf("RevisionByID(head): %v", err)
	}
	if err := ws.blobs().Delete(ctx, headRev.BlobRef); err != nil {
		t.Fatalf("out-of-band Delete(head): %v", err)
	}

	report, err := ws.VerifyStorageConsistency(ctx)
	if err != nil {
		t.Fatalf("VerifyStorageConsistency returned error, want nil even for a head miss: %v", err)
	}
	var sawHeadMiss bool
	for _, f := range report.Findings {
		if f.Kind == FindingBytesMissing && f.RevisionID == headID {
			sawHeadMiss = true
			if !f.IsHead {
				t.Errorf("head revision %d finding IsHead=false, want true (unrecoverable)", headID)
			}
		}
	}
	if !sawHeadMiss {
		t.Errorf("no FindingBytesMissing for head revision %d in %+v", headID, report.Findings)
	}
	if !report.HasViolations() {
		t.Errorf("HasViolations() = false, want true (head miss is a violation)")
	}

	// Read-only: the head is NOT demoted by verify.
	got, err := ws.store().RevisionByID(ctx, headID)
	if err != nil {
		t.Fatalf("RevisionByID(head) after verify: %v", err)
	}
	if got.Tier != "full" {
		t.Errorf("head tier = %q after verify, want unchanged 'full'", got.Tier)
	}
}
