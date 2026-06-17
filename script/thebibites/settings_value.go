package thebibites

import (
	"fmt"

	mutator "github.com/asemones/bibicontrol/savemutator/thebibites"
	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
	"go.starlark.net/starlark"
)

// settings_value.go is the T7/P1 settings read+write surface. It mirrors the gene
// surface (gene.go): named scalar values read in-memory from the per-scope
// settings tables and written back through the generated settings_value resolver
// (savemutator/thebibites/sqlref_settings.go). Three scopes are exposed here —
// simulation, independent, and material(name); zone-scoped values share the same
// SettingValueRow shape and arrive with the save.zones surface (P2).
//
//	save.settings.simulation["maxBibiteCount"].value      # read
//	save.settings.simulation["maxBibiteCount"].set(200)   # write
//	save.settings.independent["..."].set(v)
//	save.settings.material("Plant")["energy"].set(v)
//
// Like every other mutation surface, a scalar set is staged on the session for the
// single end-of-run write AND mirrored into DuckDB (keyed by entry_name + the
// row's unique path) so an in-run save.sql observes it without a reparse.

// scopeOwner is the owner_id discriminator a flat scope filters on.
const (
	simulationOwnerID  = "settings"
	independentOwnerID = "independents"
)

// Settings is the save.settings namespace. Its attributes select a scope.
type Settings struct {
	ls *LoadedSave
}

var (
	_ starlark.Value    = (*Settings)(nil)
	_ starlark.HasAttrs = (*Settings)(nil)
)

func (s *Settings) String() string        { return "settings" }
func (s *Settings) Type() string          { return "settings" }
func (s *Settings) Freeze()               {}
func (s *Settings) Truth() starlark.Bool  { return starlark.True }
func (s *Settings) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable type: settings") }

func (s *Settings) Attr(name string) (starlark.Value, error) {
	switch name {
	case "simulation":
		return &SettingScope{ls: s.ls, table: "settings_simulation_values", ownerID: simulationOwnerID}, nil
	case "independent":
		return &SettingScope{ls: s.ls, table: "settings_independent_values", ownerID: independentOwnerID}, nil
	case "material":
		return starlark.NewBuiltin("material", s.materialBuiltin), nil
	default:
		return nil, nil
	}
}

func (s *Settings) AttrNames() []string {
	return []string{"independent", "material", "simulation"}
}

// materialBuiltin implements save.settings.material("Name") -> the material's
// settings scope.
func (s *Settings) materialBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var name string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "name", &name); err != nil {
		return nil, err
	}
	return &SettingScope{ls: s.ls, table: "settings_material_values", ownerID: name}, nil
}

// SettingScope is one settings scope (simulation / independent / a single
// material). It is a mapping keyed by setting name: subscripting returns a Setting
// handle whose .value reads and .set(v) writes.
type SettingScope struct {
	ls      *LoadedSave
	table   string
	ownerID string
}

var (
	_ starlark.Value   = (*SettingScope)(nil)
	_ starlark.Mapping = (*SettingScope)(nil)
)

func (sc *SettingScope) String() string {
	return fmt.Sprintf("settings[%s/%s]", sc.table, sc.ownerID)
}
func (sc *SettingScope) Type() string         { return "setting_scope" }
func (sc *SettingScope) Freeze()              {}
func (sc *SettingScope) Truth() starlark.Bool { return starlark.True }
func (sc *SettingScope) Hash() (uint32, error) {
	return 0, fmt.Errorf("unhashable type: %s", sc.Type())
}

// Get implements scope["setting_name"] -> Setting handle. A missing setting
// reports found=false (Starlark raises a KeyError).
func (sc *SettingScope) Get(k starlark.Value) (starlark.Value, bool, error) {
	name, ok := starlark.AsString(k)
	if !ok {
		return nil, false, fmt.Errorf("setting name must be a string, got %s", k.Type())
	}
	row, ok := sc.ls.settingRow(sc.table, sc.ownerID, name)
	if !ok {
		return nil, false, nil
	}
	return &Setting{ls: sc.ls, table: sc.table, row: row}, true, nil
}

// Setting is a handle on one settings value: .value/.name/.type/.scope read it,
// .set(v) stages a guarded write. It retains the full locator via a pointer into
// the backing SettingValueRow, so .set writes through and later reads observe it.
type Setting struct {
	ls    *LoadedSave
	table string
	row   *tb.SettingValueRow
}

var (
	_ starlark.Value    = (*Setting)(nil)
	_ starlark.HasAttrs = (*Setting)(nil)
)

func (s *Setting) String() string       { return fmt.Sprintf("setting<%s>", s.row.SettingName) }
func (s *Setting) Type() string         { return "setting" }
func (s *Setting) Freeze()              {}
func (s *Setting) Truth() starlark.Bool { return starlark.True }
func (s *Setting) Hash() (uint32, error) {
	return starlark.String(s.row.EntryName + "\x00" + s.row.Path).Hash()
}

func (s *Setting) Attr(name string) (starlark.Value, error) {
	switch name {
	case "value":
		return settingValueToStarlark(*s.row), nil
	case "name":
		return starlark.String(s.row.SettingName), nil
	case "type":
		return starlark.String(string(s.row.Type)), nil
	case "scope":
		return starlark.String(s.row.Scope), nil
	case "set":
		return starlark.NewBuiltin("set", s.setBuiltin), nil
	default:
		return nil, nil
	}
}

func (s *Setting) AttrNames() []string {
	return []string{"name", "scope", "set", "type", "value"}
}

// setBuiltin implements setting.set(value): stage a guarded settings-value write.
// The value is validated against the setting's scalar type, written through the
// in-memory SettingValueRow, staged on the session via the settings_value resolver
// (Path + WrapperRawJSON passed verbatim, so wrapper-vs-bare and zone-index guards
// are the mutator's concern), and mirrored into DuckDB keyed by (entry_name, path).
func (s *Setting) setBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var v starlark.Value
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "value", &v); err != nil {
		return nil, err
	}
	if err := s.ls.setSettingValue(s.table, s.row, v); err != nil {
		return nil, err
	}
	return starlark.None, nil
}

// setSettingValue stages and mirrors one settings-value write.
func (ls *LoadedSave) setSettingValue(table string, row *tb.SettingValueRow, v starlark.Value) error {
	goVal, err := fromStarlark(v)
	if err != nil {
		return fmt.Errorf("setting %q: %w", row.SettingName, err)
	}
	if err := validateValue(scalarTypeRule(row.Type), goVal); err != nil {
		return fmt.Errorf("setting %q: %w", row.SettingName, err)
	}
	column, sqlType, err := scalarValueColumn(row.Type)
	if err != nil {
		return fmt.Errorf("setting %q: %w", row.SettingName, err)
	}
	old, staged, err := applyScalarValue(row.Type, goVal, &row.NumberValue, &row.BoolValue, &row.StringValue)
	if err != nil {
		return fmt.Errorf("setting %q: %w", row.SettingName, err)
	}
	ref := mutator.SQLValueRef{
		Table:          table,
		Column:         column,
		EntryName:      row.EntryName,
		OwnerKind:      row.OwnerKind,
		OwnerID:        row.OwnerID,
		SettingName:    row.SettingName,
		Path:           row.Path,
		ValueType:      string(row.Type),
		WrapperRawJSON: row.WrapperRawJSON,
	}
	if err := ls.stageScalarSet(ref, old, staged, table, column, sqlType, []mirrorLocator{
		{column: "entry_name", value: row.EntryName},
		{column: "path", value: row.Path},
	}, nil); err != nil {
		return fmt.Errorf("setting %q: %w", row.SettingName, err)
	}
	return nil
}

// settingValueToStarlark converts a typed settings cell into a Starlark value
// following its ScalarType. Null settings read as None.
func settingValueToStarlark(r tb.SettingValueRow) starlark.Value {
	switch r.Type {
	case tb.ScalarNumber:
		return starlark.Float(r.NumberValue)
	case tb.ScalarBool:
		return starlark.Bool(r.BoolValue)
	case tb.ScalarString:
		return starlark.String(r.StringValue)
	default:
		return starlark.None
	}
}
