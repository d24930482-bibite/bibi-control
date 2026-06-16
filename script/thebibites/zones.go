package thebibites

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"

	mutator "github.com/asemones/bibicontrol/savemutator/thebibites"
	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
	"go.starlark.net/starlark"
)

// zones.go is the P2 read+mutation surface for settings zones, exposed as
// save.zones. It mirrors the entity scalar surface (entity.go) for writes and the
// settings surface (settings_value.go) for zone-scoped values:
//
//	save.zones[i].name = "Plains"          # scalar set (name/material/distribution)
//	save.zones[i].values["fertility"].set(0.9)  # zone-scoped value (reuses P1 Setting)
//	save.zones[i].delete()                 # structural delete (id-guarded)
//	z = save.zones.clone(0); z.name = "X"; z.append()  # create a new zone
//
// A scalar set is staged AND mirrored into DuckDB (keyed by entry_name+zone_index)
// so an in-run save.sql observes it. Delete and clone-append are STRUCTURAL: staged
// for the eventual commit but NOT mirrored, so an in-run read/query still sees the
// original zone set; the change appears only after commit.
//
// clone(i) deep-copies the template zone's full JSON (SettingsZoneRow.RawJSON), so
// the new zone inherits every field — including its zone-scoped values — verbatim.
// name/material/distribution are editable on the pending zone (plain top-level
// keys); editing the inherited values on a *pending* clone is intentionally not
// supported (it would require replicating the mutator's wrapper-vs-bare value
// handling on the raw map) — edit them via save.zones[i].values after committing.
// On append a fresh zone id is assigned to avoid colliding with the template;
// zone-group membership and other id references are not reconciled (a known v2
// limitation, like brain-graph integrity).

// Zones is the save.zones collection: an indexable, iterable sequence over the
// save's settings zones, plus clone() (create) and count().
type Zones struct {
	ls *LoadedSave
}

var (
	_ starlark.Value     = (*Zones)(nil)
	_ starlark.Indexable = (*Zones)(nil)
	_ starlark.Sequence  = (*Zones)(nil)
	_ starlark.HasAttrs  = (*Zones)(nil)
)

func (zs *Zones) String() string        { return "zones" }
func (zs *Zones) Type() string          { return "zones" }
func (zs *Zones) Freeze()               {}
func (zs *Zones) Truth() starlark.Bool  { return starlark.Bool(zs.Len() > 0) }
func (zs *Zones) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable type: zones") }

func (zs *Zones) Len() int                   { return len(zs.ls.tables.SettingsZones) }
func (zs *Zones) Index(i int) starlark.Value { return &Zone{ls: zs.ls, idx: i} }
func (zs *Zones) Iterate() starlark.Iterator { return &zoneIterator{zs: zs} }

func (zs *Zones) Attr(name string) (starlark.Value, error) {
	switch name {
	case "clone":
		return starlark.NewBuiltin("clone", zs.cloneBuiltin), nil
	case "count":
		return starlark.NewBuiltin("count", zs.countBuiltin), nil
	}
	return nil, nil
}

func (zs *Zones) AttrNames() []string { return []string{"clone", "count"} }

func (zs *Zones) countBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(b.Name(), args, kwargs); err != nil {
		return nil, err
	}
	return starlark.MakeInt(zs.Len()), nil
}

// cloneBuiltin implements save.zones.clone(index) -> PendingZone: a detached deep
// copy of the template zone's JSON, ready to edit and append.
func (zs *Zones) cloneBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var index int
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "index", &index); err != nil {
		return nil, err
	}
	if index < 0 || index >= len(zs.ls.tables.SettingsZones) {
		return nil, fmt.Errorf("zones.clone: index %d out of range (have %d zones)", index, len(zs.ls.tables.SettingsZones))
	}
	src := &zs.ls.tables.SettingsZones[index]
	var data map[string]any
	if err := json.Unmarshal([]byte(src.RawJSON), &data); err != nil {
		return nil, fmt.Errorf("zones.clone(%d): %w", index, err)
	}
	return &PendingZone{ls: zs.ls, src: src, data: data}, nil
}

type zoneIterator struct {
	zs  *Zones
	pos int
}

func (it *zoneIterator) Next(p *starlark.Value) bool {
	if it.pos >= it.zs.Len() {
		return false
	}
	*p = &Zone{ls: it.zs.ls, idx: it.pos}
	it.pos++
	return true
}

func (it *zoneIterator) Done() {}

// Zone is a handle on one settings zone, addressed by its slice index. Scalar
// columns (name/material/distribution) read and write through zoneRegistry();
// .values is the zone-scoped settings-value mapping (reusing P1's SettingScope);
// .delete() stages a structural delete.
type Zone struct {
	ls  *LoadedSave
	idx int
}

var (
	_ starlark.Value       = (*Zone)(nil)
	_ starlark.HasAttrs    = (*Zone)(nil)
	_ starlark.HasSetField = (*Zone)(nil)
)

func (z *Zone) row() *tb.SettingsZoneRow { return &z.ls.tables.SettingsZones[z.idx] }

func (z *Zone) String() string       { return fmt.Sprintf("zone[%d]", z.idx) }
func (z *Zone) Type() string         { return "zone" }
func (z *Zone) Freeze()              {}
func (z *Zone) Truth() starlark.Bool { return starlark.True }
func (z *Zone) Hash() (uint32, error) {
	return starlark.String(fmt.Sprintf("zone\x00%s\x00%d", z.row().EntryName, z.idx)).Hash()
}

func (z *Zone) Attr(name string) (starlark.Value, error) {
	switch name {
	case "values":
		return &SettingScope{ls: z.ls, table: "settings_zone_values", ownerID: zoneValuesOwnerID(z.row())}, nil
	case "delete":
		return starlark.NewBuiltin("delete", z.deleteBuiltin), nil
	}
	spec, ok := zoneRegistry()[name]
	if !ok {
		return nil, nil
	}
	rv := reflect.ValueOf(z.row()).Elem()
	return toStarlark(rv.FieldByIndex(spec.fieldIndex))
}

func (z *Zone) AttrNames() []string {
	specs := zoneRegistry()
	names := make([]string, 0, len(specs)+2)
	for name := range specs {
		names = append(names, name)
	}
	names = append(names, "delete", "values")
	sort.Strings(names)
	return names
}

// SetField mutates a writable zone scalar (z.name/material/distribution). It
// validates, writes through to the in-memory row, stages a guarded set built
// directly from the zone locator (entry_name + zone_index, with zone_id as the
// stale-index guard), and mirrors it into DuckDB keyed by (entry_name, zone_index).
func (z *Zone) SetField(name string, val starlark.Value) error {
	if name == "values" || name == "delete" {
		return fmt.Errorf("zone.%s is read-only", name)
	}
	spec, ok := zoneRegistry()[name]
	if !ok {
		return fmt.Errorf("cannot set zone.%s: unknown attribute", name)
	}
	if !spec.writable {
		return fmt.Errorf("zone.%s is read-only (locator column, not writable)", name)
	}
	row := z.row()
	rv := reflect.ValueOf(row).Elem()
	old, err := goScalar(rv.FieldByIndex(spec.fieldIndex))
	if err != nil {
		return fmt.Errorf("zone.%s: %w", name, err)
	}
	goVal, err := fromStarlark(val)
	if err != nil {
		return fmt.Errorf("zone.%s: %w", name, err)
	}
	if err := validateSet(spec, goVal); err != nil {
		return fmt.Errorf("zone.%s: %w", name, err)
	}
	staged, err := setRowField(rv, spec.fieldIndex, goVal)
	if err != nil {
		return fmt.Errorf("zone.%s: %w", name, err)
	}
	ref := mutator.SQLValueRef{
		Table:        "settings_zones",
		Column:       spec.sourceColumn,
		EntryName:    row.EntryName,
		ZoneIndex:    row.ZoneIndex,
		HasZoneIndex: true,
		ZoneID:       row.ZoneID,
		HasZoneID:    row.HasZoneID,
	}
	if err := z.ls.session.StageSQLSet(ref.WithExpected(old), staged); err != nil {
		return fmt.Errorf("zone.%s: %w", name, err)
	}
	z.ls.stagedOps++
	z.ls.recordMirrorRow("settings_zones", spec.sourceColumn, spec.sqlType, []mirrorLocator{
		{column: "entry_name", value: row.EntryName},
		{column: "zone_index", value: row.ZoneIndex},
	}, staged)
	return nil
}

// deleteBuiltin implements zone.delete(): stage a structural delete located by
// zone_index and guarded by zone_id (so a shifted/stale index fails loudly at
// commit rather than removing a different zone). Not mirrored into DuckDB.
func (z *Zone) deleteBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(b.Name(), args, kwargs); err != nil {
		return nil, err
	}
	row := z.row()
	ref := mutator.SQLValueRef{
		Table:        "settings_zones",
		EntryName:    row.EntryName,
		ZoneIndex:    row.ZoneIndex,
		HasZoneIndex: true,
		ZoneID:       row.ZoneID,
		HasZoneID:    row.HasZoneID,
	}
	if err := z.ls.session.StageSQLDelete(ref); err != nil {
		return nil, fmt.Errorf("zone.delete: %w", err)
	}
	z.ls.stagedOps++
	return starlark.None, nil
}

// zoneValuesOwnerID reproduces the parser's owner-id rule for a zone's settings
// values (parse_settings.go: ownerIDFromInt(zone.ID, zone.HasID, zoneIndex)), so
// save.zones[i].values resolves through the shared (owner_id, setting_name) index.
func zoneValuesOwnerID(row *tb.SettingsZoneRow) string {
	if row.HasZoneID {
		return strconv.FormatInt(row.ZoneID, 10)
	}
	return strconv.Itoa(row.ZoneIndex)
}

// PendingZone is a detached, editable deep copy of a template zone's JSON, created
// by save.zones.clone(i). Editing name/material/distribution mutates the copy;
// .append() stages a structural append of the whole object (with a fresh id). The
// pending zone is not part of save.zones and is invisible to in-run reads/queries
// until commit.
type PendingZone struct {
	ls       *LoadedSave
	src      *tb.SettingsZoneRow
	data     map[string]any
	appended bool
}

var (
	_ starlark.Value       = (*PendingZone)(nil)
	_ starlark.HasAttrs    = (*PendingZone)(nil)
	_ starlark.HasSetField = (*PendingZone)(nil)
)

func (pz *PendingZone) String() string        { return "pending_zone" }
func (pz *PendingZone) Type() string          { return "pending_zone" }
func (pz *PendingZone) Freeze()               {}
func (pz *PendingZone) Truth() starlark.Bool  { return starlark.True }
func (pz *PendingZone) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable type: pending_zone") }

func (pz *PendingZone) Attr(name string) (starlark.Value, error) {
	if name == "append" {
		return starlark.NewBuiltin("append", pz.appendBuiltin), nil
	}
	spec, ok := zoneRegistry()[name]
	if !ok {
		return nil, nil
	}
	// Read the (possibly edited) value out of the pending JSON map, falling back to
	// the template row for columns the raw map does not surface as a scalar.
	if spec.jsonKey != "" {
		if v, ok := pz.data[spec.jsonKey]; ok {
			return fromSQLValue(v)
		}
	}
	rv := reflect.ValueOf(pz.src).Elem()
	return toStarlark(rv.FieldByIndex(spec.fieldIndex))
}

func (pz *PendingZone) AttrNames() []string {
	specs := zoneRegistry()
	names := make([]string, 0, len(specs)+1)
	for name := range specs {
		names = append(names, name)
	}
	names = append(names, "append")
	sort.Strings(names)
	return names
}

// SetField edits a writable scalar (name/material/distribution) on the pending
// zone's JSON. Validated like a committed zone set, but it only mutates the
// in-memory copy — nothing is staged until append().
func (pz *PendingZone) SetField(name string, val starlark.Value) error {
	if pz.appended {
		return fmt.Errorf("zone already appended; clone again for another")
	}
	spec, ok := zoneRegistry()[name]
	if !ok {
		return fmt.Errorf("cannot set zone.%s: unknown attribute", name)
	}
	if !spec.writable {
		return fmt.Errorf("zone.%s is read-only (locator column, not writable)", name)
	}
	goVal, err := fromStarlark(val)
	if err != nil {
		return fmt.Errorf("zone.%s: %w", name, err)
	}
	if err := validateSet(spec, goVal); err != nil {
		return fmt.Errorf("zone.%s: %w", name, err)
	}
	pz.data[spec.jsonKey] = goVal
	return nil
}

// appendBuiltin implements pendingZone.append(): assign a fresh zone id (so the
// clone does not collide with its template) and stage a structural append of the
// whole zone object. Not mirrored into DuckDB — visible only after commit.
func (pz *PendingZone) appendBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(b.Name(), args, kwargs); err != nil {
		return nil, err
	}
	if pz.appended {
		return nil, fmt.Errorf("zone already appended")
	}
	if id, ok := pz.ls.allocZoneID(); ok {
		pz.data["id"] = id
	}
	ref := mutator.SQLValueRef{Table: "settings_zones", EntryName: pz.src.EntryName}
	if err := pz.ls.session.StageSQLAppend(ref, pz.data); err != nil {
		return nil, fmt.Errorf("zones.clone(...).append: %w", err)
	}
	pz.ls.stagedOps++
	pz.appended = true
	return starlark.None, nil
}
