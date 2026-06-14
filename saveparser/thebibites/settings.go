package thebibites

import (
	"fmt"
	"sort"
)

func parseSettings(ctx *parserContext, entry *Entry) *SettingsState {
	raw, ok := asMap(entry.JSON)
	if !ok {
		ctx.addDiagnostic(SeverityWarning, "settings_not_object", entry.Name, "settings JSON is not an object")
		return nil
	}
	settings := &SettingsState{
		EntryName: entry.Name,
		Raw:       raw,
		Scalars:   collectScalars(entry.Name, "settings", entry.Name, "settings", raw),
	}
	settings.Materials = parseSettingsMaterials(entry.Name, raw)
	settings.Zones = parseSettingsZones(entry.Name, raw)
	settings.BibiteSpawners = parseSettingsBibiteSpawners(entry.Name, raw)
	settings.SettingsChangers = parseSettingsChangers(entry.Name, raw)
	return settings
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
			Name:    key,
			Raw:     raw,
			Scalars: collectScalars(entryName, "settings_material", key, "settings.materials."+key, raw),
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
			Index:   i,
			Raw:     raw,
			Scalars: collectScalars(entryName, "settings_zone", fmt.Sprintf("%d", i), fmt.Sprintf("settings.zones[%d]", i), raw),
		}
		if v, ok := stringAt(raw, "name"); ok {
			zone.Name = v
		}
		if v, ok := intAt(raw, "id"); ok {
			zone.ID = v
			zone.HasID = true
		}
		if v, ok := stringAt(raw, "material"); ok {
			zone.Material = v
		}
		zones = append(zones, zone)
	}
	return zones
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
			Index:   i,
			Raw:     raw,
			Scalars: collectScalars(entryName, "settings_bibite_spawner", fmt.Sprintf("%d", i), fmt.Sprintf("settings.bibites[%d]", i), raw),
		}
		if v, ok := stringAt(raw, "path"); ok {
			spawner.Path = v
		}
		spawners = append(spawners, spawner)
	}
	return spawners
}

func parseSettingsChangers(entryName string, settings map[string]any) []SettingsChanger {
	values, ok := listAt(settings, "settingsChangers")
	if !ok {
		return nil
	}
	changers := make([]SettingsChanger, 0, len(values))
	for i, value := range values {
		raw, ok := asMap(value)
		if !ok {
			continue
		}
		changer := SettingsChanger{
			Index:   i,
			Raw:     raw,
			Scalars: collectScalars(entryName, "settings_changer", fmt.Sprintf("%d", i), fmt.Sprintf("settings.settingsChangers[%d]", i), raw),
		}
		if v, ok := stringAt(raw, "name"); ok {
			changer.Name = v
		}
		changers = append(changers, changer)
	}
	return changers
}
