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

// clonePellet drives save.pellets.clone(i) and asserts a *PendingPellet.
func clonePellet(t *testing.T, ls *LoadedSave, i int) *PendingPellet {
	t.Helper()
	cv, err := callMethod(t, &Pellets{ls: ls}, "clone", starlark.MakeInt(i))
	if err != nil {
		t.Fatalf("clone(%d): %v", i, err)
	}
	pp, ok := cv.(*PendingPellet)
	if !ok {
		t.Fatalf("clone(%d) returned %T, want *PendingPellet", i, cv)
	}
	return pp
}

// TestPelletCloneAppendPersists: clone a pellet, edit a few scalars, append to a
// group, and confirm the reparsed save gained the pellet — with the edited scalars
// AND the template's physics (rb2d) inherited verbatim by the deep copy.
func TestPelletCloneAppendPersists(t *testing.T) {
	ls := loadFixture(t)
	if len(ls.tables.Pellets) == 0 {
		t.Skip("fixture has no pellets")
	}
	tmpl := ls.tables.Pellets[0]
	group := tmpl.GroupIndex
	before := len(ls.tables.Pellets)
	inGroup := 0 // current size of the target group == the appended element's gpi
	for _, r := range ls.tables.Pellets {
		if r.EntryName == tmpl.EntryName && r.GroupIndex == group {
			inGroup++
		}
	}

	pp := clonePellet(t, ls, 0)
	const (
		wantMat = "ClonedMeat"
		wantAmt = 42.5
		wantX   = 123.5
		wantY   = -67.25
	)
	edits := []struct {
		name string
		val  starlark.Value
	}{
		{"material", starlark.String(wantMat)},
		{"amount", starlark.Float(wantAmt)},
		{"position_x", starlark.Float(wantX)},
		{"position_y", starlark.Float(wantY)},
	}
	for _, e := range edits {
		if err := pp.SetField(e.name, e.val); err != nil {
			t.Fatalf("SetField(%s): %v", e.name, err)
		}
	}
	if _, err := callMethod(t, pp, "append", starlark.String(tmpl.Zone)); err != nil {
		t.Fatalf("append: %v", err)
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
	if len(tables.Pellets) != before+1 {
		t.Errorf("pellet count after append = %d, want %d", len(tables.Pellets), before+1)
	}
	got, ok := findPellet(tables.Pellets, group, inGroup)
	if !ok {
		t.Fatalf("appended pellet (group %d, gpi %d) missing after reparse", group, inGroup)
	}
	if got.Material != wantMat {
		t.Errorf("material = %q, want %q", got.Material, wantMat)
	}
	if got.Amount != wantAmt {
		t.Errorf("amount = %v, want %v", got.Amount, wantAmt)
	}
	if got.TransformPositionX != wantX || got.TransformPositionY != wantY {
		t.Errorf("position = (%v,%v), want (%v,%v)", got.TransformPositionX, got.TransformPositionY, wantX, wantY)
	}
	if got.RB2DPX != tmpl.RB2DPX {
		t.Errorf("inherited rb2d_px = %v, want %v (template copied verbatim)", got.RB2DPX, tmpl.RB2DPX)
	}

	// Churn DoD: a clone-append commit does exactly one archive write, no reparse.
	if ls.writeArchiveCount > 1 {
		t.Errorf("writeArchiveCount = %d, want <= 1", ls.writeArchiveCount)
	}
	if ls.reparseCount != 0 {
		t.Errorf("reparseCount = %d, want 0", ls.reparseCount)
	}
}

// TestPelletCloneEditReadBack: editing a nested scalar on a pending pellet is
// observable through a read (the local nested setter+getter round trip), and no op
// is staged until append().
func TestPelletCloneEditReadBack(t *testing.T) {
	ls := loadFixture(t)
	if len(ls.tables.Pellets) == 0 {
		t.Skip("fixture has no pellets")
	}
	pp := clonePellet(t, ls, 0)
	if err := pp.SetField("position_x", starlark.Float(314.0)); err != nil {
		t.Fatalf("SetField(position_x): %v", err)
	}
	if err := pp.SetField("material", starlark.String("Glass")); err != nil {
		t.Fatalf("SetField(material): %v", err)
	}
	if gx, _ := pp.Attr("position_x"); mustFloat(t, gx) != 314.0 {
		t.Errorf("read-back position_x = %v, want 314 (transform.position[0])", mustFloat(t, gx))
	}
	if gm, _ := pp.Attr("material"); mustString(t, gm) != "Glass" {
		t.Errorf("read-back material = %q, want Glass (pellet.material)", mustString(t, gm))
	}
	if ls.stagedOps != 0 {
		t.Errorf("stagedOps = %d before append, want 0 (edits only mutate the copy)", ls.stagedOps)
	}
}

// TestPelletCloneIntegralScalarStagesFloat: setting an integral value on a DOUBLE
// pellet field through the pending (clone) path must coerce to a float in the cloned
// JSON, identical to the committed Pellet.SetField path (setRowField -> float64).
// Guards F1: the pending write used to stage the raw integer goVal, diverging from
// the committed path's on-disk JSON type fidelity.
func TestPelletCloneIntegralScalarStagesFloat(t *testing.T) {
	ls := loadFixture(t)
	if len(ls.tables.Pellets) == 0 {
		t.Skip("fixture has no pellets")
	}
	pp := clonePellet(t, ls, 0)
	// amount is a DOUBLE column; assigning a Starlark int must land as a float64 in
	// the clone's JSON, mirroring the committed path.
	if err := pp.SetField("amount", starlark.MakeInt(5)); err != nil {
		t.Fatalf("SetField(amount=int): %v", err)
	}
	spec, ok := pelletRegistry()["amount"]
	if !ok {
		t.Fatal("pelletRegistry has no amount spec")
	}
	v, ok := getNestedPellet(pp.data, spec.jsonKey)
	if !ok {
		t.Fatalf("amount path %q missing in clone", spec.jsonKey)
	}
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("clone amount staged as %T (%v), want float64 (matches committed setRowField path)", v, v)
	}
	if f != 5.0 {
		t.Errorf("clone amount = %v, want 5.0", f)
	}
}

// TestPelletCloneAppendNotMirrored: a clone-append is structural — staged but not
// mirrored, so it is invisible to in-run reads/queries until commit.
func TestPelletCloneAppendNotMirrored(t *testing.T) {
	ls := loadFixture(t)
	if len(ls.tables.Pellets) == 0 {
		t.Skip("fixture has no pellets")
	}
	before := len(ls.tables.Pellets)
	zone := ls.tables.Pellets[0].Zone
	pp := clonePellet(t, ls, 0)
	if _, err := callMethod(t, pp, "append", starlark.String(zone)); err != nil {
		t.Fatalf("append: %v", err)
	}
	if len(ls.tables.Pellets) != before {
		t.Errorf("tables.Pellets grew to %d, want %d (append is structural)", len(ls.tables.Pellets), before)
	}
	rows, err := ls.query("SELECT count(*) FROM pellets WHERE save_id = ?", ls.saveID)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("no count row")
	}
	var n int
	if err := rows.Scan(&n); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if n != before {
		t.Errorf("in-run pellet count = %d, want %d (append must not be mirrored)", n, before)
	}
}

// TestPelletCloneRejects: out-of-range clone, editing a read-only locator on a
// pending pellet, append to a missing group, and a second append are each rejected.
func TestPelletCloneRejects(t *testing.T) {
	ls := loadFixture(t)
	if len(ls.tables.Pellets) == 0 {
		t.Skip("fixture has no pellets")
	}
	ps := &Pellets{ls: ls}
	if _, err := callMethod(t, ps, "clone", starlark.MakeInt(len(ls.tables.Pellets))); err == nil {
		t.Error("clone(out-of-range) error = nil, want rejection")
	}
	pp := clonePellet(t, ls, 0)
	if err := pp.SetField("group_index", starlark.MakeInt(1)); err == nil {
		t.Error("SetField(group_index) on pending error = nil, want read-only rejection")
	}
	if _, err := callMethod(t, pp, "append", starlark.String("__no_such_zone__")); err == nil {
		t.Error("append(zone=unknown) error = nil, want unknown-zone rejection")
	}
	zone := ls.tables.Pellets[0].Zone
	if _, err := callMethod(t, pp, "append", starlark.String(zone)); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := callMethod(t, pp, "append", starlark.String(zone)); err == nil {
		t.Error("second append error = nil, want already-appended rejection")
	}
}
