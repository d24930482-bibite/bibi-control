package thebibites

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"testing"

	"go.starlark.net/starlark"

	"github.com/asemones/bibicontrol/script"
)

// callMethod invokes a HasAttrs method builtin by name and returns its result.
func callMethod(t *testing.T, v starlark.HasAttrs, name string, args ...starlark.Value) (starlark.Value, error) {
	t.Helper()
	attr, err := v.Attr(name)
	if err != nil {
		t.Fatalf("Attr(%q): %v", name, err)
	}
	if attr == nil {
		t.Fatalf("Attr(%q) returned nil (no such method)", name)
	}
	fn, ok := attr.(*starlark.Builtin)
	if !ok {
		t.Fatalf("%q is %T, want *Builtin", name, attr)
	}
	return fn.CallInternal(&starlark.Thread{}, starlark.Tuple(args), nil)
}

func mustFloat(t *testing.T, v starlark.Value) float64 {
	t.Helper()
	f, ok := starlark.AsFloat(v)
	if !ok {
		t.Fatalf("value %v (%s) is not a number", v, v.Type())
	}
	return f
}

func approxEqual(a, b float64) bool {
	if a == b {
		return true
	}
	return math.Abs(a-b) <= 1e-6*(1+math.Max(math.Abs(a), math.Abs(b)))
}

func medianOf(xs []float64) float64 {
	sort.Float64s(xs)
	n := len(xs)
	if n == 0 {
		return math.NaN()
	}
	if n%2 == 1 {
		return xs[n/2]
	}
	return (xs[n/2-1] + xs[n/2]) / 2
}

func bibiteEnergies(ls *LoadedSave) []float64 {
	xs := make([]float64, 0, len(ls.tables.Bibites))
	for _, b := range ls.tables.Bibites {
		xs = append(xs, b.Energy)
	}
	return xs
}

// TestSaveSQLReturnsRows exercises the raw escape hatch: save.sql returns a list
// of dicts whose values match the in-memory rows.
func TestSaveSQLReturnsRows(t *testing.T) {
	ls := loadFixture(t)
	s := &Save{ls: ls}
	query := fmt.Sprintf(
		"SELECT entry_name, energy FROM bibites WHERE save_id = '%s' ORDER BY entry_name", ls.saveID)
	v, err := callMethod(t, s, "sql", starlark.String(query))
	if err != nil {
		t.Fatalf("save.sql: %v", err)
	}
	list, ok := v.(*starlark.List)
	if !ok {
		t.Fatalf("save.sql returned %T, want *List", v)
	}
	if list.Len() != len(ls.tables.Bibites) {
		t.Fatalf("save.sql returned %d rows, want %d", list.Len(), len(ls.tables.Bibites))
	}
	// DuckDB never reparsed; it was opened exactly once.
	if ls.dbOpenCount != 1 {
		t.Errorf("dbOpenCount=%d, want 1", ls.dbOpenCount)
	}

	// Spot-check the first row's dict shape and a value.
	first := list.Index(0).(*starlark.Dict)
	if _, found, _ := first.Get(starlark.String("entry_name")); !found {
		t.Errorf("row dict missing entry_name key")
	}
	energyVal, found, _ := first.Get(starlark.String("energy"))
	if !found {
		t.Fatalf("row dict missing energy key")
	}
	// The min-entry_name row's energy should appear in the raw bibites table.
	if _, ok := energyVal.(starlark.Float); !ok {
		t.Errorf("energy is %T, want Float", energyVal)
	}
}

// TestPushdownMatchesRawAndHost asserts the three paths agree: collection
// push-down aggregate == raw save.sql == host-side builtin over the materialized
// list, and that the aggregate never materializes Entity rows.
func TestPushdownMatchesRawAndHost(t *testing.T) {
	ls := loadFixture(t)
	energies := bibiteEnergies(ls)
	wantMedian := medianOf(append([]float64(nil), energies...))

	bibites := &EntityCollection{ls: ls, kind: "bibite"}

	// Push-down.
	pd, err := callMethod(t, bibites, "median", starlark.String("energy"))
	if err != nil {
		t.Fatalf("push-down median: %v", err)
	}
	gotPD := mustFloat(t, pd)

	// Raw save.sql.
	s := &Save{ls: ls}
	rawV, err := callMethod(t, s, "sql", starlark.String(
		fmt.Sprintf("SELECT median(energy) AS m FROM bibites WHERE save_id = '%s'", ls.saveID)))
	if err != nil {
		t.Fatalf("raw median: %v", err)
	}
	rawList := rawV.(*starlark.List)
	rawDict := rawList.Index(0).(*starlark.Dict)
	rawCell, _, _ := rawDict.Get(starlark.String("m"))
	gotRaw := mustFloat(t, rawCell)

	// Host builtin over the materialized list.
	hostList := starlark.NewList(nil)
	for _, e := range energies {
		_ = hostList.Append(starlark.Float(e))
	}
	hostMedian, ok := hostAggregates()["median"].(*starlark.Builtin)
	if !ok {
		t.Fatalf("host median is %T, want *Builtin", hostAggregates()["median"])
	}
	hostV, err := callBuiltin(t, hostMedian, hostList)
	if err != nil {
		t.Fatalf("host median: %v", err)
	}
	gotHost := mustFloat(t, hostV)

	if !approxEqual(gotPD, wantMedian) || !approxEqual(gotPD, gotRaw) || !approxEqual(gotPD, gotHost) {
		t.Errorf("median mismatch: pushdown=%v raw=%v host=%v want=%v", gotPD, gotRaw, gotHost, wantMedian)
	}

	// The aggregate path must not have materialized Entity rows.
	if ls.rowsMaterialized != 0 {
		t.Errorf("rowsMaterialized=%d, want 0 (aggregate must not materialize entities)", ls.rowsMaterialized)
	}
}

// TestGroupByNoMaterialization runs a grouped aggregate and asserts it pushes
// down (no Entity materialization, single DuckDB open, reused across queries) and
// matches a Go-side grouping.
func TestGroupByNoMaterialization(t *testing.T) {
	ls := loadFixture(t)

	// Expected: median energy per species, computed off the in-memory rows.
	bySpecies := map[int64][]float64{}
	for _, b := range ls.tables.Bibites {
		bySpecies[b.SpeciesID] = append(bySpecies[b.SpeciesID], b.Energy)
	}

	bibites := &EntityCollection{ls: ls, kind: "bibite"}
	grouped, err := callMethod(t, bibites, "group_by", starlark.String("species_id"))
	if err != nil {
		t.Fatalf("group_by: %v", err)
	}
	gc, ok := grouped.(*GroupedCollection)
	if !ok {
		t.Fatalf("group_by returned %T, want *GroupedCollection", grouped)
	}
	res, err := callMethod(t, gc, "median", starlark.String("energy"))
	if err != nil {
		t.Fatalf("grouped median: %v", err)
	}
	dict := res.(*starlark.Dict)

	if dict.Len() != len(bySpecies) {
		t.Errorf("group count=%d, want %d distinct species", dict.Len(), len(bySpecies))
	}
	for _, item := range dict.Items() {
		keyInt, ok := item[0].(starlark.Int)
		if !ok {
			t.Fatalf("group key is %T, want Int", item[0])
		}
		key, _ := keyInt.Int64()
		got := mustFloat(t, item[1])
		want := medianOf(append([]float64(nil), bySpecies[key]...))
		if !approxEqual(got, want) {
			t.Errorf("species %d median=%v, want %v", key, got, want)
		}
	}

	if ls.rowsMaterialized != 0 {
		t.Errorf("rowsMaterialized=%d, want 0", ls.rowsMaterialized)
	}
	if ls.dbOpenCount != 1 {
		t.Errorf("dbOpenCount=%d, want 1", ls.dbOpenCount)
	}

	// A second aggregate reuses the same DuckDB handle.
	if _, err := callMethod(t, bibites, "count"); err != nil {
		t.Fatalf("count: %v", err)
	}
	if ls.dbOpenCount != 1 {
		t.Errorf("dbOpenCount=%d after second query, want 1 (handle reused)", ls.dbOpenCount)
	}
}

// TestCountAndQuantile checks count() over a narrowed collection and quantile.
func TestCountAndQuantile(t *testing.T) {
	ls := loadFixture(t)
	bibites := &EntityCollection{ls: ls, kind: "bibite"}

	total, err := callMethod(t, bibites, "count")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if got := mustFloat(t, total); int(got) != len(ls.tables.Bibites) {
		t.Errorf("count=%v, want %d", got, len(ls.tables.Bibites))
	}

	// quantile(0.5) equals median.
	med, err := callMethod(t, bibites, "median", starlark.String("energy"))
	if err != nil {
		t.Fatalf("median: %v", err)
	}
	q50, err := callMethod(t, bibites, "quantile", starlark.String("energy"), starlark.Float(0.5))
	if err != nil {
		t.Fatalf("quantile: %v", err)
	}
	if !approxEqual(mustFloat(t, med), mustFloat(t, q50)) {
		t.Errorf("quantile(0.5)=%v != median=%v", mustFloat(t, q50), mustFloat(t, med))
	}

	// Out-of-range q is rejected before touching DuckDB.
	if _, err := callMethod(t, bibites, "quantile", starlark.String("energy"), starlark.Float(1.5)); err == nil {
		t.Errorf("quantile(1.5) should error")
	}
}

// TestSubTableAggregate aggregates a 1:1 sub-table column via the LEFT JOIN and
// matches a Go grouping that mirrors the join (bibite drives, body provides value
// when present).
func TestSubTableAggregate(t *testing.T) {
	ls := loadFixture(t)

	fatByEntry := make(map[string]float64, len(ls.tables.BibiteBody))
	for _, b := range ls.tables.BibiteBody {
		fatByEntry[b.EntryName] = b.FatReservesAmount
	}
	var want []float64
	for _, b := range ls.tables.Bibites {
		if v, ok := fatByEntry[b.EntryName]; ok {
			want = append(want, v)
		}
	}

	bibites := &EntityCollection{ls: ls, kind: "bibite"}
	got, err := callMethod(t, bibites, "median", starlark.String("fat_reserves_amount"))
	if err != nil {
		t.Fatalf("median(fat_reserves_amount): %v", err)
	}
	if !approxEqual(mustFloat(t, got), medianOf(want)) {
		t.Errorf("sub-table median=%v, want %v", mustFloat(t, got), medianOf(want))
	}
}

// TestWhereCollisionPredicate forces a sub-table join and references a column
// (body_id) present on both the identity and the joined sub-table; the rewrite
// qualifies it so the query is unambiguous and runs.
func TestWhereCollisionPredicate(t *testing.T) {
	ls := loadFixture(t)
	bibites := &EntityCollection{ls: ls, kind: "bibite"}

	narrowed, err := callMethod(t, bibites, "where",
		starlark.String("fat_reserves_amount >= 0 and body_id >= 0"))
	if err != nil {
		t.Fatalf("where: %v", err)
	}
	nc := narrowed.(*EntityCollection)
	got, err := callMethod(t, nc, "count")
	if err != nil {
		t.Fatalf("count after collision predicate: %v", err)
	}

	// Expected: bibites that have a body row (so fat_reserves is non-NULL) with a
	// body_id (the identity table always has body_id set for live bibites).
	bodyEntries := make(map[string]bool, len(ls.tables.BibiteBody))
	for _, b := range ls.tables.BibiteBody {
		bodyEntries[b.EntryName] = true
	}
	var want int
	for _, b := range ls.tables.Bibites {
		if bodyEntries[b.EntryName] && b.HasBodyID && b.BodyID >= 0 {
			want++
		}
	}
	if int(mustFloat(t, got)) != want {
		t.Errorf("collision-predicate count=%v, want %d", mustFloat(t, got), want)
	}
}

// TestFilteredCollectionLenAndIterate asserts a filtered collection honors its
// where predicate on len(), iteration, and truth-test, and that all three agree
// with .count() — the F2 regression (previously Len/Iterate walked the full
// identity table, disagreeing with the predicate-aware .count()).
func TestFilteredCollectionLenAndIterate(t *testing.T) {
	ls := loadFixture(t)

	// Pick a threshold that actually partitions the population so the predicate is
	// non-trivial (not all rows, not zero rows).
	energies := bibiteEnergies(ls)
	threshold := medianOf(append([]float64(nil), energies...))
	var wantNames []string
	for _, b := range ls.tables.Bibites {
		if b.Energy > threshold {
			wantNames = append(wantNames, b.EntryName)
		}
	}
	if len(wantNames) == 0 || len(wantNames) == len(ls.tables.Bibites) {
		t.Fatalf("threshold %v does not partition the population (matched %d of %d)",
			threshold, len(wantNames), len(ls.tables.Bibites))
	}

	bibites := &EntityCollection{ls: ls, kind: "bibite"}
	narrowedV, err := callMethod(t, bibites, "where",
		starlark.String(fmt.Sprintf("energy > %v", threshold)))
	if err != nil {
		t.Fatalf("where: %v", err)
	}
	narrowed := narrowedV.(*EntityCollection)

	// Len() honors the predicate.
	if got := narrowed.Len(); got != len(wantNames) {
		t.Errorf("filtered Len()=%d, want %d", got, len(wantNames))
	}

	// Truth() reflects the filtered (non-empty) set.
	if narrowed.Truth() != starlark.True {
		t.Errorf("filtered Truth()=false, want true (set is non-empty)")
	}

	// .count() agrees with Len().
	countV, err := callMethod(t, narrowed, "count")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if int(mustFloat(t, countV)) != len(wantNames) {
		t.Errorf("filtered count()=%v, want %d", mustFloat(t, countV), len(wantNames))
	}

	// Iteration yields exactly the matching entities (compared as a set, since the
	// push-down order is unspecified).
	want := make(map[string]bool, len(wantNames))
	for _, n := range wantNames {
		want[n] = true
	}
	got := make(map[string]bool)
	for _, e := range collect(t, narrowed) {
		got[e.entryName] = true
	}
	if len(got) != len(want) {
		t.Errorf("iteration yielded %d entities, want %d", len(got), len(want))
	}
	for n := range want {
		if !got[n] {
			t.Errorf("iteration missing matching entity %q", n)
		}
	}
	for n := range got {
		if !want[n] {
			t.Errorf("iteration yielded non-matching entity %q", n)
		}
	}
}

// TestRewritePredicate unit-tests the identifier tokenizer directly: friendly
// columns get qualified, keywords/functions/literals are left intact.
func TestRewritePredicate(t *testing.T) {
	ls := loadFixture(t)

	got, tables, err := ls.rewritePredicate("bibite", "fat_reserves_amount >= 0 and body_id >= 0", nil)
	if err != nil {
		t.Fatalf("rewritePredicate: %v", err)
	}
	if !strings.Contains(got, `"bibite_body"."fat_reserves_amount"`) {
		t.Errorf("rewrite did not qualify sub-table column: %q", got)
	}
	if !strings.Contains(got, `"bibites"."body_id"`) {
		t.Errorf("rewrite did not qualify colliding identity column: %q", got)
	}
	if !tables["bibite_body"] {
		t.Errorf("rewrite did not report bibite_body as referenced: %v", tables)
	}

	// Keywords and function calls survive; the inner column still qualifies.
	got2, _, err := ls.rewritePredicate("bibite", "abs(energy) > 1 and not dead", nil)
	if err != nil {
		t.Fatalf("rewritePredicate: %v", err)
	}
	if !strings.Contains(got2, "abs(") {
		t.Errorf("rewrite mangled function call: %q", got2)
	}
	if !strings.Contains(got2, ` not `) {
		t.Errorf("rewrite mangled keyword 'not': %q", got2)
	}
	if !strings.Contains(got2, `abs("bibites"."energy")`) {
		t.Errorf("rewrite did not qualify column inside function call: %q", got2)
	}
	if !strings.Contains(got2, `"bibites"."dead"`) {
		t.Errorf("rewrite did not qualify 'dead': %q", got2)
	}

	// String literals are copied verbatim (a column-looking word inside quotes is
	// not qualified).
	got3, _, err := ls.rewritePredicate("bibite", "species_id = 1 or energy = 'energy'", nil)
	if err != nil {
		t.Fatalf("rewritePredicate: %v", err)
	}
	if !strings.Contains(got3, "'energy'") {
		t.Errorf("rewrite altered a string literal: %q", got3)
	}
}

// TestUnknownColumnDiagnostics: an unknown aggregate column errors at resolution;
// an unknown column inside a .where() surfaces a diagnostic naming the predicate.
func TestUnknownColumnDiagnostics(t *testing.T) {
	ls := loadFixture(t)
	bibites := &EntityCollection{ls: ls, kind: "bibite"}

	if _, err := callMethod(t, bibites, "median", starlark.String("not_a_column")); err == nil {
		t.Errorf("median of unknown column should error")
	}

	narrowed, err := callMethod(t, bibites, "where", starlark.String("not_a_real_column > 5"))
	if err != nil {
		t.Fatalf("where: %v", err)
	}
	_, err = callMethod(t, narrowed.(*EntityCollection), "count")
	if err == nil {
		t.Fatalf("count with unknown where column should error")
	}
	if !strings.Contains(err.Error(), "not_a_real_column > 5") {
		t.Errorf("error %q does not name the predicate", err.Error())
	}
}

// TestFlushMirrorNoop: T5 never marks the mirror dirty, so flushMirror is a no-op.
func TestFlushMirrorNoop(t *testing.T) {
	ls := loadFixture(t)
	if ls.mirrorDirty {
		t.Fatalf("mirrorDirty should start false")
	}
	if err := ls.flushMirror(context.Background()); err != nil {
		t.Fatalf("flushMirror: %v", err)
	}
	if ls.mirrorDirty {
		t.Errorf("flushMirror flipped mirrorDirty; T5 should never mark dirty")
	}
}

// TestAnalyticsViaScript exercises the full Starlark surface end to end.
func TestAnalyticsViaScript(t *testing.T) {
	ls := loadFixture(t)
	program := []byte(`
s = open()
rows = s.sql("SELECT species_id, energy FROM bibites")
print("rows=%d" % len(rows))
med = s.bibites.median("energy")
print("median=%s" % str(med))
groups = s.bibites.group_by("species_id").mean("energy")
print("groups=%d" % len(groups))
host = median([1.0, 2.0, 3.0, 4.0])
print("host=%s" % str(host))
`)
	res, err := script.Run(context.Background(), program, Globals(ls), script.Options{Filename: "analytics.star"})
	if err != nil {
		t.Fatalf("script.Run: %v (%+v)", err, res.Diagnostics)
	}
	for _, want := range []string{"rows=", "median=", "groups=", "host=2.5"} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("output %q missing %q", res.Output, want)
		}
	}
}

// callBuiltin invokes a *starlark.Builtin with positional args.
func callBuiltin(t *testing.T, b *starlark.Builtin, args ...starlark.Value) (starlark.Value, error) {
	t.Helper()
	return b.CallInternal(&starlark.Thread{}, starlark.Tuple(args), nil)
}
