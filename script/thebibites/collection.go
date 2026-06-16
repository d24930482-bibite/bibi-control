package thebibites

import (
	"fmt"

	"go.starlark.net/starlark"
)

// EntityCollection is a lazy, read-only sequence over an entity kind's identity
// table (save.bibites / save.eggs). It materializes Entity values one at a time
// during iteration; T5 adds query-backed narrowing (.where) and aggregates.
type EntityCollection struct {
	ls   *LoadedSave
	kind string
}

var (
	_ starlark.Value    = (*EntityCollection)(nil)
	_ starlark.Iterable = (*EntityCollection)(nil)
	_ starlark.Sequence = (*EntityCollection)(nil)
)

func (c *EntityCollection) String() string       { return c.kind + "s" }
func (c *EntityCollection) Type() string         { return c.kind + "_collection" }
func (c *EntityCollection) Freeze()              {}
func (c *EntityCollection) Truth() starlark.Bool { return starlark.Bool(c.Len() > 0) }
func (c *EntityCollection) Hash() (uint32, error) {
	return 0, fmt.Errorf("unhashable type: %s", c.Type())
}

// identityAccess returns the access handle for this kind's identity table.
func (c *EntityCollection) identityAccess() *tableAccess {
	tables := entityTables[c.kind]
	if len(tables) == 0 {
		return nil
	}
	return c.ls.access[tables[0]]
}

func (c *EntityCollection) Len() int {
	ta := c.identityAccess()
	if ta == nil {
		return 0
	}
	return len(ta.order)
}

func (c *EntityCollection) Iterate() starlark.Iterator {
	ta := c.identityAccess()
	var names []string
	if ta != nil {
		names = ta.order
	}
	return &entityIterator{ls: c.ls, kind: c.kind, names: names}
}

type entityIterator struct {
	ls    *LoadedSave
	kind  string
	names []string
	pos   int
}

func (it *entityIterator) Next(p *starlark.Value) bool {
	if it.pos >= len(it.names) {
		return false
	}
	*p = &Entity{ls: it.ls, kind: it.kind, entryName: it.names[it.pos]}
	it.pos++
	return true
}

func (it *entityIterator) Done() {}
