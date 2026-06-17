package thebibites

import "go.starlark.net/starlark"

// Globals returns the predeclared Starlark globals for a loaded save. It is the
// bridge consumed by script.Run: the returned dict provides `open()` (which hands
// out the loaded save's read/analytics surface as a Save value), the host
// aggregate builtins (sum/mean/median over small materialized lists), and
// `autocommit` (the commit-intent declaration consumed by RunAndCommit).
func Globals(ls *LoadedSave) starlark.StringDict {
	globals := starlark.StringDict{
		"open":       starlark.NewBuiltin("open", openBuiltin(ls)),
		"autocommit": starlark.NewBuiltin("autocommit", autocommitBuiltin(ls)),
	}
	for name, builtin := range hostAggregates() {
		globals[name] = builtin
	}
	return globals
}

// openBuiltin returns the `open(path=None)` builtin for the standalone,
// host-preloaded path. The host (runLoaded) has already Load-ed `ls`, so open()
// returns the Save bound to *that* LoadedSave — it must not re-Load or
// re-ExtractTables, or staged mutations would land on a throwaway save and never
// reach prepareCommit. The optional `path` arg is accepted and ignored so the
// surface stays forward-compatible with Track E's path-driven workspace.open.
func openBuiltin(ls *LoadedSave) func(*starlark.Thread, *starlark.Builtin, starlark.Tuple, []starlark.Tuple) (starlark.Value, error) {
	return func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var path string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "path?", &path); err != nil {
			return nil, err
		}
		_ = path
		return &Save{ls: ls}, nil
	}
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
