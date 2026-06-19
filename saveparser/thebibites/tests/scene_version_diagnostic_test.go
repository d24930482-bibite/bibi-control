package tests

import (
	"path/filepath"
	"testing"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

// TestSceneVersionGateEmitsDiagnostic is the M8 save-format drift gate: a scene
// whose version is outside the known/supported set must surface a
// scene_version_unsupported diagnostic (loud-but-localized) so a format bump shows
// up instead of silently mis-parsing — while still parsing the rest of the scene
// (the gate is non-fatal). A known version, and a scene with no version key at all
// (legacy saves), must stay quiet (no spurious noise).
func TestSceneVersionGateEmitsDiagnostic(t *testing.T) {
	const code = "scene_version_unsupported"

	// A bogus future version trips the gate but still parses nBibites/simulatedTime.
	bogus, err := tb.ParseBytesAs(tb.EntryScene, []byte(bom+`{"version":"99.9-future","nBibites":7,"simulatedTime":1234.5}`))
	if err != nil {
		t.Fatalf("ParseBytesAs(bogus version) error = %v", err)
	}
	if !hasDiagnostic(bogus.Diagnostics, code) {
		t.Fatalf("bogus scene version did not emit %q diagnostic: %#v", code, bogus.Diagnostics)
	}
	if bogus.Scene == nil {
		t.Fatalf("bogus scene version aborted parsing; gate must be non-fatal")
	}
	if !bogus.Scene.HasNBibites || bogus.Scene.NBibites != 7 {
		t.Fatalf("bogus scene nBibites = %d (has=%v), want 7 — gate must not stop parsing", bogus.Scene.NBibites, bogus.Scene.HasNBibites)
	}
	if !bogus.Scene.HasTime || bogus.Scene.SimulatedTime != 1234.5 {
		t.Fatalf("bogus scene simulatedTime = %v (has=%v), want 1234.5 — gate must not stop parsing", bogus.Scene.SimulatedTime, bogus.Scene.HasTime)
	}

	// A known version (carried by the real save fixtures) must NOT trip the gate.
	known, err := tb.ParseBytesAs(tb.EntryScene, []byte(bom+`{"version":"0.6.3","nBibites":7}`))
	if err != nil {
		t.Fatalf("ParseBytesAs(known version) error = %v", err)
	}
	if hasDiagnostic(known.Diagnostics, code) {
		t.Fatalf("known scene version spuriously emitted %q diagnostic: %#v", code, known.Diagnostics)
	}

	// A scene with no version key (legacy save) must stay quiet too.
	noVersion, err := tb.ParseBytesAs(tb.EntryScene, []byte(bom+`{"nBibites":7}`))
	if err != nil {
		t.Fatalf("ParseBytesAs(no version) error = %v", err)
	}
	if hasDiagnostic(noVersion.Diagnostics, code) {
		t.Fatalf("scene without version key spuriously emitted %q diagnostic: %#v", code, noVersion.Diagnostics)
	}
}

// TestSceneVersionGateReachesDiagnosticsTable confirms the drift gate surfaces both
// on a full archive parse and, after ExtractTables, in the normalized diagnostics
// table (the queryable surface) — and that a normal save (known version) produces
// zero of them, so the suite's own real-save fixtures stay quiet.
func TestSceneVersionGateReachesDiagnosticsTable(t *testing.T) {
	const code = "scene_version_unsupported"

	bogusPath := filepath.Join(t.TempDir(), "bogus-version.zip")
	createZip(t, bogusPath, map[string]string{
		"scene.bb8scene":       bom + `{"version":"99.9-future","nBibites":0}`,
		"settings.bb8settings": bom + `{}`,
		"speciesData.json":     bom + `{}`,
		"pellets.bb8scene":     bom + `{"pellets":[]}`,
		"pheromones.bb8scene":  bom + `{"pheromones":[]}`,
		"vars.bb8scene":        bom + `{"towerMaxID":1}`,
	})
	archive, err := tb.ParseFile(bogusPath, nil)
	if err != nil {
		t.Fatalf("ParseFile(bogus-version) error = %v", err)
	}
	if !hasDiagnostic(archive.Diagnostics, code) {
		t.Fatalf("archive parse did not emit %q diagnostic: %#v", code, archive.Diagnostics)
	}
	out := tb.ExtractTables("bogus-version", archive)
	if !diagnosticRowExists(out.Diagnostics, code) {
		t.Fatalf("%q diagnostic did not reach the normalized diagnostics table", code)
	}

	knownPath := filepath.Join(t.TempDir(), "known-version.zip")
	createZip(t, knownPath, map[string]string{
		"scene.bb8scene":       bom + `{"version":"0.6.3","nBibites":0}`,
		"settings.bb8settings": bom + `{}`,
		"speciesData.json":     bom + `{}`,
		"pellets.bb8scene":     bom + `{"pellets":[]}`,
		"pheromones.bb8scene":  bom + `{"pheromones":[]}`,
		"vars.bb8scene":        bom + `{"towerMaxID":1}`,
	})
	known, err := tb.ParseFile(knownPath, nil)
	if err != nil {
		t.Fatalf("ParseFile(known-version) error = %v", err)
	}
	if hasDiagnostic(known.Diagnostics, code) {
		t.Fatalf("known scene version spuriously emitted %q diagnostic: %#v", code, known.Diagnostics)
	}
	knownOut := tb.ExtractTables("known-version", known)
	if diagnosticRowExists(knownOut.Diagnostics, code) {
		t.Fatalf("known scene version spuriously emitted %q in diagnostics table", code)
	}
}
