package duckdb

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

func TestImportExtractedSaveCoversEveryTable(t *testing.T) {
	ctx := context.Background()
	db, err := Open("")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	save := sampleExtractedSave()
	if err := ImportExtractedSave(ctx, db, save); err != nil {
		t.Fatalf("ImportExtractedSave() error = %v", err)
	}

	for _, table := range allTables {
		if got := countRows(t, ctx, db, table, save.Archive.SaveID); got == 0 {
			t.Fatalf("%s rows = 0, want at least one", table)
		}
	}

	var entryName string
	var bodyID int64
	if err := db.QueryRowContext(ctx, `
		SELECT entry_name, body_id
		FROM bibite_mutation_refs
		WHERE save_id = ?
	`, save.Archive.SaveID).Scan(&entryName, &bodyID); err != nil {
		t.Fatalf("query bibite_mutation_refs: %v", err)
	}
	if entryName != "bibites/bibite_0.bb8" || bodyID != 42 {
		t.Fatalf("candidate ref = (%q, %d), want (bibites/bibite_0.bb8, 42)", entryName, bodyID)
	}

	if err := ImportExtractedSave(ctx, db, save); err != nil {
		t.Fatalf("second ImportExtractedSave() error = %v", err)
	}
	if got := countRows(t, ctx, db, "bibites", save.Archive.SaveID); got != 1 {
		t.Fatalf("bibites rows after replace = %d, want 1", got)
	}
}
func logImportRowCounts(t *testing.T, ctx context.Context, db *sql.DB, saveID string) {
	t.Helper()
	type tableCount struct {
		table string
		count int64
	}
	counts := make([]tableCount, 0, len(allTables))
	var total int64
	for _, table := range allTables {
		var n int64
		q := fmt.Sprintf("SELECT count(*) FROM %s WHERE save_id = ?", QuoteIdent(table))
		if err := db.QueryRowContext(ctx, q, saveID).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		counts = append(counts, tableCount{table, n})
		total += n
	}
	sort.Slice(counts, func(i, j int) bool { return counts[i].count > counts[j].count })
	t.Logf("imported %d rows for save_id %q across %d tables:", total, saveID, len(allTables))
	for _, c := range counts {
		if c.count == 0 {
			continue
		}
		t.Logf("  %8d  %s", c.count, c.table)
	}
}
func TestLargestFixtureQueryRefsExistInArchiveState(t *testing.T) {
	ctx := context.Background()
	const saveID = "largest-fixture-query"
	fixturePath := filepath.Join(repoRoot(t), "testdata", "saves", "the-bibites", "autosave_20260301021357.zip")

	archive, err := tb.ParseFile(fixturePath, nil)
	if err != nil {
		t.Fatalf("ParseFile(%q) error = %v", fixturePath, err)
	}
	save := tb.ExtractTables(saveID, archive)

	db, err := OpenAndImport(ctx, "", save)
	if err != nil {
		t.Fatalf("OpenAndImport() error = %v", err)
	}
	defer db.Close()

	expected := plantStomachRefsFromArchive(archive)
	if len(expected) == 0 {
		t.Fatalf("largest fixture archive has no bibites with Plant stomach content")
	}

	rows, err := db.QueryContext(ctx, `
		SELECT refs.entry_name, refs.body_id
		FROM bibite_mutation_refs refs
		WHERE refs.save_id = ?
		  AND EXISTS (
		      SELECT 1
		      FROM bibite_stomach_contents contents
		      WHERE contents.save_id = refs.save_id
		        AND contents.entry_name = refs.entry_name
		        AND contents.body_id = refs.body_id
		        AND contents.material = 'Plant'
		  )
		ORDER BY refs.entry_name, refs.body_id
	`, saveID)
	if err != nil {
		t.Fatalf("query Plant stomach refs: %v", err)
	}

	defer rows.Close()
	logImportRowCounts(t, ctx, db, saveID)
	got := make(map[bibiteRef]struct{})
	for rows.Next() {
		var ref bibiteRef
		if err := rows.Scan(&ref.entryName, &ref.bodyID); err != nil {
			t.Fatalf("scan Plant stomach ref: %v", err)
		}
		if archive.Entry(ref.entryName) == nil {
			t.Fatalf("DuckDB ref %q/%d does not have an archive entry", ref.entryName, ref.bodyID)
		}
		if _, ok := expected[ref]; !ok {
			t.Fatalf("DuckDB ref %q/%d was not found in parsed archive Plant stomach state", ref.entryName, ref.bodyID)
		}
		if _, ok := got[ref]; ok {
			t.Fatalf("DuckDB returned duplicate Plant stomach ref %q/%d", ref.entryName, ref.bodyID)
		}
		got[ref] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate Plant stomach refs: %v", err)
	}
	if len(got) != len(expected) {
		t.Fatalf("Plant stomach refs = %d, want %d from parsed archive state", len(got), len(expected))
	}
}
func TestDumpJSONScalarPaths(t *testing.T) {
	ctx := context.Background()
	const saveID = "largest-fixture-query"
	fixturePath := filepath.Join(repoRoot(t), "testdata", "saves", "the-bibites", "autosave_20260301021357.zip")
	archive, err := tb.ParseFile(fixturePath, nil)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	save := tb.ExtractTables(saveID, archive)
	db, err := OpenAndImport(ctx, "", save)
	if err != nil {
		t.Fatalf("OpenAndImport: %v", err)
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, `
		SELECT path, count(*) AS n
		FROM json_scalars
		WHERE save_id = ?
		GROUP BY path
		ORDER BY n DESC
		LIMIT 40
	`, saveID)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var path string
		var n int64
		if err := rows.Scan(&path, &n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		t.Logf("%8d  %s", n, path)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller(0) failed")
	}
	return filepath.Dir(filepath.Dir(file))
}

func countRows(t *testing.T, ctx context.Context, db *sql.DB, table, saveID string) int64 {
	t.Helper()

	query := "SELECT count(*) FROM " + QuoteIdent(table) + " WHERE save_id = ?"
	var count int64
	if err := db.QueryRowContext(ctx, query, saveID).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

type bibiteRef struct {
	entryName string
	bodyID    int64
}

func plantStomachRefsFromArchive(archive *tb.Archive) map[bibiteRef]struct{} {
	refs := make(map[bibiteRef]struct{})
	for _, bibite := range archive.Bibites {
		if !bibite.HasID {
			continue
		}
		for _, content := range bibite.StomachContents {
			if content.Material == "Plant" {
				refs[bibiteRef{
					entryName: bibite.EntryName,
					bodyID:    bibite.ID,
				}] = struct{}{}
				break
			}
		}
	}
	return refs
}

func sampleExtractedSave() tb.ExtractedSave {
	const saveID = "sample-save"

	return tb.ExtractedSave{
		Archive: tb.SaveArchiveRow{
			SaveID:     saveID,
			SourcePath: "/tmp/source.zip",
			FileName:   "source.zip",
			SizeBytes:  12345,
			SHA256:     "archive-sha",
		},
		Entries: []tb.SaveEntryRow{{
			SaveID:           saveID,
			EntryIndex:       0,
			EntryName:        "bibites/bibite_0.bb8",
			Kind:             tb.EntryBibite,
			SHA256:           "entry-sha",
			CompressedSize:   100,
			UncompressedSize: 200,
			HasUTF8BOM:       true,
		}},
		Diagnostics: []tb.DiagnosticRow{{
			SaveID:    saveID,
			EntryName: "bibites/bibite_0.bb8",
			Severity:  tb.SeverityWarning,
			Code:      "sample",
			Message:   "sample diagnostic",
		}},
		Scene: &tb.SceneRow{
			SaveID:              saveID,
			EntryName:           "scene.bb8scene",
			Version:             "1",
			SimulatedTime:       10.5,
			HasSimulatedTime:    true,
			ReportedNBibites:    1,
			HasReportedNBibites: true,
			ReportedNPellets:    2,
			HasReportedNPellets: true,
			ParsedBibites:       1,
			ParsedEggs:          1,
			AliveBibites:        1,
			DeadBibites:         0,
			DyingBibites:        0,
			ParsedPellets:       1,
		},
		Vars: &tb.VarsRow{
			SaveID:        saveID,
			EntryName:     "vars.bb8scene",
			TowerMaxID:    99,
			HasTowerMaxID: true,
		},
		SceneColorSelectors: []tb.SceneColorSelectorRow{{
			SaveID:             saveID,
			EntryName:          "scene.bb8scene",
			ColorSelectorIndex: 0,
			RawJSON:            `{"color":"red"}`,
		}},
		ScenePheroTowers: []tb.SceneTowerRow{{
			SaveID:     saveID,
			EntryName:  "scene.bb8scene",
			TowerKind:  "phero",
			TowerIndex: 0,
			RawJSON:    `{"tower":"phero"}`,
		}},
		SceneRadTowers: []tb.SceneTowerRow{{
			SaveID:     saveID,
			EntryName:  "scene.bb8scene",
			TowerKind:  "rad",
			TowerIndex: 0,
			RawJSON:    `{"tower":"rad"}`,
		}},
		SettingsSimulationValues:  []tb.SettingValueRow{sampleSettingValue(saveID, "settings.bb8settings", "sim", "pelletEnergy")},
		SettingsIndependentValues: []tb.SettingValueRow{sampleSettingValue(saveID, "settings.bb8settings", "independent", "worldSize")},
		SettingsMaterials: []tb.SettingsMaterialRow{{
			SaveID:        saveID,
			EntryName:     "settings.bb8settings",
			MaterialIndex: 0,
			MaterialName:  "Plant",
			RawJSON:       `{"name":"Plant"}`,
		}},
		SettingsMaterialValues: []tb.SettingValueRow{sampleSettingValue(saveID, "settings.bb8settings", "material", "energy")},
		SettingsZones: []tb.SettingsZoneRow{{
			SaveID:       saveID,
			EntryName:    "settings.bb8settings",
			ZoneIndex:    0,
			ZoneID:       7,
			HasZoneID:    true,
			Name:         "Zone A",
			Material:     "Plant",
			Distribution: "uniform",
			RawJSON:      `{"id":7}`,
		}},
		SettingsZoneGeometry: []tb.SettingsZoneGeometryRow{{
			SaveID:           saveID,
			EntryName:        "settings.bb8settings",
			ZoneIndex:        0,
			ZoneID:           7,
			HasZoneID:        true,
			GeometryIndex:    0,
			GeometryKind:     "circle",
			PositionX:        1.5,
			PositionY:        2.5,
			Radius:           10,
			RadiusIsRelative: false,
			RawJSON:          `{"radius":10}`,
		}},
		SettingsZoneValues: []tb.SettingValueRow{sampleSettingValue(saveID, "settings.bb8settings", "zone", "fertility")},
		SettingsZoneGroups: []tb.SettingsZoneGroupRow{{
			SaveID:     saveID,
			EntryName:  "settings.bb8settings",
			GroupIndex: 0,
			Name:       "Group A",
			RawJSON:    `{"name":"Group A"}`,
		}},
		SettingsBibiteSpawners: []tb.SettingsBibiteSpawnerRow{{
			SaveID:         saveID,
			EntryName:      "settings.bb8settings",
			SpawnerIndex:   0,
			Path:           "bibites[0]",
			SpawnPriority:  1,
			Minimum:        2,
			RandomizeGenes: "false",
			GrowthAtSpawn:  "adult",
			Tagging:        "none",
			SpawnType:      "default",
			TotalSpawned:   3,
			RawJSON:        `{"spawnPriority":1}`,
		}},
		SettingsChangers: []tb.SettingsChangerRow{{
			SaveID:       saveID,
			EntryName:    "settings.bb8settings",
			ChangerIndex: 0,
			Name:         "Changer",
			Repeat:       true,
			Start:        4,
			RawJSON:      `{"name":"Changer"}`,
		}},
		SettingsChangerPoints: []tb.SettingsChangerPointRow{{
			SaveID:       saveID,
			EntryName:    "settings.bb8settings",
			ChangerIndex: 0,
			PointIndex:   0,
			T:            1,
			Y:            2,
			D:            "linear",
			F:            3,
		}},
		SettingsChangerTargets: []tb.SettingsChangerTargetRow{{
			SaveID:       saveID,
			EntryName:    "settings.bb8settings",
			ChangerIndex: 0,
			TargetKey:    "target",
			Scope:        "zone",
			ZoneIndex:    0,
			ZoneID:       7,
			HasZoneID:    true,
			SettingName:  "fertility",
			Type:         tb.ScalarNumber,
			NumberValue:  0.5,
			StringValue:  "",
			BoolValue:    false,
			RawJSON:      `0.5`,
		}},
		ActiveSpecies: []tb.ActiveSpeciesRow{{
			SaveID:             saveID,
			EntryName:          "speciesData.json",
			ActiveSpeciesIndex: 0,
			SpeciesID:          3,
		}},
		Species: []tb.SpeciesRow{{
			SaveID:                    saveID,
			EntryName:                 "speciesData.json",
			SpeciesIndex:              0,
			SpeciesID:                 3,
			HasSpeciesID:              true,
			ParentID:                  2,
			HasParentID:               true,
			GenerationOfFirstSpecimen: 4,
			TimeCreation:              5,
			Favorite:                  true,
			GenericName:               "Generic",
			SpecificName:              "Specific",
			Description:               "Description",
			TemplateVersion:           "1",
		}},
		SpeciesGenes:         []tb.GeneRow{sampleGene(saveID, "species_template", "3", "speciesData.json", "Diet")},
		SpeciesBrainNodes:    []tb.BrainNodeRow{sampleBrainNode(saveID, "species_template", "3", "speciesData.json")},
		SpeciesBrainSynapses: []tb.BrainSynapseRow{sampleBrainSynapse(saveID, "species_template", "3", "speciesData.json")},
		Bibites: []tb.BibiteRow{{
			SaveID:             saveID,
			EntryName:          "bibites/bibite_0.bb8",
			BodyID:             42,
			HasBodyID:          true,
			SpeciesID:          3,
			Generation:         4,
			Dead:               false,
			Dying:              false,
			Health:             1,
			Energy:             12.5,
			TimeAlive:          6,
			TransformPositionX: 1,
			TransformPositionY: 2,
			TransformRotation:  3,
			TransformScale:     1,
			RB2DPX:             1,
			RB2DPY:             2,
			RB2DVX:             0.1,
			RB2DVY:             0.2,
			RB2DR:              0.3,
		}},
		BibiteGenes: []tb.GeneRow{sampleGene(saveID, "bibite", "42", "bibites/bibite_0.bb8", "Speed")},
		BibiteBody: []tb.BibiteBodyRow{{
			SaveID:              saveID,
			EntryName:           "bibites/bibite_0.bb8",
			BodyID:              42,
			HasBodyID:           true,
			D2Size:              1,
			FatReservesAmount:   2,
			AttackedDmg:         3,
			TimesAttacked:       4,
			TotalDamageSuffered: 5,
			BrainTicksCount:     6,
			VisionLookupCount:   7,
			VisionSensingCount:  8,
			CorpseEnergyOffset:  9,
		}},
		BibiteMouth: []tb.BibiteMouthRow{{
			SaveID:            saveID,
			EntryName:         "bibites/bibite_0.bb8",
			BodyID:            42,
			HasBodyID:         true,
			AttackedLastFrame: true,
			BibitesBitten:     1,
			BiteProgress:      2,
			MurderedArea:      3,
			TotalDamageDealt:  4,
			TotalMurders:      5,
		}},
		BibitePheromoneEmitters: []tb.BibitePheromoneEmitterRow{{
			SaveID:    saveID,
			EntryName: "bibites/bibite_0.bb8",
			BodyID:    42,
			HasBodyID: true,
			Progress:  0.1,
		}},
		BibiteEggLayers: []tb.BibiteEggLayerRow{{
			SaveID:      saveID,
			EntryName:   "bibites/bibite_0.bb8",
			BodyID:      42,
			HasBodyID:   true,
			EggProgress: 0.2,
			NEggsLaid:   2,
		}},
		BibiteControl: []tb.BibiteControlRow{{
			SaveID:      saveID,
			EntryName:   "bibites/bibite_0.bb8",
			BodyID:      42,
			HasBodyID:   true,
			TotalTravel: 10,
		}},
		BibiteStomachContents: []tb.StomachContentRow{{
			SaveID:             saveID,
			EntryName:          "bibites/bibite_0.bb8",
			BodyID:             42,
			HasBodyID:          true,
			ContentIndex:       0,
			Material:           "Plant",
			Amount:             1,
			AverageChunkAmount: 0.5,
		}},
		BibiteChildren: []tb.BibiteChildRow{{
			SaveID:       saveID,
			EntryName:    "bibites/bibite_0.bb8",
			ParentBodyID: 42,
			HasParentID:  true,
			ChildIndex:   0,
			ChildBodyID:  43,
		}},
		BibiteBrainNodes:    []tb.BrainNodeRow{sampleBrainNode(saveID, "bibite", "42", "bibites/bibite_0.bb8")},
		BibiteBrainSynapses: []tb.BrainSynapseRow{sampleBrainSynapse(saveID, "bibite", "42", "bibites/bibite_0.bb8")},
		Eggs: []tb.EggRow{{
			SaveID:             saveID,
			EntryName:          "eggs/egg_0.bb8",
			EggID:              44,
			HasEggID:           true,
			SpeciesID:          3,
			Generation:         5,
			HatchProgress:      0.5,
			Energy:             3,
			TransformPositionX: 1,
			TransformPositionY: 2,
			TransformRotation:  3,
			TransformScale:     1,
			RB2DPX:             1,
			RB2DPY:             2,
			RB2DVX:             0.1,
			RB2DVY:             0.2,
			RB2DR:              0.3,
		}},
		EggGenes:         []tb.GeneRow{sampleGene(saveID, "egg", "44", "eggs/egg_0.bb8", "Growth")},
		EggBrainNodes:    []tb.BrainNodeRow{sampleBrainNode(saveID, "egg", "44", "eggs/egg_0.bb8")},
		EggBrainSynapses: []tb.BrainSynapseRow{sampleBrainSynapse(saveID, "egg", "44", "eggs/egg_0.bb8")},
		PelletGroups: []tb.PelletGroupRow{{
			SaveID:      saveID,
			EntryName:   "pellets.bb8scene",
			GroupIndex:  0,
			Zone:        "Zone A",
			PelletCount: 1,
		}},
		Pellets: []tb.PelletRow{{
			SaveID:               saveID,
			EntryName:            "pellets.bb8scene",
			PelletIndex:          0,
			GroupIndex:           0,
			GroupPelletIndex:     0,
			Zone:                 "Zone A",
			Material:             "Plant",
			Amount:               2,
			MatterDecayTimeAlive: 1,
			MatterDecayRotAmount: 2,
			HasMatterDecay:       true,
			TransformPositionX:   1,
			TransformPositionY:   2,
			TransformRotation:    3,
			TransformScale:       1,
			RB2DPX:               1,
			RB2DPY:               2,
			RB2DVX:               0.1,
			RB2DVY:               0.2,
			RB2DR:                0.3,
		}},
		Pheromones: []tb.PheromoneRow{{
			SaveID:             saveID,
			EntryName:          "pheromones.bb8scene",
			PheromoneIndex:     0,
			TransformPositionX: 1,
			TransformPositionY: 2,
			TransformRotation:  3,
			TransformScale:     1,
			HeadingRawJSON:     `{"x":1}`,
			RStrength:          0.1,
			GStrength:          0.2,
			BStrength:          0.3,
			NR:                 1,
			NG:                 2,
			NB:                 3,
		}},
		JSONScalars: []tb.ScalarRow{{
			SaveID:      saveID,
			EntryName:   "bibites/bibite_0.bb8",
			OwnerKind:   "bibite",
			OwnerID:     "42",
			Path:        "body.health",
			Type:        tb.ScalarNumber,
			NumberValue: 1,
			StringValue: "",
			BoolValue:   false,
			RawJSON:     `1`,
		}},
	}
}

func sampleSettingValue(saveID, entryName, scope, name string) tb.SettingValueRow {
	return tb.SettingValueRow{
		SaveID:         saveID,
		EntryName:      entryName,
		Scope:          scope,
		OwnerKind:      scope,
		OwnerID:        "owner",
		SettingName:    name,
		Path:           name + ".Value",
		Type:           tb.ScalarNumber,
		NumberValue:    1.25,
		StringValue:    "",
		BoolValue:      false,
		RawJSON:        `1.25`,
		WrapperRawJSON: `{"Value":1.25}`,
	}
}

func sampleGene(saveID, ownerKind, ownerID, entryName, name string) tb.GeneRow {
	return tb.GeneRow{
		SaveID:      saveID,
		OwnerKind:   ownerKind,
		OwnerID:     ownerID,
		EntryName:   entryName,
		GeneName:    name,
		Path:        "genes.genes." + name,
		Type:        tb.ScalarNumber,
		NumberValue: 0.75,
		BoolValue:   false,
		StringValue: "",
		RawJSON:     `0.75`,
	}
}

func sampleBrainNode(saveID, ownerKind, ownerID, entryName string) tb.BrainNodeRow {
	return tb.BrainNodeRow{
		SaveID:         saveID,
		OwnerKind:      ownerKind,
		OwnerID:        ownerID,
		EntryName:      entryName,
		NodeRowIndex:   0,
		NodeIndex:      1,
		Innovation:     2,
		Type:           3,
		TypeName:       "Input",
		Desc:           "node",
		Archetype:      4,
		BaseActivation: 0.1,
		Value:          0.2,
		LastInput:      0.3,
		LastOutput:     0.4,
	}
}

func sampleBrainSynapse(saveID, ownerKind, ownerID, entryName string) tb.BrainSynapseRow {
	return tb.BrainSynapseRow{
		SaveID:          saveID,
		OwnerKind:       ownerKind,
		OwnerID:         ownerID,
		EntryName:       entryName,
		SynapseRowIndex: 0,
		Innovation:      1,
		NodeIn:          2,
		NodeOut:         3,
		Weight:          0.9,
		Enabled:         true,
	}
}
