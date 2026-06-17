package workspace

// query_test.go — white-box tests for query.go (C4).
//
// Tests are in package workspace (same package as the production code) so they
// can reach unexported helpers (ws.duck(), ws.store(), ensureReadOnly, etc.) and
// reuse the test helpers declared in world_test.go in the same package:
//   - newWorkspace(t, ctx) *Workspace
//   - fixturePath(t, name) string
//   - fixtureA, fixtureB
//   - countBySaveID(t, ctx, ws, table, saveID) int64

import (
	"context"
	"errors"
	"testing"
)

// TestQueryWholeWorkspaceSpansAllWorlds confirms that Query (no world filter)
// returns rows for all worlds' history partitions. After AddWorld×2, a SELECT
// over save_archives must contain both worlds' revision sha256s.
func TestQueryWholeWorkspaceSpansAllWorlds(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	worldA, err := ws.AddWorld(ctx, fixturePath(t, fixtureA), "world-a")
	if err != nil {
		t.Fatalf("AddWorld A: %v", err)
	}
	worldB, err := ws.AddWorld(ctx, fixturePath(t, fixtureB), "world-b")
	if err != nil {
		t.Fatalf("AddWorld B: %v", err)
	}

	// Collect the revision sha256s for both worlds.
	revsA, err := ws.store().RevisionsForWorld(ctx, worldA.ID)
	if err != nil {
		t.Fatalf("RevisionsForWorld A: %v", err)
	}
	revsB, err := ws.store().RevisionsForWorld(ctx, worldB.ID)
	if err != nil {
		t.Fatalf("RevisionsForWorld B: %v", err)
	}
	if len(revsA) != 1 || len(revsB) != 1 {
		t.Fatalf("expected 1 revision per world, got A=%d B=%d", len(revsA), len(revsB))
	}
	shaA := revsA[0].SHA256
	shaB := revsB[0].SHA256

	rows, err := ws.Query(ctx, "SELECT save_id, count(*) AS n FROM save_archives GROUP BY save_id")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	foundA, foundB := false, false
	for _, row := range rows {
		id, _ := row["save_id"].(string)
		if id == shaA {
			foundA = true
		}
		if id == shaB {
			foundB = true
		}
	}
	if !foundA {
		t.Errorf("Query result missing world A history sha256 %q", shaA)
	}
	if !foundB {
		t.Errorf("Query result missing world B history sha256 %q", shaB)
	}
}

// TestQueryAttributesViaMirrorCatalog verifies that mirror_saves is populated
// and that a JOIN between save_archives and mirror_saves provides world_id
// attribution, with one row group per world.
func TestQueryAttributesViaMirrorCatalog(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	worldA, err := ws.AddWorld(ctx, fixturePath(t, fixtureA), "world-a")
	if err != nil {
		t.Fatalf("AddWorld A: %v", err)
	}
	worldB, err := ws.AddWorld(ctx, fixturePath(t, fixtureB), "world-b")
	if err != nil {
		t.Fatalf("AddWorld B: %v", err)
	}

	rows, err := ws.Query(ctx,
		"SELECT m.world_id, count(*) AS n FROM save_archives s "+
			"JOIN mirror_saves m ON s.save_id = m.save_id GROUP BY m.world_id")
	if err != nil {
		t.Fatalf("Query with mirror_saves JOIN: %v", err)
	}

	if len(rows) != 2 {
		t.Fatalf("expected 2 world rows, got %d: %v", len(rows), rows)
	}

	ids := map[string]bool{}
	for _, row := range rows {
		wid, _ := row["world_id"].(string)
		n, _ := row["n"]
		var count int64
		switch v := n.(type) {
		case int64:
			count = v
		case int32:
			count = int64(v)
		}
		if count < 1 {
			t.Errorf("world %q has count %d < 1", wid, count)
		}
		ids[wid] = true
	}
	if !ids[worldA.ID] {
		t.Errorf("world A id %q missing from attribution result", worldA.ID)
	}
	if !ids[worldB.ID] {
		t.Errorf("world B id %q missing from attribution result", worldB.ID)
	}
}

// TestHistoryQueryScopesToOneWorld verifies that HistoryQuery with the
// world_saves CTE restricts results to the specified world's revisions only.
func TestHistoryQueryScopesToOneWorld(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	worldA, err := ws.AddWorld(ctx, fixturePath(t, fixtureA), "world-a")
	if err != nil {
		t.Fatalf("AddWorld A: %v", err)
	}
	worldB, err := ws.AddWorld(ctx, fixturePath(t, fixtureB), "world-b")
	if err != nil {
		t.Fatalf("AddWorld B: %v", err)
	}

	// Collect sha256s so we can assert cross-world isolation.
	revsA, _ := ws.store().RevisionsForWorld(ctx, worldA.ID)
	revsB, _ := ws.store().RevisionsForWorld(ctx, worldB.ID)
	shaA := revsA[0].SHA256
	shaB := revsB[0].SHA256

	// HistoryQuery on worldA should count exactly the history rows for A (1 revision).
	rowsA, err := ws.HistoryQuery(ctx, worldA.ID,
		"SELECT count(*) AS n FROM save_archives s JOIN world_saves w ON s.save_id = w.save_id")
	if err != nil {
		t.Fatalf("HistoryQuery A: %v", err)
	}
	if len(rowsA) != 1 {
		t.Fatalf("HistoryQuery A: expected 1 result row, got %d", len(rowsA))
	}
	nA := rowsA[0]["n"]
	var countA int64
	switch v := nA.(type) {
	case int64:
		countA = v
	case int32:
		countA = int64(v)
	}
	if countA != 1 {
		t.Errorf("HistoryQuery A count = %d, want 1", countA)
	}

	// HistoryQuery on worldB must not return world A's sha256.
	rowsB, err := ws.HistoryQuery(ctx, worldB.ID,
		"SELECT s.save_id FROM save_archives s JOIN world_saves w ON s.save_id = w.save_id")
	if err != nil {
		t.Fatalf("HistoryQuery B: %v", err)
	}
	for _, row := range rowsB {
		id, _ := row["save_id"].(string)
		if id == shaA {
			t.Errorf("HistoryQuery on worldB returned worldA sha256 %q", shaA)
		}
	}

	// And worldA's query must not return worldB's sha256.
	rowsA2, err := ws.HistoryQuery(ctx, worldA.ID,
		"SELECT s.save_id FROM save_archives s JOIN world_saves w ON s.save_id = w.save_id")
	if err != nil {
		t.Fatalf("HistoryQuery A (sha check): %v", err)
	}
	for _, row := range rowsA2 {
		id, _ := row["save_id"].(string)
		if id == shaB {
			t.Errorf("HistoryQuery on worldA returned worldB sha256 %q", shaB)
		}
	}
}

// TestHistoryQueryCarriesSimTime verifies that mirror_saves.sim_time is
// populated from scenes.simulated_time (the history partition value, not the
// worlds.sim_time SQLite head value). The world.SimTime from AddWorld is the
// same value — but only because the fixture has one revision; the test cross-
// checks both to confirm the scenes source is wired correctly.
func TestHistoryQueryCarriesSimTime(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	world, err := ws.AddWorld(ctx, fixturePath(t, fixtureA), "world-a")
	if err != nil {
		t.Fatalf("AddWorld: %v", err)
	}

	// Skip if the fixture carries no simulated_time (has_simulated_time = false).
	if world.SimTime == nil {
		t.Skip("fixtureA carries no sim_time; skipping sim_time test")
	}

	rows, err := ws.HistoryQuery(ctx, world.ID,
		"SELECT m.sim_time FROM mirror_saves m JOIN world_saves w ON m.save_id = w.save_id")
	if err != nil {
		t.Fatalf("HistoryQuery sim_time: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 mirror_saves row, got %d", len(rows))
	}

	rawSimTime := rows[0]["sim_time"]
	if rawSimTime == nil {
		t.Fatalf("mirror_saves.sim_time is NULL; want %v (scenes source not wired)", *world.SimTime)
	}

	var gotSimTime float64
	switch v := rawSimTime.(type) {
	case float64:
		gotSimTime = v
	case float32:
		gotSimTime = float64(v)
	default:
		t.Fatalf("mirror_saves.sim_time has unexpected type %T: %v", rawSimTime, rawSimTime)
	}

	const tol = 1e-6

	// Cross-check directly against scenes.simulated_time keyed by the revision
	// sha256 (the history partition value). This is the actual source the catalog
	// must read — distinct from the worlds.sim_time SQLite head value. Asserting
	// equality with the scenes value (not just world.SimTime) pins reinterpretation
	// (c): per-revision sim_time comes from scenes, not the SQLite head column.
	revs, err := ws.store().RevisionsForWorld(ctx, world.ID)
	if err != nil {
		t.Fatalf("RevisionsForWorld: %v", err)
	}
	if len(revs) != 1 {
		t.Fatalf("expected 1 revision, got %d", len(revs))
	}
	sha := revs[0].SHA256

	var sceneSimTime float64
	if err := ws.duck().QueryRowContext(ctx,
		"SELECT simulated_time FROM scenes WHERE save_id = ? AND has_simulated_time LIMIT 1", sha,
	).Scan(&sceneSimTime); err != nil {
		t.Fatalf("direct scenes.simulated_time lookup for sha %q: %v", sha, err)
	}

	if diff := gotSimTime - sceneSimTime; diff < -tol || diff > tol {
		t.Errorf("mirror_saves.sim_time = %v, want scenes.simulated_time %v (catalog must source from scenes, not worlds.sim_time; diff %v)",
			gotSimTime, sceneSimTime, diff)
	}

	// And it must match the head value too (identical for a single-revision
	// fixture); a mismatch would mean the scene value diverged from the head.
	if diff := gotSimTime - *world.SimTime; diff < -tol || diff > tol {
		t.Errorf("mirror_saves.sim_time = %v, want head %v (diff %v exceeds tol %v)",
			gotSimTime, *world.SimTime, diff, tol)
	}
}

// TestReadOnlyRejectsMutations verifies that Query and HistoryQuery reject
// non-SELECT statements and chained statements before touching the database.
func TestReadOnlyRejectsMutations(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	world, err := ws.AddWorld(ctx, fixturePath(t, fixtureA), "world-a")
	if err != nil {
		t.Fatalf("AddWorld: %v", err)
	}

	revsA, err := ws.store().RevisionsForWorld(ctx, world.ID)
	if err != nil {
		t.Fatalf("RevisionsForWorld: %v", err)
	}
	sha := revsA[0].SHA256

	// Record the row count before the mutation attempts.
	baseline := countBySaveID(t, ctx, ws, "save_archives", sha)

	mutationCases := []string{
		"INSERT INTO save_archives (save_id) VALUES ('x')",
		"UPDATE save_archives SET sha256 = 'x'",
		"DELETE FROM save_archives",
		"DROP TABLE save_archives",
		"ATTACH 'x.db'",
		"PRAGMA database_list",
		"SELECT 1; DELETE FROM save_archives",
		"  -- comment\n  DELETE FROM save_archives",
		"/* block */ INSERT INTO save_archives (save_id) VALUES ('y')",
		// CTE-wrapped mutations: DuckDB executes the mutation that follows (or is
		// nested inside) a CTE list. A leading WITH must NOT wave these through —
		// this is the bypass the reviewer found (WITH ... DELETE deletes a
		// retained history-partition row through the public Query API).
		"WITH x AS (SELECT 1) DELETE FROM save_archives",
		"WITH x AS (SELECT 1) INSERT INTO save_archives (save_id) VALUES ('z')",
		"WITH x AS (SELECT 1) UPDATE save_archives SET save_id = save_id",
		// Mutation nested inside the CTE body (the depth-agnostic scan catches it).
		"WITH x AS (DELETE FROM save_archives RETURNING save_id) SELECT * FROM x",
	}

	for _, stmt := range mutationCases {
		t.Run("Query/"+stmt[:min(30, len(stmt))], func(t *testing.T) {
			_, qErr := ws.Query(ctx, stmt)
			if !errors.Is(qErr, ErrReadOnlyQuery) {
				t.Errorf("Query(%q): got %v, want ErrReadOnlyQuery", stmt, qErr)
			}
		})
		t.Run("HistoryQuery/"+stmt[:min(30, len(stmt))], func(t *testing.T) {
			_, hErr := ws.HistoryQuery(ctx, world.ID, stmt)
			if !errors.Is(hErr, ErrReadOnlyQuery) {
				t.Errorf("HistoryQuery(%q): got %v, want ErrReadOnlyQuery", stmt, hErr)
			}
		})
	}

	// Confirm the database is unchanged.
	after := countBySaveID(t, ctx, ws, "save_archives", sha)
	if after != baseline {
		t.Errorf("save_archives row count changed from %d to %d after rejected mutations", baseline, after)
	}

	// Accepted cases: plain SELECT and WITH … SELECT.
	acceptedCases := []string{
		"SELECT 1",
		"WITH x AS (SELECT 1) SELECT * FROM x",
		"SELECT 1;",        // trailing semicolon only
		"  SELECT 1  ",     // leading/trailing whitespace
		"-- comment\nSELECT 1", // leading line comment
		"/* block */\nSELECT 1", // leading block comment
	}
	for _, stmt := range acceptedCases {
		t.Run("Query/accept/"+stmt[:min(30, len(stmt))], func(t *testing.T) {
			_, err := ws.Query(ctx, stmt)
			if errors.Is(err, ErrReadOnlyQuery) {
				t.Errorf("Query(%q) was rejected, want accepted", stmt)
			}
		})
	}
}

// TestQueryRejectsBeforeTouchingDB confirms that ensureReadOnly rejects
// non-SELECT statements on an empty workspace (no worlds) before any DB I/O.
func TestQueryRejectsBeforeTouchingDB(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx) // no worlds added

	_, err := ws.Query(ctx, "DELETE FROM save_archives")
	if !errors.Is(err, ErrReadOnlyQuery) {
		t.Errorf("Query on empty workspace: got %v, want ErrReadOnlyQuery", err)
	}

	_, err = ws.HistoryQuery(ctx, "nonexistent-world-id", "INSERT INTO save_archives (save_id) VALUES ('x')")
	if !errors.Is(err, ErrReadOnlyQuery) {
		t.Errorf("HistoryQuery on empty workspace: got %v, want ErrReadOnlyQuery", err)
	}
}

// min is a tiny helper so we can truncate statement strings in test names on
// Go versions < 1.21 that lack builtin min. (Go 1.21+ has it, but define it
// inline so the package compiles on go 1.21 toolchains already in use.)
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
