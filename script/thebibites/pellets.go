package thebibites

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	mutator "github.com/asemones/bibicontrol/savemutator/thebibites"
	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
	"go.starlark.net/starlark"
)

// pellets.go is the P2 read+mutation surface for food pellets, exposed as
// save.pellets — a flat, indexable sequence across all pellet groups:
//
//	save.pellets[i].amount = 5.0   # scalar set (material/amount/position/rb2d/decay)
//	save.pellets[i].delete()       # structural delete (reconciles scene nPellets)
//	p = save.pellets.clone(0); p.material = "Meat"; p.append(zone="World")  # create
//
// A scalar set is staged AND mirrored into DuckDB (keyed by entry_name +
// group_index + group_pellet_index) so an in-run save.sql observes it. delete() and
// clone-append are STRUCTURAL: staged for the eventual commit but NOT mirrored (an
// in-run query still sees the original pellets); the mutator reconciles the scene's
// nPellets on apply.
//
// Pellet CREATION is by clone (mirroring save.zones.clone): clone(i) deep-copies an
// existing pellet's full nested JSON (from the archive's retained Raw — PelletRow
// carries no RawJSON, and pellets are too numerous to store one per row), so the new
// pellet inherits transform/rb2d/matterDecay verbatim and is a complete, valid
// element. Editing a scalar on the pending pellet writes through the field's nested
// sqlref path (e.g. transform.position[0]); .append(zone="X") resolves that zone to
// its pellet group and stages the element into it (zone name is the ergonomic handle
// — raw group indices are an internal positional detail). A pending pellet is
// invisible to in-run reads/queries until commit. Zone-group/cross-reference
// reconciliation is not done (a known v2 limit).

// Pellets is the save.pellets collection: an indexable, iterable sequence over
// every pellet in the save (flat across groups), plus count().
type Pellets struct {
	ls *LoadedSave
}

var (
	_ starlark.Value     = (*Pellets)(nil)
	_ starlark.Indexable = (*Pellets)(nil)
	_ starlark.Sequence  = (*Pellets)(nil)
	_ starlark.HasAttrs  = (*Pellets)(nil)
)

func (ps *Pellets) String() string        { return "pellets" }
func (ps *Pellets) Type() string          { return "pellets" }
func (ps *Pellets) Freeze()               {}
func (ps *Pellets) Truth() starlark.Bool  { return starlark.Bool(ps.Len() > 0) }
func (ps *Pellets) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable type: pellets") }

func (ps *Pellets) Len() int                   { return len(ps.ls.tables.Pellets) }
func (ps *Pellets) Index(i int) starlark.Value { return &Pellet{ls: ps.ls, idx: i} }
func (ps *Pellets) Iterate() starlark.Iterator { return &pelletIterator{ps: ps} }

func (ps *Pellets) Attr(name string) (starlark.Value, error) {
	switch name {
	case "clone":
		return starlark.NewBuiltin("clone", ps.cloneBuiltin), nil
	case "count":
		return starlark.NewBuiltin("count", ps.countBuiltin), nil
	}
	return nil, nil
}

func (ps *Pellets) AttrNames() []string { return []string{"clone", "count"} }

func (ps *Pellets) countBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(b.Name(), args, kwargs); err != nil {
		return nil, err
	}
	return starlark.MakeInt(ps.Len()), nil
}

// cloneBuiltin implements save.pellets.clone(index) -> PendingPellet: a detached
// deep copy of the template pellet's full nested JSON, ready to edit and append.
// The source is the archive's retained Raw map (PelletRow has no RawJSON);
// tables.Pellets is built from archive.PelletData.Pellets in order, so the flat
// index lines up 1:1.
func (ps *Pellets) cloneBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var index int
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "index", &index); err != nil {
		return nil, err
	}
	if index < 0 || index >= len(ps.ls.tables.Pellets) {
		return nil, fmt.Errorf("pellets.clone: index %d out of range (have %d pellets)", index, len(ps.ls.tables.Pellets))
	}
	if ps.ls.archive == nil || ps.ls.archive.PelletData == nil || index >= len(ps.ls.archive.PelletData.Pellets) {
		return nil, fmt.Errorf("pellets.clone(%d): source pellet JSON unavailable", index)
	}
	// Deep-copy via a json round-trip so edits do not mutate the archive's own map
	// (which the mutator writes at commit) — same deep-copy semantics as zones.
	encoded, err := json.Marshal(ps.ls.archive.PelletData.Pellets[index].Raw)
	if err != nil {
		return nil, fmt.Errorf("pellets.clone(%d): %w", index, err)
	}
	var data map[string]any
	if err := json.Unmarshal(encoded, &data); err != nil {
		return nil, fmt.Errorf("pellets.clone(%d): %w", index, err)
	}
	return &PendingPellet{ls: ps.ls, src: &ps.ls.tables.Pellets[index], data: data}, nil
}

type pelletIterator struct {
	ps  *Pellets
	pos int
}

func (it *pelletIterator) Next(p *starlark.Value) bool {
	if it.pos >= it.ps.Len() {
		return false
	}
	*p = &Pellet{ls: it.ps.ls, idx: it.pos}
	it.pos++
	return true
}

func (it *pelletIterator) Done() {}

// Pellet is a handle on one pellet, addressed by its flat slice index. Scalar
// columns read and write through pelletRegistry(); .delete() stages a structural
// delete.
type Pellet struct {
	ls  *LoadedSave
	idx int
}

var (
	_ starlark.Value       = (*Pellet)(nil)
	_ starlark.HasAttrs    = (*Pellet)(nil)
	_ starlark.HasSetField = (*Pellet)(nil)
)

func (p *Pellet) row() *tb.PelletRow { return &p.ls.tables.Pellets[p.idx] }

func (p *Pellet) String() string       { return fmt.Sprintf("pellet[%d]", p.idx) }
func (p *Pellet) Type() string         { return "pellet" }
func (p *Pellet) Freeze()              {}
func (p *Pellet) Truth() starlark.Bool { return starlark.True }
func (p *Pellet) Hash() (uint32, error) {
	row := p.row()
	return starlark.String(fmt.Sprintf("pellet\x00%s\x00%d\x00%d", row.EntryName, row.GroupIndex, row.GroupPelletIndex)).Hash()
}

func (p *Pellet) Attr(name string) (starlark.Value, error) {
	if name == "delete" {
		return starlark.NewBuiltin("delete", p.deleteBuiltin), nil
	}
	spec, ok := pelletRegistry()[name]
	if !ok {
		return nil, nil
	}
	rv := reflect.ValueOf(p.row()).Elem()
	return toStarlark(rv.FieldByIndex(spec.fieldIndex))
}

func (p *Pellet) AttrNames() []string {
	specs := pelletRegistry()
	names := make([]string, 0, len(specs)+1)
	for name := range specs {
		names = append(names, name)
	}
	names = append(names, "delete")
	sort.Strings(names)
	return names
}

// pelletRef builds the locator core shared by a pellet's set and delete: the
// pellets table keyed by entry_name + group_index + group_pellet_index, with the
// group's zone as a stale-group guard.
func (p *Pellet) pelletRef(row *tb.PelletRow) mutator.SQLValueRef {
	return mutator.SQLValueRef{
		Table:               "pellets",
		EntryName:           row.EntryName,
		GroupIndex:          row.GroupIndex,
		HasGroupIndex:       true,
		GroupPelletIndex:    row.GroupPelletIndex,
		HasGroupPelletIndex: true,
		Zone:                row.Zone,
		HasZone:             true,
	}
}

// SetField mutates a writable pellet scalar (p.amount, p.material, p.position_x,
// rb2d/decay, ...). It validates, writes through to the in-memory row, stages a
// guarded set, and mirrors it into DuckDB keyed by (entry_name, group_index,
// group_pellet_index).
func (p *Pellet) SetField(name string, val starlark.Value) error {
	if name == "delete" {
		return fmt.Errorf("pellet.%s is read-only", name)
	}
	spec, ok := pelletRegistry()[name]
	if !ok {
		return fmt.Errorf("cannot set pellet.%s: unknown attribute", name)
	}
	if !spec.writable {
		return fmt.Errorf("pellet.%s is read-only (locator column, not writable)", name)
	}
	row := p.row()
	rv := reflect.ValueOf(row).Elem()
	old, err := goScalar(rv.FieldByIndex(spec.fieldIndex))
	if err != nil {
		return fmt.Errorf("pellet.%s: %w", name, err)
	}
	goVal, err := fromStarlark(val)
	if err != nil {
		return fmt.Errorf("pellet.%s: %w", name, err)
	}
	if err := validateSet(spec, goVal); err != nil {
		return fmt.Errorf("pellet.%s: %w", name, err)
	}
	staged, err := setRowField(rv, spec.fieldIndex, goVal)
	if err != nil {
		return fmt.Errorf("pellet.%s: %w", name, err)
	}
	ref := p.pelletRef(row)
	ref.Column = spec.sourceColumn
	if err := p.ls.stageScalarSet(ref, old, staged, "pellets", spec.sourceColumn, spec.sqlType, []mirrorLocator{
		{column: "entry_name", value: row.EntryName},
		{column: "group_index", value: row.GroupIndex},
		{column: "group_pellet_index", value: row.GroupPelletIndex},
	}, nil); err != nil {
		return fmt.Errorf("pellet.%s: %w", name, err)
	}
	return nil
}

// deleteBuiltin implements pellet.delete(): stage a structural delete located by
// group_index + group_pellet_index, guarded by the group's zone and the current
// material (so a shifted/stale index fails loudly at commit rather than removing a
// different pellet). The mutator reconciles the scene's nPellets. Not mirrored.
func (p *Pellet) deleteBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(b.Name(), args, kwargs); err != nil {
		return nil, err
	}
	row := p.row()
	ref := p.pelletRef(row)
	// Stale-value guard on a stable element field (material), mirroring the
	// sub-collection delete pattern; skipped only if the column is somehow absent.
	if guard, ok := pelletRegistry()["material"]; ok {
		cur, err := goScalar(reflect.ValueOf(row).Elem().FieldByIndex(guard.fieldIndex))
		if err != nil {
			return nil, fmt.Errorf("pellet.delete: %w", err)
		}
		ref.Column = guard.sourceColumn
		ref = ref.WithExpected(cur)
	}
	if err := p.ls.session.StageSQLDelete(ref); err != nil {
		return nil, fmt.Errorf("pellet.delete: %w", err)
	}
	p.ls.stagedOps++
	p.ls.markStructuralStaged()
	return starlark.None, nil
}

// PendingPellet is a detached, editable deep copy of a template pellet's nested
// JSON, created by save.pellets.clone(i). Editing a writable scalar mutates the
// copy in place (through its sqlref path); .append(group=j) stages a structural
// append of the whole element into pellets[j].pellets. The pending pellet is not
// part of save.pellets and is invisible to in-run reads/queries until commit.
type PendingPellet struct {
	ls       *LoadedSave
	src      *tb.PelletRow
	data     map[string]any
	appended bool
}

var (
	_ starlark.Value       = (*PendingPellet)(nil)
	_ starlark.HasAttrs    = (*PendingPellet)(nil)
	_ starlark.HasSetField = (*PendingPellet)(nil)
)

func (pp *PendingPellet) String() string        { return "pending_pellet" }
func (pp *PendingPellet) Type() string          { return "pending_pellet" }
func (pp *PendingPellet) Freeze()               {}
func (pp *PendingPellet) Truth() starlark.Bool  { return starlark.True }
func (pp *PendingPellet) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable type: pending_pellet") }

func (pp *PendingPellet) Attr(name string) (starlark.Value, error) {
	if name == "append" {
		return starlark.NewBuiltin("append", pp.appendBuiltin), nil
	}
	spec, ok := pelletRegistry()[name]
	if !ok {
		return nil, nil
	}
	// Read the (possibly edited) value out of the pending JSON by its nested sqlref
	// path, falling back to the template row for anything the raw map does not
	// surface (e.g. pure locators with no path).
	if spec.jsonKey != "" {
		if v, ok := getNestedPellet(pp.data, spec.jsonKey); ok {
			return fromSQLValue(v)
		}
	}
	rv := reflect.ValueOf(pp.src).Elem()
	return toStarlark(rv.FieldByIndex(spec.fieldIndex))
}

func (pp *PendingPellet) AttrNames() []string {
	specs := pelletRegistry()
	names := make([]string, 0, len(specs)+1)
	for name := range specs {
		names = append(names, name)
	}
	names = append(names, "append")
	sort.Strings(names)
	return names
}

// SetField edits a writable pellet scalar on the pending JSON. Validated like a
// committed pellet set, but it only mutates the in-memory copy — nothing is staged
// until append(). The write goes through the field's nested sqlref path (e.g.
// transform.position[0]), NOT a flat data[jsonKey]=v (which would create a literal
// dotted key and corrupt the element — the reason the flat sub-collection append
// could not be reused for pellets).
func (pp *PendingPellet) SetField(name string, val starlark.Value) error {
	if pp.appended {
		return fmt.Errorf("pellet already appended; clone again for another")
	}
	spec, ok := pelletRegistry()[name]
	if !ok {
		return fmt.Errorf("cannot set pellet.%s: unknown attribute", name)
	}
	if !spec.writable {
		return fmt.Errorf("pellet.%s is read-only (locator column, not writable)", name)
	}
	goVal, err := fromStarlark(val)
	if err != nil {
		return fmt.Errorf("pellet.%s: %w", name, err)
	}
	if err := validateSet(spec, goVal); err != nil {
		return fmt.Errorf("pellet.%s: %w", name, err)
	}
	// Coerce to the field's type before writing into the clone so the pending and
	// committed pellet paths produce identical on-disk JSON: the committed path runs
	// goVal through setRowField (DOUBLE -> float64), so e.g. p.amount = 5 must stage
	// 5.0 here too, not the raw integer goVal from fromStarlark.
	coerced, err := coercePelletScalar(spec.sqlType, goVal)
	if err != nil {
		return fmt.Errorf("pellet.%s: %w", name, err)
	}
	if err := setNestedPellet(pp.data, spec.jsonKey, coerced); err != nil {
		return fmt.Errorf("pellet.%s: %w", name, err)
	}
	return nil
}

// coercePelletScalar coerces goVal (an int64/float64/bool/string from fromStarlark)
// to the Go type implied by the column's generated sqlType, so the pending-pellet
// clone write matches the committed path's setRowField coercion (most pellet scalars
// are DOUBLE, so an integral Starlark int must be staged as a float). Mirrors the
// kind policy in deriveType/asFloat64/asInt64; an unknown sqlType passes the value
// through unchanged (same lenient stance deriveType takes for new generated types).
func coercePelletScalar(sqlType string, goVal any) (any, error) {
	switch deriveType(sqlType) {
	case kindNumber:
		f, ok := asFloat64(goVal)
		if !ok {
			return nil, fmt.Errorf("cannot assign %T to numeric field", goVal)
		}
		return f, nil
	case kindInt:
		n, ok := asInt64(goVal)
		if !ok {
			return nil, fmt.Errorf("cannot assign %T to integer field", goVal)
		}
		return n, nil
	case kindUint:
		n, ok := asInt64(goVal)
		if !ok || n < 0 {
			return nil, fmt.Errorf("cannot assign %T to unsigned field", goVal)
		}
		return n, nil
	default:
		// kindBool/kindString/kindUnknown: validateSet has already type-checked, and
		// bool/string need no numeric coercion, so pass through unchanged.
		return goVal, nil
	}
}

// appendBuiltin implements pendingPellet.append(zone="..."): resolve the pellet group
// for that zone (the ergonomic handle — raw group indices are an internal detail),
// then stage a structural append of the whole pellet object into that group's array.
// The zone is also passed as the append's stale guard. The mutator reconciles the
// scene's nPellets. Not mirrored into DuckDB — visible only after commit. Unlike
// zones, pellets carry no id, so there is no id allocation.
func (pp *PendingPellet) appendBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var zone string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "zone", &zone); err != nil {
		return nil, err
	}
	if pp.appended {
		return nil, fmt.Errorf("pellet already appended")
	}
	group, err := pp.ls.pelletGroupByZone(pp.src.EntryName, zone)
	if err != nil {
		return nil, fmt.Errorf("pellets.clone(...).append: %w", err)
	}
	ref := mutator.SQLValueRef{
		Table:         "pellets",
		EntryName:     pp.src.EntryName,
		GroupIndex:    group,
		HasGroupIndex: true,
		Zone:          zone,
		HasZone:       true,
	}
	if err := pp.ls.session.StageSQLAppend(ref, pp.data); err != nil {
		return nil, fmt.Errorf("pellets.clone(...).append: %w", err)
	}
	pp.ls.stagedOps++
	pp.ls.markStructuralStaged()
	pp.appended = true
	return starlark.None, nil
}

// pelletGroupByZone resolves a pellet group by its zone name within the given pellets
// entry, returning the group index for the clone-append target. Zone name is the
// ergonomic, stable handle exposed to scripts — raw group indices are an internal
// positional detail. Errors loudly if no group carries that zone (listing what is
// available) or if more than one does (the format does not guarantee zone is unique
// across groups, so we refuse to guess rather than append to the wrong one).
func (ls *LoadedSave) pelletGroupByZone(entryName, zone string) (int, error) {
	matches := make([]int, 0, 1)
	available := make([]string, 0, len(ls.tables.PelletGroups))
	for i := range ls.tables.PelletGroups {
		g := &ls.tables.PelletGroups[i]
		if g.EntryName != entryName {
			continue
		}
		available = append(available, g.Zone)
		if g.Zone == zone {
			matches = append(matches, g.GroupIndex)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return 0, fmt.Errorf("no pellet group for zone %q (available: %s)", zone, strings.Join(available, ", "))
	default:
		return 0, fmt.Errorf("zone %q matches %d pellet groups; ambiguous", zone, len(matches))
	}
}

// --- isolated nested-path helpers (intentionally local to pellets.go) ---
//
// These read/write a value inside a cloned pellet's nested map by its generated
// sqlref path (e.g. "transform.position[0]", "rb2d.px"). They are deliberately NOT
// promoted into convert.go or a shared json-path utility: the syntax handling is
// only needed for the pellet clone surface, mirrors the mutator's own package-private
// parsePath/setJSONPath, and should not be prematurely generalized. Because a clone
// is already a complete structure, the setter is set-into-existing only — it errors
// on a missing intermediate rather than scaffolding objects/arrays from scratch.

// pelletPathStep is one navigation step: a map key, or an array index.
type pelletPathStep struct {
	key     string
	index   int
	isIndex bool
}

// parsePelletPath splits a dotted sqlref path into steps, expanding a trailing
// "[n]" on a segment (e.g. "position[0]") into a key step followed by an index step.
func parsePelletPath(path string) ([]pelletPathStep, error) {
	if path == "" {
		return nil, fmt.Errorf("empty path")
	}
	var steps []pelletPathStep
	for _, seg := range strings.Split(path, ".") {
		open := strings.IndexByte(seg, '[')
		if open < 0 {
			steps = append(steps, pelletPathStep{key: seg})
			continue
		}
		if !strings.HasSuffix(seg, "]") {
			return nil, fmt.Errorf("malformed path segment %q", seg)
		}
		idx, err := strconv.Atoi(seg[open+1 : len(seg)-1])
		if err != nil {
			return nil, fmt.Errorf("malformed array index in %q: %w", seg, err)
		}
		if key := seg[:open]; key != "" {
			steps = append(steps, pelletPathStep{key: key})
		}
		steps = append(steps, pelletPathStep{index: idx, isIndex: true})
	}
	return steps, nil
}

// setNestedPellet sets value at path within data, navigating existing maps/slices.
// A missing intermediate (or leaf key) is an error — the clone already carries the
// full structure, so this never needs to create one. Slice elements are mutated in
// place (the backing array is shared with the parent map), so no write-back needed.
func setNestedPellet(data map[string]any, path string, value any) error {
	steps, err := parsePelletPath(path)
	if err != nil {
		return err
	}
	var current any = data
	for i, step := range steps {
		last := i == len(steps)-1
		if step.isIndex {
			arr, ok := current.([]any)
			if !ok {
				return fmt.Errorf("path %q: expected array at step %d", path, i)
			}
			if step.index < 0 || step.index >= len(arr) {
				return fmt.Errorf("path %q: index %d out of range", path, step.index)
			}
			if last {
				arr[step.index] = value
				return nil
			}
			current = arr[step.index]
			continue
		}
		m, ok := current.(map[string]any)
		if !ok {
			return fmt.Errorf("path %q: expected object at step %d", path, i)
		}
		if _, exists := m[step.key]; !exists {
			return fmt.Errorf("path %q: key %q is missing", path, step.key)
		}
		if last {
			m[step.key] = value
			return nil
		}
		current = m[step.key]
	}
	return nil
}

// getNestedPellet reads the value at path within data, or (nil,false) if any step
// is absent or the wrong shape.
func getNestedPellet(data map[string]any, path string) (any, bool) {
	steps, err := parsePelletPath(path)
	if err != nil {
		return nil, false
	}
	var current any = data
	for _, step := range steps {
		if step.isIndex {
			arr, ok := current.([]any)
			if !ok || step.index < 0 || step.index >= len(arr) {
				return nil, false
			}
			current = arr[step.index]
			continue
		}
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		next, exists := m[step.key]
		if !exists {
			return nil, false
		}
		current = next
	}
	return current, true
}
