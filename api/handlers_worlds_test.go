package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/asemones/bibicontrol/api"
	"github.com/asemones/bibicontrol/workspace"
)

// repoRootForAPI walks up from this test file to the directory containing
// go.mod, mirroring the helper in workspace/world_test.go.
func repoRootForAPI(t *testing.T) string {
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

// fixturePathForAPI returns the path to the named save fixture.
func fixturePathForAPI(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(repoRootForAPI(t), "testdata", "saves", "the-bibites", name)
}

// seedWorkspace creates a workspace with one world (world-a) using the tiny
// fixture, closes it, then returns the workspace id and world id so the daemon
// re-opens it through its cache.
func seedWorkspace(t *testing.T) (root, wsID, worldID string) {
	t.Helper()
	ctx := testCtx(t)
	root = t.TempDir()

	ws, err := workspace.Create(ctx, root, "owner", "ws")
	if err != nil {
		t.Fatalf("workspace.Create: %v", err)
	}
	wsID = ws.ID()

	fix := fixturePathForAPI(t, "dasdasd.zip")
	world, err := ws.AddWorld(ctx, fix, "world-a")
	if err != nil {
		t.Fatalf("ws.AddWorld: %v", err)
	}
	worldID = world.ID

	if err := ws.Close(); err != nil {
		t.Fatalf("ws.Close: %v", err)
	}
	return root, wsID, worldID
}

// TestWorldsList verifies GET /api/workspaces/{id}/worlds returns 200 with a
// JSON array containing the seeded world.
func TestWorldsList(t *testing.T) {
	root, wsID, _ := seedWorkspace(t)

	d := api.New(root, "owner")
	defer func() { _ = d.Close() }()

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+wsID+"/worlds", nil)
	rec := httptest.NewRecorder()
	d.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("worlds list: got status %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var worlds []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&worlds); err != nil {
		t.Fatalf("worlds list: decode body: %v", err)
	}
	if len(worlds) != 1 {
		t.Fatalf("worlds list: got %d worlds, want 1", len(worlds))
	}
	w := worlds[0]
	if got := w["name"]; got != "world-a" {
		t.Errorf("worlds list: name=%q, want %q", got, "world-a")
	}
	if w["head_revision"] == nil {
		t.Error("worlds list: head_revision should be non-null after AddWorld")
	}
	// live_node must be present as null (no node started).
	if _, ok := w["live_node"]; !ok {
		t.Error("worlds list: live_node key must be present")
	}
	if w["live_node"] != nil {
		t.Errorf("worlds list: live_node=%v, want null (no node running)", w["live_node"])
	}
}

// TestWorldHistory verifies GET /api/workspaces/{id}/worlds/{wid}/history
// returns 200 with one revision that is the head.
func TestWorldHistory(t *testing.T) {
	root, wsID, worldID := seedWorkspace(t)

	d := api.New(root, "owner")
	defer func() { _ = d.Close() }()

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+wsID+"/worlds/"+worldID+"/history", nil)
	rec := httptest.NewRecorder()
	d.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("world history: got status %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var revs []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&revs); err != nil {
		t.Fatalf("world history: decode body: %v", err)
	}
	if len(revs) != 1 {
		t.Fatalf("world history: got %d revisions, want 1", len(revs))
	}
	rev := revs[0]

	// parent_id must be null for the root revision.
	if rev["parent_id"] != nil {
		t.Errorf("world history: parent_id=%v, want null", rev["parent_id"])
	}
	// is_head must be true.
	if got, _ := rev["is_head"].(bool); !got {
		t.Errorf("world history: is_head=%v, want true", rev["is_head"])
	}
	// source_path must be non-empty.
	if sp, _ := rev["source_path"].(string); sp == "" {
		t.Error("world history: source_path is empty")
	}
	// created_at must parse as RFC3339.
	if ca, _ := rev["created_at"].(string); ca == "" {
		t.Error("world history: created_at is empty")
	} else if _, err := time.Parse(time.RFC3339, ca); err != nil {
		t.Errorf("world history: created_at=%q does not parse as RFC3339: %v", ca, err)
	}
}

// TestWorldsUnknownWorkspace verifies that an unknown workspace id returns 404.
func TestWorldsUnknownWorkspace(t *testing.T) {
	d := api.New(t.TempDir(), "owner")
	defer func() { _ = d.Close() }()

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/does-not-exist/worlds", nil)
	rec := httptest.NewRecorder()
	d.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown workspace: got status %d, want 404; body: %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("unknown workspace: decode body: %v", err)
	}
	if body["error"] == "" {
		t.Error("unknown workspace: expected non-empty error field")
	}
}

// TestWorldHistoryUnknownWorld verifies that history for a bogus world id in a
// real workspace returns 404.
func TestWorldHistoryUnknownWorld(t *testing.T) {
	root, wsID, _ := seedWorkspace(t)

	d := api.New(root, "owner")
	defer func() { _ = d.Close() }()

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+wsID+"/worlds/does-not-exist/history", nil)
	rec := httptest.NewRecorder()
	d.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown world: got status %d, want 404; body: %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("unknown world: decode body: %v", err)
	}
	if body["error"] == "" {
		t.Error("unknown world: expected non-empty error field")
	}
}
