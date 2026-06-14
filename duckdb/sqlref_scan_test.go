package duckdb

import (
	"context"
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

func bibiteHealth(rows []tb.BibiteRow, entryName string, bodyID int64) (float64, bool) {
	for _, row := range rows {
		if row.EntryName == entryName && row.HasBodyID && row.BodyID == bodyID {
			return row.Health, true
		}
	}
	return 0, false
}

func settingNumberByPath(rows []tb.SettingValueRow, path string) (float64, bool) {
	for _, row := range rows {
		if row.Path == path && row.Type == tb.ScalarNumber {
			return row.NumberValue, true
		}
	}
	return 0, false
}
