package thebibites

import "fmt"

func parsePellets(ctx *parserContext, entry *Entry) *PelletData {
	raw, ok := asMap(entry.JSON)
	if !ok {
		ctx.addDiagnostic(SeverityWarning, "pellets_not_object", entry.Name, "pellets JSON is not an object")
		return nil
	}
	data := &PelletData{
		EntryName: entry.Name,
		Raw:       raw,
		Scalars:   collectScalars(entry.Name, "pellets", entry.Name, "pellets", raw),
	}
	pelletIndex := 0
	if groups, ok := listAt(raw, "pellets"); ok {
		for groupIndex, value := range groups {
			group, pellets := parsePelletGroup(entry.Name, groupIndex, value, pelletIndex)
			if group == nil {
				continue
			}
			pelletIndex += len(pellets)
			data.Groups = append(data.Groups, *group)
			data.Pellets = append(data.Pellets, pellets...)
		}
	}
	return data
}

func parsePelletGroup(entryName string, groupIndex int, value any, firstPelletIndex int) (*PelletGroup, []Pellet) {
	raw, ok := asMap(value)
	if !ok {
		return nil, nil
	}
	group := &PelletGroup{
		Index:     groupIndex,
		EntryName: entryName,
		Raw:       raw,
		Scalars:   collectScalars(entryName, "pellet_group", fmt.Sprintf("%d", groupIndex), fmt.Sprintf("pellets.groups[%d]", groupIndex), raw),
	}
	if v, ok := stringAt(raw, "zone"); ok {
		group.Zone = v
	}

	values, ok := listAt(raw, "pellets")
	if !ok {
		return group, nil
	}
	group.Count = len(values)
	pellets := make([]Pellet, 0, len(values))
	for groupPelletIndex, pelletValue := range values {
		pellet := parsePellet(entryName, group.Zone, groupIndex, groupPelletIndex, firstPelletIndex+len(pellets), pelletValue)
		if pellet == nil {
			continue
		}
		pellets = append(pellets, *pellet)
	}
	return group, pellets
}

func parsePellet(entryName, zone string, groupIndex, groupPelletIndex, pelletIndex int, value any) *Pellet {
	raw, ok := asMap(value)
	if !ok {
		return nil
	}
	ownerID := fmt.Sprintf("%d", pelletIndex)
	pellet := &Pellet{
		Index:            pelletIndex,
		GroupIndex:       groupIndex,
		GroupPelletIndex: groupPelletIndex,
		EntryName:        entryName,
		Zone:             zone,
		Raw:              raw,
		Scalars:          collectScalars(entryName, "pellet", ownerID, fmt.Sprintf("pellets.groups[%d].pellets[%d]", groupIndex, groupPelletIndex), raw),
	}
	if transform, ok := mapAt(raw, "transform"); ok {
		pellet.Transform = parseTransform(transform)
	}
	if rb2d, ok := mapAt(raw, "rb2d"); ok {
		pellet.RigidBody = parseRigidBody(rb2d)
	}
	if pelletState, ok := mapAt(raw, "pellet"); ok {
		if v, ok := stringAt(pelletState, "material"); ok {
			pellet.Material = v
		}
		if v, ok := floatAt(pelletState, "amount"); ok {
			pellet.Amount = v
		}
	}
	if matterDecay, ok := mapAt(raw, "matterDecay"); ok {
		pellet.HasMatterDecay = true
		if v, ok := floatAt(matterDecay, "timeAlive"); ok {
			pellet.MatterDecayTimeAlive = v
		}
		if v, ok := floatAt(matterDecay, "rotAmount"); ok {
			pellet.MatterDecayRotAmount = v
		}
	}
	return pellet
}

func parsePheromones(ctx *parserContext, entry *Entry) ([]Pheromone, []Scalar) {
	raw, ok := asMap(entry.JSON)
	if !ok {
		ctx.addDiagnostic(SeverityWarning, "pheromones_not_object", entry.Name, "pheromones JSON is not an object")
		return nil, nil
	}
	scalars := collectScalars(entry.Name, "pheromones", entry.Name, "pheromones", raw)
	values, ok := listAt(raw, "pheromones")
	if !ok {
		return nil, scalars
	}
	pheromones := make([]Pheromone, 0, len(values))
	for i, value := range values {
		itemRaw, ok := asMap(value)
		if !ok {
			continue
		}
		pheromone := Pheromone{
			Index:     i,
			EntryName: entry.Name,
			Raw:       itemRaw,
			Scalars:   collectScalars(entry.Name, "pheromone", fmt.Sprintf("%d", i), fmt.Sprintf("pheromones[%d]", i), itemRaw),
		}
		if transform, ok := mapAt(itemRaw, "transform"); ok {
			pheromone.Transform = parseTransform(transform)
		}
		if phero, ok := mapAt(itemRaw, "phero"); ok {
			if heading, ok := phero["heading"]; ok {
				pheromone.HeadingRawJSON = rawJSON(heading)
			}
			if v, ok := floatAt(phero, "Rstrength"); ok {
				pheromone.RStrength = v
			}
			if v, ok := floatAt(phero, "Gstrength"); ok {
				pheromone.GStrength = v
			}
			if v, ok := floatAt(phero, "Bstrength"); ok {
				pheromone.BStrength = v
			}
			if v, ok := floatAt(phero, "nR"); ok {
				pheromone.NR = v
			}
			if v, ok := floatAt(phero, "nG"); ok {
				pheromone.NG = v
			}
			if v, ok := floatAt(phero, "nB"); ok {
				pheromone.NB = v
			}
		}
		pheromones = append(pheromones, pheromone)
	}
	return pheromones, scalars
}
