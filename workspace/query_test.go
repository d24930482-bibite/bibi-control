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
	"strings"
	"testing"

	"github.com/asemones/bibicontrol/blobstore"
	"github.com/asemones/bibicontrol/revisionstore"
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

// --- C4b: catalog cache + invalidation + batched-rebuild perf-shape tests ---
//
// These read w.rebuildCount / w.scenesReadCount / w.insertExecCount, the
// test-only instrumentation seams bumped inside refreshMirrorCatalog. They are
// safe to read directly here because each test runs its queries sequentially in
// one goroutine (no concurrent mutator), and the rebuild always writes them
// under w.mu before the query call returns.

// TestCatalogCachedAcrossRepeatQueries is the core "N queries don't trigger N
// rebuilds" proof: after two AddWorlds, K repeat Query calls with no
// intervening mutation rebuild exactly once (first builds, rest are cache hits).
func TestCatalogCachedAcrossRepeatQueries(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	if _, err := ws.AddWorld(ctx, fixturePath(t, fixtureB), "world-b"); err != nil {
		t.Fatalf("AddWorld: %v", err)
	}

	const K = 5
	for i := 0; i < K; i++ {
		if _, err := ws.Query(ctx, "SELECT count(*) AS n FROM save_archives"); err != nil {
			t.Fatalf("Query #%d: %v", i, err)
		}
	}

	if ws.rebuildCount != 1 {
		t.Fatalf("rebuildCount = %d after %d repeat queries, want 1 (cache must short-circuit)", ws.rebuildCount, K)
	}
}

// TestCatalogRebuildsOnNewRevision proves a new revision moves the fingerprint
// and forces exactly one additional rebuild — and that the steady state between
// is a cache hit. A second AddWorld is the new-revision trigger.
func TestCatalogRebuildsOnNewRevision(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	world, err := ws.AddWorld(ctx, fixturePath(t, fixtureA), "world-a")
	if err != nil {
		t.Fatalf("AddWorld A: %v", err)
	}

	// First query builds (#1).
	if _, err := ws.Query(ctx, "SELECT count(*) AS n FROM save_archives"); err != nil {
		t.Fatalf("Query #1: %v", err)
	}
	if ws.rebuildCount != 1 {
		t.Fatalf("rebuildCount = %d after first query, want 1", ws.rebuildCount)
	}

	// Second query with no mutation is a cache hit (still #1).
	if _, err := ws.Query(ctx, "SELECT count(*) AS n FROM save_archives"); err != nil {
		t.Fatalf("Query #2 (cache hit): %v", err)
	}
	if ws.rebuildCount != 1 {
		t.Fatalf("rebuildCount = %d after cache-hit query, want 1", ws.rebuildCount)
	}

	// A new revision moves the fingerprint (new max id + new count). Recorded
	// cheaply via the store (advancing world A's head) — the invalidation keys on
	// the registry, not on which mutator produced the revision.
	importRevs, err := ws.store().RevisionsForWorld(ctx, world.ID)
	if err != nil {
		t.Fatalf("RevisionsForWorld: %v", err)
	}
	run, err := ws.store().RecordScriptRun(ctx, revisionstore.ScriptRunInput{
		ScriptSHA256: importRevs[0].SHA256,
		Status:       "succeeded",
	})
	if err != nil {
		t.Fatalf("RecordScriptRun: %v", err)
	}
	newRef := blobstore.Ref{SHA256: strings.Repeat("c", blobstore.SHA256HexLength), Size: 99}
	if _, err := ws.store().RecordRevisionAdvancingHead(ctx, world.ID, nil, revisionstore.RevisionInput{
		ParentID:    &importRevs[0].ID,
		WorldID:     world.ID,
		BlobRef:     newRef,
		ScriptRunID: run.ID,
	}); err != nil {
		t.Fatalf("RecordRevisionAdvancingHead: %v", err)
	}
	if _, err := ws.Query(ctx, "SELECT count(*) AS n FROM save_archives"); err != nil {
		t.Fatalf("Query #3 (after new revision): %v", err)
	}
	if ws.rebuildCount != 2 {
		t.Fatalf("rebuildCount = %d after new revision, want 2 (fingerprint must move on new revision)", ws.rebuildCount)
	}
}

// TestCatalogRebuildsOnTierFlip is the correctness proof for the StateSum
// dimension: an in-place G2 eviction (full,present -> mirror_only,absent) on a
// non-head revision invalidates the cache (Count/MaxID unchanged) AND the
// rebuilt mirror_saves row reflects the new tier/blob_present. A relocated-but-
// still-stale cache would ship the OLD tier here.
func TestCatalogRebuildsOnTierFlip(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	// Build a world with >=2 revisions so there is a non-head revision eligible
	// for eviction. The import is the first revision; a cheap store-recorded
	// revision advancing the head makes the import the non-head one (no DuckDB
	// import / Starlark commit cost). The import's blob is non-inline, so it has
	// deletable backing bytes for the G2 flip.
	world, err := ws.AddWorld(ctx, fixturePath(t, fixtureA), "world-a")
	if err != nil {
		t.Fatalf("AddWorld: %v", err)
	}
	importRevs, err := ws.store().RevisionsForWorld(ctx, world.ID)
	if err != nil {
		t.Fatalf("RevisionsForWorld (import): %v", err)
	}
	if len(importRevs) != 1 {
		t.Fatalf("want 1 import revision, got %d", len(importRevs))
	}
	nonHead := importRevs[0] // becomes non-head after the next revision advances the head

	// Record a new head revision with a DISTINCT (synthetic, non-inline) blob ref
	// so the import's sha256 keeps refcount 1 and is evictable. The head's bytes
	// need not exist on disk: this test only evicts the non-head and the catalog
	// flip is a pure SQLite op; the head blob is never loaded.
	run, err := ws.store().RecordScriptRun(ctx, revisionstore.ScriptRunInput{
		ScriptSHA256: nonHead.SHA256,
		Status:       "succeeded",
	})
	if err != nil {
		t.Fatalf("RecordScriptRun: %v", err)
	}
	headRef := blobstore.Ref{
		SHA256: strings.Repeat("b", blobstore.SHA256HexLength),
		Size:   1234,
	}
	if _, err := ws.store().RecordRevisionAdvancingHead(ctx, world.ID, nil, revisionstore.RevisionInput{
		ParentID:    &nonHead.ID,
		WorldID:     world.ID,
		BlobRef:     headRef,
		ScriptRunID: run.ID,
	}); err != nil {
		t.Fatalf("RecordRevisionAdvancingHead: %v", err)
	}

	// Build (#1), then a cache hit.
	if _, err := ws.Query(ctx, "SELECT count(*) AS n FROM save_archives"); err != nil {
		t.Fatalf("Query #1: %v", err)
	}
	if _, err := ws.Query(ctx, "SELECT count(*) AS n FROM save_archives"); err != nil {
		t.Fatalf("Query #2 (cache hit): %v", err)
	}
	if ws.rebuildCount != 1 {
		t.Fatalf("rebuildCount = %d before flip, want 1", ws.rebuildCount)
	}

	// G2 in-place flip on the non-head revision.
	if err := ws.EvictRevisionBlob(ctx, nonHead.ID); err != nil {
		t.Fatalf("EvictRevisionBlob: %v", err)
	}

	// Next query must rebuild (the StateSum moved even though Count/MaxID did not).
	rows, err := ws.HistoryQuery(ctx, world.ID,
		"SELECT m.tier AS tier, m.blob_present AS blob_present FROM mirror_saves m "+
			"JOIN world_saves w ON m.save_id = w.save_id WHERE m.save_id = '"+nonHead.SHA256+"'")
	if err != nil {
		t.Fatalf("HistoryQuery after flip: %v", err)
	}
	if ws.rebuildCount != 2 {
		t.Fatalf("rebuildCount = %d after in-place tier flip, want 2 (StateSum must invalidate the cache)", ws.rebuildCount)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 mirror_saves row for evicted sha, got %d", len(rows))
	}
	gotTier, _ := rows[0]["tier"].(string)
	if gotTier != "mirror_only" {
		t.Errorf("mirror_saves.tier = %q after evict, want mirror_only (stale cache would show 'full')", gotTier)
	}
	present, ok := rows[0]["blob_present"].(bool)
	if !ok {
		t.Fatalf("blob_present has unexpected type %T: %v", rows[0]["blob_present"], rows[0]["blob_present"])
	}
	if present {
		t.Errorf("mirror_saves.blob_present = true after evict, want false")
	}
}

// TestBatchedRebuildSingleScenesRead is the relocation guard: one rebuild over R
// revisions performs exactly ONE set-based scenes read and ceil(R/chunk) INSERT
// execs (NOT R) — proving the per-revision DuckDB loop (the original N+1) is
// gone, not relocated.
func TestBatchedRebuildSingleScenesRead(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	// Several revisions in one workspace so R > 1 and the per-revision-vs-batched
	// distinction is observable. Two AddWorlds give two revisions; a third is
	// recorded directly against world A via the store (cheap — no DuckDB import,
	// no Starlark commit) so the test exercises R>=3 without the heavy CommitWorld
	// cost. A revision with no scene import simply maps to NULL sim_time via
	// map-miss, which is also a fidelity check.
	worldA, err := ws.AddWorld(ctx, fixturePath(t, fixtureA), "world-a")
	if err != nil {
		t.Fatalf("AddWorld A: %v", err)
	}
	if _, err := ws.AddWorld(ctx, fixturePath(t, fixtureB), "world-b"); err != nil {
		t.Fatalf("AddWorld B: %v", err)
	}

	// Cheap third revision: reuse world A's head blob ref + a fresh script run,
	// advancing world A's head. No DuckDB import is performed for this revision.
	headRevs, err := ws.store().RevisionsForWorld(ctx, worldA.ID)
	if err != nil {
		t.Fatalf("RevisionsForWorld A: %v", err)
	}
	run, err := ws.store().RecordScriptRun(ctx, revisionstore.ScriptRunInput{
		ScriptSHA256: headRevs[0].SHA256,
		Status:       "succeeded",
	})
	if err != nil {
		t.Fatalf("RecordScriptRun: %v", err)
	}
	if _, err := ws.store().RecordRevisionAdvancingHead(ctx, worldA.ID, nil, revisionstore.RevisionInput{
		ParentID:    &headRevs[0].ID,
		WorldID:     worldA.ID,
		BlobRef:     headRevs[0].BlobRef,
		ScriptRunID: run.ID,
	}); err != nil {
		t.Fatalf("RecordRevisionAdvancingHead: %v", err)
	}

	revs, err := ws.store().RevisionsForWorkspace(ctx, ws.ID())
	if err != nil {
		t.Fatalf("RevisionsForWorkspace: %v", err)
	}
	r := len(revs)
	if r < 3 {
		t.Fatalf("expected >=3 revisions for a meaningful batched-shape proof, got %d", r)
	}

	if _, err := ws.Query(ctx, "SELECT count(*) AS n FROM save_archives"); err != nil {
		t.Fatalf("Query: %v", err)
	}

	if ws.rebuildCount != 1 {
		t.Fatalf("rebuildCount = %d, want exactly 1 rebuild", ws.rebuildCount)
	}
	if ws.scenesReadCount != 1 {
		t.Fatalf("scenesReadCount = %d for one rebuild over %d revisions, want exactly 1 (a per-revision scenes loop would be %d)",
			ws.scenesReadCount, r, r)
	}
	wantInserts := int64((r + catalogInsertChunk - 1) / catalogInsertChunk)
	if ws.insertExecCount != wantInserts {
		t.Fatalf("insertExecCount = %d for %d revisions (chunk %d), want %d (a per-revision INSERT loop would be %d)",
			ws.insertExecCount, r, catalogInsertChunk, wantInserts, r)
	}
}
