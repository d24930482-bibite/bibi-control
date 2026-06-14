package duckdb

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mutator "github.com/asemones/bibicontrol/savemutator/thebibites"
	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

func TestScanSQLRefsScansDuckDBRows(t *testing.T) {
	ctx := context.Background()
	save := sampleExtractedSave()
	db, err := OpenAndImport(ctx, "", save)
	if err != nil {
		t.Fatalf("OpenAndImport() error = %v", err)
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, `
		SELECT entry_name, body_id, has_body_id, health
		FROM bibites
		WHERE save_id = ?
	`, save.Archive.SaveID)
	if err != nil {
		t.Fatalf("query bibites: %v", err)
	}
	defer rows.Close()

	got, err := ScanSQLRefs(rows, SQLRefScanSpec{
		Table:  "bibites",
		Column: "health",
	})
	if err != nil {
		t.Fatalf("ScanSQLRefs() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("SQL refs = %d, want 1", len(got))
	}
	ref := got[0].Ref
	if ref.Table != "bibites" || ref.Column != "health" {
		t.Fatalf("ref target = %s.%s, want bibites.health", ref.Table, ref.Column)
	}
	if ref.EntryName != "bibites/bibite_0.bb8" || ref.BodyID != 42 || !ref.HasBodyID {
		t.Fatalf("ref locator = (%q, %d, %t), want (bibites/bibite_0.bb8, 42, true)", ref.EntryName, ref.BodyID, ref.HasBodyID)
	}
	if got[0].CurrentValue != 1.0 {
		t.Fatalf("current value = %v, want 1", got[0].CurrentValue)
	}
}

func TestScanSQLRefsRequiresCurrentValueColumn(t *testing.T) {
	ctx := context.Background()
	save := sampleExtractedSave()
	db, err := OpenAndImport(ctx, "", save)
	if err != nil {
		t.Fatalf("OpenAndImport() error = %v", err)
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, `
		SELECT entry_name, body_id, has_body_id
		FROM bibites
		WHERE save_id = ?
	`, save.Archive.SaveID)
	if err != nil {
		t.Fatalf("query bibites: %v", err)
	}
	defer rows.Close()

	_, err = ScanSQLRefs(rows, SQLRefScanSpec{
		Table:  "bibites",
		Column: "health",
	})
	if err == nil {
		t.Fatalf("ScanSQLRefs() error = nil, want missing value column")
	}
	if !strings.Contains(err.Error(), "current value column") {
		t.Fatalf("ScanSQLRefs() error = %v, want current value column", err)
	}
}

func TestScanSQLRefsStagesFixtureMutation(t *testing.T) {
	ctx := context.Background()
	const saveID = "sql-ref-refeed-fixture"
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

	rows, err := db.QueryContext(ctx, `
		SELECT entry_name, body_id, has_body_id, health
		FROM bibites
		WHERE save_id = ?
		  AND has_body_id
		  AND NOT dead
		  AND health > 0
		ORDER BY energy ASC
		LIMIT 1
	`, saveID)
	if err != nil {
		t.Fatalf("query bibite health refs: %v", err)
	}
	defer rows.Close()

	refs, err := ScanSQLRefs(rows, SQLRefScanSpec{
		Table:  "bibites",
		Column: "health",
	})
	if err != nil {
		t.Fatalf("ScanSQLRefs() error = %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("SQL refs = %d, want 1", len(refs))
	}

	session := mutator.NewSession(archive)
	if err := session.StageSQLSet(refs[0].Ref.WithExpected(refs[0].CurrentValue), 0.0); err != nil {
		t.Fatalf("StageSQLSet() error = %v", err)
	}
	fresh, err := session.Commit(filepath.Join(t.TempDir(), "mutated.zip"))
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	mutated := tb.ExtractTables("mutated", fresh)
	got, ok := bibiteHealth(mutated.Bibites, refs[0].Ref.EntryName, refs[0].Ref.BodyID)
	if !ok {
		t.Fatalf("mutated bibite %q/%d not found", refs[0].Ref.EntryName, refs[0].Ref.BodyID)
	}
	if got != 0.0 {
		t.Fatalf("mutated health = %v, want 0", got)
	}
}

func TestScanSQLRefsStagesFixtureSettingsValueMutation(t *testing.T) {
	ctx := context.Background()
	const saveID = "sql-ref-settings-refeed-fixture"
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

	rows, err := db.QueryContext(ctx, `
		SELECT entry_name,
		       owner_kind,
		       owner_id,
		       setting_name,
		       path,
		       value_type,
		       wrapper_raw_json,
		       number_value
		FROM settings_simulation_values
		WHERE save_id = ?
		  AND value_type = 'number'
		ORDER BY setting_name
		LIMIT 1
	`, saveID)
	if err != nil {
		t.Fatalf("query settings value refs: %v", err)
	}
	defer rows.Close()

	refs, err := ScanSQLRefs(rows, SQLRefScanSpec{
		Table:  "settings_simulation_values",
		Column: "number_value",
	})
	if err != nil {
		t.Fatalf("ScanSQLRefs() error = %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("SQL refs = %d, want 1", len(refs))
	}
	current, ok := refs[0].CurrentValue.(float64)
	if !ok {
		t.Fatalf("current value = %T, want float64", refs[0].CurrentValue)
	}
	next := current + 1.25

	session := mutator.NewSession(archive)
	if err := session.StageSQLSet(refs[0].Ref.WithExpected(refs[0].CurrentValue), next); err != nil {
		t.Fatalf("StageSQLSet() error = %v", err)
	}
	fresh, err := session.Commit(filepath.Join(t.TempDir(), "mutated.zip"))
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	mutated := tb.ExtractTables("mutated", fresh)
	got, ok := settingNumberByPath(mutated.SettingsSimulationValues, refs[0].Ref.Path)
	if !ok {
		t.Fatalf("mutated setting path %q not found", refs[0].Ref.Path)
	}
	if got != next {
		t.Fatalf("mutated setting = %v, want %v", got, next)
	}
}

func TestSmokeFixtureSetsBibitesGreenAndZoneFertility(t *testing.T) {
	ctx := context.Background()
	const saveID = "sql-ref-smoke-green-fertility"
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

	session := mutator.NewSession(archive)
	for _, gene := range []struct {
		name  string
		value float64
	}{
		{name: "ColorR", value: 0.0},
		{name: "ColorG", value: 1.0},
		{name: "ColorB", value: 0.0},
	} {
		refs := scanBibiteGeneRefs(t, ctx, db, saveID, gene.name)
		if len(refs) != len(save.Bibites) {
			t.Fatalf("%s refs = %d, want one per bibite (%d)", gene.name, len(refs), len(save.Bibites))
		}
		stageSQLRefs(t, session, refs, gene.value)
	}

	zoneRefs := scanZoneFertilityRefs(t, ctx, db, saveID)
	if len(zoneRefs) != len(save.SettingsZones) {
		t.Fatalf("zone fertility refs = %d, want one per zone (%d)", len(zoneRefs), len(save.SettingsZones))
	}
	stageSQLRefs(t, session, zoneRefs, 30.0)

	changerRefs := scanZoneChangerFertilityRefs(t, ctx, db, saveID)
	if len(changerRefs) == 0 {
		t.Fatalf("zone changer fertility refs = 0, want at least one")
	}
	stageSQLRefs(t, session, changerRefs, 30.0)

	outDir := filepath.Join(os.TempDir(), "bibicontrol-smoke")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", outDir, err)
	}
	outPath := filepath.Join(outDir, "green-fertility.zip")
	fresh, err := session.Commit(outPath)
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	t.Logf("wrote smoke save: %s", outPath)
	installedPath, err := mutator.InstallSaveFile(outPath, "green-fertility.zip")
	if err != nil {
		t.Fatalf("InstallSaveFile() error = %v", err)
	}
	t.Logf("installed smoke save: %s", installedPath)

	mutated := tb.ExtractTables("mutated", fresh)
	assertAllBibitesGreen(t, mutated.BibiteGenes, len(mutated.Bibites))
	assertAllZonesFertility(t, mutated.SettingsZoneValues, len(mutated.SettingsZones), 30.0)
	assertAllZoneChangerFertility(t, mutated.SettingsChangerTargets, 30.0)
}

func bibiteHealth(rows []tb.BibiteRow, entryName string, bodyID int64) (float64, bool) {
	for _, row := range rows {
		if row.EntryName == entryName && row.HasBodyID && row.BodyID == bodyID {
			return row.Health, true
		}
	}
	return 0, false
}

func scanBibiteGeneRefs(t *testing.T, ctx context.Context, db queryer, saveID, geneName string) []SQLRefRow {
	t.Helper()

	rows, err := db.QueryContext(ctx, `
		SELECT entry_name,
		       owner_kind,
		       owner_id,
		       path,
		       number_value
		FROM bibite_genes
		WHERE save_id = ?
		  AND gene_name = ?
		  AND value_type = 'number'
		ORDER BY entry_name, owner_id
	`, saveID, geneName)
	if err != nil {
		t.Fatalf("query %s refs: %v", geneName, err)
	}
	defer rows.Close()

	refs, err := ScanSQLRefs(rows, SQLRefScanSpec{
		Table:  "bibite_genes",
		Column: "number_value",
	})
	if err != nil {
		t.Fatalf("ScanSQLRefs(%s) error = %v", geneName, err)
	}
	return refs
}

func scanZoneFertilityRefs(t *testing.T, ctx context.Context, db queryer, saveID string) []SQLRefRow {
	t.Helper()

	rows, err := db.QueryContext(ctx, `
		SELECT entry_name,
		       owner_kind,
		       owner_id,
		       setting_name,
		       path,
		       value_type,
		       wrapper_raw_json,
		       number_value
		FROM settings_zone_values
		WHERE save_id = ?
		  AND setting_name = 'fertility'
		  AND value_type = 'number'
		ORDER BY path
	`, saveID)
	if err != nil {
		t.Fatalf("query zone fertility refs: %v", err)
	}
	defer rows.Close()

	refs, err := ScanSQLRefs(rows, SQLRefScanSpec{
		Table:  "settings_zone_values",
		Column: "number_value",
	})
	if err != nil {
		t.Fatalf("ScanSQLRefs(zone fertility) error = %v", err)
	}
	return refs
}

func scanZoneChangerFertilityRefs(t *testing.T, ctx context.Context, db queryer, saveID string) []SQLRefRow {
	t.Helper()

	rows, err := db.QueryContext(ctx, `
		SELECT entry_name,
		       changer_index,
		       target_key,
		       scope,
		       zone_index,
		       zone_id,
		       has_zone_id,
		       setting_name,
		       value_type,
		       number_value
		FROM settings_changer_targets
		WHERE save_id = ?
		  AND scope = 'zone'
		  AND setting_name = 'fertility'
		  AND value_type = 'number'
		ORDER BY changer_index, target_key
	`, saveID)
	if err != nil {
		t.Fatalf("query zone changer fertility refs: %v", err)
	}
	defer rows.Close()

	refs, err := ScanSQLRefs(rows, SQLRefScanSpec{
		Table:  "settings_changer_targets",
		Column: "number_value",
	})
	if err != nil {
		t.Fatalf("ScanSQLRefs(zone changer fertility) error = %v", err)
	}
	return refs
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func stageSQLRefs(t *testing.T, session *mutator.Session, refs []SQLRefRow, value any) {
	t.Helper()

	for i, row := range refs {
		if err := session.StageSQLSet(row.Ref.WithExpected(row.CurrentValue), value); err != nil {
			t.Fatalf("StageSQLSet ref %d (%s.%s %s): %v", i, row.Ref.Table, row.Ref.Column, row.Ref.Path, err)
		}
	}
}

func assertAllBibitesGreen(t *testing.T, rows []tb.GeneRow, wantBibites int) {
	t.Helper()

	want := map[string]float64{
		"ColorR": 0.0,
		"ColorG": 1.0,
		"ColorB": 0.0,
	}
	seen := make(map[string]map[string]struct{}, len(want))
	for gene := range want {
		seen[gene] = make(map[string]struct{}, wantBibites)
	}

	for _, row := range rows {
		wantValue, ok := want[row.GeneName]
		if !ok {
			continue
		}
		if row.Type != tb.ScalarNumber {
			t.Fatalf("%s for %s/%s type = %s, want number", row.GeneName, row.EntryName, row.OwnerID, row.Type)
		}
		if row.NumberValue != wantValue {
			t.Fatalf("%s for %s/%s = %v, want %v", row.GeneName, row.EntryName, row.OwnerID, row.NumberValue, wantValue)
		}
		key := row.EntryName + "\x00" + row.OwnerID
		if _, exists := seen[row.GeneName][key]; exists {
			t.Fatalf("duplicate %s row for %s/%s", row.GeneName, row.EntryName, row.OwnerID)
		}
		seen[row.GeneName][key] = struct{}{}
	}

	for gene, refs := range seen {
		if len(refs) != wantBibites {
			t.Fatalf("%s rows = %d, want one per bibite (%d)", gene, len(refs), wantBibites)
		}
	}
}

func assertAllZonesFertility(t *testing.T, rows []tb.SettingValueRow, wantZones int, wantValue float64) {
	t.Helper()

	seen := make(map[string]struct{}, wantZones)
	for _, row := range rows {
		if row.SettingName != "fertility" {
			continue
		}
		if row.Type != tb.ScalarNumber {
			t.Fatalf("fertility %s type = %s, want number", row.Path, row.Type)
		}
		if row.NumberValue != wantValue {
			t.Fatalf("fertility %s = %v, want %v", row.Path, row.NumberValue, wantValue)
		}
		if _, exists := seen[row.Path]; exists {
			t.Fatalf("duplicate fertility row for %s", row.Path)
		}
		seen[row.Path] = struct{}{}
	}
	if len(seen) != wantZones {
		t.Fatalf("fertility rows = %d, want one per zone (%d)", len(seen), wantZones)
	}
}

func assertAllZoneChangerFertility(t *testing.T, rows []tb.SettingsChangerTargetRow, wantValue float64) {
	t.Helper()

	seen := 0
	for _, row := range rows {
		if row.Scope != "zone" || row.SettingName != "fertility" {
			continue
		}
		seen++
		if row.Type != tb.ScalarNumber {
			t.Fatalf("changer fertility %s type = %s, want number", row.TargetKey, row.Type)
		}
		if row.NumberValue != wantValue {
			t.Fatalf("changer fertility %s = %v, want %v", row.TargetKey, row.NumberValue, wantValue)
		}
	}
	if seen == 0 {
		t.Fatalf("changer fertility rows = 0, want at least one")
	}
}

func settingNumberByPath(rows []tb.SettingValueRow, path string) (float64, bool) {
	for _, row := range rows {
		if row.Path == path && row.Type == tb.ScalarNumber {
			return row.NumberValue, true
		}
	}
	return 0, false
}
