package thebibites

import (
	"path/filepath"
	"testing"

	"go.starlark.net/starlark"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

// pelletAt drives save.pellets[i] to obtain a *Pellet, skipping if the fixture has
// fewer pellets.
func pelletAt(t *testing.T, ls *LoadedSave, i int) *Pellet {
	t.Helper()
	ps := &Pellets{ls: ls}
	if i >= ps.Len() {
		t.Skipf("fixture has only %d pellets", ps.Len())
	}
	p, ok := ps.Index(i).(*Pellet)
	if !ok {
		t.Fatalf("pellets[%d] is %T, want *Pellet", i, ps.Index(i))
	}
	return p
}

// findPellet locates a pellet by its (group_index, group_pellet_index) locator in a
// reparsed set.
func findPellet(rows []tb.PelletRow, group, groupPellet int) (tb.PelletRow, bool) {
	for _, r := range rows {
		if r.GroupIndex == group && r.GroupPelletIndex == groupPellet {
			return r, true
		}
	}
	return tb.PelletRow{}, false
}

// TestPelletRead: save.pellets length and per-pellet scalar/locator reads match the
// normalized rows.
func TestPelletRead(t *testing.T) {
	ls := loadFixture(t)
	ps := &Pellets{ls: ls}
	if ps.Len() != len(ls.tables.Pellets) {
		t.Fatalf("pellets Len = %d, want %d", ps.Len(), len(ls.tables.Pellets))
	}
	if ps.Len() == 0 {
		t.Skip("fixture has no pellets")
	}
	p := pelletAt(t, ls, 0)
	row := ls.tables.Pellets[0]

	amt, err := p.Attr("amount")
	if err != nil {
		t.Fatalf("amount: %v", err)
	}
	if got := mustFloat(t, amt); got != row.Amount {
		t.Errorf("pellet[0].amount = %v, want %v", got, row.Amount)
	}
	mat, err := p.Attr("material")
	if err != nil {
		t.Fatalf("material: %v", err)
	}
	if got := mustString(t, mat); got != row.Material {
		t.Errorf("pellet[0].material = %q, want %q", got, row.Material)
	}
	gi, err := p.Attr("group_index")
	if err != nil {
		t.Fatalf("group_index: %v", err)
	}
	if got := mustInt(t, gi); got != int64(row.GroupIndex) {
		t.Errorf("pellet[0].group_index = %d, want %d", got, row.GroupIndex)
	}
}

// TestPelletScalarSetRoundTrips: p.amount = v writes through and persists through
// reparse (located by its group locators).
func TestPelletScalarSetRoundTrips(t *testing.T) {
	ls := loadFixture(t)
	if len(ls.tables.Pellets) == 0 {
		t.Skip("fixture has no pellets")
	}
	p := pelletAt(t, ls, 0)
	row := ls.tables.Pellets[0]
	const want = 7.25
	if err := p.SetField("amount", starlark.Float(want)); err != nil {
		t.Fatalf("SetField(amount): %v", err)
	}
	if got, _ := p.Attr("amount"); mustFloat(t, got) != want {
		t.Errorf("post-set amount = %v, want %v", mustFloat(t, got), want)
	}

	tmp := filepath.Join(t.TempDir(), "out.zip")
	if err := ls.WriteSave(tmp); err != nil {
		t.Fatalf("WriteSave: %v", err)
	}
	re, err := tb.ParseFile(tmp, nil)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	tables := tb.ExtractTables(re.SHA256, re)
	got, ok := findPellet(tables.Pellets, row.GroupIndex, row.GroupPelletIndex)
	if !ok {
		t.Fatalf("pellet (group %d, %d) missing after reparse", row.GroupIndex, row.GroupPelletIndex)
	}
	if got.Amount != want {
		t.Errorf("reparsed pellet amount = %v, want %v", got.Amount, want)
	}
}

// TestPelletScalarSetMirror: a pellet scalar set is visible to an in-run save.sql
// via the mirror (single UPDATE, DuckDB opened once).
func TestPelletScalarSetMirror(t *testing.T) {
	ls := loadFixture(t)
	if len(ls.tables.Pellets) == 0 {
		t.Skip("fixture has no pellets")
	}
	row := ls.tables.Pellets[0]
	p := pelletAt(t, ls, 0)
	const want = 9.5
	if err := p.SetField("amount", starlark.Float(want)); err != nil {
		t.Fatalf("SetField(amount): %v", err)
	}

	rows, err := ls.query(
		"SELECT amount FROM pellets WHERE save_id = ? AND group_index = ? AND group_pellet_index = ?",
		ls.saveID, row.GroupIndex, row.GroupPelletIndex)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("no pellet row from in-run query")
	}
	var got float64
	if err := rows.Scan(&got); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got != want {
		t.Errorf("in-run SQL pellet amount = %v, want %v (mirror not applied)", got, want)
	}
	if ls.dbOpenCount != 1 {
		t.Errorf("dbOpenCount = %d, want 1", ls.dbOpenCount)
	}
	if ls.flushStmtCount != 1 {
		t.Errorf("flushStmtCount = %d, want 1 (single mirror UPDATE)", ls.flushStmtCount)
	}
}

// TestPelletDeletePersists: pellet.delete() stages one op and the reparsed save has
// one fewer pellet (the mutator reconciles scene nPellets).
func TestPelletDeletePersists(t *testing.T) {
	ls := loadFixture(t)
	before := len(ls.tables.Pellets)
	if before == 0 {
		t.Skip("fixture has no pellets")
	}
	p := pelletAt(t, ls, 0)
	if _, err := callMethod(t, p, "delete"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if ls.stagedOps != 1 {
		t.Errorf("stagedOps = %d, want 1", ls.stagedOps)
	}
	tmp := filepath.Join(t.TempDir(), "out.zip")
	if err := ls.WriteSave(tmp); err != nil {
		t.Fatalf("WriteSave: %v", err)
	}
	re, err := tb.ParseFile(tmp, nil)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	tables := tb.ExtractTables(re.SHA256, re)
	if len(tables.Pellets) != before-1 {
		t.Errorf("pellet count after delete = %d, want %d", len(tables.Pellets), before-1)
	}
}

// TestPelletSetFieldRejects: unknown field, read-only locator, and wrong-typed value
// are each rejected before staging.
func TestPelletSetFieldRejects(t *testing.T) {
	ls := loadFixture(t)
	if len(ls.tables.Pellets) == 0 {
		t.Skip("fixture has no pellets")
	}
	p := pelletAt(t, ls, 0)
	if err := p.SetField("group_index", starlark.MakeInt(3)); err == nil {
		t.Error("SetField(group_index) error = nil, want rejection (read-only locator)")
	}
	if err := p.SetField("bogus", starlark.Float(1)); err == nil {
		t.Error("SetField(bogus) error = nil, want rejection (unknown attribute)")
	}
	if err := p.SetField("amount", starlark.String("lots")); err == nil {
		t.Error("SetField(amount=string) error = nil, want type rejection")
	}
	if ls.stagedOps != 0 {
		t.Errorf("stagedOps = %d after rejected sets, want 0", ls.stagedOps)
	}
}
