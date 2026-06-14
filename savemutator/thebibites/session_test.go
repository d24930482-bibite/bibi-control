package thebibites

import (
	"archive/zip"
	"bytes"
	"path/filepath"
	"slices"
	"testing"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

func TestSessionStagesAppliesAndCommitsSet(t *testing.T) {
	archive := parseSyntheticArchive(t)
	bibite := archive.Entry("bibites/bibite_0.bb8")
	scene := archive.Entry("scene.bb8scene")
	originalBibiteHash := bibite.SHA256
	originalBibiteRaw := append([]byte(nil), bibite.Raw...)
	originalSceneHash := scene.SHA256

	if got := archive.Bibites[0].Energy; got != 12.5 {
		t.Fatalf("initial parsed energy = %v, want 12.5", got)
	}

	session := NewSession(archive)
	if err := session.StageBibiteEnergy(BibiteRef{
		EntryName: "bibites/bibite_0.bb8",
		BodyID:    42,
	}, 99.25); err != nil {
		t.Fatalf("StageBibiteEnergy() error = %v", err)
	}
	if session.State() != StateStaged {
		t.Fatalf("state = %s, want %s", session.State(), StateStaged)
	}
	if !session.ProjectionsValid() {
		t.Fatalf("projections should remain valid before apply")
	}
	if !bytes.Equal(bibite.Raw, originalBibiteRaw) {
		t.Fatalf("stage changed entry raw bytes")
	}

	if err := session.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if session.State() != StateApplied {
		t.Fatalf("state = %s, want %s", session.State(), StateApplied)
	}
	if session.ProjectionsValid() {
		t.Fatalf("projections should be invalid after apply")
	}
	if got := session.DirtyEntries(); !slices.Equal(got, []string{"bibites/bibite_0.bb8"}) {
		t.Fatalf("dirty entries = %v", got)
	}
	if !bytes.HasPrefix(bibite.Raw, utf8BOM) {
		t.Fatalf("mutated entry did not preserve UTF-8 BOM")
	}
	if bibite.SHA256 == originalBibiteHash {
		t.Fatalf("bibite hash did not change")
	}
	if scene.SHA256 != originalSceneHash {
		t.Fatalf("unrelated scene hash changed")
	}
	if got := archive.Bibites[0].Energy; got != 12.5 {
		t.Fatalf("parsed projection energy changed before commit: %v", got)
	}

	outPath := filepath.Join(t.TempDir(), "mutated.zip")
	fresh, err := session.Commit(outPath)
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if session.State() != StateClean {
		t.Fatalf("state = %s, want %s", session.State(), StateClean)
	}
	if !session.ProjectionsValid() {
		t.Fatalf("projections should be valid after commit")
	}
	if len(session.DirtyEntries()) != 0 {
		t.Fatalf("dirty entries were not cleared after commit")
	}
	if got := fresh.Bibites[0].Energy; got != 99.25 {
		t.Fatalf("committed parsed energy = %v, want 99.25", got)
	}
	tables := tb.ExtractTables("mutated", fresh)
	if got := tables.Bibites[0].Energy; got != 99.25 {
		t.Fatalf("normalized energy = %v, want 99.25", got)
	}
	if got := fresh.Entry("scene.bb8scene").SHA256; got != originalSceneHash {
		t.Fatalf("committed unrelated scene hash = %s, want %s", got, originalSceneHash)
	}
}

func TestSessionSetSupportsNestedArrayPaths(t *testing.T) {
	archive := parseSyntheticArchive(t)
	session := NewSession(archive)
	target := BibiteTarget(BibiteRef{
		EntryName: "bibites/bibite_0.bb8",
		BodyID:    42,
	})
	if err := session.StageSet(target, "body.stomach.content[0].amount", 7.75); err != nil {
		t.Fatalf("StageSet() error = %v", err)
	}
	fresh, err := session.Commit(filepath.Join(t.TempDir(), "mutated.zip"))
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if got := fresh.Bibites[0].StomachContents[0].Amount; got != 7.75 {
		t.Fatalf("committed stomach amount = %v, want 7.75", got)
	}
}

func TestSetJSONPathSupportsQuotedMapKeys(t *testing.T) {
	root := map[string]any{
		"settingsChangers": []any{
			map[string]any{
				"settingsBases": map[string]any{
					"Zone(0).fertility": 0.7827918,
				},
			},
		},
	}

	path := `settingsChangers[0].settingsBases["Zone(0).fertility"]`
	if err := setJSONPath(root, path, 30.0, SetOptions{}); err != nil {
		t.Fatalf("setJSONPath() error = %v", err)
	}
	got, ok, err := getJSONPath(root, path)
	if err != nil {
		t.Fatalf("getJSONPath() error = %v", err)
	}
	if !ok || got != 30.0 {
		t.Fatalf("quoted key value = %v/%t, want 30/true", got, ok)
	}
}

func TestSessionSetUpdatesSettingsAndZones(t *testing.T) {
	session := NewSession(parseSyntheticArchive(t))

	zoneTarget := SettingsTarget(Require("zones[0].id", int64(7)))
	if err := session.StageSet(SettingsTarget(), "pelletEnergy.Value", 33.5); err != nil {
		t.Fatalf("StageSet(pelletEnergy) error = %v", err)
	}
	if err := session.StageSet(SettingsTarget(), "independents.worldSize.Value", 2000.0); err != nil {
		t.Fatalf("StageSet(worldSize) error = %v", err)
	}
	if err := session.StageSet(zoneTarget, "zones[0].name", "Updated Zone"); err != nil {
		t.Fatalf("StageSet(zone name) error = %v", err)
	}
	if err := session.StageSet(zoneTarget, "zones[0].fertility.Value", 0.85); err != nil {
		t.Fatalf("StageSet(zone fertility) error = %v", err)
	}
	if err := session.StageSet(zoneTarget, "zones[0].posX", -4.5); err != nil {
		t.Fatalf("StageSet(zone posX) error = %v", err)
	}
	if err := session.StageSet(zoneTarget, "zones[0].posY", 8.25); err != nil {
		t.Fatalf("StageSet(zone posY) error = %v", err)
	}
	if err := session.StageSet(zoneTarget, "zones[0].radius", 42.0); err != nil {
		t.Fatalf("StageSet(zone radius) error = %v", err)
	}
	if err := session.StageSet(zoneTarget, "zones[0].size", 99.0); err != nil {
		t.Fatalf("StageSet(zone size) error = %v", err)
	}

	fresh, err := session.Commit(filepath.Join(t.TempDir(), "mutated.zip"))
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	tables := tb.ExtractTables("settings", fresh)
	if got := settingNumber(t, tables.SettingsSimulationValues, "pelletEnergy"); got != 33.5 {
		t.Fatalf("pelletEnergy = %v, want 33.5", got)
	}
	if got := settingNumber(t, tables.SettingsIndependentValues, "worldSize"); got != 2000.0 {
		t.Fatalf("worldSize = %v, want 2000", got)
	}
	if len(tables.SettingsZones) != 1 {
		t.Fatalf("settings zones = %d, want 1", len(tables.SettingsZones))
	}
	if got := tables.SettingsZones[0].Name; got != "Updated Zone" {
		t.Fatalf("zone name = %q, want Updated Zone", got)
	}
	if got := settingNumber(t, tables.SettingsZoneValues, "fertility"); got != 0.85 {
		t.Fatalf("zone fertility = %v, want 0.85", got)
	}
	if got := settingNumber(t, tables.SettingsZoneValues, "size"); got != 99.0 {
		t.Fatalf("zone size = %v, want 99", got)
	}
	if len(tables.SettingsZoneGeometry) != 1 {
		t.Fatalf("zone geometry rows = %d, want 1", len(tables.SettingsZoneGeometry))
	}
	geometry := tables.SettingsZoneGeometry[0]
	if geometry.PositionX != -4.5 || geometry.PositionY != 8.25 || geometry.Radius != 42.0 {
		t.Fatalf("zone geometry = (%v, %v, r=%v), want (-4.5, 8.25, r=42)", geometry.PositionX, geometry.PositionY, geometry.Radius)
	}
}

func TestSessionSetUpdatesPellets(t *testing.T) {
	session := NewSession(parseSyntheticArchive(t))

	target := PelletsTarget(
		Require("pellets[0].zone", "Zone A"),
		Require("pellets[0].pellets[0].pellet.material", "Plant"),
	)
	if err := session.StageSet(target, "pellets[0].pellets[0].pellet.amount", 12.25); err != nil {
		t.Fatalf("StageSet(pellet amount) error = %v", err)
	}
	if err := session.StageSet(target, "pellets[0].pellets[0].transform.position[0]", -1.5); err != nil {
		t.Fatalf("StageSet(pellet x) error = %v", err)
	}
	if err := session.StageSet(target, "pellets[0].pellets[0].transform.position[1]", 2.25); err != nil {
		t.Fatalf("StageSet(pellet y) error = %v", err)
	}

	fresh, err := session.Commit(filepath.Join(t.TempDir(), "mutated.zip"))
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	tables := tb.ExtractTables("pellets", fresh)
	if len(tables.Pellets) != 1 {
		t.Fatalf("pellets = %d, want 1", len(tables.Pellets))
	}
	pellet := tables.Pellets[0]
	if pellet.Amount != 12.25 || pellet.TransformPositionX != -1.5 || pellet.TransformPositionY != 2.25 {
		t.Fatalf("pellet amount/location = %v (%v, %v), want 12.25 (-1.5, 2.25)", pellet.Amount, pellet.TransformPositionX, pellet.TransformPositionY)
	}
}

func TestSessionSetUpdatesBibiteGenesAndLocation(t *testing.T) {
	session := NewSession(parseSyntheticArchive(t))

	target := BibiteTarget(BibiteRef{
		EntryName: "bibites/bibite_0.bb8",
		BodyID:    42,
	})
	if err := session.StageSet(target, "genes.genes.Diet", 0.9); err != nil {
		t.Fatalf("StageSet(bibite gene) error = %v", err)
	}
	if err := session.StageSet(target, "transform.position[0]", -10.0); err != nil {
		t.Fatalf("StageSet(bibite transform x) error = %v", err)
	}
	if err := session.StageSet(target, "transform.position[1]", 14.5); err != nil {
		t.Fatalf("StageSet(bibite transform y) error = %v", err)
	}
	if err := session.StageSet(target, "rb2d.px", -10.0); err != nil {
		t.Fatalf("StageSet(bibite rb2d px) error = %v", err)
	}
	if err := session.StageSet(target, "rb2d.py", 14.5); err != nil {
		t.Fatalf("StageSet(bibite rb2d py) error = %v", err)
	}

	fresh, err := session.Commit(filepath.Join(t.TempDir(), "mutated.zip"))
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	tables := tb.ExtractTables("bibite", fresh)
	if len(tables.Bibites) != 1 {
		t.Fatalf("bibites = %d, want 1", len(tables.Bibites))
	}
	bibite := tables.Bibites[0]
	if bibite.TransformPositionX != -10.0 || bibite.TransformPositionY != 14.5 || bibite.RB2DPX != -10.0 || bibite.RB2DPY != 14.5 {
		t.Fatalf("bibite location = transform(%v, %v) rb2d(%v, %v), want (-10, 14.5) for both", bibite.TransformPositionX, bibite.TransformPositionY, bibite.RB2DPX, bibite.RB2DPY)
	}
	if got := geneNumber(t, tables.BibiteGenes, "Diet"); got != 0.9 {
		t.Fatalf("Diet gene = %v, want 0.9", got)
	}
}

func TestSessionRejectsGuardMismatchWithoutChangingRaw(t *testing.T) {
	archive := parseSyntheticArchive(t)
	bibite := archive.Entry("bibites/bibite_0.bb8")
	originalRaw := append([]byte(nil), bibite.Raw...)

	session := NewSession(archive)
	if err := session.StageBibiteEnergy(BibiteRef{
		EntryName: "bibites/bibite_0.bb8",
		BodyID:    999,
	}, 99.25); err != nil {
		t.Fatalf("StageBibiteEnergy() error = %v", err)
	}
	if err := session.Apply(); err == nil {
		t.Fatalf("Apply() error = nil, want guard mismatch")
	}
	if !bytes.Equal(bibite.Raw, originalRaw) {
		t.Fatalf("failed apply changed raw bytes")
	}
	if session.State() != StateStaged {
		t.Fatalf("state after failed apply = %s, want %s", session.State(), StateStaged)
	}
}

func TestSessionApplyIsAtomic(t *testing.T) {
	archive := parseSyntheticArchive(t)
	bibite := archive.Entry("bibites/bibite_0.bb8")
	originalRaw := append([]byte(nil), bibite.Raw...)

	session := NewSession(archive)
	target := BibiteTarget(BibiteRef{
		EntryName: "bibites/bibite_0.bb8",
		BodyID:    42,
	})
	if err := session.StageSet(target, "body.energy", 20.0); err != nil {
		t.Fatalf("StageSet(valid) error = %v", err)
	}
	if err := session.StageSet(target, "body.missing.value", 1.0); err != nil {
		t.Fatalf("StageSet(invalid target path) error = %v", err)
	}
	if err := session.Apply(); err == nil {
		t.Fatalf("Apply() error = nil, want missing path")
	}
	if !bytes.Equal(bibite.Raw, originalRaw) {
		t.Fatalf("failed atomic apply changed raw bytes")
	}
}

func TestSessionSetCanCreateFinalMissingKey(t *testing.T) {
	archive := parseSyntheticArchive(t)
	session := NewSession(archive)
	target := BibiteTarget(BibiteRef{
		EntryName: "bibites/bibite_0.bb8",
		BodyID:    42,
	})
	if err := session.StageSetWithOptions(target, "body.newValue", "ok", SetOptions{CreateMissing: true}); err != nil {
		t.Fatalf("StageSetWithOptions() error = %v", err)
	}
	fresh, err := session.Commit(filepath.Join(t.TempDir(), "mutated.zip"))
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	got, ok, err := getJSONPath(fresh.Entry("bibites/bibite_0.bb8").JSON, "body.newValue")
	if err != nil {
		t.Fatalf("getJSONPath() error = %v", err)
	}
	if !ok || got != "ok" {
		t.Fatalf("new field = %v/%t, want ok/true", got, ok)
	}
}

func TestStageRejectsUnsupportedOperationKind(t *testing.T) {
	session := NewSession(parseSyntheticArchive(t))
	err := session.Stage(Operation{
		Kind:   OperationKind("remove"),
		Target: EntryTarget("scene.bb8scene", tb.EntryScene),
		Path:   "nBibites",
	})
	if err == nil {
		t.Fatalf("Stage() error = nil, want unsupported operation kind")
	}
}

func parseSyntheticArchive(t *testing.T) *tb.Archive {
	t.Helper()

	rawBibite := append([]byte(nil), utf8BOM...)
	rawBibite = append(rawBibite, []byte(`{"transform":{"position":[1,2],"rotation":0,"scale":1},"rb2d":{"px":1,"py":2,"vx":0,"vy":0,"r":0},"body":{"id":42,"energy":12.5,"stomach":{"content":[{"material":"Plant","amount":2.5,"averageChunkAmount":1.25}]}},"genes":{"speciesID":3,"gen":4,"genes":{"Diet":0.1,"Speed":1.1}},"brain":{"Nodes":[],"Synapses":[]}}`)...)
	rawScene := append([]byte(nil), utf8BOM...)
	rawScene = append(rawScene, []byte(`{"nBibites":1}`)...)
	rawSettings := append([]byte(nil), utf8BOM...)
	rawSettings = append(rawSettings, []byte(`{"pelletEnergy":{"Value":20},"debugFlag":{"Value":true},"worldLabel":{"Value":"alpha"},"independents":{"worldSize":{"Value":1000}},"materials":{"Plant":{"energy":{"Value":2}}},"zones":[{"id":7,"name":"Zone A","material":"Plant","distribution":"uniform","posX":1,"posY":2,"radius":10,"radiusIsRelative":false,"fertility":{"Value":0.4},"size":5}],"zoneGroups":[],"bibites":[],"settingsChangers":[{"name":"season","repeat":true,"start":0,"settingsBases":{"Zone(0).fertility":0.4}}]}`)...)
	rawPellets := append([]byte(nil), utf8BOM...)
	rawPellets = append(rawPellets, []byte(`{"pellets":[{"zone":"Zone A","pellets":[{"transform":{"position":[3,4],"rotation":0,"scale":1},"rb2d":{"px":3,"py":4,"vx":0,"vy":0,"r":0},"pellet":{"material":"Plant","amount":5},"matterDecay":{"timeAlive":1,"rotAmount":2}}]}]}`)...)

	sourcePath := filepath.Join(t.TempDir(), "source.zip")
	archive := &tb.Archive{
		Entries: []tb.Entry{
			{
				Index:  0,
				Name:   "scene.bb8scene",
				Kind:   tb.EntryScene,
				Method: zip.Deflate,
				Raw:    rawScene,
			},
			{
				Index:  1,
				Name:   "settings.bb8settings",
				Kind:   tb.EntrySettings,
				Method: zip.Deflate,
				Raw:    rawSettings,
			},
			{
				Index:  2,
				Name:   "pellets.bb8scene",
				Kind:   tb.EntryPellets,
				Method: zip.Deflate,
				Raw:    rawPellets,
			},
			{
				Index:  3,
				Name:   "bibites/bibite_0.bb8",
				Kind:   tb.EntryBibite,
				Method: zip.Deflate,
				Raw:    rawBibite,
			},
		},
	}
	if err := tb.WriteArchive(sourcePath, archive); err != nil {
		t.Fatalf("WriteArchive(synthetic) error = %v", err)
	}
	parsed, err := tb.ParseFile(sourcePath, nil)
	if err != nil {
		t.Fatalf("ParseFile(synthetic) error = %v", err)
	}
	return parsed
}

func settingNumber(t *testing.T, rows []tb.SettingValueRow, name string) float64 {
	t.Helper()

	for _, row := range rows {
		if row.SettingName == name {
			return row.NumberValue
		}
	}
	t.Fatalf("missing setting %q", name)
	return 0
}

func settingBool(t *testing.T, rows []tb.SettingValueRow, name string) bool {
	t.Helper()

	for _, row := range rows {
		if row.SettingName == name {
			return row.BoolValue
		}
	}
	t.Fatalf("missing setting %q", name)
	return false
}

func settingString(t *testing.T, rows []tb.SettingValueRow, name string) string {
	t.Helper()

	for _, row := range rows {
		if row.SettingName == name {
			return row.StringValue
		}
	}
	t.Fatalf("missing setting %q", name)
	return ""
}

func changerTargetNumber(t *testing.T, rows []tb.SettingsChangerTargetRow, targetKey string) float64 {
	t.Helper()

	for _, row := range rows {
		if row.TargetKey == targetKey {
			return row.NumberValue
		}
	}
	t.Fatalf("missing changer target %q", targetKey)
	return 0
}

func geneNumber(t *testing.T, rows []tb.GeneRow, name string) float64 {
	t.Helper()

	for _, row := range rows {
		if row.GeneName == name {
			return row.NumberValue
		}
	}
	t.Fatalf("missing gene %q", name)
	return 0
}
