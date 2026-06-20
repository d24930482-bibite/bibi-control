package thebibites

import (
	"strings"
	"testing"

	"go.starlark.net/starlark"
)

// ---------------------------------------------------------------------------
// M7 bullet 1 — write NULL (b.field = None, round-trip b.field = b.field).
// ---------------------------------------------------------------------------

// TestFromStarlarkNullable pins the binding-layer split: the nullable helper maps
// None to (nil, true, nil) and delegates every other scalar to fromStarlark, while
// fromStarlark itself still REJECTS None (regression guard for its 9 callers that
// have no nil handling).
func TestFromStarlarkNullable(t *testing.T) {
	v, isNull, err := fromStarlarkNullable(starlark.None)
	if err != nil || !isNull || v != nil {
		t.Errorf("fromStarlarkNullable(None) = (%v, %v, %v), want (nil, true, nil)", v, isNull, err)
	}
	v, isNull, err = fromStarlarkNullable(starlark.MakeInt(5))
	if err != nil || isNull {
		t.Errorf("fromStarlarkNullable(5) = (%v, %v, %v), want (5, false, nil)", v, isNull, err)
	}
	if n, ok := v.(int64); !ok || n != 5 {
		t.Errorf("fromStarlarkNullable(5) value = %v (%T), want int64(5)", v, v)
	}
	// fromStarlark must still reject None — the existing callers depend on it.
	if _, err := fromStarlark(starlark.None); err == nil {
		t.Error("fromStarlark(None) returned nil error; it must still reject None")
	}
}

// TestSetFieldNullWritesAndRoundTrips: b.field = None stages a set whose staged
// value is nil (a JSON null at commit). The in-memory read-back is the zeroed cell
// (0.0 here), NOT None — None is reserved for an absent 1:1 row (documented
// read-back caveat). The round-trip b.field = b.field then works (assigning the
// read-back value, whether scalar or None, is no longer rejected at fromStarlark).
func TestSetFieldNullWritesAndRoundTrips(t *testing.T) {
	ls := loadFixture(t)
	name := firstBibiteEntry(t, ls)
	e := &Entity{ls: ls, kind: "bibite", entryName: name}

	if err := e.SetField("energy", starlark.None); err != nil {
		t.Fatalf("b.energy = None: %v", err)
	}
	if ls.stagedOps != 1 {
		t.Fatalf("stagedOps = %d after null write, want 1", ls.stagedOps)
	}

	// The staged mirror value for energy is nil (-> SQL NULL / JSON null).
	key := mirrorKey{table: "bibites", column: "energy"}
	col := ls.mirror.cols[key]
	if col == nil {
		t.Fatal("no mirror buffered for bibites.energy after null write")
	}
	if len(col.rows) != 1 {
		t.Fatalf("mirror rows = %d, want 1", len(col.rows))
	}
	for _, r := range col.rows {
		if r.value != nil {
			t.Errorf("staged mirror value = %v (%T), want nil (JSON null)", r.value, r.value)
		}
	}

	// In-memory read-back: the present row was zeroed (the documented semantics), so
	// it reads back 0.0 — NOT None.
	got, err := e.Attr("energy")
	if err != nil {
		t.Fatalf("read-back energy: %v", err)
	}
	if got == starlark.None {
		t.Error("read-back of a null-written present row is None; want the zeroed cell (0.0)")
	}
	if f := mustFloat(t, got); f != 0 {
		t.Errorf("read-back energy = %v, want 0 (zeroed cell)", f)
	}

	// Round-trip: assign the read-back value straight back; no rejection.
	if err := e.SetField("energy", got); err != nil {
		t.Fatalf("round-trip b.energy = b.energy after null write: %v", err)
	}
}

// TestSetFieldNullRoundTripFromAbsentRow: a field that READS None (an absent 1:1
// sub-table row) round-trips b.f = b.f without error — the fromStarlark None
// rejection that used to break this is gone on the entity scalar surface. The set
// is a no-op clear (nil -> the column is already null), but it must not error.
func TestSetFieldNullRoundTripFromAbsentRow(t *testing.T) {
	ls := loadFixture(t)
	// Find a (kind, attr) whose 1:1 sub-table row is absent for some entity, so the
	// attr reads None. Walk the registry for a writable scalar on a sub-table.
	for kind, attrs := range attrRegistry() {
		ta := ls.access[identityTableMust(t, kind)]
		if ta == nil || len(ta.order) == 0 {
			continue
		}
		for attrName, spec := range attrs {
			if !spec.writable || spec.table == identityTableMust(t, kind) {
				continue
			}
			for _, entry := range ta.order {
				if _, ok := ls.rowForEntry(spec.table, entry); ok {
					continue // row present -> reads a scalar, not None
				}
				e := &Entity{ls: ls, kind: kind, entryName: entry}
				v, err := e.Attr(attrName)
				if err != nil {
					t.Fatalf("%s.%s read: %v", kind, attrName, err)
				}
				if v != starlark.None {
					continue
				}
				// v is None; assigning it back must NOT error (it routes through the
				// nullable path). A null write needs the row present to zero it, so an
				// absent row's set will fail at rowForEntry — which is a clean,
				// non-fromStarlark error. We only assert fromStarlark no longer rejects
				// None on this surface: the error (if any) must not be the old
				// "cannot set attribute to NoneType".
				err = e.SetField(attrName, v)
				if err != nil && strings.Contains(err.Error(), "NoneType") {
					t.Errorf("%s.%s = None still rejected at fromStarlark: %v", kind, attrName, err)
				}
				return
			}
		}
	}
	t.Skip("fixture has no entity with an absent optional 1:1 row")
}

// identityTableMust resolves a kind's identity table or fails the test.
func identityTableMust(t *testing.T, kind string) string {
	t.Helper()
	tbl, err := identityTable(kind)
	if err != nil {
		t.Fatalf("identityTable(%q): %v", kind, err)
	}
	return tbl
}

// ---------------------------------------------------------------------------
// M7 bullet 2 — b.delete() returns a count.
// ---------------------------------------------------------------------------

// TestEntityDeleteReturnsCount: a single-entity delete returns int 1 (the count
// staged), aligning with the bulk where(...).delete() count contract — it used to
// return None.
func TestEntityDeleteReturnsCount(t *testing.T) {
	ls := loadFixture(t)
	e := &Entity{ls: ls, kind: "bibite", entryName: firstBibiteEntry(t, ls)}

	res, err := callMethod(t, e, "delete")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if res == starlark.None {
		t.Fatal("b.delete() returned None, want int 1 (count contract)")
	}
	if got := mustInt(t, res); got != 1 {
		t.Errorf("b.delete() = %d, want 1", got)
	}
}

// ---------------------------------------------------------------------------
// M7 bullet 3 — species_id referential guard.
// ---------------------------------------------------------------------------

// badSpeciesID returns an id that no species row in the fixture carries.
func badSpeciesID(t *testing.T, ls *LoadedSave) int64 {
	t.Helper()
	if len(ls.tables.Species) == 0 {
		t.Skip("fixture has no species")
	}
	max := int64(0)
	for i := range ls.tables.Species {
		if s := &ls.tables.Species[i]; s.HasSpeciesID && s.SpeciesID > max {
			max = s.SpeciesID
		}
	}
	return max + 1000 // guaranteed beyond every existing id
}

// goodSpeciesID returns an id that an existing species row carries.
func goodSpeciesID(t *testing.T, ls *LoadedSave) int64 {
	t.Helper()
	for i := range ls.tables.Species {
		if s := &ls.tables.Species[i]; s.HasSpeciesID {
			return s.SpeciesID
		}
	}
	t.Skip("fixture has no id-bearing species")
	return 0
}

// TestSetFieldSpeciesIDReferentialGuard: a scalar b.species_id = <nonexistent id>
// is rejected (diagnostic names species_id and the bad id) and nothing stages; an
// existing id is accepted.
func TestSetFieldSpeciesIDReferentialGuard(t *testing.T) {
	ls := loadFixture(t)
	e := &Entity{ls: ls, kind: "bibite", entryName: firstBibiteEntry(t, ls)}
	bad := badSpeciesID(t, ls)

	err := e.SetField("species_id", starlark.MakeInt64(bad))
	if err == nil {
		t.Fatalf("b.species_id = %d (nonexistent) was accepted, want rejection", bad)
	}
	if !strings.Contains(err.Error(), "species_id") {
		t.Errorf("diagnostic %q does not name species_id", err.Error())
	}
	if ls.stagedOps != 0 {
		t.Errorf("stagedOps = %d after rejected species_id set, want 0", ls.stagedOps)
	}

	good := goodSpeciesID(t, ls)
	if err := e.SetField("species_id", starlark.MakeInt64(good)); err != nil {
		t.Errorf("b.species_id = %d (existing) was rejected: %v", good, err)
	}
	if ls.stagedOps != 1 {
		t.Errorf("stagedOps = %d after one valid species_id set, want 1", ls.stagedOps)
	}
}

// TestSetFieldSpeciesIDNullRejected: species_id is not nullable; b.species_id = None
// must be rejected with a clear message and stage nothing (it must not slip past the
// guard as an absence clear).
func TestSetFieldSpeciesIDNullRejected(t *testing.T) {
	ls := loadFixture(t)
	e := &Entity{ls: ls, kind: "bibite", entryName: firstBibiteEntry(t, ls)}

	err := e.SetField("species_id", starlark.None)
	if err == nil {
		t.Fatal("b.species_id = None was accepted, want rejection (species_id is not nullable)")
	}
	if !strings.Contains(err.Error(), "species_id") {
		t.Errorf("diagnostic %q does not name species_id", err.Error())
	}
	if ls.stagedOps != 0 {
		t.Errorf("stagedOps = %d after rejected null species_id, want 0", ls.stagedOps)
	}
}

// TestBulkSetSpeciesIDReferentialGuard: bulkSet("bibite", pred, "species_id", bad)
// is rejected BEFORE the query (the value is a single constant, mirroring
// TestBulkSetValidatesBeforeQuery), even when the predicate matches zero rows;
// nothing stages. A valid id is accepted.
func TestBulkSetSpeciesIDReferentialGuard(t *testing.T) {
	ls := loadFixture(t)
	bad := badSpeciesID(t, ls)

	// Zero-match predicate: the referential check still rejects up front.
	if _, err := ls.bulkSet("bibite", "species_id == -999999", "species_id", starlark.MakeInt64(bad)); err == nil {
		t.Error("zero-match bulk species_id set with a nonexistent id: expected rejection, got nil")
	}
	// Matching predicate: the same bad id is rejected before any stage.
	if _, err := ls.bulkSet("bibite", "species_id >= 0", "species_id", starlark.MakeInt64(bad)); err == nil {
		t.Error("matching bulk species_id set with a nonexistent id: expected rejection, got nil")
	}
	if ls.stagedOps != 0 {
		t.Errorf("stagedOps = %d after rejected bulk species_id sets, want 0", ls.stagedOps)
	}
	if ls.flushStmtCount != 0 {
		t.Errorf("flushStmtCount = %d after rejected bulk species_id sets, want 0", ls.flushStmtCount)
	}

	good := goodSpeciesID(t, ls)
	n, err := ls.bulkSet("bibite", "species_id >= 0", "species_id", starlark.MakeInt64(good))
	if err != nil {
		t.Fatalf("bulk species_id set to an existing id: %v", err)
	}
	if n <= 0 {
		t.Errorf("bulk species_id set staged %d rows, want > 0", n)
	}
}

// TestBulkSetExprSpeciesIDReferentialGuard: a per-row set_expr that computes a
// nonexistent species_id is rejected POST-compute (the guard runs on each computed
// value), and nothing persists. Using a constant-valued expression keeps the
// computed value deterministic without depending on per-row data.
func TestBulkSetExprSpeciesIDReferentialGuard(t *testing.T) {
	ls := loadFixture(t)
	bad := badSpeciesID(t, ls)

	expr := strings.TrimSpace(itoa64(bad)) // an integer literal expression
	if _, err := ls.bulkSetExpr("bibite", "species_id >= 0", "species_id", expr); err == nil {
		t.Errorf("bulk set_expr computing a nonexistent species_id (%s): expected rejection, got nil", expr)
	}
	if ls.stagedOps != 0 {
		t.Errorf("stagedOps = %d after rejected bulk set_expr, want 0", ls.stagedOps)
	}

	// A constant expression that equals an EXISTING species id is accepted.
	good := goodSpeciesID(t, ls)
	n, err := ls.bulkSetExpr("bibite", "species_id >= 0", "species_id", itoa64(good))
	if err != nil {
		t.Fatalf("bulk set_expr computing an existing species_id (%d): %v", good, err)
	}
	if n <= 0 {
		t.Errorf("bulk set_expr staged %d rows, want > 0", n)
	}
}

// itoa64 renders an int64 as a base-10 string for an integer-literal SQL expression.
func itoa64(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// ---------------------------------------------------------------------------
// M7 bullet 4 — structural-edit in-run read is LOUD; scalar sets stay readable.
// ---------------------------------------------------------------------------

// TestScalarSetStillReadableAfterMirror is the bullet-4 regression: a pure scalar
// set is MIRRORED, so an in-run read-after-write must still succeed (the structural
// guard must NOT fire on a scalar set — it keys off a dedicated flag, never the
// shared stagedOps counter that scalar sets also bump).
func TestScalarSetStillReadableAfterMirror(t *testing.T) {
	ls := loadFixture(t)
	name := firstBibiteEntry(t, ls)
	e := &Entity{ls: ls, kind: "bibite", entryName: name}

	const want = 4242.0
	if err := e.SetField("energy", starlark.Float(want)); err != nil {
		t.Fatalf("set energy: %v", err)
	}
	if ls.structuralStaged {
		t.Fatal("scalar set wrongly armed the structural-read guard")
	}
	// The in-run read observes the mirrored value — it is NOT blocked.
	if got := bibiteEnergySQL(t, ls, name); got != want {
		t.Errorf("in-run energy read = %v, want %v (scalar set must stay readable)", got, want)
	}
	// And a narrowed count over the mirrored value still works.
	bibites := &EntityCollection{ls: ls, kind: "bibite"}
	narrowed, err := callMethod(t, bibites, "where", starlark.String("energy == 4242"))
	if err != nil {
		t.Fatalf("where: %v", err)
	}
	cnt, err := callMethod(t, narrowed.(*EntityCollection), "count")
	if err != nil {
		t.Fatalf("count after scalar set: %v (structural guard must not fire)", err)
	}
	if got := mustInt(t, cnt); got < 1 {
		t.Errorf("count of energy==4242 = %d, want >= 1", got)
	}
}

// TestStructuralStageThenReadErrorsViaCollection: after a where(...).delete() (the
// bulk structural path), a subsequent collection .count() / aggregate also fails
// loudly — proving the guard covers every push-down read route, not just raw
// ls.query.
func TestStructuralStageThenReadErrorsViaCollection(t *testing.T) {
	ls := loadFixture(t)
	bibites := &EntityCollection{ls: ls, kind: "bibite"}
	if bibites.Len() < 2 {
		t.Skip("need >= 2 bibites")
	}

	all, err := callMethod(t, bibites, "where", starlark.String("true"))
	if err != nil {
		t.Fatalf("where(true): %v", err)
	}
	fc := all.(*EntityCollection)
	if _, err := callMethod(t, fc, "delete", starlark.Bool(true)); err != nil {
		t.Fatalf("delete(prune=True): %v", err)
	}

	// A fresh narrowed read after the staged bulk delete must refuse loudly.
	narrowed, err := callMethod(t, bibites, "where", starlark.String("energy >= 0"))
	if err != nil {
		t.Fatalf("where(energy>=0): %v", err)
	}
	if _, err := callMethod(t, narrowed.(*EntityCollection), "count"); err == nil {
		t.Fatal("count after a staged bulk delete returned nil error, want a loud refusal")
	} else if !strings.Contains(err.Error(), "structural edit") {
		t.Errorf("post-delete count error = %q, want it to mention the structural edit", err.Error())
	}

	// save.sql also refuses.
	s := &Save{ls: ls}
	if _, err := callMethod(t, s, "sql", starlark.String("SELECT count(*) FROM bibites")); err == nil {
		t.Fatal("save.sql after a staged bulk delete returned nil error, want a loud refusal")
	}
}
