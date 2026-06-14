package tests

import (
	"path/filepath"
	"testing"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

func TestExtractTablesLargestFixture(t *testing.T) {
	archive, err := tb.ParseFile(filepath.Join(fixtureDir, "autosave_20260301021357.zip"), nil)
	if err != nil {
		t.Fatalf("tb.ParseFile() error = %v", err)
	}

	tables := tb.ExtractTables("largest", archive)
	if tables.Archive.SaveID != "largest" {
		t.Fatalf("archive save ID = %q, want largest", tables.Archive.SaveID)
	}
	if len(tables.Entries) != 1043 {
		t.Fatalf("entries = %d, want 1043", len(tables.Entries))
	}
	if len(tables.Bibites) != 1027 {
		t.Fatalf("bibites = %d, want 1027", len(tables.Bibites))
	}
	if len(tables.Eggs) != 8 {
		t.Fatalf("eggs = %d, want 8", len(tables.Eggs))
	}
	if len(tables.Pellets) != 22902 {
		t.Fatalf("pellets = %d, want 22902", len(tables.Pellets))
	}
	if tables.Scene == nil {
		t.Fatalf("scene row was not extracted")
	}
	if tables.Scene.ReportedNBibites != 812 {
		t.Fatalf("scene reported nBibites = %d, want 812", tables.Scene.ReportedNBibites)
	}
	if tables.Scene.ParsedBibites != 1027 {
		t.Fatalf("scene parsed bibites = %d, want 1027", tables.Scene.ParsedBibites)
	}
	if len(tables.SettingsZones) != 2 || len(tables.SettingsZoneGeometry) != 2 {
		t.Fatalf("settings zones/geometry = %d/%d, want 2/2", len(tables.SettingsZones), len(tables.SettingsZoneGeometry))
	}
	if len(tables.SettingsBibiteSpawners) != 1 {
		t.Fatalf("settings spawners = %d, want 1", len(tables.SettingsBibiteSpawners))
	}
	if len(tables.SettingsChangers) != 2 {
		t.Fatalf("settings changers = %d, want 2", len(tables.SettingsChangers))
	}
	if len(tables.SettingsChangerPoints) == 0 || len(tables.SettingsChangerTargets) == 0 {
		t.Fatalf("settings changer child rows were not extracted")
	}
	if len(tables.ActiveSpecies) == 0 || len(tables.Species) == 0 {
		t.Fatalf("species rows were not extracted")
	}
	if len(tables.BibiteBody) == 0 || len(tables.BibiteMouth) == 0 || len(tables.BibiteControl) == 0 {
		t.Fatalf("bibite body child rows were not extracted")
	}
	if len(tables.BibiteGenes) == 0 || len(tables.EggGenes) == 0 {
		t.Fatalf("entity gene rows were not extracted")
	}
	if !hasMatterDecayRow(tables.Pellets) {
		t.Fatalf("expected at least one pellet matterDecay row value")
	}
	if len(tables.JSONScalars) == 0 {
		t.Fatalf("JSON scalar fallback rows were not extracted")
	}
}

func TestExtractTablesPheromonesWithoutSettingChangers(t *testing.T) {
	archive, err := tb.ParseFile(filepath.Join(fixtureDir, "dasdasd.zip"), nil)
	if err != nil {
		t.Fatalf("tb.ParseFile() error = %v", err)
	}

	tables := tb.ExtractTables("pheromones", archive)
	if len(tables.Pheromones) != 1 {
		t.Fatalf("pheromones = %d, want 1", len(tables.Pheromones))
	}
	if tables.Pheromones[0].HeadingRawJSON == "" {
		t.Fatalf("pheromone heading raw JSON was not preserved")
	}
	if tables.Pheromones[0].RStrength == 0 {
		t.Fatalf("pheromone R strength was not extracted")
	}
	if len(tables.SettingsChangers) != 0 || len(tables.SettingsChangerTargets) != 0 {
		t.Fatalf("setting changers/targets = %d/%d, want 0/0", len(tables.SettingsChangers), len(tables.SettingsChangerTargets))
	}
}

func hasMatterDecayRow(pellets []tb.PelletRow) bool {
	for _, pellet := range pellets {
		if pellet.HasMatterDecay {
			return true
		}
	}
	return false
}
