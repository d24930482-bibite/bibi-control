package workspace

// spanning_test.go — E3 integration tests for the spanning entity collections
// (world.bibites / workspace.bibites etc.) over two real worlds. White-box (package
// workspace) so it can reach ws.duck()/ws.store() and the unexported spanning
// helpers, and reuse the world_test.go harness (newWorkspace/fixturePath/
// fixtureA/fixtureB).
//
// The load-bearing assertion is cross-world isolation (the E2 leak class): with two
// worlds of DIFFERENT row counts present, a world's spanning count must equal THAT
// world's own count and exclude the other's. A passing aggregate that silently drops
// the scope clause would read the SUM, so every isolation assertion checks the
// per-world count, never just "non-zero".

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/asemones/bibicontrol/script/thebibites"
	"go.starlark.net/starlark"
)

// callNoArg invokes a no-argument Starlark builtin (e.g. a collection's count()).
func callNoArg(t *testing.T, fn starlark.Value) (starlark.Value, error) {
	t.Helper()
	c, ok := fn.(starlark.Callable)
	if !ok {
		t.Fatalf("value %T is not callable", fn)
	}
	return starlark.Call(&starlark.Thread{}, c, nil, nil)
}

// callOneStr invokes a one-string-arg Starlark builtin (e.g. where(predicate)).
func callOneStr(t *testing.T, fn starlark.Value, arg string) (starlark.Value, error) {
	t.Helper()
	c, ok := fn.(starlark.Callable)
	if !ok {
		t.Fatalf("value %T is not callable", fn)
	}
	return starlark.Call(&starlark.Thread{}, c, starlark.Tuple{starlark.String(arg)}, nil)
}

// starlarkToInt64 extracts an int64 from a Starlark Int.
func starlarkToInt64(t *testing.T, v starlark.Value) int64 {
	t.Helper()
	i, ok := v.(starlark.Int)
	if !ok {
		t.Fatalf("value %T is not a Starlark Int (%v)", v, v)
	}
	n, ok := i.Int64()
	if !ok {
		t.Fatalf("Starlark Int %v overflows int64", i)
	}
	return n
}

// whereWorldID narrows a spanning collection by world_id (the friendly catalog
// column) — proving the predicate scopes WITHOUT a caller-written JOIN.
func whereWorldID(t *testing.T, c *thebibites.EntityCollection, worldID string) *thebibites.EntityCollection {
	t.Helper()
	whereFn, err := c.Attr("where")
	if err != nil {
		t.Fatalf("Attr(where): %v", err)
	}
	res, err := callOneStr(t, whereFn, "world_id = '"+worldID+"'")
	if err != nil {
		t.Fatalf("where(world_id): %v", err)
	}
	out, ok := res.(*thebibites.EntityCollection)
	if !ok {
		t.Fatalf("where returned %T, want *EntityCollection", res)
	}
	return out
}

// groupByCount runs c.group_by(col).count() and returns the dict as a Go map keyed
// by the group value's string form -> int64 count.
func groupByCount(t *testing.T, c *thebibites.EntityCollection, col string) map[string]int64 {
	t.Helper()
	gbFn, err := c.Attr("group_by")
	if err != nil {
		t.Fatalf("Attr(group_by): %v", err)
	}
	grouped, err := callOneStr(t, gbFn, col)
	if err != nil {
		t.Fatalf("group_by(%q): %v", col, err)
	}
	ga, ok := grouped.(starlark.HasAttrs)
	if !ok {
		t.Fatalf("group_by returned %T, want HasAttrs", grouped)
	}
	countFn, err := ga.Attr("count")
	if err != nil {
		t.Fatalf("grouped Attr(count): %v", err)
	}
	res, err := callNoArg(t, countFn)
	if err != nil {
		t.Fatalf("grouped count(): %v", err)
	}
	dict, ok := res.(*starlark.Dict)
	if !ok {
		t.Fatalf("grouped count returned %T, want *Dict", res)
	}
	out := make(map[string]int64, dict.Len())
	for _, item := range dict.Items() {
		key := item[0]
		var ks string
		switch k := key.(type) {
		case starlark.String:
			ks = string(k)
		default:
			ks = k.String()
		}
		out[ks] = starlarkToInt64(t, item[1])
	}
	return out
}

func groupByWorldCount(t *testing.T, c *thebibites.EntityCollection) map[string]int64 {
	return groupByCount(t, c, "world_id")
}

func groupBySimTimeCount(t *testing.T, c *thebibites.EntityCollection) map[string]int64 {
	return groupByCount(t, c, "sim_time")
}

// historyCount returns the committed-history row count for one world's entity table
// (bibites/eggs/pellets), via the scoped HistoryQuery escape hatch. This is the
// independent ground truth the spanning DSL must reproduce WITHOUT a caller JOIN.
func historyCount(t *testing.T, ctx context.Context, ws *Workspace, worldID, table string) int64 {
	t.Helper()
	rows, err := ws.HistoryQuery(ctx, worldID,
		"SELECT count(*) AS n FROM "+table+" JOIN world_saves USING (save_id)")
	if err != nil {
		t.Fatalf("HistoryQuery count %s for %s: %v", table, worldID, err)
	}
	if len(rows) != 1 {
		t.Fatalf("HistoryQuery count %s returned %d rows, want 1", table, len(rows))
	}
	return asInt64Col(t, rows[0]["n"])
}

// spanningCarrierTables maps a spanning kind name to the two carrier tables whose
// rows it unions, so the integration test's ground truth (the per-world history
// count the spanning DSL must reproduce) can sum over both carriers via HistoryQuery
// — independent of the spanning push-down under test.
var spanningCarrierTables = map[string][2]string{
	"genes":    {"bibite_genes", "egg_genes"},
	"nodes":    {"bibite_brain_nodes", "egg_brain_nodes"},
	"synapses": {"bibite_brain_synapses", "egg_brain_synapses"},
}

// historyCountUnion returns one world's committed-history row count for a spanning
// kind (genes/nodes/synapses) = count over carrier1 + carrier2, each scoped to this
// world via the world_saves CTE. This is the independent ground truth the spanning
// DSL must reproduce with NO caller JOIN. It mirrors historyCount but sums the two
// carrier tables a spanning kind unions.
func historyCountUnion(t *testing.T, ctx context.Context, ws *Workspace, worldID, kind string) int64 {
	t.Helper()
	tables, ok := spanningCarrierTables[kind]
	if !ok {
		t.Fatalf("unknown spanning kind %q", kind)
	}
	rows, err := ws.HistoryQuery(ctx, worldID,
		"SELECT (SELECT count(*) FROM "+tables[0]+" JOIN world_saves USING (save_id)) + "+
			"(SELECT count(*) FROM "+tables[1]+" JOIN world_saves USING (save_id)) AS n")
	if err != nil {
		t.Fatalf("HistoryQuery union count %s for %s: %v", kind, worldID, err)
	}
	if len(rows) != 1 {
		t.Fatalf("HistoryQuery union count %s returned %d rows, want 1", kind, len(rows))
	}
	return asInt64Col(t, rows[0]["n"])
}

func asInt64Col(t *testing.T, v any) int64 {
	t.Helper()
	switch x := v.(type) {
	case int64:
		return x
	case int32:
		return int64(x)
	case int:
		return int64(x)
	default:
		t.Fatalf("unexpected count type %T (%v)", v, v)
		return 0
	}
}

// spanCount runs a spanning collection's .count() and returns it as int64.
func spanCount(t *testing.T, c *thebibites.EntityCollection) int64 {
	t.Helper()
	v, err := c.Attr("count")
	if err != nil {
		t.Fatalf("Attr(count): %v", err)
	}
	res, err := callNoArg(t, v)
	if err != nil {
		t.Fatalf("count(): %v", err)
	}
	return starlarkToInt64(t, res)
}

// TestSpanningTwoWorlds is the consolidated read-only matrix over ONE shared
// two-world workspace (fixtureA + fixtureB, with DIFFERENT bibite/egg/pellet
// counts). Sharing the workspace across subtests keeps the (expensive) AddWorld
// imports to two for the whole read-only matrix while still covering every
// non-negotiable: cross-world isolation (the E2-leak guard), group_by('world_id'),
// the friendly world_id predicate, and the user-facing Starlark surface.
func TestSpanningTwoWorlds(t *testing.T) {
	ctx := testCtxAuto(t)
	ws := newWorkspace(t, ctx)

	worldA, err := ws.AddWorld(ctx, fixturePath(t, fixtureA), "world-a")
	if err != nil {
		t.Fatalf("AddWorld A: %v", err)
	}
	worldB, err := ws.AddWorld(ctx, fixturePath(t, fixtureB), "world-b")
	if err != nil {
		t.Fatalf("AddWorld B: %v", err)
	}

	// Per-world ground truth for each entity kind (the independent reference the
	// spanning DSL must reproduce with no caller JOIN).
	wantA := map[string]int64{}
	wantB := map[string]int64{}
	for _, table := range []string{"bibites", "eggs", "pellets"} {
		wantA[table] = historyCount(t, ctx, ws, worldA.ID, table)
		wantB[table] = historyCount(t, ctx, ws, worldB.ID, table)
	}

	// Isolation (the headline E2-leak guard) across bibites/eggs/pellets: each
	// world's spanning count == THAT world's own count; workspace == the sum.
	t.Run("isolation", func(t *testing.T) {
		for _, kind := range []string{"bibites", "eggs", "pellets"} {
			a, b := wantA[kind], wantB[kind]
			if a == 0 && b == 0 {
				t.Logf("%s: both worlds empty, skipping", kind)
				continue
			}
			if kind == "bibites" && a == b {
				t.Fatalf("fixtures must differ in bibite count (A=%d B=%d)", a, b)
			}
			if got := spanCount(t, mustColl(t, ctx, ws, thebibites.NewWorldHistoryScope(worldA.ID), kind)); got != a {
				t.Errorf("world A %s spanning count = %d, want %d (leak/scope bug)", kind, got, a)
			}
			if got := spanCount(t, mustColl(t, ctx, ws, thebibites.NewWorldHistoryScope(worldB.ID), kind)); got != b {
				t.Errorf("world B %s spanning count = %d, want %d (leak/scope bug)", kind, got, b)
			}
			if got := spanCount(t, mustColl(t, ctx, ws, thebibites.NewWorkspaceScope(), kind)); got != a+b {
				t.Errorf("workspace %s spanning count = %d, want %d (A+B)", kind, got, a+b)
			}
		}
	})

	// group_by('world_id') returns exactly the two world ids with per-world counts.
	t.Run("group_by_world_id", func(t *testing.T) {
		grouped := groupByWorldCount(t, mustColl(t, ctx, ws, thebibites.NewWorkspaceScope(), "bibites"))
		if len(grouped) != 2 {
			t.Fatalf("group_by('world_id') returned %d keys, want 2: %v", len(grouped), grouped)
		}
		if grouped[worldA.ID] != wantA["bibites"] {
			t.Errorf("group_by world A count = %d, want %d", grouped[worldA.ID], wantA["bibites"])
		}
		if grouped[worldB.ID] != wantB["bibites"] {
			t.Errorf("group_by world B count = %d, want %d", grouped[worldB.ID], wantB["bibites"])
		}
	})

	// The friendly world_id predicate narrows the all-worlds scope to one world,
	// equal to that world's own count, WITHOUT a caller JOIN.
	t.Run("where_world_id", func(t *testing.T) {
		coll := mustColl(t, ctx, ws, thebibites.NewWorkspaceScope(), "bibites")
		if got := spanCount(t, whereWorldID(t, coll, worldA.ID)); got != wantA["bibites"] {
			t.Errorf("workspace.bibites.where(world_id=A).count() = %d, want %d", got, wantA["bibites"])
		}
	})

	// The user-facing Starlark surface: cross-world isolation + a no-JOIN program.
	t.Run("starlark", func(t *testing.T) {
		prog := `
wa = workspace.world("` + worldA.ID + `")
wb = workspace.world("` + worldB.ID + `")
print(wa.bibites.count())
print(wb.bibites.count())
print(workspace.bibites.count())
print(len(workspace.bibites.group_by("world_id").count()))
m = wa.bibites.group_by("sim_time").mean("energy")
`
		res := mustRunAuto(t, ctx, ws, prog)
		lines := strings.Split(strings.TrimRight(res.Output, "\n"), "\n")
		if len(lines) != 4 {
			t.Fatalf("want 4 output lines, got %v\nOutput:\n%s", lines, res.Output)
		}
		if got := parseInt64(t, lines[0]); got != wantA["bibites"] {
			t.Errorf("world A bibites.count() = %d, want %d", got, wantA["bibites"])
		}
		if got := parseInt64(t, lines[1]); got != wantB["bibites"] {
			t.Errorf("world B bibites.count() = %d, want %d", got, wantB["bibites"])
		}
		if got := parseInt64(t, lines[2]); got != wantA["bibites"]+wantB["bibites"] {
			t.Errorf("workspace bibites.count() = %d, want %d (A+B)", got, wantA["bibites"]+wantB["bibites"])
		}
		if got := parseInt64(t, lines[3]); got != 2 {
			t.Errorf("group_by('world_id') key count = %d, want 2", got)
		}
	})

	// Perf shape (non-negotiable #5): repeated spanning reads on this unchanged
	// workspace trigger NO extra catalog rebuilds and NO extra scenes reads — the
	// catalog is fingerprint-gated and refreshed once per call (a cache hit when
	// unchanged), and each aggregate is one SELECT (no per-revision loop). Run last
	// so the earlier subtests have already warmed the catalog.
	t.Run("perf_shape", func(t *testing.T) {
		rebuildsBefore := ws.rebuildCount
		scenesBefore := ws.scenesReadCount
		for i := 0; i < 5; i++ {
			c := mustColl(t, ctx, ws, thebibites.NewWorkspaceScope(), "bibites")
			_ = spanCount(t, c)
			_ = spanCount(t, whereWorldID(t, c, worldA.ID))
		}
		if ws.rebuildCount != rebuildsBefore {
			t.Errorf("repeated spanning reads rebuilt the catalog %d extra times (want 0)", ws.rebuildCount-rebuildsBefore)
		}
		if ws.scenesReadCount != scenesBefore {
			t.Errorf("repeated spanning reads re-read scenes %d extra times (want 0)", ws.scenesReadCount-scenesBefore)
		}
	})

	// Mutation is rejected on the spanning scope through the Starlark surface (both
	// world.bibites and workspace.bibites), and the aggregate surface still works.
	t.Run("mutation_rejected", func(t *testing.T) {
		for _, expr := range []string{
			`workspace.world("` + worldA.ID + `").bibites.set("energy", 1)`,
			`workspace.world("` + worldA.ID + `").bibites.delete()`,
			`workspace.bibites.set_expr("energy", "energy + 1")`,
			`workspace.bibites.delete()`,
		} {
			_, err := runAuto(ctx, ws, expr)
			if err == nil {
				t.Errorf("expected error for spanning mutation %q", expr)
				continue
			}
			msg := err.Error()
			if !strings.Contains(msg, "has no") && !strings.Contains(msg, "attribute") &&
				!strings.Contains(msg, "spanning") && !strings.Contains(msg, "mutation is per-save") {
				t.Errorf("spanning mutation %q error = %q, want an attribute/spanning rejection", expr, msg)
			}
		}
		res := mustRunAuto(t, ctx, ws, `print(workspace.bibites.count() >= 0)`)
		if strings.TrimSpace(res.Output) != "True" {
			t.Errorf("workspace.bibites.count() sanity failed: %q", res.Output)
		}
	})

	// M1: the same cross-world matrix for the new spanning kinds (genes/nodes/
	// synapses). The headline invariant is the silent-leak guard: a dropped/missing
	// scope clause would make a per-world aggregate silently SUM across ALL worlds and
	// a count() would not notice — so each per-world spanning count is asserted against
	// THAT world's own (carrier-union) history count, and workspace == A+B. Reuses the
	// shared two-world workspace (no extra AddWorld imports).
	wantUnionA := map[string]int64{}
	wantUnionB := map[string]int64{}
	for _, kind := range []string{"genes", "nodes", "synapses"} {
		wantUnionA[kind] = historyCountUnion(t, ctx, ws, worldA.ID, kind)
		wantUnionB[kind] = historyCountUnion(t, ctx, ws, worldB.ID, kind)
	}

	t.Run("spanning_kinds_isolation", func(t *testing.T) {
		for _, kind := range []string{"genes", "nodes", "synapses"} {
			a, b := wantUnionA[kind], wantUnionB[kind]
			if a == 0 && b == 0 {
				t.Fatalf("%s: both worlds empty — fixtures must carry %s rows for the leak guard", kind, kind)
			}
			// world A's spanning kind count == A's own carrier-union count (NOT A+B).
			if got := spanCount(t, mustColl(t, ctx, ws, thebibites.NewWorldHistoryScope(worldA.ID), kind)); got != a {
				t.Errorf("world A %s spanning count = %d, want %d (cross-world leak/scope bug)", kind, got, a)
			}
			if got := spanCount(t, mustColl(t, ctx, ws, thebibites.NewWorldHistoryScope(worldB.ID), kind)); got != b {
				t.Errorf("world B %s spanning count = %d, want %d (cross-world leak/scope bug)", kind, got, b)
			}
			// workspace spanning kind count == A+B (the all-worlds aggregate).
			if got := spanCount(t, mustColl(t, ctx, ws, thebibites.NewWorkspaceScope(), kind)); got != a+b {
				t.Errorf("workspace %s spanning count = %d, want %d (A+B)", kind, got, a+b)
			}
		}
	})

	// group_by('world_id') on a spanning kind returns exactly the two worlds with
	// per-world counts — the catalog columns are scope-level, so this is free once the
	// kind registers.
	t.Run("spanning_kinds_group_by_world_id", func(t *testing.T) {
		grouped := groupByWorldCount(t, mustColl(t, ctx, ws, thebibites.NewWorkspaceScope(), "synapses"))
		if len(grouped) != 2 {
			t.Fatalf("synapses group_by('world_id') returned %d keys, want 2: %v", len(grouped), grouped)
		}
		if grouped[worldA.ID] != wantUnionA["synapses"] {
			t.Errorf("synapses group_by world A = %d, want %d", grouped[worldA.ID], wantUnionA["synapses"])
		}
		if grouped[worldB.ID] != wantUnionB["synapses"] {
			t.Errorf("synapses group_by world B = %d, want %d", grouped[worldB.ID], wantUnionB["synapses"])
		}
	})

	// A friendly-column predicate (synapses where("enabled"), genes where("name ==
	// ...")) scopes the all-worlds collection WITHOUT a caller JOIN. enabled-only count
	// must be <= total and the world_id predicate narrows to one world.
	t.Run("spanning_kinds_where_no_join", func(t *testing.T) {
		all := mustColl(t, ctx, ws, thebibites.NewWorkspaceScope(), "synapses")
		total := spanCount(t, all)
		enabledColl, err := all.Attr("where")
		if err != nil {
			t.Fatalf("synapses Attr(where): %v", err)
		}
		enRes, err := callOneStr(t, enabledColl, "enabled")
		if err != nil {
			t.Fatalf("synapses.where(enabled): %v", err)
		}
		enCount := spanCount(t, enRes.(*thebibites.EntityCollection))
		if enCount > total {
			t.Errorf("enabled synapse count %d > total %d (predicate not applied)", enCount, total)
		}
		// world_id predicate narrows to world A's own union count, no caller JOIN.
		genes := mustColl(t, ctx, ws, thebibites.NewWorkspaceScope(), "genes")
		if got := spanCount(t, whereWorldID(t, genes, worldA.ID)); got != wantUnionA["genes"] {
			t.Errorf("workspace.genes.where(world_id=A).count() = %d, want %d", got, wantUnionA["genes"])
		}
	})

	// The user-facing Starlark surface for the new kinds: a no-JOIN program over
	// world.genes / workspace.synapses / world.nodes.
	t.Run("spanning_kinds_starlark", func(t *testing.T) {
		prog := `
wa = workspace.world("` + worldA.ID + `")
print(wa.genes.count())
print(workspace.synapses.where("enabled").count() >= 0)
print(len(wa.nodes.group_by("world_id").count()))
g = workspace.genes.where("type == 'number'").mean("value")
`
		res := mustRunAuto(t, ctx, ws, prog)
		lines := strings.Split(strings.TrimRight(res.Output, "\n"), "\n")
		if len(lines) != 3 {
			t.Fatalf("want 3 output lines, got %v\nOutput:\n%s", lines, res.Output)
		}
		if got := parseInt64(t, lines[0]); got != wantUnionA["genes"] {
			t.Errorf("world A genes.count() = %d, want %d", got, wantUnionA["genes"])
		}
		if strings.TrimSpace(lines[1]) != "True" {
			t.Errorf("workspace.synapses.where(enabled).count() >= 0 = %q, want True", lines[1])
		}
		if got := parseInt64(t, lines[2]); got != 1 {
			t.Errorf("world A nodes.group_by('world_id') key count = %d, want 1", got)
		}
	})

	// Mutation is rejected on the spanning scope for the new kinds too (inherited
	// read-only gate): workspace.genes.set(...) / world.synapses.delete().
	t.Run("spanning_kinds_mutation_rejected", func(t *testing.T) {
		for _, expr := range []string{
			`workspace.genes.set("value", 1)`,
			`workspace.world("` + worldA.ID + `").synapses.delete()`,
			`workspace.world("` + worldA.ID + `").nodes.set("value", 0)`,
		} {
			_, err := runAuto(ctx, ws, expr)
			if err == nil {
				t.Errorf("expected error for spanning mutation %q", expr)
				continue
			}
			msg := err.Error()
			if !strings.Contains(msg, "has no") && !strings.Contains(msg, "attribute") &&
				!strings.Contains(msg, "spanning") && !strings.Contains(msg, "mutation is per-save") {
				t.Errorf("spanning mutation %q error = %q, want an attribute/spanning rejection", expr, msg)
			}
		}
	})
}

// mustColl refreshes the catalog once and returns the named spanning collection,
// fataling on error. It collapses the spanningReader+Collection boilerplate the
// subtests repeat.
func mustColl(t *testing.T, ctx context.Context, ws *Workspace, scope thebibites.SaveScope, kind string) *thebibites.EntityCollection {
	t.Helper()
	coll, err := ws.spanningCollection(ctx, scope, kind)
	if err != nil {
		t.Fatalf("spanningCollection(%q): %v", kind, err)
	}
	return coll
}

// TestSpanningHistorySpansRevisions proves history retention through the DSL: after a
// second commit of world A, world_a.bibites.group_by('sim_time') spans both
// revisions' partitions (more rows than a single revision).
func TestSpanningHistorySpansRevisions(t *testing.T) {
	ctx := testCtxAuto(t)
	ws := newWorkspace(t, ctx)

	worldA, err := ws.AddWorld(ctx, fixturePath(t, fixtureA), "world-a")
	if err != nil {
		t.Fatalf("AddWorld A: %v", err)
	}
	oneRevCount := historyCount(t, ctx, ws, worldA.ID, "bibites")

	// Commit a 2nd revision (a distinct energy set on one bibite produces distinct
	// bytes -> a new sha256 -> a retained 2nd history partition). Reuses the proven
	// commit_test helpers.
	entry := firstBibiteEntryName(t, ctx, ws, worldA.ID)
	if _, err := ws.CommitWorld(ctx, worldA.ID, setBibiteEnergy(entry, 123.0), thebibites.RunOptions{}); err != nil {
		t.Fatalf("CommitWorld #2: %v", err)
	}

	twoRevCount := historyCount(t, ctx, ws, worldA.ID, "bibites")
	if twoRevCount <= oneRevCount {
		t.Fatalf("second commit did not add a history partition (1rev=%d, 2rev=%d)", oneRevCount, twoRevCount)
	}

	reader, err := ws.spanningReader(ctx, thebibites.NewWorldHistoryScope(worldA.ID))
	if err != nil {
		t.Fatalf("spanningReader: %v", err)
	}
	coll, err := reader.Collection("bibites")
	if err != nil {
		t.Fatalf("Collection: %v", err)
	}
	// The spanning count over the world's whole history equals the two-revision total.
	if got := spanCount(t, coll); got != twoRevCount {
		t.Errorf("world history spanning count = %d, want %d (both revisions)", got, twoRevCount)
	}
	// group_by('sim_time') must return at least one bucket; with two revisions of
	// differing sim_time it is two (committed history carries per-revision sim_time).
	buckets := groupBySimTimeCount(t, coll)
	if len(buckets) == 0 {
		t.Errorf("group_by('sim_time') returned no buckets")
	}
	var sum int64
	for _, n := range buckets {
		sum += n
	}
	if sum != twoRevCount {
		t.Errorf("group_by('sim_time') bucket sum = %d, want %d", sum, twoRevCount)
	}
}

func parseInt64(t *testing.T, s string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		t.Fatalf("parse int %q: %v", s, err)
	}
	return n
}
