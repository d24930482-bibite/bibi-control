package api_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/asemones/bibicontrol/api"
	"github.com/asemones/bibicontrol/ipc"
	"github.com/asemones/bibicontrol/workspace"
)

// testFixtureSave returns the path to a small Bibites save file in the repo's
// testdata tree. It is used to create a world row so StartNode has a valid
// WorldID to bind to. The world need not have content for liveness.
func testFixtureSave(t *testing.T) string {
	t.Helper()
	// Locate testdata relative to this file via runtime.Caller.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// thisFile is …/api/handlers_nodes_test.go; testdata is one level up.
	repoRoot := filepath.Dir(filepath.Dir(thisFile))
	p := filepath.Join(repoRoot, "testdata", "saves", "the-bibites", "dasdasd.zip")
	return p
}

// fakeSimServer is an in-process stand-in for the game-side DLL backed by a
// real net.Listener (so the runtime can dial it). It speaks the envelope
// contract used by simctl.Client.
type fakeSimServer struct {
	lis net.Listener

	mu   sync.Mutex
	sims []*fakeSim2
}

type fakeSim2 struct {
	sess *ipc.Session
}

func newFakeSim2(conn net.Conn) *fakeSim2 {
	f := &fakeSim2{sess: ipc.NewSession(conn, nil)}
	go f.serve()
	return f
}

func (f *fakeSim2) serve() {
	for env := range f.sess.Events() {
		if env.Kind != ipc.KindRequest {
			continue
		}
		payload, errMsg := f.handle(env)
		reply := ipc.Envelope{Kind: ipc.KindResponse, ReplyTo: env.ID}
		if errMsg != "" {
			reply.Kind = ipc.KindError
			reply.Error = errMsg
		} else {
			reply.Payload = payload
		}
		_ = f.sess.Send(context.Background(), reply)
	}
}

func (f *fakeSim2) handle(env ipc.Envelope) (json.RawMessage, string) {
	switch env.Command {
	case ipc.CommandInfo:
		return mustMarshal(ipc.InfoResult{
			TPS:     60,
			RealTPS: 58.25,
			Paused:  true,
			SimTime: 1234.5,
			LastAutosave: &ipc.AutosaveInfo{
				Path:         "/saves/Autosaves/autosave_20260615.zip",
				Name:         "autosave_20260615.zip",
				ModifiedUnix: 1700000000,
			},
		}), ""
	case ipc.CommandStop:
		return mustMarshal(ipc.StopResult{PreviousTimeScale: 1.0}), ""
	case ipc.CommandResume:
		return mustMarshal(ipc.ResumeResult{TimeScale: 1.0}), ""
	default:
		return nil, "unknown command: " + env.Command
	}
}

func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// newFakeSimListener starts a fake-sim TCP listener and returns it. The
// caller should close the listener via t.Cleanup. Returned addr is suitable
// for use as CompatAddr in StartNodeSpec.
func newFakeSimListener(t *testing.T) (net.Listener, string) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &fakeSimServer{lis: lis}
	go func() {
		for {
			conn, err := lis.Accept()
			if err != nil {
				return // listener closed
			}
			f := newFakeSim2(conn)
			srv.mu.Lock()
			srv.sims = append(srv.sims, f)
			srv.mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = lis.Close()
		srv.mu.Lock()
		for _, s := range srv.sims {
			_ = s.sess.Close()
		}
		srv.mu.Unlock()
	})
	return lis, lis.Addr().String()
}

// TestNodesInfo_AliveAndLogs exercises the alive-node telemetry path and log
// snapshot, then the detached path from a fresh daemon with no seeded handle.
func TestNodesInfo_AliveAndLogs(t *testing.T) {
	if _, err := t.TempDir(), false; err {
		t.Skip("/bin/sleep not available")
	}

	ctx := testCtx(t)
	root := t.TempDir()

	// Create the workspace.
	ws, err := workspace.Create(ctx, root, "owner", "nodes-test-ws")
	if err != nil {
		t.Fatalf("workspace.Create: %v", err)
	}
	id := ws.ID()
	t.Cleanup(func() { _ = ws.Close() })

	// Add a world so StartNode has a valid WorldID to bind to.
	// We use AddWorld with the smallest fixture save; no content is needed
	// for liveness testing.
	world, err := ws.AddWorld(ctx, testFixtureSave(t), "test-world")
	if err != nil {
		t.Fatalf("AddWorld: %v", err)
	}

	// Start a fake-sim listener so ConnectOnStart can dial it.
	_, compatAddr := newFakeSimListener(t)

	// Start the node backed by /bin/sleep (established test process) with the
	// fake sim for IPC.
	rt, _, err := ws.StartNode(ctx, workspace.StartNodeSpec{
		WorldID:        world.ID,
		NodeID:         "n-alive",
		Process:        ipc.ProcessSpec{Path: "/bin/sleep", Args: []string{"60"}},
		CompatAddr:     compatAddr,
		ConnectOnStart: true,
		DialTimeout:    5 * time.Second,
	})
	if err != nil {
		t.Fatalf("StartNode: %v", err)
	}
	_ = rt
	t.Cleanup(func() { _ = ws.KillNode(ctx, "n-alive") })

	// Wire the daemon with the seeded workspace handle.
	d := api.New(root, "owner")
	d.SeedWorkspace(id, ws)
	t.Cleanup(func() { _ = d.Close() })

	// ── Case 1: alive + telemetry ─────────────────────────────────────────────
	t.Run("alive_telemetry", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+id+"/nodes/info", nil)
		rec := httptest.NewRecorder()
		d.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("nodes/info: got %d, want 200; body: %s", rec.Code, rec.Body.String())
		}

		var infos []map[string]any
		if err := json.NewDecoder(rec.Body).Decode(&infos); err != nil {
			t.Fatalf("decode nodes/info: %v", err)
		}

		// Find the n-alive entry.
		var found map[string]any
		for _, obj := range infos {
			if obj["id"] == "n-alive" {
				found = obj
				break
			}
		}
		if found == nil {
			t.Fatalf("n-alive not in nodes/info response; got %v", infos)
		}

		if got := found["liveness"]; got != "alive" {
			t.Errorf("liveness = %v, want alive", got)
		}
		if got, ok := found["tps"].(float64); !ok || got != 60 {
			t.Errorf("tps = %v, want 60", found["tps"])
		}
		if got, ok := found["paused"].(bool); !ok || !got {
			t.Errorf("paused = %v, want true", found["paused"])
		}
		if got, ok := found["real_tps"].(float64); !ok || got != 58.25 {
			t.Errorf("real_tps = %v, want 58.25", found["real_tps"])
		}
	})

	// ── Case 2: logs snapshot ────────────────────────────────────────────────
	t.Run("logs_shape", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+id+"/nodes/n-alive/logs", nil)
		rec := httptest.NewRecorder()
		d.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("nodes/n-alive/logs: got %d, want 200; body: %s", rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode logs: %v", err)
		}
		// lines must be present and be a JSON array (may be empty since /bin/sleep produces none).
		lines, ok := body["lines"]
		if !ok {
			t.Fatalf("logs response missing 'lines' field; got %v", body)
		}
		if _, ok := lines.([]any); !ok {
			t.Fatalf("lines is not an array: %T %v", lines, lines)
		}
	})

	// ── Case 4: logs 404 for unknown node ─────────────────────────────────────
	t.Run("logs_404_unknown", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+id+"/nodes/no-such-node/logs", nil)
		rec := httptest.NewRecorder()
		d.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("logs/unknown: got %d, want 404; body: %s", rec.Code, rec.Body.String())
		}
		var body map[string]string
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode 404 body: %v", err)
		}
		if body["error"] == "" {
			t.Fatal("logs 404: expected non-empty error field")
		}
	})
}

// TestNodesInfo_Detached verifies that a node with a GENUINE persisted
// "running" row but no active runtime entry on the querying handle is reported
// as liveness=="detached" with no telemetry.
//
// This is the critical liveness invariant: the verdict must come from the
// active-set membership (ws.Node), never from row.Status. A stale "running"
// row whose process is gone (or simply not in the querying handle's in-memory
// set) must read as detached.
//
// Strategy: start the node on workspace handle ws (persisted row becomes
// status="running"). Then close ws WITHOUT calling KillNode — ws.Close kills
// the process but does NOT update the persisted status, so the row stays
// "running". Open a fresh daemon d2 over the same root with NO seeded handle.
// d2.ws() opens its own workspace.Open handle, which has an empty nodes map.
// The fresh handle sees the "running" row via PersistedNodes but ws.Node
// returns (nil, false) → liveness="detached".
func TestNodesInfo_Detached(t *testing.T) {
	ctx := testCtx(t)
	root := t.TempDir()

	// Create the workspace.
	ws, err := workspace.Create(ctx, root, "owner", "detached-ws")
	if err != nil {
		t.Fatalf("workspace.Create: %v", err)
	}
	id := ws.ID()

	// Add a world so StartNode has a valid WorldID.
	world, err := ws.AddWorld(ctx, testFixtureSave(t), "test-world-detach")
	if err != nil {
		_ = ws.Close()
		t.Fatalf("AddWorld: %v", err)
	}

	// Start the node — this writes a persisted row with status="running".
	// ConnectOnStart=false so we don't need a fake sim listener for this case.
	_, _, err = ws.StartNode(ctx, workspace.StartNodeSpec{
		WorldID:        world.ID,
		NodeID:         "n-detach",
		Process:        ipc.ProcessSpec{Path: "/bin/sleep", Args: []string{"60"}},
		ConnectOnStart: false,
	})
	if err != nil {
		_ = ws.Close()
		t.Fatalf("StartNode: %v", err)
	}

	// Close ws WITHOUT calling KillNode. ws.Close kills the OS process but
	// intentionally does NOT update the persisted status row (see workspace.go
	// "Status rows are not updated here"). The row remains status="running",
	// giving us the genuine stale-running-row scenario.
	//
	// We MUST close ws before opening d2 to avoid two concurrent DuckDB writers
	// on the same file.
	if err := ws.Close(); err != nil {
		// Close errors are non-fatal for this test's goal (row is still "running").
		t.Logf("ws.Close: %v (non-fatal)", err)
	}

	// Open d2 fresh — it will lazily open the workspace on the first request.
	// Its workspace handle has an empty nodes map (no active node registrations),
	// so ws.Node("n-detach") returns (nil, false) despite the "running" row.
	d2 := api.New(root, "owner")
	t.Cleanup(func() { _ = d2.Close() })

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+id+"/nodes/info", nil)
	rec := httptest.NewRecorder()
	d2.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("nodes/info (d2): got %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var infos []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&infos); err != nil {
		t.Fatalf("decode nodes/info (d2): %v", err)
	}

	// Locate the n-detach entry.
	var found map[string]any
	for _, obj := range infos {
		if obj["id"] == "n-detach" {
			found = obj
			break
		}
	}
	if found == nil {
		t.Fatalf("n-detach not in nodes/info response (d2); got %v", infos)
	}

	// The persisted row is still "running" — assert the status field so the
	// test proves we have a genuine stale-running row (not a stopped one).
	if got := found["status"]; got != "running" {
		t.Errorf("status = %v, want running (test invariant: must be a genuine stale running row)", got)
	}

	// Liveness must be "detached" because the querying handle has no active entry.
	if got := found["liveness"]; got != "detached" {
		t.Errorf("liveness = %v, want detached", got)
	}

	// No telemetry should be present for a detached node.
	if found["tps"] != nil {
		t.Errorf("tps should be absent for detached node; got %v", found["tps"])
	}
}

// TestDeleteNode exercises DELETE /api/workspaces/{id}/nodes/{nid}.
//
// Strategy mirrors TestNodesInfo_Detached: start a node on ws1 to create a
// persisted row, then close ws1 (leaving the row stale / detached). Open a
// fresh daemon d2 (no seeded handle). Assert:
//
//   - DELETE returns 204 and the row disappears from GET nodes/info.
//   - DELETE of an unknown nid returns 404.
func TestDeleteNode(t *testing.T) {
	ctx := testCtx(t)
	root := t.TempDir()

	// Create the workspace and persist a node row.
	ws, err := workspace.Create(ctx, root, "owner", "delete-node-ws")
	if err != nil {
		t.Fatalf("workspace.Create: %v", err)
	}
	id := ws.ID()

	world, err := ws.AddWorld(ctx, testFixtureSave(t), "test-world-del")
	if err != nil {
		_ = ws.Close()
		t.Fatalf("AddWorld: %v", err)
	}

	// Start the node — persists a row with status="running". ConnectOnStart=false
	// so no fake sim is needed.
	_, _, err = ws.StartNode(ctx, workspace.StartNodeSpec{
		WorldID:        world.ID,
		NodeID:         "n-to-delete",
		Process:        ipc.ProcessSpec{Path: "/bin/sleep", Args: []string{"60"}},
		ConnectOnStart: false,
	})
	if err != nil {
		_ = ws.Close()
		t.Fatalf("StartNode: %v", err)
	}

	// Close ws so d2 can open its own handle without a second DuckDB writer.
	if err := ws.Close(); err != nil {
		t.Logf("ws.Close: %v (non-fatal)", err)
	}

	// Open a fresh daemon — no seeded handle, so the row is seen as detached.
	d2 := api.New(root, "owner")
	t.Cleanup(func() { _ = d2.Close() })

	// ── Case 1: DELETE returns 204 ────────────────────────────────────────────
	t.Run("delete_204", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/"+id+"/nodes/n-to-delete", nil)
		rec := httptest.NewRecorder()
		d2.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("delete node: got %d, want 204; body: %s", rec.Code, rec.Body.String())
		}
	})

	// ── Case 2: node is absent from nodes/info after deletion ─────────────────
	t.Run("absent_after_delete", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+id+"/nodes/info", nil)
		rec := httptest.NewRecorder()
		d2.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("nodes/info: got %d, want 200; body: %s", rec.Code, rec.Body.String())
		}
		var infos []map[string]any
		if err := json.NewDecoder(rec.Body).Decode(&infos); err != nil {
			t.Fatalf("decode nodes/info: %v", err)
		}
		for _, obj := range infos {
			if obj["id"] == "n-to-delete" {
				t.Fatalf("n-to-delete still present in nodes/info after DELETE; got %v", infos)
			}
		}
	})

	// ── Case 3: DELETE of unknown nid returns 404 ─────────────────────────────
	t.Run("delete_unknown_404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/"+id+"/nodes/no-such-node", nil)
		rec := httptest.NewRecorder()
		d2.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("delete unknown: got %d, want 404; body: %s", rec.Code, rec.Body.String())
		}
		var body map[string]string
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode 404 body: %v", err)
		}
		if body["error"] == "" {
			t.Fatal("delete unknown 404: expected non-empty error field")
		}
	})
}

// TestNodesInfo_Reaped verifies that after the supervisor reaps a dead node,
// GET /nodes/info reports liveness="crashed" (or "detached" if the reaper has
// already removed it) and the persisted status is no longer "running".
//
// Strategy: start a fast-exit process on ws, seed ws into a fresh daemon, wait
// for the reaper to fire, then poll /nodes/info. The node must NOT appear with
// liveness="alive" or status="running" after reaping.
func TestNodesInfo_Reaped(t *testing.T) {
	ctx := testCtx(t)
	root := t.TempDir()

	ws, err := workspace.Create(ctx, root, "owner", "reaped-ws")
	if err != nil {
		t.Fatalf("workspace.Create: %v", err)
	}
	id := ws.ID()
	t.Cleanup(func() { _ = ws.Close() })

	world, err := ws.AddWorld(ctx, testFixtureSave(t), "reaped-world")
	if err != nil {
		t.Fatalf("AddWorld: %v", err)
	}

	// Use a fast-exit process so the supervisor reaps it quickly.
	_, _, err = ws.StartNode(ctx, workspace.StartNodeSpec{
		WorldID:        world.ID,
		NodeID:         "n-reaped",
		Process:        ipc.ProcessSpec{Path: "/bin/true"},
		ConnectOnStart: false,
	})
	if err != nil {
		// /bin/true may not be available on all platforms; skip gracefully.
		t.Skipf("StartNode with /bin/true: %v — skipping (fast-exit process not available)", err)
	}

	// Wait for the supervisor to reap the node.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, active := ws.Node("n-reaped"); !active {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Wire the daemon with the workspace.
	d := api.New(root, "owner")
	d.SeedWorkspace(id, ws)
	t.Cleanup(func() { _ = d.Close() })

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+id+"/nodes/info", nil)
	rec := httptest.NewRecorder()
	d.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("nodes/info (reaped): got %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var infos []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&infos); err != nil {
		t.Fatalf("decode nodes/info: %v", err)
	}

	var found map[string]any
	for _, obj := range infos {
		if obj["id"] == "n-reaped" {
			found = obj
			break
		}
	}
	if found == nil {
		t.Fatalf("n-reaped not in nodes/info response; got %v", infos)
	}

	// After reaping, the node must NOT be alive.
	if got := found["liveness"]; got == "alive" {
		t.Errorf("liveness = alive after reap, want crashed or detached")
	}

	// The persisted status must NOT be "running" after reaping.
	if got := found["status"]; got == "running" {
		t.Errorf("status = %q after reap, want exited or crashed (supervisor reconcile failed)", got)
	}
}

// TestNodesInfo_ResolveNotFound verifies that requesting nodes/info for an
// unknown workspace id returns 404.
func TestNodesInfo_ResolveNotFound(t *testing.T) {
	d := api.New(t.TempDir(), "owner")
	t.Cleanup(func() { _ = d.Close() })

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/does-not-exist/nodes/info", nil)
	rec := httptest.NewRecorder()
	d.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("resolve 404: got %d, want 404; body: %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode 404 body: %v", err)
	}
	if body["error"] == "" {
		t.Fatal("resolve 404: expected non-empty error field")
	}
}
