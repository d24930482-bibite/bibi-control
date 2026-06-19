package thebibites

import (
	"strings"
	"testing"
	"unicode"

	"go.starlark.net/starlark"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

// ---------------------------------------------------------------------------
// Gene case-insensitive lookup tests
// ---------------------------------------------------------------------------

// TestGeneLookupCaseInsensitive verifies that GeneCollection.Get and
// GeneCollection.get both resolve the upper/lower-cased spelling of a canonical
// gene name to the same value, and that a genuinely absent name still returns
// found=false (Get) / None (.get default).
func TestGeneLookupCaseInsensitive(t *testing.T) {
	ls := loadFixture(t)
	entry, gene := firstNumberGene(t, ls)

	c := &GeneCollection{ls: ls, kind: "bibite", entryName: entry}

	// Read with exact canonical case — baseline.
	exact, foundExact, err := c.Get(starlark.String(gene))
	if err != nil || !foundExact {
		t.Fatalf("Get(%q) exact: found=%v err=%v", gene, foundExact, err)
	}

	// Upper-cased spelling should resolve to the same value.
	upper := strings.ToUpper(gene)
	upVal, foundUp, err := c.Get(starlark.String(upper))
	if err != nil {
		t.Fatalf("Get(%q) upper: err=%v", upper, err)
	}
	if !foundUp {
		t.Fatalf("Get(%q) upper: found=false, want true", upper)
	}
	if upVal != exact {
		t.Errorf("Get(%q)=%v, Get(%q)=%v, want same value", gene, exact, upper, upVal)
	}

	// Lower-cased spelling should also resolve.
	lower := strings.ToLower(gene)
	lowVal, foundLow, err := c.Get(starlark.String(lower))
	if err != nil {
		t.Fatalf("Get(%q) lower: err=%v", lower, err)
	}
	if !foundLow {
		t.Fatalf("Get(%q) lower: found=false, want true", lower)
	}
	if lowVal != exact {
		t.Errorf("Get(%q)=%v, Get(%q)=%v, want same value", gene, exact, lower, lowVal)
	}

	// genes.get upper-cased.
	e := &Entity{ls: ls, kind: "bibite", entryName: entry}
	builtinUp := callGeneNoFatal(t, e, upper)
	if builtinUp == starlark.None {
		t.Errorf("genes.get(%q) upper = None, want non-None", upper)
	}
	if builtinUp != exact {
		t.Errorf("genes.get(%q)=%v, Get(%q)=%v, want same value", upper, builtinUp, gene, exact)
	}

	// Absent gene: Get returns found=false, genes.get returns None.
	_, foundAbsent, err := c.Get(starlark.String("DefinitelyAbsent_M3"))
	if err != nil {
		t.Fatalf("Get(absent): unexpected error %v", err)
	}
	if foundAbsent {
		t.Errorf("Get(absent): found=true, want false")
	}

	absentBuiltin := callGeneNoFatal(t, e, "DefinitelyAbsent_M3")
	if absentBuiltin != starlark.None {
		t.Errorf("genes.get(absent)=%v, want None", absentBuiltin)
	}
}

// TestGeneLookupCaseCollisionLoud verifies that when two gene names on one
// entity differ only by case (e.g. "M3FooGene"/"m3foogene"), a fold query that
// matches both but is an exact match for neither returns a loud error naming
// both, never a silent pick. An exact-case query still resolves its own key
// (contract rule 3). This is the load-bearing test: a pick-first implementation
// would pass all case-hit tests above but MUST fail here.
func TestGeneLookupCaseCollisionLoud(t *testing.T) {
	ls := loadFixture(t)
	entry := firstBibiteEntry(t, ls)

	// Two canonical names that fold to the same lowercase.
	// The fold query "m3foogene" is NOT an exact key for either, so the exact-
	// match fast path is skipped and the fold scan finds both → error.
	const (
		canon1 = "M3FooGene" // folds to "m3foogene"
		canon2 = "m3FooGene" // also folds to "m3foogene"
		query  = "m3foogene" // fold query: exact-matches neither
	)

	row1 := tb.GeneRow{EntryName: entry, GeneName: canon1, Type: tb.ScalarNumber, NumberValue: 1.0}
	row2 := tb.GeneRow{EntryName: entry, GeneName: canon2, Type: tb.ScalarNumber, NumberValue: 2.0}
	backing := []tb.GeneRow{row1, row2}
	set := &geneSet{
		backing: backing,
		order:   []int{0, 1},
		byName:  map[string]int{canon1: 0, canon2: 1},
	}

	c := &GeneCollection{ls: ls, kind: "bibite", entryName: entry}

	// Inject the custom geneSet; ensure the gene index is already initialized.
	ls.geneOnce.Do(ls.buildGeneIndex)
	ls.geneIdx["bibite"][entry] = set

	// Fold query (exact-matches neither) must error, not silently pick one.
	_, _, err := c.Get(starlark.String(query))
	if err == nil {
		t.Fatalf("Get(%q) with collision: want error, got nil", query)
	}
	if !strings.Contains(err.Error(), canon1) || !strings.Contains(err.Error(), canon2) {
		t.Errorf("collision error %q should name both %q and %q", err.Error(), canon1, canon2)
	}

	// SetKey with fold query must also error.
	if setErr := c.SetKey(starlark.String(query), starlark.Float(9)); setErr == nil {
		t.Errorf("SetKey(%q) with collision: want error, got nil", query)
	}

	// genes.get with fold query must error (collision is loud, never default).
	e := &Entity{ls: ls, kind: "bibite", entryName: entry}
	genesAttr, attrErr := e.Attr("genes")
	if attrErr != nil {
		t.Fatalf("Attr(genes): %v", attrErr)
	}
	getAttr, getErr := genesAttr.(*GeneCollection).Attr("get")
	if getErr != nil {
		t.Fatalf("genes.Attr(get): %v", getErr)
	}
	fn := getAttr.(*starlark.Builtin)
	_, builtinErr := fn.CallInternal(&starlark.Thread{}, starlark.Tuple{starlark.String(query)}, nil)
	if builtinErr == nil {
		t.Errorf("genes.get(%q) with collision: want error, got nil", query)
	}

	// Exact-case query for canon1 still resolves correctly (rule 3).
	v1, found1, err1 := c.Get(starlark.String(canon1))
	if err1 != nil {
		t.Fatalf("Get(%q) exact: err=%v", canon1, err1)
	}
	if !found1 {
		t.Errorf("Get(%q) exact: found=false, want true", canon1)
	}
	if v1 != starlark.Float(1.0) {
		t.Errorf("Get(%q) exact=%v, want 1.0", canon1, v1)
	}

	// Exact-case query for canon2 resolves its own row too.
	v2, found2, err2 := c.Get(starlark.String(canon2))
	if err2 != nil {
		t.Fatalf("Get(%q) exact: err=%v", canon2, err2)
	}
	if !found2 {
		t.Errorf("Get(%q) exact: found=false, want true", canon2)
	}
	if v2 != starlark.Float(2.0) {
		t.Errorf("Get(%q) exact=%v, want 2.0", canon2, v2)
	}
}

// callGeneNoFatal calls b.genes.get(name) — the tolerant gene read that
// replaced the removed gene() point read — and returns the result; fatals on a
// genuine error (not on None, which is the expected miss return).
func callGeneNoFatal(t *testing.T, e *Entity, name string) starlark.Value {
	t.Helper()
	genesAttr, err := e.Attr("genes")
	if err != nil {
		t.Fatalf("Attr(genes): %v", err)
	}
	getAttr, err := genesAttr.(*GeneCollection).Attr("get")
	if err != nil {
		t.Fatalf("genes.Attr(get): %v", err)
	}
	fn := getAttr.(*starlark.Builtin)
	v, err := fn.CallInternal(&starlark.Thread{}, starlark.Tuple{starlark.String(name)}, nil)
	if err != nil {
		t.Fatalf("genes.get(%q): %v", name, err)
	}
	return v
}

// ---------------------------------------------------------------------------
// Settings case-insensitive lookup tests
// ---------------------------------------------------------------------------

// TestSettingLookupCaseInsensitive verifies that SettingScope.Get resolves
// a setting by a recased setting name and, for the material scope, a recased
// material name.
func TestSettingLookupCaseInsensitive(t *testing.T) {
	ls := loadFixture(t)

	// -- Simulation scope: look up a setting with alternate case. --
	row, ok := firstSettingOfType(ls.tables.SettingsSimulationValues, tb.ScalarNumber)
	if !ok {
		t.Skip("fixture has no numeric simulation setting")
	}

	sc := &SettingScope{ls: ls, table: "settings_simulation_values", ownerID: simulationOwnerID}

	// Exact lookup baseline.
	exactV, foundExact, err := sc.Get(starlark.String(row.SettingName))
	if err != nil || !foundExact {
		t.Fatalf("Get(%q) exact: found=%v err=%v", row.SettingName, foundExact, err)
	}
	exactSetting := exactV.(*Setting)
	exactVal, _ := exactSetting.Attr("value")

	// Cased-up lookup.
	upper := toUpperAlpha(row.SettingName)
	upV, foundUp, err := sc.Get(starlark.String(upper))
	if err != nil {
		t.Fatalf("Get(%q) upper: err=%v", upper, err)
	}
	if !foundUp {
		t.Fatalf("Get(%q) upper: found=false, want true (case-insensitive)", upper)
	}
	upSetting := upV.(*Setting)
	upVal, _ := upSetting.Attr("value")
	if upVal != exactVal {
		t.Errorf("setting value via upper=%v, via exact=%v, want same", upVal, exactVal)
	}

	// -- Material scope: recased material name should resolve. --
	mrow, ok := firstSettingOfType(ls.tables.SettingsMaterialValues, tb.ScalarNumber)
	if !ok {
		t.Skip("fixture has no numeric material setting")
	}

	upperMat := toUpperAlpha(mrow.OwnerID)
	msc := &SettingScope{ls: ls, table: "settings_material_values", ownerID: upperMat}
	_, foundMat, err := msc.Get(starlark.String(mrow.SettingName))
	if err != nil {
		t.Fatalf("material Get(%q) upper owner: err=%v", mrow.SettingName, err)
	}
	if !foundMat {
		t.Fatalf("material Get(%q) upper owner: found=false, want true", mrow.SettingName)
	}
}

// TestSettingLookupCaseCollisionLoud verifies that when two setting names
// differ only by case in the same scope, a fold query that matches both but
// is an exact match for neither returns a loud error, and exact-case still
// resolves each key individually.
func TestSettingLookupCaseCollisionLoud(t *testing.T) {
	ls := loadFixture(t)
	ls.settingsOnce.Do(ls.buildSettingsIndex)

	const table = "settings_simulation_values"
	const ownerID = simulationOwnerID
	const (
		canon1 = "M3Setting"  // folds to "m3setting"
		canon2 = "m3Setting"  // also folds to "m3setting"
		query  = "m3setting"  // exact-matches neither canon1 nor canon2
	)

	// Inject two collision entries into the live settings index.
	byName := ls.settingsIdx[table][ownerID]
	if byName == nil {
		byName = make(map[string]int)
		if ls.settingsIdx[table] == nil {
			ls.settingsIdx[table] = make(map[string]map[string]int)
		}
		ls.settingsIdx[table][ownerID] = byName
	}

	// We need real backing rows so the index has valid indices.
	// Append synthetic rows to the simulation values backing.
	idx1 := len(ls.tables.SettingsSimulationValues)
	ls.tables.SettingsSimulationValues = append(ls.tables.SettingsSimulationValues,
		tb.SettingValueRow{OwnerID: ownerID, SettingName: canon1, Type: tb.ScalarNumber, NumberValue: 10},
		tb.SettingValueRow{OwnerID: ownerID, SettingName: canon2, Type: tb.ScalarNumber, NumberValue: 20},
	)
	byName[canon1] = idx1
	byName[canon2] = idx1 + 1

	sc := &SettingScope{ls: ls, table: table, ownerID: ownerID}

	// Fold query must error.
	_, _, err := sc.Get(starlark.String(query))
	if err == nil {
		t.Fatalf("Get(%q) with collision: want error, got nil", query)
	}
	if !strings.Contains(err.Error(), canon1) || !strings.Contains(err.Error(), canon2) {
		t.Errorf("collision error %q should name both %q and %q", err.Error(), canon1, canon2)
	}

	// Exact-case query for canon1 resolves.
	v1, found1, err1 := sc.Get(starlark.String(canon1))
	if err1 != nil || !found1 {
		t.Fatalf("Get(%q) exact: found=%v err=%v", canon1, found1, err1)
	}
	s1 := v1.(*Setting)
	val1, _ := s1.Attr("value")
	if val1 != starlark.Float(10) {
		t.Errorf("exact Get(%q) value=%v, want 10", canon1, val1)
	}

	// Exact-case query for canon2 resolves its row.
	v2, found2, err2 := sc.Get(starlark.String(canon2))
	if err2 != nil || !found2 {
		t.Fatalf("Get(%q) exact: found=%v err=%v", canon2, found2, err2)
	}
	s2 := v2.(*Setting)
	val2, _ := s2.Attr("value")
	if val2 != starlark.Float(20) {
		t.Errorf("exact Get(%q) value=%v, want 20", canon2, val2)
	}
}

// toUpperAlpha returns s with all ASCII letters upper-cased. Used to produce a
// reliably different casing that does not introduce characters absent in the
// original (e.g. digits remain unchanged).
func toUpperAlpha(s string) string {
	r := []rune(s)
	for i, ch := range r {
		if unicode.IsLetter(ch) {
			r[i] = unicode.ToUpper(ch)
		}
	}
	return string(r)
}
