package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

// fixtureA and fixtureB are two distinct committed saves under
// testdata/saves/the-bibites. fixtureA is the largest (used by
// duckdb/import_test.go) and reliably populates the bibites projection.
// fixtureSmall is a complete but tiny save (32 bibites vs fixtureA's 1040); use
// it for structural/lifecycle tests that need a valid world but not data scale,
// since AddWorld import cost is ~proportional to bibite count.
const (
	fixtureA     = "autosave_20260301021357.zip"
	fixtureB     = "s.zip"
	fixtureSmall = "dasdasd.zip"
)

// repoRootDir walks up from this test file to the directory containing go.mod so
// the committed testdata fixtures can be located independent of cwd. The duckdb
// package's repoRoot helper is in package duckdb and not importable here.
func repoRootDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found walking up from test dir")
		}
		dir = parent
	}
}

func fixturePath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(repoRootDir(t), "testdata", "saves", "the-bibites", name)
}

// newWorkspace creates a fresh workspace under a temp root and registers its
// Close on cleanup.
func newWorkspace(t *testing.T, ctx context.Context) *Workspace {
	t.Helper()
	ws, err := Create(ctx, t.TempDir(), "alice", "demo")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	return ws
}

func countBySaveID(t *testing.T, ctx context.Context, ws *Workspace, table, saveID string) int64 {
	t.Helper()
	query := "SELECT count(*) FROM " + table + " WHERE save_id = ?"
	var count int64
	if err := ws.duck().QueryRowContext(ctx, query, saveID).Scan(&count); err != nil {
		t.Fatalf("count %s for save_id %q: %v", table, saveID, err)
	}
	return count
}

func TestAddWorldCreatesWorldRevisionAndHead(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	src := fixturePath(t, fixtureA)
	world, err := ws.AddWorld(ctx, src, "world-a")
	if err != nil {
		t.Fatalf("AddWorld: %v", err)
	}

	if world.ID == "" {
		t.Fatalf("world ID is empty")
	}
	if world.Name != "world-a" {
		t.Fatalf("world Name = %q, want %q", world.Name, "world-a")
	}
	if world.WorkspaceID != ws.ID() {
		t.Fatalf("world WorkspaceID = %q, want %q", world.WorkspaceID, ws.ID())
	}
	if world.HeadRevisionID == nil {
		t.Fatalf("world HeadRevisionID is nil (head not advanced)")
	}

	// Parse the fixture independently to learn the expected content hash and
	// whether the scene carries a sim time.
	archive, err := tb.ParseFile(src, nil)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if archive.Scene != nil && archive.Scene.HasTime {
		if world.SimTime == nil {
			t.Fatalf("world SimTime is nil but fixture scene has time %v", archive.Scene.SimulatedTime)
		}
		if *world.SimTime != archive.Scene.SimulatedTime {
			t.Fatalf("world SimTime = %v, want %v", *world.SimTime, archive.Scene.SimulatedTime)
		}
	}

	revisions, err := ws.store().RevisionsForWorld(ctx, world.ID)
	if err != nil {
		t.Fatalf("RevisionsForWorld: %v", err)
	}
	if len(revisions) != 1 {
		t.Fatalf("RevisionsForWorld returned %d revisions, want 1", len(revisions))
	}
	rev := revisions[0]
	if rev.ParentID != nil {
		t.Fatalf("first revision ParentID = %v, want nil", *rev.ParentID)
	}
	if rev.WorldID != world.ID {
		t.Fatalf("revision WorldID = %q, want %q", rev.WorldID, world.ID)
	}
	if rev.Tier != "full" {
		t.Fatalf("revision Tier = %q, want %q", rev.Tier, "full")
	}
	if !rev.BlobPresent {
		t.Fatalf("revision BlobPresent = false, want true")
	}
	// G1 establishes the blob self-ref atomically inside the recording tx, so a
	// freshly recorded revision is at refcount = 1 on its own — no extra
	// IncBlobRef. Anything else means C1 double-counted (or under-counted).
	if rev.Refcount != 1 {
		t.Fatalf("revision Refcount = %d, want 1", rev.Refcount)
	}
	if rev.BlobRef.SHA256 != archive.SHA256 {
		t.Fatalf("revision BlobRef.SHA256 = %q, want %q", rev.BlobRef.SHA256, archive.SHA256)
	}

	// The world's head points at the recorded revision.
	got, err := ws.store().GetWorld(ctx, world.ID)
	if err != nil {
		t.Fatalf("GetWorld: %v", err)
	}
	if got.HeadRevisionID == nil {
		t.Fatalf("GetWorld HeadRevisionID is nil")
	}
	if *got.HeadRevisionID != rev.ID {
		t.Fatalf("GetWorld HeadRevisionID = %d, want %d", *got.HeadRevisionID, rev.ID)
	}
}

func TestAddWorldSeedsDualKeyMirror(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	src := fixturePath(t, fixtureA)
	world, err := ws.AddWorld(ctx, src, "world-a")
	if err != nil {
		t.Fatalf("AddWorld: %v", err)
	}

	revisions, err := ws.store().RevisionsForWorld(ctx, world.ID)
	if err != nil {
		t.Fatalf("RevisionsForWorld: %v", err)
	}
	if len(revisions) != 1 {
		t.Fatalf("want 1 revision, got %d", len(revisions))
	}
	sha := revisions[0].BlobRef.SHA256

	// Working partition keyed by world id, history partition keyed by revision
	// sha256 — distinct keys, never overwriting each other. The bibites table is
	// non-empty for this fixture; both keys must carry the same row count.
	workingBibites := countBySaveID(t, ctx, ws, "bibites", world.ID)
	if workingBibites == 0 {
		t.Fatalf("working partition bibites count is 0 for world id %q", world.ID)
	}
	histBibites := countBySaveID(t, ctx, ws, "bibites", sha)
	if histBibites != workingBibites {
		t.Fatalf("history bibites count %d != working bibites count %d", histBibites, workingBibites)
	}

	// save_archives carries one row per save_id — assert both partitions exist.
	if got := countBySaveID(t, ctx, ws, "save_archives", world.ID); got != 1 {
		t.Fatalf("save_archives count for world id = %d, want 1", got)
	}
	if got := countBySaveID(t, ctx, ws, "save_archives", sha); got != 1 {
		t.Fatalf("save_archives count for revision sha = %d, want 1", got)
	}

	// The two keys are different values (the headline dual-key risk).
	if world.ID == sha {
		t.Fatalf("world id and revision sha256 are the same value %q", sha)
	}
}

// TestHistoryPartitionEqualsWorkingModuloSaveID proves, on the real AddWorld
// import path, that the DB-derived history partition is byte-identical to the
// working partition across EVERY normalized table, differing only in save_id. It
// adds two distinct worlds and, for each, row-compares working (keyed by world id)
// vs history (keyed by revision sha256) for every table in tb.NormalizedTables —
// the non-negotiable history byte-equality guarantee for H3.
func TestHistoryPartitionEqualsWorkingModuloSaveID(t *testing.T) {
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

	for _, world := range []struct {
		name string
		id   string
	}{{"A", worldA.ID}, {"B", worldB.ID}} {
		revisions, err := ws.store().RevisionsForWorld(ctx, world.id)
		if err != nil {
			t.Fatalf("world %s RevisionsForWorld: %v", world.name, err)
		}
		if len(revisions) != 1 {
			t.Fatalf("world %s: want 1 revision, got %d", world.name, len(revisions))
		}
		sha := revisions[0].BlobRef.SHA256
		if sha == world.id {
			t.Fatalf("world %s: working key and history key are equal (%q)", world.name, sha)
		}

		populated := 0
		for _, table := range tb.NormalizedTables {
			workingRows := dumpWorkspacePartition(t, ctx, ws, table, world.id)
			histRows := dumpWorkspacePartition(t, ctx, ws, table, sha)
			if !workspaceRowSetsEqual(workingRows, histRows) {
				t.Fatalf("world %s table %s: history rows differ from working rows modulo save_id (working=%d, history=%d)",
					world.name, table.Table, len(workingRows), len(histRows))
			}
			if len(workingRows) > 0 {
				populated++
			}
		}
		if populated == 0 {
			t.Fatalf("world %s: no normalized table had rows; the comparison was vacuous", world.name)
		}
		// The catalog/sim_time path reads scenes by the sha256 history key, so the
		// populated set MUST include scenes and the core entity tables.
		for _, must := range []string{"bibites", "species", "save_archives", "scenes"} {
			if got := countBySaveID(t, ctx, ws, must, sha); got == 0 {
				t.Fatalf("world %s: history table %s is empty under sha %q (catalog dependence)", world.name, must, sha)
			}
		}
	}
}

// dumpWorkspacePartition returns every column of every row for saveID in table,
// ordered deterministically by all columns, with save_id EXCLUDED from the result
// so working (keyed by world id) and history (keyed by sha) compare equal when
// they differ only in save_id.
func dumpWorkspacePartition(t *testing.T, ctx context.Context, ws *Workspace, table tb.NormalizedTableSpec, saveID string) [][]any {
	t.Helper()
	cols := make([]string, 0, len(table.Fields))
	for _, f := range table.Fields {
		if f.Column == "save_id" {
			continue
		}
		cols = append(cols, `"`+f.Column+`"`)
	}
	if len(cols) == 0 {
		// A save_id-only table: compare by row count via a constant projection.
		cols = []string{"1"}
	}
	colList := joinComma(cols)
	query := "SELECT " + colList + " FROM " + table.Table + " WHERE save_id = ? ORDER BY " + colList
	rows, err := ws.duck().QueryContext(ctx, query, saveID)
	if err != nil {
		t.Fatalf("query %s: %v", table.Table, err)
	}
	defer rows.Close()
	ncols := len(cols)
	var out [][]any
	for rows.Next() {
		cells := make([]any, ncols)
		ptrs := make([]any, ncols)
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("scan %s: %v", table.Table, err)
		}
		out = append(out, cells)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s: %v", table.Table, err)
	}
	return out
}

func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}

func workspaceRowSetsEqual(a, b [][]any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if fmtCell(a[i][j]) != fmtCell(b[i][j]) {
				return false
			}
		}
	}
	return true
}

func fmtCell(v any) string {
	return fmt.Sprintf("%T:%v", v, v)
}

func TestAddTwoWorldsAreIsolated(t *testing.T) {
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

	if worldA.ID == worldB.ID {
		t.Fatalf("both worlds got id %q", worldA.ID)
	}
	if worldA.HeadRevisionID == nil || worldB.HeadRevisionID == nil {
		t.Fatalf("a world head is nil: A=%v B=%v", worldA.HeadRevisionID, worldB.HeadRevisionID)
	}
	if *worldA.HeadRevisionID == *worldB.HeadRevisionID {
		t.Fatalf("both worlds share head revision id %d", *worldA.HeadRevisionID)
	}

	// Each world's working partition is independently keyed in the one DuckDB
	// file: exactly one save_archives row per world id.
	if got := countBySaveID(t, ctx, ws, "save_archives", worldA.ID); got != 1 {
		t.Fatalf("save_archives count for world A = %d, want 1", got)
	}
	if got := countBySaveID(t, ctx, ws, "save_archives", worldB.ID); got != 1 {
		t.Fatalf("save_archives count for world B = %d, want 1", got)
	}

	worlds, err := ws.store().ListWorlds(ctx, ws.ID())
	if err != nil {
		t.Fatalf("ListWorlds: %v", err)
	}
	if len(worlds) != 2 {
		t.Fatalf("ListWorlds returned %d worlds, want 2", len(worlds))
	}
	ids := map[string]bool{}
	for _, wld := range worlds {
		ids[wld.ID] = true
	}
	if !ids[worldA.ID] || !ids[worldB.ID] {
		t.Fatalf("ListWorlds missing a world id: %v", ids)
	}
}

func TestAddWorldBytesMatchesAddWorld(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	src := fixturePath(t, fixtureA)
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	world, err := ws.AddWorldBytes(ctx, data, "from-bytes")
	if err != nil {
		t.Fatalf("AddWorldBytes: %v", err)
	}

	archive, err := tb.ParseFile(src, nil)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	revisions, err := ws.store().RevisionsForWorld(ctx, world.ID)
	if err != nil {
		t.Fatalf("RevisionsForWorld: %v", err)
	}
	if len(revisions) != 1 {
		t.Fatalf("want 1 revision, got %d", len(revisions))
	}
	if got := revisions[0].BlobRef.SHA256; got != archive.SHA256 {
		t.Fatalf("AddWorldBytes revision sha256 = %q, want %q (temp-file round-trip lost identity)", got, archive.SHA256)
	}
	if revisions[0].Refcount != 1 {
		t.Fatalf("AddWorldBytes revision Refcount = %d, want 1", revisions[0].Refcount)
	}
}

func TestAddWorldBlobStored(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	world, err := ws.AddWorld(ctx, fixturePath(t, fixtureA), "world-a")
	if err != nil {
		t.Fatalf("AddWorld: %v", err)
	}

	revisions, err := ws.store().RevisionsForWorld(ctx, world.ID)
	if err != nil {
		t.Fatalf("RevisionsForWorld: %v", err)
	}
	if len(revisions) != 1 {
		t.Fatalf("want 1 revision, got %d", len(revisions))
	}

	has, err := ws.blobs().Has(ctx, revisions[0].BlobRef)
	if err != nil {
		t.Fatalf("blobs.Has: %v", err)
	}
	if !has {
		t.Fatalf("blob for revision sha256 %q not stored", revisions[0].BlobRef.SHA256)
	}
}
