package thebibites

import (
	"fmt"
	"sort"

	"go.starlark.net/starlark"
)

// aggregates.go provides free-standing host aggregate builtins (sum/mean/median)
// over an already-materialized Starlark iterable of numbers. They are a
// convenience for SMALL lists the script has materialized in the interpreter —
// NOT the scale path. For "scan many rows + do math", use the push-down
// collection aggregates (save.bibites.median("energy")) or save.sql, which run in
// DuckDB without materializing rows.
//
// Only sum/mean/median are provided: count/min/max are already covered by the
// Starlark universe (len/min/max), so installing same-named predeclared globals
// would shadow those builtins. The collection equivalents (.count/.min/.max)
// still exist for the push-down path.
func hostAggregates() starlark.StringDict {
	return starlark.StringDict{
		"sum":    starlark.NewBuiltin("sum", aggSum),
		"mean":   starlark.NewBuiltin("mean", aggMean),
		"median": starlark.NewBuiltin("median", aggMedian),
	}
}

func aggSum(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	xs, err := unpackFloats(b, args, kwargs)
	if err != nil {
		return nil, err
	}
	var total float64
	for _, x := range xs {
		total += x
	}
	return starlark.Float(total), nil
}

func aggMean(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	xs, err := unpackFloats(b, args, kwargs)
	if err != nil {
		return nil, err
	}
	if len(xs) == 0 {
		return nil, fmt.Errorf("%s: empty sequence", b.Name())
	}
	var total float64
	for _, x := range xs {
		total += x
	}
	return starlark.Float(total / float64(len(xs))), nil
}

func aggMedian(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	xs, err := unpackFloats(b, args, kwargs)
	if err != nil {
		return nil, err
	}
	if len(xs) == 0 {
		return nil, fmt.Errorf("%s: empty sequence", b.Name())
	}
	sort.Float64s(xs)
	n := len(xs)
	if n%2 == 1 {
		return starlark.Float(xs[n/2]), nil
	}
	// Linear-interpolated median of the two middle values — matches DuckDB's
	// median() over an even count.
	return starlark.Float((xs[n/2-1] + xs[n/2]) / 2), nil
}

// unpackFloats reads the single iterable argument into a []float64. Each element
// must be a number (int or float).
func unpackFloats(b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) ([]float64, error) {
	var iterable starlark.Iterable
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "values", &iterable); err != nil {
		return nil, err
	}
	it := iterable.Iterate()
	defer it.Done()
	var out []float64
	var v starlark.Value
	for it.Next(&v) {
		f, ok := starlark.AsFloat(v)
		if !ok {
			return nil, fmt.Errorf("%s: %s is not a number", b.Name(), v.Type())
		}
		out = append(out, f)
	}
	return out, nil
}
