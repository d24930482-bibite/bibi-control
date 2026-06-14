package thebibites

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

// SQLValueRef identifies one normalized SQL cell well enough to resolve it
// back to a guarded archive JSON path. Only allowlisted table/column pairs are
// writable; normalized rows remain query projections, not editable state.
type SQLValueRef struct {
	Table  string
	Column string

	EntryName string

	BodyID    int64
	HasBodyID bool

	EggID    int64
	HasEggID bool

	OwnerKind string
	OwnerID   string
	Path      string

	SettingName    string
	ValueType      string
	WrapperRawJSON string

	ContentIndex    int
	HasContentIndex bool

	GroupIndex          int
	HasGroupIndex       bool
	GroupPelletIndex    int
	HasGroupPelletIndex bool
	Zone                string
	HasZone             bool
	PheromoneIndex      int
	HasPheromoneIndex   bool
	NodeRowIndex        int
	HasNodeRowIndex     bool
	SynapseRowIndex     int
	HasSynapseRowIndex  bool
	ZoneIndex           int
	HasZoneIndex        bool
	ZoneID              int64
	HasZoneID           bool
	Expected            any
	HasExpected         bool
}

// WithExpected adds a stale-value guard for the resolved JSON path.
func (r SQLValueRef) WithExpected(value any) SQLValueRef {
	r.Expected = value
	r.HasExpected = true
	return r
}

// StageSQLSet resolves ref into a guarded JSON set operation and stages it.
func (s *Session) StageSQLSet(ref SQLValueRef, value any) error {
	op, err := SQLSet(ref, value)
	if err != nil {
		return err
	}
	return s.Stage(op)
}

// SQLSet resolves ref into a guarded JSON set operation.
func SQLSet(ref SQLValueRef, value any) (Operation, error) {
	target, path, err := ResolveSQLValueRef(ref)
	if err != nil {
		return Operation{}, err
	}
	if ref.HasExpected {
		target.Guards = append(target.Guards, Require(path, ref.Expected))
	}
	return Set(target, path, value), nil
}

// ResolveSQLValueRef resolves a normalized SQL cell to an archive target and
// JSON path. Unsupported cells return an error instead of guessing.
func ResolveSQLValueRef(ref SQLValueRef) (Target, string, error) {
	if ref.Table == "" {
		return Target{}, "", fmt.Errorf("sql value ref table is required")
	}
	if ref.Column == "" {
		return Target{}, "", fmt.Errorf("sql value ref column is required")
	}

	switch ref.Table {
	case "bibites":
		return resolveBibiteColumn(ref, bibiteColumnPaths)
	case "bibite_body":
		return resolveBibiteColumn(ref, bibiteBodyColumnPaths)
	case "bibite_mouth":
		return resolveBibiteColumn(ref, bibiteMouthColumnPaths)
	case "bibite_pheromone_emitters":
		return resolveBibiteColumn(ref, bibitePheromoneColumnPaths)
	case "bibite_egg_layers":
		return resolveBibiteColumn(ref, bibiteEggLayerColumnPaths)
	case "bibite_control":
		return resolveBibiteColumn(ref, bibiteControlColumnPaths)
	case "bibite_stomach_contents":
		return resolveBibiteStomachColumn(ref)
	case "bibite_genes":
		return resolveGeneColumn(ref, tb.EntryBibite)
	case "bibite_brain_nodes":
		return resolveEntityBrainNodeColumn(ref, tb.EntryBibite)
	case "bibite_brain_synapses":
		return resolveEntityBrainSynapseColumn(ref, tb.EntryBibite)
	case "eggs":
		return resolveEggColumn(ref, eggColumnPaths)
	case "egg_genes":
		return resolveGeneColumn(ref, tb.EntryEgg)
	case "egg_brain_nodes":
		return resolveEntityBrainNodeColumn(ref, tb.EntryEgg)
	case "egg_brain_synapses":
		return resolveEntityBrainSynapseColumn(ref, tb.EntryEgg)
	case "pellets":
		return resolvePelletColumn(ref)
	case "pheromones":
		return resolvePheromoneColumn(ref)
	case "settings_simulation_values", "settings_independent_values", "settings_material_values", "settings_zone_values":
		return resolveSettingsValueColumn(ref)
	case "settings_zones":
		return resolveSettingsZoneColumn(ref)
	default:
		return Target{}, "", unsupportedSQLValueRef(ref)
	}
}

var bibiteColumnPaths = map[string]string{
	"species_id":           "genes.speciesID",
	"generation":           "genes.gen",
	"dead":                 "body.dead",
	"dying":                "body.dying",
	"health":               "body.health",
	"energy":               "body.energy",
	"time_alive":           "clock.timeAlive",
	"transform_position_x": "transform.position[0]",
	"transform_position_y": "transform.position[1]",
	"transform_rotation":   "transform.rotation",
	"transform_scale":      "transform.scale",
	"rb2d_px":              "rb2d.px",
	"rb2d_py":              "rb2d.py",
	"rb2d_vx":              "rb2d.vx",
	"rb2d_vy":              "rb2d.vy",
	"rb2d_r":               "rb2d.r",
}

var bibiteBodyColumnPaths = map[string]string{
	"d2_size":               "body.d2Size",
	"fat_reserves_amount":   "body.fatReservesAmount",
	"attacked_dmg":          "body.attackedDmg",
	"times_attacked":        "body.timesAttacked",
	"total_damage_suffered": "body.totalDamageSuffered",
	"brain_ticks_count":     "body.brainTicksCount",
	"vision_lookup_count":   "body.visionLookupCount",
	"vision_sensing_count":  "body.visionSensingCount",
	"corpse_energy_offset":  "body.corpseEnergyOffset",
}

var bibiteMouthColumnPaths = map[string]string{
	"attacked_last_frame": "body.mouth.attackedLastFrame",
	"bibites_bitten":      "body.mouth.bibitesBitten",
	"bite_progress":       "body.mouth.biteProgress",
	"murdered_area":       "body.mouth.murderedArea",
	"total_damage_dealt":  "body.mouth.totalDamageDealt",
	"total_murders":       "body.mouth.totalMurders",
}

var bibitePheromoneColumnPaths = map[string]string{
	"progress": "body.phero.progress",
}

var bibiteEggLayerColumnPaths = map[string]string{
	"egg_progress": "body.eggLayer.eggProgress",
	"n_eggs_laid":  "body.eggLayer.nEggsLaid",
}

var bibiteControlColumnPaths = map[string]string{
	"total_travel": "body.control.totalTravel",
}

var eggColumnPaths = map[string]string{
	"species_id":           "genes.speciesID",
	"generation":           "genes.gen",
	"hatch_progress":       "egg.hatchProgress",
	"energy":               "egg.energy",
	"transform_position_x": "transform.position[0]",
	"transform_position_y": "transform.position[1]",
	"transform_rotation":   "transform.rotation",
	"transform_scale":      "transform.scale",
	"rb2d_px":              "rb2d.px",
	"rb2d_py":              "rb2d.py",
	"rb2d_vx":              "rb2d.vx",
	"rb2d_vy":              "rb2d.vy",
	"rb2d_r":               "rb2d.r",
}

var brainNodeColumnKeys = map[string]string{
	"node_index":      "Index",
	"innovation":      "Inov",
	"node_type":       "Type",
	"type_name":       "TypeName",
	"node_desc":       "Desc",
	"archetype":       "archetype",
	"base_activation": "baseActivation",
	"value":           "Value",
	"last_input":      "LastInput",
	"last_output":     "LastOutput",
}

var brainSynapseColumnKeys = map[string]string{
	"innovation": "Inov",
	"node_in":    "NodeIn",
	"node_out":   "NodeOut",
	"weight":     "Weight",
	"enabled":    "En",
}

func resolveBibiteColumn(ref SQLValueRef, paths map[string]string) (Target, string, error) {
	path, ok := paths[ref.Column]
	if !ok {
		return Target{}, "", unsupportedSQLValueRef(ref)
	}
	target, err := bibiteTargetFromSQLRef(ref)
	if err != nil {
		return Target{}, "", err
	}
	return target, path, nil
}

func resolveBibiteStomachColumn(ref SQLValueRef) (Target, string, error) {
	field, ok := map[string]string{
		"material":             "material",
		"amount":               "amount",
		"average_chunk_amount": "averageChunkAmount",
	}[ref.Column]
	if !ok {
		return Target{}, "", unsupportedSQLValueRef(ref)
	}
	if !ref.HasContentIndex {
		return Target{}, "", fmt.Errorf("%s.%s requires content_index", ref.Table, ref.Column)
	}
	target, err := bibiteTargetFromSQLRef(ref)
	if err != nil {
		return Target{}, "", err
	}
	return target, fmt.Sprintf("body.stomach.content[%d].%s", ref.ContentIndex, field), nil
}

func resolveGeneColumn(ref SQLValueRef, kind tb.EntryKind) (Target, string, error) {
	switch ref.Column {
	case "number_value", "bool_value", "string_value":
	default:
		return Target{}, "", unsupportedSQLValueRef(ref)
	}
	if ref.Path == "" {
		return Target{}, "", fmt.Errorf("%s.%s requires path", ref.Table, ref.Column)
	}

	switch kind {
	case tb.EntryBibite:
		target, err := bibiteTargetFromGeneRef(ref)
		if err != nil {
			return Target{}, "", err
		}
		return target, ref.Path, nil
	case tb.EntryEgg:
		target, err := eggTargetFromGeneRef(ref)
		if err != nil {
			return Target{}, "", err
		}
		return target, ref.Path, nil
	default:
		return Target{}, "", unsupportedSQLValueRef(ref)
	}
}

func resolveEntityBrainNodeColumn(ref SQLValueRef, kind tb.EntryKind) (Target, string, error) {
	key, ok := brainNodeColumnKeys[ref.Column]
	if !ok {
		return Target{}, "", unsupportedSQLValueRef(ref)
	}
	if !ref.HasNodeRowIndex {
		return Target{}, "", fmt.Errorf("%s.%s requires node_row_index", ref.Table, ref.Column)
	}
	target, err := entityTargetFromSQLRef(ref, kind)
	if err != nil {
		return Target{}, "", err
	}
	return target, fmt.Sprintf("brain.Nodes[%d].%s", ref.NodeRowIndex, key), nil
}

func resolveEntityBrainSynapseColumn(ref SQLValueRef, kind tb.EntryKind) (Target, string, error) {
	key, ok := brainSynapseColumnKeys[ref.Column]
	if !ok {
		return Target{}, "", unsupportedSQLValueRef(ref)
	}
	if !ref.HasSynapseRowIndex {
		return Target{}, "", fmt.Errorf("%s.%s requires synapse_row_index", ref.Table, ref.Column)
	}
	target, err := entityTargetFromSQLRef(ref, kind)
	if err != nil {
		return Target{}, "", err
	}
	return target, fmt.Sprintf("brain.Synapses[%d].%s", ref.SynapseRowIndex, key), nil
}

func resolveEggColumn(ref SQLValueRef, paths map[string]string) (Target, string, error) {
	path, ok := paths[ref.Column]
	if !ok {
		return Target{}, "", unsupportedSQLValueRef(ref)
	}
	target, err := eggTargetFromSQLRef(ref)
	if err != nil {
		return Target{}, "", err
	}
	return target, path, nil
}

func resolvePelletColumn(ref SQLValueRef) (Target, string, error) {
	field, ok := map[string]string{
		"material":                "pellet.material",
		"amount":                  "pellet.amount",
		"matter_decay_time_alive": "matterDecay.timeAlive",
		"matter_decay_rot_amount": "matterDecay.rotAmount",
		"transform_position_x":    "transform.position[0]",
		"transform_position_y":    "transform.position[1]",
		"transform_rotation":      "transform.rotation",
		"transform_scale":         "transform.scale",
		"rb2d_px":                 "rb2d.px",
		"rb2d_py":                 "rb2d.py",
		"rb2d_vx":                 "rb2d.vx",
		"rb2d_vy":                 "rb2d.vy",
		"rb2d_r":                  "rb2d.r",
	}[ref.Column]
	if !ok {
		return Target{}, "", unsupportedSQLValueRef(ref)
	}
	if ref.EntryName == "" {
		return Target{}, "", fmt.Errorf("%s.%s requires entry_name", ref.Table, ref.Column)
	}
	if !ref.HasGroupIndex {
		return Target{}, "", fmt.Errorf("%s.%s requires group_index", ref.Table, ref.Column)
	}
	if !ref.HasGroupPelletIndex {
		return Target{}, "", fmt.Errorf("%s.%s requires group_pellet_index", ref.Table, ref.Column)
	}
	guards := make([]Guard, 0, 1)
	if ref.HasZone {
		guards = append(guards, Require(fmt.Sprintf("pellets[%d].zone", ref.GroupIndex), ref.Zone))
	}
	path := fmt.Sprintf("pellets[%d].pellets[%d].%s", ref.GroupIndex, ref.GroupPelletIndex, field)
	return EntryTarget(ref.EntryName, tb.EntryPellets, guards...), path, nil
}

func resolvePheromoneColumn(ref SQLValueRef) (Target, string, error) {
	field, ok := map[string]string{
		"transform_position_x": "transform.position[0]",
		"transform_position_y": "transform.position[1]",
		"transform_rotation":   "transform.rotation",
		"transform_scale":      "transform.scale",
		"r_strength":           "phero.Rstrength",
		"g_strength":           "phero.Gstrength",
		"b_strength":           "phero.Bstrength",
		"nr":                   "phero.nR",
		"ng":                   "phero.nG",
		"nb":                   "phero.nB",
	}[ref.Column]
	if !ok {
		return Target{}, "", unsupportedSQLValueRef(ref)
	}
	if ref.EntryName == "" {
		return Target{}, "", fmt.Errorf("%s.%s requires entry_name", ref.Table, ref.Column)
	}
	if !ref.HasPheromoneIndex {
		return Target{}, "", fmt.Errorf("%s.%s requires pheromone_index", ref.Table, ref.Column)
	}
	return EntryTarget(ref.EntryName, tb.EntryPheromones), fmt.Sprintf("pheromones[%d].%s", ref.PheromoneIndex, field), nil
}

func resolveSettingsValueColumn(ref SQLValueRef) (Target, string, error) {
	wantType, ok := map[string]string{
		"number_value": string(tb.ScalarNumber),
		"string_value": string(tb.ScalarString),
		"bool_value":   string(tb.ScalarBool),
	}[ref.Column]
	if !ok {
		return Target{}, "", unsupportedSQLValueRef(ref)
	}
	if ref.EntryName == "" {
		return Target{}, "", fmt.Errorf("%s.%s requires entry_name", ref.Table, ref.Column)
	}
	if ref.Path == "" {
		return Target{}, "", fmt.Errorf("%s.%s requires path", ref.Table, ref.Column)
	}
	if ref.SettingName == "" {
		return Target{}, "", fmt.Errorf("%s.%s requires setting_name", ref.Table, ref.Column)
	}
	if ref.ValueType == "" {
		return Target{}, "", fmt.Errorf("%s.%s requires value_type", ref.Table, ref.Column)
	}
	if ref.ValueType != wantType {
		return Target{}, "", fmt.Errorf("%s.%s value_type = %q, want %q", ref.Table, ref.Column, ref.ValueType, wantType)
	}
	if ref.WrapperRawJSON == "" {
		return Target{}, "", fmt.Errorf("%s.%s requires wrapper_raw_json", ref.Table, ref.Column)
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

	guards := make([]Guard, 0, 1)
	if hasZoneIndex {
		if ref.HasZoneIndex && ref.ZoneIndex != zoneIndex {
			return Target{}, "", fmt.Errorf("%s.%s zone_index = %d, want %d from path", ref.Table, ref.Column, ref.ZoneIndex, zoneIndex)
		}
		if ref.HasZoneID {
			guards = append(guards, Require(fmt.Sprintf("zones[%d].id", zoneIndex), ref.ZoneID))
		}
	}
	return EntryTarget(ref.EntryName, tb.EntrySettings, guards...), path, nil
}

func settingsValueArchivePath(ref SQLValueRef) (path string, zoneIndex int, hasZoneIndex bool, err error) {
	if !safeSettingsPathSegment(ref.SettingName) {
		return "", 0, false, fmt.Errorf("%s.%s setting_name %q is not a safe path segment", ref.Table, ref.Column, ref.SettingName)
	}

	switch ref.Table {
	case "settings_simulation_values":
		if ref.OwnerKind != "settings" {
			return "", 0, false, fmt.Errorf("%s.%s owner_kind = %q, want settings", ref.Table, ref.Column, ref.OwnerKind)
		}
		if ref.OwnerID != "settings" {
			return "", 0, false, fmt.Errorf("%s.%s owner_id = %q, want settings", ref.Table, ref.Column, ref.OwnerID)
		}
		expected := "settings." + ref.SettingName
		if ref.Path != expected {
			return "", 0, false, fmt.Errorf("%s.%s path = %q, want %q", ref.Table, ref.Column, ref.Path, expected)
		}
		return ref.SettingName, 0, false, nil

	case "settings_independent_values":
		if ref.OwnerKind != "settings_independent" {
			return "", 0, false, fmt.Errorf("%s.%s owner_kind = %q, want settings_independent", ref.Table, ref.Column, ref.OwnerKind)
		}
		if ref.OwnerID != "independents" {
			return "", 0, false, fmt.Errorf("%s.%s owner_id = %q, want independents", ref.Table, ref.Column, ref.OwnerID)
		}
		expected := "settings.independents." + ref.SettingName
		if ref.Path != expected {
			return "", 0, false, fmt.Errorf("%s.%s path = %q, want %q", ref.Table, ref.Column, ref.Path, expected)
		}
		return "independents." + ref.SettingName, 0, false, nil

	case "settings_material_values":
		if ref.OwnerKind != "settings_material" {
			return "", 0, false, fmt.Errorf("%s.%s owner_kind = %q, want settings_material", ref.Table, ref.Column, ref.OwnerKind)
		}
		if !safeSettingsPathSegment(ref.OwnerID) {
			return "", 0, false, fmt.Errorf("%s.%s owner_id %q is not a safe path segment", ref.Table, ref.Column, ref.OwnerID)
		}
		expected := "settings.materials." + ref.OwnerID + "." + ref.SettingName
		if ref.Path != expected {
			return "", 0, false, fmt.Errorf("%s.%s path = %q, want %q", ref.Table, ref.Column, ref.Path, expected)
		}
		return "materials." + ref.OwnerID + "." + ref.SettingName, 0, false, nil

	case "settings_zone_values":
		if ref.OwnerKind != "settings_zone" {
			return "", 0, false, fmt.Errorf("%s.%s owner_kind = %q, want settings_zone", ref.Table, ref.Column, ref.OwnerKind)
		}
		if ref.OwnerID == "" {
			return "", 0, false, fmt.Errorf("%s.%s requires owner_id", ref.Table, ref.Column)
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

func resolveSettingsZoneColumn(ref SQLValueRef) (Target, string, error) {
	field, ok := map[string]string{
		"name":         "name",
		"material":     "material",
		"distribution": "distribution",
	}[ref.Column]
	if !ok {
		return Target{}, "", unsupportedSQLValueRef(ref)
	}
	if ref.EntryName == "" {
		return Target{}, "", fmt.Errorf("%s.%s requires entry_name", ref.Table, ref.Column)
	}
	if !ref.HasZoneIndex {
		return Target{}, "", fmt.Errorf("%s.%s requires zone_index", ref.Table, ref.Column)
	}
	guards := make([]Guard, 0, 1)
	if ref.HasZoneID {
		guards = append(guards, Require(fmt.Sprintf("zones[%d].id", ref.ZoneIndex), ref.ZoneID))
	}
	return EntryTarget(ref.EntryName, tb.EntrySettings, guards...), fmt.Sprintf("zones[%d].%s", ref.ZoneIndex, field), nil
}

func entityTargetFromSQLRef(ref SQLValueRef, kind tb.EntryKind) (Target, error) {
	switch kind {
	case tb.EntryBibite:
		return bibiteTargetFromSQLRef(ref)
	case tb.EntryEgg:
		return eggTargetFromSQLRef(ref)
	default:
		return Target{}, unsupportedSQLValueRef(ref)
	}
}

func bibiteTargetFromSQLRef(ref SQLValueRef) (Target, error) {
	if ref.EntryName == "" {
		return Target{}, fmt.Errorf("%s.%s requires entry_name", ref.Table, ref.Column)
	}
	if !ref.HasBodyID {
		return Target{}, fmt.Errorf("%s.%s requires body_id", ref.Table, ref.Column)
	}
	return BibiteTarget(BibiteRef{EntryName: ref.EntryName, BodyID: ref.BodyID}), nil
}

func eggTargetFromSQLRef(ref SQLValueRef) (Target, error) {
	if ref.EntryName == "" {
		return Target{}, fmt.Errorf("%s.%s requires entry_name", ref.Table, ref.Column)
	}
	if !ref.HasEggID {
		return Target{}, fmt.Errorf("%s.%s requires egg_id", ref.Table, ref.Column)
	}
	return EntryTarget(ref.EntryName, tb.EntryEgg, Require("egg.id", ref.EggID)), nil
}

func bibiteTargetFromGeneRef(ref SQLValueRef) (Target, error) {
	id, err := ownerIDAsInt(ref)
	if err != nil {
		return Target{}, err
	}
	if ref.OwnerKind != "" && ref.OwnerKind != "bibite" {
		return Target{}, fmt.Errorf("%s.%s owner_kind = %q, want bibite", ref.Table, ref.Column, ref.OwnerKind)
	}
	ref.BodyID = id
	ref.HasBodyID = true
	return bibiteTargetFromSQLRef(ref)
}

func eggTargetFromGeneRef(ref SQLValueRef) (Target, error) {
	id, err := ownerIDAsInt(ref)
	if err != nil {
		return Target{}, err
	}
	if ref.OwnerKind != "" && ref.OwnerKind != "egg" {
		return Target{}, fmt.Errorf("%s.%s owner_kind = %q, want egg", ref.Table, ref.Column, ref.OwnerKind)
	}
	ref.EggID = id
	ref.HasEggID = true
	return eggTargetFromSQLRef(ref)
}

func ownerIDAsInt(ref SQLValueRef) (int64, error) {
	if ref.OwnerID == "" {
		return 0, fmt.Errorf("%s.%s requires owner_id", ref.Table, ref.Column)
	}
	id, err := strconv.ParseInt(ref.OwnerID, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s.%s owner_id %q is not an integer: %w", ref.Table, ref.Column, ref.OwnerID, err)
	}
	return id, nil
}

func unsupportedSQLValueRef(ref SQLValueRef) error {
	return fmt.Errorf("sql value ref %s.%s is not writable", ref.Table, ref.Column)
}
