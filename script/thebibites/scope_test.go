package thebibites

import (
	"database/sql"
	"strings"
	"testing"

	"go.starlark.net/starlark"
)

// scope_test.go — white-box tests for the E3 SaveScope seam and the read-only
// gating it imposes on spanning EntityCollections. The aggregate push-down itself
// is exercised end-to-end over two real worlds in workspace/spanning_test.go; these
// tests lock the SQL-fragment shapes and the read-only invariants in isolation.

// TestWorkingScopeClauseByteIdentical asserts the default working scope emits
// exactly the pre-E3 hardcoded clause "<identity>.save_id = ?" / [saveID], so the
// single-save path is behavior-preserving.
func TestWorkingScopeClauseByteIdentical(t *testing.T) {
	s := workingScope{saveID: "world-42"}
	clause, args := s.scopeClause("bibites")
	if clause != `"bibites".save_id = ?` {
		t.Errorf("working scope clause = %q, want %q", clause, `"bibites".save_id = ?`)
	}
	if len(args) != 1 || args[0] != "world-42" {
		t.Errorf("working scope args = %v, want [world-42]", args)
	}
	if !s.writable() {
		t.Errorf("working scope must be writable")
	}
	if j := s.catalogJoin("bibites"); j != "" {
		t.Errorf("working scope catalogJoin = %q, want empty", j)
	}
	if s.catalogCols() != nil {
		t.Errorf("working scope must contribute no catalog columns")
	}
}

// TestWorldHistoryScopeClause asserts the one-world spanning scope filters by
// save_id IN (SELECT … WHERE world_id = ?) with the world id bound as an arg, is
// read-only, and contributes the catalog join + columns.
func TestWorldHistoryScopeClause(t *testing.T) {
	s := NewWorldHistoryScope("world-a")
	clause, args := s.scopeClause("eggs")
	want := `"eggs".save_id IN (SELECT save_id FROM "mirror_saves" WHERE world_id = ?)`
	if clause != want {
		t.Errorf("world-history clause = %q, want %q", clause, want)
	}
	if len(args) != 1 || args[0] != "world-a" {
		t.Errorf("world-history args = %v, want [world-a]", args)
	}
	if s.writable() {
		t.Errorf("spanning scope must NOT be writable")
	}
	if !strings.Contains(s.catalogJoin("eggs"), `LEFT JOIN "mirror_saves"`) {
		t.Errorf("world-history catalogJoin missing LEFT JOIN: %q", s.catalogJoin("eggs"))
	}
	cols := s.catalogCols()
	if cols["world_id"] != "world_id" || cols["sim_time"] != "sim_time" {
		t.Errorf("world-history catalogCols = %v, want world_id/sim_time", cols)
	}
}

// TestWorkspaceScopeClause asserts the all-worlds spanning scope filters by
// save_id IN (SELECT save_id FROM mirror_saves) with NO bound arg (no world
// filter), is read-only, and contributes the catalog join.
func TestWorkspaceScopeClause(t *testing.T) {
	s := NewWorkspaceScope()
	clause, args := s.scopeClause("bibites")
	want := `"bibites".save_id IN (SELECT save_id FROM "mirror_saves")`
	if clause != want {
		t.Errorf("workspace clause = %q, want %q", clause, want)
	}
	if len(args) != 0 {
		t.Errorf("workspace args = %v, want none", args)
	}
	if s.writable() {
		t.Errorf("workspace scope must NOT be writable")
	}
}

// TestSpanningCollectionAttrHidesMutation locks non-negotiable #3 at the type
// level: a read-only spanning collection exposes ONLY aggregate/where/group_by;
// set/set_expr/delete resolve to a clean AttributeError (nil, nil) and are absent
// from AttrNames.
func TestSpanningCollectionAttrHidesMutation(t *testing.T) {
	ls := loadFixture(t)
	for _, kind := range []string{"bibite", "egg", "pellet"} {
		c := &EntityCollection{ls: ls, kind: kind, scope: NewWorkspaceScope()}

		for _, mut := range []string{"set", "set_expr", "delete"} {
			v, err := c.Attr(mut)
			if err != nil {
				t.Errorf("[%s] Attr(%q) error = %v, want clean nil", kind, mut, err)
			}
			if v != nil {
				t.Errorf("[%s] Attr(%q) = %v, want nil (mutation hidden on spanning scope)", kind, mut, v)
			}
		}
		names := map[string]bool{}
		for _, n := range c.AttrNames() {
			names[n] = true
		}
		for _, mut := range []string{"set", "set_expr", "delete"} {
			if names[mut] {
				t.Errorf("[%s] AttrNames contains %q on a spanning scope", kind, mut)
			}
		}
		for _, agg := range []string{"count", "sum", "mean", "median", "min", "max", "quantile", "where", "group_by"} {
			if !names[agg] {
				t.Errorf("[%s] AttrNames missing aggregate/narrow method %q", kind, agg)
			}
			v, err := c.Attr(agg)
			if err != nil || v == nil {
				t.Errorf("[%s] Attr(%q) = (%v, %v), want a builtin", kind, agg, v, err)
			}
		}
	}
}

// TestSingleSaveCollectionStillExposesMutation guards the regression: a default
// (working-scope) collection keeps the full mutation surface.
func TestSingleSaveCollectionStillExposesMutation(t *testing.T) {
	ls := loadFixture(t)
	c := &EntityCollection{ls: ls, kind: "bibite"}
	names := map[string]bool{}
	for _, n := range c.AttrNames() {
		names[n] = true
	}
	for _, mut := range []string{"set", "set_expr", "delete"} {
		if !names[mut] {
			t.Errorf("single-save collection lost mutation method %q", mut)
		}
		v, err := c.Attr(mut)
		if err != nil || v == nil {
			t.Errorf("single-save Attr(%q) = (%v, %v), want a builtin", mut, v, err)
		}
	}
}

// TestSpanningMutationBuiltinRejectsLoudly is the belt-and-suspenders check: even
// if a mutation builtin is reached directly (bypassing the Attr gate), it rejects
// with a clean error naming the per-save path.
func TestSpanningMutationBuiltinRejectsLoudly(t *testing.T) {
	ls := loadFixture(t)
	c := &EntityCollection{ls: ls, kind: "bibite", scope: NewWorldHistoryScope("world-a")}

	thread := &starlark.Thread{}
	cases := []struct {
		name    string
		fn      func() (starlark.Value, error)
		wantSub string
	}{
		{"set", func() (starlark.Value, error) {
			return c.setBuiltin(thread, starlark.NewBuiltin("set", c.setBuiltin),
				starlark.Tuple{starlark.String("energy"), starlark.MakeInt(1)}, nil)
		}, "cannot set a spanning bibite collection"},
		{"set_expr", func() (starlark.Value, error) {
			return c.setExprBuiltin(thread, starlark.NewBuiltin("set_expr", c.setExprBuiltin),
				starlark.Tuple{starlark.String("energy"), starlark.String("energy + 1")}, nil)
		}, "cannot set_expr a spanning bibite collection"},
		{"delete", func() (starlark.Value, error) {
			return c.deleteBuiltin(thread, starlark.NewBuiltin("delete", c.deleteBuiltin),
				starlark.Tuple{}, nil)
		}, "cannot delete a spanning bibite collection"},
	}
	for _, tc := range cases {
		_, err := tc.fn()
		if err == nil {
			t.Errorf("%s on spanning collection did not error", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.wantSub) {
			t.Errorf("%s error = %q, want substring %q", tc.name, err.Error(), tc.wantSub)
		}
	}
	// No mutation must have been staged.
	if ls.stagedOps != 0 {
		t.Errorf("spanning mutation staged %d ops, want 0", ls.stagedOps)
	}
}

// TestSpanningCollectionNotIterable asserts the spanning collection is
// aggregate-only: Len/Iterate panic loudly (the script engine recovers this into a
// clean diagnostic) rather than silently iterating a working-scoped or empty set.
func TestSpanningCollectionNotIterable(t *testing.T) {
	ls := loadFixture(t)
	c := &EntityCollection{ls: ls, kind: "bibite", scope: NewWorkspaceScope()}

	func() {
		defer func() {
			r := recover()
			if r == nil {
				t.Errorf("Len() on spanning collection did not panic")
				return
			}
			if !strings.Contains(toString(r), "aggregate-only") {
				t.Errorf("Len() panic = %v, want 'aggregate-only'", r)
			}
		}()
		_ = c.Len()
	}()

	func() {
		defer func() {
			if recover() == nil {
				t.Errorf("Iterate() on spanning collection did not panic")
			}
		}()
		_ = c.Iterate()
	}()

	// EntryNames (the transfer-selection path) rejects with an error, not a panic.
	if _, err := c.EntryNames(); err == nil {
		t.Errorf("EntryNames() on spanning collection did not error")
	}

	// Truth must be safe (no panic) — a spanning collection is a truthy handle.
	if c.Truth() != starlark.True {
		t.Errorf("spanning collection Truth() = %v, want True", c.Truth())
	}
}

// TestSpanningReaderConstruction guards NewSpanningReader's arg validation and the
// nil-archive read path invariants (it builds a *LoadedSave with no parsed
// archive).
func TestSpanningReaderConstruction(t *testing.T) {
	if _, err := NewSpanningReader(nil, NewWorkspaceScope()); err == nil {
		t.Errorf("NewSpanningReader(nil db) did not error")
	}
	// A writable scope must be rejected — the reader is read-only by construction.
	ls := loadFixture(t)
	if _, err := NewSpanningReader(openTestDB(t, ls), workingScope{saveID: "x"}); err == nil {
		t.Errorf("NewSpanningReader(writable scope) did not error")
	}
	r, err := NewSpanningReader(openTestDB(t, ls), NewWorkspaceScope())
	if err != nil {
		t.Fatalf("NewSpanningReader: %v", err)
	}
	if r.ls.archive != nil {
		t.Errorf("spanning reader ls must carry no parsed archive")
	}
	for _, name := range []string{"bibites", "eggs", "pellets"} {
		coll, err := r.Collection(name)
		if err != nil {
			t.Fatalf("Collection(%q): %v", name, err)
		}
		if coll.scope == nil || coll.scope.writable() {
			t.Errorf("Collection(%q) scope must be read-only spanning", name)
		}
	}
	if _, err := r.Collection("zones"); err == nil {
		t.Errorf("Collection(unknown) did not error")
	}
}

// openTestDB lazily opens the fixture's in-memory analytics DB so the spanning
// reader has a real *sql.DB handle (its content is irrelevant to these construction
// tests — the cross-world aggregate is proven in the workspace integration tests).
func openTestDB(t *testing.T, ls *LoadedSave) *sql.DB {
	t.Helper()
	db, err := ls.openDB(ls.queryCtx())
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	return db
}

func toString(v any) string {
	if err, ok := v.(error); ok {
		return err.Error()
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
