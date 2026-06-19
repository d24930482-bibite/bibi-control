package thebibites

import "fmt"

// knownSceneVersions is the set of Bibites save-format versions the scene parser
// has been validated against. It is the single source of truth for the
// scene_version_unsupported drift gate below: a non-empty scene version outside
// this set is loud-but-localized (a diagnostic, not an abort), so a save-format
// bump surfaces in the diagnostics table instead of silently mis-parsing. Seed
// from the versions the real save fixtures carry; extend deliberately when a new
// format is verified.
var knownSceneVersions = map[string]struct{}{
	"0.6.3":   {},
	"0.6.3.1": {},
}

func parseScene(ctx *parserContext, entry *Entry) *SceneState {
	raw, ok := asMap(entry.JSON)
	if !ok {
		ctx.addDiagnostic(SeverityWarning, "scene_not_object", entry.Name, "scene JSON is not an object")
		return nil
	}
	scene := &SceneState{
		EntryName: entry.Name,
		Raw:       raw,
	}
	if v, ok := stringAt(raw, "version"); ok {
		scene.Version = v
		if _, known := knownSceneVersions[v]; v != "" && !known {
			ctx.addDiagnostic(SeverityWarning, "scene_version_unsupported", entry.Name,
				fmt.Sprintf("scene version %q is not in the known set; parsing may drift", v))
		}
	}
	if v, ok := intAt(raw, "nBibites"); ok {
		scene.NBibites = v
		scene.HasNBibites = true
	}
	if v, ok := intAt(raw, "nPellets"); ok {
		scene.NPellets = v
		scene.HasNPellets = true
	}
	if v, ok := floatAt(raw, "simulatedTime"); ok {
		scene.SimulatedTime = v
		scene.HasTime = true
	}
	return scene
}
