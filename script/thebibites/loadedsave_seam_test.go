package thebibites

import (
	"context"
	"database/sql"
	"testing"

	"go.starlark.net/starlark"

	"github.com/asemones/bibicontrol/duckdb"
	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

// openSharedDB opens an in-memory DuckDB to stand in for the per-workspace
// shared handle that LoadInto is given. The test owns it (mirroring the
// workspace), so it Closes it on cleanup — LoadInto must never close it.
func openSharedDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := duckdb.Open("")
	if err != nil {
		t.Fatalf("duckdb.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// seedWorld imports the fixture's normalized rows into db under save_id=worldID,
// standing in for the C1/C3 working-partition seed that LoadInto expects to find
// already present (B1 itself imports nothing). It returns the rows so the caller
// can assert against counts keyed by worldID.
func seedWorld(t *testing.T, db *sql.DB, worldID string) tb.ExtractedSave {
	t.Helper()
	archive, err := tb.ParseFile(fixture, nil)
	if err != nil {
		t.Fatalf("ParseFile(%s): %v", fixture, err)
	}
	tables := tb.ExtractTables(worldID, archive)
	if err := duckdb.ImportExtractedSave(context.Background(), db, tables); err != nil {
		t.Fatalf("ImportExtractedSave(%s): %v", worldID, err)
	}
	return tables
}

// countBibites reads the bibite row count for a given save_id partition through
// the injected handle (the analytics read path that LoadInto wires).
func countBibites(t *testing.T, ls *LoadedSave, saveID string) int {
	t.Helper()
	rows, err := ls.query("SELECT count(*) AS n FROM bibites WHERE save_id = ?", saveID)
	if err != nil {
		t.Fatalf("count query: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("count query returned no row")
	}
	var n int
	if err := rows.Scan(&n); err != nil {
		t.Fatalf("scan count: %v", err)
	}
	return n
}

// TestLoadIntoSetsWorldIDAndInjectsDB proves LoadInto pins the partition key to
// the explicit world id, reuses the injected handle (same pointer), reads the
// seeded working partition through it, and never runs the in-memory lazy import.
func TestLoadIntoSetsWorldIDAndInjectsDB(t *testing.T) {
	db := openSharedDB(t)
	seeded := seedWorld(t, db, "world-1")

	ls, err := LoadInto(fixture, "world-1", db)
	if err != nil {
		t.Fatalf("LoadInto: %v", err)
	}
	if ls.saveID != "world-1" {
		t.Errorf("ls.saveID = %q, want %q", ls.saveID, "world-1")
	}
	if ls.db != db {
		t.Error("ls.db is not the injected *sql.DB (LoadInto must reuse the shared handle)")
	}
	// The in-memory ExtractedSave that backs reads/mirror is stamped with worldID,
	// not the file hash, so it matches the DuckDB partition the workspace seeded.
	if ls.tables.Archive.SaveID != "world-1" {
		t.Errorf("ls.tables.Archive.SaveID = %q, want %q", ls.tables.Archive.SaveID, "world-1")
	}

	if got, want := countBibites(t, ls, "world-1"), len(ls.tables.Bibites); got != want {
		t.Errorf("bibite count via injected db = %d, want %d (in-memory tables)", got, want)
	}
	if got, want := len(ls.tables.Bibites), len(seeded.Bibites); got != want {
		t.Errorf("in-memory bibites = %d, want %d (seeded partition)", got, want)
	}

	// The injected path short-circuits openDB before the import branch, so the
	// in-memory lazy-open counter stays 0. (Do not move the increment above the
	// ls.db != nil early return.)
	if ls.dbOpenCount != 0 {
		t.Errorf("ls.dbOpenCount = %d, want 0 (injected db must not run the in-memory import)", ls.dbOpenCount)
	}
}

// TestLoadIntoMirrorRoundTripScopedToWorld proves a mutation through the Save
// object write-throughs into the injected DB scoped to save_id = worldID: the
// mutated value is observed via a read on world-1, while a sibling world-2
// partition in the same shared handle is untouched (mirror scoping isolates
// worlds).
func TestLoadIntoMirrorRoundTripScopedToWorld(t *testing.T) {
	db := openSharedDB(t)
	seedWorld(t, db, "world-1")
	seedWorld(t, db, "world-2")

	ls, err := LoadInto(fixture, "world-1", db)
	if err != nil {
		t.Fatalf("LoadInto: %v", err)
	}

	target := firstBibiteEntry(t, ls)
	before2 := bibiteEnergyForWorld(t, ls, "world-2", target)

	const newEnergy = 4321.0
	e := &Entity{ls: ls, kind: "bibite", entryName: target}
	if err := e.SetField("energy", starlark.Float(newEnergy)); err != nil {
		t.Fatalf("SetField energy: %v", err)
	}

	// Read back through DuckDB so flushMirror runs and the write-through is
	// observed on world-1.
	if got := bibiteEnergyForWorld(t, ls, "world-1", target); got != newEnergy {
		t.Errorf("world-1 energy after set = %v, want %v (mirror write-through)", got, newEnergy)
	}
	// world-2 is a different partition in the same handle; mirror scoping (save_id
	// = worldID) must leave it untouched.
	if got := bibiteEnergyForWorld(t, ls, "world-2", target); got != before2 {
		t.Errorf("world-2 energy = %v, want %v (other world must not be mutated)", got, before2)
	}
}

// bibiteEnergyForWorld reads one bibite's energy from the given save_id partition
// through the injected handle.
func bibiteEnergyForWorld(t *testing.T, ls *LoadedSave, saveID, entryName string) float64 {
	t.Helper()
	rows, err := ls.query("SELECT energy FROM bibites WHERE save_id = ? AND entry_name = ?", saveID, entryName)
	if err != nil {
		t.Fatalf("energy query (%s): %v", saveID, err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("no bibite row for %q in %q", entryName, saveID)
	}
	var v float64
	if err := rows.Scan(&v); err != nil {
		t.Fatalf("scan energy: %v", err)
	}
	return v
}

// TestLoadIntoRejectsEmptyWorldOrNilDB proves the seam fails loud on the
// programming errors the workspace must never make: an empty world id or a nil
// shared handle.
func TestLoadIntoRejectsEmptyWorldOrNilDB(t *testing.T) {
	db := openSharedDB(t)

	if _, err := LoadInto(fixture, "", db); err == nil {
		t.Error("LoadInto with empty worldID returned nil error, want rejection")
	}
	if _, err := LoadInto(fixture, "world-1", nil); err == nil {
		t.Error("LoadInto with nil db returned nil error, want rejection")
	}
}

// TestLoadStillDerivesHashKeyAndLazyOpens guards the standalone path: Load keeps
// deriving the partition key from the archive hash and leaves db nil so the first
// query runs the in-memory lazy import (dbOpenCount == 1).
func TestLoadStillDerivesHashKeyAndLazyOpens(t *testing.T) {
	ls := loadFixture(t)
	if ls.db != nil {
		t.Error("Load left ls.db non-nil, want nil (lazy in-memory open)")
	}
	archive, err := tb.ParseFile(fixture, nil)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	wantKey := archive.SHA256
	if wantKey == "" {
		wantKey = archive.FileName
	}
	if ls.saveID != wantKey {
		t.Errorf("Load saveID = %q, want hash-derived %q", ls.saveID, wantKey)
	}

	rows, err := ls.query("SELECT count(*) AS n FROM bibites WHERE save_id = ?", ls.saveID)
	if err != nil {
		t.Fatalf("count query: %v", err)
	}
	rows.Close()
	if ls.dbOpenCount != 1 {
		t.Errorf("standalone dbOpenCount = %d, want 1 (in-memory lazy open)", ls.dbOpenCount)
	}
}
