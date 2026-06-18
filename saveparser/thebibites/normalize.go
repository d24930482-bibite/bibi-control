package thebibites

import (
	"fmt"
	"sort"
)

func ExtractTables(saveID string, archive *Archive) ExtractedSave {
	var out ExtractedSave
	if archive == nil {
		return out
	}

	out.Archive = SaveArchiveRow{
		SaveID:     saveID,
		SourcePath: archive.SourcePath,
		FileName:   archive.FileName,
		SizeBytes:  archive.Size,
		SHA256:     archive.SHA256,
	}
	for _, entry := range archive.Entries {
		out.Entries = append(out.Entries, SaveEntryRow{
			SaveID:           saveID,
			EntryIndex:       entry.Index,
			EntryName:        entry.Name,
			Kind:             entry.Kind,
			SHA256:           entry.SHA256,
			CompressedSize:   entry.CompressedSize,
			UncompressedSize: entry.UncompressedSize,
			HasUTF8BOM:       entry.HasUTF8BOM,
		})
	}
	for _, diagnostic := range archive.Diagnostics {
		out.Diagnostics = append(out.Diagnostics, DiagnosticRow{
			SaveID:    saveID,
			EntryName: diagnostic.Entry,
			Severity:  diagnostic.Severity,
			Code:      diagnostic.Code,
			Message:   diagnostic.Message,
		})
	}

	normalizeScene(saveID, archive, &out)
	normalizeSettings(saveID, archive.Settings, &out)
	normalizeSpecies(saveID, archive.Species, &out)
	normalizeBibites(saveID, archive.Bibites, &out)
	normalizeEggs(saveID, archive.Eggs, &out)
	normalizeEnvironment(saveID, archive, &out)
	return out
}

func normalizeScene(saveID string, archive *Archive, out *ExtractedSave) {
	if archive.Scene != nil {
		scene := archive.Scene
		out.Scene = &SceneRow{
			SaveID:              saveID,
			EntryName:           scene.EntryName,
			Version:             scene.Version,
			SimulatedTime:       scene.SimulatedTime,
			HasSimulatedTime:    scene.HasTime,
			ReportedNBibites:    scene.NBibites,
			HasReportedNBibites: scene.HasNBibites,
			ReportedNPellets:    scene.NPellets,
			HasReportedNPellets: scene.HasNPellets,
			ParsedBibites:       archive.Counts.ParsedBibites,
			ParsedEggs:          archive.Counts.ParsedEggs,
			AliveBibites:        archive.Counts.AliveBibites,
			DeadBibites:         archive.Counts.DeadBibites,
			DyingBibites:        archive.Counts.DyingBibites,
			ParsedPellets:       archive.Counts.Pellets,
		}
		if values, ok := listAt(scene.Raw, "colorSelectors"); ok {
			for i, value := range values {
				out.SceneColorSelectors = append(out.SceneColorSelectors, SceneColorSelectorRow{
					SaveID:             saveID,
					EntryName:          scene.EntryName,
					ColorSelectorIndex: i,
					RawJSON:            rawJSON(value),
				})
			}
		}
		appendSceneTowerRows(saveID, scene.EntryName, "phero", scene.Raw, "pheroTowers", &out.ScenePheroTowers)
		appendSceneTowerRows(saveID, scene.EntryName, "rad", scene.Raw, "radTowers", &out.SceneRadTowers)
	}
	if archive.Vars != nil {
		vars := archive.Vars
		row := VarsRow{
			SaveID:    saveID,
			EntryName: vars.EntryName,
		}
		if v, ok := intAt(vars.Raw, "towerMaxID"); ok {
			row.TowerMaxID = v
			row.HasTowerMaxID = true
		}
		out.Vars = &row
	}
}

func appendSceneTowerRows(saveID, entryName, towerKind string, raw map[string]any, key string, out *[]SceneTowerRow) {
	values, ok := listAt(raw, key)
	if !ok {
		return
	}
	for i, value := range values {
		*out = append(*out, SceneTowerRow{
			SaveID:     saveID,
			EntryName:  entryName,
			TowerKind:  towerKind,
			TowerIndex: i,
			RawJSON:    rawJSON(value),
		})
	}
}

func normalizeSettings(saveID string, settings *SettingsState, out *ExtractedSave) {
	if settings == nil {
		return
	}
	out.SettingsSimulationValues = appendSettingValueRows(out.SettingsSimulationValues, saveID, settings.EntryName, settings.SimulationValues)
	out.SettingsIndependentValues = appendSettingValueRows(out.SettingsIndependentValues, saveID, settings.EntryName, settings.IndependentValues)

	for i, material := range settings.Materials {
		out.SettingsMaterials = append(out.SettingsMaterials, SettingsMaterialRow{
			SaveID:        saveID,
			EntryName:     settings.EntryName,
			MaterialIndex: i,
			MaterialName:  material.Name,
			RawJSON:       rawJSON(material.Raw),
		})
		out.SettingsMaterialValues = appendSettingValueRows(out.SettingsMaterialValues, saveID, settings.EntryName, material.Values)
	}
	for _, zone := range settings.Zones {
		zoneRow := SettingsZoneRow{
			SaveID:    saveID,
			EntryName: settings.EntryName,
			ZoneIndex: zone.Index,
			ZoneID:    zone.ID,
			HasZoneID: zone.HasID,
			RawJSON:   rawJSON(zone.Raw),
		}
		populateSQLRefFields(&zoneRow, zone.Raw, "settings_zones")
		out.SettingsZones = append(out.SettingsZones, zoneRow)
		for _, geometry := range zone.Geometry {
			out.SettingsZoneGeometry = append(out.SettingsZoneGeometry, SettingsZoneGeometryRow{
				SaveID:           saveID,
				EntryName:        settings.EntryName,
				ZoneIndex:        zone.Index,
				ZoneID:           zone.ID,
				HasZoneID:        zone.HasID,
				GeometryIndex:    geometry.Index,
				GeometryKind:     geometry.GeometryKind,
				PositionX:        geometry.PositionX,
				PositionY:        geometry.PositionY,
				Radius:           geometry.Radius,
				RadiusIsRelative: geometry.RadiusIsRelative,
				RawJSON:          geometry.RawJSON,
			})
		}
		out.SettingsZoneValues = appendSettingValueRows(out.SettingsZoneValues, saveID, settings.EntryName, zone.Values)
	}
	for _, group := range settings.ZoneGroups {
		out.SettingsZoneGroups = append(out.SettingsZoneGroups, SettingsZoneGroupRow{
			SaveID:     saveID,
			EntryName:  settings.EntryName,
			GroupIndex: group.Index,
			Name:       group.Name,
			RawJSON:    rawJSON(group.Raw),
		})
	}
	for _, spawner := range settings.BibiteSpawners {
		out.SettingsBibiteSpawners = append(out.SettingsBibiteSpawners, SettingsBibiteSpawnerRow{
			SaveID:         saveID,
			EntryName:      settings.EntryName,
			SpawnerIndex:   spawner.Index,
			Path:           spawner.Path,
			SpawnPriority:  spawner.SpawnPriority,
			Minimum:        spawner.Minimum,
			RandomizeGenes: spawner.RandomizeGenes,
			GrowthAtSpawn:  spawner.GrowthAtSpawn,
			Tagging:        spawner.Tagging,
			SpawnType:      spawner.SpawnType,
			TotalSpawned:   spawner.TotalSpawned,
			RawJSON:        rawJSON(spawner.Raw),
		})
	}
	for _, changer := range settings.SettingsChangers {
		out.SettingsChangers = append(out.SettingsChangers, SettingsChangerRow{
			SaveID:       saveID,
			EntryName:    settings.EntryName,
			ChangerIndex: changer.Index,
			Name:         changer.Name,
			Repeat:       changer.Repeat,
			Start:        changer.Start,
			RawJSON:      rawJSON(changer.Raw),
		})
		for _, point := range changer.Points {
			out.SettingsChangerPoints = append(out.SettingsChangerPoints, SettingsChangerPointRow{
				SaveID:       saveID,
				EntryName:    settings.EntryName,
				ChangerIndex: changer.Index,
				PointIndex:   point.Index,
				T:            point.T,
				Y:            point.Y,
				D:            point.D,
				F:            point.F,
			})
		}
		for _, target := range changer.Targets {
			out.SettingsChangerTargets = append(out.SettingsChangerTargets, SettingsChangerTargetRow{
				SaveID:       saveID,
				EntryName:    settings.EntryName,
				ChangerIndex: changer.Index,
				TargetKey:    target.TargetKey,
				Scope:        target.Scope,
				ZoneIndex:    target.ZoneIndex,
				ZoneID:       target.ZoneID,
				HasZoneID:    target.HasZoneID,
				SettingName:  target.SettingName,
				Type:         target.Type,
				NumberValue:  target.NumberValue,
				StringValue:  target.StringValue,
				BoolValue:    target.BoolValue,
				RawJSON:      target.RawJSON,
			})
		}
	}
}

func appendSettingValueRows(rows []SettingValueRow, saveID, entryName string, values []SettingValue) []SettingValueRow {
	for _, value := range values {
		rows = append(rows, SettingValueRow{
			SaveID:         saveID,
			EntryName:      entryName,
			Scope:          value.Scope,
			OwnerKind:      value.OwnerKind,
			OwnerID:        value.OwnerID,
			SettingName:    value.Name,
			Path:           value.Path,
			Type:           value.Type,
			NumberValue:    value.NumberValue,
			StringValue:    value.StringValue,
			BoolValue:      value.BoolValue,
			RawJSON:        value.RawJSON,
			WrapperRawJSON: value.WrapperRawJSON,
		})
	}
	return rows
}

func normalizeSpecies(saveID string, species *SpeciesData, out *ExtractedSave) {
	if species == nil {
		return
	}
	for i, speciesID := range species.ActiveSpeciesIDs {
		out.ActiveSpecies = append(out.ActiveSpecies, ActiveSpeciesRow{
			SaveID:             saveID,
			EntryName:          species.EntryName,
			ActiveSpeciesIndex: i,
			SpeciesID:          speciesID,
		})
	}
	for _, record := range species.Records {
		ownerID := ownerIDFromInt(record.SpeciesID, record.HasSpeciesID, fmt.Sprintf("%d", record.Index))
		out.Species = append(out.Species, SpeciesRow{
			SaveID:                    saveID,
			EntryName:                 species.EntryName,
			SpeciesIndex:              record.Index,
			SpeciesID:                 record.SpeciesID,
			HasSpeciesID:              record.HasSpeciesID,
			ParentID:                  record.ParentID,
			HasParentID:               record.HasParentID,
			GenerationOfFirstSpecimen: record.GenerationOfFirstSpecimen,
			TimeCreation:              record.TimeCreation,
			Favorite:                  record.Favorite,
			GenericName:               record.GenericName,
			SpecificName:              record.SpecificName,
			Description:               record.Description,
			TemplateVersion:           record.TemplateVersion,
		})
		if template, ok := mapAt(record.Raw, "template"); ok {
			if genes, ok := mapAt(template, "genes"); ok {
				appendGeneRowsFromMap(&out.SpeciesGenes, saveID, species.EntryName, "species_template", ownerID, "template.genes", genes)
			}
		}
		out.SpeciesBrainNodes = appendBrainNodeRows(out.SpeciesBrainNodes, saveID, record.TemplateBrainNodes)
		out.SpeciesBrainSynapses = appendBrainSynapseRows(out.SpeciesBrainSynapses, saveID, record.TemplateBrainSynapses)
	}
}

func normalizeBibites(saveID string, bibites []Bibite, out *ExtractedSave) {
	for _, bibite := range bibites {
		ownerID := ownerIDFromInt(bibite.ID, bibite.HasID, bibite.EntryName)
		row := BibiteRow{
			SaveID:    saveID,
			EntryName: bibite.EntryName,
			BodyID:    bibite.ID,
			HasBodyID: bibite.HasID,
		}
		populateSQLRefFields(&row, bibite.Raw, "bibites")
		out.Bibites = append(out.Bibites, row)
		if genes, ok := mapAt(bibite.Raw, "genes"); ok {
			appendGeneRowsFromEntityGenes(&out.BibiteGenes, saveID, bibite.EntryName, "bibite", ownerID, genes)
		}
		if body, ok := mapAt(bibite.Raw, "body"); ok {
			appendBibiteBodyRows(saveID, bibite, body, out)
		}
		for _, content := range bibite.StomachContents {
			out.BibiteStomachContents = append(out.BibiteStomachContents, StomachContentRow{
				SaveID:             saveID,
				EntryName:          content.EntryName,
				BodyID:             content.BibiteID,
				HasBodyID:          content.HasBibiteID,
				ContentIndex:       content.Index,
				Material:           content.Material,
				Amount:             content.Amount,
				AverageChunkAmount: content.AverageChunkAmount,
			})
		}
		for _, child := range bibite.Children {
			out.BibiteChildren = append(out.BibiteChildren, BibiteChildRow{
				SaveID:       saveID,
				EntryName:    child.EntryName,
				ParentBodyID: child.ParentID,
				HasParentID:  child.HasParentID,
				ChildIndex:   child.Index,
				ChildBodyID:  child.ChildID,
			})
		}
		out.BibiteBrainNodes = appendBrainNodeRows(out.BibiteBrainNodes, saveID, bibite.BrainNodes)
		out.BibiteBrainSynapses = appendBrainSynapseRows(out.BibiteBrainSynapses, saveID, bibite.BrainSynapses)
	}
}

func appendBibiteBodyRows(saveID string, bibite Bibite, body map[string]any, out *ExtractedSave) {
	bodyRow := BibiteBodyRow{SaveID: saveID, EntryName: bibite.EntryName, BodyID: bibite.ID, HasBodyID: bibite.HasID}
	populateSQLRefFields(&bodyRow, bibite.Raw, "bibite_body")
	out.BibiteBody = append(out.BibiteBody, bodyRow)

	if _, ok := mapAt(body, "mouth"); ok {
		mouthRow := BibiteMouthRow{SaveID: saveID, EntryName: bibite.EntryName, BodyID: bibite.ID, HasBodyID: bibite.HasID}
		populateSQLRefFields(&mouthRow, bibite.Raw, "bibite_mouth")
		out.BibiteMouth = append(out.BibiteMouth, mouthRow)
	}
	if _, ok := mapAt(body, "phero"); ok {
		pheroRow := BibitePheromoneEmitterRow{SaveID: saveID, EntryName: bibite.EntryName, BodyID: bibite.ID, HasBodyID: bibite.HasID}
		populateSQLRefFields(&pheroRow, bibite.Raw, "bibite_pheromone_emitters")
		out.BibitePheromoneEmitters = append(out.BibitePheromoneEmitters, pheroRow)
	}
	if _, ok := mapAt(body, "eggLayer"); ok {
		eggLayerRow := BibiteEggLayerRow{SaveID: saveID, EntryName: bibite.EntryName, BodyID: bibite.ID, HasBodyID: bibite.HasID}
		populateSQLRefFields(&eggLayerRow, bibite.Raw, "bibite_egg_layers")
		out.BibiteEggLayers = append(out.BibiteEggLayers, eggLayerRow)
	}
	if _, ok := mapAt(body, "control"); ok {
		controlRow := BibiteControlRow{SaveID: saveID, EntryName: bibite.EntryName, BodyID: bibite.ID, HasBodyID: bibite.HasID}
		populateSQLRefFields(&controlRow, bibite.Raw, "bibite_control")
		out.BibiteControl = append(out.BibiteControl, controlRow)
	}
}

func normalizeEggs(saveID string, eggs []Egg, out *ExtractedSave) {
	for _, egg := range eggs {
		ownerID := ownerIDFromInt(egg.ID, egg.HasID, egg.EntryName)
		eggRow := EggRow{
			SaveID:    saveID,
			EntryName: egg.EntryName,
			EggID:     egg.ID,
			HasEggID:  egg.HasID,
		}
		populateSQLRefFields(&eggRow, egg.Raw, "eggs")
		out.Eggs = append(out.Eggs, eggRow)
		if genes, ok := mapAt(egg.Raw, "genes"); ok {
			appendGeneRowsFromEntityGenes(&out.EggGenes, saveID, egg.EntryName, "egg", ownerID, genes)
		}
		out.EggBrainNodes = appendBrainNodeRows(out.EggBrainNodes, saveID, egg.BrainNodes)
		out.EggBrainSynapses = appendBrainSynapseRows(out.EggBrainSynapses, saveID, egg.BrainSynapses)
	}
}

func normalizeEnvironment(saveID string, archive *Archive, out *ExtractedSave) {
	if archive.PelletData != nil {
		for _, group := range archive.PelletData.Groups {
			out.PelletGroups = append(out.PelletGroups, PelletGroupRow{
				SaveID:      saveID,
				EntryName:   group.EntryName,
				GroupIndex:  group.Index,
				Zone:        group.Zone,
				PelletCount: group.Count,
			})
		}
		for _, pellet := range archive.PelletData.Pellets {
			pelletRow := PelletRow{
				SaveID:           saveID,
				EntryName:        pellet.EntryName,
				PelletIndex:      pellet.Index,
				GroupIndex:       pellet.GroupIndex,
				GroupPelletIndex: pellet.GroupPelletIndex,
				Zone:             pellet.Zone,
				HasMatterDecay:   pellet.HasMatterDecay,
			}
			populateSQLRefFields(&pelletRow, pellet.Raw, "pellets")
			out.Pellets = append(out.Pellets, pelletRow)
		}
	}
	for _, pheromone := range archive.Pheromones {
		pheromoneRow := PheromoneRow{
			SaveID:         saveID,
			EntryName:      pheromone.EntryName,
			PheromoneIndex: pheromone.Index,
			HeadingRawJSON: pheromone.HeadingRawJSON,
		}
		populateSQLRefFields(&pheromoneRow, pheromone.Raw, "pheromones")
		out.Pheromones = append(out.Pheromones, pheromoneRow)
	}
}

func appendGeneRowsFromEntityGenes(rows *[]GeneRow, saveID, entryName, ownerKind, ownerID string, genes map[string]any) {
	keys := sortedKeys(genes)
	for _, key := range keys {
		if key == "genes" {
			if nested, ok := asMap(genes[key]); ok {
				appendGeneRowsFromMap(rows, saveID, entryName, ownerKind, ownerID, "genes.genes", nested)
			}
			continue
		}
		appendGeneRow(rows, saveID, entryName, ownerKind, ownerID, key, "genes."+key, genes[key])
	}
}

func appendGeneRowsFromMap(rows *[]GeneRow, saveID, entryName, ownerKind, ownerID, pathPrefix string, genes map[string]any) {
	for _, key := range sortedKeys(genes) {
		path := key
		if pathPrefix != "" {
			path = pathPrefix + "." + key
		}
		appendGeneRow(rows, saveID, entryName, ownerKind, ownerID, key, path, genes[key])
	}
}

func appendGeneRow(rows *[]GeneRow, saveID, entryName, ownerKind, ownerID, geneName, path string, value any) {
	valueType, numberValue, stringValue, boolValue, raw, ok := scalarParts(value)
	if !ok {
		return
	}
	*rows = append(*rows, GeneRow{
		SaveID:      saveID,
		OwnerKind:   ownerKind,
		OwnerID:     ownerID,
		EntryName:   entryName,
		GeneName:    geneName,
		Path:        path,
		Type:        valueType,
		NumberValue: numberValue,
		BoolValue:   boolValue,
		StringValue: stringValue,
		RawJSON:     raw,
	})
}

func appendBrainNodeRows(rows []BrainNodeRow, saveID string, nodes []BrainNode) []BrainNodeRow {
	for _, node := range nodes {
		rows = append(rows, BrainNodeRow{
			SaveID:         saveID,
			OwnerKind:      node.OwnerKind,
			OwnerID:        node.OwnerID,
			EntryName:      node.EntryName,
			NodeRowIndex:   node.Index,
			NodeIndex:      node.NodeIndex,
			Innovation:     node.Innovation,
			Type:           node.Type,
			TypeName:       node.TypeName,
			Desc:           node.Desc,
			Archetype:      node.Archetype,
			BaseActivation: node.BaseActivation,
			Value:          node.Value,
			LastInput:      node.LastInput,
			LastOutput:     node.LastOutput,
		})
	}
	return rows
}

func appendBrainSynapseRows(rows []BrainSynapseRow, saveID string, synapses []BrainSynapse) []BrainSynapseRow {
	for _, synapse := range synapses {
		rows = append(rows, BrainSynapseRow{
			SaveID:          saveID,
			OwnerKind:       synapse.OwnerKind,
			OwnerID:         synapse.OwnerID,
			EntryName:       synapse.EntryName,
			SynapseRowIndex: synapse.Index,
			Innovation:      synapse.Innovation,
			NodeIn:          synapse.NodeIn,
			NodeOut:         synapse.NodeOut,
			Weight:          synapse.Weight,
			Enabled:         synapse.Enabled,
		})
	}
	return rows
}

func sortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
