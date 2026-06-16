package thebibites

import (
	"path/filepath"
	"testing"

	"go.starlark.net/starlark"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

// bibiteRowPositionX finds a bibite's transform_position_x in a normalized set.
func bibiteRowPositionX(t *testing.T, tables tb.ExtractedSave, entry string) float64 {
	t.Helper()
	for _, b := range tables.Bibites {
		if b.EntryName == entry {
			return b.TransformPositionX
		}
	}
	t.Fatalf("no bibite %q in reparsed save", entry)
	return 0
}

// TestAliasWritablePositionX is the risk #1 regression: a writable friendly alias
// (position_x -> transform_position_x) must stage, mirror, and persist against the
// real generated column. Before the sourceColumn split, SetField set ref.Column =
// "position_x", which the mutator could not resolve.
func TestAliasWritablePositionX(t *testing.T) {
	ls := loadFixture(t)
	entry := firstBibiteEntry(t, ls)

	// The alias is registered for both kinds and keeps the source column readable.
	if _, ok := attrRegistry()["bibite"]["position_x"]; !ok {
		t.Fatal("position_x alias not registered for bibite")
	}
	if spec := attrRegistry()["bibite"]["position_x"]; spec.sourceColumn != "transform_position_x" {
		t.Fatalf("position_x sourceColumn = %q, want transform_position_x", spec.sourceColumn)
	}

	e := &Entity{ls: ls, kind: "bibite", entryName: entry}
	const want = -17.5 // signed: positions are not in the non-negative guard set
	if err := e.SetField("position_x", starlark.Float(want)); err != nil {
		t.Fatalf("SetField(position_x): %v", err)
	}
	// Write-through via the alias and via the source column both observe it.
	if got := attrFloat(t, e, "position_x"); got != want {
		t.Errorf("b.position_x = %v, want %v", got, want)
	}
	if got := attrFloat(t, e, "transform_position_x"); got != want {
		t.Errorf("b.transform_position_x = %v, want %v (source column read)", got, want)
	}

	// In-run SQL observes the mirror on the real column.
	rows, err := ls.query("SELECT transform_position_x FROM bibites WHERE save_id = ? AND entry_name = ?", ls.saveID, entry)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	var sqlVal float64
	if rows.Next() {
		if err := rows.Scan(&sqlVal); err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
	}
	rows.Close()
	if sqlVal != want {
		t.Errorf("in-run SQL transform_position_x = %v, want %v", sqlVal, want)
	}

	tmp := filepath.Join(t.TempDir(), "out.zip")
	if err := ls.WriteSave(tmp); err != nil {
		t.Fatalf("WriteSave: %v", err)
	}
	re, err := tb.ParseFile(tmp, nil)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if got := bibiteRowPositionX(t, tb.ExtractTables(re.SHA256, re), entry); got != want {
		t.Errorf("reparsed transform_position_x = %v, want %v", got, want)
	}
}

// TestAliasAggregateResolves: an aggregate over a friendly alias compiles against
// the real source column and matches the raw-SQL equivalent.
func TestAliasAggregateResolves(t *testing.T) {
	ls := loadFixture(t)
	coll := &EntityCollection{ls: ls, kind: "bibite"}

	res, err := callMethod(t, coll, "mean", starlark.String("position_x"))
	if err != nil {
		t.Fatalf("mean(position_x): %v", err)
	}
	viaAlias := mustFloat(t, res)

	rows, err := ls.query("SELECT avg(transform_position_x) FROM bibites WHERE save_id = ?", ls.saveID)
	if err != nil {
		t.Fatalf("raw query: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("no aggregate row")
	}
	var viaRaw float64
	if err := rows.Scan(&viaRaw); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !approxEqual(viaAlias, viaRaw) {
		t.Errorf("mean(position_x) = %v, raw avg(transform_position_x) = %v", viaAlias, viaRaw)
	}
}

// TestAliasBulkSetResolves: a bulk where(...).set over a friendly alias stages
// against the real source column and persists to DuckDB.
func TestAliasBulkSetResolves(t *testing.T) {
	ls := loadFixture(t)
	coll := &EntityCollection{ls: ls, kind: "bibite"}
	const want = -3.5
	res, err := callMethod(t, coll, "set", starlark.String("rotation"), starlark.Float(want))
	if err != nil {
		t.Fatalf("bulk set(rotation): %v", err)
	}
	if staged := mustInt(t, res); staged <= 0 {
		t.Fatalf("bulk set staged %d rows", staged)
	}

	rows, err := ls.query("SELECT count(*) FROM bibites WHERE save_id = ? AND transform_rotation = ?", ls.saveID, want)
	if err != nil {
		t.Fatalf("verify query: %v", err)
	}
	defer rows.Close()
	rows.Next()
	var got int
	if err := rows.Scan(&got); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got != len(ls.tables.Bibites) {
		t.Errorf("%d of %d bibites at new rotation", got, len(ls.tables.Bibites))
	}
}
