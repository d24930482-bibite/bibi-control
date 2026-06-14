package thebibites

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

func TestStageSQLSetStagesResolvesAndCommits(t *testing.T) {
	session := NewSession(parseSyntheticArchive(t))

	if err := session.StageSQLSet(SQLValueRef{
		Table:     "bibites",
		Column:    "energy",
		EntryName: "bibites/bibite_0.bb8",
		BodyID:    42,
		HasBodyID: true,
	}.WithExpected(12.5), 33.25); err != nil {
		t.Fatalf("StageSQLSet(bibites.energy) error = %v", err)
	}
	if err := session.StageSQLSet(SQLValueRef{
		Table:     "bibite_genes",
		Column:    "number_value",
		EntryName: "bibites/bibite_0.bb8",
		OwnerKind: "bibite",
		OwnerID:   "42",
		Path:      "genes.genes.Diet",
	}.WithExpected(0.1), 0.55); err != nil {
		t.Fatalf("StageSQLSet(bibite_genes.number_value) error = %v", err)
	}
	if err := session.StageSQLSet(SQLValueRef{
		Table:           "bibite_stomach_contents",
		Column:          "amount",
		EntryName:       "bibites/bibite_0.bb8",
		BodyID:          42,
		HasBodyID:       true,
		ContentIndex:    0,
		HasContentIndex: true,
	}.WithExpected(2.5), 8.75); err != nil {
		t.Fatalf("StageSQLSet(bibite_stomach_contents.amount) error = %v", err)
	}
	if err := session.StageSQLSet(SQLValueRef{
		Table:               "pellets",
		Column:              "amount",
		EntryName:           PelletsEntryName,
		GroupIndex:          0,
		HasGroupIndex:       true,
		GroupPelletIndex:    0,
		HasGroupPelletIndex: true,
		Zone:                "Zone A",
		HasZone:             true,
	}.WithExpected(5.0), 9.5); err != nil {
		t.Fatalf("StageSQLSet(pellets.amount) error = %v", err)
	}
	if err := session.StageSQLSet(SQLValueRef{
		Table:        "settings_zones",
		Column:       "name",
		EntryName:    SettingsEntryName,
		ZoneIndex:    0,
		HasZoneIndex: true,
		ZoneID:       7,
		HasZoneID:    true,
	}.WithExpected("Zone A"), "SQL Zone"); err != nil {
		t.Fatalf("StageSQLSet(settings_zones.name) error = %v", err)
	}

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

	if err := session.StageSQLSet(SQLValueRef{
		Table:          "settings_simulation_values",
		Column:         "number_value",
		EntryName:      SettingsEntryName,
		OwnerKind:      "settings",
		OwnerID:        "settings",
		SettingName:    "pelletEnergy",
		Path:           "settings.pelletEnergy",
		ValueType:      "number",
		WrapperRawJSON: `{"Value":20}`,
	}.WithExpected(20.0), 33.5); err != nil {
		t.Fatalf("StageSQLSet(settings_simulation_values.number_value) error = %v", err)
	}
	if err := session.StageSQLSet(SQLValueRef{
		Table:          "settings_simulation_values",
		Column:         "bool_value",
		EntryName:      SettingsEntryName,
		OwnerKind:      "settings",
		OwnerID:        "settings",
		SettingName:    "debugFlag",
		Path:           "settings.debugFlag",
		ValueType:      "bool",
		WrapperRawJSON: `{"Value":true}`,
	}.WithExpected(true), false); err != nil {
		t.Fatalf("StageSQLSet(settings_simulation_values.bool_value) error = %v", err)
	}
	if err := session.StageSQLSet(SQLValueRef{
		Table:          "settings_simulation_values",
		Column:         "string_value",
		EntryName:      SettingsEntryName,
		OwnerKind:      "settings",
		OwnerID:        "settings",
		SettingName:    "worldLabel",
		Path:           "settings.worldLabel",
		ValueType:      "string",
		WrapperRawJSON: `{"Value":"alpha"}`,
	}.WithExpected("alpha"), "beta"); err != nil {
		t.Fatalf("StageSQLSet(settings_simulation_values.string_value) error = %v", err)
	}
	if err := session.StageSQLSet(SQLValueRef{
		Table:          "settings_independent_values",
		Column:         "number_value",
		EntryName:      SettingsEntryName,
		OwnerKind:      "settings_independent",
		OwnerID:        "independents",
		SettingName:    "worldSize",
		Path:           "settings.independents.worldSize",
		ValueType:      "number",
		WrapperRawJSON: `{"Value":1000}`,
	}.WithExpected(1000.0), 2000.0); err != nil {
		t.Fatalf("StageSQLSet(settings_independent_values.number_value) error = %v", err)
	}
	if err := session.StageSQLSet(SQLValueRef{
		Table:          "settings_material_values",
		Column:         "number_value",
		EntryName:      SettingsEntryName,
		OwnerKind:      "settings_material",
		OwnerID:        "Plant",
		SettingName:    "energy",
		Path:           "settings.materials.Plant.energy",
		ValueType:      "number",
		WrapperRawJSON: `{"Value":2}`,
	}.WithExpected(2.0), 4.25); err != nil {
		t.Fatalf("StageSQLSet(settings_material_values.number_value) error = %v", err)
	}
	if err := session.StageSQLSet(SQLValueRef{
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
	}.WithExpected(0.4), 0.85); err != nil {
		t.Fatalf("StageSQLSet(settings_zone_values.number_value wrapped) error = %v", err)
	}
	if err := session.StageSQLSet(SQLValueRef{
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
	}.WithExpected(5.0), 99.0); err != nil {
		t.Fatalf("StageSQLSet(settings_zone_values.number_value unwrapped) error = %v", err)
	}

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
