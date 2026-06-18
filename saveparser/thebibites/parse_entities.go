package thebibites

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
	}
	if !hasID {
		ctx.addDiagnostic(SeverityWarning, "bibite_missing_body_id", entry.Name, "bibite body.id is missing or not numeric")
	}

	parseBibiteBody(entry.Name, id, hasID, body, bibite)
	parseEntityBrain(entry.Name, "bibite", ownerID, raw, &bibite.BrainNodes, &bibite.BrainSynapses)
	return bibite
}

func parseBibiteBody(entryName string, id int64, hasID bool, body map[string]any, bibite *Bibite) {
	if body == nil {
		return
	}
	if v, ok := boolAt(body, "dead"); ok {
		bibite.Dead = v
	}
	if v, ok := boolAt(body, "dying"); ok {
		bibite.Dying = v
	}
	bibite.StomachContents = parseStomachContents(entryName, id, hasID, body)
	bibite.Children = parseChildLinks(entryName, id, hasID, body)
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
	}
	parseEntityBrain(entry.Name, "egg", ownerID, raw, &egg.BrainNodes, &egg.BrainSynapses)
	return egg
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
