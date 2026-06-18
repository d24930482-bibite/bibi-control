package duckdb

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

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
// TestCopySavePartitionEqualsDoubleExtract proves the DB-derived history
// partition is byte-identical (modulo save_id) to today's second-extract output
// across every normalized table. It imports a save under a working key W, then
// builds the dst partition H two ways — the OLD path (ReplaceExtractedSave of a
// fresh ExtractTables(H, …)) and the NEW path (CopySavePartition(W, H)) — into
// separate in-memory dbs and asserts the full row set for H matches across all
// tables. It also asserts CopySavePartition is idempotent (delete-then-insert).
func TestCopySavePartitionEqualsDoubleExtract(t *testing.T) {
	ctx := context.Background()
	const fixtureSaveW = "copy-working-key"
	const fixtureSaveH = "copy-history-key"

	fixturePath := filepath.Join(repoRoot(t), "testdata", "saves", "the-bibites", "autosave_20260301021357.zip")
	archive, err := tb.ParseFile(fixturePath, nil)
	if err != nil {
		t.Fatalf("ParseFile(%q) error = %v", fixturePath, err)
	}

	// NEW path: import working under W, derive history under H via CopySavePartition.
	newDB, err := OpenAndImport(ctx, "", tb.ExtractTables(fixtureSaveW, archive))
	if err != nil {
		t.Fatalf("OpenAndImport(new) error = %v", err)
	}
	defer newDB.Close()
	if err := CopySavePartition(ctx, newDB, fixtureSaveW, fixtureSaveH); err != nil {
		t.Fatalf("CopySavePartition() error = %v", err)
	}

	// OLD path: import the SAME working under W, then a SECOND ExtractTables(H, …)
	// + ReplaceExtractedSave into the same db — exactly today's double-import.
	oldDB, err := OpenAndImport(ctx, "", tb.ExtractTables(fixtureSaveW, archive))
	if err != nil {
		t.Fatalf("OpenAndImport(old) error = %v", err)
	}
	defer oldDB.Close()
	if err := ReplaceExtractedSave(ctx, oldDB, tb.ExtractTables(fixtureSaveH, archive)); err != nil {
		t.Fatalf("ReplaceExtractedSave(old hist) error = %v", err)
	}

	populated := 0
	for _, table := range tb.NormalizedTables {
		oldRows := dumpPartition(t, ctx, oldDB, table, fixtureSaveH)
		newRows := dumpPartition(t, ctx, newDB, table, fixtureSaveH)
		if !rowSetsEqual(oldRows, newRows) {
			t.Fatalf("%s: derived history rows differ from double-extract (old=%d rows, new=%d rows)",
				table.Table, len(oldRows), len(newRows))
		}
		// And history (new path) == working modulo save_id: the working rows under
		// W with save_id rewritten to H must equal the derived history rows.
		workingRows := dumpPartitionRewritingSaveID(t, ctx, newDB, table, fixtureSaveW, fixtureSaveH)
		if !rowSetsEqual(workingRows, newRows) {
			t.Fatalf("%s: derived history rows differ from working rows modulo save_id", table.Table)
		}
		if len(newRows) > 0 {
			populated++
		}
	}
	if populated == 0 {
		t.Fatalf("no normalized table had rows; the byte-equality comparison was vacuous")
	}

	// Idempotency: a second CopySavePartition yields the same row counts.
	before := dumpPartition(t, ctx, newDB, tb.NormalizedTables[0], fixtureSaveH)
	if err := CopySavePartition(ctx, newDB, fixtureSaveW, fixtureSaveH); err != nil {
		t.Fatalf("second CopySavePartition() error = %v", err)
	}
	for _, table := range tb.NormalizedTables {
		got := countRows(t, ctx, newDB, table.Table, fixtureSaveH)
		want := int64(len(dumpPartition(t, ctx, oldDB, table, fixtureSaveH)))
		if got != want {
			t.Fatalf("%s: rows after re-derive = %d, want %d (idempotency broken)", table.Table, got, want)
		}
	}
	_ = before
}

// TestCopySavePartitionPerf reports the extract+import wall time of the OLD
// double-extract path (ExtractTables+ReplaceExtractedSave twice) vs the NEW path
// (one ExtractTables+ImportExtractedSave for the working partition + a single
// set-based CopySavePartition for history) on the 3.1MB fixture. It is the
// reviewer's perf evidence that the SECOND extract+import is gone; it does not
// assert a threshold (timings vary by host) but logs both numbers and asserts the
// new path imports the same row counts.
func TestCopySavePartitionPerf(t *testing.T) {
	if testing.Short() {
		t.Skip("perf measurement; skipped in -short")
	}
	ctx := context.Background()
	const wKey = "perf-working"
	const hKey = "perf-history"
	fixturePath := filepath.Join(repoRoot(t), "testdata", "saves", "the-bibites", "autosave_20260301021357.zip")
	archive, err := tb.ParseFile(fixturePath, nil)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	// OLD path: working extract+import, THEN a second history extract+import.
	oldDB, err := Open("")
	if err != nil {
		t.Fatalf("Open(old): %v", err)
	}
	defer oldDB.Close()
	if err := ApplyMigrations(ctx, oldDB); err != nil {
		t.Fatalf("ApplyMigrations(old): %v", err)
	}
	oldStart := time.Now()
	if err := ReplaceExtractedSave(ctx, oldDB, tb.ExtractTables(wKey, archive)); err != nil {
		t.Fatalf("old working import: %v", err)
	}
	oldWorking := time.Since(oldStart)
	hist2Start := time.Now()
	if err := ReplaceExtractedSave(ctx, oldDB, tb.ExtractTables(hKey, archive)); err != nil {
		t.Fatalf("old history import (2nd extract): %v", err)
	}
	oldHistory := time.Since(hist2Start)
	oldTotal := oldWorking + oldHistory

	// NEW path: working extract+import, THEN derive history via CopySavePartition.
	newDB, err := Open("")
	if err != nil {
		t.Fatalf("Open(new): %v", err)
	}
	defer newDB.Close()
	if err := ApplyMigrations(ctx, newDB); err != nil {
		t.Fatalf("ApplyMigrations(new): %v", err)
	}
	newStart := time.Now()
	if err := ReplaceExtractedSave(ctx, newDB, tb.ExtractTables(wKey, archive)); err != nil {
		t.Fatalf("new working import: %v", err)
	}
	newWorking := time.Since(newStart)
	copyStart := time.Now()
	if err := CopySavePartition(ctx, newDB, wKey, hKey); err != nil {
		t.Fatalf("new history copy: %v", err)
	}
	newCopy := time.Since(copyStart)
	newTotal := newWorking + newCopy

	t.Logf("OLD extract+import: working=%s + 2nd-extract-history=%s = total %s", oldWorking, oldHistory, oldTotal)
	t.Logf("NEW extract+import: working=%s + DB-copy-history=%s = total %s", newWorking, newCopy, newTotal)

	// Row counts must match the old double-extract for every table (the perf win
	// must not change what is materialized).
	for _, table := range tb.NormalizedTables {
		o := countRows(t, ctx, oldDB, table.Table, hKey)
		n := countRows(t, ctx, newDB, table.Table, hKey)
		if o != n {
			t.Fatalf("%s: new history rows = %d, want %d (perf path changed materialization)", table.Table, n, o)
		}
	}
}

// dumpPartition returns every column of every row for saveID in table, ordered
// deterministically by all columns so two equal row sets compare equal.
func dumpPartition(t *testing.T, ctx context.Context, db *sql.DB, table tb.NormalizedTableSpec, saveID string) [][]any {
	t.Helper()
	cols := make([]string, len(table.Fields))
	for i, f := range table.Fields {
		cols[i] = QuoteIdent(f.Column)
	}
	colList := strings.Join(cols, ", ")
	query := fmt.Sprintf("SELECT %s FROM %s WHERE save_id = ? ORDER BY %s",
		colList, QuoteIdent(table.Table), colList)
	return scanAll(t, ctx, db, query, len(cols), saveID)
}

// dumpPartitionRewritingSaveID returns every row for srcSaveID in table with the
// save_id column rewritten to dstSaveID, ordered like dumpPartition so it can be
// compared against the derived dst partition (history == working modulo save_id).
func dumpPartitionRewritingSaveID(t *testing.T, ctx context.Context, db *sql.DB, table tb.NormalizedTableSpec, srcSaveID, dstSaveID string) [][]any {
	t.Helper()
	cols := make([]string, len(table.Fields))
	sel := make([]string, len(table.Fields))
	for i, f := range table.Fields {
		q := QuoteIdent(f.Column)
		cols[i] = q
		if f.Column == "save_id" {
			sel[i] = "? AS " + q
		} else {
			sel[i] = q
		}
	}
	query := fmt.Sprintf("SELECT %s FROM %s WHERE save_id = ? ORDER BY %s",
		strings.Join(sel, ", "), QuoteIdent(table.Table), strings.Join(cols, ", "))
	return scanAll(t, ctx, db, query, len(cols), dstSaveID, srcSaveID)
}

func scanAll(t *testing.T, ctx context.Context, db *sql.DB, query string, ncols int, args ...any) [][]any {
	t.Helper()
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	defer rows.Close()
	var out [][]any
	for rows.Next() {
		cells := make([]any, ncols)
		ptrs := make([]any, ncols)
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, cells)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	return out
}

func rowSetsEqual(a, b [][]any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if fmt.Sprintf("%T:%v", a[i][j], a[i][j]) != fmt.Sprintf("%T:%v", b[i][j], b[i][j]) {
				return false
			}
		}
	}
	return true
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

// extractorFixture is a throwaway row type with one field of every kind in the
// normalized-table universe (string, signed int widths, unsigned int widths,
// floats, bool) plus a pointer variant of each family. buildColumnExtractors is
// exercised through the real build path against it so the value-identity test
// proves the typed extractor matches fieldValue for every type, including the
// pointer/nil->NULL and uint64-overflow seams that are load-bearing for DuckDB.
type extractorFixture struct {
	Str   string
	I     int
	I8    int8
	I16   int16
	I32   int32
	I64   int64
	U     uint
	U8    uint8
	U16   uint16
	U32   uint32
	U64   uint64
	F32   float32
	F64   float64
	B     bool
	PStr  *string
	PI64  *int64
	PU64  *uint64
	PF64  *float64
	PB    *bool
	PNil  *int64
	PPI64 **int64
}

// TestFieldExtractorMatchesFieldValue asserts the precomputed typed extractor
// produces byte-identical (driver.Value, error) results to the reference
// fieldValue conversion for every field kind, including a uint64 > maxDriverInt64
// overflow (which must return the exact same error) and nil-vs-set pointers (NULL
// vs deref). Any divergence here is silent DuckDB corruption — this is the guard.
func TestFieldExtractorMatchesFieldValue(t *testing.T) {
	str := "deref-me"
	i64 := int64(-7)
	u64ok := uint64(123)
	f64 := 3.5
	b := true
	pi64 := int64(99)
	ppi64 := &pi64

	row := extractorFixture{
		Str:   "hello",
		I:     -1,
		I8:    -8,
		I16:   -16,
		I32:   -32,
		I64:   -64,
		U:     1,
		U8:    8,
		U16:   16,
		U32:   32,
		U64:   maxDriverInt64, // largest value that does NOT overflow
		F32:   1.25,
		F64:   2.5,
		B:     true,
		PStr:  &str,
		PI64:  &i64,
		PU64:  &u64ok,
		PF64:  &f64,
		PB:    &b,
		PNil:  nil,
		PPI64: &ppi64,
	}

	elem := reflect.TypeOf(row)
	rv := reflect.ValueOf(row)

	// Build extractors through the real path: one fieldSpec per struct field,
	// keyed by field name (the column name is irrelevant to extraction).
	fields := make([]fieldSpec, elem.NumField())
	for i := 0; i < elem.NumField(); i++ {
		fields[i] = fieldSpec{field: elem.Field(i).Name, column: elem.Field(i).Name}
	}
	extractors, err := buildColumnExtractors(elem, fields)
	if err != nil {
		t.Fatalf("buildColumnExtractors() error = %v", err)
	}
	if len(extractors) != elem.NumField() {
		t.Fatalf("extractors = %d, want %d", len(extractors), elem.NumField())
	}

	for i := 0; i < elem.NumField(); i++ {
		name := elem.Field(i).Name
		fv := rv.Field(i)

		gotVal, gotErr := extractors[i].extract(fv)
		wantVal, wantErr := fieldValue(fv)

		if !errorsEquivalent(gotErr, wantErr) {
			t.Fatalf("field %s: extractor err = %v, fieldValue err = %v", name, gotErr, wantErr)
		}
		if fmt.Sprintf("%T:%v", gotVal, gotVal) != fmt.Sprintf("%T:%v", wantVal, wantVal) {
			t.Fatalf("field %s: extractor value = %T:%v, fieldValue value = %T:%v",
				name, gotVal, gotVal, wantVal, wantVal)
		}
	}

	// Explicit overflow seam: a uint64 just past maxDriverInt64 must return the
	// exact same error from both paths (not swallowed, not wrapped differently).
	overflow := reflect.ValueOf(maxDriverInt64 + 1)
	exVal, exErr := cellValue(overflow)
	fvVal, fvErr := fieldValue(overflow)
	if exErr == nil || fvErr == nil {
		t.Fatalf("uint64 overflow: expected error, got extractor=%v fieldValue=%v", exErr, fvErr)
	}
	if exErr.Error() != fvErr.Error() {
		t.Fatalf("uint64 overflow: extractor err %q != fieldValue err %q", exErr.Error(), fvErr.Error())
	}
	if exErr.Error() != fmt.Sprintf("uint value %d overflows int64", maxDriverInt64+1) {
		t.Fatalf("uint64 overflow: unexpected error string %q", exErr.Error())
	}
	if exVal != nil || fvVal != nil {
		t.Fatalf("uint64 overflow: expected nil value, got extractor=%v fieldValue=%v", exVal, fvVal)
	}

	// Explicit nil-pointer seam: a nil *T must yield (nil, nil) NULL from both.
	var nilPtr *int64
	npv := reflect.ValueOf(nilPtr)
	if v, err := extractorForType(npv.Type())(npv); v != nil || err != nil {
		t.Fatalf("nil pointer extractor = (%v, %v), want (nil, nil)", v, err)
	}
}

// errorsEquivalent reports whether two errors are both nil or have equal
// messages (the extractor must match fieldValue's error text exactly).
func errorsEquivalent(a, b error) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	return a.Error() == b.Error()
}

// TestAppendMaterializationUnchanged proves the typed-extractor append path lands
// byte-identical values in every normalized table versus the reference fieldValue
// extraction, over the full 3.1MB fixture (~40 tables / 134k rows). It imports the
// fixture, then for every tb.NormalizedTables table compares the dumped DuckDB rows
// against the values recomputed directly from the in-memory ExtractedSave via
// fieldValue. This is the "speedup must not change what lands" gate.
func TestAppendMaterializationUnchanged(t *testing.T) {
	ctx := context.Background()
	const saveID = "materialization-identity"
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

	saveValue := reflect.ValueOf(save)
	compared := 0
	for _, table := range tb.NormalizedTables {
		want := referenceRowsViaFieldValue(t, saveValue, table)
		got := dumpPartitionValueKeyed(t, ctx, db, table, saveID)
		if !valueRowSetsEqual(want, got) {
			t.Fatalf("%s: imported rows differ from fieldValue extraction (fieldValue=%d rows, db=%d rows)",
				table.Table, len(want), len(got))
		}
		if len(want) > 0 {
			compared += len(want)
		}
	}
	if compared == 0 {
		t.Fatalf("no normalized table had rows; the byte-identity comparison was vacuous")
	}
	t.Logf("materialization byte-identical to fieldValue across %d tables, %d rows", len(tb.NormalizedTables), compared)
}

// referenceRowsViaFieldValue extracts every row of one table from the in-memory
// ExtractedSave using the reference fieldValue conversion, ordered to match
// dumpPartition (sorted by the formatted cell tuple) so the two row sets compare
// directly. It is the golden the import path must reproduce exactly.
func referenceRowsViaFieldValue(t *testing.T, saveValue reflect.Value, table tb.NormalizedTableSpec) [][]any {
	t.Helper()
	rows := saveValue.FieldByName(table.SaveField)
	if !rows.IsValid() {
		t.Fatalf("ExtractedSave missing field %s for table %s", table.SaveField, table.Table)
	}
	// Normalize to a slice of rows mirroring insertExtractedTable/appendRows.
	switch rows.Kind() {
	case reflect.Pointer:
		if rows.IsNil() {
			return nil
		}
	case reflect.Slice:
		if rows.Len() == 0 {
			return nil
		}
	}
	if rows.Kind() != reflect.Slice {
		single := reflect.MakeSlice(reflect.SliceOf(rows.Type()), 1, 1)
		single.Index(0).Set(rows)
		rows = single
	}

	specs := fieldSpecs(table.Fields)
	out := make([][]any, 0, rows.Len())
	for i := 0; i < rows.Len(); i++ {
		row := rows.Index(i)
		for row.Kind() == reflect.Pointer {
			row = row.Elem()
		}
		cells := make([]any, len(specs))
		for j, spec := range specs {
			sf, ok := row.Type().FieldByName(spec.field)
			if !ok {
				t.Fatalf("%s: missing field %s", table.Table, spec.field)
			}
			v, err := fieldValue(row.FieldByIndex(sf.Index))
			if err != nil {
				t.Fatalf("%s field %s: fieldValue error = %v", table.Table, spec.field, err)
			}
			cells[j] = v
		}
		out = append(out, cells)
	}
	sortRowTuples(out)
	return out
}

// sortRowTuples orders rows by their value-normalized cell tuple so a golden built
// from in-memory Go values lines up positionally with the DuckDB-scanned rows for
// valueRowSetsEqual, independent of column order ties.
func sortRowTuples(rows [][]any) {
	sort.Slice(rows, func(i, j int) bool {
		return rowTupleKey(rows[i]) < rowTupleKey(rows[j])
	})
}

func rowTupleKey(cells []any) string {
	parts := make([]string, len(cells))
	for i, c := range cells {
		parts[i] = cellValueKey(c)
	}
	return strings.Join(parts, "\x00")
}

// cellValueKey is a type-normalized rendering of a scalar cell. It deliberately
// erases the Go integer-type tag so that an int64(n) produced by fieldValue and a
// uint64(n) scanned back from a UBIGINT column (the unsigned columns the appender
// writes as int64 and DuckDB returns as uint64) compare equal by VALUE — the
// integrity property H5 must preserve. Floats and strings/bools render by value.
// This is the existing int64->UBIGINT round-trip, not anything H5 changed; the
// extractor itself is proven identical to fieldValue by TestFieldExtractorMatchesFieldValue.
func cellValueKey(c any) string {
	switch v := c.(type) {
	case nil:
		return "NULL"
	case int64:
		return fmt.Sprintf("i:%d", v)
	case uint64:
		return fmt.Sprintf("i:%d", v)
	case int32:
		return fmt.Sprintf("i:%d", v)
	case float64:
		return fmt.Sprintf("f:%v", v)
	case float32:
		return fmt.Sprintf("f:%v", float64(v))
	case bool:
		return fmt.Sprintf("b:%t", v)
	case string:
		return "s:" + v
	default:
		return fmt.Sprintf("%T:%v", c, c)
	}
}

// dumpPartitionValueKeyed dumps a table partition and sorts the rows by the same
// value-normalized key used for the golden, so the two row sets align for
// valueRowSetsEqual without depending on the int-vs-uint type tag in ORDER BY.
func dumpPartitionValueKeyed(t *testing.T, ctx context.Context, db *sql.DB, table tb.NormalizedTableSpec, saveID string) [][]any {
	t.Helper()
	rows := dumpPartition(t, ctx, db, table, saveID)
	sortRowTuples(rows)
	return rows
}

// valueRowSetsEqual compares two row sets by value-normalized cell keys (so the
// int64/uint64 round-trip is treated as equal), asserting every cell of every row
// is value-identical.
func valueRowSetsEqual(a, b [][]any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if cellValueKey(a[i][j]) != cellValueKey(b[i][j]) {
				return false
			}
		}
	}
	return true
}

// TestAppendPerf logs the append wall-time of the typed-extractor path on the
// 3.1MB fixture with ApplyMigrations timed separately, and asserts the imported
// row counts per table as a correctness floor. It models TestCopySavePartitionPerf
// and does not assert a hard timing threshold (the before/after numeric comparison
// is the reviewer's Perf-review job, since a pre-H5 build is not in-process).
func TestAppendPerf(t *testing.T) {
	if testing.Short() {
		t.Skip("perf measurement; skipped in -short")
	}
	ctx := context.Background()
	const saveID = "append-perf"
	fixturePath := filepath.Join(repoRoot(t), "testdata", "saves", "the-bibites", "autosave_20260301021357.zip")
	archive, err := tb.ParseFile(fixturePath, nil)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	save := tb.ExtractTables(saveID, archive)

	db, err := Open("")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	migStart := time.Now()
	if err := ApplyMigrations(ctx, db); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	migDur := time.Since(migStart)

	appendStart := time.Now()
	if err := ReplaceExtractedSave(ctx, db, save); err != nil {
		t.Fatalf("ReplaceExtractedSave: %v", err)
	}
	appendDur := time.Since(appendStart)

	var total int64
	for _, table := range tb.NormalizedTables {
		total += countRows(t, ctx, db, table.Table, saveID)
	}
	if total == 0 {
		t.Fatalf("no rows imported; perf measurement was vacuous")
	}
	t.Logf("ApplyMigrations=%s  ReplaceExtractedSave(append)=%s  rows=%d", migDur, appendDur, total)
}
