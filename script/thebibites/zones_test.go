package thebibites

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"testing"

	"go.starlark.net/starlark"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
	"github.com/asemones/bibicontrol/script"
)

// mustString asserts v is a Starlark string and returns it.
func mustString(t *testing.T, v starlark.Value) string {
	t.Helper()
	s, ok := starlark.AsString(v)
	if !ok {
		t.Fatalf("value %v (%s) is not a string", v, v.Type())
	}
	return s
}

// zoneAt drives save.zones[i] to obtain a *Zone, skipping if the fixture has fewer
// zones.
func zoneAt(t *testing.T, ls *LoadedSave, i int) *Zone {
	t.Helper()
	zs := &Zones{ls: ls}
	if i >= zs.Len() {
		t.Skipf("fixture has only %d zones", zs.Len())
	}
	z, ok := zs.Index(i).(*Zone)
	if !ok {
		t.Fatalf("zones[%d] is %T, want *Zone", i, zs.Index(i))
	}
	return z
}

// TestZoneRead: save.zones length and per-zone scalar reads match the normalized
// rows.
func TestZoneRead(t *testing.T) {
	ls := loadFixture(t)
	zs := &Zones{ls: ls}
	if zs.Len() != len(ls.tables.SettingsZones) {
		t.Fatalf("zones Len = %d, want %d", zs.Len(), len(ls.tables.SettingsZones))
	}
	if zs.Len() == 0 {
		t.Skip("fixture has no zones")
	}
	for i := range ls.tables.SettingsZones {
		z := zoneAt(t, ls, i)
		name, err := z.Attr("name")
		if err != nil {
			t.Fatalf("zone[%d].name: %v", i, err)
		}
		if got := mustString(t, name); got != ls.tables.SettingsZones[i].Name {
			t.Errorf("zone[%d].name = %q, want %q", i, got, ls.tables.SettingsZones[i].Name)
		}
		zi, err := z.Attr("zone_index")
		if err != nil {
			t.Fatalf("zone[%d].zone_index: %v", i, err)
		}
		if got := mustInt(t, zi); got != int64(ls.tables.SettingsZones[i].ZoneIndex) {
			t.Errorf("zone[%d].zone_index = %d, want %d", i, got, ls.tables.SettingsZones[i].ZoneIndex)
		}
	}
}

// TestZoneScalarSetRoundTrips: z.name = "..." writes through and persists through
// reparse, leaving a sibling zone untouched.
func TestZoneScalarSetRoundTrips(t *testing.T) {
	ls := loadFixture(t)
	if len(ls.tables.SettingsZones) == 0 {
		t.Skip("fixture has no zones")
	}
	z := zoneAt(t, ls, 0)
	const want = "ScriptedZone"
	if err := z.SetField("name", starlark.String(want)); err != nil {
		t.Fatalf("SetField(name): %v", err)
	}
	if got, _ := z.Attr("name"); mustString(t, got) != want {
		t.Errorf("post-set name = %q, want %q", mustString(t, got), want)
	}

	sibling := ""
	if len(ls.tables.SettingsZones) > 1 {
		sibling = ls.tables.SettingsZones[1].Name
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
	if tables.SettingsZones[0].Name != want {
		t.Errorf("reparsed zone[0].name = %q, want %q", tables.SettingsZones[0].Name, want)
	}
	if sibling != "" && tables.SettingsZones[1].Name != sibling {
		t.Errorf("unrelated zone[1].name = %q, want %q", tables.SettingsZones[1].Name, sibling)
	}
}

// TestZoneScalarSetMirror: a zone scalar set is visible to an in-run save.sql via
// the mirror (single UPDATE, DuckDB opened once).
func TestZoneScalarSetMirror(t *testing.T) {
	ls := loadFixture(t)
	if len(ls.tables.SettingsZones) == 0 {
		t.Skip("fixture has no zones")
	}
	row := ls.tables.SettingsZones[0]
	z := zoneAt(t, ls, 0)
	const want = "MirroredZone"
	if err := z.SetField("name", starlark.String(want)); err != nil {
		t.Fatalf("SetField(name): %v", err)
	}

	rows, err := ls.query(
		"SELECT name FROM settings_zones WHERE save_id = ? AND entry_name = ? AND zone_index = ?",
		ls.saveID, row.EntryName, row.ZoneIndex)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("no zone row from in-run query")
	}
	var got string
	if err := rows.Scan(&got); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got != want {
		t.Errorf("in-run SQL zone name = %q, want %q (mirror not applied)", got, want)
	}
	if ls.dbOpenCount != 1 {
		t.Errorf("dbOpenCount = %d, want 1", ls.dbOpenCount)
	}
	if ls.flushStmtCount != 1 {
		t.Errorf("flushStmtCount = %d, want 1 (single mirror UPDATE)", ls.flushStmtCount)
	}
}

// TestZoneValueRoundTrips: save.zones[i].values["k"].set(v) writes through the P1
// settings surface to the right zone and persists.
func TestZoneValueRoundTrips(t *testing.T) {
	ls := loadFixture(t)
	vrow, ok := firstSettingOfType(ls.tables.SettingsZoneValues, tb.ScalarNumber)
	if !ok {
		t.Skip("fixture has no numeric zone value")
	}
	zoneIdx := -1
	for i := range ls.tables.SettingsZones {
		if zoneValuesOwnerID(&ls.tables.SettingsZones[i]) == vrow.OwnerID {
			zoneIdx = i
			break
		}
	}
	if zoneIdx < 0 {
		t.Fatalf("no zone matches zone-value owner %q", vrow.OwnerID)
	}

	z := zoneAt(t, ls, zoneIdx)
	valuesAttr, err := z.Attr("values")
	if err != nil {
		t.Fatalf("Attr(values): %v", err)
	}
	scope, ok := valuesAttr.(*SettingScope)
	if !ok {
		t.Fatalf("zone.values is %T, want *SettingScope", valuesAttr)
	}
	hv, found, err := scope.Get(starlark.String(vrow.SettingName))
	if err != nil || !found {
		t.Fatalf("values.Get(%q): found=%v err=%v", vrow.SettingName, found, err)
	}
	const want = 0.625
	if _, err := callMethod(t, hv.(*Setting), "set", starlark.Float(want)); err != nil {
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
	got, ok := findSetting(tables.SettingsZoneValues, vrow.OwnerID, vrow.SettingName)
	if !ok {
		t.Fatalf("zone value %q missing after reparse", vrow.SettingName)
	}
	if got.NumberValue != want {
		t.Errorf("reparsed zone value = %v, want %v", got.NumberValue, want)
	}
}

// TestZoneDeletePersists: zone.delete() stages one op and the reparsed save has one
// fewer zone.
func TestZoneDeletePersists(t *testing.T) {
	ls := loadFixture(t)
	before := len(ls.tables.SettingsZones)
	if before == 0 {
		t.Skip("fixture has no zones")
	}
	z := zoneAt(t, ls, before-1) // delete the last zone
	if _, err := callMethod(t, z, "delete"); err != nil {
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
	if len(tables.SettingsZones) != before-1 {
		t.Errorf("zone count after delete = %d, want %d", len(tables.SettingsZones), before-1)
	}
}

// TestZoneCloneAppend: clone -> edit name -> append creates a new zone (not visible
// mid-run) that persists with the edited name and a fresh, unique id.
func TestZoneCloneAppend(t *testing.T) {
	ls := loadFixture(t)
	before := len(ls.tables.SettingsZones)
	if before == 0 {
		t.Skip("fixture has no zones")
	}
	zs := &Zones{ls: ls}
	cloneAttr, err := zs.Attr("clone")
	if err != nil {
		t.Fatalf("Attr(clone): %v", err)
	}
	pzVal, err := callBuiltin(t, cloneAttr.(*starlark.Builtin), starlark.MakeInt(0))
	if err != nil {
		t.Fatalf("clone(0): %v", err)
	}
	pz, ok := pzVal.(*PendingZone)
	if !ok {
		t.Fatalf("clone is %T, want *PendingZone", pzVal)
	}
	const want = "ClonedZone"
	if err := pz.SetField("name", starlark.String(want)); err != nil {
		t.Fatalf("pending SetField(name): %v", err)
	}
	if zs.Len() != before {
		t.Errorf("mid-run zones Len = %d, want %d (clone not applied)", zs.Len(), before)
	}
	if _, err := callMethod(t, pz, "append"); err != nil {
		t.Fatalf("append: %v", err)
	}
	if zs.Len() != before {
		t.Errorf("post-stage zones Len = %d, want %d (append staged, not applied)", zs.Len(), before)
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
	if len(tables.SettingsZones) != before+1 {
		t.Fatalf("zone count after clone-append = %d, want %d", len(tables.SettingsZones), before+1)
	}
	found := false
	ids := make(map[int64]int)
	for _, zr := range tables.SettingsZones {
		if zr.HasZoneID {
			ids[zr.ZoneID]++
		}
		if zr.Name == want {
			found = true
		}
	}
	if !found {
		t.Errorf("appended zone with name %q not found after reparse", want)
	}
	for id, n := range ids {
		if n > 1 {
			t.Errorf("zone id %d appears %d times (clone reused the template id)", id, n)
		}
	}
}

// clonePendingZone drives save.zones.clone(i) to obtain a *PendingZone, skipping if
// the fixture has no zones.
func clonePendingZone(t *testing.T, ls *LoadedSave, i int) *PendingZone {
	t.Helper()
	zs := &Zones{ls: ls}
	if zs.Len() <= i {
		t.Skipf("fixture has only %d zones", zs.Len())
	}
	cloneAttr, err := zs.Attr("clone")
	if err != nil {
		t.Fatalf("Attr(clone): %v", err)
	}
	pzVal, err := callBuiltin(t, cloneAttr.(*starlark.Builtin), starlark.MakeInt(i))
	if err != nil {
		t.Fatalf("clone(%d): %v", i, err)
	}
	pz, ok := pzVal.(*PendingZone)
	if !ok {
		t.Fatalf("clone is %T, want *PendingZone", pzVal)
	}
	return pz
}

// pendingZoneValuesOf returns the z.values mapping of a pending zone.
func pendingZoneValuesOf(t *testing.T, pz *PendingZone) *pendingZoneValues {
	t.Helper()
	attr, err := pz.Attr("values")
	if err != nil {
		t.Fatalf("pending.Attr(values): %v", err)
	}
	pv, ok := attr.(*pendingZoneValues)
	if !ok {
		t.Fatalf("pending.values is %T, want *pendingZoneValues", attr)
	}
	return pv
}

// firstBareNumberZoneValue finds a top-level key on the cloned zone whose value is a
// bare numeric scalar (float64 from json.Unmarshal) and is NOT one of the structural
// columns name/material/distribution (those are edited via SetField, not .values).
// Returns the key and its current value, or ok=false.
func firstBareNumberZoneValue(data map[string]any) (string, float64, bool) {
	structural := map[string]bool{
		"name": true, "material": true, "distribution": true, "geometry": true,
		"id": true, "posX": true, "posY": true, "radius": true, "radiusIsRelative": true,
	}
	// Sort keys for a stable choice across runs.
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if structural[k] {
			continue
		}
		if f, ok := data[k].(float64); ok {
			return k, f, true
		}
	}
	return "", 0, false
}

// TestPendingZoneValueBareRoundTrips: editing a bare numeric zone value on a clone
// before append creates a new zone carrying the edited value, leaving the template
// zone's value untouched (verified through a full reparse).
func TestPendingZoneValueBareRoundTrips(t *testing.T) {
	ls := loadFixture(t)
	pz := clonePendingZone(t, ls, 0)
	key, orig, ok := firstBareNumberZoneValue(pz.data)
	if !ok {
		t.Skip("fixture zone 0 has no bare numeric value")
	}
	const want = 0.123456
	pv := pendingZoneValuesOf(t, pz)
	if err := pv.SetKey(starlark.String(key), starlark.Float(want)); err != nil {
		t.Fatalf("SetKey(%q): %v", key, err)
	}
	// Nothing staged by the edit alone.
	if ls.stagedOps != 0 {
		t.Errorf("stagedOps = %d after value edit, want 0 (not staged until append)", ls.stagedOps)
	}
	// Read-back through Get observes the edit.
	gv, found, err := pv.Get(starlark.String(key))
	if err != nil || !found {
		t.Fatalf("Get(%q): found=%v err=%v", key, found, err)
	}
	if got := mustFloat(t, gv); got != want {
		t.Errorf("read-back %q = %v, want %v", key, got, want)
	}

	before := len(ls.tables.SettingsZones)
	if _, err := callMethod(t, pz, "append"); err != nil {
		t.Fatalf("append: %v", err)
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
	if len(tables.SettingsZones) != before+1 {
		t.Fatalf("zone count after clone-append = %d, want %d", len(tables.SettingsZones), before+1)
	}
	// The appended zone (last) carries the edited value; the template zone 0 keeps its
	// original value.
	appended := tables.SettingsZones[len(tables.SettingsZones)-1]
	var appendedData map[string]any
	if err := json.Unmarshal([]byte(appended.RawJSON), &appendedData); err != nil {
		t.Fatalf("unmarshal appended zone: %v", err)
	}
	if got, _ := appendedData[key].(float64); got != want {
		t.Errorf("appended zone %q = %v, want %v", key, got, want)
	}
	var templateData map[string]any
	if err := json.Unmarshal([]byte(tables.SettingsZones[0].RawJSON), &templateData); err != nil {
		t.Fatalf("unmarshal template zone: %v", err)
	}
	if got, _ := templateData[key].(float64); got != orig {
		t.Errorf("template zone %q = %v, want %v (unchanged)", key, got, orig)
	}
}

// TestPendingZoneValueWrappedRoundTrips: when a zone value is WRAPPED ({"Value": x,
// ...siblings}), editing it writes into ["Value"], preserves the wrapper and its
// sibling keys, and reparses as the same ScalarNumber. Because the live fixture's zone
// values happen to be bare, this synthesizes a wrapped value on the clone first.
func TestPendingZoneValueWrappedRoundTrips(t *testing.T) {
	ls := loadFixture(t)
	pz := clonePendingZone(t, ls, 0)
	key, _, ok := firstBareNumberZoneValue(pz.data)
	if !ok {
		t.Skip("fixture zone 0 has no numeric value to wrap")
	}
	// Promote the bare value to a wrapped value with a sibling key, emulating the
	// format's wrapped shape, so we exercise the wrapper-preserving write path.
	pz.data[key] = map[string]any{"Value": 1.0, "sibling": "keep-me"}

	const want = 0.654321
	pv := pendingZoneValuesOf(t, pz)
	if err := pv.SetKey(starlark.String(key), starlark.Float(want)); err != nil {
		t.Fatalf("SetKey(%q): %v", key, err)
	}
	// The in-memory map preserved the wrapper and its sibling.
	obj, ok := pz.data[key].(map[string]any)
	if !ok {
		t.Fatalf("after edit %q is %T, want map (wrapper clobbered)", key, pz.data[key])
	}
	if got, _ := obj["Value"].(float64); got != want {
		t.Errorf("wrapped %q.Value = %v, want %v", key, got, want)
	}
	if obj["sibling"] != "keep-me" {
		t.Errorf("wrapper sibling lost: %v", obj["sibling"])
	}

	before := len(ls.tables.SettingsZones)
	if _, err := callMethod(t, pz, "append"); err != nil {
		t.Fatalf("append: %v", err)
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
	if len(tables.SettingsZones) != before+1 {
		t.Fatalf("zone count after clone-append = %d, want %d", len(tables.SettingsZones), before+1)
	}
	appended := tables.SettingsZones[len(tables.SettingsZones)-1]
	got, found := findSetting(tables.SettingsZoneValues, zoneValuesOwnerID(&appended), key)
	if !found {
		t.Fatalf("appended zone value %q missing after reparse", key)
	}
	if got.Type != tb.ScalarNumber {
		t.Errorf("appended zone value %q type = %s, want %s (wrapper survived)", key, got.Type, tb.ScalarNumber)
	}
	if got.NumberValue != want {
		t.Errorf("appended zone value %q = %v, want %v", key, got.NumberValue, want)
	}
	// The wrapper sibling survives the round-trip in the raw JSON.
	var appendedData map[string]any
	if err := json.Unmarshal([]byte(appended.RawJSON), &appendedData); err != nil {
		t.Fatalf("unmarshal appended zone: %v", err)
	}
	wrapped, ok := appendedData[key].(map[string]any)
	if !ok {
		t.Fatalf("reparsed %q is %T, want wrapped object", key, appendedData[key])
	}
	if wrapped["sibling"] != "keep-me" {
		t.Errorf("reparsed wrapper sibling = %v, want %q", wrapped["sibling"], "keep-me")
	}
}

// TestPendingZoneValueIntStagesFloat: an integral Starlark int set on a numeric zone
// value is coerced to a float in the clone (the int->float fidelity rule), so the
// appended zone reparses as a number, not an int-shaped value.
func TestPendingZoneValueIntStagesFloat(t *testing.T) {
	ls := loadFixture(t)
	pz := clonePendingZone(t, ls, 0)
	key, _, ok := firstBareNumberZoneValue(pz.data)
	if !ok {
		t.Skip("fixture zone 0 has no bare numeric value")
	}
	pv := pendingZoneValuesOf(t, pz)
	if err := pv.SetKey(starlark.String(key), starlark.MakeInt(7)); err != nil {
		t.Fatalf("SetKey(%q, int): %v", key, err)
	}
	got, isFloat := pz.data[key].(float64)
	if !isFloat {
		t.Fatalf("after int set, %q is %T, want float64 (int->float coercion)", key, pz.data[key])
	}
	if got != 7.0 {
		t.Errorf("coerced %q = %v, want 7.0", key, got)
	}
}

// TestPendingZoneValueRejects: a type change, an absent key, and an edit after append
// each fail loudly and stage nothing.
func TestPendingZoneValueRejects(t *testing.T) {
	ls := loadFixture(t)
	pz := clonePendingZone(t, ls, 0)
	key, _, ok := firstBareNumberZoneValue(pz.data)
	if !ok {
		t.Skip("fixture zone 0 has no bare numeric value")
	}
	pv := pendingZoneValuesOf(t, pz)

	// Type change: a numeric value set to a string is rejected.
	if err := pv.SetKey(starlark.String(key), starlark.String("not-a-number")); err == nil {
		t.Errorf("SetKey(%q, string) on numeric value: want error, got nil", key)
	}
	// Absent key: no scaffolding.
	if err := pv.SetKey(starlark.String("definitely_not_a_zone_key"), starlark.Float(1)); err == nil {
		t.Errorf("SetKey(absent key): want error, got nil")
	}
	if ls.stagedOps != 0 {
		t.Errorf("stagedOps = %d after rejected edits, want 0", ls.stagedOps)
	}

	// After append, editing is guarded.
	if _, err := callMethod(t, pz, "append"); err != nil {
		t.Fatalf("append: %v", err)
	}
	stagedAfterAppend := ls.stagedOps
	if err := pv.SetKey(starlark.String(key), starlark.Float(0.5)); err == nil {
		t.Errorf("SetKey after append: want error, got nil")
	}
	if ls.stagedOps != stagedAfterAppend {
		t.Errorf("stagedOps changed from %d to %d on rejected post-append edit", stagedAfterAppend, ls.stagedOps)
	}
}

// TestPendingZoneValueViaScript: the end-to-end Starlark surface
// s = open(); z = s.zones.clone(0); z.name = "X"; z.values["k"] = v; z.append(); s.commit(tmp)
// creates a new zone whose name and edited value both persist.
func TestPendingZoneValueViaScript(t *testing.T) {
	ls := loadFixture(t)
	// Pick a real bare numeric key from a throwaway clone so we don't hardcode a name.
	probe := clonePendingZone(t, ls, 0)
	key, _, ok := firstBareNumberZoneValue(probe.data)
	if !ok {
		t.Skip("fixture zone 0 has no bare numeric value")
	}
	before := len(ls.tables.SettingsZones)
	tmp := filepath.Join(t.TempDir(), "out.zip")

	const wantName = "ScriptedValueZone"
	const wantVal = 0.91234
	program := []byte(fmt.Sprintf(`
s = open()

def mutate():
    z = s.zones.clone(0)
    z.name = %q
    z.values[%q] = %v
    z.append()
    return s.commit(%q)

print("staged=%%d" %% mutate())
`, wantName, key, wantVal, tmp))

	res, err := script.Run(context.Background(), program, Globals(ls), script.Options{Filename: "zones.star"})
	if err != nil {
		t.Fatalf("script.Run: %v (%+v)", err, res.Diagnostics)
	}

	re, err := tb.ParseFile(tmp, nil)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	tables := tb.ExtractTables(re.SHA256, re)
	if len(tables.SettingsZones) != before+1 {
		t.Fatalf("zone count after scripted clone-append = %d, want %d", len(tables.SettingsZones), before+1)
	}
	var found *tb.SettingsZoneRow
	for i := range tables.SettingsZones {
		if tables.SettingsZones[i].Name == wantName {
			found = &tables.SettingsZones[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("scripted zone %q not found after reparse", wantName)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(found.RawJSON), &data); err != nil {
		t.Fatalf("unmarshal scripted zone: %v", err)
	}
	if got, _ := data[key].(float64); got != wantVal {
		t.Errorf("scripted zone %q = %v, want %v", key, got, wantVal)
	}
}
