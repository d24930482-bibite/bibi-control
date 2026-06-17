package workspace

import (
	"context"
	"fmt"

	"github.com/asemones/bibicontrol/ipc"
	"github.com/asemones/bibicontrol/noderuntime"
	"github.com/asemones/bibicontrol/simctl"
)

// NodeState is the combined snapshot of a node's runtime process/connection
// state and its last live telemetry from an INFO command.
//
// Info is a pointer so "no telemetry" (disconnected node) is representable
// distinctly from a zero-valued InfoResult. Connected is hoisted from
// Runtime.Connected so callers do not have to reach into Runtime.
//
// This shape is load-bearing for D3 (reload path) and E1 (node.info() Starlark
// binding). Keep Info as a pointer and Connected hoisted.
type NodeState struct {
	// Runtime holds the process/connection state from noderuntime.Runtime.State().
	Runtime noderuntime.State

	// Connected mirrors Runtime.Connected; hoisted for callers.
	Connected bool

	// Info is the last INFO telemetry; nil when the node is not connected.
	// A connected node that fails the INFO round-trip returns an error rather
	// than a nil Info (callers should not infer "no telemetry" for a connected
	// node).
	Info *ipc.InfoResult
}

// NodeInfo fetches live telemetry from the game node identified by nodeID.
// It returns an error when nodeID is not in the active set or when the node
// has no compat session (noderuntime.ErrNoSession, wrapped).
//
// The IPC round-trip runs without holding w.mu to avoid blocking all workspace
// operations behind a slow game process.
func (w *Workspace) NodeInfo(ctx context.Context, nodeID string) (ipc.InfoResult, error) {
	if w == nil {
		return ipc.InfoResult{}, fmt.Errorf("workspace: NodeInfo on nil workspace")
	}
	rt, ok := w.Node(nodeID)
	if !ok {
		return ipc.InfoResult{}, fmt.Errorf("workspace: NodeInfo: unknown node %q", nodeID)
	}
	// Lock is released by w.Node; IPC call runs without w.mu held.
	result, err := simctl.New(rt).Info(ctx)
	if err != nil {
		return ipc.InfoResult{}, fmt.Errorf("workspace: NodeInfo %q: %w", nodeID, err)
	}
	return result, nil
}

// NodeStop pauses the game simulation for the node identified by nodeID.
// It returns an error when nodeID is not in the active set or when the node
// has no compat session (noderuntime.ErrNoSession, wrapped).
//
// The IPC round-trip runs without holding w.mu.
func (w *Workspace) NodeStop(ctx context.Context, nodeID string) (ipc.StopResult, error) {
	if w == nil {
		return ipc.StopResult{}, fmt.Errorf("workspace: NodeStop on nil workspace")
	}
	rt, ok := w.Node(nodeID)
	if !ok {
		return ipc.StopResult{}, fmt.Errorf("workspace: NodeStop: unknown node %q", nodeID)
	}
	result, err := simctl.New(rt).Stop(ctx)
	if err != nil {
		return ipc.StopResult{}, fmt.Errorf("workspace: NodeStop %q: %w", nodeID, err)
	}
	return result, nil
}

// NodeResume runs the game simulation at the given time scale for the node
// identified by nodeID. timeScale must be > 0 (enforced server-side by the
// DLL; this method is a pure passthrough and does not add a client-side guard).
//
// It returns an error when nodeID is not in the active set or when the node
// has no compat session (noderuntime.ErrNoSession, wrapped).
//
// The IPC round-trip runs without holding w.mu.
func (w *Workspace) NodeResume(ctx context.Context, nodeID string, timeScale float64) (ipc.ResumeResult, error) {
	if w == nil {
		return ipc.ResumeResult{}, fmt.Errorf("workspace: NodeResume on nil workspace")
	}
	rt, ok := w.Node(nodeID)
	if !ok {
		return ipc.ResumeResult{}, fmt.Errorf("workspace: NodeResume: unknown node %q", nodeID)
	}
	result, err := simctl.New(rt).Resume(ctx, timeScale)
	if err != nil {
		return ipc.ResumeResult{}, fmt.Errorf("workspace: NodeResume %q: %w", nodeID, err)
	}
	return result, nil
}

// NodeState returns the combined runtime state and live telemetry for the node
// identified by nodeID.
//
// When the node is not connected (rt.Connected() == false), NodeState returns
// success with Connected == false and Info == nil — a started-but-not-yet-
// connected node is a legitimate state, not an error.
//
// When the node is connected but the INFO call fails, NodeState returns an
// error: a NodeState that claims connected-but-no-info would mislead D3/E1.
//
// Returns an error when nodeID is not in the active set.
func (w *Workspace) NodeState(ctx context.Context, nodeID string) (NodeState, error) {
	if w == nil {
		return NodeState{}, fmt.Errorf("workspace: NodeState on nil workspace")
	}
	rt, ok := w.Node(nodeID)
	if !ok {
		return NodeState{}, fmt.Errorf("workspace: NodeState: unknown node %q", nodeID)
	}
	// State() and Connected() each take/release the runtime's own lock.
	// Neither holds w.mu.
	rtState := rt.State()
	connected := rt.Connected()

	if !connected {
		return NodeState{
			Runtime:   rtState,
			Connected: false,
			Info:      nil,
		}, nil
	}

	// IPC round-trip runs without any lock held.
	info, err := simctl.New(rt).Info(ctx)
	if err != nil {
		return NodeState{}, fmt.Errorf("workspace: NodeState %q: info: %w", nodeID, err)
	}
	return NodeState{
		Runtime:   rtState,
		Connected: true,
		Info:      &info,
	}, nil
}
