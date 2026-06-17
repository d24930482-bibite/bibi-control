package thebibites

import (
	"fmt"

	"go.starlark.net/starlark"
)

// aggRunner executes a fully-formed aggregate call against the analytics layer.
// EntityCollection wires it to scalarAgg (one scalar result); GroupedCollection
// wires it to groupedAgg (a dict keyed by group value). It is the only behavioral
// difference between the two collections' aggregate method sets.
type aggRunner func(aggCall) (starlark.Value, error)

// aggMethod returns Attr's one-column aggregate builtin (sum/mean/median/min/max)
// for the given fn, unpacking a single `column` arg and dispatching through run.
func aggMethod(fn string, run aggRunner) *starlark.Builtin {
	return starlark.NewBuiltin(fn, func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var col string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "column", &col); err != nil {
			return nil, err
		}
		return run(aggCall{fn: fn, col: col})
	})
}

// countMethod returns the no-arg count builtin dispatching through run.
func countMethod(run aggRunner) *starlark.Builtin {
	return starlark.NewBuiltin("count", func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := starlark.UnpackArgs(b.Name(), args, kwargs); err != nil {
			return nil, err
		}
		return run(aggCall{fn: "count"})
	})
}

// quantileMethod returns the quantile builtin (column + q∈[0,1]) dispatching
// through run. The q range is validated before push-down.
func quantileMethod(run aggRunner) *starlark.Builtin {
	return starlark.NewBuiltin("quantile", func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var col string
		var q float64
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "column", &col, "q", &q); err != nil {
			return nil, err
		}
		if q < 0 || q > 1 {
			return nil, fmt.Errorf("quantile q must be in [0, 1], got %v", q)
		}
		return run(aggCall{fn: "quantile", col: col, q: q})
	})
}

// aggAttr dispatches the aggregate-method names (count/quantile/sum/mean/median/
// min/max) shared by both collections, returning (nil, false) for any other name
// so the caller can fall through to its own (collection-specific) methods.
func aggAttr(name string, run aggRunner) (starlark.Value, bool) {
	switch name {
	case "count":
		return countMethod(run), true
	case "quantile":
		return quantileMethod(run), true
	case "sum", "mean", "median", "min", "max":
		return aggMethod(name, run), true
	}
	return nil, false
}

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

	// resolved memoizes the predicate push-down for Len/Iterate/Truth on a
	// filtered collection so a len()+iteration pair runs the query once. Only set
	// for a filtered collection (where != ""); unfiltered uses the identity table's
	// in-memory order directly. The push-down is resolved EAGERLY in whereBuiltin
	// (one query at .where() time) and reused here.
	//
	// resolveErr captures a push-down failure (e.g. an unknown column in the
	// predicate). Len/Iterate cannot return an error, so rather than mapping the
	// failure to a silent empty set (which would let a typo'd predicate report
	// "processed nothing" as success), they surface it LOUDLY: len(c)/iteration
	// panic with the error, which the script engine's host-panic recovery turns
	// into a clean diagnostic. A valid predicate that simply matches nothing still
	// iterates empty with no error.
	//
	// resolvedAtOps snapshots ls.stagedOps when resolved was last computed. Since
	// stagedOps advances on every staging path (scalar/bulk set, delete, append,
	// zone clone), entryNames re-resolves when it observes a different value, so a
	// filtered collection's Len/Iterate cannot go stale against a fresh count()
	// after an in-run mutation touches a predicate-relevant column.
	resolved      []string
	resolvedSet   bool
	resolveErr    error
	resolvedAtOps int
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
	if v, ok := aggAttr(name, c.runAgg); ok {
		return v, nil
	}
	switch name {
	case "where":
		return starlark.NewBuiltin("where", c.whereBuiltin), nil
	case "group_by":
		return starlark.NewBuiltin("group_by", c.groupByBuiltin), nil
	case "set":
		return starlark.NewBuiltin("set", c.setBuiltin), nil
	case "set_expr":
		return starlark.NewBuiltin("set_expr", c.setExprBuiltin), nil
	case "delete":
		return starlark.NewBuiltin("delete", c.deleteBuiltin), nil
	}
	return nil, nil
}

// runAgg is EntityCollection's aggRunner: one scalar result over the (filtered)
// collection.
func (c *EntityCollection) runAgg(call aggCall) (starlark.Value, error) {
	return c.ls.scalarAgg(c.kind, c.where, call)
}

func (c *EntityCollection) AttrNames() []string {
	return []string{"count", "delete", "group_by", "max", "mean", "median", "min", "quantile", "set", "set_expr", "sum", "where"}
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

// setExprBuiltin implements where(...).set_expr(column, expr): one batched set
// where each matched entity's new value is computed per row by evaluating the SQL
// expression `expr` against that row (e.g. set_expr("energy", "energy * 0.9")). It
// is a DISTINCT method from set (not string-vs-constant auto-detection, which is
// ambiguous for TEXT columns): the second argument is always a SQL expression.
// Returns the number of rows staged.
func (c *EntityCollection) setExprBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var column, expr string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "column", &column, "expr", &expr); err != nil {
		return nil, err
	}
	n, err := c.ls.bulkSetExpr(c.kind, c.where, column, expr)
	if err != nil {
		return nil, err
	}
	return starlark.MakeInt(n), nil
}

// deleteBuiltin implements where(...).delete(prune=False): stage a whole-entity
// delete for every matching entity — the structural counterpart of .set. Iteration
// does NOT apply the where predicate, so this push-down resolution is the only
// predicate-scoped delete; a `for e in coll.where(...): e.delete()` loop would walk
// every entity, not the matches. prune cascades parent links (bibite only). Returns
// the number staged for deletion.
func (c *EntityCollection) deleteBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var prune bool
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "prune?", &prune); err != nil {
		return nil, err
	}
	// Refuse a whole-population delete on an unfiltered collection: a dropped or
	// typo'd .where() must not silently stage delete-all. An intentional
	// delete-all opts in explicitly with .where("true").
	if c.where == "" {
		return nil, fmt.Errorf("delete() on an unfiltered %s collection would delete the whole population; scope it with .where(...) (use .where(\"true\") to delete all intentionally)", c.kind)
	}
	n, err := c.ls.bulkDelete(c.kind, c.where, prune)
	if err != nil {
		return nil, err
	}
	return starlark.MakeInt(n), nil
}

// whereBuiltin narrows the collection, returning a new unmaterialized collection.
// It eagerly resolves the combined predicate (a single push-down query, memoized
// on the new collection so Len/Iterate reuse it) and captures any resolution
// failure in resolveErr. The error is NOT returned here: the analytics path
// (count/aggregates) re-validates the predicate and reports the failure on its own,
// so .where() itself stays non-erroring (a narrow handle is always returned).
// Len/Iterate then surface resolveErr loudly instead of iterating a silent empty
// set — see entryNames.
func (c *EntityCollection) whereBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var predicate string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "predicate", &predicate); err != nil {
		return nil, err
	}
	where := combineWhere(c.where, predicate)
	names, err := c.ls.matchingEntryNames(c.kind, where)
	return &EntityCollection{
		ls:            c.ls,
		kind:          c.kind,
		where:         where,
		resolved:      names,
		resolvedSet:   true,
		resolveErr:    err,
		resolvedAtOps: c.ls.stagedOps,
	}, nil
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

// entryNames returns the entry_names this collection enumerates plus any push-down
// resolution error. An unfiltered collection (where == "") uses the identity
// table's in-memory order — the fast path, no DuckDB. A filtered collection reuses
// the set resolved eagerly in whereBuiltin, re-resolving only when ls.stagedOps
// differs from the snapshot taken at resolution time — i.e. an in-run mutation may
// have changed which entities match — so Len/Iterate stay consistent with a fresh
// count() after a mutation touches a predicate-relevant column.
func (c *EntityCollection) entryNames() ([]string, error) {
	if c.where == "" {
		ta := c.identityAccess()
		if ta == nil {
			return nil, nil
		}
		return ta.order, nil
	}
	if !c.resolvedSet || c.resolvedAtOps != c.ls.stagedOps {
		c.resolved, c.resolveErr = c.ls.matchingEntryNames(c.kind, c.where)
		c.resolvedSet = true
		c.resolvedAtOps = c.ls.stagedOps
	}
	return c.resolved, c.resolveErr
}

// resolveOrPanic returns the matching entry_names, panicking on a resolution error.
// Len/Iterate cannot return an error; a panic here is recovered by the script
// engine into a clean diagnostic, so a malformed predicate fails loudly instead of
// silently iterating nothing.
func (c *EntityCollection) resolveOrPanic() []string {
	names, err := c.entryNames()
	if err != nil {
		panic(err)
	}
	return names
}

func (c *EntityCollection) Len() int {
	return len(c.resolveOrPanic())
}

func (c *EntityCollection) Iterate() starlark.Iterator {
	return &entityIterator{ls: c.ls, kind: c.kind, names: c.resolveOrPanic()}
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
	if v, ok := aggAttr(name, g.runAgg); ok {
		return v, nil
	}
	return nil, nil
}

func (g *GroupedCollection) AttrNames() []string {
	return []string{"count", "max", "mean", "median", "min", "quantile", "sum"}
}

// runAgg is GroupedCollection's aggRunner: a dict keyed by group value over the
// (filtered) collection grouped by groupCol.
func (g *GroupedCollection) runAgg(call aggCall) (starlark.Value, error) {
	return g.ls.groupedAgg(g.kind, g.where, g.groupCol, call)
}
