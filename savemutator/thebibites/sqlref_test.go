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
