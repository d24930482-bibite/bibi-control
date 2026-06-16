package thebibites

import (
	"fmt"
	"sort"

	"go.starlark.net/starlark"
)

// Entity is a read-only Starlark view of one normalized entity (a bibite or an
// egg), identified by its entry name. Bibites and eggs share this type because
// their read surface is identical — only the registry kind and gene table
// differ. Attribute resolution is data-driven via attrRegistry().
type Entity struct {
	ls        *LoadedSave
	kind      string // "bibite" or "egg"
	entryName string
}

var (
	_ starlark.Value       = (*Entity)(nil)
	_ starlark.HasAttrs    = (*Entity)(nil)
	_ starlark.HasSetField = (*Entity)(nil)
)

func (e *Entity) String() string        { return fmt.Sprintf("%s<%s>", e.kind, e.entryName) }
func (e *Entity) Type() string          { return e.kind }
func (e *Entity) Freeze()               {} // immutable view; nothing to freeze
func (e *Entity) Truth() starlark.Bool  { return starlark.True }
func (e *Entity) Hash() (uint32, error) { return starlark.String(e.kind + e.entryName).Hash() }

// Attr resolves a friendly attribute or the gene() method. Unknown names return
// (nil, nil) so Starlark reports a clean "<kind> has no .<name> attribute".
func (e *Entity) Attr(name string) (starlark.Value, error) {
	switch name {
	case "gene":
		return starlark.NewBuiltin("gene", e.geneBuiltin), nil
	case "genes":
		return &GeneCollection{ls: e.ls, kind: e.kind, entryName: e.entryName}, nil
	}
	spec, ok := attrRegistry()[e.kind][name]
	if !ok {
		return nil, nil
	}
	switch spec.category {
	case categoryScalar:
		row, ok := e.ls.rowForEntry(spec.table, e.entryName)
		if !ok {
			// 1:1 sub-table has no row for this entity.
			return starlark.None, nil
		}
		return toStarlark(row.FieldByIndex(spec.fieldIndex))
	default:
		return nil, fmt.Errorf("attribute %q has unsupported category", name)
	}
}

// AttrNames lists every readable attribute plus gene(), sorted for deterministic
// dir() output.
func (e *Entity) AttrNames() []string {
	attrs := attrRegistry()[e.kind]
	names := make([]string, 0, len(attrs)+1)
	for name := range attrs {
		names = append(names, name)
	}
	names = append(names, "gene", "genes")
	sort.Strings(names)
	return names
}

// SetField mutates a writable scalar attribute (b.energy = x). It resolves the
// attribute through the same generated-metadata registry used for reads, so write
// capability comes straight from attrSpec.writable (the field's sqlref path) — no
// parallel allowlist. It rejects unknown/read-only attributes and non-scalar
// values with a clean error, captures the current value as a stale-value guard,
// stages a guarded set on the session, writes the new value through to the
// in-memory row (so a later plain read observes it), and records a DuckDB mirror
// intent. T6 is scalar set only — gene writes and structural mutations, plus the
// value-validation layer (range/enum/type-match), are later tickets.
func (e *Entity) SetField(name string, val starlark.Value) error {
	if name == "gene" || name == "genes" {
		return fmt.Errorf("%s.%s is read-only", e.kind, name)
	}
	spec, ok := attrRegistry()[e.kind][name]
	if !ok {
		return fmt.Errorf("cannot set %s.%s: unknown attribute", e.kind, name)
	}
	if !spec.writable {
		return fmt.Errorf("%s.%s is read-only", e.kind, name)
	}
	row, ok := e.ls.rowForEntry(spec.table, e.entryName)
	if !ok {
		return fmt.Errorf("cannot set %s.%s: no %s row for %s", e.kind, name, spec.table, e.entryName)
	}
	old, err := goScalar(row.FieldByIndex(spec.fieldIndex))
	if err != nil {
		return fmt.Errorf("%s.%s: %w", e.kind, name, err)
	}
	goVal, err := fromStarlark(val)
	if err != nil {
		return fmt.Errorf("%s.%s: %w", e.kind, name, err)
	}
	staged, err := setRowField(row, spec.fieldIndex, goVal)
	if err != nil {
		return fmt.Errorf("%s.%s: %w", e.kind, name, err)
	}
	ref, err := e.ls.entityLocatorRef(e.kind, e.entryName)
	if err != nil {
		return err
	}
	ref.Table, ref.Column = spec.table, spec.column
	if err := e.ls.session.StageSQLSet(ref.WithExpected(old), staged); err != nil {
		return fmt.Errorf("%s.%s: %w", e.kind, name, err)
	}
	e.ls.stagedOps++
	e.ls.recordMirror(spec.table, spec.column, spec.sqlType, e.entryName, staged)
	return nil
}

// geneBuiltin implements e.gene("Name") -> typed gene value (None if absent).
func (e *Entity) geneBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var name string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "name", &name); err != nil {
		return nil, err
	}
	genes := e.ls.genesFor(e.kind, e.entryName)
	if genes == nil {
		return starlark.None, nil
	}
	g, ok := genes.byName[name]
	if !ok {
		return starlark.None, nil
	}
	return geneValueToStarlark(g), nil
}
