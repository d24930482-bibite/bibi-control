package workspace

import (
	"context"
	"errors"
	"fmt"

	"github.com/asemones/bibicontrol/revisionstore"
)

// ErrHeadBlobMissing is returned by ReconcileBlobs when a world head revision is
// 'full'/blob_present=1 but its backing blob bytes are gone. A head with missing
// bytes is unrecoverable corruption (there is no mirror→blob rematerialization),
// so reconcile fails loud rather than silently demoting a head.
var ErrHeadBlobMissing = errors.New("workspace: world head revision blob is missing (unrecoverable)")

// EvictKind discriminates the eviction policy.
type EvictKind int

const (
	// evictKeepLastN keeps the last N revisions (by lineage order) of the world.
	evictKeepLastN EvictKind = iota
	// evictOlderThanSimTime evicts revisions recorded before a cutoff.
	evictOlderThanSimTime
)

// EvictPolicy selects which of a world's revisions are eviction candidates. It
// is small and explicit; EvictWorldHistory switches on Kind to build the
// candidate list. Construct it via KeepLastN or OlderThanSimTime.
type EvictPolicy struct {
	Kind EvictKind
	// N is the count kept for evictKeepLastN.
	N int
	// Before is the cutoff for evictOlderThanSimTime.
	Before float64
}

// KeepLastN evicts every revision of the world except the last n in lineage
// order (and never the head — head protection is double-gated). n must be >= 0;
// n == 0 makes every non-head revision a candidate.
func KeepLastN(n int) EvictPolicy {
	return EvictPolicy{Kind: evictKeepLastN, N: n}
}

// OlderThanSimTime evicts revisions recorded before t.
//
// Per-revision sim_time is NOT stored today (sim_time lives on the world row,
// updated as the head advances; save_revisions has no sim_time column). To avoid
// guessing, this constructor scopes "older than" to the revision's created_at
// timestamp interpreted as Unix seconds: a revision is a candidate when its
// CreatedAt (Unix seconds) is strictly less than t. KeepLastN is the must-ship
// policy; OlderThanSimTime is the secondary, created_at-scoped approximation.
func OlderThanSimTime(t float64) EvictPolicy {
	return EvictPolicy{Kind: evictOlderThanSimTime, Before: t}
}

// EvictResult reports the outcome of an EvictWorldHistory sweep so callers and
// tests can assert without re-querying.
type EvictResult struct {
	// Candidates is the number of revisions the policy selected.
	Candidates int
	// Demoted is the number flipped to tier=mirror_only (catalog committed).
	Demoted int
	// BytesDeleted is the number whose blobstore object delete succeeded.
	BytesDeleted int
	// RefusedHead is the number refused because they are a world head.
	RefusedHead int
	// RefusedShared is the number refused because refcount > 1.
	RefusedShared int
	// DeleteErrors collects soft (non-fatal) blobstore delete failures that
	// occurred AFTER a successful catalog commit. The revision is correctly
	// mirror_only; the orphan bytes are reconcile/G3's to sweep. Surfaced so the
	// caller knows, never rolled back.
	DeleteErrors []error
}

// ReconcileResult reports the outcome of a ReconcileBlobs pass.
type ReconcileResult struct {
	// Demoted is the number of non-head full-but-missing rows demoted to
	// mirror_only.
	Demoted int
	// Promoted is the number of mirror_only rows whose bytes legitimately
	// reappeared (present and still referenced) re-promoted to full.
	Promoted int
}

// EvictWorldHistory is the memory-reclamation op for Track G: for each eviction
// candidate selected by policy it demotes the revision to mirror_only in the
// SQLite catalog (G1's committed, fsynced tx) and then — only after that commit
// returns — deletes the blobstore bytes.
//
// G2 is a catalog(SQLite)+blobstore op ONLY. It NEVER writes DuckDB: history
// partitions are keyed purely by save_id (= revision sha256) with no
// blob_present coupling, so "keep the mirror" is achieved by simply not touching
// DuckDB — the evicted revision's history rows survive and stay queryable.
//
// Head protection is double-gated: the policy never selects the head AND G1's
// EvictRevisionBlob refuses it (ErrRevisionIsHead). Shared blobs (refcount > 1)
// are refused by G1 (ErrBlobStillReferenced) and never deleted. Both refusals
// are tallied and the sweep continues; only a real DB error aborts.
func (w *Workspace) EvictWorldHistory(ctx context.Context, worldID string, policy EvictPolicy) (EvictResult, error) {
	if w == nil {
		return EvictResult{}, fmt.Errorf("workspace: EvictWorldHistory on nil workspace")
	}
	if worldID == "" {
		return EvictResult{}, fmt.Errorf("workspace: worldID is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// Single-writer discipline: hold w.mu for the whole sweep, the same
	// whole-body-lock shape importWorldFromArchive/Load use. The per-revision
	// core (evictRevisionBlobLocked) assumes the lock is already held and never
	// re-takes it — w.mu is non-reentrant.
	w.mu.Lock()
	defer w.mu.Unlock()

	world, err := w.store().GetWorld(ctx, worldID)
	if err != nil {
		if revisionstore.IsNotFound(err) {
			return EvictResult{}, fmt.Errorf("workspace: world %q not found", worldID)
		}
		return EvictResult{}, fmt.Errorf("workspace: get world %q: %w", worldID, err)
	}

	revisions, err := w.store().RevisionsForWorld(ctx, worldID)
	if err != nil {
		return EvictResult{}, fmt.Errorf("workspace: list revisions for world %q: %w", worldID, err)
	}

	candidates := selectCandidates(revisions, world.HeadRevisionID, policy)

	var result EvictResult
	result.Candidates = len(candidates)
	for _, rev := range candidates {
		deleted, err := w.evictRevisionBlobLocked(ctx, rev.ID, &result)
		if err != nil {
			switch {
			case errors.Is(err, revisionstore.ErrRevisionIsHead):
				result.RefusedHead++
				continue
			case errors.Is(err, revisionstore.ErrBlobStillReferenced):
				result.RefusedShared++
				continue
			default:
				// A real DB error aborts the sweep (and is returned). Counters
				// already reflect what completed before the failure.
				return result, err
			}
		}
		result.Demoted++
		if deleted {
			result.BytesDeleted++
		}
	}
	return result, nil
}

// selectCandidates builds the eviction candidate list from policy, always
// excluding the world head (defense in depth — G1 also refuses it). revisions is
// in lineage order (insertion id order, from RevisionsForWorld).
func selectCandidates(revisions []revisionstore.Revision, headID *int64, policy EvictPolicy) []revisionstore.Revision {
	isHead := func(id int64) bool { return headID != nil && *headID == id }

	switch policy.Kind {
	case evictKeepLastN:
		n := policy.N
		if n < 0 {
			n = 0
		}
		cut := len(revisions) - n
		if cut < 0 {
			cut = 0
		}
		var out []revisionstore.Revision
		for _, rev := range revisions[:cut] {
			if isHead(rev.ID) {
				continue
			}
			out = append(out, rev)
		}
		return out
	case evictOlderThanSimTime:
		var out []revisionstore.Revision
		for _, rev := range revisions {
			if isHead(rev.ID) {
				continue
			}
			if float64(rev.CreatedAt.Unix()) < policy.Before {
				out = append(out, rev)
			}
		}
		return out
	default:
		return nil
	}
}

// EvictRevisionBlob is the single-revision wrapper: it demotes the revision in
// the catalog (G1's committed tx) and then deletes the blobstore bytes. It is
// the public per-revision lever the tests call directly; the sweep uses the
// unlocked core.
func (w *Workspace) EvictRevisionBlob(ctx context.Context, revisionID int64) error {
	if w == nil {
		return fmt.Errorf("workspace: EvictRevisionBlob on nil workspace")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err := w.evictRevisionBlobLocked(ctx, revisionID, nil)
	return err
}

// evictRevisionBlobLocked is the per-revision crash-safe core. The caller MUST
// already hold w.mu. It returns whether the blobstore bytes were deleted.
//
// Ordering (the whole point of the ticket): the CATALOG FLIP IS DURABLE BEFORE
// ANY BYTE IS DELETED.
//  1. w.store().EvictRevisionBlob runs G1's one SQLite tx: it refuses a head
//     (ErrRevisionIsHead) and refcount>1 (ErrBlobStillReferenced), else flips
//     tier='mirror_only', blob_present=0 and COMMITS (fsync). Those typed errors
//     are returned to the caller, which maps them to "refused" — never a delete.
//  2. ONLY after that commit succeeds, re-read the revision for its BlobRef and
//     call blobs().Delete (idempotent: NotFound→nil). A crash before the commit
//     leaves the blob fully present (re-run evicts cleanly); a crash at/within
//     the delete leaves a mirror_only row with possibly-present bytes that
//     reconcile/G3 reclaim — never a deleted-but-still-referenced blob.
//
// A Delete error after a successful commit is a SOFT error: the revision is
// correctly mirror_only and there is nothing to roll back; the orphan bytes are
// reconcile/G3's to sweep. It is recorded in result.DeleteErrors (when result is
// non-nil) but the core returns (false, nil), NOT the soft error — the catalog is
// not undone. The public EvictRevisionBlob wrapper passes result=nil, so it sees
// only that no bytes were deleted (returns nil) and leaves the orphan for G3's
// GCUnreferencedBlobs to sweep.
func (w *Workspace) evictRevisionBlobLocked(ctx context.Context, revisionID int64, result *EvictResult) (bool, error) {
	// 1. Catalog flip, committed and fsynced, with the head/refcount gates.
	if err := w.store().EvictRevisionBlob(ctx, revisionID); err != nil {
		return false, err
	}

	// 2. Re-read for the BlobRef now that the catalog says mirror_only.
	rev, err := w.store().RevisionByID(ctx, revisionID)
	if err != nil {
		// The catalog flip already committed; surface the read failure as a soft
		// delete error (we cannot find the ref to delete the bytes). Do not roll
		// back — the revision is correctly evicted; the bytes are reconcile/G3's.
		softErr := fmt.Errorf("workspace: re-read evicted revision %d for blob delete: %w", revisionID, err)
		if result != nil {
			result.DeleteErrors = append(result.DeleteErrors, softErr)
		}
		return false, nil
	}

	// 3. Delete the bytes (idempotent). A failure here is soft — the catalog is
	// already correctly mirror_only.
	if err := w.blobs().Delete(ctx, rev.BlobRef); err != nil {
		softErr := fmt.Errorf("workspace: delete evicted blob for revision %d (%s): %w", revisionID, rev.BlobRef.SHA256, err)
		if result != nil {
			result.DeleteErrors = append(result.DeleteErrors, softErr)
		}
		return false, nil
	}
	return true, nil
}

// ReconcileBlobs repairs catalog-vs-blobstore drift in both directions at
// startup. It does NOT auto-wire into Open — the host calls it explicitly.
//
//   - full/blob_present=1 whose bytes are MISSING: a non-head row is demoted to
//     mirror_only (the normal post-crash-mid-evict state). A HEAD with missing
//     bytes is unrecoverable corruption (no mirror→blob path) → fail loud with
//     ErrHeadBlobMissing rather than silently demote a head.
//   - mirror_only whose bytes REAPPEARED by hash AND are still referenced by a
//     full revision of the same sha256: re-promote to full (G3's narrow repair
//     of "crashed before deleting and the bytes are legitimately still needed").
//     A mirror_only row whose bytes are present but UNREFERENCED is an orphan for
//     G3 to GC — it is NOT re-promoted (that would resurrect an intentionally
//     evicted revision: the structural no-rematerialization invariant).
func (w *Workspace) ReconcileBlobs(ctx context.Context) (ReconcileResult, error) {
	if w == nil {
		return ReconcileResult{}, fmt.Errorf("workspace: ReconcileBlobs on nil workspace")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	var result ReconcileResult

	// Direction 1: full-but-missing rows.
	fulls, err := w.store().FullRevisions(ctx)
	if err != nil {
		return result, fmt.Errorf("workspace: list full revisions: %w", err)
	}
	for _, rev := range fulls {
		has, err := w.blobs().Has(ctx, rev.BlobRef)
		if err != nil {
			return result, fmt.Errorf("workspace: stat blob for revision %d: %w", rev.ID, err)
		}
		if has {
			continue
		}
		// Bytes are gone. A head with missing bytes is unrecoverable.
		isHead, err := w.store().IsRevisionHead(ctx, rev.ID)
		if err != nil {
			return result, fmt.Errorf("workspace: head check for revision %d: %w", rev.ID, err)
		}
		if isHead {
			return result, fmt.Errorf("%w: revision %d (sha %s)", ErrHeadBlobMissing, rev.ID, rev.BlobRef.SHA256)
		}
		if err := w.store().MarkMirrorOnly(ctx, rev.ID); err != nil {
			return result, fmt.Errorf("workspace: demote full-but-missing revision %d: %w", rev.ID, err)
		}
		result.Demoted++
	}

	// Direction 2: mirror_only rows whose bytes reappeared AND are still
	// referenced by a full revision of the same sha256.
	mirrors, err := w.store().MirrorOnlyRevisions(ctx)
	if err != nil {
		return result, fmt.Errorf("workspace: list mirror_only revisions: %w", err)
	}
	for _, rev := range mirrors {
		has, err := w.blobs().Has(ctx, rev.BlobRef)
		if err != nil {
			return result, fmt.Errorf("workspace: stat blob for revision %d: %w", rev.ID, err)
		}
		if !has {
			continue
		}
		// Bytes present: only re-promote if still legitimately referenced by a
		// full revision sharing the sha256. An unreferenced present blob is an
		// orphan for G3 to GC — never resurrect it.
		referenced, err := w.shaHasFullReference(ctx, rev.SHA256)
		if err != nil {
			return result, err
		}
		if !referenced {
			continue
		}
		if err := w.store().PromoteRevision(ctx, rev.ID); err != nil {
			return result, fmt.Errorf("workspace: re-promote revision %d: %w", rev.ID, err)
		}
		result.Promoted++
	}

	return result, nil
}

// shaHasFullReference reports whether any revision sharing sha256 is still
// tier=full/blob_present=1 — i.e. the bytes are legitimately needed and a
// reappeared mirror_only row of the same content may be re-promoted.
func (w *Workspace) shaHasFullReference(ctx context.Context, sha256 string) (bool, error) {
	revs, err := w.store().RevisionsBySHA256(ctx, sha256)
	if err != nil {
		return false, fmt.Errorf("workspace: list revisions by sha256 %s: %w", sha256, err)
	}
	for _, r := range revs {
		if r.Tier == "full" && r.BlobPresent {
			return true, nil
		}
	}
	return false, nil
}
