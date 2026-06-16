package thebibites

import (
	"path/filepath"
	"testing"

	"go.starlark.net/starlark"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
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
