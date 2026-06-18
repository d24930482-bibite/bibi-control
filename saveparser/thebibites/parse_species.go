package thebibites

import "fmt"

func parseSpecies(ctx *parserContext, entry *Entry) *SpeciesData {
	raw, ok := asMap(entry.JSON)
	if !ok {
		ctx.addDiagnostic(SeverityWarning, "species_not_object", entry.Name, "species JSON is not an object")
		return nil
	}
	species := &SpeciesData{
		EntryName: entry.Name,
		Raw:       raw,
	}
	species.ActiveSpeciesIDs = parseActiveSpeciesIDs(raw)
	species.Records = parseSpeciesRecords(entry.Name, raw)
	return species
}

func parseActiveSpeciesIDs(species map[string]any) []int64 {
	values, ok := listAt(species, "activeSpeciesList")
	if !ok {
		return nil
	}
	ids := make([]int64, 0, len(values))
	for _, value := range values {
		if id, ok := toInt(value); ok {
			ids = append(ids, id)
		}
	}
	return ids
}

func parseSpeciesRecords(entryName string, species map[string]any) []SpeciesRecord {
	values, ok := listAt(species, "recordedSpecies")
	if !ok {
		return nil
	}
	records := make([]SpeciesRecord, 0, len(values))
	for i, value := range values {
		raw, ok := asMap(value)
		if !ok {
			continue
		}
		record := SpeciesRecord{
			Index: i,
			Raw:   raw,
		}
		if v, ok := intAt(raw, "speciesID"); ok {
			record.SpeciesID = v
			record.HasSpeciesID = true
		}
		ownerID := ownerIDFromInt(record.SpeciesID, record.HasSpeciesID, fmt.Sprintf("%d", i))
		if v, ok := intAt(raw, "parentID"); ok {
			record.ParentID = v
			record.HasParentID = true
		}
		if v, ok := intAt(raw, "generationOfFirstSpecimen"); ok {
			record.GenerationOfFirstSpecimen = v
		}
		if v, ok := floatAt(raw, "timeCreation"); ok {
			record.TimeCreation = v
		}
		if v, ok := boolAt(raw, "favorite"); ok {
			record.Favorite = v
		}
		if v, ok := stringAt(raw, "genericName"); ok {
			record.GenericName = v
		}
		if v, ok := stringAt(raw, "specificName"); ok {
			record.SpecificName = v
		}
		if v, ok := stringAt(raw, "description"); ok {
			record.Description = v
		}
		parseSpeciesTemplate(entryName, ownerID, raw, &record)
		records = append(records, record)
	}
	return records
}

func parseSpeciesTemplate(entryName, ownerID string, recordRaw map[string]any, record *SpeciesRecord) {
	template, ok := mapAt(recordRaw, "template")
	if !ok {
		return
	}
	if v, ok := stringAt(template, "version"); ok {
		record.TemplateVersion = v
	}
	if nodes, ok := listAt(template, "nodes"); ok {
		record.TemplateBrainNodes = parseBrainNodes(entryName, "species_template", ownerID, nodes)
	}
	if synapses, ok := listAt(template, "synapses"); ok {
		record.TemplateBrainSynapses = parseBrainSynapses(entryName, "species_template", ownerID, synapses)
	}
}
