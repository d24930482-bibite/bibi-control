package thebibites

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

func settingsValueColumnResolver(columns map[string]string) sqlRefResolver {
	return func(ref SQLValueRef) (Target, string, error) {
		return resolveSettingsValueColumn(ref, columns)
	}
}

func resolveSettingsValueColumn(ref SQLValueRef, columns map[string]string) (Target, string, error) {
	wantType, err := sqlRefColumnValue(ref, columns)
	if err != nil {
		return Target{}, "", unsupportedSQLValueRef(ref)
	}
	if err := requireSQLRefString(ref, ref.EntryName, "entry_name"); err != nil {
		return Target{}, "", err
	}
	if err := requireSQLRefString(ref, ref.Path, "path"); err != nil {
		return Target{}, "", err
	}
	if err := requireSQLRefString(ref, ref.SettingName, "setting_name"); err != nil {
		return Target{}, "", err
	}
	if err := requireSQLRefValueType(ref, wantType); err != nil {
		return Target{}, "", err
	}
	if err := requireSQLRefString(ref, ref.WrapperRawJSON, "wrapper_raw_json"); err != nil {
		return Target{}, "", err
	}

	path, zoneIndex, hasZoneIndex, err := settingsValueArchivePath(ref)
	if err != nil {
		return Target{}, "", err
	}
	wrapped, err := settingValueUsesWrapper(ref.WrapperRawJSON)
	if err != nil {
		return Target{}, "", fmt.Errorf("%s.%s wrapper_raw_json: %w", ref.Table, ref.Column, err)
	}
	if wrapped {
		path += ".Value"
	}

	var guards []Guard
	if hasZoneIndex {
		if ref.HasZoneIndex && ref.ZoneIndex != zoneIndex {
			return Target{}, "", fmt.Errorf("%s.%s zone_index = %d, want %d from path", ref.Table, ref.Column, ref.ZoneIndex, zoneIndex)
		}
		guards = zoneIDGuards(ref, zoneIndex)
	}
	return EntryTarget(ref.EntryName, tb.EntrySettings, guards...), path, nil
}

func settingsValueArchivePath(ref SQLValueRef) (path string, zoneIndex int, hasZoneIndex bool, err error) {
	if !safeSettingsPathSegment(ref.SettingName) {
		return "", 0, false, fmt.Errorf("%s.%s setting_name %q is not a safe path segment", ref.Table, ref.Column, ref.SettingName)
	}

	switch ref.Table {
	case "settings_simulation_values":
		path, err := settingsScopedValueArchivePath(ref, "settings", "settings", "settings.", "")
		if err != nil {
			return "", 0, false, err
		}
		return path, 0, false, nil

	case "settings_independent_values":
		path, err := settingsScopedValueArchivePath(ref, "settings_independent", "independents", "settings.independents.", "independents.")
		if err != nil {
			return "", 0, false, err
		}
		return path, 0, false, nil

	case "settings_material_values":
		if !safeSettingsPathSegment(ref.OwnerID) {
			return "", 0, false, fmt.Errorf("%s.%s owner_id %q is not a safe path segment", ref.Table, ref.Column, ref.OwnerID)
		}
		path, err := settingsScopedValueArchivePath(ref, "settings_material", ref.OwnerID, "settings.materials."+ref.OwnerID+".", "materials."+ref.OwnerID+".")
		if err != nil {
			return "", 0, false, err
		}
		return path, 0, false, nil

	case "settings_zone_values":
		if err := requireSQLRefEqual(ref, "owner_kind", ref.OwnerKind, "settings_zone"); err != nil {
			return "", 0, false, err
		}
		if err := requireSQLRefString(ref, ref.OwnerID, "owner_id"); err != nil {
			return "", 0, false, err
		}
		index, err := settingsZoneValuePathIndex(ref.Path, ref.SettingName)
		if err != nil {
			return "", 0, false, fmt.Errorf("%s.%s %w", ref.Table, ref.Column, err)
		}
		return fmt.Sprintf("zones[%d].%s", index, ref.SettingName), index, true, nil

	default:
		return "", 0, false, unsupportedSQLValueRef(ref)
	}
}

func settingsScopedValueArchivePath(ref SQLValueRef, ownerKind, ownerID, sqlPathPrefix, archivePathPrefix string) (string, error) {
	if err := requireSQLRefEqual(ref, "owner_kind", ref.OwnerKind, ownerKind); err != nil {
		return "", err
	}
	if err := requireSQLRefEqual(ref, "owner_id", ref.OwnerID, ownerID); err != nil {
		return "", err
	}
	expected := sqlPathPrefix + ref.SettingName
	if ref.Path != expected {
		return "", fmt.Errorf("%s.%s path = %q, want %q", ref.Table, ref.Column, ref.Path, expected)
	}
	return archivePathPrefix + ref.SettingName, nil
}

func settingsChangerTargetColumnResolver(columns map[string]string) sqlRefResolver {
	return func(ref SQLValueRef) (Target, string, error) {
		return resolveSettingsChangerTargetColumn(ref, columns)
	}
}

func resolveSettingsChangerTargetColumn(ref SQLValueRef, columns map[string]string) (Target, string, error) {
	wantType, err := sqlRefColumnValue(ref, columns)
	if err != nil {
		return Target{}, "", unsupportedSQLValueRef(ref)
	}
	if err := requireSQLRefString(ref, ref.EntryName, "entry_name"); err != nil {
		return Target{}, "", err
	}
	if err := requireSQLRefFlag(ref, ref.HasChangerIndex, "changer_index"); err != nil {
		return Target{}, "", err
	}
	if err := requireSQLRefString(ref, ref.TargetKey, "target_key"); err != nil {
		return Target{}, "", err
	}
	if err := requireSQLRefEqual(ref, "scope", ref.Scope, "zone"); err != nil {
		return Target{}, "", err
	}
	if err := requireSQLRefFlag(ref, ref.HasZoneIndex, "zone_index"); err != nil {
		return Target{}, "", err
	}
	if err := requireSQLRefString(ref, ref.SettingName, "setting_name"); err != nil {
		return Target{}, "", err
	}
	if err := requireSQLRefValueType(ref, wantType); err != nil {
		return Target{}, "", err
	}

	expectedKey := fmt.Sprintf("Zone(%d).%s", ref.ZoneIndex, ref.SettingName)
	if ref.TargetKey != expectedKey {
		return Target{}, "", fmt.Errorf("%s.%s target_key = %q, want %q", ref.Table, ref.Column, ref.TargetKey, expectedKey)
	}

	path := fmt.Sprintf("settingsChangers[%d].settingsBases[%s]", ref.ChangerIndex, strconv.Quote(ref.TargetKey))
	return EntryTarget(ref.EntryName, tb.EntrySettings, zoneIDGuards(ref, ref.ZoneIndex)...), path, nil
}

func settingValueUsesWrapper(raw string) (bool, error) {
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return false, err
	}
	if object, ok := value.(map[string]any); ok {
		if _, ok := object["Value"]; ok {
			return true, nil
		}
		return false, fmt.Errorf("object does not contain Value")
	}
	return false, nil
}

func settingsZoneValuePathIndex(path, settingName string) (int, error) {
	const prefix = "settings.zones["
	if !strings.HasPrefix(path, prefix) {
		return 0, fmt.Errorf("path = %q, want %sN].%s", path, prefix, settingName)
	}
	rest := strings.TrimPrefix(path, prefix)
	end := strings.IndexByte(rest, ']')
	if end < 0 {
		return 0, fmt.Errorf("path = %q has unterminated zone index", path)
	}
	rawIndex := rest[:end]
	index, err := strconv.Atoi(rawIndex)
	if err != nil || index < 0 {
		return 0, fmt.Errorf("path = %q has invalid zone index %q", path, rawIndex)
	}
	expectedSuffix := "]." + settingName
	if rest[end:] != expectedSuffix {
		return 0, fmt.Errorf("path = %q, want settings.zones[%d].%s", path, index, settingName)
	}
	return index, nil
}

func safeSettingsPathSegment(segment string) bool {
	return segment != "" && !strings.ContainsAny(segment, ".[]")
}

func settingsZoneColumnResolver(columns map[string]string) sqlRefResolver {
	return func(ref SQLValueRef) (Target, string, error) {
		return resolveSettingsZoneColumn(ref, columns)
	}
}

func resolveSettingsZoneColumn(ref SQLValueRef, columns map[string]string) (Target, string, error) {
	field, err := sqlRefColumnValue(ref, columns)
	if err != nil {
		return Target{}, "", err
	}
	if err := requireSQLRefString(ref, ref.EntryName, "entry_name"); err != nil {
		return Target{}, "", err
	}
	if err := requireSQLRefFlag(ref, ref.HasZoneIndex, "zone_index"); err != nil {
		return Target{}, "", err
	}
	return EntryTarget(ref.EntryName, tb.EntrySettings, zoneIDGuards(ref, ref.ZoneIndex)...), fmt.Sprintf("zones[%d].%s", ref.ZoneIndex, field), nil
}
