package thebibites

import "go.starlark.net/starlark"

// Globals returns the predeclared Starlark globals for a loaded save. It is the
// bridge consumed by script.Run: the returned dict binds `save` to the loaded
// save's read surface. Host builtins (e.g. analytics helpers) attach here later.
func Globals(ls *LoadedSave) starlark.StringDict {
	return starlark.StringDict{
		"save": &Save{ls: ls},
	}
}
