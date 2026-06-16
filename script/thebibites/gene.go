package thebibites

import (
	"fmt"

	"go.starlark.net/starlark"

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

// GeneCollection is a lazy, read-only sequence over one entity's genes, in save
// order. Exposed as b.genes; iterate with `for g in b.genes`.
type GeneCollection struct {
	ls        *LoadedSave
	kind      string
	entryName string
}

var (
	_ starlark.Value    = (*GeneCollection)(nil)
	_ starlark.Iterable = (*GeneCollection)(nil)
	_ starlark.Sequence = (*GeneCollection)(nil)
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
	return set.order
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
