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

	Scalars     []Scalar
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

	// Each entity already collected its scalars into its own slice, and result.Scalars
	// is empty here, so we share the entity slice directly instead of copying it in
	// (the copy was a top GC cost on bibite-heavy saves). applyParseResult later
	// copies these into a.Scalars, so the shared backing is never mutated afterward.
	switch entry.Kind {
	case EntryScene:
		result.Scene = parseScene(ctx, entry)
		if result.Scene != nil {
			result.Scalars = result.Scene.Scalars
		}
	case EntryVars:
		result.Vars = parseGenericJSON(ctx, entry, "vars")
		if result.Vars != nil {
			result.Generic = result.Vars
			result.Scalars = result.Vars.Scalars
		}
	case EntrySettings:
		result.Settings = parseSettings(ctx, entry)
		if result.Settings != nil {
			result.Scalars = result.Settings.Scalars
		}
	case EntrySpecies:
		result.Species = parseSpecies(ctx, entry)
		if result.Species != nil {
			result.Scalars = result.Species.Scalars
		}
	case EntryBibite:
		result.Bibite = parseBibite(ctx, entry)
		if result.Bibite != nil {
			result.Scalars = result.Bibite.Scalars
		}
	case EntryEgg:
		result.Egg = parseEgg(ctx, entry)
		if result.Egg != nil {
			result.Scalars = result.Egg.Scalars
		}
	case EntryPellets:
		result.PelletData = parsePellets(ctx, entry)
		if result.PelletData != nil {
			result.Scalars = result.PelletData.Scalars
		}
	case EntryPheromones:
		result.Pheromones, result.Scalars = parsePheromones(ctx, entry)
	case EntryUnknownJSON:
		result.Generic = parseGenericJSON(ctx, entry, "unknown_json")
		if result.Generic != nil {
			result.Scalars = result.Generic.Scalars
		}
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
	a.Scalars = append(a.Scalars, result.Scalars...)
	a.Diagnostics = append(a.Diagnostics, result.Diagnostics...)
}

func parseGenericJSON(ctx *parserContext, entry *Entry, ownerKind string) *GenericJSONState {
	raw, ok := asMap(entry.JSON)
	if !ok {
		ctx.addDiagnostic(SeverityWarning, "json_not_object", entry.Name, "JSON entry is not an object")
		return nil
	}
	return &GenericJSONState{
		EntryName: entry.Name,
		Raw:       raw,
		Scalars:   collectScalars(entry.Name, ownerKind, entry.Name, ownerKind, raw),
	}
}
