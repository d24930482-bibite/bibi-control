package thebibites

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
