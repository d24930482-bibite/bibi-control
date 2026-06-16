package thebibites

import "go.starlark.net/starlark"

// Globals returns the predeclared Starlark globals for a loaded save. It is the
// bridge consumed by script.Run: the returned dict binds `save` to the loaded
// save's read/analytics surface, the host aggregate builtins (sum/mean/median
// over small materialized lists), and `autocommit` (the commit-intent
// declaration consumed by RunAndCommit).
func Globals(ls *LoadedSave) starlark.StringDict {
	globals := starlark.StringDict{
		"save":       &Save{ls: ls},
		"autocommit": starlark.NewBuiltin("autocommit", autocommitBuiltin(ls)),
	}
	for name, builtin := range hostAggregates() {
		globals[name] = builtin
	}
	return globals
}

// autocommitBuiltin returns the `autocommit(enabled=True)` declaration. A script
// calls it at the top to opt a pure-analysis run out of producing a revision;
// absent the call, commit intent stays on (the default). It only sets intent —
// the host (RunAndCommit) decides whether to actually commit.
func autocommitBuiltin(ls *LoadedSave) func(*starlark.Thread, *starlark.Builtin, starlark.Tuple, []starlark.Tuple) (starlark.Value, error) {
	return func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		enabled := true
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "enabled?", &enabled); err != nil {
			return nil, err
		}
		ls.willCommit = enabled
		return starlark.None, nil
	}
}
