package workspace

// DSLCheatlistWorkspaceTypes returns the static member lists for the workspace
// package DSL types. It is the single source of truth for the cheatlist generator
// and cross-check test; every list is populated by calling AttrNames() on a zero
// value so strings are not duplicated.
//
// Keys match the AC_TYPE_MEMBERS key names in api/web/app.js.
func DSLCheatlistWorkspaceTypes() map[string][]string {
	return map[string][]string{
		"workspace": (&workspaceValue{}).AttrNames(),
		"world":     (&worldValue{}).AttrNames(),
		"node":      (&nodeValue{}).AttrNames(),
	}
}
