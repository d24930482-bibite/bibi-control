package thebibites

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

func stageSQLRefSet(t *testing.T, session *Session, ref SQLValueRef, expected, value any) {
	t.Helper()

	if err := session.StageSQLSet(ref.WithExpected(expected), value); err != nil {
		t.Fatalf("StageSQLSet(%s.%s) error = %v", ref.Table, ref.Column, err)
	}
}

func TestStageSQLSetStagesResolvesAndCommits(t *testing.T) {
	session := NewSession(parseSyntheticArchive(t))

	stageSQLRefSet(t, session, SQLValueRef{
		Table:     "bibites",
		Column:    "energy",
		EntryName: "bibites/bibite_0.bb8",
		BodyID:    42,
		HasBodyID: true,
	}, 12.5, 33.25)
	stageSQLRefSet(t, session, SQLValueRef{
		Table:     "bibite_genes",
		Column:    "number_value",
		EntryName: "bibites/bibite_0.bb8",
		OwnerKind: "bibite",
		OwnerID:   "42",
		Path:      "genes.genes.Diet",
	}, 0.1, 0.55)
	stageSQLRefSet(t, session, SQLValueRef{
		Table:           "bibite_stomach_contents",
		Column:          "amount",
		EntryName:       "bibites/bibite_0.bb8",
		BodyID:          42,
		HasBodyID:       true,
		ContentIndex:    0,
		HasContentIndex: true,
	}, 2.5, 8.75)
	stageSQLRefSet(t, session, SQLValueRef{
		Table:               "pellets",
		Column:              "amount",
		EntryName:           PelletsEntryName,
		GroupIndex:          0,
		HasGroupIndex:       true,
		GroupPelletIndex:    0,
		HasGroupPelletIndex: true,
		Zone:                "Zone A",
		HasZone:             true,
	}, 5.0, 9.5)
	stageSQLRefSet(t, session, SQLValueRef{
		Table:        "settings_zones",
		Column:       "name",
		EntryName:    SettingsEntryName,
		ZoneIndex:    0,
		HasZoneIndex: true,
		ZoneID:       7,
		HasZoneID:    true,
	}, "Zone A", "SQL Zone")

	fresh, err := session.Commit(filepath.Join(t.TempDir(), "mutated.zip"))
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	tables := tb.ExtractTables("sql-ref", fresh)
	if got := tables.Bibites[0].Energy; got != 33.25 {
		t.Fatalf("bibite energy = %v, want 33.25", got)
	}
	if got := geneNumber(t, tables.BibiteGenes, "Diet"); got != 0.55 {
		t.Fatalf("Diet gene = %v, want 0.55", got)
	}
	if got := tables.BibiteStomachContents[0].Amount; got != 8.75 {
		t.Fatalf("stomach amount = %v, want 8.75", got)
	}
	if got := tables.Pellets[0].Amount; got != 9.5 {
		t.Fatalf("pellet amount = %v, want 9.5", got)
	}
	if got := tables.Pellets[0].GroupPelletIndex; got != 0 {
		t.Fatalf("pellet group-local index = %d, want 0", got)
	}
	if got := tables.SettingsZones[0].Name; got != "SQL Zone" {
		t.Fatalf("settings zone name = %q, want SQL Zone", got)
	}
}

func TestStageSQLSetUpdatesSettingsValueRows(t *testing.T) {
	session := NewSession(parseSyntheticArchive(t))

	stageSQLRefSet(t, session, SQLValueRef{
		Table:          "settings_simulation_values",
		Column:         "number_value",
		EntryName:      SettingsEntryName,
		OwnerKind:      "settings",
		OwnerID:        "settings",
		SettingName:    "pelletEnergy",
		Path:           "settings.pelletEnergy",
		ValueType:      "number",
		WrapperRawJSON: `{"Value":20}`,
	}, 20.0, 33.5)
	stageSQLRefSet(t, session, SQLValueRef{
		Table:          "settings_simulation_values",
		Column:         "bool_value",
		EntryName:      SettingsEntryName,
		OwnerKind:      "settings",
		OwnerID:        "settings",
		SettingName:    "debugFlag",
		Path:           "settings.debugFlag",
		ValueType:      "bool",
		WrapperRawJSON: `{"Value":true}`,
	}, true, false)
	stageSQLRefSet(t, session, SQLValueRef{
		Table:          "settings_simulation_values",
		Column:         "string_value",
		EntryName:      SettingsEntryName,
		OwnerKind:      "settings",
		OwnerID:        "settings",
		SettingName:    "worldLabel",
		Path:           "settings.worldLabel",
		ValueType:      "string",
		WrapperRawJSON: `{"Value":"alpha"}`,
	}, "alpha", "beta")
	stageSQLRefSet(t, session, SQLValueRef{
		Table:          "settings_independent_values",
		Column:         "number_value",
		EntryName:      SettingsEntryName,
		OwnerKind:      "settings_independent",
		OwnerID:        "independents",
		SettingName:    "worldSize",
		Path:           "settings.independents.worldSize",
		ValueType:      "number",
		WrapperRawJSON: `{"Value":1000}`,
	}, 1000.0, 2000.0)
	stageSQLRefSet(t, session, SQLValueRef{
		Table:          "settings_material_values",
		Column:         "number_value",
		EntryName:      SettingsEntryName,
		OwnerKind:      "settings_material",
		OwnerID:        "Plant",
		SettingName:    "energy",
		Path:           "settings.materials.Plant.energy",
		ValueType:      "number",
		WrapperRawJSON: `{"Value":2}`,
	}, 2.0, 4.25)
	stageSQLRefSet(t, session, SQLValueRef{
		Table:          "settings_zone_values",
		Column:         "number_value",
		EntryName:      SettingsEntryName,
		OwnerKind:      "settings_zone",
		OwnerID:        "7",
		SettingName:    "fertility",
		Path:           "settings.zones[0].fertility",
		ValueType:      "number",
		WrapperRawJSON: `{"Value":0.4}`,
		ZoneIndex:      0,
		HasZoneIndex:   true,
		ZoneID:         7,
		HasZoneID:      true,
	}, 0.4, 0.85)
	stageSQLRefSet(t, session, SQLValueRef{
		Table:          "settings_zone_values",
		Column:         "number_value",
		EntryName:      SettingsEntryName,
		OwnerKind:      "settings_zone",
		OwnerID:        "7",
		SettingName:    "size",
		Path:           "settings.zones[0].size",
		ValueType:      "number",
		WrapperRawJSON: `5`,
		ZoneIndex:      0,
		HasZoneIndex:   true,
		ZoneID:         7,
		HasZoneID:      true,
	}, 5.0, 99.0)
	stageSQLRefSet(t, session, SQLValueRef{
		Table:           "settings_changer_targets",
		Column:          "number_value",
		EntryName:       SettingsEntryName,
		ChangerIndex:    0,
		HasChangerIndex: true,
		TargetKey:       "Zone(0).fertility",
		Scope:           "zone",
		ZoneIndex:       0,
		HasZoneIndex:    true,
		ZoneID:          7,
		HasZoneID:       true,
		SettingName:     "fertility",
		ValueType:       "number",
	}, 0.4, 30.0)

	fresh, err := session.Commit(filepath.Join(t.TempDir(), "mutated.zip"))
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	tables := tb.ExtractTables("settings-values", fresh)
	if got := settingNumber(t, tables.SettingsSimulationValues, "pelletEnergy"); got != 33.5 {
		t.Fatalf("pelletEnergy = %v, want 33.5", got)
	}
	if got := settingBool(t, tables.SettingsSimulationValues, "debugFlag"); got {
		t.Fatalf("debugFlag = %v, want false", got)
	}
	if got := settingString(t, tables.SettingsSimulationValues, "worldLabel"); got != "beta" {
		t.Fatalf("worldLabel = %q, want beta", got)
	}
	if got := settingNumber(t, tables.SettingsIndependentValues, "worldSize"); got != 2000.0 {
		t.Fatalf("worldSize = %v, want 2000", got)
	}
	if got := settingNumber(t, tables.SettingsMaterialValues, "energy"); got != 4.25 {
		t.Fatalf("material energy = %v, want 4.25", got)
	}
	if got := settingNumber(t, tables.SettingsZoneValues, "fertility"); got != 0.85 {
		t.Fatalf("zone fertility = %v, want 0.85", got)
	}
	if got := settingNumber(t, tables.SettingsZoneValues, "size"); got != 99.0 {
		t.Fatalf("zone size = %v, want 99", got)
	}
	if got := changerTargetNumber(t, tables.SettingsChangerTargets, "Zone(0).fertility"); got != 30.0 {
		t.Fatalf("changer Zone(0).fertility = %v, want 30", got)
	}
}

func TestResolveSQLValueRefsAllowlist(t *testing.T) {
	const (
		bibiteEntry = "bibites/bibite_0.bb8"
		eggEntry    = "eggs/egg_0.bb8egg"
	)

	bibiteBase := SQLValueRef{
		EntryName: bibiteEntry,
		BodyID:    42,
		HasBodyID: true,
	}
	bibiteTarget := BibiteTarget(BibiteRef{
		EntryName: bibiteEntry,
		BodyID:    42,
	})
	eggBase := SQLValueRef{
		EntryName: eggEntry,
		EggID:     99,
		HasEggID:  true,
	}
	eggTarget := EntryTarget(eggEntry, tb.EntryEgg, Require("egg.id", int64(99)))

	var tests []sqlRefResolveCase
	for _, table := range []struct {
		name    string
		columns map[string]string
	}{
		{name: "bibites", columns: bibiteColumnPaths},
		{name: "bibite_body", columns: bibiteBodyColumnPaths},
		{name: "bibite_mouth", columns: bibiteMouthColumnPaths},
		{name: "bibite_pheromone_emitters", columns: bibitePheromoneColumnPaths},
		{name: "bibite_egg_layers", columns: bibiteEggLayerColumnPaths},
		{name: "bibite_control", columns: bibiteControlColumnPaths},
	} {
		addSQLRefPathCases(&tests, table.name, bibiteBase, bibiteTarget, table.columns, sqlRefPath)
	}

	stomachBase := bibiteBase
	stomachBase.ContentIndex = 2
	stomachBase.HasContentIndex = true
	addSQLRefPathCases(&tests, "bibite_stomach_contents", stomachBase, bibiteTarget, bibiteStomachContentColumnFields, func(field string) string {
		return fmt.Sprintf("body.stomach.content[%d].%s", stomachBase.ContentIndex, field)
	})

	addSQLRefValueColumnCases(&tests, "bibite_genes", SQLValueRef{
		EntryName: bibiteEntry,
		OwnerKind: "bibite",
		OwnerID:   "42",
		Path:      "genes.genes.TestGene",
	}, bibiteTarget, "genes.genes.TestGene")

	nodeBase := bibiteBase
	nodeBase.NodeRowIndex = 3
	nodeBase.HasNodeRowIndex = true
	addSQLRefPathCases(&tests, "bibite_brain_nodes", nodeBase, bibiteTarget, brainNodeColumnKeys, func(key string) string {
		return fmt.Sprintf("brain.Nodes[%d].%s", nodeBase.NodeRowIndex, key)
	})
	synapseBase := bibiteBase
	synapseBase.SynapseRowIndex = 4
	synapseBase.HasSynapseRowIndex = true
	addSQLRefPathCases(&tests, "bibite_brain_synapses", synapseBase, bibiteTarget, brainSynapseColumnKeys, func(key string) string {
		return fmt.Sprintf("brain.Synapses[%d].%s", synapseBase.SynapseRowIndex, key)
	})

	addSQLRefPathCases(&tests, "eggs", eggBase, eggTarget, eggColumnPaths, sqlRefPath)
	addSQLRefValueColumnCases(&tests, "egg_genes", SQLValueRef{
		EntryName: eggEntry,
		OwnerKind: "egg",
		OwnerID:   "99",
		Path:      "genes.genes.TestGene",
	}, eggTarget, "genes.genes.TestGene")

	eggNodeBase := eggBase
	eggNodeBase.NodeRowIndex = 3
	eggNodeBase.HasNodeRowIndex = true
	addSQLRefPathCases(&tests, "egg_brain_nodes", eggNodeBase, eggTarget, brainNodeColumnKeys, func(key string) string {
		return fmt.Sprintf("brain.Nodes[%d].%s", eggNodeBase.NodeRowIndex, key)
	})
	eggSynapseBase := eggBase
	eggSynapseBase.SynapseRowIndex = 4
	eggSynapseBase.HasSynapseRowIndex = true
	addSQLRefPathCases(&tests, "egg_brain_synapses", eggSynapseBase, eggTarget, brainSynapseColumnKeys, func(key string) string {
		return fmt.Sprintf("brain.Synapses[%d].%s", eggSynapseBase.SynapseRowIndex, key)
	})

	pelletBase := SQLValueRef{
		EntryName:           PelletsEntryName,
		GroupIndex:          1,
		HasGroupIndex:       true,
		GroupPelletIndex:    2,
		HasGroupPelletIndex: true,
		Zone:                "Zone A",
		HasZone:             true,
	}
	pelletTarget := EntryTarget(PelletsEntryName, tb.EntryPellets, Require("pellets[1].zone", "Zone A"))
	addSQLRefPathCases(&tests, "pellets", pelletBase, pelletTarget, pelletColumnPaths, func(path string) string {
		return fmt.Sprintf("pellets[%d].pellets[%d].%s", pelletBase.GroupIndex, pelletBase.GroupPelletIndex, path)
	})

	pheromoneBase := SQLValueRef{
		EntryName:         PheromonesEntryName,
		PheromoneIndex:    5,
		HasPheromoneIndex: true,
	}
	addSQLRefPathCases(&tests, "pheromones", pheromoneBase, EntryTarget(PheromonesEntryName, tb.EntryPheromones), pheromoneColumnPaths, func(path string) string {
		return fmt.Sprintf("pheromones[%d].%s", pheromoneBase.PheromoneIndex, path)
	})

	settingsZoneBase := SQLValueRef{
		EntryName:    SettingsEntryName,
		ZoneIndex:    1,
		HasZoneIndex: true,
		ZoneID:       77,
		HasZoneID:    true,
	}
	settingsZoneTarget := EntryTarget(SettingsEntryName, tb.EntrySettings, Require("zones[1].id", int64(77)))
	addSQLRefPathCases(&tests, "settings_zones", settingsZoneBase, settingsZoneTarget, settingsZoneColumnPaths, func(path string) string {
		return fmt.Sprintf("zones[%d].%s", settingsZoneBase.ZoneIndex, path)
	})
	addSettingsValueCases(&tests)
	tests = append(tests, sqlRefResolveCase{
		name: "settings_changer_targets.number_value",
		ref: SQLValueRef{
			Table:           "settings_changer_targets",
			Column:          "number_value",
			EntryName:       SettingsEntryName,
			ChangerIndex:    2,
			HasChangerIndex: true,
			TargetKey:       "Zone(1).fertility",
			Scope:           "zone",
			ZoneIndex:       1,
			HasZoneIndex:    true,
			ZoneID:          77,
			HasZoneID:       true,
			SettingName:     "fertility",
			ValueType:       string(tb.ScalarNumber),
		},
		wantTarget: settingsZoneTarget,
		wantPath:   `settingsChangers[2].settingsBases["Zone(1).fertility"]`,
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTarget, gotPath, err := ResolveSQLValueRef(tt.ref)
			if err != nil {
				t.Fatalf("ResolveSQLValueRef() error = %v", err)
			}
			if gotPath != tt.wantPath {
				t.Fatalf("path = %q, want %q", gotPath, tt.wantPath)
			}
			if !reflect.DeepEqual(gotTarget, tt.wantTarget) {
				t.Fatalf("target = %#v, want %#v", gotTarget, tt.wantTarget)
			}

			op, err := SQLSet(tt.ref.WithExpected(sqlRefTestValue(tt.ref)), sqlRefTestValue(tt.ref))
			if err != nil {
				t.Fatalf("SQLSet() error = %v", err)
			}
			if err := validateOperationShape(op); err != nil {
				t.Fatalf("validateOperationShape(SQLSet()) error = %v", err)
			}
		})
	}
}

func TestWritableSQLRefCatalogMatchesNormalizedSchema(t *testing.T) {
	columnsByTable := make(map[string]map[string]struct{}, len(tb.NormalizedTables))
	for _, table := range tb.NormalizedTables {
		columns := make(map[string]struct{}, len(table.Fields))
		for _, field := range table.Fields {
			columns[field.Column] = struct{}{}
		}
		columnsByTable[table.Table] = columns
	}

	seenTables := map[string]struct{}{}
	seenRefs := map[string]struct{}{}
	for _, spec := range writableSQLRefTables {
		if spec.table == "" {
			t.Fatal("writable SQL ref catalog has empty table name")
		}
		if _, ok := seenTables[spec.table]; ok {
			t.Fatalf("writable SQL ref catalog has duplicate table %q", spec.table)
		}
		seenTables[spec.table] = struct{}{}

		schemaColumns, ok := columnsByTable[spec.table]
		if !ok {
			t.Fatalf("writable SQL ref catalog references unknown normalized table %q", spec.table)
		}
		if len(spec.columns) == 0 {
			t.Fatalf("writable SQL ref catalog table %q has no columns", spec.table)
		}
		for column := range spec.columns {
			if column == "" {
				t.Fatalf("writable SQL ref catalog table %q has empty column name", spec.table)
			}
			key := spec.table + "." + column
			if _, ok := seenRefs[key]; ok {
				t.Fatalf("writable SQL ref catalog has duplicate ref %s", key)
			}
			seenRefs[key] = struct{}{}
			if _, ok := schemaColumns[column]; !ok {
				t.Fatalf("writable SQL ref catalog references missing normalized column %s", key)
			}
		}
	}
}

func TestSQLSetLiveFixtureResolverPathsCommitAndReparse(t *testing.T) {
	covered := make(map[string]struct{})
	outDir := t.TempDir()
	for _, fixture := range liveSQLRefFixtureCandidates(t) {
		if len(fixture.cases) == 0 {
			continue
		}
		for _, tc := range fixture.cases {
			covered[tc.ref.Table+"."+tc.ref.Column] = struct{}{}
		}

		outPath := filepath.Join(outDir, strings.TrimSuffix(filepath.Base(fixture.path), ".zip")+"_sqlref_matrix.zip")
		after := commitLiveSQLRefFixture(t, fixture, outPath)
		assertLiveSQLRefCasesMutated(t, filepath.Base(fixture.path), fixture.cases, after)
	}
	assertCoversWritableSQLRefs(t, covered)
}

func TestSmokeLiveSQLRefMatrixInstallsAllObservedFields(t *testing.T) {
	outDir := filepath.Join(os.TempDir(), "bibicontrol-smoke")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", outDir, err)
	}

	selected := selectLiveSQLRefSmokeFixtures(t, liveSQLRefFixtureCandidates(t))
	covered := make(map[string]struct{})
	for _, fixture := range selected {
		for _, tc := range fixture.cases {
			covered[tc.ref.Table+"."+tc.ref.Column] = struct{}{}
		}

		dstName := liveSQLRefSmokeSaveName(fixture.path)
		outPath := filepath.Join(outDir, dstName)
		after := commitLiveSQLRefFixture(t, fixture, outPath)
		assertLiveSQLRefCasesMutated(t, filepath.Base(fixture.path), fixture.cases, after)
		t.Logf("wrote all-observed SQL ref smoke save: %s", outPath)

		installedPath := installLiveSQLRefSmokeSave(t, outPath, dstName)
		t.Logf("installed all-observed SQL ref smoke save: %s", installedPath)
	}
	assertCoversWritableSQLRefs(t, covered)
}

func TestLiveSQLRefEnumBackedStringsUseKnownAlternates(t *testing.T) {
	if got := nextLiveSQLRefValue(t, tb.ExtractedSave{}, "settings_zones", "distribution", "CentricGradual"); got != "Flat" {
		t.Fatalf("settings_zones.distribution next value = %v, want Flat", got)
	}

	row := reflect.ValueOf(tb.SettingValueRow{SettingName: "movement"})
	if got := nextLiveSQLRefCaseValue(t, tb.ExtractedSave{}, "settings_zone_values", "string_value", "None", row); got != "Free" {
		t.Fatalf("settings_zone_values.string_value movement next value = %v, want Free", got)
	}
}

func TestLiveSQLRefRuntimeMaterialsUseObservedAlternates(t *testing.T) {
	tables := tb.ExtractedSave{
		SettingsMaterials: []tb.SettingsMaterialRow{
			{MaterialName: "ArmorSettings"},
			{MaterialName: "PlantSettings"},
		},
		Pellets: []tb.PelletRow{
			{Material: "Plant"},
			{Material: "Meat"},
		},
		BibiteStomachContents: []tb.StomachContentRow{
			{Material: "Plant"},
			{Material: "Meat"},
		},
	}

	if got := nextLiveSQLRefValue(t, tables, "pellets", "material", "Plant"); got != "Meat" {
		t.Fatalf("pellets.material next value = %v, want Meat", got)
	}
	if got := nextLiveSQLRefValue(t, tables, "bibite_stomach_contents", "material", "Plant"); got != "Meat" {
		t.Fatalf("bibite_stomach_contents.material next value = %v, want Meat", got)
	}
	if got := nextLiveSQLRefValue(t, tables, "settings_zones", "material", "Plant"); got != "ArmorSettings" {
		t.Fatalf("settings_zones.material next value = %v, want ArmorSettings", got)
	}
}

func TestSQLSetRejectsUnsupportedRefs(t *testing.T) {
	_, err := SQLSet(SQLValueRef{
		Table:  "settings_zone_geometry",
		Column: "position_x",
	}, 1.0)
	if err == nil {
		t.Fatalf("SQLSet(settings_zone_geometry.position_x) error = nil, want unsupported")
	}
	if !strings.Contains(err.Error(), "not writable") {
		t.Fatalf("SQLSet() error = %v, want not writable", err)
	}
}

func TestSQLSetRequiresPrecisePelletLocator(t *testing.T) {
	_, err := SQLSet(SQLValueRef{
		Table:         "pellets",
		Column:        "amount",
		EntryName:     PelletsEntryName,
		GroupIndex:    0,
		HasGroupIndex: true,
	}, 1.0)
	if err == nil {
		t.Fatalf("SQLSet(pellets without group_pellet_index) error = nil, want error")
	}
	if !strings.Contains(err.Error(), "group_pellet_index") {
		t.Fatalf("SQLSet() error = %v, want group_pellet_index", err)
	}
}

func TestSQLSetRejectsUnsafeSettingsValueRefs(t *testing.T) {
	valid := SQLValueRef{
		Table:          "settings_independent_values",
		Column:         "number_value",
		EntryName:      SettingsEntryName,
		OwnerKind:      "settings_independent",
		OwnerID:        "independents",
		SettingName:    "worldSize",
		Path:           "settings.independents.worldSize",
		ValueType:      "number",
		WrapperRawJSON: `{"Value":1000}`,
	}

	tests := []struct {
		name string
		ref  SQLValueRef
		want string
	}{
		{
			name: "missing wrapper raw",
			ref: func() SQLValueRef {
				ref := valid
				ref.WrapperRawJSON = ""
				return ref
			}(),
			want: "wrapper_raw_json",
		},
		{
			name: "wrong value type for column",
			ref: func() SQLValueRef {
				ref := valid
				ref.Column = "string_value"
				return ref
			}(),
			want: "value_type",
		},
		{
			name: "path owner mismatch",
			ref: func() SQLValueRef {
				ref := valid
				ref.Path = "settings.pelletEnergy"
				return ref
			}(),
			want: "path",
		},
		{
			name: "unsafe setting name",
			ref: func() SQLValueRef {
				ref := valid
				ref.SettingName = "world.Size"
				ref.Path = "settings.independents.world.Size"
				return ref
			}(),
			want: "safe path segment",
		},
		{
			name: "wrapper object without Value",
			ref: func() SQLValueRef {
				ref := valid
				ref.WrapperRawJSON = `{"Other":1000}`
				return ref
			}(),
			want: "Value",
		},
		{
			name: "zone index mismatch",
			ref: SQLValueRef{
				Table:          "settings_zone_values",
				Column:         "number_value",
				EntryName:      SettingsEntryName,
				OwnerKind:      "settings_zone",
				OwnerID:        "7",
				SettingName:    "fertility",
				Path:           "settings.zones[0].fertility",
				ValueType:      "number",
				WrapperRawJSON: `{"Value":0.4}`,
				ZoneIndex:      1,
				HasZoneIndex:   true,
			},
			want: "zone_index",
		},
		{
			name: "changer target key mismatch",
			ref: SQLValueRef{
				Table:           "settings_changer_targets",
				Column:          "number_value",
				EntryName:       SettingsEntryName,
				ChangerIndex:    0,
				HasChangerIndex: true,
				TargetKey:       "Zone(1).fertility",
				Scope:           "zone",
				ZoneIndex:       0,
				HasZoneIndex:    true,
				SettingName:     "fertility",
				ValueType:       "number",
			},
			want: "target_key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := SQLSet(tt.ref, 1.0)
			if err == nil {
				t.Fatalf("SQLSet() error = nil, want %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("SQLSet() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestSQLSetExpectedGuardMismatchDoesNotChangeRaw(t *testing.T) {
	archive := parseSyntheticArchive(t)
	bibite := archive.Entry("bibites/bibite_0.bb8")
	originalRaw := append([]byte(nil), bibite.Raw...)

	session := NewSession(archive)
	if err := session.StageSQLSet(SQLValueRef{
		Table:     "bibites",
		Column:    "energy",
		EntryName: "bibites/bibite_0.bb8",
		BodyID:    42,
		HasBodyID: true,
	}.WithExpected(99.0), 33.25); err != nil {
		t.Fatalf("StageSQLSet() error = %v", err)
	}
	if err := session.Apply(); err == nil {
		t.Fatalf("Apply() error = nil, want expected guard mismatch")
	}
	if !bytes.Equal(bibite.Raw, originalRaw) {
		t.Fatalf("failed SQL apply changed raw bytes")
	}
}

type sqlRefResolveCase struct {
	name       string
	ref        SQLValueRef
	wantTarget Target
	wantPath   string
}

type liveSQLRefMutationCase struct {
	name    string
	ref     SQLValueRef
	current any
	next    any
	lookup  func(tb.ExtractedSave) (any, bool)
}

type liveSQLRefFixtureCandidate struct {
	path     string
	archive  *tb.Archive
	cases    []liveSQLRefMutationCase
	coverage map[string]struct{}
}

func addSQLRefPathCases(tests *[]sqlRefResolveCase, table string, base SQLValueRef, target Target, paths map[string]string, renderPath func(string) string) {
	for _, column := range sortedSQLRefKeys(paths) {
		ref := base
		ref.Table = table
		ref.Column = column
		*tests = append(*tests, sqlRefResolveCase{
			name:       table + "." + column,
			ref:        ref,
			wantTarget: target,
			wantPath:   renderPath(paths[column]),
		})
	}
}

func addSQLRefValueColumnCases(tests *[]sqlRefResolveCase, table string, base SQLValueRef, target Target, wantPath string) {
	for _, column := range sortedSQLRefKeys(settingsValueColumnTypes) {
		ref := base
		ref.Table = table
		ref.Column = column
		*tests = append(*tests, sqlRefResolveCase{
			name:       table + "." + column,
			ref:        ref,
			wantTarget: target,
			wantPath:   wantPath,
		})
	}
}

func addSettingsValueCases(tests *[]sqlRefResolveCase) {
	specs := []struct {
		table      string
		ownerKind  string
		ownerID    string
		pathPrefix string
		wantPrefix string
		target     Target
		zone       bool
	}{
		{
			table:      "settings_simulation_values",
			ownerKind:  "settings",
			ownerID:    "settings",
			pathPrefix: "settings.",
			target:     SettingsTarget(),
		},
		{
			table:      "settings_independent_values",
			ownerKind:  "settings_independent",
			ownerID:    "independents",
			pathPrefix: "settings.independents.",
			wantPrefix: "independents.",
			target:     SettingsTarget(),
		},
		{
			table:      "settings_material_values",
			ownerKind:  "settings_material",
			ownerID:    "Plant",
			pathPrefix: "settings.materials.Plant.",
			wantPrefix: "materials.Plant.",
			target:     SettingsTarget(),
		},
		{
			table:      "settings_zone_values",
			ownerKind:  "settings_zone",
			ownerID:    "77",
			pathPrefix: "settings.zones[1].",
			wantPrefix: "zones[1].",
			target:     SettingsTarget(Require("zones[1].id", int64(77))),
			zone:       true,
		},
	}

	for _, spec := range specs {
		for _, column := range sortedSQLRefKeys(settingsValueColumnTypes) {
			valueType := settingsValueColumnTypes[column]
			settingName := valueType + "Setting"
			ref := SQLValueRef{
				Table:          spec.table,
				Column:         column,
				EntryName:      SettingsEntryName,
				OwnerKind:      spec.ownerKind,
				OwnerID:        spec.ownerID,
				SettingName:    settingName,
				Path:           spec.pathPrefix + settingName,
				ValueType:      valueType,
				WrapperRawJSON: sqlRefWrappedValue(valueType),
			}
			if spec.zone {
				ref.ZoneIndex = 1
				ref.HasZoneIndex = true
				ref.ZoneID = 77
				ref.HasZoneID = true
			}
			*tests = append(*tests, sqlRefResolveCase{
				name:       spec.table + "." + column,
				ref:        ref,
				wantTarget: spec.target,
				wantPath:   spec.wantPrefix + settingName + ".Value",
			})
		}
	}
}

func parseLiveSQLRefFixture(t *testing.T, path string) *tb.Archive {
	t.Helper()

	archive, err := tb.ParseFile(path, nil)
	if err != nil {
		t.Fatalf("ParseFile(%s) error = %v", path, err)
	}
	assertNoParseErrors(t, archive)
	return archive
}

func liveSQLRefFixturePaths(t *testing.T) []string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller(0) failed")
	}
	pattern := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(file))), "testdata", "saves", "the-bibites", "*.zip")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("Glob(%q) error = %v", pattern, err)
	}
	if len(paths) == 0 {
		t.Fatalf("Glob(%q) returned no live save fixtures", pattern)
	}
	sort.Strings(paths)
	return paths
}

func liveSQLRefMutationCases(t *testing.T, tables tb.ExtractedSave) []liveSQLRefMutationCase {
	t.Helper()

	var cases []liveSQLRefMutationCase
	for _, table := range []struct {
		name    string
		rows    any
		columns map[string]string
	}{
		{name: "bibites", rows: tables.Bibites, columns: bibiteColumnPaths},
		{name: "bibite_body", rows: tables.BibiteBody, columns: bibiteBodyColumnPaths},
		{name: "bibite_mouth", rows: tables.BibiteMouth, columns: bibiteMouthColumnPaths},
		{name: "bibite_pheromone_emitters", rows: tables.BibitePheromoneEmitters, columns: bibitePheromoneColumnPaths},
		{name: "bibite_egg_layers", rows: tables.BibiteEggLayers, columns: bibiteEggLayerColumnPaths},
		{name: "bibite_control", rows: tables.BibiteControl, columns: bibiteControlColumnPaths},
	} {
		addLiveBodyIDTableCases(t, &cases, tables, table.name, table.rows, table.columns)
	}
	addLiveStomachCases(t, &cases, tables)
	addLiveGeneCases(t, &cases, tables, "bibite_genes", tables.BibiteGenes)
	addLiveBrainNodeCases(t, &cases, tables, "bibite_brain_nodes", tables.BibiteBrainNodes)
	addLiveBrainSynapseCases(t, &cases, tables, "bibite_brain_synapses", tables.BibiteBrainSynapses)
	addLiveEggCases(t, &cases, tables)
	addLiveGeneCases(t, &cases, tables, "egg_genes", tables.EggGenes)
	addLiveBrainNodeCases(t, &cases, tables, "egg_brain_nodes", tables.EggBrainNodes)
	addLiveBrainSynapseCases(t, &cases, tables, "egg_brain_synapses", tables.EggBrainSynapses)
	addLivePelletCases(t, &cases, tables)
	addLivePheromoneCases(t, &cases, tables)
	addLiveSettingsZoneCases(t, &cases, tables)
	addLiveSettingsValueCases(t, &cases, tables, "settings_simulation_values", tables.SettingsSimulationValues)
	addLiveSettingsValueCases(t, &cases, tables, "settings_independent_values", tables.SettingsIndependentValues)
	addLiveSettingsValueCases(t, &cases, tables, "settings_material_values", tables.SettingsMaterialValues)
	addLiveSettingsValueCases(t, &cases, tables, "settings_zone_values", tables.SettingsZoneValues)
	addLiveSettingsChangerTargetCases(t, &cases, tables)
	return cases
}

func liveSQLRefFixtureCandidates(t *testing.T) []liveSQLRefFixtureCandidate {
	t.Helper()

	var fixtures []liveSQLRefFixtureCandidate
	for _, fixturePath := range liveSQLRefFixturePaths(t) {
		archive := parseLiveSQLRefFixture(t, fixturePath)
		before := tb.ExtractTables("before", archive)
		cases := liveSQLRefMutationCases(t, before)
		fixtures = append(fixtures, liveSQLRefFixtureCandidate{
			path:     fixturePath,
			archive:  archive,
			cases:    cases,
			coverage: liveSQLRefCaseCoverage(cases),
		})
	}
	return fixtures
}

func commitLiveSQLRefFixture(t *testing.T, fixture liveSQLRefFixtureCandidate, outPath string) tb.ExtractedSave {
	t.Helper()

	session := NewSession(fixture.archive)
	for _, tc := range fixture.cases {
		ref := tc.ref.WithExpected(tc.current)
		if err := session.StageSQLSet(ref, tc.next); err != nil {
			t.Fatalf("StageSQLSet(%s from %s) error = %v", tc.name, filepath.Base(fixture.path), err)
		}
	}

	fresh, err := session.Commit(outPath)
	if err != nil {
		t.Fatalf("Commit(%s live matrix) error = %v", filepath.Base(fixture.path), err)
	}
	assertNoParseErrors(t, fresh)

	fromDisk, err := tb.ParseFile(outPath, nil)
	if err != nil {
		t.Fatalf("ParseFile(committed %s) error = %v", filepath.Base(fixture.path), err)
	}
	assertNoParseErrors(t, fromDisk)
	return tb.ExtractTables("after", fromDisk)
}

func assertLiveSQLRefCasesMutated(t *testing.T, fixtureName string, cases []liveSQLRefMutationCase, after tb.ExtractedSave) {
	t.Helper()

	for _, tc := range cases {
		t.Run(fixtureName+"/"+tc.name, func(t *testing.T) {
			got, ok := tc.lookup(after)
			if !ok {
				t.Fatalf("mutated normalized row was not found")
			}
			if !sqlRefValuesEqual(got, tc.next) {
				t.Fatalf("normalized value = %#v (%T), want %#v (%T)", got, got, tc.next, tc.next)
			}
		})
	}
}

func selectLiveSQLRefSmokeFixtures(t *testing.T, fixtures []liveSQLRefFixtureCandidate) []liveSQLRefFixtureCandidate {
	t.Helper()

	remaining := observedWritableSQLRefSet()
	selected := make([]liveSQLRefFixtureCandidate, 0, len(fixtures))
	used := make(map[int]struct{})

	const preferredFixture = "autosave_20260301021357.zip"
	for i, fixture := range fixtures {
		if filepath.Base(fixture.path) != preferredFixture || len(fixture.cases) == 0 {
			continue
		}
		selected = append(selected, fixture)
		used[i] = struct{}{}
		removeCoveredSQLRefs(remaining, fixture.coverage)
		break
	}

	for len(remaining) > 0 {
		bestIndex := -1
		bestGain := 0
		for i, fixture := range fixtures {
			if _, ok := used[i]; ok || len(fixture.cases) == 0 {
				continue
			}
			gain := countCoveredSQLRefs(remaining, fixture.coverage)
			if gain > bestGain {
				bestIndex = i
				bestGain = gain
			}
		}
		if bestIndex == -1 {
			t.Fatalf("live SQL ref smoke fixtures do not cover observed writable refs: %s", strings.Join(sortedSQLRefSet(remaining), ", "))
		}
		selected = append(selected, fixtures[bestIndex])
		used[bestIndex] = struct{}{}
		removeCoveredSQLRefs(remaining, fixtures[bestIndex].coverage)
	}

	return selected
}

func liveSQLRefSmokeSaveName(fixturePath string) string {
	base := strings.TrimSuffix(filepath.Base(fixturePath), ".zip")
	return "all-observed-sqlref-" + base + ".zip"
}

func installLiveSQLRefSmokeSave(t *testing.T, outPath, dstName string) string {
	t.Helper()

	if strings.TrimSpace(os.Getenv(BibitesSavefilesDirEnv)) != "" {
		installedPath, err := InstallSaveFile(outPath, dstName)
		if err != nil {
			t.Fatalf("InstallSaveFile() error = %v", err)
		}
		return installedPath
	}

	dstDir := filepath.Join(os.TempDir(), "bibicontrol-savefiles")
	installedPath, err := InstallSaveFileToDir(outPath, dstDir, dstName)
	if err != nil {
		t.Fatalf("InstallSaveFileToDir(%q) error = %v", dstDir, err)
	}
	t.Logf("%s unset; installed smoke save to temp Savefiles dir %s", BibitesSavefilesDirEnv, dstDir)
	return installedPath
}

func liveSQLRefCaseCoverage(cases []liveSQLRefMutationCase) map[string]struct{} {
	coverage := make(map[string]struct{}, len(cases))
	for _, tc := range cases {
		coverage[tc.ref.Table+"."+tc.ref.Column] = struct{}{}
	}
	return coverage
}

func observedWritableSQLRefSet() map[string]struct{} {
	unobserved := schemaCapableUnobservedSQLRefs()
	observed := make(map[string]struct{})
	for _, key := range writableSQLRefKeys() {
		if _, ok := unobserved[key]; !ok {
			observed[key] = struct{}{}
		}
	}
	return observed
}

func removeCoveredSQLRefs(remaining, coverage map[string]struct{}) {
	for key := range coverage {
		delete(remaining, key)
	}
}

func countCoveredSQLRefs(remaining, coverage map[string]struct{}) int {
	count := 0
	for key := range coverage {
		if _, ok := remaining[key]; ok {
			count++
		}
	}
	return count
}

func addLiveBodyIDTableCases(t *testing.T, cases *[]liveSQLRefMutationCase, tables tb.ExtractedSave, table string, rows any, columns map[string]string) {
	t.Helper()

	row, ok := firstNormalizedRow(rows, func(row reflect.Value) bool {
		return rowBoolField(row, "HasBodyID")
	})
	if !ok {
		return
	}
	base := SQLValueRef{
		EntryName: rowStringField(row, "EntryName"),
		BodyID:    rowInt64Field(row, "BodyID"),
		HasBodyID: true,
	}
	matcher := liveRowMatcher{
		"EntryName": base.EntryName,
		"BodyID":    base.BodyID,
		"HasBodyID": true,
	}
	addLiveColumnCases(t, cases, tables, table, base, row, matcher, columns)
}

func addLiveStomachCases(t *testing.T, cases *[]liveSQLRefMutationCase, tables tb.ExtractedSave) {
	t.Helper()

	row, ok := firstNormalizedRow(tables.BibiteStomachContents, func(row reflect.Value) bool {
		return rowBoolField(row, "HasBodyID")
	})
	if !ok {
		return
	}
	base := SQLValueRef{
		EntryName:       rowStringField(row, "EntryName"),
		BodyID:          rowInt64Field(row, "BodyID"),
		HasBodyID:       true,
		ContentIndex:    rowIntField(row, "ContentIndex"),
		HasContentIndex: true,
	}
	matcher := liveRowMatcher{
		"EntryName":    base.EntryName,
		"BodyID":       base.BodyID,
		"HasBodyID":    true,
		"ContentIndex": base.ContentIndex,
	}
	addLiveColumnCases(t, cases, tables, "bibite_stomach_contents", base, row, matcher, bibiteStomachContentColumnFields)
}

func addLiveGeneCases(t *testing.T, cases *[]liveSQLRefMutationCase, tables tb.ExtractedSave, table string, rows []tb.GeneRow) {
	t.Helper()

	for _, column := range sortedSQLRefKeys(settingsValueColumnTypes) {
		valueType := tb.ScalarType(settingsValueColumnTypes[column])
		row, ok := firstGeneRow(rows, valueType)
		if !ok {
			continue
		}
		ref := SQLValueRef{
			Table:     table,
			Column:    column,
			EntryName: row.EntryName,
			OwnerKind: row.OwnerKind,
			OwnerID:   row.OwnerID,
			Path:      row.Path,
		}
		matcher := liveRowMatcher{
			"EntryName": ref.EntryName,
			"OwnerKind": ref.OwnerKind,
			"OwnerID":   ref.OwnerID,
			"Path":      ref.Path,
			"Type":      valueType,
		}
		addLiveCase(t, cases, tables, table, column, ref, reflect.ValueOf(row), matcher)
	}
}

func firstGeneRow(rows []tb.GeneRow, valueType tb.ScalarType) (tb.GeneRow, bool) {
	for _, row := range rows {
		if row.Type == valueType && strings.Contains(row.Path, ".genes.") {
			return row, true
		}
	}
	for _, row := range rows {
		if row.Type == valueType {
			return row, true
		}
	}
	return tb.GeneRow{}, false
}

func addLiveBrainNodeCases(t *testing.T, cases *[]liveSQLRefMutationCase, tables tb.ExtractedSave, table string, rows []tb.BrainNodeRow) {
	t.Helper()

	if len(rows) == 0 {
		return
	}
	row := rows[0]
	ref := SQLValueRef{
		Table:           table,
		EntryName:       row.EntryName,
		OwnerKind:       row.OwnerKind,
		OwnerID:         row.OwnerID,
		NodeRowIndex:    row.NodeRowIndex,
		HasNodeRowIndex: true,
	}
	setEntitySQLRefID(t, &ref)
	matcher := liveRowMatcher{
		"EntryName":    ref.EntryName,
		"OwnerKind":    ref.OwnerKind,
		"OwnerID":      ref.OwnerID,
		"NodeRowIndex": ref.NodeRowIndex,
	}
	addLiveColumnCases(t, cases, tables, table, ref, reflect.ValueOf(row), matcher, brainNodeColumnKeys)
}

func addLiveBrainSynapseCases(t *testing.T, cases *[]liveSQLRefMutationCase, tables tb.ExtractedSave, table string, rows []tb.BrainSynapseRow) {
	t.Helper()

	if len(rows) == 0 {
		return
	}
	row := rows[0]
	ref := SQLValueRef{
		Table:              table,
		EntryName:          row.EntryName,
		OwnerKind:          row.OwnerKind,
		OwnerID:            row.OwnerID,
		SynapseRowIndex:    row.SynapseRowIndex,
		HasSynapseRowIndex: true,
	}
	setEntitySQLRefID(t, &ref)
	matcher := liveRowMatcher{
		"EntryName":       ref.EntryName,
		"OwnerKind":       ref.OwnerKind,
		"OwnerID":         ref.OwnerID,
		"SynapseRowIndex": ref.SynapseRowIndex,
	}
	addLiveColumnCases(t, cases, tables, table, ref, reflect.ValueOf(row), matcher, brainSynapseColumnKeys)
}

func setEntitySQLRefID(t *testing.T, ref *SQLValueRef) {
	t.Helper()

	id, err := strconv.ParseInt(ref.OwnerID, 10, 64)
	if err != nil {
		t.Fatalf("%s owner_id %q is not numeric: %v", ref.Table, ref.OwnerID, err)
	}
	switch ref.OwnerKind {
	case "bibite":
		ref.BodyID = id
		ref.HasBodyID = true
	case "egg":
		ref.EggID = id
		ref.HasEggID = true
	default:
		t.Fatalf("%s owner_kind = %q, want bibite or egg", ref.Table, ref.OwnerKind)
	}
}

func addLiveEggCases(t *testing.T, cases *[]liveSQLRefMutationCase, tables tb.ExtractedSave) {
	t.Helper()

	row, ok := firstNormalizedRow(tables.Eggs, func(row reflect.Value) bool {
		return rowBoolField(row, "HasEggID")
	})
	if !ok {
		return
	}
	base := SQLValueRef{
		EntryName: rowStringField(row, "EntryName"),
		EggID:     rowInt64Field(row, "EggID"),
		HasEggID:  true,
	}
	matcher := liveRowMatcher{
		"EntryName": base.EntryName,
		"EggID":     base.EggID,
		"HasEggID":  true,
	}
	addLiveColumnCases(t, cases, tables, "eggs", base, row, matcher, eggColumnPaths)
}

func addLivePelletCases(t *testing.T, cases *[]liveSQLRefMutationCase, tables tb.ExtractedSave) {
	t.Helper()

	for _, column := range sortedSQLRefKeys(pelletColumnPaths) {
		row, ok := firstNormalizedRow(tables.Pellets, func(row reflect.Value) bool {
			if strings.HasPrefix(column, "matter_decay_") {
				return rowBoolField(row, "HasMatterDecay")
			}
			return true
		})
		if !ok {
			continue
		}
		ref := SQLValueRef{
			Table:               "pellets",
			Column:              column,
			EntryName:           rowStringField(row, "EntryName"),
			GroupIndex:          rowIntField(row, "GroupIndex"),
			HasGroupIndex:       true,
			GroupPelletIndex:    rowIntField(row, "GroupPelletIndex"),
			HasGroupPelletIndex: true,
			Zone:                rowStringField(row, "Zone"),
			HasZone:             true,
		}
		matcher := liveRowMatcher{
			"EntryName":        ref.EntryName,
			"GroupIndex":       ref.GroupIndex,
			"GroupPelletIndex": ref.GroupPelletIndex,
		}
		addLiveCase(t, cases, tables, "pellets", column, ref, row, matcher)
	}
}

func addLivePheromoneCases(t *testing.T, cases *[]liveSQLRefMutationCase, tables tb.ExtractedSave) {
	t.Helper()

	row, ok := firstNormalizedRow(tables.Pheromones, func(row reflect.Value) bool {
		return true
	})
	if !ok {
		return
	}
	base := SQLValueRef{
		EntryName:         rowStringField(row, "EntryName"),
		PheromoneIndex:    rowIntField(row, "PheromoneIndex"),
		HasPheromoneIndex: true,
	}
	matcher := liveRowMatcher{
		"EntryName":      base.EntryName,
		"PheromoneIndex": base.PheromoneIndex,
	}
	addLiveColumnCases(t, cases, tables, "pheromones", base, row, matcher, pheromoneColumnPaths)
}

func addLiveSettingsZoneCases(t *testing.T, cases *[]liveSQLRefMutationCase, tables tb.ExtractedSave) {
	t.Helper()

	row, ok := firstNormalizedRow(tables.SettingsZones, func(row reflect.Value) bool {
		return true
	})
	if !ok {
		return
	}
	base := SQLValueRef{
		EntryName:    rowStringField(row, "EntryName"),
		ZoneIndex:    rowIntField(row, "ZoneIndex"),
		HasZoneIndex: true,
		ZoneID:       rowInt64Field(row, "ZoneID"),
		HasZoneID:    rowBoolField(row, "HasZoneID"),
	}
	matcher := liveRowMatcher{
		"EntryName": base.EntryName,
		"ZoneIndex": base.ZoneIndex,
	}
	addLiveColumnCases(t, cases, tables, "settings_zones", base, row, matcher, settingsZoneColumnPaths)
}

func addLiveSettingsValueCases(t *testing.T, cases *[]liveSQLRefMutationCase, tables tb.ExtractedSave, table string, rows []tb.SettingValueRow) {
	t.Helper()

	for _, column := range sortedSQLRefKeys(settingsValueColumnTypes) {
		valueType := tb.ScalarType(settingsValueColumnTypes[column])
		row, ok := firstLiveSettingValueRow(table, rows, valueType)
		if !ok {
			continue
		}
		ref := SQLValueRef{
			Table:          table,
			Column:         column,
			EntryName:      row.EntryName,
			OwnerKind:      row.OwnerKind,
			OwnerID:        row.OwnerID,
			SettingName:    row.SettingName,
			Path:           row.Path,
			ValueType:      string(row.Type),
			WrapperRawJSON: row.WrapperRawJSON,
		}
		if table == "settings_zone_values" {
			zoneIndex, err := settingsZoneValuePathIndex(row.Path, row.SettingName)
			if err != nil {
				t.Fatalf("%s path %q: %v", table, row.Path, err)
			}
			ref.ZoneIndex = zoneIndex
			ref.HasZoneIndex = true
			if zone, ok := settingsZoneByIndex(tables.SettingsZones, zoneIndex); ok {
				ref.ZoneID = zone.ZoneID
				ref.HasZoneID = zone.HasZoneID
			}
		}
		matcher := liveRowMatcher{
			"EntryName":   ref.EntryName,
			"OwnerKind":   ref.OwnerKind,
			"OwnerID":     ref.OwnerID,
			"SettingName": ref.SettingName,
			"Path":        ref.Path,
			"Type":        valueType,
		}
		addLiveCase(t, cases, tables, table, column, ref, reflect.ValueOf(row), matcher)
	}
}

func firstLiveSettingValueRow(table string, rows []tb.SettingValueRow, valueType tb.ScalarType) (tb.SettingValueRow, bool) {
	if table == "settings_material_values" && valueType == tb.ScalarBool {
		for _, row := range rows {
			if row.Type == valueType && row.BoolValue {
				return row, true
			}
		}
	}
	return firstSettingValueRow(rows, valueType)
}

func firstSettingValueRow(rows []tb.SettingValueRow, valueType tb.ScalarType) (tb.SettingValueRow, bool) {
	for _, row := range rows {
		if row.Type == valueType {
			return row, true
		}
	}
	return tb.SettingValueRow{}, false
}

func settingsZoneByIndex(rows []tb.SettingsZoneRow, index int) (tb.SettingsZoneRow, bool) {
	for _, row := range rows {
		if row.ZoneIndex == index {
			return row, true
		}
	}
	return tb.SettingsZoneRow{}, false
}

func addLiveSettingsChangerTargetCases(t *testing.T, cases *[]liveSQLRefMutationCase, tables tb.ExtractedSave) {
	t.Helper()

	row, ok := firstNormalizedRow(tables.SettingsChangerTargets, func(row reflect.Value) bool {
		zoneIndex := rowIntField(row, "ZoneIndex")
		settingName := rowStringField(row, "SettingName")
		return rowStringField(row, "Scope") == "zone" &&
			row.Interface().(tb.SettingsChangerTargetRow).Type == tb.ScalarNumber &&
			rowStringField(row, "TargetKey") == fmt.Sprintf("Zone(%d).%s", zoneIndex, settingName)
	})
	if !ok {
		return
	}
	ref := SQLValueRef{
		Table:           "settings_changer_targets",
		Column:          "number_value",
		EntryName:       rowStringField(row, "EntryName"),
		ChangerIndex:    rowIntField(row, "ChangerIndex"),
		HasChangerIndex: true,
		TargetKey:       rowStringField(row, "TargetKey"),
		Scope:           rowStringField(row, "Scope"),
		ZoneIndex:       rowIntField(row, "ZoneIndex"),
		HasZoneIndex:    true,
		ZoneID:          rowInt64Field(row, "ZoneID"),
		HasZoneID:       rowBoolField(row, "HasZoneID"),
		SettingName:     rowStringField(row, "SettingName"),
		ValueType:       string(tb.ScalarNumber),
	}
	matcher := liveRowMatcher{
		"EntryName":    ref.EntryName,
		"ChangerIndex": ref.ChangerIndex,
		"TargetKey":    ref.TargetKey,
		"Scope":        ref.Scope,
	}
	addLiveCase(t, cases, tables, "settings_changer_targets", "number_value", ref, row, matcher)
}

func addLiveColumnCases(t *testing.T, cases *[]liveSQLRefMutationCase, tables tb.ExtractedSave, table string, base SQLValueRef, row reflect.Value, matcher liveRowMatcher, columns map[string]string) {
	t.Helper()

	for _, column := range sortedSQLRefKeys(columns) {
		ref := base
		ref.Table = table
		ref.Column = column
		addLiveCase(t, cases, tables, table, column, ref, row, matcher)
	}
}

func addLiveCase(t *testing.T, cases *[]liveSQLRefMutationCase, tables tb.ExtractedSave, table, column string, ref SQLValueRef, row reflect.Value, matcher liveRowMatcher) {
	t.Helper()

	current := normalizedColumnValue(t, row, table, column)
	next := nextLiveSQLRefCaseValue(t, tables, table, column, current, row)
	*cases = append(*cases, liveSQLRefMutationCase{
		name:    table + "." + column,
		ref:     ref,
		current: current,
		next:    next,
		lookup:  liveColumnLookup(table, column, matcher),
	})
}

func nextLiveSQLRefCaseValue(t *testing.T, tables tb.ExtractedSave, table, column string, current any, row reflect.Value) any {
	t.Helper()

	if table == "settings_zone_values" && column == "string_value" {
		if settingValue, ok := row.Interface().(tb.SettingValueRow); ok && settingValue.SettingName == "movement" {
			value, ok := current.(string)
			if !ok {
				t.Fatalf("%s.%s movement current value %T is not a string", table, column, current)
			}
			return alternateLiveMovement(value)
		}
	}
	return nextLiveSQLRefValue(t, tables, table, column, current)
}

type liveRowMatcher map[string]any

func liveColumnLookup(table, column string, matcher liveRowMatcher) func(tb.ExtractedSave) (any, bool) {
	return func(tables tb.ExtractedSave) (any, bool) {
		rows := extractedTableRows(tables, table)
		for i := 0; i < rows.Len(); i++ {
			row := rows.Index(i)
			if row.Kind() == reflect.Pointer {
				if row.IsNil() {
					continue
				}
				row = row.Elem()
			}
			if liveRowMatches(row, matcher) {
				return normalizedColumnValue(nil, row, table, column), true
			}
		}
		return nil, false
	}
}

func liveRowMatches(row reflect.Value, matcher liveRowMatcher) bool {
	for field, want := range matcher {
		got := row.FieldByName(field).Interface()
		if !sqlRefValuesEqual(got, want) {
			return false
		}
	}
	return true
}

func firstNormalizedRow(rows any, match func(reflect.Value) bool) (reflect.Value, bool) {
	value := reflect.ValueOf(rows)
	for i := 0; i < value.Len(); i++ {
		row := value.Index(i)
		if row.Kind() == reflect.Pointer {
			if row.IsNil() {
				continue
			}
			row = row.Elem()
		}
		if match(row) {
			return row, true
		}
	}
	return reflect.Value{}, false
}

func extractedTableRows(tables tb.ExtractedSave, tableName string) reflect.Value {
	save := reflect.ValueOf(tables)
	for _, table := range tb.NormalizedTables {
		if table.Table == tableName {
			rows := save.FieldByName(table.SaveField)
			if rows.Kind() == reflect.Pointer {
				if rows.IsNil() {
					return reflect.Zero(reflect.SliceOf(rows.Type().Elem()))
				}
				slice := reflect.MakeSlice(reflect.SliceOf(rows.Type().Elem()), 1, 1)
				slice.Index(0).Set(rows.Elem())
				return slice
			}
			return rows
		}
	}
	return reflect.Value{}
}

func normalizedColumnValue(t *testing.T, row reflect.Value, tableName, column string) any {
	if row.Kind() == reflect.Pointer {
		row = row.Elem()
	}
	fieldName := normalizedFieldForColumn(t, tableName, column)
	return row.FieldByName(fieldName).Interface()
}

func normalizedFieldForColumn(t *testing.T, tableName, column string) string {
	if t != nil {
		t.Helper()
	}
	for _, table := range tb.NormalizedTables {
		if table.Table != tableName {
			continue
		}
		for _, field := range table.Fields {
			if field.Column == column {
				return field.Field
			}
		}
	}
	if t != nil {
		t.Fatalf("missing normalized field for %s.%s", tableName, column)
	}
	panic(fmt.Sprintf("missing normalized field for %s.%s", tableName, column))
}

func nextLiveSQLRefValue(t *testing.T, tables tb.ExtractedSave, table, column string, current any) any {
	t.Helper()

	switch value := current.(type) {
	case bool:
		return !value
	case string:
		switch column {
		case "material":
			return alternateLiveMaterial(tables, table, value)
		case "distribution":
			return alternateLiveDistribution(tables, value)
		default:
			if value == "" {
				return "sql_ref_live_mutated"
			}
			return value + "_sql"
		}
	case int:
		return value + 1
	case int64:
		return value + 1
	case float64:
		next := value + 1.25
		if strings.HasSuffix(column, "scale") && next == 0 {
			return 1.25
		}
		return next
	default:
		t.Fatalf("%s.%s current value %T is not supported by live SQL ref test", table, column, current)
		return nil
	}
}

func alternateLiveMaterial(tables tb.ExtractedSave, table, current string) string {
	if table == "pellets" || table == "bibite_stomach_contents" {
		if value, ok := alternateObservedLiveMatterMaterial(tables, current); ok {
			return value
		}
	}
	for _, material := range tables.SettingsMaterials {
		if material.MaterialName != "" && material.MaterialName != current {
			return material.MaterialName
		}
	}
	if current != "Plant" {
		return "Plant"
	}
	return current + "_sql"
}

func alternateObservedLiveMatterMaterial(tables tb.ExtractedSave, current string) (string, bool) {
	for _, pellet := range tables.Pellets {
		if pellet.Material != "" && pellet.Material != current {
			return pellet.Material, true
		}
	}
	for _, content := range tables.BibiteStomachContents {
		if content.Material != "" && content.Material != current {
			return content.Material, true
		}
	}
	return "", false
}

func alternateLiveDistribution(tables tb.ExtractedSave, current string) string {
	for _, zone := range tables.SettingsZones {
		if zone.Distribution != "" && zone.Distribution != current {
			return zone.Distribution
		}
	}
	return alternateKnownLiveValue(current, liveSpawnDistributionValues)
}

var liveSpawnDistributionValues = []string{
	"Flat",
	"CentricGradual",
	"ExteriorGradual",
	"Ring",
	"FlatRing",
	"Rect",
}

var liveMovementValues = []string{
	"None",
	"Free",
	"Repulsed",
	"Orbit",
	"Attached",
}

func alternateLiveMovement(current string) string {
	return alternateKnownLiveValue(current, liveMovementValues)
}

func alternateKnownLiveValue(current string, values []string) string {
	for _, value := range values {
		if value != "" && value != current {
			return value
		}
	}
	return current
}

func rowStringField(row reflect.Value, field string) string {
	return row.FieldByName(field).String()
}

func rowBoolField(row reflect.Value, field string) bool {
	return row.FieldByName(field).Bool()
}

func rowIntField(row reflect.Value, field string) int {
	return int(row.FieldByName(field).Int())
}

func rowInt64Field(row reflect.Value, field string) int64 {
	return row.FieldByName(field).Int()
}

func assertCoversWritableSQLRefs(t *testing.T, got map[string]struct{}) {
	t.Helper()

	knownUnobserved := schemaCapableUnobservedSQLRefs()
	var missing []string
	for _, key := range writableSQLRefKeys() {
		if _, ok := got[key]; !ok {
			if _, expectedGap := knownUnobserved[key]; expectedGap {
				continue
			}
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("live SQL ref fixture does not cover writable refs: %s", strings.Join(missing, ", "))
	}
}

func schemaCapableUnobservedSQLRefs() map[string]struct{} {
	return map[string]struct{}{
		"bibite_genes.string_value":                {},
		"egg_genes.string_value":                   {},
		"settings_simulation_values.string_value":  {},
		"settings_independent_values.string_value": {},
		"settings_material_values.string_value":    {},
		"settings_zone_values.bool_value":          {},
	}
}

func assertNoParseErrors(t *testing.T, archive *tb.Archive) {
	t.Helper()

	for _, diagnostic := range archive.Diagnostics {
		if diagnostic.Severity == tb.SeverityError {
			t.Fatalf("parse diagnostic error in %s: %s", diagnostic.Entry, diagnostic.Message)
		}
	}
}

func sqlRefValuesEqual(a, b any) bool {
	if af, ok := sqlRefFloat(a); ok {
		if bf, ok := sqlRefFloat(b); ok {
			return af == bf
		}
	}
	return reflect.DeepEqual(a, b)
}

func sqlRefFloat(value any) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case float64:
		return v, true
	default:
		return 0, false
	}
}

func sortedSQLRefKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedSQLRefSet(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sqlRefPath(path string) string {
	return path
}

func sqlRefWrappedValue(valueType string) string {
	switch valueType {
	case string(tb.ScalarBool):
		return `{"Value":true}`
	case string(tb.ScalarString):
		return `{"Value":"value"}`
	default:
		return `{"Value":1}`
	}
}

func sqlRefTestValue(ref SQLValueRef) any {
	switch ref.Column {
	case "dead", "dying", "attacked_last_frame", "enabled", "bool_value":
		return true
	case "material", "type_name", "node_desc", "name", "distribution", "string_value":
		return "value"
	default:
		return 1.25
	}
}
