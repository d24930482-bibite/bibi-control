package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/asemones/bibicontrol/api"
	"github.com/asemones/bibicontrol/script"
	"github.com/asemones/bibicontrol/workspace"
)

// testCtx returns a context that times out after 30 seconds, giving headroom
// for slow CI machines running file I/O and DuckDB initialization.
func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// TestHealth checks that GET /api/health returns 200 with {"status":"ok"}.
func TestHealth(t *testing.T) {
	d := api.New(t.TempDir(), "owner")
	defer func() { _ = d.Close() }()

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	d.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("health: got status %d, want 200", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("health: decode body: %v", err)
	}
	if got := body["status"]; got != "ok" {
		t.Fatalf("health: status=%q, want %q", got, "ok")
	}
}

// TestRun verifies that POST /api/workspaces/{id}/run executes a Starlark
// program and returns 200 with the Result encoded as JSON.
//
// Flow: Create a workspace via workspace.Create, capture its ID, then Close it
// so the Daemon's ws() cache-miss path has to re-Open it — this proves the
// cache path works (the workspace is opened through the daemon, not reused from
// the Create call). We assert that Result.Output contains the expected string.
func TestRun(t *testing.T) {
	ctx := testCtx(t)
	root := t.TempDir()

	// Create the workspace and capture its id.
	ws, err := workspace.Create(ctx, root, "owner", "testws")
	if err != nil {
		t.Fatalf("workspace.Create: %v", err)
	}
	id := ws.ID()
	// Close it so the daemon must re-Open via its cache.
	if err := ws.Close(); err != nil {
		t.Fatalf("ws.Close: %v", err)
	}

	d := api.New(root, "owner")
	defer func() { _ = d.Close() }()

	body := bytes.NewBufferString(`{"program":"print(\"hi\")"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+id+"/run", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	d.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("run: got status %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var res script.Result
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatalf("run: decode result: %v", err)
	}
	if !strings.Contains(res.Output, "hi") {
		t.Fatalf("run: Output=%q, want to contain %q", res.Output, "hi")
	}
}

// TestNotImplemented verifies that a stubbed route returns 501.
func TestNotImplemented(t *testing.T) {
	d := api.New(t.TempDir(), "owner")
	defer func() { _ = d.Close() }()

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces", nil)
	rec := httptest.NewRecorder()
	d.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("not-implemented: got status %d, want 501", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("not-implemented: decode body: %v", err)
	}
	if body["error"] == "" {
		t.Fatal("not-implemented: expected non-empty error field")
	}
}
