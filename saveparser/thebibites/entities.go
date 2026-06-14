package thebibites

import "fmt"

func parseBibite(ctx *parserContext, entry *Entry) *Bibite {
	raw, ok := asMap(entry.JSON)
	if !ok {
		ctx.addDiagnostic(SeverityWarning, "bibite_not_object", entry.Name, "bibite JSON is not an object")
		return nil
	}

	body, _ := mapAt(raw, "body")
	id, hasID := intAt(body, "id")
	ownerID := ownerIDFromInt(id, hasID, entry.Name)
	bibite := &Bibite{
		EntryName: entry.Name,
		FileIndex: entry.Index,
		Raw:       raw,
		ID:        id,
		HasID:     hasID,
		Scalars:   collectScalars(entry.Name, "bibite", ownerID, "bibite", raw),
	}
	if !hasID {
		ctx.addDiagnostic(SeverityWarning, "bibite_missing_body_id", entry.Name, "bibite body.id is missing or not numeric")
	}

	if transform, ok := mapAt(raw, "transform"); ok {
		bibite.Transform = parseTransform(transform)
	}
	if rb2d, ok := mapAt(raw, "rb2d"); ok {
		bibite.RigidBody = parseRigidBody(rb2d)
	}
	parseBibiteGenes(entry.Name, ownerID, raw, bibite)
	parseBibiteBody(entry.Name, id, hasID, ownerID, body, bibite)
	parseBibiteClock(entry.Name, ownerID, raw, bibite)
	parseEntityBrain(entry.Name, "bibite", ownerID, raw, &bibite.BrainNodes, &bibite.BrainSynapses)
	return bibite
}

func parseBibiteGenes(entryName, ownerID string, raw map[string]any, bibite *Bibite) {
	genes, ok := mapAt(raw, "genes")
	if !ok {
		return
	}
	if v, ok := intAt(genes, "speciesID"); ok {
		bibite.SpeciesID = v
	}
	if v, ok := intAt(genes, "gen"); ok {
		bibite.Generation = v
	}
	bibite.GeneScalars = collectScalars(entryName, "bibite_genes", ownerID, "genes", genes)
}

func parseBibiteBody(entryName string, id int64, hasID bool, ownerID string, body map[string]any, bibite *Bibite) {
	if body == nil {
		return
	}
	if v, ok := boolAt(body, "dead"); ok {
		bibite.Dead = v
	}
	if v, ok := boolAt(body, "dying"); ok {
		bibite.Dying = v
	}
	if v, ok := floatAt(body, "health"); ok {
		bibite.Health = v
	}
	if v, ok := floatAt(body, "energy"); ok {
		bibite.Energy = v
	}
	parseBibiteBodyDetails(body, bibite)
	bibite.BodyScalars = collectScalars(entryName, "bibite_body", ownerID, "body", body)
	bibite.StomachContents = parseStomachContents(entryName, id, hasID, body)
	bibite.Children = parseChildLinks(entryName, id, hasID, body)
}

func parseBibiteBodyDetails(body map[string]any, bibite *Bibite) {
	if v, ok := floatAt(body, "d2Size"); ok {
		bibite.BodyDetails.D2Size = v
	}
	if v, ok := floatAt(body, "fatReservesAmount"); ok {
		bibite.BodyDetails.FatReservesAmount = v
	}
	if v, ok := floatAt(body, "attackedDmg"); ok {
		bibite.BodyDetails.AttackedDmg = v
	}
	if v, ok := floatAt(body, "timesAttacked"); ok {
		bibite.BodyDetails.TimesAttacked = v
	}
	if v, ok := floatAt(body, "totalDamageSuffered"); ok {
		bibite.BodyDetails.TotalDamageSuffered = v
	}
	if v, ok := floatAt(body, "brainTicksCount"); ok {
		bibite.BodyDetails.BrainTicksCount = v
	}
	if v, ok := floatAt(body, "visionLookupCount"); ok {
		bibite.BodyDetails.VisionLookupCount = v
	}
	if v, ok := floatAt(body, "visionSensingCount"); ok {
		bibite.BodyDetails.VisionSensingCount = v
	}
	if v, ok := floatAt(body, "corpseEnergyOffset"); ok {
		bibite.BodyDetails.CorpseEnergyOffset = v
	}
	if mouth, ok := mapAt(body, "mouth"); ok {
		parseBibiteMouth(mouth, bibite)
	}
	if phero, ok := mapAt(body, "phero"); ok {
		if v, ok := floatAt(phero, "progress"); ok {
			bibite.Pheromone.Progress = v
		}
	}
	if eggLayer, ok := mapAt(body, "eggLayer"); ok {
		if v, ok := floatAt(eggLayer, "eggProgress"); ok {
			bibite.EggLayer.EggProgress = v
		}
		if v, ok := floatAt(eggLayer, "nEggsLaid"); ok {
			bibite.EggLayer.NEggsLaid = v
		}
	}
	if control, ok := mapAt(body, "control"); ok {
		if v, ok := floatAt(control, "totalTravel"); ok {
			bibite.Control.TotalTravel = v
		}
	}
}

func parseBibiteMouth(mouth map[string]any, bibite *Bibite) {
	if v, ok := boolAt(mouth, "attackedLastFrame"); ok {
		bibite.Mouth.AttackedLastFrame = v
	}
	if v, ok := floatAt(mouth, "bibitesBitten"); ok {
		bibite.Mouth.BibitesBitten = v
	}
	if v, ok := floatAt(mouth, "biteProgress"); ok {
		bibite.Mouth.BiteProgress = v
	}
	if v, ok := floatAt(mouth, "murderedArea"); ok {
		bibite.Mouth.MurderedArea = v
	}
	if v, ok := floatAt(mouth, "totalDamageDealt"); ok {
		bibite.Mouth.TotalDamageDealt = v
	}
	if v, ok := floatAt(mouth, "totalMurders"); ok {
		bibite.Mouth.TotalMurders = v
	}
}

func parseBibiteClock(entryName, ownerID string, raw map[string]any, bibite *Bibite) {
	clock, ok := mapAt(raw, "clock")
	if !ok {
		return
	}
	if v, ok := floatAt(clock, "timeAlive"); ok {
		bibite.TimeAlive = v
	}
	bibite.ClockScalars = collectScalars(entryName, "bibite_clock", ownerID, "clock", clock)
}

func parseStomachContents(entryName string, bibiteID int64, hasBibiteID bool, body map[string]any) []StomachContent {
	stomach, ok := mapAt(body, "stomach")
	if !ok {
		return nil
	}
	values, ok := listAt(stomach, "content")
	if !ok {
		return nil
	}
	contents := make([]StomachContent, 0, len(values))
	ownerID := ownerIDFromInt(bibiteID, hasBibiteID, entryName)
	for i, value := range values {
		raw, ok := asMap(value)
		if !ok {
			continue
		}
		content := StomachContent{
			Index:       i,
			EntryName:   entryName,
			BibiteID:    bibiteID,
			HasBibiteID: hasBibiteID,
			Raw:         raw,
			Scalars:     collectScalars(entryName, "bibite_stomach_content", ownerID, fmt.Sprintf("body.stomach.content[%d]", i), raw),
		}
		if v, ok := stringAt(raw, "material"); ok {
			content.Material = v
		}
		if v, ok := floatAt(raw, "amount"); ok {
			content.Amount = v
		}
		if v, ok := floatAt(raw, "averageChunkAmount"); ok {
			content.AverageChunkAmount = v
		}
		contents = append(contents, content)
	}
	return contents
}

func parseChildLinks(entryName string, parentID int64, hasParentID bool, body map[string]any) []ChildLink {
	eggLayer, ok := mapAt(body, "eggLayer")
	if !ok {
		return nil
	}
	values, ok := listAt(eggLayer, "children")
	if !ok {
		return nil
	}
	children := make([]ChildLink, 0, len(values))
	for i, value := range values {
		if childID, ok := toInt(value); ok {
			children = append(children, ChildLink{
				Index:       i,
				EntryName:   entryName,
				ParentID:    parentID,
				HasParentID: hasParentID,
				ChildID:     childID,
			})
		}
	}
	return children
}

func parseEgg(ctx *parserContext, entry *Entry) *Egg {
	raw, ok := asMap(entry.JSON)
	if !ok {
		ctx.addDiagnostic(SeverityWarning, "egg_not_object", entry.Name, "egg JSON is not an object")
		return nil
	}

	eggState, _ := mapAt(raw, "egg")
	id, hasID := intAt(eggState, "id")
	ownerID := ownerIDFromInt(id, hasID, entry.Name)
	egg := &Egg{
		EntryName: entry.Name,
		FileIndex: entry.Index,
		Raw:       raw,
		ID:        id,
		HasID:     hasID,
		Scalars:   collectScalars(entry.Name, "egg", ownerID, "egg_entity", raw),
	}
	if transform, ok := mapAt(raw, "transform"); ok {
		egg.Transform = parseTransform(transform)
	}
	if rb2d, ok := mapAt(raw, "rb2d"); ok {
		egg.RigidBody = parseRigidBody(rb2d)
	}
	parseEggGenes(entry.Name, ownerID, raw, egg)
	parseEggState(entry.Name, ownerID, eggState, egg)
	parseEntityBrain(entry.Name, "egg", ownerID, raw, &egg.BrainNodes, &egg.BrainSynapses)
	return egg
}

func parseEggGenes(entryName, ownerID string, raw map[string]any, egg *Egg) {
	genes, ok := mapAt(raw, "genes")
	if !ok {
		return
	}
	if v, ok := intAt(genes, "speciesID"); ok {
		egg.SpeciesID = v
	}
	if v, ok := intAt(genes, "gen"); ok {
		egg.Generation = v
	}
	egg.GeneScalars = collectScalars(entryName, "egg_genes", ownerID, "genes", genes)
}

func parseEggState(entryName, ownerID string, eggState map[string]any, egg *Egg) {
	if eggState == nil {
		return
	}
	if v, ok := floatAt(eggState, "hatchProgress"); ok {
		egg.HatchProgress = v
	}
	if v, ok := floatAt(eggState, "energy"); ok {
		egg.Energy = v
	}
	egg.EggScalars = collectScalars(entryName, "egg_state", ownerID, "egg", eggState)
}

func parseEntityBrain(entryName, ownerKind, ownerID string, raw map[string]any, nodesOut *[]BrainNode, synapsesOut *[]BrainSynapse) {
	brain, ok := mapAt(raw, "brain")
	if !ok {
		return
	}
	if nodes, ok := listAt(brain, "Nodes"); ok {
		*nodesOut = parseBrainNodes(entryName, ownerKind, ownerID, nodes)
	}
	if synapses, ok := listAt(brain, "Synapses"); ok {
		*synapsesOut = parseBrainSynapses(entryName, ownerKind, ownerID, synapses)
	}
}
