package thebibites

import (
	"fmt"

	"go.starlark.net/starlark"
)

// EntityCollection is a lazy, query-backed sequence over an entity kind's
// identity table (save.bibites / save.eggs). Iteration materializes Entity values
// one at a time; aggregates (.count/.sum/.mean/.median/.min/.max/.quantile) and
// grouping (.group_by) push down into DuckDB and never materialize rows. .where
// narrows the collection by AND-combining a raw SQL predicate; the stored text
// stays raw and is friendly-column-rewritten once at query-compile time.
type EntityCollection struct {
	ls    *LoadedSave
	kind  string
	where string // raw, un-rewritten predicate; "" means unfiltered
}

var (
	_ starlark.Value    = (*EntityCollection)(nil)
	_ starlark.Iterable = (*EntityCollection)(nil)
	_ starlark.Sequence = (*EntityCollection)(nil)
	_ starlark.HasAttrs = (*EntityCollection)(nil)
)

func (c *EntityCollection) String() string       { return c.kind + "s" }
func (c *EntityCollection) Type() string         { return c.kind + "_collection" }
func (c *EntityCollection) Freeze()              {}
func (c *EntityCollection) Truth() starlark.Bool { return starlark.Bool(c.Len() > 0) }
func (c *EntityCollection) Hash() (uint32, error) {
	return 0, fmt.Errorf("unhashable type: %s", c.Type())
}

// Attr exposes the query-backed narrowing/aggregate methods. Unknown names return
// (nil, nil) for a clean Starlark AttributeError.
func (c *EntityCollection) Attr(name string) (starlark.Value, error) {
	switch name {
	case "where":
		return starlark.NewBuiltin("where", c.whereBuiltin), nil
	case "group_by":
		return starlark.NewBuiltin("group_by", c.groupByBuiltin), nil
	case "count":
		return starlark.NewBuiltin("count", c.countBuiltin), nil
	case "quantile":
		return starlark.NewBuiltin("quantile", c.quantileBuiltin), nil
	case "sum", "mean", "median", "min", "max":
		return c.aggBuiltin(name), nil
	case "set":
		return starlark.NewBuiltin("set", c.setBuiltin), nil
	}
	return nil, nil
}

func (c *EntityCollection) AttrNames() []string {
	return []string{"count", "group_by", "max", "mean", "median", "min", "quantile", "set", "sum", "where"}
}

// setBuiltin implements where(...).set(column, value): one batched scalar set over
// every matching entity. value is a Starlark scalar constant applied to all
// matched rows. Returns the number of rows staged.
func (c *EntityCollection) setBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var column string
	var value starlark.Value
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "column", &column, "value", &value); err != nil {
		return nil, err
	}
	n, err := c.ls.bulkSet(c.kind, c.where, column, value)
	if err != nil {
		return nil, err
	}
	return starlark.MakeInt(n), nil
}

// whereBuiltin narrows the collection, returning a new unmaterialized collection.
func (c *EntityCollection) whereBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var predicate string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "predicate", &predicate); err != nil {
		return nil, err
	}
	return &EntityCollection{ls: c.ls, kind: c.kind, where: combineWhere(c.where, predicate)}, nil
}

// aggBuiltin builds a one-column aggregate method (sum/mean/median/min/max).
func (c *EntityCollection) aggBuiltin(fn string) *starlark.Builtin {
	return starlark.NewBuiltin(fn, func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var col string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "column", &col); err != nil {
			return nil, err
		}
		return c.ls.scalarAgg(c.kind, c.where, aggCall{fn: fn, col: col})
	})
}

func (c *EntityCollection) countBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(b.Name(), args, kwargs); err != nil {
		return nil, err
	}
	return c.ls.scalarAgg(c.kind, c.where, aggCall{fn: "count"})
}

func (c *EntityCollection) quantileBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var col string
	var q float64
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "column", &col, "q", &q); err != nil {
		return nil, err
	}
	if q < 0 || q > 1 {
		return nil, fmt.Errorf("quantile q must be in [0, 1], got %v", q)
	}
	return c.ls.scalarAgg(c.kind, c.where, aggCall{fn: "quantile", col: col, q: q})
}

func (c *EntityCollection) groupByBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var col string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "column", &col); err != nil {
		return nil, err
	}
	return &GroupedCollection{ls: c.ls, kind: c.kind, where: c.where, groupCol: col}, nil
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
	// Producing an Entity is the row-materialization path the analytics layer
	// avoids; the counter lets tests assert aggregates never reach it.
	it.ls.rowsMaterialized++
	*p = &Entity{ls: it.ls, kind: it.kind, entryName: it.names[it.pos]}
	it.pos++
	return true
}

func (it *entityIterator) Done() {}

// GroupedCollection is the result of save.bibites[.where(...)].group_by(col). Its
// aggregate methods compile to "SELECT <group>, agg(col) … GROUP BY <group>" and
// return a dict keyed by group value. It carries the same raw where predicate as
// the collection it came from; nothing is materialized.
type GroupedCollection struct {
	ls       *LoadedSave
	kind     string
	where    string
	groupCol string
}

var (
	_ starlark.Value    = (*GroupedCollection)(nil)
	_ starlark.HasAttrs = (*GroupedCollection)(nil)
)

func (g *GroupedCollection) String() string       { return g.kind + "s.group_by(" + g.groupCol + ")" }
func (g *GroupedCollection) Type() string         { return "grouped_collection" }
func (g *GroupedCollection) Freeze()              {}
func (g *GroupedCollection) Truth() starlark.Bool { return starlark.True }
func (g *GroupedCollection) Hash() (uint32, error) {
	return 0, fmt.Errorf("unhashable type: %s", g.Type())
}

func (g *GroupedCollection) Attr(name string) (starlark.Value, error) {
	switch name {
	case "count":
		return starlark.NewBuiltin("count", g.countBuiltin), nil
	case "quantile":
		return starlark.NewBuiltin("quantile", g.quantileBuiltin), nil
	case "sum", "mean", "median", "min", "max":
		return g.aggBuiltin(name), nil
	}
	return nil, nil
}

func (g *GroupedCollection) AttrNames() []string {
	return []string{"count", "max", "mean", "median", "min", "quantile", "sum"}
}

func (g *GroupedCollection) aggBuiltin(fn string) *starlark.Builtin {
	return starlark.NewBuiltin(fn, func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var col string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "column", &col); err != nil {
			return nil, err
		}
		return g.ls.groupedAgg(g.kind, g.where, g.groupCol, aggCall{fn: fn, col: col})
	})
}

func (g *GroupedCollection) countBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(b.Name(), args, kwargs); err != nil {
		return nil, err
	}
	return g.ls.groupedAgg(g.kind, g.where, g.groupCol, aggCall{fn: "count"})
}

func (g *GroupedCollection) quantileBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var col string
	var q float64
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "column", &col, "q", &q); err != nil {
		return nil, err
	}
	if q < 0 || q > 1 {
		return nil, fmt.Errorf("quantile q must be in [0, 1], got %v", q)
	}
	return g.ls.groupedAgg(g.kind, g.where, g.groupCol, aggCall{fn: "quantile", col: col, q: q})
}
