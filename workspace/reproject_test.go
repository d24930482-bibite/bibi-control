package workspace

import (
	"context"
	"strings"
	"testing"

	"github.com/asemones/bibicontrol/revisionstore"
	"github.com/asemones/bibicontrol/script/thebibites"
)

// emptyWorkingPartition deletes every row for save_id across the mirror tables,
// simulating the one known non-corrupting drift the commit/ingest path documents:
// a successful record-advancing-head followed by a failed ReplaceExtractedSave
// leaves the working partition lagging the head. We force the most extreme form
// (fully empty) so the rebuild is unambiguous.
func emptyWorkingPartition(t *testing.T, ctx context.Context, ws *Workspace, saveID string) {
	t.Helper()
	// Delete from every mirror BASE TABLE that carries a save_id partition.
	// Discover them from information_schema so the helper survives schema growth,
	// but restrict to base tables: some save_id-carrying objects (e.g.
	// bibite_mutation_refs) are VIEWs, which DuckDB cannot DELETE from.
	rows, err := ws.duck().QueryContext(ctx, `
		SELECT c.table_name
		FROM information_schema.columns c
		JOIN information_schema.tables t
		  ON c.table_schema = t.table_schema AND c.table_name = t.table_name
		WHERE c.column_name = 'save_id'
		  AND c.table_schema = 'main'
		  AND t.table_type = 'BASE TABLE'
	`)
	if err != nil {
		t.Fatalf("list save_id tables: %v", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatalf("scan table name: %v", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("iterate save_id tables: %v", err)
	}
	rows.Close()
	if len(tables) == 0 {
		t.Fatal("no save_id-partitioned tables found; fixture/schema assumption broken")
	}
	for _, table := range tables {
		if _, err := ws.duck().ExecContext(ctx, "DELETE FROM "+table+" WHERE save_id = ?", saveID); err != nil {
			t.Fatalf("delete working rows from %s for save_id %q: %v", table, saveID, err)
		}
	}
}

// TestReprojectWorkingRebuildsFromHead proves the on-demand rebuild: after the
// working partition is emptied (the documented drift), ReprojectWorking restores
// it from the current head blob.
func TestReprojectWorkingRebuildsFromHead(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	world, err := ws.AddWorld(ctx, fixturePath(t, fixtureSmall), "world-a")
	if err != nil {
		t.Fatalf("AddWorld: %v", err)
	}

	// Sanity: the working partition is seeded.
	if got := countBySaveID(t, ctx, ws, "save_archives", world.ID); got != 1 {
		t.Fatalf("save_archives for world id before drift = %d, want 1", got)
	}
	bibitesBefore := countBySaveID(t, ctx, ws, "bibites", world.ID)
	if bibitesBefore == 0 {
		t.Fatalf("bibites for world id before drift = 0; fixture must be non-empty")
	}

	// Force the drift: empty the working partition.
	emptyWorkingPartition(t, ctx, ws, world.ID)
	if got := countBySaveID(t, ctx, ws, "save_archives", world.ID); got != 0 {
		t.Fatalf("save_archives for world id after empty = %d, want 0", got)
	}
	if got := countBySaveID(t, ctx, ws, "bibites", world.ID); got != 0 {
		t.Fatalf("bibites for world id after empty = %d, want 0", got)
	}

	// Reproject from head.
	if err := ws.ReprojectWorking(ctx, world.ID); err != nil {
		t.Fatalf("ReprojectWorking: %v", err)
	}

	// The working partition is rebuilt to head.
	if got := countBySaveID(t, ctx, ws, "save_archives", world.ID); got != 1 {
		t.Errorf("save_archives for world id after reproject = %d, want 1", got)
	}
	if got := countBySaveID(t, ctx, ws, "bibites", world.ID); got != bibitesBefore {
		t.Errorf("bibites for world id after reproject = %d, want %d (restored to head)", got, bibitesBefore)
	}
}

// TestReprojectWorkingReflectsLatestHead proves reproject re-seeds from the
// CURRENT head, not the original import: after a commit moves the head and the
// working partition is then emptied, reproject restores the committed (mutated)
// value, not the pre-commit value.
func TestReprojectWorkingReflectsLatestHead(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	world, err := ws.AddWorld(ctx, fixturePath(t, fixtureSmall), "world-a")
	if err != nil {
		t.Fatalf("AddWorld: %v", err)
	}
	entry := firstBibiteEntryName(t, ctx, ws, world.ID)
	if _, err := ws.CommitWorld(ctx, world.ID, setBibiteEnergy(entry, 777.0), thebibites.RunOptions{}); err != nil {
		t.Fatalf("CommitWorld: %v", err)
	}
	// After commit the working partition reflects energy 777.
	if got := bibiteEnergyInPartition(t, ctx, ws, world.ID, entry); got != 777.0 {
		t.Fatalf("working energy after commit = %v, want 777", got)
	}

	// Drift, then reproject.
	emptyWorkingPartition(t, ctx, ws, world.ID)
	if err := ws.ReprojectWorking(ctx, world.ID); err != nil {
		t.Fatalf("ReprojectWorking: %v", err)
	}

	// Reproject restored the head (committed) value, proving it re-seeds from the
	// current head blob, not a stale projection or the original import.
	if got := bibiteEnergyInPartition(t, ctx, ws, world.ID, entry); got != 777.0 {
		t.Errorf("working energy after reproject = %v, want 777 (current head)", got)
	}
}

// TestReprojectWorkingHeadlessFails proves a bare world row (no head) fails loudly
// with a "no head" error rather than panicking on the nil head dereference.
func TestReprojectWorkingHeadlessFails(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	bare, err := ws.store().CreateWorld(ctx, revisionstore.WorldInput{
		WorkspaceID: ws.ID(),
		Name:        "bare",
	})
	if err != nil {
		t.Fatalf("CreateWorld: %v", err)
	}

	err = ws.ReprojectWorking(ctx, bare.ID)
	if err == nil {
		t.Fatal("ReprojectWorking(headless) returned nil error, want a 'no head' error")
	}
	if !strings.Contains(err.Error(), "no head") {
		t.Errorf("ReprojectWorking(headless) error = %q, want it to mention 'no head'", err.Error())
	}
}

// TestReprojectWorkingUnknownWorldFails proves an unknown world id fails loudly.
func TestReprojectWorkingUnknownWorldFails(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)
	if err := ws.ReprojectWorking(ctx, "no-such-world"); err == nil {
		t.Fatal("ReprojectWorking(unknown) returned nil error, want error")
	}
}

// TestReprojectWorkingDoesNotTouchHistory is the load-bearing invariant pin:
// reproject is WORKING-PARTITION-ONLY and must never write the head's history
// partition (keyed by the head sha256). After a commit produces a head with a
// history partition, the history partition row counts are unchanged across a
// reproject (and across the drift+reproject of the WORKING partition).
func TestReprojectWorkingDoesNotTouchHistory(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	world, err := ws.AddWorld(ctx, fixturePath(t, fixtureSmall), "world-a")
	if err != nil {
		t.Fatalf("AddWorld: %v", err)
	}
	entry := firstBibiteEntryName(t, ctx, ws, world.ID)
	if _, err := ws.CommitWorld(ctx, world.ID, setBibiteEnergy(entry, 333.0), thebibites.RunOptions{}); err != nil {
		t.Fatalf("CommitWorld: %v", err)
	}

	// Resolve the head sha256 — the key of the head's history partition.
	got, err := ws.store().GetWorld(ctx, world.ID)
	if err != nil {
		t.Fatalf("GetWorld: %v", err)
	}
	if got.HeadRevisionID == nil {
		t.Fatal("world has no head after commit")
	}
	headRev, err := ws.store().RevisionByID(ctx, *got.HeadRevisionID)
	if err != nil {
		t.Fatalf("RevisionByID(head): %v", err)
	}
	headSHA := headRev.SHA256

	// Snapshot the head's history partition row counts BEFORE reproject.
	histArchivesBefore := countBySaveID(t, ctx, ws, "save_archives", headSHA)
	histBibitesBefore := countBySaveID(t, ctx, ws, "bibites", headSHA)
	if histArchivesBefore != 1 || histBibitesBefore == 0 {
		t.Fatalf("head history partition before reproject = (archives=%d, bibites=%d), want (1, >0)", histArchivesBefore, histBibitesBefore)
	}
	// The committed (333) value must already be in the head's history partition.
	if v := bibiteEnergyInPartition(t, ctx, ws, headSHA, entry); v != 333.0 {
		t.Fatalf("head history energy before reproject = %v, want 333", v)
	}

	// Drift the WORKING partition then reproject. History must be untouched.
	emptyWorkingPartition(t, ctx, ws, world.ID)
	if err := ws.ReprojectWorking(ctx, world.ID); err != nil {
		t.Fatalf("ReprojectWorking: %v", err)
	}

	if got := countBySaveID(t, ctx, ws, "save_archives", headSHA); got != histArchivesBefore {
		t.Errorf("head history save_archives after reproject = %d, want %d (history immutable)", got, histArchivesBefore)
	}
	if got := countBySaveID(t, ctx, ws, "bibites", headSHA); got != histBibitesBefore {
		t.Errorf("head history bibites after reproject = %d, want %d (history immutable)", got, histBibitesBefore)
	}
	if v := bibiteEnergyInPartition(t, ctx, ws, headSHA, entry); v != 333.0 {
		t.Errorf("head history energy after reproject = %v, want 333 (history untouched)", v)
	}
}

// TestReprojectWorkingDropsCachedHandle proves reproject drops the cached working
// copy so a later OpenWorld lazy-reloads from the now-consistent partition.
func TestReprojectWorkingDropsCachedHandle(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	world, err := ws.AddWorld(ctx, fixturePath(t, fixtureSmall), "world-a")
	if err != nil {
		t.Fatalf("AddWorld: %v", err)
	}
	// Load to populate the cache.
	if _, err := ws.Load(ctx, world.ID); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := ws.worlds[world.ID]; !ok {
		t.Fatalf("expected cached handle after Load")
	}

	if err := ws.ReprojectWorking(ctx, world.ID); err != nil {
		t.Fatalf("ReprojectWorking: %v", err)
	}
	if _, ok := ws.worlds[world.ID]; ok {
		t.Errorf("cached handle still present after reproject; want dropped so OpenWorld reloads")
	}
}
