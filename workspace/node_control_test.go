package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/asemones/bibicontrol/ipc"
	"github.com/asemones/bibicontrol/noderuntime"
)

// fakeSimCtl is an in-process stand-in for the game-side DLL, adapted from
// simctl/simctl_test.go. It speaks the same envelope contract and records the
// last command and RESUME request for assertion.
type fakeSimCtl struct {
	sess *ipc.Session

	mu          sync.Mutex
	lastCommand string
	lastResume  ipc.ResumeRequest
}

func newFakeSimCtl(conn net.Conn) *fakeSimCtl {
	f := &fakeSimCtl{sess: ipc.NewSession(conn, nil)}
	go f.serve()
	return f
}

func (f *fakeSimCtl) serve() {
	for env := range f.sess.Events() {
		if env.Kind != ipc.KindRequest {
			continue
		}
		payload, errMsg := f.handleEnv(env)
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

func (f *fakeSimCtl) handleEnv(env ipc.Envelope) (json.RawMessage, string) {
	f.mu.Lock()
	f.lastCommand = env.Command
	f.mu.Unlock()

	switch env.Command {
	case ipc.CommandStop:
		return mustJSONCtl(ipc.StopResult{PreviousTimeScale: 3.5}), ""
	case ipc.CommandResume:
		var req ipc.ResumeRequest
		if err := json.Unmarshal(env.Payload, &req); err != nil {
			return nil, err.Error()
		}
		if req.TimeScale <= 0 {
			return nil, "time_scale must be > 0"
		}
		f.mu.Lock()
		f.lastResume = req
		f.mu.Unlock()
		return mustJSONCtl(ipc.ResumeResult{TimeScale: req.TimeScale}), ""
	case ipc.CommandInfo:
		return mustJSONCtl(ipc.InfoResult{
			TPS:     60,
			RealTPS: 58.25,
			Paused:  true,
			SimTime: 1234.5,
			LastAutosave: &ipc.AutosaveInfo{
				Path:         "/saves/Autosaves/autosave_20260615.zip",
				Name:         "autosave_20260615.zip",
				ModifiedUnix: 1700000000,
				Time:         "2026-06-15T12:00:00.0000000Z",
			},
		}), ""
	case ipc.CommandReload:
		return mustJSONCtl(ipc.ReloadResult{Save: "/saves/Autosaves/autosave_20260615.zip", Ok: true}), ""
	default:
		return nil, "unknown command: " + env.Command
	}
}

func mustJSONCtl(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// newFakeNode creates a fake game node backed by an in-process pipe, registers
// it into ws.nodes under nodeID, and returns the runtime and the fake server
// for assertion. The fake uses net.Pipe so tests are OS-independent.
//
// Cleanup closes both ends of the pipe via t.Cleanup.
func newFakeNode(t *testing.T, ws *Workspace, nodeID string) (*noderuntime.Runtime, *fakeSimCtl) {
	t.Helper()
	cConn, sConn := net.Pipe()
	fake := newFakeSimCtl(sConn)
	rt := noderuntime.Wrap(nodeID, "run", nil, ipc.NewSession(cConn, nil))

	ws.mu.Lock()
	ws.nodes[nodeID] = rt
	ws.mu.Unlock()

	t.Cleanup(func() {
		_ = rt.Close()
		_ = fake.sess.Close()
	})

	return rt, fake
}

// testCtxCtl returns a context that times out after 3 seconds.
func testCtxCtl(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// newTestWorkspace creates a bare workspace (no world required for passthrough
// methods) for use in node_control tests.
func newTestWorkspace(t *testing.T) *Workspace {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	ws, err := Create(ctx, root, "testowner", "testws-ctrl")
	if err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	return ws
}

// TestNodeInfo_RoundTrip verifies that NodeInfo delegates to simctl.Client.Info
// and returns the fake's telemetry.
func TestNodeInfo_RoundTrip(t *testing.T) {
	ws := newTestWorkspace(t)
	_, _ = newFakeNode(t, ws, "node-info")

	res, err := ws.NodeInfo(testCtxCtl(t), "node-info")
	if err != nil {
		t.Fatalf("NodeInfo: %v", err)
	}
	if res.TPS != 60 || res.RealTPS != 58.25 || !res.Paused || res.SimTime != 1234.5 {
		t.Fatalf("unexpected InfoResult: %+v", res)
	}
	if res.LastAutosave == nil || res.LastAutosave.Name != "autosave_20260615.zip" {
		t.Fatalf("unexpected LastAutosave: %+v", res.LastAutosave)
	}
}

// TestNodeStop_RoundTrip verifies that NodeStop delegates to simctl.Client.Stop
// and returns PreviousTimeScale == 3.5.
func TestNodeStop_RoundTrip(t *testing.T) {
	ws := newTestWorkspace(t)
	_, fake := newFakeNode(t, ws, "node-stop")

	res, err := ws.NodeStop(testCtxCtl(t), "node-stop")
	if err != nil {
		t.Fatalf("NodeStop: %v", err)
	}
	if res.PreviousTimeScale != 3.5 {
		t.Fatalf("PreviousTimeScale = %v, want 3.5", res.PreviousTimeScale)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.lastCommand != ipc.CommandStop {
		t.Fatalf("server saw command %q, want %q", fake.lastCommand, ipc.CommandStop)
	}
}

// TestNodeResume_RoundTrip verifies that NodeResume delegates to
// simctl.Client.Resume and that the server receives the correct time scale.
// It also asserts that a time scale <= 0 is rejected server-side.
func TestNodeResume_RoundTrip(t *testing.T) {
	ws := newTestWorkspace(t)
	_, fake := newFakeNode(t, ws, "node-resume")

	ctx := testCtxCtl(t)
	res, err := ws.NodeResume(ctx, "node-resume", 4.25)
	if err != nil {
		t.Fatalf("NodeResume(4.25): %v", err)
	}
	if res.TimeScale != 4.25 {
		t.Fatalf("TimeScale = %v, want 4.25", res.TimeScale)
	}
	fake.mu.Lock()
	if fake.lastResume.TimeScale != 4.25 {
		t.Fatalf("server saw TimeScale = %v, want 4.25", fake.lastResume.TimeScale)
	}
	fake.mu.Unlock()

	// A zero time scale must be rejected (enforced server-side).
	_, err = ws.NodeResume(ctx, "node-resume", 0)
	if err == nil {
		t.Fatalf("NodeResume(0) should return an error (server rejects time_scale <= 0)")
	}
}

// TestNodeState_Connected verifies that NodeState on a connected node returns
// Connected == true, a non-nil Info with the fake's telemetry, and a Runtime
// carrying the expected node id.
func TestNodeState_Connected(t *testing.T) {
	ws := newTestWorkspace(t)
	_, _ = newFakeNode(t, ws, "node-state")

	ns, err := ws.NodeState(testCtxCtl(t), "node-state")
	if err != nil {
		t.Fatalf("NodeState: %v", err)
	}
	if !ns.Connected {
		t.Errorf("NodeState.Connected = false, want true")
	}
	if ns.Info == nil {
		t.Fatalf("NodeState.Info is nil, want non-nil for connected node")
	}
	if ns.Info.TPS != 60 || !ns.Info.Paused {
		t.Errorf("unexpected Info: %+v", ns.Info)
	}
	if ns.Runtime.NodeID != "node-state" {
		t.Errorf("Runtime.NodeID = %q, want %q", ns.Runtime.NodeID, "node-state")
	}
	if !ns.Runtime.Connected {
		t.Errorf("Runtime.Connected = false, want true")
	}
}

// TestNodeState_Disconnected verifies that NodeState on a started-but-not-
// connected node (session == nil) returns success with Connected == false and
// Info == nil. This is a legitimate state, not an error.
func TestNodeState_Disconnected(t *testing.T) {
	ws := newTestWorkspace(t)

	// Register a runtime with no session (nil) — simulates a node whose compat
	// layer has not connected yet.
	rt := noderuntime.Wrap("node-disc", "run", nil, nil)
	ws.mu.Lock()
	ws.nodes["node-disc"] = rt
	ws.mu.Unlock()
	t.Cleanup(func() { _ = rt.Close() })

	ns, err := ws.NodeState(testCtxCtl(t), "node-disc")
	if err != nil {
		t.Fatalf("NodeState on disconnected node returned error: %v", err)
	}
	if ns.Connected {
		t.Errorf("NodeState.Connected = true on disconnected node, want false")
	}
	if ns.Info != nil {
		t.Errorf("NodeState.Info = %+v on disconnected node, want nil", ns.Info)
	}

	// NodeInfo/NodeStop/NodeResume on the disconnected node must return an error
	// wrapping noderuntime.ErrNoSession.
	ctx := testCtxCtl(t)
	_, err = ws.NodeInfo(ctx, "node-disc")
	if err == nil {
		t.Errorf("NodeInfo on disconnected node: want error, got nil")
	} else if !errors.Is(err, noderuntime.ErrNoSession) {
		t.Errorf("NodeInfo error = %v, want to wrap noderuntime.ErrNoSession", err)
	}

	_, err = ws.NodeStop(ctx, "node-disc")
	if err == nil {
		t.Errorf("NodeStop on disconnected node: want error, got nil")
	} else if !errors.Is(err, noderuntime.ErrNoSession) {
		t.Errorf("NodeStop error = %v, want to wrap noderuntime.ErrNoSession", err)
	}

	_, err = ws.NodeResume(ctx, "node-disc", 1.0)
	if err == nil {
		t.Errorf("NodeResume on disconnected node: want error, got nil")
	} else if !errors.Is(err, noderuntime.ErrNoSession) {
		t.Errorf("NodeResume error = %v, want to wrap noderuntime.ErrNoSession", err)
	}
}

// TestNodeControl_UnknownNode verifies that all four methods return an error
// when the given nodeID is not registered in the active set.
func TestNodeControl_UnknownNode(t *testing.T) {
	ws := newTestWorkspace(t)
	ctx := testCtxCtl(t)

	if _, err := ws.NodeInfo(ctx, "ghost"); err == nil {
		t.Errorf("NodeInfo on unknown node: want error, got nil")
	}
	if _, err := ws.NodeStop(ctx, "ghost"); err == nil {
		t.Errorf("NodeStop on unknown node: want error, got nil")
	}
	if _, err := ws.NodeResume(ctx, "ghost", 1.0); err == nil {
		t.Errorf("NodeResume on unknown node: want error, got nil")
	}
	if _, err := ws.NodeState(ctx, "ghost"); err == nil {
		t.Errorf("NodeState on unknown node: want error, got nil")
	}
}
