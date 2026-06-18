package tests

import (
	"path/filepath"
	"testing"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

// bom is the UTF-8 byte order mark the game prefixes every JSON entry with. Built
// from an escape so the test source file itself stays BOM-free.
const bom = "\ufeff"

// TestUnknownJSONSectionEmitsDiagnostic is the HARD churn-resilience guard for
// H4: after dropping the json_scalars EAV (its only data trace), a brand-new
// unknown JSON object section must still surface loudly as a Diagnostic so save
// format drift stays visible. A known section like vars must NOT trip it.
func TestUnknownJSONSectionEmitsDiagnostic(t *testing.T) {
	const code = "unknown_json_section"

	// An unknown JSON section that decodes to a valid object: previously its only
	// signal was json_scalars rows; now it must emit the diagnostic.
	parsedUnknown, err := tb.ParseEntryBytesAs("future.bb8scene", tb.EntryUnknownJSON, []byte(`{"newThing":{"a":1},"b":2}`))
	if err != nil {
		t.Fatalf("ParseEntryBytesAs(unknown_json) error = %v", err)
	}
	if !hasDiagnostic(parsedUnknown.Diagnostics, code) {
		t.Fatalf("unknown JSON section did not emit %q diagnostic: %#v", code, parsedUnknown.Diagnostics)
	}
	if parsedUnknown.Generic == nil {
		t.Fatalf("unknown JSON section was not captured as generic state")
	}

	// A known vars section is a recognized section with a typed vars row; it must
	// NOT emit the unknown-section diagnostic (no spurious noise).
	parsedVars, err := tb.ParseBytesAs(tb.EntryVars, []byte(bom+`{"towerMaxID":5}`))
	if err != nil {
		t.Fatalf("ParseBytesAs(vars) error = %v", err)
	}
	if hasDiagnostic(parsedVars.Diagnostics, code) {
		t.Fatalf("known vars section spuriously emitted %q diagnostic: %#v", code, parsedVars.Diagnostics)
	}

	// An unknown section that is NOT a JSON object still gets the existing
	// json_not_object warning, not the unknown-section one (the new diagnostic is
	// scoped to valid objects, which were previously silent).
	parsedNonObject, err := tb.ParseEntryBytesAs("future.bb8scene", tb.EntryUnknownJSON, []byte(`[1,2,3]`))
	if err != nil {
		t.Fatalf("ParseEntryBytesAs(unknown_json non-object) error = %v", err)
	}
	if !hasDiagnostic(parsedNonObject.Diagnostics, "json_not_object") {
		t.Fatalf("non-object unknown section did not emit json_not_object: %#v", parsedNonObject.Diagnostics)
	}
	if hasDiagnostic(parsedNonObject.Diagnostics, code) {
		t.Fatalf("non-object unknown section should not emit %q: %#v", code, parsedNonObject.Diagnostics)
	}
}

// TestUnknownJSONDiagnosticSurvivesExtractTables confirms an unknown section in a
// real archive surfaces the diagnostic both on the archive and, after
// ExtractTables, in the normalized diagnostics table (the queryable surface) — and
// that a normal save with no unknown sections produces zero of them (no spurious
// noise once json_scalars is gone).
func TestUnknownJSONDiagnosticSurvivesExtractTables(t *testing.T) {
	const code = "unknown_json_section"

	// A save that additionally carries a brand-new unrecognized JSON section.
	withUnknownPath := filepath.Join(t.TempDir(), "with-unknown.zip")
	createZip(t, withUnknownPath, map[string]string{
		"scene.bb8scene":       bom + `{"nBibites":0}`,
		"settings.bb8settings": bom + `{}`,
		"speciesData.json":     bom + `{}`,
		"pellets.bb8scene":     bom + `{"pellets":[]}`,
		"pheromones.bb8scene":  bom + `{"pheromones":[]}`,
		"vars.bb8scene":        bom + `{"towerMaxID":1}`,
		"future.bb8scene":      bom + `{"newThing":{"a":1}}`,
	})
	archive, err := tb.ParseFile(withUnknownPath, nil)
	if err != nil {
		t.Fatalf("ParseFile(with-unknown) error = %v", err)
	}
	if archive.Entry("future.bb8scene") == nil || archive.Entry("future.bb8scene").Kind != tb.EntryUnknownJSON {
		t.Fatalf("future.bb8scene was not classified as unknown JSON")
	}
	if !hasDiagnostic(archive.Diagnostics, code) {
		t.Fatalf("archive parse did not emit %q diagnostic: %#v", code, archive.Diagnostics)
	}
	out := tb.ExtractTables("with-unknown", archive)
	if !diagnosticRowExists(out.Diagnostics, code) {
		t.Fatalf("%q diagnostic did not reach the normalized diagnostics table", code)
	}

	// A normal save with only recognized sections must produce zero of them.
	normalPath := filepath.Join(t.TempDir(), "normal.zip")
	createZip(t, normalPath, map[string]string{
		"scene.bb8scene":       bom + `{"nBibites":0}`,
		"settings.bb8settings": bom + `{}`,
		"speciesData.json":     bom + `{}`,
		"pellets.bb8scene":     bom + `{"pellets":[]}`,
		"pheromones.bb8scene":  bom + `{"pheromones":[]}`,
		"vars.bb8scene":        bom + `{"towerMaxID":1}`,
	})
	normal, err := tb.ParseFile(normalPath, nil)
	if err != nil {
		t.Fatalf("ParseFile(normal) error = %v", err)
	}
	if hasDiagnostic(normal.Diagnostics, code) {
		t.Fatalf("normal save spuriously emitted %q diagnostic: %#v", code, normal.Diagnostics)
	}
	normalOut := tb.ExtractTables("normal", normal)
	if diagnosticRowExists(normalOut.Diagnostics, code) {
		t.Fatalf("normal save spuriously emitted %q in diagnostics table", code)
	}
}

func diagnosticRowExists(rows []tb.DiagnosticRow, code string) bool {
	for _, r := range rows {
		if r.Code == code {
			return true
		}
	}
	return false
}
