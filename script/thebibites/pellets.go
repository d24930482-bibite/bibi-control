package thebibites

import (
	"fmt"
	"reflect"
	"sort"

	mutator "github.com/asemones/bibicontrol/savemutator/thebibites"
	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
	"go.starlark.net/starlark"
)

// pellets.go is the P2 read+mutation surface for food pellets, exposed as
// save.pellets — a flat, indexable sequence across all pellet groups:
//
//	save.pellets[i].amount = 5.0   # scalar set (material/amount/position/rb2d/decay)
//	save.pellets[i].delete()       # structural delete (reconciles scene nPellets)
//
// A scalar set is staged AND mirrored into DuckDB (keyed by entry_name +
// group_index + group_pellet_index) so an in-run save.sql observes it. delete() is
// STRUCTURAL: staged for the eventual commit but NOT mirrored (an in-run query
// still sees the pellet); the mutator reconciles the scene's nPellets on apply.
//
// Pellet CREATION (append) is deferred: a pellet's JSON element is deeply nested
// (pellet.*, transform.position[*], matterDecay.*, rb2d.*), so it cannot reuse the
// flat sub-collection append; a nested-payload design is future work.

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
	if name == "count" {
		return starlark.NewBuiltin("count", ps.countBuiltin), nil
	}
	return nil, nil
}

func (ps *Pellets) AttrNames() []string { return []string{"count"} }

func (ps *Pellets) countBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(b.Name(), args, kwargs); err != nil {
		return nil, err
	}
	return starlark.MakeInt(ps.Len()), nil
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
	if err := p.ls.session.StageSQLSet(ref.WithExpected(old), staged); err != nil {
		return fmt.Errorf("pellet.%s: %w", name, err)
	}
	p.ls.stagedOps++
	p.ls.recordMirrorRow("pellets", spec.sourceColumn, spec.sqlType, []mirrorLocator{
		{column: "entry_name", value: row.EntryName},
		{column: "group_index", value: row.GroupIndex},
		{column: "group_pellet_index", value: row.GroupPelletIndex},
	}, staged)
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
	return starlark.None, nil
}
