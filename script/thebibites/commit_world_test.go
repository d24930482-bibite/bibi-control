package thebibites

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"go.starlark.net/starlark"

	"github.com/asemones/bibicontrol/blobstore"
	"github.com/asemones/bibicontrol/revisionstore"
	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

// seedCommitWorld creates a world in revs and seeds it with a first revision
// (advancing the head with parent=nil), modeling the C1 import that always
// precedes a world-level commit. It returns the world id and the seeded head id.
func seedCommitWorld(t *testing.T, ctx context.Context, revs *revisionstore.Store) (string, int64) {
	t.Helper()
	ws, err := revs.CreateWorkspace(ctx, revisionstore.WorkspaceInput{Owner: "alice", Name: "demo"})
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	world, err := revs.CreateWorld(ctx, revisionstore.WorldInput{WorkspaceID: ws.ID, Name: "world-a"})
	if err != nil {
		t.Fatalf("CreateWorld: %v", err)
	}
	// A first revision needs a script run to satisfy the FK. Reuse the fixture
	// sha as the (valid 64-hex) script sha, matching the import-run shape.
	run, err := revs.RecordScriptRun(ctx, revisionstore.ScriptRunInput{
		ScriptSHA256: seedSHA(),
		Status:       "imported",
	})
	if err != nil {
		t.Fatalf("RecordScriptRun (seed): %v", err)
	}
	rev, err := revs.RecordRevisionAdvancingHead(ctx, world.ID, nil, revisionstore.RevisionInput{
		ParentID:    nil,
		WorldID:     world.ID,
		SourcePath:  "seed",
		BlobRef:     blobstore.Ref{SHA256: seedSHA(), Size: 1},
		ScriptRunID: run.ID,
	})
	if err != nil {
		t.Fatalf("RecordRevisionAdvancingHead (seed): %v", err)
	}
	return world.ID, rev.ID
}

// seedSHA supplies a deterministic, valid 64-hex digest for the seed revision
// (its bytes are never read by the script-only commit path).
func seedSHA() string { return strings.Repeat("a", 64) }

// TestRunAndCommitWorldAdvancesHead: a mutating run against the loaded working
// copy threads parent = the seeded head, advances the world head to the new
// revision, and reports the working partition key (== worldID).
func TestRunAndCommitWorldAdvancesHead(t *testing.T) {
	ctx := context.Background()
	_, blobs, revs := newStores(t)
	worldID, seededHead := seedCommitWorld(t, ctx, revs)

	ls := loadFixture(t)
	ls.saveID = worldID // the working-partition key the workspace pins
	target := firstBibiteEntry(t, ls)

	wc, err := RunAndCommitWorld(ctx, ls, setEnergyProgram(target, 4321.0), worldID, &seededHead, blobs, revs, RunOptions{Filename: "mutate.star"})
	if err != nil {
		t.Fatalf("RunAndCommitWorld: %v (%+v)", err, wc.Result.Diagnostics)
	}
	if !wc.Committed {
		t.Fatal("Committed = false, want true for a mutating run")
	}
	if wc.SaveID != worldID {
		t.Errorf("SaveID = %q, want worldID %q", wc.SaveID, worldID)
	}
	if wc.Revision.ParentID == nil || *wc.Revision.ParentID != seededHead {
		t.Errorf("Revision.ParentID = %v, want %d", wc.Revision.ParentID, seededHead)
	}
	if wc.Revision.WorldID != worldID {
		t.Errorf("Revision.WorldID = %q, want %q", wc.Revision.WorldID, worldID)
	}
	if wc.Revision.Tier != "full" {
		t.Errorf("Revision.Tier = %q, want full", wc.Revision.Tier)
	}
	if !wc.Revision.BlobPresent {
		t.Error("Revision.BlobPresent = false, want true")
	}
	// RecordRevisionAdvancingHead self-refs the blob in-tx, so a freshly recorded
	// revision is at refcount = 1 on its own — no separate IncBlobRef.
	if wc.Revision.Refcount != 1 {
		t.Errorf("Revision.Refcount = %d, want 1 (no double-count)", wc.Revision.Refcount)
	}

	got, err := revs.GetWorld(ctx, worldID)
	if err != nil {
		t.Fatalf("GetWorld: %v", err)
	}
	if got.HeadRevisionID == nil || *got.HeadRevisionID != wc.Revision.ID {
		t.Errorf("world HeadRevisionID = %v, want new revision %d", got.HeadRevisionID, wc.Revision.ID)
	}
	if got.HeadRevisionID != nil && *got.HeadRevisionID == seededHead {
		t.Error("world head did not advance off the seeded head")
	}
}

// TestRunAndCommitWorldChurn: a pure-mutation commit performs exactly one
// WriteArchive and zero reparses (one only under Verify) — the churn DoD.
func TestRunAndCommitWorldChurn(t *testing.T) {
	ctx := context.Background()
	_, blobs, revs := newStores(t)
	worldID, seededHead := seedCommitWorld(t, ctx, revs)

	ls := loadFixture(t)
	ls.saveID = worldID
	target := firstBibiteEntry(t, ls)

	wc, err := RunAndCommitWorld(ctx, ls, setEnergyProgram(target, 4321.0), worldID, &seededHead, blobs, revs, RunOptions{})
	if err != nil {
		t.Fatalf("RunAndCommitWorld: %v", err)
	}
	if !wc.Committed {
		t.Fatal("Committed = false, want true")
	}
	if ls.writeArchiveCount != 1 {
		t.Errorf("writeArchiveCount = %d, want 1", ls.writeArchiveCount)
	}
	if ls.reparseCount != 0 {
		t.Errorf("reparseCount = %d, want 0 (no verify)", ls.reparseCount)
	}
	if ls.dbOpenCount != 0 {
		t.Errorf("dbOpenCount = %d, want 0 (pure mutation never opens DuckDB)", ls.dbOpenCount)
	}

	// With Verify the produced bytes are reparsed exactly once.
	worldID2, seededHead2 := seedCommitWorld(t, ctx, revs)
	ls2 := loadFixture(t)
	ls2.saveID = worldID2
	target2 := firstBibiteEntry(t, ls2)
	wc2, err := RunAndCommitWorld(ctx, ls2, setEnergyProgram(target2, 99.0), worldID2, &seededHead2, blobs, revs, RunOptions{Verify: true})
	if err != nil {
		t.Fatalf("RunAndCommitWorld (verify): %v", err)
	}
	if !wc2.Committed {
		t.Fatal("Committed = false under verify, want true")
	}
	if ls2.writeArchiveCount != 1 {
		t.Errorf("writeArchiveCount = %d, want 1 under verify", ls2.writeArchiveCount)
	}
	if ls2.reparseCount != 1 {
		t.Errorf("reparseCount = %d, want 1 under verify", ls2.reparseCount)
	}
}

// TestCommitLoadedWorldAdvancesHead: the object-based (no program re-run) commit
// path. Mutations are staged directly on ls (bulkSet, the same primitive
// s.bibites.where().set() drives); CommitLoadedWorld then advances the head with
// the IDENTICAL invariants as the program path — parent threaded, refcount == 1
// (no double IncBlobRef), and the churn counters (one WriteArchive, zero
// reparses). This guards the factored commit path independent of the run path.
func TestCommitLoadedWorldAdvancesHead(t *testing.T) {
	ctx := context.Background()
	_, blobs, revs := newStores(t)
	worldID, seededHead := seedCommitWorld(t, ctx, revs)

	ls := loadFixture(t)
	// Re-key the working copy to worldID (the working-partition key the workspace
	// pins via LoadInto). The in-memory rows the analytics push-down imports are
	// stamped with the partition key, so re-extract under worldID — otherwise the
	// bulkSet WHERE save_id=worldID would match nothing (the LoadInto seam seeds
	// under worldID for real; loadFixture uses the fixture's archive hash).
	ls.saveID = worldID
	ls.tables = tb.ExtractTables(worldID, ls.archive)
	ls.buildAccess()
	target := firstBibiteEntry(t, ls)

	// Stage a set directly on ls — no script.Run — exactly as world.open() ->
	// s.bibites.where(...).set(...) would in the object-based automation surface.
	staged, err := ls.bulkSet("bibite", fmt.Sprintf("entry_name == '%s'", target), "energy", starlark.Float(4321.0))
	if err != nil {
		t.Fatalf("bulkSet: %v", err)
	}
	if staged != 1 {
		t.Fatalf("staged = %d, want 1", staged)
	}

	wc, err := CommitLoadedWorld(ctx, ls, worldID, &seededHead, blobs, revs, RunOptions{})
	if err != nil {
		t.Fatalf("CommitLoadedWorld: %v", err)
	}
	if !wc.Committed {
		t.Fatal("Committed = false, want true for a staged-mutation commit")
	}
	if wc.SaveID != worldID {
		t.Errorf("SaveID = %q, want worldID %q", wc.SaveID, worldID)
	}
	if wc.Revision.ParentID == nil || *wc.Revision.ParentID != seededHead {
		t.Errorf("Revision.ParentID = %v, want %d", wc.Revision.ParentID, seededHead)
	}
	if wc.Revision.WorldID != worldID {
		t.Errorf("Revision.WorldID = %q, want %q", wc.Revision.WorldID, worldID)
	}
	// RecordRevisionAdvancingHead self-refs the blob in-tx, so the freshly recorded
	// revision is at refcount = 1 — no separate IncBlobRef in the factored path.
	if wc.Revision.Refcount != 1 {
		t.Errorf("Revision.Refcount = %d, want 1 (no double-count)", wc.Revision.Refcount)
	}
	// Churn DoD: exactly one WriteArchive, zero reparses (no verify), DuckDB never
	// opened by the pure-mutation commit — same shape as TestRunAndCommitWorldChurn.
	if ls.writeArchiveCount != 1 {
		t.Errorf("writeArchiveCount = %d, want 1", ls.writeArchiveCount)
	}
	if ls.reparseCount != 0 {
		t.Errorf("reparseCount = %d, want 0 (no verify)", ls.reparseCount)
	}

	got, err := revs.GetWorld(ctx, worldID)
	if err != nil {
		t.Fatalf("GetWorld: %v", err)
	}
	if got.HeadRevisionID == nil || *got.HeadRevisionID != wc.Revision.ID {
		t.Errorf("world HeadRevisionID = %v, want new revision %d", got.HeadRevisionID, wc.Revision.ID)
	}
	if got.HeadRevisionID != nil && *got.HeadRevisionID == seededHead {
		t.Error("world head did not advance off the seeded head")
	}
}

// TestCommitLoadedWorldNoOp: an object-based commit with nothing staged produces
// no revision and leaves the head unchanged (same no-op contract as the program
// path).
func TestCommitLoadedWorldNoOp(t *testing.T) {
	ctx := context.Background()
	_, blobs, revs := newStores(t)
	worldID, seededHead := seedCommitWorld(t, ctx, revs)

	ls := loadFixture(t)
	ls.saveID = worldID

	wc, err := CommitLoadedWorld(ctx, ls, worldID, &seededHead, blobs, revs, RunOptions{})
	if err != nil {
		t.Fatalf("CommitLoadedWorld: %v", err)
	}
	if wc.Committed {
		t.Error("Committed = true, want false with nothing staged")
	}
	if wc.Revision.ID != 0 {
		t.Errorf("Revision.ID = %d, want 0 (no revision)", wc.Revision.ID)
	}

	got, err := revs.GetWorld(ctx, worldID)
	if err != nil {
		t.Fatalf("GetWorld: %v", err)
	}
	if got.HeadRevisionID == nil || *got.HeadRevisionID != seededHead {
		t.Errorf("world head = %v, want unchanged seeded head %d", got.HeadRevisionID, seededHead)
	}
}

// TestRunAndCommitWorldNoOp: a run that stages nothing (autocommit(False))
// produces no revision and leaves the head unchanged.
func TestRunAndCommitWorldNoOp(t *testing.T) {
	ctx := context.Background()
	_, blobs, revs := newStores(t)
	worldID, seededHead := seedCommitWorld(t, ctx, revs)

	ls := loadFixture(t)
	ls.saveID = worldID
	target := firstBibiteEntry(t, ls)

	program := []byte(fmt.Sprintf(`
autocommit(False)
s = open()

def mutate():
    for b in s.bibites:
        if b.entry_name == %q:
            b.energy = 4321.0
            break

mutate()
`, target))

	wc, err := RunAndCommitWorld(ctx, ls, program, worldID, &seededHead, blobs, revs, RunOptions{})
	if err != nil {
		t.Fatalf("RunAndCommitWorld: %v", err)
	}
	if wc.Committed {
		t.Error("Committed = true, want false after autocommit(False)")
	}
	if wc.Revision.ID != 0 {
		t.Errorf("Revision.ID = %d, want 0 (no revision)", wc.Revision.ID)
	}
	if wc.Applied != nil {
		t.Error("Applied != nil, want nil when not committed")
	}

	got, err := revs.GetWorld(ctx, worldID)
	if err != nil {
		t.Fatalf("GetWorld: %v", err)
	}
	if got.HeadRevisionID == nil || *got.HeadRevisionID != seededHead {
		t.Errorf("world head = %v, want unchanged seeded head %d", got.HeadRevisionID, seededHead)
	}
}

// TestRunAndCommitWorldCommitFailure: a nil blob store on a mutating run yields a
// non-nil error, no revision, and an unadvanced head.
func TestRunAndCommitWorldCommitFailure(t *testing.T) {
	ctx := context.Background()
	_, _, revs := newStores(t)
	worldID, seededHead := seedCommitWorld(t, ctx, revs)

	ls := loadFixture(t)
	ls.saveID = worldID
	target := firstBibiteEntry(t, ls)

	wc, err := RunAndCommitWorld(ctx, ls, setEnergyProgram(target, 4321.0), worldID, &seededHead, nil, revs, RunOptions{})
	if err == nil {
		t.Fatal("RunAndCommitWorld succeeded, want a nil-blob-store error")
	}
	if wc.Committed {
		t.Error("Committed = true, want false on a commit failure")
	}

	got, err := revs.GetWorld(ctx, worldID)
	if err != nil {
		t.Fatalf("GetWorld: %v", err)
	}
	if got.HeadRevisionID == nil || *got.HeadRevisionID != seededHead {
		t.Errorf("world head = %v, want unchanged seeded head %d (commit failed)", got.HeadRevisionID, seededHead)
	}
}
