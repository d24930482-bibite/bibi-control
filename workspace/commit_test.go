package workspace

import (
	"context"
	"fmt"
	"testing"

	"github.com/asemones/bibicontrol/script/thebibites"
)

// firstBibiteEntryName reads one bibite entry_name out of the world's working
// DuckDB partition so a mutation program can target a real entity in the fixture.
func firstBibiteEntryName(t *testing.T, ctx context.Context, ws *Workspace, worldID string) string {
	t.Helper()
	var name string
	err := ws.duck().QueryRowContext(ctx,
		"SELECT entry_name FROM bibites WHERE save_id = ? ORDER BY entry_name LIMIT 1", worldID).Scan(&name)
	if err != nil {
		t.Fatalf("read first bibite entry for world %q: %v", worldID, err)
	}
	return name
}

// setBibiteEnergy returns a pure-mutation program setting one bibite's energy.
func setBibiteEnergy(entry string, energy float64) []byte {
	return []byte(fmt.Sprintf(`
s = open()

def mutate():
    for b in s.bibites:
        if b.entry_name == %q:
            b.energy = %v
            break

mutate()
`, entry, energy))
}

// bibiteEnergyInPartition reads one bibite's energy out of a specific DuckDB
// partition (save_id = key) — the working partition (worldID) or a history
// partition (revision sha256).
func bibiteEnergyInPartition(t *testing.T, ctx context.Context, ws *Workspace, key, entry string) float64 {
	t.Helper()
	var v float64
	err := ws.duck().QueryRowContext(ctx,
		"SELECT energy FROM bibites WHERE save_id = ? AND entry_name = ?", key, entry).Scan(&v)
	if err != nil {
		t.Fatalf("read energy for entry %q in partition %q: %v", entry, key, err)
	}
	return v
}

// TestCommitWorldAdvancesHeadAndThreadsParent: a mutating commit records a new
// revision threaded onto the prior head and advances the world head to it.
func TestCommitWorldAdvancesHeadAndThreadsParent(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	world, err := ws.AddWorld(ctx, fixturePath(t, fixtureSmall), "world-a")
	if err != nil {
		t.Fatalf("AddWorld: %v", err)
	}
	if world.HeadRevisionID == nil {
		t.Fatal("world head is nil after AddWorld")
	}
	firstHead := *world.HeadRevisionID
	entry := firstBibiteEntryName(t, ctx, ws, world.ID)

	rev, err := ws.CommitWorld(ctx, world.ID, setBibiteEnergy(entry, 4321.0), thebibites.RunOptions{})
	if err != nil {
		t.Fatalf("CommitWorld: %v", err)
	}
	if rev.ID == 0 {
		t.Fatal("CommitWorld returned the zero revision, want a committed revision")
	}
	if rev.ID == firstHead {
		t.Errorf("new revision id == first head %d (head did not advance)", firstHead)
	}
	if rev.ParentID == nil || *rev.ParentID != firstHead {
		t.Errorf("revision ParentID = %v, want first head %d", rev.ParentID, firstHead)
	}
	if rev.WorldID != world.ID {
		t.Errorf("revision WorldID = %q, want %q", rev.WorldID, world.ID)
	}
	if rev.Refcount != 1 {
		t.Errorf("revision Refcount = %d, want 1 (no double-count)", rev.Refcount)
	}

	got, err := ws.store().GetWorld(ctx, world.ID)
	if err != nil {
		t.Fatalf("GetWorld: %v", err)
	}
	if got.HeadRevisionID == nil || *got.HeadRevisionID != rev.ID {
		t.Errorf("world HeadRevisionID = %v, want new revision %d", got.HeadRevisionID, rev.ID)
	}
}

// TestCommitWorldHistoryRetained: committing twice leaves all three revisions'
// history partitions intact (history accumulates; a commit never deletes prior
// revisions' rows). The headline dual-key + retention assertion.
func TestCommitWorldHistoryRetained(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	world, err := ws.AddWorld(ctx, fixturePath(t, fixtureSmall), "world-a")
	if err != nil {
		t.Fatalf("AddWorld: %v", err)
	}
	entry := firstBibiteEntryName(t, ctx, ws, world.ID)

	if _, err := ws.CommitWorld(ctx, world.ID, setBibiteEnergy(entry, 100.0), thebibites.RunOptions{}); err != nil {
		t.Fatalf("CommitWorld #1: %v", err)
	}
	if _, err := ws.CommitWorld(ctx, world.ID, setBibiteEnergy(entry, 200.0), thebibites.RunOptions{}); err != nil {
		t.Fatalf("CommitWorld #2: %v", err)
	}

	revisions, err := ws.store().RevisionsForWorld(ctx, world.ID)
	if err != nil {
		t.Fatalf("RevisionsForWorld: %v", err)
	}
	if len(revisions) != 3 {
		t.Fatalf("RevisionsForWorld returned %d revisions, want 3 (import + 2 commits)", len(revisions))
	}

	// Distinct sha256 per revision (each commit changed the same value differently).
	seen := map[string]bool{}
	for _, rev := range revisions {
		if seen[rev.SHA256] {
			t.Fatalf("two revisions share sha256 %q, expected distinct history partitions", rev.SHA256)
		}
		seen[rev.SHA256] = true
	}

	// Every revision's history partition must persist (>0 rows). bibites is
	// non-empty for fixtureSmall; save_archives is the per-save_id fallback.
	for i, rev := range revisions {
		if got := countBySaveID(t, ctx, ws, "bibites", rev.SHA256); got == 0 {
			t.Errorf("history bibites partition for revision %d (sha %q) is empty", i, rev.SHA256)
		}
		if got := countBySaveID(t, ctx, ws, "save_archives", rev.SHA256); got != 1 {
			t.Errorf("save_archives count for revision %d (sha %q) = %d, want 1", i, rev.SHA256, got)
		}
	}
}

// TestCommitWorldWorkingPartitionReflectsHead: after a value-changing commit the
// working partition (save_id = worldID) holds the NEW value while the prior
// head's history partition (save_id = sha0) still holds the OLD value.
func TestCommitWorldWorkingPartitionReflectsHead(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	world, err := ws.AddWorld(ctx, fixturePath(t, fixtureSmall), "world-a")
	if err != nil {
		t.Fatalf("AddWorld: %v", err)
	}
	entry := firstBibiteEntryName(t, ctx, ws, world.ID)

	// Record the import head's sha (sha0) and the old value in its history partition.
	revs0, err := ws.store().RevisionsForWorld(ctx, world.ID)
	if err != nil {
		t.Fatalf("RevisionsForWorld (pre-commit): %v", err)
	}
	if len(revs0) != 1 {
		t.Fatalf("want 1 revision before commit, got %d", len(revs0))
	}
	sha0 := revs0[0].SHA256
	oldValue := bibiteEnergyInPartition(t, ctx, ws, sha0, entry)

	const newValue = 7777.0
	if oldValue == newValue {
		t.Fatalf("fixture already has energy %v for %q; pick a different target value", newValue, entry)
	}

	if _, err := ws.CommitWorld(ctx, world.ID, setBibiteEnergy(entry, newValue), thebibites.RunOptions{}); err != nil {
		t.Fatalf("CommitWorld: %v", err)
	}

	// Working partition re-seeded to the new head: new value.
	if got := bibiteEnergyInPartition(t, ctx, ws, world.ID, entry); got != newValue {
		t.Errorf("working partition energy = %v, want new head value %v", got, newValue)
	}
	// History partition for the prior head is immutable: still the old value.
	if got := bibiteEnergyInPartition(t, ctx, ws, sha0, entry); got != oldValue {
		t.Errorf("prior head history energy = %v, want unchanged old value %v", got, oldValue)
	}
}

// TestCommitWorldNoOpDoesNotAdvanceHead: an autocommit(False) program returns the
// zero revision, no error, and the world head is unchanged.
func TestCommitWorldNoOpDoesNotAdvanceHead(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	world, err := ws.AddWorld(ctx, fixturePath(t, fixtureSmall), "world-a")
	if err != nil {
		t.Fatalf("AddWorld: %v", err)
	}
	firstHead := *world.HeadRevisionID
	entry := firstBibiteEntryName(t, ctx, ws, world.ID)

	program := []byte(fmt.Sprintf(`
autocommit(False)
s = open()

def mutate():
    for b in s.bibites:
        if b.entry_name == %q:
            b.energy = 4321.0
            break

mutate()
`, entry))

	rev, err := ws.CommitWorld(ctx, world.ID, program, thebibites.RunOptions{})
	if err != nil {
		t.Fatalf("CommitWorld: %v", err)
	}
	if rev.ID != 0 {
		t.Errorf("revision ID = %d, want 0 (no commit)", rev.ID)
	}

	got, err := ws.store().GetWorld(ctx, world.ID)
	if err != nil {
		t.Fatalf("GetWorld: %v", err)
	}
	if got.HeadRevisionID == nil || *got.HeadRevisionID != firstHead {
		t.Errorf("world head = %v, want unchanged %d", got.HeadRevisionID, firstHead)
	}
}

// TestCommitWorldChurn: one commit adds exactly one new history partition (the
// new revision sha) and re-seeds the single working partition — no extra
// save_archives partitions appear beyond import + 1 history per commit.
func TestCommitWorldChurn(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	world, err := ws.AddWorld(ctx, fixturePath(t, fixtureSmall), "world-a")
	if err != nil {
		t.Fatalf("AddWorld: %v", err)
	}
	entry := firstBibiteEntryName(t, ctx, ws, world.ID)

	before := distinctSaveArchivePartitions(t, ctx, ws)

	if _, err := ws.CommitWorld(ctx, world.ID, setBibiteEnergy(entry, 4321.0), thebibites.RunOptions{}); err != nil {
		t.Fatalf("CommitWorld: %v", err)
	}

	after := distinctSaveArchivePartitions(t, ctx, ws)
	// Exactly one new partition (the new history sha); the working partition
	// (worldID) is re-seeded in place, not duplicated.
	if after != before+1 {
		t.Errorf("distinct save_archives partitions: before=%d after=%d, want after=before+1", before, after)
	}
}

// TestCommitWorldLazyLoads: CommitWorld on a never-Loaded world succeeds (the
// OpenWorld lazy-load path) and, after the commit consumes that copy, OpenWorld
// re-loads a fresh stageable copy from the new head — so a second commit also
// succeeds rather than failing "cannot stage after apply".
func TestCommitWorldLazyLoads(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	world, err := ws.AddWorld(ctx, fixturePath(t, fixtureSmall), "world-a")
	if err != nil {
		t.Fatalf("AddWorld: %v", err)
	}
	if _, loaded := ws.worlds[world.ID]; loaded {
		t.Fatal("world is already loaded after AddWorld; expected lazy load on first CommitWorld")
	}
	entry := firstBibiteEntryName(t, ctx, ws, world.ID)

	rev1, err := ws.CommitWorld(ctx, world.ID, setBibiteEnergy(entry, 4321.0), thebibites.RunOptions{})
	if err != nil {
		t.Fatalf("CommitWorld #1 (lazy load): %v", err)
	}
	// The consumed working copy is dropped so the next commit re-loads a fresh,
	// stageable copy from the head rev1 just produced.
	if _, loaded := ws.worlds[world.ID]; loaded {
		t.Fatal("consumed working copy still in working set; expected it dropped after commit")
	}

	rev2, err := ws.CommitWorld(ctx, world.ID, setBibiteEnergy(entry, 8765.0), thebibites.RunOptions{})
	if err != nil {
		t.Fatalf("CommitWorld #2 (re-load from head): %v", err)
	}
	if rev2.ParentID == nil || *rev2.ParentID != rev1.ID {
		t.Errorf("second commit ParentID = %v, want first commit head %d", rev2.ParentID, rev1.ID)
	}
}

// distinctSaveArchivePartitions counts the distinct save_id partitions present in
// save_archives across all worlds in the workspace DuckDB.
func distinctSaveArchivePartitions(t *testing.T, ctx context.Context, ws *Workspace) int64 {
	t.Helper()
	var n int64
	if err := ws.duck().QueryRowContext(ctx, "SELECT count(DISTINCT save_id) FROM save_archives").Scan(&n); err != nil {
		t.Fatalf("count distinct save_archives partitions: %v", err)
	}
	return n
}
