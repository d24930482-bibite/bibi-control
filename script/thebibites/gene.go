package thebibites

import (
	"fmt"

	"go.starlark.net/starlark"

	mutator "github.com/asemones/bibicontrol/savemutator/thebibites"
	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

// Gene is a read-only Starlark view of one gene: its name, typed value, and
// scalar type. Yielded by GeneCollection and addressable via b.gene(name) for a
// single lookup.
type Gene struct {
	row tb.GeneRow
}

var (
	_ starlark.Value    = (*Gene)(nil)
	_ starlark.HasAttrs = (*Gene)(nil)
)

func (g *Gene) String() string       { return fmt.Sprintf("gene<%s>", g.row.GeneName) }
func (g *Gene) Type() string         { return "gene" }
func (g *Gene) Freeze()              {}
func (g *Gene) Truth() starlark.Bool { return starlark.True }
func (g *Gene) Hash() (uint32, error) {
	return starlark.String(g.row.EntryName + "\x00" + g.row.GeneName).Hash()
}

func (g *Gene) Attr(name string) (starlark.Value, error) {
	switch name {
	case "name":
		return starlark.String(g.row.GeneName), nil
	case "value":
		return geneValueToStarlark(g.row), nil
	case "type":
		return starlark.String(string(g.row.Type)), nil
	default:
		return nil, nil
	}
}

func (g *Gene) AttrNames() []string { return []string{"name", "type", "value"} }

// GeneCollection is a lazy sequence over one entity's genes, in save order.
// Exposed as b.genes; iterate with `for g in b.genes`, read one with
// b.genes["Name"] (Mapping), write one with b.genes["Name"] = v (HasSetKey).
type GeneCollection struct {
	ls        *LoadedSave
	kind      string
	entryName string
}

var (
	_ starlark.Value     = (*GeneCollection)(nil)
	_ starlark.Iterable  = (*GeneCollection)(nil)
	_ starlark.Sequence  = (*GeneCollection)(nil)
	_ starlark.Mapping   = (*GeneCollection)(nil)
	_ starlark.HasSetKey = (*GeneCollection)(nil)
)

func (c *GeneCollection) String() string       { return "genes" }
func (c *GeneCollection) Type() string         { return "gene_collection" }
func (c *GeneCollection) Freeze()              {}
func (c *GeneCollection) Truth() starlark.Bool { return starlark.Bool(c.Len() > 0) }
func (c *GeneCollection) Hash() (uint32, error) {
	return 0, fmt.Errorf("unhashable type: %s", c.Type())
}

func (c *GeneCollection) rows() []tb.GeneRow {
	set := c.ls.genesFor(c.kind, c.entryName)
	if set == nil {
		return nil
	}
	out := make([]tb.GeneRow, len(set.order))
	for i, idx := range set.order {
		out[i] = set.backing[idx]
	}
	return out
}

// Get implements b.genes["Name"] -> typed gene value (number/bool/string). A
// missing gene reports found=false (Starlark raises a KeyError), matching mapping
// subscript semantics; b.gene("Name") is the None-returning point read.
func (c *GeneCollection) Get(k starlark.Value) (starlark.Value, bool, error) {
	name, ok := starlark.AsString(k)
	if !ok {
		return nil, false, fmt.Errorf("gene name must be a string, got %s", k.Type())
	}
	set := c.ls.genesFor(c.kind, c.entryName)
	if set == nil {
		return nil, false, nil
	}
	idx, ok := set.byName[name]
	if !ok {
		return nil, false, nil
	}
	return geneValueToStarlark(set.backing[idx]), true, nil
}

// SetKey implements b.genes["Name"] = v: stage a guarded gene-value write. The
// value is validated against the gene's scalar type, written through to the
// in-memory GeneRow (so b.gene/b.genes read it back), staged on the session, and
// mirrored into DuckDB keyed by (entry_name, path) so an in-run SQL read
// observes it too (mirror-everything). path is keyed (not gene_name) because the
// two gene nesting levels flatten into one table sharing the gene_name namespace,
// so a leaf-name collision would otherwise rewrite both rows; path is unique per
// gene entry. Unknown gene names are rejected — genes are
// keyed by the names already present on the entity, not created here.
func (c *GeneCollection) SetKey(k, v starlark.Value) error {
	name, ok := starlark.AsString(k)
	if !ok {
		return fmt.Errorf("gene name must be a string, got %s", k.Type())
	}
	set := c.ls.genesFor(c.kind, c.entryName)
	if set == nil {
		return fmt.Errorf("%s %s has no genes", c.kind, c.entryName)
	}
	idx, ok := set.byName[name]
	if !ok {
		return fmt.Errorf("unknown gene %q on %s %s", name, c.kind, c.entryName)
	}
	return c.ls.setGeneValue(c.kind, &set.backing[idx], v)
}

// setGeneValue stages and mirrors one gene-value write. The SQLValueRef is built
// straight from the GeneRow's own locator (owner_kind/owner_id/path) — the mutator
// gene resolver re-derives the body/egg target from owner_id — so no separate
// locator lookup is needed.
func (ls *LoadedSave) setGeneValue(kind string, row *tb.GeneRow, v starlark.Value) error {
	table, err := geneTable(kind)
	if err != nil {
		return err
	}
	goVal, err := fromStarlark(v)
	if err != nil {
		return fmt.Errorf("gene %q: %w", row.GeneName, err)
	}
	if err := validateValue(scalarTypeRule(row.Type), goVal); err != nil {
		return fmt.Errorf("gene %q: %w", row.GeneName, err)
	}
	column, sqlType, err := scalarValueColumn(row.Type)
	if err != nil {
		return fmt.Errorf("gene %q: %w", row.GeneName, err)
	}
	old, staged, err := applyScalarValue(row.Type, goVal, &row.NumberValue, &row.BoolValue, &row.StringValue)
	if err != nil {
		return fmt.Errorf("gene %q: %w", row.GeneName, err)
	}
	ref := mutator.SQLValueRef{
		Table:     table,
		Column:    column,
		EntryName: row.EntryName,
		OwnerKind: row.OwnerKind,
		OwnerID:   row.OwnerID,
		Path:      row.Path,
	}
	if err := ls.session.StageSQLSet(ref.WithExpected(old), staged); err != nil {
		return fmt.Errorf("gene %q: %w", row.GeneName, err)
	}
	ls.stagedOps++
	ls.recordMirrorRow(table, column, sqlType, []mirrorLocator{
		{column: "entry_name", value: row.EntryName},
		{column: "path", value: row.Path},
	}, staged)
	return nil
}

// geneTable maps an entity kind to its normalized gene table.
func geneTable(kind string) (string, error) {
	switch kind {
	case "bibite":
		return "bibite_genes", nil
	case "egg":
		return "egg_genes", nil
	default:
		return "", fmt.Errorf("unknown entity kind %q", kind)
	}
}

func (c *GeneCollection) Len() int { return len(c.rows()) }

func (c *GeneCollection) Iterate() starlark.Iterator {
	return &geneIterator{rows: c.rows()}
}

type geneIterator struct {
	rows []tb.GeneRow
	pos  int
}

func (it *geneIterator) Next(p *starlark.Value) bool {
	if it.pos >= len(it.rows) {
		return false
	}
	*p = &Gene{row: it.rows[it.pos]}
	it.pos++
	return true
}

func (it *geneIterator) Done() {}
