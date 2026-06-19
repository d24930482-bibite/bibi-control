package workspace

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/asemones/bibicontrol/ipc"
	"github.com/asemones/bibicontrol/noderuntime"
	"github.com/asemones/bibicontrol/revisionstore"
)

// progFake is a fake game node whose INFO sim_time advances by `step` seconds on
// every INFO call, so a wait/poll loop sees monotonic progress. It is the
// templated counterpart to fakeSimCtl (node_control_test.go), built specifically
// to drive the blocking-loop primitives deterministically.
type progFake struct {
	sess  *ipc.Session
	step  float64
	calls atomic.Int64
}

func (f *progFake) serve() {
	for env := range f.sess.Events() {
		if env.Kind != ipc.KindRequest {
			continue
		}
		var payload json.RawMessage
		errMsg := ""
		switch env.Command {
		case ipc.CommandInfo:
			k := f.calls.Add(1)
			payload = mustJSONCtl(ipc.InfoResult{
				TPS:     60,
				RealTPS: 59,
				Paused:  false,
				SimTime: float64(k) * f.step,
			})
		case ipc.CommandStop:
			payload = mustJSONCtl(ipc.StopResult{PreviousTimeScale: 1})
		case ipc.CommandResume:
			var req ipc.ResumeRequest
			if err := json.Unmarshal(env.Payload, &req); err != nil {
				errMsg = err.Error()
			} else {
				payload = mustJSONCtl(ipc.ResumeResult{TimeScale: req.TimeScale})
			}
		default:
			errMsg = "unknown command: " + env.Command
		}
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

// newProgressNode wires a progFake into both the active set (so the Go control
// methods resolve it) and the persisted registry (so workspace.node(id) resolves
// it from Starlark), mirroring TestAutomation_NodeInfoAndControl's setup.
func newProgressNode(t *testing.T, ws *Workspace, setupCtx context.Context, nodeID string, step float64) *progFake {
	t.Helper()
	cConn, sConn := net.Pipe()
	fake := &progFake{sess: ipc.NewSession(sConn, nil), step: step}
	go fake.serve()
	rt := noderuntime.Wrap(nodeID, "run", nil, ipc.NewSession(cConn, nil))

	ws.mu.Lock()
	ws.nodes[nodeID] = rt
	ws.mu.Unlock()

	if _, err := ws.store().CreateNode(setupCtx, revisionstore.NodeInput{
		WorkspaceID: ws.ID(),
		NodeID:      nodeID,
		RunID:       "run",
		Status:      "running",
	}); err != nil {
		t.Fatalf("CreateNode(%q): %v", nodeID, err)
	}

	t.Cleanup(func() {
		_ = rt.Close()
		_ = fake.sess.Close()
	})
	return fake
}

// TestNodeWait_ReachesCondition: a wait on sim_time returns (not timed out) once
// the fake's advancing sim_time crosses the target.
func TestNodeWait_ReachesCondition(t *testing.T) {
	ctx := testCtxAuto(t)
	ws := newTestWorkspace(t)
	newProgressNode(t, ws, ctx, "prog", 100) // poll k → sim_time = k*100

	prog := `
n = workspace.node("prog")
r = n.wait(sim_time=500, timeout="5s", poll_every="5ms")
print(r["timed_out"])
print(r["info"]["sim_time"] >= 500)
`
	res := mustRunAuto(t, ctx, ws, prog)
	lines := strings.Split(strings.TrimRight(res.Output, "\n"), "\n")
	if len(lines) != 2 || lines[0] != "False" || lines[1] != "True" {
		t.Fatalf("wait did not reach condition cleanly; output:\n%s", res.Output)
	}
}

// TestNodeWait_TimesOut: an unreachable target returns timed_out=True (graceful,
// no error) once the timeout elapses.
func TestNodeWait_TimesOut(t *testing.T) {
	ctx := testCtxAuto(t)
	ws := newTestWorkspace(t)
	newProgressNode(t, ws, ctx, "prog", 1)

	prog := `
n = workspace.node("prog")
r = n.wait(sim_time=1000000000000.0, timeout="60ms", poll_every="20ms")
print(r["timed_out"])
`
	res := mustRunAuto(t, ctx, ws, prog)
	if got := strings.TrimSpace(res.Output); got != "True" {
		t.Fatalf("timed_out = %q, want True; output:\n%s", got, res.Output)
	}
}

// TestNodeWait_Cancel: cancelling the run context while a wait is parked aborts
// promptly with a "cancelled" diagnostic (distinct from a graceful timeout).
func TestNodeWait_Cancel(t *testing.T) {
	ws := newTestWorkspace(t)
	newProgressNode(t, ws, context.Background(), "prog", 1)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(40 * time.Millisecond)
		cancel()
	}()

	prog := `
n = workspace.node("prog")
n.wait(sim_time=1000000000000.0, timeout="10s", poll_every="20ms")
`
	res, err := runAuto(ctx, ws, prog)
	if err == nil {
		t.Fatalf("wait should abort on context cancel, got nil error")
	}
	cancelled := false
	for _, d := range res.Diagnostics {
		if d.Code == "cancelled" {
			cancelled = true
		}
	}
	if !cancelled {
		t.Fatalf("want a 'cancelled' diagnostic, got %+v (err=%v)", res.Diagnostics, err)
	}
}

// TestWorkspacePoll_StopsOnCondition: poll runs the body until it returns truthy,
// reporting the iteration count and reason "condition".
func TestWorkspacePoll_StopsOnCondition(t *testing.T) {
	ctx := testCtxAuto(t)
	ws := newTestWorkspace(t)
	newProgressNode(t, ws, ctx, "prog", 100) // each info() call advances sim_time by 100

	prog := `
n = workspace.node("prog")
def each():
    return n.info()["sim_time"] >= 300
res = workspace.poll(do=each, every="5ms", timeout="5s")
print(res["iters"])
print(res["reason"])
`
	res := mustRunAuto(t, ctx, ws, prog)
	lines := strings.Split(strings.TrimRight(res.Output, "\n"), "\n")
	if len(lines) != 2 || lines[0] != "3" || lines[1] != "condition" {
		t.Fatalf("poll did not stop on condition at iter 3; output:\n%s", res.Output)
	}
}

// TestWorkspacePoll_TimeoutAndMaxIters: a body that never signals done stops on
// the timeout (reason "timeout") and, separately, on max_iters (reason
// "max_iters").
func TestWorkspacePoll_TimeoutAndMaxIters(t *testing.T) {
	ctx := testCtxAuto(t)
	ws := newTestWorkspace(t)

	timeoutProg := `
def each():
    return False
res = workspace.poll(do=each, every="20ms", timeout="60ms")
print(res["reason"])
print(res["timed_out"])
`
	res := mustRunAuto(t, ctx, ws, timeoutProg)
	lines := strings.Split(strings.TrimRight(res.Output, "\n"), "\n")
	if len(lines) != 2 || lines[0] != "timeout" || lines[1] != "True" {
		t.Fatalf("poll timeout path wrong; output:\n%s", res.Output)
	}

	maxItersProg := `
def each():
    return False
res = workspace.poll(do=each, every="1ms", timeout="5s", max_iters=2)
print(res["iters"])
print(res["reason"])
print(res["timed_out"])
`
	res = mustRunAuto(t, ctx, ws, maxItersProg)
	lines = strings.Split(strings.TrimRight(res.Output, "\n"), "\n")
	if len(lines) != 3 || lines[0] != "2" || lines[1] != "max_iters" || lines[2] != "False" {
		t.Fatalf("poll max_iters path wrong; output:\n%s", res.Output)
	}
}

// TestWaitDoesNotHoldWorkspaceLock is the headline guarantee: while one run is
// parked in node.wait, other operations on the SAME workspace proceed. A
// whole-run/global lock would make the concurrent NodeInfo calls below block
// until the wait finished, starving this loop.
func TestWaitDoesNotHoldWorkspaceLock(t *testing.T) {
	ws := newTestWorkspace(t)
	newProgressNode(t, ws, context.Background(), "waiter", 1) // slow; will time out
	_, _ = newFakeNode(t, ws, "other")                        // fixed-info node (active set)

	done := make(chan error, 1)
	go func() {
		// Unreachable target → this run parks for the full 1s timeout.
		prog := `
n = workspace.node("waiter")
n.wait(sim_time=1000000000000.0, timeout="1s", poll_every="10ms")
`
		_, err := runAuto(context.Background(), ws, prog)
		done <- err
	}()

	calls := 0
	deadline := time.After(400 * time.Millisecond)
busy:
	for {
		select {
		case <-deadline:
			break busy
		case err := <-done:
			t.Fatalf("waiter finished early (%v) — setup wrong", err)
		default:
		}
		if _, err := ws.NodeInfo(context.Background(), "other"); err != nil {
			t.Fatalf("concurrent NodeInfo during a parked wait failed: %v", err)
		}
		calls++
	}
	if calls < 5 {
		t.Fatalf("only %d concurrent ops completed during the parked wait; a parked wait must not hold the workspace lock", calls)
	}
	if err := <-done; err != nil {
		t.Fatalf("waiter run returned error: %v", err)
	}
}
