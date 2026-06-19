package workspace

import (
	"context"
	"fmt"

	"github.com/asemones/bibicontrol/blobstore"
)

// ConsistencyFindingKind discriminates the cross-store invariant a finding
// reports.
type ConsistencyFindingKind int

const (
	// FindingBytesMissing: a tier='full'/blob_present=1 revision whose backing
	// blob bytes are absent. This is a genuine invariant VIOLATION — the catalog
	// claims the bytes are present but they are not. For a head it is
	// unrecoverable (no mirror→blob path); for a non-head it is the post-crash
	// state ReconcileBlobs demotes. Either way verify only REPORTS it.
	FindingBytesMissing ConsistencyFindingKind = iota
	// FindingOrphanBytesPresent: an orphan sha (every revision of it is
	// mirror_only — no blob_present=1 row) whose backing bytes are still on disk.
	// This is the EXPECTED state between an evict/crash and the next GC pass, so
	// it is reported, never failed: it is the explicit form of "correctness rides
	// on a GC pass running" (GCUnreferencedBlobs reclaims these).
	FindingOrphanBytesPresent
)

// String renders the finding kind for logs/test diagnostics.
func (k ConsistencyFindingKind) String() string {
	switch k {
	case FindingBytesMissing:
		return "bytes_missing"
	case FindingOrphanBytesPresent:
		return "orphan_bytes_present"
	default:
		return fmt.Sprintf("unknown(%d)", int(k))
	}
}

// ConsistencyFinding is one cross-store invariant observation. It carries enough
// to locate the offending revision/blob without re-querying.
type ConsistencyFinding struct {
	Kind        ConsistencyFindingKind
	RevisionID  int64
	SHA256      string
	BlobRef     blobstore.Ref
	IsHead      bool // set on FindingBytesMissing: a head miss is unrecoverable.
	Description string
}

// StorageConsistencyReport is the result of a read-only VerifyStorageConsistency
// doctor pass. It carries per-category counts and the typed findings; it is a
// value struct returned to the caller (no mutation, no repair).
//
// BytesMissing is the count of FindingBytesMissing findings — a real invariant
// violation (catalog says full/present, bytes are gone). OrphanBytesPresent is
// the count of FindingOrphanBytesPresent findings — the EXPECTED pre-GC orphan
// state, reported not failed.
//
// RefcountAnomalies covers the refcount floor invariant. The schema CHECK
// (refcount >= 0) plus the in-tx self-ref (incBlobRefTx, store.go) already make a
// blob_present=1 row at refcount < 1 structurally impossible for committed state;
// this field counts any such anomaly verify still observes on the
// FullRevisions rows it already scans (it adds NO new SQL), so a regression in
// that invariant is explicitly checkable rather than implicit.
type StorageConsistencyReport struct {
	BytesMissing       int
	OrphanBytesPresent int
	RefcountAnomalies  int
	Findings           []ConsistencyFinding
}

// HasViolations reports whether the report contains any genuine invariant
// violation (bytes-missing or a refcount anomaly). OrphanBytesPresent is the
// expected pre-GC state and is NOT a violation, so it does not count here.
func (r StorageConsistencyReport) HasViolations() bool {
	return r.BytesMissing > 0 || r.RefcountAnomalies > 0
}

// VerifyStorageConsistency is the read-only cross-store doctor pass for the §10
// bullet-6 invariant: the DuckDB save_id partitions and the revisionstore sha256
// rows have no joint constraint enforced by the schema, so this verifier asserts
// the cross-store linkage holds. It issues ONLY SELECTs (via the existing store
// queries) and blobstore Has stats — it writes NOTHING. A found inconsistency is
// a reported finding in the returned report, NEVER a repair (repair is
// reconcile's/GC's job); the error return is non-nil ONLY on a store/blobstore
// I/O failure, mirroring the EvictResult.DeleteErrors pattern.
//
// It checks three things, reusing the queries ReconcileBlobs/GC already run:
//
//   - Full-but-missing: every tier='full'/blob_present=1 revision's bytes must be
//     present. A miss is a FindingBytesMissing (invariant VIOLATION). Head vs
//     non-head is distinguished via IsRevisionHead (a head miss is unrecoverable
//     per ErrHeadBlobMissing).
//   - Refcount floor: a full/present row must have refcount >= 1 (it self-refs in
//     the record tx). A refcount < 1 here is a self-ref bug — counted into
//     RefcountAnomalies using the Refcount already carried on the FullRevisions
//     rows (no extra SQL).
//   - Orphan-bytes-present: every orphan sha (all-mirror_only) whose non-inline
//     bytes are still on disk is a reclaimable orphan — FindingOrphanBytesPresent
//     (EXPECTED pre-GC, reported not failed).
//
// Lock discipline: it holds w.mu for the whole pass (consistent read of the
// catalog + blobstore against the single writer), the same whole-body-lock shape
// ReconcileBlobs/GCUnreferencedBlobs use. Every helper it calls (store reads,
// blobs().Has) is lock-agnostic and never re-takes w.mu (non-reentrant).
func (w *Workspace) VerifyStorageConsistency(ctx context.Context) (StorageConsistencyReport, error) {
	if w == nil {
		return StorageConsistencyReport{}, fmt.Errorf("workspace: VerifyStorageConsistency on nil workspace")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	var report StorageConsistencyReport

	// 1. Full-but-missing + refcount floor. Iterate the same set ReconcileBlobs
	// Direction 1 scans.
	fulls, err := w.store().FullRevisions(ctx)
	if err != nil {
		return report, fmt.Errorf("workspace: verify: list full revisions: %w", err)
	}
	for _, rev := range fulls {
		// Refcount floor: a full/present revision self-refs its blob in the record
		// tx, so refcount must be >= 1. A value below the floor is a self-ref bug
		// the schema CHECK (>= 0) alone would not catch.
		if rev.Refcount < 1 {
			report.RefcountAnomalies++
			report.Findings = append(report.Findings, ConsistencyFinding{
				Kind:        FindingBytesMissing, // closest typed bucket; described explicitly below
				RevisionID:  rev.ID,
				SHA256:      rev.SHA256,
				BlobRef:     rev.BlobRef,
				Description: fmt.Sprintf("full/present revision %d has refcount %d (< 1): self-ref invariant violated", rev.ID, rev.Refcount),
			})
		}

		has, err := w.blobs().Has(ctx, rev.BlobRef)
		if err != nil {
			return report, fmt.Errorf("workspace: verify: stat blob for revision %d: %w", rev.ID, err)
		}
		if has {
			continue
		}
		// Bytes are gone while the catalog says full/present: an invariant
		// violation. Distinguish head (unrecoverable) from non-head (reconcile
		// demotes) for the operator.
		isHead, err := w.store().IsRevisionHead(ctx, rev.ID)
		if err != nil {
			return report, fmt.Errorf("workspace: verify: head check for revision %d: %w", rev.ID, err)
		}
		report.BytesMissing++
		desc := fmt.Sprintf("full/present revision %d (sha %s) is missing its blob bytes", rev.ID, rev.BlobRef.SHA256)
		if isHead {
			desc += " (HEAD — unrecoverable)"
		}
		report.Findings = append(report.Findings, ConsistencyFinding{
			Kind:        FindingBytesMissing,
			RevisionID:  rev.ID,
			SHA256:      rev.SHA256,
			BlobRef:     rev.BlobRef,
			IsHead:      isHead,
			Description: desc,
		})
	}

	// 2. Orphan-bytes-present. Iterate the same orphan set GCUnreferencedBlobs
	// consumes. A non-inline orphan sha whose bytes are still on disk is a
	// reclaimable orphan — EXPECTED between an evict/crash and the next GC pass, so
	// it is REPORTED not failed (the explicit form of "correctness rides on GC").
	orphans, err := w.store().OrphanedBlobs(ctx)
	if err != nil {
		return report, fmt.Errorf("workspace: verify: list orphaned blobs: %w", err)
	}
	for _, rev := range orphans {
		// Inline refs carry their bytes in the ref itself — there is no backing
		// object to be an orphan on disk. Skip them.
		if rev.BlobRef.IsInline() {
			continue
		}
		has, err := w.blobs().Has(ctx, rev.BlobRef)
		if err != nil {
			return report, fmt.Errorf("workspace: verify: stat orphan blob %s: %w", rev.BlobRef.SHA256, err)
		}
		if !has {
			// Orphan sha with no bytes on disk: fully reclaimed, nothing to report.
			continue
		}
		report.OrphanBytesPresent++
		report.Findings = append(report.Findings, ConsistencyFinding{
			Kind:        FindingOrphanBytesPresent,
			RevisionID:  rev.ID,
			SHA256:      rev.SHA256,
			BlobRef:     rev.BlobRef,
			Description: fmt.Sprintf("orphan sha %s has bytes present on disk (reclaimable by GC; expected pre-GC)", rev.SHA256),
		})
	}

	return report, nil
}
