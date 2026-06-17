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
def mutate():
    save.settings.simulation[%q].set(999.0)
    return save.commit(%q)

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
