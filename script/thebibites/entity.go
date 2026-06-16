package thebibites

import (
	"fmt"
	"sort"

	mutator "github.com/asemones/bibicontrol/savemutator/thebibites"
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
	case "delete":
		return starlark.NewBuiltin("delete", e.deleteBuiltin), nil
	}
	if sc, ok := subCollectionRegistry()[e.kind][name]; ok {
		return &ElementCollection{ls: e.ls, kind: e.kind, entryName: e.entryName, spec: sc}, nil
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
	names = append(names, "gene", "genes", "delete")
	for sub := range subCollectionRegistry()[e.kind] {
		names = append(names, sub)
	}
	sort.Strings(names)
	return names
}

// SetField mutates a writable scalar attribute (b.energy = x). It resolves the
// attribute through the same generated-metadata registry used for reads, so write
// capability comes straight from attrSpec.writable (the field's sqlref path) — no
// parallel allowlist. It rejects unknown/read-only attributes and non-scalar
// values with a clean error, captures the current value as a stale-value guard,
// validates the new value (type/range/enum via validateSet, guards.go), stages a
// guarded set on the session, writes the new value through to the in-memory row
// (so a later plain read observes it), and records a DuckDB mirror intent. Scalar
// set only — gene writes and structural mutations are later tickets.
func (e *Entity) SetField(name string, val starlark.Value) error {
	if name == "gene" || name == "genes" || name == "delete" {
		return fmt.Errorf("%s.%s is read-only", e.kind, name)
	}
	if _, ok := subCollectionRegistry()[e.kind][name]; ok {
		return fmt.Errorf("%s.%s is a collection (use .append/.delete), not assignable", e.kind, name)
	}
	spec, ok := attrRegistry()[e.kind][name]
	if !ok {
		return fmt.Errorf("cannot set %s.%s: unknown attribute", e.kind, name)
	}
	if !spec.writable {
		return fmt.Errorf("%s.%s is read-only (derived or locator column, not writable)", e.kind, name)
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
	if err := validateSet(spec, goVal); err != nil {
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
	ref.Table, ref.Column = spec.table, spec.sourceColumn
	if err := e.ls.session.StageSQLSet(ref.WithExpected(old), staged); err != nil {
		return fmt.Errorf("%s.%s: %w", e.kind, name, err)
	}
	e.ls.stagedOps++
	e.ls.recordMirror(spec.table, spec.sourceColumn, spec.sqlType, e.entryName, staged)
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
	idx, ok := genes.byName[name]
	if !ok {
		return starlark.None, nil
	}
	return geneValueToStarlark(genes.backing[idx]), nil
}

// deleteBuiltin implements b.delete(prune=False): stage a whole-entity delete.
// This is a structural op — it is staged on the session for the eventual commit
// but, unlike a scalar set, is NOT mirrored into DuckDB, so an in-run query does
// not observe it until after commit (the consistency contract). The mutator owns
// the cascade: deleting a bibite reconciles scene nBibites and (with prune) the
// parent/child links, and deleting the last member of a species drops it from
// activeSpeciesList. The referential guard (refuse orphaning a parent link
// without prune) fires later, inside Session.Apply at commit time.
func (e *Entity) deleteBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var prune bool
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "prune?", &prune); err != nil {
		return nil, err
	}
	if err := e.ls.stageEntityDelete(e.kind, e.entryName, prune); err != nil {
		return nil, err
	}
	e.ls.stagedOps++
	return starlark.None, nil
}

// stageEntityDelete stages one whole-entity delete by entry_name, guarded by the
// kind's id stale guard. Prune control exists only for bibites (eggs have no parent
// links, so prune is a no-op for them); everything else goes through the generic
// SQL-ref delete, which resolves to a whole-entry delete with default options.
// Structural — not mirrored. Shared by Entity.delete() and the bulk
// where(...).delete() path so both get the identical cascade/guard semantics.
func (ls *LoadedSave) stageEntityDelete(kind, entryName string, prune bool) error {
	ref, err := ls.entityLocatorRef(kind, entryName)
	if err != nil {
		return err
	}
	table, err := identityTable(kind)
	if err != nil {
		return err
	}
	ref.Table = table
	if prune && kind == "bibite" {
		err = ls.session.StageDeleteBibiteWithOptions(
			mutator.BibiteRef{EntryName: entryName, BodyID: ref.BodyID},
			mutator.DeleteOptions{PruneParentLinks: true},
		)
	} else {
		err = ls.session.StageSQLDelete(ref)
	}
	if err != nil {
		return fmt.Errorf("%s.delete: %w", kind, err)
	}
	return nil
}
