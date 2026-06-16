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
	_ starlark.Value    = (*Entity)(nil)
	_ starlark.HasAttrs = (*Entity)(nil)
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
