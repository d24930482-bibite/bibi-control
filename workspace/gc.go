package workspace

import (
	"context"
	"fmt"

	"github.com/asemones/bibicontrol/blobstore"
)

// GCResult reports the outcome of a GCUnreferencedBlobs sweep so callers and
// tests can assert without re-querying. It mirrors EvictResult's shape.
type GCResult struct {
	// Candidates is the number of orphan shas OrphanedBlobs reported.
	Candidates int
	// BytesDeleted is the number of orphan blobstore objects successfully deleted.
	BytesDeleted int
	// Skipped is the number of candidates not deleted: inline refs (no backing
	// object) and shas a concurrent record/promote re-introduced a full reference
	// for between the catalog query and the under-lock re-check.
	Skipped int
	// DeleteErrors collects soft (non-fatal) blobstore delete failures. The
	// catalog is already in the terminal mirror_only state, so a failed byte
	// delete leaves a still-reclaimable orphan a re-run will sweep. Surfaced so
	// the caller knows, never aborts the sweep.
	DeleteErrors []error
}

// GCUnreferencedBlobs is Track G's byte-reclamation op: it deletes the on-disk
// blob objects whose sha256 is referenced by NO blob_present=1 (full) revision —
// the orphan G2 leaves when it crashes between the catalog commit and the byte
// delete (or after ReconcileBlobs demotes the last full row sharing a sha). It is
// a manual/host-triggered admin op, not automatic, and writes NO catalog state:
// the catalog is already in the terminal mirror_only state when bytes are an
// orphan, so GC is byte-delete-only.
//
// Crash-safe and idempotent: it only ever deletes bytes the committed catalog
// already says are not needed, and blobstore.Delete is idempotent (NotFound→nil),
// so a crash mid-sweep and a re-run are both safe (a re-run is a no-op).
//
// "Never delete a live blob" is double-gated, mirroring G2: the OrphanedBlobs SQL
// excludes any sha with a blob_present=1 row, AND each candidate is re-verified
// under w.mu via shaHasFullReference immediately before delete (a concurrent
// record/promote may have re-introduced a full reference). Inline refs have no
// backing object and are skipped (not counted as bytes reclaimed). A blobstore
// delete error is soft (collected in DeleteErrors, sweep continues); a real
// store/DB error from OrphanedBlobs or shaHasFullReference aborts.
func (w *Workspace) GCUnreferencedBlobs(ctx context.Context) (GCResult, error) {
	if w == nil {
		return GCResult{}, fmt.Errorf("workspace: GCUnreferencedBlobs on nil workspace")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// Single-writer discipline: hold w.mu for the whole sweep, the same
	// whole-body-lock shape EvictWorldHistory/ReconcileBlobs use. shaHasFullReference
	// is lock-agnostic and never re-takes w.mu (non-reentrant).
	w.mu.Lock()
	defer w.mu.Unlock()

	orphans, err := w.store().OrphanedBlobs(ctx)
	if err != nil {
		return GCResult{}, fmt.Errorf("workspace: list orphaned blobs: %w", err)
	}

	var result GCResult
	result.Candidates = len(orphans)
	for _, rev := range orphans {
		// Inline refs carry their bytes in the ref itself: no backing object to
		// delete, and they must NOT be counted as bytes reclaimed.
		if rev.BlobRef.IsInline() {
			result.Skipped++
			continue
		}

		// Re-verify the orphan invariant under the lock immediately before the
		// delete. If a concurrent record/promote re-introduced a full reference,
		// SKIP — this is the load-bearing "never delete a live blob" guard,
		// defense-in-depth over the SQL filter.
		referenced, err := w.shaHasFullReference(ctx, rev.SHA256)
		if err != nil {
			return result, err
		}
		if referenced {
			result.Skipped++
			continue
		}

		// Only count bytes we actually reclaim. An orphan row stays mirror_only
		// after its bytes are gone, so it keeps appearing in OrphanedBlobs; a
		// re-run must be a true no-op (BytesDeleted == 0), not re-count the
		// already-absent object's idempotent Delete. A real stat error aborts.
		has, err := w.blobs().Has(ctx, rev.BlobRef)
		if err != nil {
			return result, fmt.Errorf("workspace: stat orphan blob %s: %w", rev.BlobRef.SHA256, err)
		}
		if !has {
			result.Skipped++
			continue
		}

		if err := w.blobs().Delete(ctx, rev.BlobRef); err != nil {
			softErr := fmt.Errorf("workspace: delete orphan blob %s: %w", rev.BlobRef.SHA256, err)
			result.DeleteErrors = append(result.DeleteErrors, softErr)
			continue
		}
		result.BytesDeleted++
	}
	return result, nil
}

// PromoteReappearedBlob is the explicit runtime re-promotion lever for "bytes
// came back by hash" (re-ingest / re-import / shared by another world): it flips
// every mirror_only revision of ref's sha256 back to full/blob_present=1 when the
// bytes are present again, and returns the number promoted.
//
// It is the runtime companion to ReconcileBlobs's Direction 2 (which covers the
// startup-drift case): same no-resurrection rule, applied to a specific ref a
// caller knows just arrived. The blobs().Has gate is what keeps it honest — a
// mirror_only row whose bytes are absent is intentionally evicted and is left
// mirror_only (no false claim of rematerializability). PromoteRevision flips
// tier/blob_present ONLY (refcount untouched: the evicted row kept its self-ref
// through eviction), so there is no double refcount.
func (w *Workspace) PromoteReappearedBlob(ctx context.Context, ref blobstore.Ref) (int, error) {
	if w == nil {
		return 0, fmt.Errorf("workspace: PromoteReappearedBlob on nil workspace")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	has, err := w.blobs().Has(ctx, ref)
	if err != nil {
		return 0, fmt.Errorf("workspace: stat blob %s: %w", ref.SHA256, err)
	}
	if !has {
		// Bytes are not actually present: never promote (that would falsely claim
		// rematerializability; the no-mirror→blob invariant).
		return 0, nil
	}

	revs, err := w.store().RevisionsBySHA256(ctx, ref.SHA256)
	if err != nil {
		return 0, fmt.Errorf("workspace: list revisions by sha256 %s: %w", ref.SHA256, err)
	}
	promoted := 0
	for _, r := range revs {
		if r.Tier != "mirror_only" {
			continue
		}
		if err := w.store().PromoteRevision(ctx, r.ID); err != nil {
			return promoted, fmt.Errorf("workspace: promote revision %d: %w", r.ID, err)
		}
		promoted++
	}
	return promoted, nil
}
