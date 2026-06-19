package workspace

import (
	"context"
	"errors"
	"testing"

	"github.com/asemones/bibicontrol/revisionstore"
	"github.com/asemones/bibicontrol/script/thebibites"
)

// buildWorldWithHistoryAt creates a workspace under root, imports fixtureSmall,
// and commits it twice so the world has three revisions (import + 2 commits) with
// the head at the last. It returns the workspace id, the world, and its revisions
// in lineage order. The workspace is left OPEN — the caller Closes it before
// reopening with OpenAndReconcile (this is the crash-recovery seam: the on-disk
// state survives the Close, only the in-memory handle is gone).
func buildWorldWithHistoryAt(t *testing.T, ctx context.Context, root string) (string, revisionstore.World, []revisionstore.Revision, *Workspace) {
	t.Helper()
	ws, err := Create(ctx, root, "alice", "demo")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
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
	return ws.ID(), got, revs, ws
}

// TestOpenAndReconcileDemotesFullButMissing proves the daemon's crash-recovery
// path: a non-head full revision whose bytes went missing while the workspace was
// closed is demoted to mirror_only when the workspace is reopened with
// OpenAndReconcile.
func TestOpenAndReconcileDemotesFullButMissing(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	id, world, revs, ws := buildWorldWithHistoryAt(t, ctx, root)
	headID := *world.HeadRevisionID

	var rev revisionstore.Revision
	for _, r := range revs {
		if r.ID != headID {
			rev = r
			break
		}
	}
	// Simulate a crash that deleted a non-head full revision's bytes out-of-band
	// while the catalog still says full/present. Delete directly via blobs(), then
	// Close the workspace so the reopen is a genuine fresh handle.
	if err := ws.blobs().Delete(ctx, rev.BlobRef); err != nil {
		t.Fatalf("out-of-band Delete: %v", err)
	}
	if err := ws.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, res, err := OpenAndReconcile(ctx, root, id)
	if err != nil {
		t.Fatalf("OpenAndReconcile: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	if res.Demoted != 1 {
		t.Errorf("res.Demoted = %d, want 1", res.Demoted)
	}
	got, err := reopened.store().RevisionByID(ctx, rev.ID)
	if err != nil {
		t.Fatalf("RevisionByID: %v", err)
	}
	if got.Tier != "mirror_only" || got.BlobPresent {
		t.Errorf("after OpenAndReconcile row = (tier=%q, present=%v), want (mirror_only, false)", got.Tier, got.BlobPresent)
	}
}

// TestOpenAndReconcileHeadMissingFailsLoud proves two things at once: a HEAD with
// missing bytes makes OpenAndReconcile fail loud with ErrHeadBlobMissing (no
// silent demote), AND the failed handle is Closed (leaks nothing) — proven by a
// subsequent plain Open succeeding (a leaked DuckDB writer would block/corrupt
// the second open).
func TestOpenAndReconcileHeadMissingFailsLoud(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	id, world, _, ws := buildWorldWithHistoryAt(t, ctx, root)
	headID := *world.HeadRevisionID
	headRev, err := ws.store().RevisionByID(ctx, headID)
	if err != nil {
		t.Fatalf("RevisionByID(head): %v", err)
	}
	// Delete the HEAD's bytes out-of-band: unrecoverable corruption.
	if err := ws.blobs().Delete(ctx, headRev.BlobRef); err != nil {
		t.Fatalf("out-of-band Delete(head): %v", err)
	}
	if err := ws.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, _, err := OpenAndReconcile(ctx, root, id)
	if !errors.Is(err, ErrHeadBlobMissing) {
		t.Fatalf("OpenAndReconcile = %v, want ErrHeadBlobMissing", err)
	}
	if reopened != nil {
		t.Fatalf("OpenAndReconcile returned non-nil workspace on failure, want nil (and Closed)")
	}

	// The failed handle was Closed: a plain Open must still succeed. If
	// OpenAndReconcile had leaked the DuckDB writer, this second open would fail or
	// corrupt the file.
	plain, err := Open(ctx, root, id)
	if err != nil {
		t.Fatalf("plain Open after failed OpenAndReconcile leaked a handle: %v", err)
	}
	t.Cleanup(func() { _ = plain.Close() })

	// The head must NOT have been silently demoted by the failed reconcile.
	got, err := plain.store().RevisionByID(ctx, headID)
	if err != nil {
		t.Fatalf("RevisionByID(head) after failed reconcile: %v", err)
	}
	if got.Tier != "full" {
		t.Errorf("head tier = %q after failed reconcile, want unchanged 'full' (no silent demote)", got.Tier)
	}
}

// TestOpenAndReconcileCleanIsNoOp proves the happy path: opening a clean
// workspace reconciles nothing and returns a usable handle.
func TestOpenAndReconcileCleanIsNoOp(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	id, _, _, ws := buildWorldWithHistoryAt(t, ctx, root)
	if err := ws.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, res, err := OpenAndReconcile(ctx, root, id)
	if err != nil {
		t.Fatalf("OpenAndReconcile (clean): %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	if res.Demoted != 0 || res.Promoted != 0 {
		t.Errorf("clean reconcile result = %+v, want zero", res)
	}
	if reopened.ID() != id {
		t.Errorf("reopened ID = %q, want %q", reopened.ID(), id)
	}
}
