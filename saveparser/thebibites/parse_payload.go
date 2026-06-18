package thebibites

type parserContext struct {
	diagnostics []Diagnostic
}

func (ctx *parserContext) addDiagnostic(severity DiagnosticSeverity, code, entry, message string) {
	ctx.diagnostics = append(ctx.diagnostics, Diagnostic{
		Severity: severity,
		Code:     code,
		Entry:    entry,
		Message:  message,
	})
}

type parseResult struct {
	Scene      *SceneState
	Vars       *GenericJSONState
	Generic    *GenericJSONState
	Settings   *SettingsState
	Species    *SpeciesData
	Bibite     *Bibite
	Egg        *Egg
	PelletData *PelletData
	Pheromones []Pheromone

	Diagnostics []Diagnostic
}

func parseEntryPayload(entry *Entry) parseResult {
	ctx := &parserContext{}
	result := parseResult{}

	if isJSONKind(entry.Kind) {
		value, hasBOM, err := decodeJSONWithBOM(entry.Raw)
		entry.HasUTF8BOM = hasBOM
		if err != nil {
			ctx.addDiagnostic(SeverityError, "json_decode_failed", entry.Name, err.Error())
			result.Diagnostics = ctx.diagnostics
			return result
		}
		entry.JSON = value
	}

	switch entry.Kind {
	case EntryScene:
		result.Scene = parseScene(ctx, entry)
	case EntryVars:
		result.Vars = parseGenericJSON(ctx, entry, "vars")
		if result.Vars != nil {
			result.Generic = result.Vars
		}
	case EntrySettings:
		result.Settings = parseSettings(ctx, entry)
	case EntrySpecies:
		result.Species = parseSpecies(ctx, entry)
	case EntryBibite:
		result.Bibite = parseBibite(ctx, entry)
	case EntryEgg:
		result.Egg = parseEgg(ctx, entry)
	case EntryPellets:
		result.PelletData = parsePellets(ctx, entry)
	case EntryPheromones:
		result.Pheromones = parsePheromones(ctx, entry)
	case EntryUnknownJSON:
		result.Generic = parseGenericJSON(ctx, entry, "unknown_json")
	}

	result.Diagnostics = ctx.diagnostics
	return result
}

func (a *Archive) applyParseResult(result parseResult) {
	if result.Scene != nil {
		a.Scene = result.Scene
	}
	if result.Vars != nil {
		a.Vars = result.Vars
	}
	if result.Settings != nil {
		a.Settings = result.Settings
	}
	if result.Species != nil {
		a.Species = result.Species
	}
	if result.Bibite != nil {
		a.Bibites = append(a.Bibites, *result.Bibite)
	}
	if result.Egg != nil {
		a.Eggs = append(a.Eggs, *result.Egg)
	}
	if result.PelletData != nil {
		a.PelletData = result.PelletData
	}
	if len(result.Pheromones) > 0 {
		a.Pheromones = append(a.Pheromones, result.Pheromones...)
	}
	a.Diagnostics = append(a.Diagnostics, result.Diagnostics...)
}

// parseGenericJSON captures a JSON object section that has no dedicated typed
// parser. ownerKind distinguishes the known "vars" section (which has a typed
// `vars` row) from an "unknown_json" section. Since H4 dropped the json_scalars
// EAV that was the only data trace of an unknown section, parseGenericJSON now
// emits a loud Diagnostic for unknown sections so save-format drift (a new or
// renamed top-level JSON section) stays visible in the diagnostics table even
// though its data is no longer captured. Known sections like "vars" never trip
// it. See the H4 churn-resilience guardrail and the save-format-churn-strategy.
func parseGenericJSON(ctx *parserContext, entry *Entry, ownerKind string) *GenericJSONState {
	raw, ok := asMap(entry.JSON)
	if !ok {
		ctx.addDiagnostic(SeverityWarning, "json_not_object", entry.Name, "JSON entry is not an object")
		return nil
	}
	if ownerKind == "unknown_json" {
		ctx.addDiagnostic(SeverityWarning, "unknown_json_section", entry.Name,
			"unrecognized JSON section captured only as a diagnostic; add a typed table if its data is needed")
	}
	return &GenericJSONState{
		EntryName: entry.Name,
		Raw:       raw,
	}
}
