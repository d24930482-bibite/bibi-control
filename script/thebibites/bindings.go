package thebibites

import "go.starlark.net/starlark"

// Globals returns the predeclared Starlark globals for a loaded save. It is the
// bridge consumed by script.Run: the returned dict binds `save` to the loaded
// save's read/analytics surface plus the host aggregate builtins (sum/mean/median
// over small materialized lists).
func Globals(ls *LoadedSave) starlark.StringDict {
	globals := starlark.StringDict{
		"save": &Save{ls: ls},
	}
	for name, builtin := range hostAggregates() {
		globals[name] = builtin
	}
	return globals
}
