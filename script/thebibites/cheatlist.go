package thebibites

// DSLCheatlistTypes returns the static member lists for the thebibites DSL types
// that expose a fixed AttrNames(). This is the single source of truth consumed by
// both the cheatlist generator (cmd/gen_dsl_cheatlist) and the cross-check test
// (TestCheatlistMatchesBindings). Every list is populated by calling AttrNames()
// on a zero or lightweight instance — no string is typed twice.
//
// Keys match the AC_TYPE_MEMBERS key names in api/web/app.js.
//
// The "collection_readonly" key holds the spanning/read-only branch
// (workspace.bibites, world.nodes, etc.); "collection" holds the mutable
// single-save branch (s.bibites, s.eggs, etc.). AC_TYPE_MEMBERS.collection is the
// mutable branch by construction (the cross-check asserts this explicitly).
func DSLCheatlistTypes() map[string][]string {
	// collection — two branches.
	roCollection := &EntityCollection{scope: NewWorkspaceScope()} // read-only
	mutableCollection := &EntityCollection{}                      // mutable (nil scope → working)

	return map[string][]string{
		"session":             (&Save{}).AttrNames(),
		"collection_readonly": roCollection.AttrNames(),
		"collection":          mutableCollection.AttrNames(),
		"settings":            (&Settings{}).AttrNames(),
		"setting_scope":       (&SettingScope{}).AttrNames(),
		"gene":                (&Gene{}).AttrNames(),
		"gene_collection":     (&GeneCollection{}).AttrNames(),
		"setting":             (&Setting{}).AttrNames(),
	}
}
