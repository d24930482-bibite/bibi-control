package thebibites

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.starlark.net/starlark"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
	"github.com/asemones/bibicontrol/script"
)

// firstBibiteEntry returns the entry name of the first bibite in save order.
func firstBibiteEntry(t *testing.T, ls *LoadedSave) string {
	t.Helper()
	ta := ls.access["bibites"]
	if ta == nil || len(ta.order) == 0 {
		t.Fatal("fixture has no bibites")
	}
	return ta.order[0]
}

// bibiteEnergySQL reads one bibite's energy back through DuckDB (the analytics
// read path), so it observes any mirrored in-run mutation.
func bibiteEnergySQL(t *testing.T, ls *LoadedSave, entryName string) float64 {
	t.Helper()
	rows, err := ls.query("SELECT energy FROM bibites WHERE save_id = ? AND entry_name = ?", ls.saveID, entryName)
	if err != nil {
		t.Fatalf("energy query: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("no bibite row for %q", entryName)
	}
	var v float64
	if err := rows.Scan(&v); err != nil {
		t.Fatalf("scan energy: %v", err)
	}
	return v
}

// bibiteRowEnergy finds a bibite's energy in a normalized table set by entry name.
func bibiteRowEnergy(t *testing.T, tables tb.ExtractedSave, entryName string) float64 {
	t.Helper()
	for _, b := range tables.Bibites {
		if b.EntryName == entryName {
			return b.Energy
		}
	}
	t.Fatalf("no bibite %q in reparsed save", entryName)
	return 0
}

func mustInt(t *testing.T, v starlark.Value) int64 {
	t.Helper()
	i, ok := v.(starlark.Int)
	if !ok {
		t.Fatalf("value is %T, want starlark.Int", v)
	}
	n, ok := i.Int64()
	if !ok {
		t.Fatalf("int %s overflows int64", i.String())
	}
	return n
}

// TestSetFieldPersists: a per-entity scalar set writes a temp save whose reparse
// shows the changed field, while an unrelated entry keeps its SHA256 byte-for-byte.
func TestSetFieldPersists(t *testing.T) {
	ls := loadFixture(t)
	orig, err := tb.ParseFile(fixture, nil)
	if err != nil {
		t.Fatalf("reference parse: %v", err)
	}

	order := ls.access["bibites"].order
	if len(order) < 2 {
		t.Fatal("fixture needs at least two bibites")
	}
	target, other := order[0], order[1]

	e := &Entity{ls: ls, kind: "bibite", entryName: target}
	const newEnergy = 4242.5
	if err := e.SetField("energy", starlark.Float(newEnergy)); err != nil {
		t.Fatalf("SetField: %v", err)
	}
	// Write-through: a plain attribute read observes the new value immediately.
	if got := attrFloat(t, e, "energy"); got != newEnergy {
		t.Errorf("post-set attr energy = %v, want %v", got, newEnergy)
	}

	tmp := filepath.Join(t.TempDir(), "out.zip")
	if err := ls.WriteSave(tmp); err != nil {
		t.Fatalf("WriteSave: %v", err)
	}

	re, err := tb.ParseFile(tmp, nil)
	if err != nil {
		t.Fatalf("reparse written save: %v", err)
	}
	if got := bibiteRowEnergy(t, tb.ExtractTables(re.SHA256, re), target); got != newEnergy {
		t.Errorf("reparsed energy = %v, want %v", got, newEnergy)
	}

	if orig.Entry(other).SHA256 != re.Entry(other).SHA256 {
		t.Errorf("unrelated entry %q SHA256 changed: %s -> %s", other, orig.Entry(other).SHA256, re.Entry(other).SHA256)
	}
	if orig.Entry(target).SHA256 == re.Entry(target).SHA256 {
		t.Errorf("mutated entry %q SHA256 unchanged", target)
	}
}

// TestSetFieldRejectedStageLeavesNoPhantom: a set that fails to stage (here a
// post-apply set, rejected by the session) must not leave a phantom in-memory
// value. The write-through is rolled back, so a later plain read returns the
// applied value, not the rejected one.
func TestSetFieldRejectedStageLeavesNoPhantom(t *testing.T) {
	ls := loadFixture(t)
	name := firstBibiteEntry(t, ls)
	e := &Entity{ls: ls, kind: "bibite", entryName: name}

	const applied = 1234.0
	if err := e.SetField("energy", starlark.Float(applied)); err != nil {
		t.Fatalf("SetField (staged): %v", err)
	}
	// Apply the session: further staging is now rejected ("cannot stage after apply").
	if err := ls.ensureApplied(); err != nil {
		t.Fatalf("ensureApplied: %v", err)
	}

	const rejected = 9999.0
	if err := e.SetField("energy", starlark.Float(rejected)); err == nil {
		t.Fatal("SetField after apply succeeded, want a stage rejection")
	}
	// The rejected set must not have left its value in memory.
	if got := attrFloat(t, e, "energy"); got != applied {
		t.Errorf("post-rejection energy = %v, want %v (no phantom write-through)", got, applied)
	}
}

// TestInRunReadAfterWriteSQL: a query -> set -> query sequence observes the new
// value via DuckDB with no reparse, DuckDB opened exactly once, and the mutation
// applied as a single mirror UPDATE.
func TestInRunReadAfterWriteSQL(t *testing.T) {
	ls := loadFixture(t)
	name := firstBibiteEntry(t, ls)

	before := bibiteEnergySQL(t, ls, name) // opens DuckDB (snapshot)
	e := &Entity{ls: ls, kind: "bibite", entryName: name}
	want := before + 1000
	if err := e.SetField("energy", starlark.Float(want)); err != nil {
		t.Fatalf("SetField: %v", err)
	}
	after := bibiteEnergySQL(t, ls, name) // flushes the mirror, then reads

	if after != want {
		t.Errorf("in-run SQL energy = %v, want %v", after, want)
	}
	if ls.dbOpenCount != 1 {
		t.Errorf("dbOpenCount = %d, want 1 (no re-import)", ls.dbOpenCount)
	}
	if ls.flushStmtCount != 1 {
		t.Errorf("flushStmtCount = %d, want 1 (single mirror UPDATE)", ls.flushStmtCount)
	}
	if ls.rowsMaterialized != 0 {
		t.Errorf("rowsMaterialized = %d, want 0 (set path materializes no entities)", ls.rowsMaterialized)
	}
}

// TestRowByRowBatchedFlush: N row-by-row sets followed by one query flush as a
// single UPDATE per column, not N point-updates.
func TestRowByRowBatchedFlush(t *testing.T) {
	ls := loadFixture(t)
	order := ls.access["bibites"].order
	const n = 8
	if len(order) < n {
		t.Fatalf("fixture needs at least %d bibites", n)
	}
	const v = 777.0
	for i := 0; i < n; i++ {
		e := &Entity{ls: ls, kind: "bibite", entryName: order[i]}
		if err := e.SetField("energy", starlark.Float(v)); err != nil {
			t.Fatalf("SetField %d: %v", i, err)
		}
	}

	// One query triggers exactly one flush statement for the single column.
	rows, err := ls.query("SELECT count(*) FROM bibites WHERE save_id = ? AND entry_name IN (?,?,?,?,?,?,?,?) AND energy = ?",
		ls.saveID, order[0], order[1], order[2], order[3], order[4], order[5], order[6], order[7], v)
	if err != nil {
		t.Fatalf("verify query: %v", err)
	}
	defer rows.Close()
	rows.Next()
	var got int
	if err := rows.Scan(&got); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got != n {
		t.Errorf("%d of %d bibites show the new energy", got, n)
	}
	if ls.flushStmtCount != 1 {
		t.Errorf("flushStmtCount = %d, want 1 (batched, not %d point-updates)", ls.flushStmtCount, n)
	}
	if ls.stagedOps != n {
		t.Errorf("stagedOps = %d, want %d", ls.stagedOps, n)
	}
}

// TestBulkWhereSet: where(...).set stages one batch over the matched rows and
// mirrors them as a single UPDATE.
func TestBulkWhereSet(t *testing.T) {
	ls := loadFixture(t)
	speciesID := ls.tables.Bibites[0].SpeciesID

	coll := &EntityCollection{ls: ls, kind: "bibite"}
	narrowed, err := callMethod(t, coll, "where", starlark.String(fmt.Sprintf("species_id == %d", speciesID)))
	if err != nil {
		t.Fatalf("where: %v", err)
	}
	nc, ok := narrowed.(*EntityCollection)
	if !ok {
		t.Fatalf("where returned %T", narrowed)
	}

	const v = 555.0
	res, err := callMethod(t, nc, "set", starlark.String("energy"), starlark.Float(v))
	if err != nil {
		t.Fatalf("bulk set: %v", err)
	}
	staged := mustInt(t, res)
	if staged <= 0 {
		t.Fatalf("bulk set staged %d rows", staged)
	}
	if int64(ls.stagedOps) != staged {
		t.Errorf("stagedOps = %d, want %d", ls.stagedOps, staged)
	}

	// Every matched bibite now reads v, and the matched count equals staged.
	rows, err := ls.query("SELECT count(*) FILTER (WHERE energy = ?), count(*) FROM bibites WHERE save_id = ? AND species_id = ?", v, ls.saveID, speciesID)
	if err != nil {
		t.Fatalf("verify query: %v", err)
	}
	defer rows.Close()
	rows.Next()
	var matched, total int64
	if err := rows.Scan(&matched, &total); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if matched != total || total != staged {
		t.Errorf("species %d: %d/%d at new energy, staged %d", speciesID, matched, total, staged)
	}
	if ls.flushStmtCount != 1 {
		t.Errorf("flushStmtCount = %d, want 1 (one UPDATE)", ls.flushStmtCount)
	}
}

// sqlStr renders s as a single-quoted DuckDB string literal for inline use in a
// .where() predicate (where the text is raw SQL, so a Go %q double-quoted form
// would be read as an identifier, not a literal).
func sqlStr(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// TestBulkWhereSetExprPersists: set_expr("energy", "energy * 0.9") computes a
// distinct per-row value, persists through a reparse, and leaves an unmatched entry
// byte-for-byte (SHA256-stable).
func TestBulkWhereSetExprPersists(t *testing.T) {
	ls := loadFixture(t)
	orig, err := tb.ParseFile(fixture, nil)
	if err != nil {
		t.Fatalf("reference parse: %v", err)
	}

	order := ls.access["bibites"].order
	if len(order) < 2 {
		t.Fatal("fixture needs at least two bibites")
	}
	target, other := order[0], order[1]

	// Capture the pre-set energies of the two we will narrow to.
	wantTarget := bibiteRowEnergy(t, ls.tables, target) * 0.9
	wantOther := bibiteRowEnergy(t, ls.tables, other) * 0.9

	coll := &EntityCollection{ls: ls, kind: "bibite"}
	narrowed, err := callMethod(t, coll, "where",
		starlark.String(fmt.Sprintf("entry_name == %s OR entry_name == %s", sqlStr(target), sqlStr(other))))
	if err != nil {
		t.Fatalf("where: %v", err)
	}
	res, err := callMethod(t, narrowed.(*EntityCollection), "set_expr",
		starlark.String("energy"), starlark.String("energy * 0.9"))
	if err != nil {
		t.Fatalf("set_expr: %v", err)
	}
	if n := mustInt(t, res); n != 2 {
		t.Fatalf("set_expr staged %d rows, want 2", n)
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

	if got := bibiteRowEnergy(t, tables, target); !floatsClose(got, wantTarget) {
		t.Errorf("target reparsed energy = %v, want %v", got, wantTarget)
	}
	if got := bibiteRowEnergy(t, tables, other); !floatsClose(got, wantOther) {
		t.Errorf("other reparsed energy = %v, want %v", got, wantOther)
	}
	// The two distinct targets should generally differ (proves it is per-row, not a
	// single shared constant); skip the assert only on the degenerate equal-energy case.
	if wantTarget != wantOther && floatsClose(wantTarget, wantOther) {
		t.Errorf("expected distinct per-row energies, both = %v", wantTarget)
	}

	// An entry not in the narrowed set is untouched, byte-for-byte.
	var bystander string
	for _, n := range order[2:] {
		bystander = n
		break
	}
	if bystander != "" {
		if orig.Entry(bystander).SHA256 != re.Entry(bystander).SHA256 {
			t.Errorf("unmatched entry %q SHA256 changed", bystander)
		}
	}
}

// floatsClose compares two float64 with a small relative tolerance (the reparse
// round-trips through JSON, so an exact == can spuriously fail).
func floatsClose(a, b float64) bool {
	const eps = 1e-6
	d := a - b
	if d < 0 {
		d = -d
	}
	scale := 1.0
	if b != 0 {
		s := b
		if s < 0 {
			s = -s
		}
		scale = s
	}
	return d <= eps*scale
}

// TestBulkWhereSetExprInRunReadAfterWrite: the in-run save.sql read reflects the
// computed value, DuckDB opened once, applied as a single mirror UPDATE.
func TestBulkWhereSetExprInRunReadAfterWrite(t *testing.T) {
	ls := loadFixture(t)
	name := firstBibiteEntry(t, ls)

	before := bibiteEnergySQL(t, ls, name) // opens DuckDB (snapshot)
	coll := &EntityCollection{ls: ls, kind: "bibite"}
	narrowed, err := callMethod(t, coll, "where", starlark.String(fmt.Sprintf("entry_name == %s", sqlStr(name))))
	if err != nil {
		t.Fatalf("where: %v", err)
	}
	if _, err := callMethod(t, narrowed.(*EntityCollection), "set_expr",
		starlark.String("energy"), starlark.String("energy * 2")); err != nil {
		t.Fatalf("set_expr: %v", err)
	}
	after := bibiteEnergySQL(t, ls, name) // flushes the mirror, then reads

	if !floatsClose(after, before*2) {
		t.Errorf("in-run SQL energy = %v, want %v", after, before*2)
	}
	if ls.dbOpenCount != 1 {
		t.Errorf("dbOpenCount = %d, want 1 (no re-import)", ls.dbOpenCount)
	}
	if ls.flushStmtCount != 1 {
		t.Errorf("flushStmtCount = %d, want 1 (single mirror UPDATE)", ls.flushStmtCount)
	}
	if ls.rowsMaterialized != 0 {
		t.Errorf("rowsMaterialized = %d, want 0 (set_expr materializes no entities)", ls.rowsMaterialized)
	}
}

// TestBulkWhereSetExprCrossColumn: an expression referencing a column on a JOINed
// sub-table (energy on bibites + d2_size on bibite_body) resolves and computes.
func TestBulkWhereSetExprCrossColumn(t *testing.T) {
	ls := loadFixture(t)
	name := firstBibiteEntry(t, ls)

	// Compute the expected value from the live in-memory rows.
	baseEnergy := bibiteRowEnergy(t, ls.tables, name)
	var d2 float64
	for _, b := range ls.tables.BibiteBody {
		if b.EntryName == name {
			d2 = b.D2Size
			break
		}
	}

	coll := &EntityCollection{ls: ls, kind: "bibite"}
	narrowed, err := callMethod(t, coll, "where", starlark.String(fmt.Sprintf("entry_name == %s", sqlStr(name))))
	if err != nil {
		t.Fatalf("where: %v", err)
	}
	res, err := callMethod(t, narrowed.(*EntityCollection), "set_expr",
		starlark.String("energy"), starlark.String("energy + d2_size"))
	if err != nil {
		t.Fatalf("set_expr cross-column: %v", err)
	}
	if n := mustInt(t, res); n != 1 {
		t.Fatalf("set_expr staged %d rows, want 1", n)
	}
	got := bibiteEnergySQL(t, ls, name)
	if !floatsClose(got, baseEnergy+d2) {
		t.Errorf("cross-column energy = %v, want %v", got, baseEnergy+d2)
	}
}

// TestBulkWhereSetExprIntegerTarget: a BIGINT column (generation) with an integer
// expression stages an integer-typed value, observable through the SQL read path.
func TestBulkWhereSetExprIntegerTarget(t *testing.T) {
	ls := loadFixture(t)
	name := firstBibiteEntry(t, ls)

	var before int64
	for _, b := range ls.tables.Bibites {
		if b.EntryName == name {
			before = b.Generation
			break
		}
	}

	coll := &EntityCollection{ls: ls, kind: "bibite"}
	narrowed, err := callMethod(t, coll, "where", starlark.String(fmt.Sprintf("entry_name == %s", sqlStr(name))))
	if err != nil {
		t.Fatalf("where: %v", err)
	}
	if _, err := callMethod(t, narrowed.(*EntityCollection), "set_expr",
		starlark.String("generation"), starlark.String("generation + 1")); err != nil {
		t.Fatalf("set_expr integer target: %v", err)
	}

	rows, err := ls.query("SELECT generation FROM bibites WHERE save_id = ? AND entry_name = ?", ls.saveID, name)
	if err != nil {
		t.Fatalf("generation query: %v", err)
	}
	defer rows.Close()
	rows.Next()
	var got int64
	if err := rows.Scan(&got); err != nil {
		t.Fatalf("scan generation: %v", err)
	}
	if got != before+1 {
		t.Errorf("generation = %d, want %d", got, before+1)
	}
}

// TestBulkWhereSetExprErrors: each malformed set_expr returns a clean error and
// stages nothing.
func TestBulkWhereSetExprErrors(t *testing.T) {
	cases := []struct {
		name   string
		column string
		expr   string
	}{
		{"unknown column", "energy", "definitely_not_a_column + 1"},
		{"read-only target", "body_id", "body_id + 1"},
		{"negative rejected by guard", "energy", "energy - 1e12"},
		{"null result", "energy", "NULL"},
		{"string for numeric", "energy", "'hello'"},
		{"raw semicolon", "energy", "energy; DROP TABLE bibites"},
		{"subquery", "energy", "(SELECT max(energy) FROM bibites)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ls := loadFixture(t)
			coll := &EntityCollection{ls: ls, kind: "bibite"}
			res, err := callMethod(t, coll, "set_expr", starlark.String(tc.column), starlark.String(tc.expr))
			if err == nil {
				t.Fatalf("set_expr(%q, %q) = %v, want error", tc.column, tc.expr, res)
			}
			if ls.stagedOps != 0 {
				t.Errorf("stagedOps = %d after rejected set_expr, want 0", ls.stagedOps)
			}
		})
	}
}

// TestBulkWhereSetExprZeroMatch: a predicate matching nothing stages nothing and
// returns 0.
func TestBulkWhereSetExprZeroMatch(t *testing.T) {
	ls := loadFixture(t)
	coll := &EntityCollection{ls: ls, kind: "bibite"}
	narrowed, err := callMethod(t, coll, "where", starlark.String("energy < -1"))
	if err != nil {
		t.Fatalf("where: %v", err)
	}
	res, err := callMethod(t, narrowed.(*EntityCollection), "set_expr",
		starlark.String("energy"), starlark.String("energy * 0.9"))
	if err != nil {
		t.Fatalf("set_expr zero-match: %v", err)
	}
	if n := mustInt(t, res); n != 0 {
		t.Errorf("set_expr staged %d rows, want 0", n)
	}
	if ls.stagedOps != 0 {
		t.Errorf("stagedOps = %d, want 0", ls.stagedOps)
	}
}

// TestStaleValueGuard: a set staged with an expected value that does not match
// the underlying save is rejected at apply time. The locator+guard come from the
// same entityLocatorRef path SetField uses; a deliberately wrong Expected stands
// in for an underlying value that changed since it was read.
func TestStaleValueGuard(t *testing.T) {
	ls := loadFixture(t)
	name := firstBibiteEntry(t, ls)

	ref, err := ls.entityLocatorRef("bibite", name)
	if err != nil {
		t.Fatalf("entityLocatorRef: %v", err)
	}
	ref.Table, ref.Column = "bibites", "energy"
	if err := ls.session.StageSQLSet(ref.WithExpected(float64(-987654.0)), float64(1.0)); err != nil {
		t.Fatalf("stage: %v", err)
	}

	tmp := filepath.Join(t.TempDir(), "out.zip")
	if err := ls.WriteSave(tmp); err == nil {
		t.Fatal("expected stale-value guard to reject the apply, got nil")
	}
}

// TestSetCleanErrors: read-only, unknown, and non-scalar sets each return a clean
// error and stage nothing. Note: b.field = None is NO LONGER an error on a writable
// scalar — it is a valid NULL clear (M7 bullet 1), covered by
// TestSetFieldNullWritesAndRoundTrips; a Starlark LIST is still a non-scalar reject.
func TestSetCleanErrors(t *testing.T) {
	ls := loadFixture(t)
	e := &Entity{ls: ls, kind: "bibite", entryName: firstBibiteEntry(t, ls)}

	cases := []struct {
		name string
		attr string
		val  starlark.Value
	}{
		{"read-only locator", "body_id", starlark.MakeInt(5)},
		{"unknown attribute", "definitely_not_a_column", starlark.Float(1)},
		{"non-scalar value", "energy", starlark.NewList(nil)},
	}
	for _, tc := range cases {
		if err := e.SetField(tc.attr, tc.val); err == nil {
			t.Errorf("%s: expected error, got nil", tc.name)
		}
	}
	if ls.stagedOps != 0 {
		t.Errorf("stagedOps = %d after rejected sets, want 0", ls.stagedOps)
	}
}

// TestDryRunWritesNothing: under dry-run, commit stages but writes no file.
func TestDryRunWritesNothing(t *testing.T) {
	ls := loadFixture(t)
	ls.dryRun = true
	e := &Entity{ls: ls, kind: "bibite", entryName: firstBibiteEntry(t, ls)}
	if err := e.SetField("energy", starlark.Float(123.0)); err != nil {
		t.Fatalf("SetField: %v", err)
	}

	save := &Save{ls: ls}
	commitAttr, err := save.Attr("commit")
	if err != nil {
		t.Fatalf("Attr(commit): %v", err)
	}
	tmp := filepath.Join(t.TempDir(), "out.zip")
	res, err := callBuiltin(t, commitAttr.(*starlark.Builtin), starlark.String(tmp))
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if got := mustInt(t, res); got != 1 {
		t.Errorf("commit returned %d staged ops, want 1", got)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Errorf("dry-run wrote a file at %s (stat err: %v)", tmp, err)
	}
}

// TestMutationViaScript: a Starlark program sets a field and commits; the written
// save, reparsed, shows the change.
func TestMutationViaScript(t *testing.T) {
	ls := loadFixture(t)
	name := firstBibiteEntry(t, ls)
	tmp := filepath.Join(t.TempDir(), "out.zip")

	program := []byte(fmt.Sprintf(`
s = open()

def mutate():
    for b in s.bibites:
        if b.entry_name == %q:
            b.energy = 4321.0
            break
    return s.commit(%q)

print("staged=%%d" %% mutate())
`, name, tmp))

	res, err := script.Run(context.Background(), program, Globals(ls), script.Options{Filename: "mutate.star"})
	if err != nil {
		t.Fatalf("script.Run: %v (%+v)", err, res.Diagnostics)
	}

	re, err := tb.ParseFile(tmp, nil)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if got := bibiteRowEnergy(t, tb.ExtractTables(re.SHA256, re), name); got != 4321.0 {
		t.Errorf("scripted set energy = %v, want 4321.0", got)
	}
}
