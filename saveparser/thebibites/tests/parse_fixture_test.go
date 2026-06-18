package tests

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

var fixtureDir = filepath.Join("..", "..", "..", "testdata", "saves", "the-bibites")

func TestParseFixtureArchives(t *testing.T) {
	tests := []struct {
		name        string
		sha256      string
		totalFiles  int
		bibiteFiles int
		eggFiles    int
	}{
		{
			name:        "autosave_20260228004041.zip",
			sha256:      "498f228d2c0d40e7289bd8df672fd028749646d5da6ab8d69453919a01adfb81",
			totalFiles:  55,
			bibiteFiles: 46,
			eggFiles:    1,
		},
		{
			name:        "autosave_20260228005042.zip",
			sha256:      "259316aaa7fa2b503e8ec7ec38c3141052797dcdaac04ff55a934b6e359b2c12",
			totalFiles:  230,
			bibiteFiles: 219,
			eggFiles:    3,
		},
		{
			name:        "autosave_20260228010042.zip",
			sha256:      "2deb226057f60c34b8159cd09f078ebd4ab60b2575ec242339b5f463e3973348",
			totalFiles:  482,
			bibiteFiles: 467,
			eggFiles:    7,
		},
		{
			name:        "autosave_20260301021357.zip",
			sha256:      "d1381916a8f8ab8cb1dc35fe26b0eafa37a807049b1e6b70a1101412d0f9333c",
			totalFiles:  1043,
			bibiteFiles: 1027,
			eggFiles:    8,
		},
		{
			name:        "d.zip",
			sha256:      "d93c4d0ff2b320b712b6fb9a042f9c8626ad27af0f40596b8e79ac81faedb51f",
			totalFiles:  670,
			bibiteFiles: 653,
			eggFiles:    9,
		},
		{
			name:        "dasdasd.zip",
			sha256:      "b7e27f6f4b28fffd68f3e9972c62f4c6b50c82150a63460318a464d00ebafcf4",
			totalFiles:  35,
			bibiteFiles: 27,
			eggFiles:    0,
		},
		{
			name:        "dddd.zip",
			sha256:      "c612f29ca24f0a341636512581d64db41a45b92ca3140233ed3be7731b8361a0",
			totalFiles:  35,
			bibiteFiles: 27,
			eggFiles:    0,
		},
		{
			name:        "s.zip",
			sha256:      "2c68a80f8ce92980880b114fc9b43c585716f307eb090979448ff4aa7725577f",
			totalFiles:  604,
			bibiteFiles: 585,
			eggFiles:    11,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			archive, err := tb.ParseFile(filepath.Join(fixtureDir, tt.name), nil)
			if err != nil {
				t.Fatalf("tb.ParseFile() error = %v", err)
			}

			if archive.SHA256 != tt.sha256 {
				t.Fatalf("archive hash = %s, want %s", archive.SHA256, tt.sha256)
			}
			if archive.Counts.ArchiveEntryCount != tt.totalFiles {
				t.Fatalf("entry count = %d, want %d", archive.Counts.ArchiveEntryCount, tt.totalFiles)
			}
			if archive.Counts.BibiteFileCount != tt.bibiteFiles {
				t.Fatalf("bibite files = %d, want %d", archive.Counts.BibiteFileCount, tt.bibiteFiles)
			}
			if archive.Counts.EggFileCount != tt.eggFiles {
				t.Fatalf("egg files = %d, want %d", archive.Counts.EggFileCount, tt.eggFiles)
			}
			if archive.Counts.ParsedBibites != tt.bibiteFiles {
				t.Fatalf("parsed bibites = %d, want %d", archive.Counts.ParsedBibites, tt.bibiteFiles)
			}
			if archive.Counts.ParsedEggs != tt.eggFiles {
				t.Fatalf("parsed eggs = %d, want %d", archive.Counts.ParsedEggs, tt.eggFiles)
			}
			if archive.Entry("data.bin") == nil || len(archive.Entry("data.bin").Raw) == 0 {
				t.Fatalf("data.bin was not preserved")
			}
			if archive.Entry("img.png") == nil || len(archive.Entry("img.png").Raw) == 0 {
				t.Fatalf("img.png was not preserved")
			}
			for _, name := range []string{"settings.bb8settings", "speciesData.json", "scene.bb8scene"} {
				entry := archive.Entry(name)
				if entry == nil {
					t.Fatalf("missing %s", name)
				}
				if !entry.HasUTF8BOM {
					t.Fatalf("%s did not record expected UTF-8 BOM", name)
				}
				if entry.JSON == nil {
					t.Fatalf("%s did not retain decoded JSON", name)
				}
			}
			if len(archive.Bibites) > 0 && len(archive.Bibites[0].BrainNodes) == 0 {
				t.Fatalf("first bibite has no parsed brain nodes")
			}
			if archive.Settings == nil || len(archive.Settings.Materials) == 0 || len(archive.Settings.Zones) == 0 {
				t.Fatalf("settings materials/zones were not parsed")
			}
			if archive.Species == nil || len(archive.Species.Records) == 0 {
				t.Fatalf("species records were not parsed")
			}
			if archive.PelletData == nil || archive.Counts.Pellets == 0 {
				t.Fatalf("pellets were not parsed")
			}
		})
	}
}

func TestParseLargestFixtureDerivedCounts(t *testing.T) {
	archive, err := tb.ParseFile(filepath.Join(fixtureDir, "autosave_20260301021357.zip"), nil)
	if err != nil {
		t.Fatalf("tb.ParseFile() error = %v", err)
	}

	if archive.Counts.BibiteFileCount != 1027 {
		t.Fatalf("bibite file count = %d, want 1027", archive.Counts.BibiteFileCount)
	}
	if archive.Counts.EggFileCount != 8 {
		t.Fatalf("egg file count = %d, want 8", archive.Counts.EggFileCount)
	}
	if archive.Counts.UniqueBibiteBodyIDs != 1027 {
		t.Fatalf("unique body.id count = %d, want 1027", archive.Counts.UniqueBibiteBodyIDs)
	}
	if archive.Counts.AliveBibites != 804 {
		t.Fatalf("alive bibites = %d, want 804", archive.Counts.AliveBibites)
	}
	if archive.Counts.DeadBibites != 223 {
		t.Fatalf("dead bibites = %d, want 223", archive.Counts.DeadBibites)
	}
	if !archive.Counts.HasSceneNBibites || archive.Counts.SceneReportedBibites != 812 {
		t.Fatalf("scene nBibites = %d, has %t; want 812", archive.Counts.SceneReportedBibites, archive.Counts.HasSceneNBibites)
	}
	if !archive.Counts.HasSceneNPellets || archive.Counts.SceneReportedPellets != 22902 {
		t.Fatalf("scene nPellets = %d, has %t; want 22902", archive.Counts.SceneReportedPellets, archive.Counts.HasSceneNPellets)
	}
	if archive.Counts.Pellets != 22902 {
		t.Fatalf("parsed pellets = %d, want 22902", archive.Counts.Pellets)
	}
	if !hasDiagnostic(archive.Diagnostics, "scene_nbibites_mismatch") {
		t.Fatalf("expected scene_nbibites_mismatch diagnostic")
	}
	if len(archive.Eggs) != 8 || len(archive.Eggs[0].BrainNodes) == 0 || len(archive.Eggs[0].BrainSynapses) == 0 {
		t.Fatalf("eggs did not include parsed brain data")
	}
	if len(archive.Bibites[0].StomachContents) == 0 {
		t.Fatalf("bibite stomach contents were not parsed")
	}
	if len(archive.Bibites[0].Children) == 0 {
		t.Fatalf("bibite children were not parsed")
	}
}

func TestParseSingleEntryBytes(t *testing.T) {
	const fixtureName = "autosave_20260301021357.zip"

	bibiteRaw := readFixtureEntry(t, fixtureName, "bibites/bibite_0.bb8")
	parsedBibite, err := tb.ParseEntryBytes("bibites/bibite_0.bb8", bibiteRaw)
	if err != nil {
		t.Fatalf("tb.ParseEntryBytes() error = %v", err)
	}
	if parsedBibite.Entry.Kind != tb.EntryBibite {
		t.Fatalf("kind = %s, want %s", parsedBibite.Entry.Kind, tb.EntryBibite)
	}
	if !parsedBibite.Entry.HasUTF8BOM {
		t.Fatalf("bibite did not record UTF-8 BOM")
	}
	if parsedBibite.Bibite == nil {
		t.Fatalf("Bibite = nil")
	}
	if parsedBibite.Bibite.ID != 1925731107 {
		t.Fatalf("bibite body.id = %d, want 1925731107", parsedBibite.Bibite.ID)
	}
	if len(parsedBibite.Bibite.BrainNodes) != 48 {
		t.Fatalf("bibite brain nodes = %d, want 48", len(parsedBibite.Bibite.BrainNodes))
	}
	if len(parsedBibite.Bibite.BrainSynapses) != 3 {
		t.Fatalf("bibite brain synapses = %d, want 3", len(parsedBibite.Bibite.BrainSynapses))
	}
	if len(parsedBibite.Bibite.StomachContents) == 0 {
		t.Fatalf("bibite stomach contents were not parsed")
	}
	if len(parsedBibite.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %#v", parsedBibite.Diagnostics)
	}

	sceneRaw := readFixtureEntry(t, fixtureName, "scene.bb8scene")
	parsedScene, err := tb.ParseBytesAs(tb.EntryScene, sceneRaw)
	if err != nil {
		t.Fatalf("tb.ParseBytesAs(scene) error = %v", err)
	}
	if parsedScene.Scene == nil {
		t.Fatalf("Scene = nil")
	}
	if parsedScene.Scene.NBibites != 812 {
		t.Fatalf("scene nBibites = %d, want 812", parsedScene.Scene.NBibites)
	}
	if hasDiagnostic(parsedScene.Diagnostics, "scene_nbibites_mismatch") {
		t.Fatalf("single-entry parse should not emit archive-level count mismatch")
	}

	parsedCustomScene, err := tb.ParseEntryBytesAs("scratch/scene.bb8scene", tb.EntryScene, sceneRaw)
	if err != nil {
		t.Fatalf("tb.ParseEntryBytesAs(scene) error = %v", err)
	}
	if parsedCustomScene.Entry.Kind != tb.EntryScene || parsedCustomScene.Scene == nil {
		t.Fatalf("explicit scene kind was not honored")
	}

	eggRaw := readFixtureEntry(t, fixtureName, "eggs/egg_0.bb8")
	parsedEgg, err := tb.ParseBytesAs(tb.EntryEgg, eggRaw)
	if err != nil {
		t.Fatalf("tb.ParseBytesAs(egg) error = %v", err)
	}
	if parsedEgg.Egg == nil {
		t.Fatalf("Egg = nil")
	}
	if len(parsedEgg.Egg.BrainNodes) != 48 || len(parsedEgg.Egg.BrainSynapses) != 3 {
		t.Fatalf("egg brain data was not parsed")
	}
}

func TestSingleEntryMalformedJSONIsDiagnostic(t *testing.T) {
	parsed, err := tb.ParseBytesAs(tb.EntryBibite, []byte("\ufeff{\"body\":"))
	if err != nil {
		t.Fatalf("tb.ParseBytesAs() error = %v", err)
	}
	if parsed.Bibite != nil {
		t.Fatalf("Bibite = %#v, want nil", parsed.Bibite)
	}
	if !hasDiagnostic(parsed.Diagnostics, "json_decode_failed") {
		t.Fatalf("expected json_decode_failed diagnostic")
	}
	if len(parsed.Entry.Raw) == 0 {
		t.Fatalf("raw bytes were not preserved")
	}
}

func TestRejectsUnsafeZipEntryNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unsafe.zip")
	createZip(t, path, map[string]string{
		"../escape.bb8scene": "\ufeff{}",
	})

	_, err := tb.ParseFile(path, nil)
	if err == nil {
		t.Fatalf("tb.ParseFile() error = nil, want unsafe entry error")
	}
}

func TestMalformedJSONIsDiagnosticNotArchiveFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "malformed.zip")
	createZip(t, path, map[string]string{
		"scene.bb8scene":       "\ufeff{\"nBibites\":1}",
		"settings.bb8settings": "\ufeff{}",
		"speciesData.json":     "\ufeff{}",
		"pellets.bb8scene":     "\ufeff{\"pellets\":[]}",
		"pheromones.bb8scene":  "\ufeff{\"pheromones\":[]}",
		"vars.bb8scene":        "\ufeff{}",
		"data.bin":             "opaque",
		"img.png":              "preview",
		"bibites/bibite_0.bb8": "\ufeff{\"body\":",
	})

	archive, err := tb.ParseFile(path, nil)
	if err != nil {
		t.Fatalf("tb.ParseFile() error = %v", err)
	}
	if archive.Counts.BibiteFileCount != 1 {
		t.Fatalf("bibite file count = %d, want 1", archive.Counts.BibiteFileCount)
	}
	if archive.Counts.ParsedBibites != 0 {
		t.Fatalf("parsed bibites = %d, want 0", archive.Counts.ParsedBibites)
	}
	if !hasDiagnostic(archive.Diagnostics, "json_decode_failed") {
		t.Fatalf("expected json_decode_failed diagnostic")
	}
}

func hasDiagnostic(diagnostics []tb.Diagnostic, code string) bool {
	return slices.ContainsFunc(diagnostics, func(diagnostic tb.Diagnostic) bool {
		return diagnostic.Code == code
	})
}

func readFixtureEntry(t *testing.T, fixtureName, entryName string) []byte {
	t.Helper()

	reader, err := zip.OpenReader(filepath.Join(fixtureDir, fixtureName))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer reader.Close()

	for _, file := range reader.File {
		if file.Name != entryName {
			continue
		}
		entry, err := file.Open()
		if err != nil {
			t.Fatalf("open fixture entry %s: %v", entryName, err)
		}
		defer entry.Close()

		raw, err := io.ReadAll(entry)
		if err != nil {
			t.Fatalf("read fixture entry %s: %v", entryName, err)
		}
		return raw
	}
	t.Fatalf("fixture entry %s not found", entryName)
	return nil
}

func createZip(t *testing.T, path string, files map[string]string) {
	t.Helper()

	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	defer file.Close()

	writer := zip.NewWriter(file)
	defer writer.Close()

	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %s: %v", name, err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatalf("write zip entry %s: %v", name, err)
		}
	}
}
