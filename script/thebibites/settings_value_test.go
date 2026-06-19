package thebibites

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"go.starlark.net/starlark"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
	"github.com/asemones/bibicontrol/script"
)

// firstSettingOfType returns the first settings row of a given scalar type, so the
// settings tests pick real keys from the fixture rather than hardcoding names
// (robust to save-format churn).
func firstSettingOfType(rows []tb.SettingValueRow, typ tb.ScalarType) (tb.SettingValueRow, bool) {
	for _, r := range rows {
		if r.Type == typ {
			return r, true
		}
	}
	return tb.SettingValueRow{}, false
}

// findSetting locates a settings row by (owner_id, setting_name) in a reparsed set.
func findSetting(rows []tb.SettingValueRow, owner, name string) (tb.SettingValueRow, bool) {
	for _, r := range rows {
		if r.OwnerID == owner && r.SettingName == name {
			return r, true
		}
	}
	return tb.SettingValueRow{}, false
}

// settingHandle drives the public surface (save.settings.<scope>[...] /
// material(name)[...]) to obtain a Setting handle.
func settingHandle(t *testing.T, ls *LoadedSave, scope, owner, name string) *Setting {
	t.Helper()
	s := &Settings{ls: ls}
	var sc *SettingScope
	if scope == "material" {
		attr, err := s.Attr("material")
		if err != nil {
			t.Fatalf("Attr(material): %v", err)
		}
		res, err := callBuiltin(t, attr.(*starlark.Builtin), starlark.String(owner))
		if err != nil {
			t.Fatalf("material(%q): %v", owner, err)
		}
		sc = res.(*SettingScope)
	} else {
		attr, err := s.Attr(scope)
		if err != nil {
			t.Fatalf("Attr(%q): %v", scope, err)
		}
		var ok bool
		sc, ok = attr.(*SettingScope)
		if !ok {
			t.Fatalf("save.settings.%s is %T, want *SettingScope", scope, attr)
		}
	}
	v, found, err := sc.Get(starlark.String(name))
	if err != nil {
		t.Fatalf("scope.Get(%q): %v", name, err)
	}
	if !found {
		t.Fatalf("setting %q not found in scope %s", name, scope)
	}
	return v.(*Setting)
}

// TestSettingsWrappedNumberRoundTrips: a wrapped simulation number setting writes
// through (Setting.value), persists through reparse, and the wrapper is preserved
// (the mutator targets path.Value). Covers the wrapper-vs-bare wrapped case.
func TestSettingsWrappedNumberRoundTrips(t *testing.T) {
	ls := loadFixture(t)
	row, ok := firstSettingOfType(ls.tables.SettingsSimulationValues, tb.ScalarNumber)
	if !ok {
		t.Skip("fixture has no numeric simulation setting")
	}

	h := settingHandle(t, ls, "simulation", row.OwnerID, row.SettingName)
	const want = 73.5
	if _, err := callMethod(t, h, "set", starlark.Float(want)); err != nil {
		t.Fatalf("set: %v", err)
	}
	// Write-through: .value reads the new value immediately.
	val, err := h.Attr("value")
	if err != nil {
		t.Fatalf("Attr(value): %v", err)
	}
	if got := mustFloat(t, val); got != want {
		t.Errorf("post-set .value = %v, want %v", got, want)
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
	got, ok := findSetting(tables.SettingsSimulationValues, row.OwnerID, row.SettingName)
	if !ok {
		t.Fatalf("setting %q missing after reparse", row.SettingName)
	}
	if got.NumberValue != want {
		t.Errorf("reparsed %q = %v, want %v", row.SettingName, got.NumberValue, want)
	}
	if got.Type != tb.ScalarNumber {
		t.Errorf("reparsed %q type = %s, want number (wrapper/shape changed)", row.SettingName, got.Type)
	}
}

// TestSettingsBareMaterialRoundTrips: a bare material number setting persists
// through reparse. Covers the wrapper-vs-bare bare case.
func TestSettingsBareMaterialRoundTrips(t *testing.T) {
	ls := loadFixture(t)
	row, ok := firstSettingOfType(ls.tables.SettingsMaterialValues, tb.ScalarNumber)
	if !ok {
		t.Skip("fixture has no numeric material setting")
	}

	h := settingHandle(t, ls, "material", row.OwnerID, row.SettingName)
	const want = 12.25
	if _, err := callMethod(t, h, "set", starlark.Float(want)); err != nil {
		t.Fatalf("set: %v", err)
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
	got, ok := findSetting(tables.SettingsMaterialValues, row.OwnerID, row.SettingName)
	if !ok {
		t.Fatalf("material setting %s/%s missing after reparse", row.OwnerID, row.SettingName)
	}
	if got.NumberValue != want {
		t.Errorf("reparsed material %s/%s = %v, want %v", row.OwnerID, row.SettingName, got.NumberValue, want)
	}
}

// TestSettingsBoolRoundTrips: a boolean setting writes and persists, exercising the
// bool_value column selection.
func TestSettingsBoolRoundTrips(t *testing.T) {
	ls := loadFixture(t)
	row, ok := firstSettingOfType(ls.tables.SettingsIndependentValues, tb.ScalarBool)
	scope := "independent"
	rows := ls.tables.SettingsIndependentValues
	if !ok {
		if row, ok = firstSettingOfType(ls.tables.SettingsSimulationValues, tb.ScalarBool); ok {
			scope, rows = "simulation", ls.tables.SettingsSimulationValues
		} else {
			t.Skip("fixture has no boolean setting")
		}
	}
	_ = rows

	h := settingHandle(t, ls, scope, row.OwnerID, row.SettingName)
	want := !row.BoolValue
	if _, err := callMethod(t, h, "set", starlark.Bool(want)); err != nil {
		t.Fatalf("set: %v", err)
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
	var got tb.SettingValueRow
	if scope == "independent" {
		got, ok = findSetting(tables.SettingsIndependentValues, row.OwnerID, row.SettingName)
	} else {
		got, ok = findSetting(tables.SettingsSimulationValues, row.OwnerID, row.SettingName)
	}
	if !ok {
		t.Fatalf("bool setting %q missing after reparse", row.SettingName)
	}
	if got.BoolValue != want {
		t.Errorf("reparsed bool %q = %v, want %v", row.SettingName, got.BoolValue, want)
	}
}

// TestSettingsWrongTypeRejected: setting a numeric setting to a string is rejected
// by the value guard before staging.
func TestSettingsWrongTypeRejected(t *testing.T) {
	ls := loadFixture(t)
	row, ok := firstSettingOfType(ls.tables.SettingsSimulationValues, tb.ScalarNumber)
	if !ok {
		t.Skip("fixture has no numeric simulation setting")
	}
	h := settingHandle(t, ls, "simulation", row.OwnerID, row.SettingName)
	if _, err := callMethod(t, h, "set", starlark.String("not a number")); err == nil {
		t.Fatal("expected wrong-type set to be rejected, got nil")
	}
	if ls.stagedOps != 0 {
		t.Errorf("stagedOps = %d after rejected set, want 0", ls.stagedOps)
	}
}

// TestSettingsUnknownKeyNotFound: subscripting a missing setting reports not-found
// (Starlark raises a KeyError), distinct from a present setting.
func TestSettingsUnknownKeyNotFound(t *testing.T) {
	ls := loadFixture(t)
	sc := &SettingScope{ls: ls, table: "settings_simulation_values", ownerID: simulationOwnerID}
	if _, found, err := sc.Get(starlark.String("definitely_not_a_setting")); err != nil || found {
		t.Errorf("unknown setting: found=%v err=%v, want found=false err=nil", found, err)
	}
}

// TestSettingsMirrorEverything: a settings set is visible to an in-run save.sql in
// the same run (mirror-everything), with DuckDB opened once and the change applied
// as a single mirror UPDATE.
func TestSettingsMirrorEverything(t *testing.T) {
	ls := loadFixture(t)
	row, ok := firstSettingOfType(ls.tables.SettingsSimulationValues, tb.ScalarNumber)
	if !ok {
		t.Skip("fixture has no numeric simulation setting")
	}

	h := settingHandle(t, ls, "simulation", row.OwnerID, row.SettingName)
	const want = 4242.0
	if _, err := callMethod(t, h, "set", starlark.Float(want)); err != nil {
		t.Fatalf("set: %v", err)
	}

	rows, err := ls.query(
		"SELECT number_value FROM settings_simulation_values WHERE save_id = ? AND entry_name = ? AND path = ?",
		ls.saveID, row.EntryName, row.Path)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("no settings row from in-run query")
	}
	var got float64
	if err := rows.Scan(&got); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got != want {
		t.Errorf("in-run SQL settings value = %v, want %v (mirror not applied)", got, want)
	}
	if ls.dbOpenCount != 1 {
		t.Errorf("dbOpenCount = %d, want 1", ls.dbOpenCount)
	}
	if ls.flushStmtCount != 1 {
		t.Errorf("flushStmtCount = %d, want 1 (single mirror UPDATE)", ls.flushStmtCount)
	}
}

// TestSettingScopeGet is the contract proof for scope.get(key, default=None) —
// the tolerant settings read that mirrors genes.get. It pins: a hit returns the
// Setting handle (exact and recased), a genuine miss returns the default (None or
// the supplied value), and a fold-collision is loud (never swallowed).
func TestSettingScopeGet(t *testing.T) {
	ls := loadFixture(t)

	row, ok := firstSettingOfType(ls.tables.SettingsSimulationValues, tb.ScalarNumber)
	if !ok {
		t.Skip("fixture has no numeric simulation setting")
	}
	sc := &SettingScope{ls: ls, table: "settings_simulation_values", ownerID: simulationOwnerID}

	// .get("name") returns a *Setting handle (not a bare value).
	got, err := callMethod(t, sc, "get", starlark.String(row.SettingName))
	if err != nil {
		t.Fatalf("scope.get(%q): %v", row.SettingName, err)
	}
	h, ok := got.(*Setting)
	if !ok {
		t.Fatalf("scope.get(%q) is %T, want *Setting handle", row.SettingName, got)
	}
	if h.row.SettingName != row.SettingName {
		t.Errorf("scope.get(%q) handle name=%q, want %q", row.SettingName, h.row.SettingName, row.SettingName)
	}

	// Recased lookup resolves the same handle (case-fold inherited from M3).
	upper := toUpperAlpha(row.SettingName)
	gotUp, err := callMethod(t, sc, "get", starlark.String(upper))
	if err != nil {
		t.Fatalf("scope.get(%q) recased: %v", upper, err)
	}
	if _, ok := gotUp.(*Setting); !ok {
		t.Errorf("scope.get(%q) recased is %T, want *Setting", upper, gotUp)
	}

	// .get("absent") returns None (default default).
	absent, err := callMethod(t, sc, "get", starlark.String("definitely_not_a_setting"))
	if err != nil {
		t.Fatalf("scope.get(absent): %v", err)
	}
	if absent != starlark.None {
		t.Errorf("scope.get(absent)=%v, want None", absent)
	}

	// .get("absent", X) returns the supplied default.
	withDefault, err := callMethod(t, sc, "get", starlark.String("definitely_not_a_setting"), starlark.Float(7.0))
	if err != nil {
		t.Fatalf("scope.get(absent, 7.0): %v", err)
	}
	if withDefault != starlark.Float(7.0) {
		t.Errorf("scope.get(absent, 7.0)=%v, want 7.0", withDefault)
	}

	// Subscript stays loud on a miss (found=false -> KeyError), distinct from .get.
	if v, found, err := sc.Get(starlark.String("definitely_not_a_setting")); err != nil || found || v != nil {
		t.Errorf("scope[absent]: value=%v found=%v err=%v, want nil/false/nil (loud KeyError)", v, found, err)
	}

	// Collision is loud through .get — never the default.
	ls.settingsOnce.Do(ls.buildSettingsIndex)
	const (
		canon1 = "M4Setting" // folds to "m4setting"
		canon2 = "m4Setting" // also folds to "m4setting"
		query  = "m4setting" // exact-matches neither
	)
	byName := ls.settingsIdx["settings_simulation_values"][simulationOwnerID]
	idx1 := len(ls.tables.SettingsSimulationValues)
	ls.tables.SettingsSimulationValues = append(ls.tables.SettingsSimulationValues,
		tb.SettingValueRow{OwnerID: simulationOwnerID, SettingName: canon1, Type: tb.ScalarNumber, NumberValue: 10},
		tb.SettingValueRow{OwnerID: simulationOwnerID, SettingName: canon2, Type: tb.ScalarNumber, NumberValue: 20},
	)
	byName[canon1] = idx1
	byName[canon2] = idx1 + 1
	if _, err := callMethod(t, sc, "get", starlark.String(query)); err == nil {
		t.Errorf("scope.get(%q) collision: want error, got nil (default-swallowed)", query)
	}
	if _, err := callMethod(t, sc, "get", starlark.String(query), starlark.Float(99)); err == nil {
		t.Errorf("scope.get(%q, default) collision: want error, got nil", query)
	}
}

// TestSettingScopeIterate verifies `for s in scope` yields *Setting handles and
// len(scope) matches the row count for that owner, in a deterministic order. This
// closes the "iterate only on genes" gap for the settings name-keyed surface.
func TestSettingScopeIterate(t *testing.T) {
	ls := loadFixture(t)

	if len(ls.tables.SettingsSimulationValues) == 0 {
		t.Skip("fixture has no simulation settings")
	}
	sc := &SettingScope{ls: ls, table: "settings_simulation_values", ownerID: simulationOwnerID}

	// Expected count: every simulation row shares the constant owner_id.
	var want int
	for _, r := range ls.tables.SettingsSimulationValues {
		if r.OwnerID == simulationOwnerID {
			want++
		}
	}

	if got := sc.Len(); got != want {
		t.Errorf("scope.Len()=%d, want %d", got, want)
	}

	it := sc.Iterate()
	defer it.Done()
	var v starlark.Value
	n := 0
	var prevName string
	for it.Next(&v) {
		h, ok := v.(*Setting)
		if !ok {
			t.Fatalf("scope iter yielded %T, want *Setting handle", v)
		}
		// Iteration order is name-sorted and stable (not Go map order).
		if n > 0 && h.row.SettingName < prevName {
			t.Errorf("scope iteration out of order: %q after %q", h.row.SettingName, prevName)
		}
		prevName = h.row.SettingName
		n++
	}
	if n != want {
		t.Errorf("iterated %d settings, want %d", n, want)
	}

	// An empty/absent material scope iterates zero times and len()s to 0.
	empty := &SettingScope{ls: ls, table: "settings_material_values", ownerID: "DefinitelyNoSuchMaterial_M4"}
	if got := empty.Len(); got != 0 {
		t.Errorf("absent scope.Len()=%d, want 0", got)
	}
	emptyIt := empty.Iterate()
	defer emptyIt.Done()
	if emptyIt.Next(&v) {
		t.Errorf("absent scope iterated a value, want none")
	}
}

// TestSettingsViaScript: the end-to-end Starlark surface
// save.settings.simulation["k"].set(v) stages and persists.
func TestSettingsViaScript(t *testing.T) {
	ls := loadFixture(t)
	row, ok := firstSettingOfType(ls.tables.SettingsSimulationValues, tb.ScalarNumber)
	if !ok {
		t.Skip("fixture has no numeric simulation setting")
	}
	tmp := filepath.Join(t.TempDir(), "out.zip")

	program := []byte(fmt.Sprintf(`
s = open()

def mutate():
    s.settings.simulation[%q].set(999.0)
    return s.commit(%q)

print("staged=%%d" %% mutate())
`, row.SettingName, tmp))

	res, err := script.Run(context.Background(), program, Globals(ls), script.Options{Filename: "settings.star"})
	if err != nil {
		t.Fatalf("script.Run: %v (%+v)", err, res.Diagnostics)
	}

	re, err := tb.ParseFile(tmp, nil)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	tables := tb.ExtractTables(re.SHA256, re)
	got, ok := findSetting(tables.SettingsSimulationValues, row.OwnerID, row.SettingName)
	if !ok {
		t.Fatalf("setting %q missing after reparse", row.SettingName)
	}
	if got.NumberValue != 999.0 {
		t.Errorf("scripted settings value = %v, want 999.0", got.NumberValue)
	}
}
