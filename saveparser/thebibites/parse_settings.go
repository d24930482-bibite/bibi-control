package thebibites

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
)

var settingsChangerZoneTargetRE = regexp.MustCompile(`^Zone\(([0-9]+)\)\.(.+)$`)

var settingsTopLevelCollections = map[string]struct{}{
	"bibites":          {},
	"independents":     {},
	"materials":        {},
	"settingsChangers": {},
	"zoneGroups":       {},
	"zones":            {},
}

var settingsZoneStructuralKeys = map[string]struct{}{
	"distribution":     {},
	"geometry":         {},
	"id":               {},
	"material":         {},
	"name":             {},
	"posX":             {},
	"posY":             {},
	"radius":           {},
	"radiusIsRelative": {},
}

func parseSettings(ctx *parserContext, entry *Entry) *SettingsState {
	raw, ok := asMap(entry.JSON)
	if !ok {
		ctx.addDiagnostic(SeverityWarning, "settings_not_object", entry.Name, "settings JSON is not an object")
		return nil
	}
	settings := &SettingsState{
		EntryName: entry.Name,
		Raw:       raw,
	}
	settings.SimulationValues = parseSettingsValues(entry.Name, "simulation", "settings", "settings", "settings", raw, settingsTopLevelCollections)
	if independents, ok := mapAt(raw, "independents"); ok {
		settings.IndependentValues = parseSettingsValues(entry.Name, "independent", "settings_independent", "independents", "settings.independents", independents, nil)
	}
	settings.Materials = parseSettingsMaterials(entry.Name, raw)
	settings.Zones = parseSettingsZones(entry.Name, raw)
	settings.ZoneGroups = parseSettingsZoneGroups(entry.Name, raw)
	settings.BibiteSpawners = parseSettingsBibiteSpawners(entry.Name, raw)
	settings.SettingsChangers = parseSettingsChangers(entry.Name, raw, settings.Zones)
	return settings
}

func parseSettingsValues(entryName, scope, ownerKind, ownerID, pathPrefix string, values map[string]any, skip map[string]struct{}) []SettingValue {
	keys := make([]string, 0, len(values))
	for key := range values {
		if _, shouldSkip := skip[key]; shouldSkip {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]SettingValue, 0, len(keys))
	for _, key := range keys {
		path := key
		if pathPrefix != "" {
			path = pathPrefix + "." + key
		}
		value, ok := parseSettingValue(key, path, scope, ownerKind, ownerID, values[key])
		if ok {
			out = append(out, value)
		}
	}
	return out
}

func parseSettingValue(name, path, scope, ownerKind, ownerID string, value any) (SettingValue, bool) {
	actual := value
	wrapperRaw := ""
	if raw, ok := asMap(value); ok {
		wrapped, exists := raw["Value"]
		if !exists {
			return SettingValue{}, false
		}
		actual = wrapped
		wrapperRaw = rawJSON(value)
	}
	valueType, numberValue, stringValue, boolValue, raw, ok := scalarParts(actual)
	if !ok {
		return SettingValue{}, false
	}
	if wrapperRaw == "" {
		wrapperRaw = rawJSON(value)
	}
	return SettingValue{
		Name:           name,
		Path:           path,
		Scope:          scope,
		OwnerKind:      ownerKind,
		OwnerID:        ownerID,
		Type:           valueType,
		NumberValue:    numberValue,
		StringValue:    stringValue,
		BoolValue:      boolValue,
		RawJSON:        raw,
		WrapperRawJSON: wrapperRaw,
	}, true
}

func parseSettingsMaterials(entryName string, settings map[string]any) []SettingsMaterial {
	materials, ok := mapAt(settings, "materials")
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(materials))
	for key := range materials {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]SettingsMaterial, 0, len(keys))
	for _, key := range keys {
		raw, ok := asMap(materials[key])
		if !ok {
			continue
		}
		out = append(out, SettingsMaterial{
			Name:   key,
			Raw:    raw,
			Values: parseSettingsValues(entryName, "material", "settings_material", key, "settings.materials."+key, raw, nil),
		})
	}
	return out
}

func parseSettingsZones(entryName string, settings map[string]any) []SettingsZone {
	values, ok := listAt(settings, "zones")
	if !ok {
		return nil
	}
	zones := make([]SettingsZone, 0, len(values))
	for i, value := range values {
		raw, ok := asMap(value)
		if !ok {
			continue
		}
		zone := SettingsZone{
			Index: i,
			Raw:   raw,
		}
		if v, ok := intAt(raw, "id"); ok {
			zone.ID = v
			zone.HasID = true
		}
		ownerID := ownerIDFromInt(zone.ID, zone.HasID, fmt.Sprintf("%d", i))
		zone.Geometry = parseSettingsZoneGeometry(raw)
		zone.Values = parseSettingsValues(entryName, "zone", "settings_zone", ownerID, fmt.Sprintf("settings.zones[%d]", i), raw, settingsZoneStructuralKeys)
		zones = append(zones, zone)
	}
	return zones
}

func parseSettingsZoneGeometry(zone map[string]any) []SettingsZoneGeometry {
	if values, ok := listAt(zone, "geometry"); ok {
		out := make([]SettingsZoneGeometry, 0, len(values))
		for i, value := range values {
			raw, ok := asMap(value)
			if !ok {
				continue
			}
			out = append(out, parseSettingsZoneGeometryObject(i, raw))
		}
		return out
	}

	if _, ok := zone["posX"]; !ok {
		if _, ok := zone["posY"]; !ok {
			if _, ok := zone["radius"]; !ok {
				if _, ok := zone["radiusIsRelative"]; !ok {
					return nil
				}
			}
		}
	}

	raw := make(map[string]any)
	for _, key := range []string{"posX", "posY", "radius", "radiusIsRelative"} {
		if value, ok := zone[key]; ok {
			raw[key] = value
		}
	}
	geometry := parseSettingsZoneGeometryObject(0, raw)
	if geometry.GeometryKind == "" {
		geometry.GeometryKind = "circle"
	}
	return []SettingsZoneGeometry{geometry}
}

func parseSettingsZoneGeometryObject(index int, raw map[string]any) SettingsZoneGeometry {
	geometry := SettingsZoneGeometry{
		Index:   index,
		RawJSON: rawJSON(raw),
	}
	if v, ok := stringAt(raw, "kind"); ok {
		geometry.GeometryKind = v
	} else if v, ok := stringAt(raw, "type"); ok {
		geometry.GeometryKind = v
	}
	if v, ok := floatAt(raw, "posX"); ok {
		geometry.PositionX = v
	} else if v, ok := floatAt(raw, "x"); ok {
		geometry.PositionX = v
	}
	if v, ok := floatAt(raw, "posY"); ok {
		geometry.PositionY = v
	} else if v, ok := floatAt(raw, "y"); ok {
		geometry.PositionY = v
	}
	if v, ok := floatAt(raw, "radius"); ok {
		geometry.Radius = v
	}
	if v, ok := boolAt(raw, "radiusIsRelative"); ok {
		geometry.RadiusIsRelative = v
	}
	return geometry
}

func parseSettingsZoneGroups(entryName string, settings map[string]any) []SettingsZoneGroup {
	values, ok := listAt(settings, "zoneGroups")
	if !ok {
		return nil
	}
	groups := make([]SettingsZoneGroup, 0, len(values))
	for i, value := range values {
		raw, ok := asMap(value)
		if !ok {
			continue
		}
		group := SettingsZoneGroup{
			Index: i,
			Raw:   raw,
		}
		if v, ok := stringAt(raw, "name"); ok {
			group.Name = v
		}
		groups = append(groups, group)
	}
	return groups
}

func parseSettingsBibiteSpawners(entryName string, settings map[string]any) []SettingsBibiteSpawner {
	values, ok := listAt(settings, "bibites")
	if !ok {
		return nil
	}
	spawners := make([]SettingsBibiteSpawner, 0, len(values))
	for i, value := range values {
		raw, ok := asMap(value)
		if !ok {
			continue
		}
		spawner := SettingsBibiteSpawner{
			Index: i,
			Raw:   raw,
		}
		if v, ok := stringAt(raw, "path"); ok {
			spawner.Path = v
		}
		if v, ok := floatAt(raw, "spawnPriority"); ok {
			spawner.SpawnPriority = v
		}
		if v, ok := floatAt(raw, "minimum"); ok {
			spawner.Minimum = v
		}
		if v, ok := stringAt(raw, "randomizeGenes"); ok {
			spawner.RandomizeGenes = v
		}
		if v, ok := stringAt(raw, "growthAtSpawn"); ok {
			spawner.GrowthAtSpawn = v
		}
		if v, ok := stringAt(raw, "tagging"); ok {
			spawner.Tagging = v
		}
		if v, ok := stringAt(raw, "spawnType"); ok {
			spawner.SpawnType = v
		}
		if v, ok := intAt(raw, "totalSpawned"); ok {
			spawner.TotalSpawned = v
		}
		spawners = append(spawners, spawner)
	}
	return spawners
}

func parseSettingsChangers(entryName string, settings map[string]any, zones []SettingsZone) []SettingsChanger {
	values, ok := listAt(settings, "settingsChangers")
	if !ok {
		return nil
	}
	zoneIDs := make(map[int]SettingsZone, len(zones))
	for _, zone := range zones {
		zoneIDs[zone.Index] = zone
	}
	changers := make([]SettingsChanger, 0, len(values))
	for i, value := range values {
		raw, ok := asMap(value)
		if !ok {
			continue
		}
		changer := SettingsChanger{
			Index: i,
			Raw:   raw,
		}
		if v, ok := stringAt(raw, "name"); ok {
			changer.Name = v
		}
		if v, ok := boolAt(raw, "repeat"); ok {
			changer.Repeat = v
		}
		if v, ok := floatAt(raw, "start"); ok {
			changer.Start = v
		}
		changer.Points = parseSettingsChangerPoints(raw)
		changer.Targets = parseSettingsChangerTargets(raw, zoneIDs)
		changers = append(changers, changer)
	}
	return changers
}

func parseSettingsChangerPoints(changer map[string]any) []SettingsChangerPoint {
	tValues, _ := listAt(changer, "t")
	yValues, _ := listAt(changer, "y")
	dValues, _ := listAt(changer, "d")
	fValues, _ := listAt(changer, "f")
	count := max(len(tValues), len(yValues), len(dValues), len(fValues))
	points := make([]SettingsChangerPoint, 0, count)
	for i := 0; i < count; i++ {
		point := SettingsChangerPoint{Index: i}
		if i < len(tValues) {
			if v, ok := toFloat(tValues[i]); ok {
				point.T = v
			}
		}
		if i < len(yValues) {
			if v, ok := toFloat(yValues[i]); ok {
				point.Y = v
			}
		}
		if i < len(dValues) {
			if v, ok := dValues[i].(string); ok {
				point.D = v
			}
		}
		if i < len(fValues) {
			if v, ok := toFloat(fValues[i]); ok {
				point.F = v
			}
		}
		points = append(points, point)
	}
	return points
}

func parseSettingsChangerTargets(changer map[string]any, zones map[int]SettingsZone) []SettingsChangerTarget {
	bases, ok := mapAt(changer, "settingsBases")
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(bases))
	for key := range bases {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	targets := make([]SettingsChangerTarget, 0, len(keys))
	for _, key := range keys {
		valueType, numberValue, stringValue, boolValue, raw, ok := scalarParts(bases[key])
		if !ok {
			continue
		}
		target := SettingsChangerTarget{
			TargetKey:   key,
			Scope:       "simulation",
			SettingName: key,
			Type:        valueType,
			NumberValue: numberValue,
			StringValue: stringValue,
			BoolValue:   boolValue,
			RawJSON:     raw,
		}
		if matches := settingsChangerZoneTargetRE.FindStringSubmatch(key); matches != nil {
			target.Scope = "zone"
			target.SettingName = matches[2]
			if zoneIndex, err := strconv.Atoi(matches[1]); err == nil {
				target.ZoneIndex = zoneIndex
				if zone, ok := zones[zoneIndex]; ok && zone.HasID {
					target.ZoneID = zone.ID
					target.HasZoneID = true
				}
			}
		}
		targets = append(targets, target)
	}
	return targets
}
