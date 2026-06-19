package workspace

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/asemones/bibicontrol/ipc"
	"github.com/asemones/bibicontrol/noderuntime"
	"github.com/asemones/bibicontrol/revisionstore"
	"github.com/google/uuid"
)

// StartNodeSpec is the workspace-level input for starting a game node. It
// wraps noderuntime.Spec with the persistence and binding fields D1 owns.
type StartNodeSpec struct {
	// WorldID is the world to bind the node to. Required.
	WorldID string

	// NodeID is the logical node id (active-set key and nodes.node_id). If
	// empty, a fresh UUID is assigned.
	NodeID string

	// RunID is the run identity stored in nodes.run_id and passed to the
	// noderuntime.
	RunID string

	// Process is the OS process spec for the game node.
	Process ipc.ProcessSpec

	// CompatAddr is the game-owned TCP endpoint, e.g. "127.0.0.1:43100".
	// If empty, Start only launches the process.
	CompatAddr string

	// ConnectOnStart controls whether StartNode dials CompatAddr before
	// returning.
	ConnectOnStart bool

	// DialTimeout bounds the ConnectOnStart dial. Defaults to 10 seconds.
	DialTimeout time.Duration

	// DialInterval is the retry cadence when ConnectOnStart is true.
	// Defaults to 200 milliseconds.
	DialInterval time.Duration

	// Codec is the wire codec for the compat session.
	Codec ipc.Codec

	// DropPath is the filesystem path where the node writes drop files;
	// stored as nodes.drop_path for D3's ship-head path.
	DropPath string
}

// StartNode launches a game-node process, persists its identity and world
// binding as a nodes registry row, and records the live runtime in the
// active-node set. On any failure after the process is started the process
// is killed before returning so no OS resource is leaked.
//
// Invariants enforced:
//   - spec.WorldID must be non-empty.
//   - A logical node id must not already be live in the active set.
//   - A world may be bound to at most one active node at a time.
func (w *Workspace) StartNode(ctx context.Context, spec StartNodeSpec) (*noderuntime.Runtime, revisionstore.Node, error) {
	if w == nil {
		return nil, revisionstore.Node{}, fmt.Errorf("workspace: StartNode on nil workspace")
	}
	if spec.WorldID == "" {
		return nil, revisionstore.Node{}, fmt.Errorf("workspace: StartNode: WorldID is required")
	}

	// Resolve the logical node id before acquiring the lock so that the
	// default is stable even if we retry.
	nodeID := spec.NodeID
	if nodeID == "" {
		nodeID = uuid.NewString()
	}

	// --- pre-start uniqueness checks (under lock) ---
	w.mu.Lock()

	if _, exists := w.nodes[nodeID]; exists {
		w.mu.Unlock()
		return nil, revisionstore.Node{}, fmt.Errorf("workspace: StartNode: node %q is already active", nodeID)
	}

	if conflict, err := w.activeNodeForWorldLocked(ctx, spec.WorldID); err != nil {
		w.mu.Unlock()
		return nil, revisionstore.Node{}, fmt.Errorf("workspace: StartNode: list nodes for world check: %w", err)
	} else if conflict != "" {
		w.mu.Unlock()
		return nil, revisionstore.Node{}, fmt.Errorf("workspace: StartNode: world %q is already bound to active node %q", spec.WorldID, conflict)
	}

	w.mu.Unlock()
	// --- end pre-start checks ---

	// Obtain (or lazily create) the log ring for this node. Done outside w.mu
	// to avoid nesting logMu under w.mu.
	ring := w.logRingFor(nodeID)

	// Build the noderuntime spec and launch the process. This may block if
	// ConnectOnStart is true, so we do it outside the lock.
	proc := spec.Process // local copy; we may replace Stdout/Stderr below
	stdoutWriter := ipc.Writer(&logBufferWriter{ring: ring, level: "info"})
	if proc.Stdout != nil {
		proc.Stdout = io.MultiWriter(proc.Stdout, stdoutWriter)
	} else {
		proc.Stdout = stdoutWriter
	}
	stderrWriter := ipc.Writer(&logBufferWriter{ring: ring, level: "error"})
	if proc.Stderr != nil {
		proc.Stderr = io.MultiWriter(proc.Stderr, stderrWriter)
	} else {
		proc.Stderr = stderrWriter
	}
	ns := noderuntime.Spec{
		NodeID:         nodeID,
		RunID:          spec.RunID,
		Process:        proc,
		CompatAddr:     spec.CompatAddr,
		ConnectOnStart: spec.ConnectOnStart,
		DialTimeout:    spec.DialTimeout,
		DialInterval:   spec.DialInterval,
		Codec:          spec.Codec,
	}

	rt, err := noderuntime.Start(ctx, ns)
	if err != nil {
		return nil, revisionstore.Node{}, fmt.Errorf("workspace: StartNode: start process: %w", err)
	}

	// Re-acquire the lock and re-check BOTH uniqueness invariants before
	// persisting. The lock was dropped unconditionally around Start, so a
	// concurrent StartNode may have claimed the same logical id OR bound
	// another live node to spec.WorldID in the meantime. Re-checking only the
	// dup-id here would let two concurrent starts for the same world both
	// survive — re-run the per-world scan as well.
	w.mu.Lock()

	if _, exists := w.nodes[nodeID]; exists {
		w.mu.Unlock()
		// Orphan-process cleanup: kill the just-started process.
		_ = rt.Kill()
		_ = rt.Close()
		return nil, revisionstore.Node{}, fmt.Errorf("workspace: StartNode: node %q became active concurrently", nodeID)
	}

	if conflict, err := w.activeNodeForWorldLocked(ctx, spec.WorldID); err != nil {
		w.mu.Unlock()
		_ = rt.Kill()
		_ = rt.Close()
		return nil, revisionstore.Node{}, fmt.Errorf("workspace: StartNode: list nodes for world re-check: %w", err)
	} else if conflict != "" {
		w.mu.Unlock()
		// Another node won the race for this world; kill the orphan we started.
		_ = rt.Kill()
		_ = rt.Close()
		return nil, revisionstore.Node{}, fmt.Errorf("workspace: StartNode: world %q became bound to active node %q concurrently", spec.WorldID, conflict)
	}

	// Persist the node row. This binds the node to the world via CreateNode's
	// world_id column — no separate BindNode call needed on the start path.
	node, err := w.store().CreateNode(ctx, revisionstore.NodeInput{
		WorkspaceID: w.id,
		WorldID:     spec.WorldID,
		NodeID:      nodeID,
		RunID:       spec.RunID,
		Status:      "running",
		CompatAddr:  spec.CompatAddr,
		DropPath:    spec.DropPath,
	})
	if err != nil {
		w.mu.Unlock()
		// Orphan-process cleanup: the process is running but we failed to
		// persist it — kill it so no OS resource is stranded.
		_ = rt.Kill()
		_ = rt.Close()
		return nil, revisionstore.Node{}, fmt.Errorf("workspace: StartNode: persist node: %w", err)
	}

	// Record in the active set. The map is keyed by logical node id.
	w.nodes[nodeID] = rt
	w.mu.Unlock()

	// M9: Register a supervisor watcher AFTER the active-set insert so the
	// reaper only fires for nodes that are actually in w.nodes. A process that
	// dies between noderuntime.Start and the insert is caught by the existing
	// post-start re-check above (if the process is already dead when we reach
	// here the Done channel is already closed and the goroutine fires
	// immediately, but reapNode's same-runtime guard makes it a no-op if the
	// workspace has already been closed or the node replaced).
	if w.supervisor != nil {
		w.supervisor.Watch(nodeID, rt.Process().Done(), w.reapNode)
	}

	return rt, node, nil
}

// Nodes returns a snapshot of the active-node set. The returned slice is a
// copy; callers may not mutate it.
func (w *Workspace) Nodes() []*noderuntime.Runtime {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]*noderuntime.Runtime, 0, len(w.nodes))
	for _, rt := range w.nodes {
		out = append(out, rt)
	}
	return out
}

// Node returns the active runtime for the given logical node id. Returns
// (nil, false) when no active node matches.
func (w *Workspace) Node(nodeID string) (*noderuntime.Runtime, bool) {
	if w == nil {
		return nil, false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	rt, ok := w.nodes[nodeID]
	return rt, ok
}

// PersistedNodes returns the nodes registry rows for this workspace. It is a
// thin passthrough to ListNodes and includes both active and historical rows.
func (w *Workspace) PersistedNodes(ctx context.Context) ([]revisionstore.Node, error) {
	if w == nil {
		return nil, fmt.Errorf("workspace: PersistedNodes on nil workspace")
	}
	nodes, err := w.store().ListNodes(ctx, w.id)
	if err != nil {
		return nil, fmt.Errorf("workspace: PersistedNodes: %w", err)
	}
	return nodes, nil
}

// StopNode gracefully stops the node identified by nodeID, closes the compat
// session, removes it from the active set, and updates its persisted status
// to "stopped".
func (w *Workspace) StopNode(ctx context.Context, nodeID string, opts noderuntime.StopOptions) error {
	if w == nil {
		return fmt.Errorf("workspace: StopNode on nil workspace")
	}

	w.mu.Lock()
	rt, ok := w.nodes[nodeID]
	if !ok {
		w.mu.Unlock()
		return fmt.Errorf("workspace: StopNode: unknown node %q", nodeID)
	}
	w.mu.Unlock()

	// M9: Cancel the supervisor watcher BEFORE stopping the process so the
	// reaper does not double-fire after a clean stop. Cancel is idempotent if
	// the watcher has already fired (the process died concurrently).
	if w.supervisor != nil {
		w.supervisor.Cancel(nodeID)
	}

	// Stop the process (outside the lock to avoid holding it across blocking
	// waits).
	if _, err := rt.Stop(ctx, opts); err != nil {
		// Best-effort: continue to update status and remove from active set.
		_ = err
	}
	_ = rt.Close()

	// Remove from active set and persist the status change.
	w.mu.Lock()
	delete(w.nodes, nodeID)
	w.mu.Unlock()
	w.dropLogRing(nodeID)

	return w.setNodeStatusByLogicalID(ctx, nodeID, "stopped")
}

// KillNode force-kills the node identified by nodeID, closes the compat
// session, removes it from the active set, and updates its persisted status
// to "stopped".
func (w *Workspace) KillNode(ctx context.Context, nodeID string) error {
	if w == nil {
		return fmt.Errorf("workspace: KillNode on nil workspace")
	}

	w.mu.Lock()
	rt, ok := w.nodes[nodeID]
	if !ok {
		w.mu.Unlock()
		return fmt.Errorf("workspace: KillNode: unknown node %q", nodeID)
	}
	w.mu.Unlock()

	// M9: Cancel the supervisor watcher before killing so the reaper does not
	// double-fire. Cancel is idempotent if the watcher has already fired.
	if w.supervisor != nil {
		w.supervisor.Cancel(nodeID)
	}

	_ = rt.Kill()
	_ = rt.Close()

	w.mu.Lock()
	delete(w.nodes, nodeID)
	w.mu.Unlock()
	w.dropLogRing(nodeID)

	return w.setNodeStatusByLogicalID(ctx, nodeID, "stopped")
}

// activeNodeForWorldLocked enforces the one-node-per-world invariant against
// the LIVE active set. It returns the logical id of an active node already
// bound to worldID, or "" when the world is free. The caller must hold w.mu.
//
// Liveness is anchored on w.nodes: a world is "bound" only if some key present
// in the in-memory active set maps (per its persisted row) to worldID. A stale
// persisted "running" row for a node that is no longer in w.nodes does not
// block a new bind. The persisted rows are consulted only to discover the
// world each active node is bound to (the active-set value is the runtime, not
// the binding).
func (w *Workspace) activeNodeForWorldLocked(ctx context.Context, worldID string) (string, error) {
	if len(w.nodes) == 0 {
		return "", nil
	}
	persisted, err := w.store().ListNodes(ctx, w.id)
	if err != nil {
		return "", err
	}
	// Map logical node_id -> world_id across ALL persisted rows (no status
	// filter; liveness is decided by w.nodes membership below).
	persistedWorld := make(map[string]string, len(persisted))
	for _, n := range persisted {
		persistedWorld[n.NodeID] = n.WorldID
	}
	for activeID := range w.nodes {
		if wid, ok := persistedWorld[activeID]; ok && wid == worldID {
			return activeID, nil
		}
	}
	return "", nil
}

// setNodeStatusByLogicalID resolves the nodes PK from the logical node_id
// and calls SetNodeStatus. SetNodeStatus keys on the PK (Node.ID), not the
// logical node_id, so this indirection is required.
func (w *Workspace) setNodeStatusByLogicalID(ctx context.Context, nodeID, status string) error {
	persisted, err := w.store().ListNodes(ctx, w.id)
	if err != nil {
		return fmt.Errorf("workspace: setNodeStatus %q: list nodes: %w", nodeID, err)
	}
	for _, n := range persisted {
		if n.NodeID == nodeID {
			return w.store().SetNodeStatus(ctx, n.ID, status)
		}
	}
	return fmt.Errorf("workspace: setNodeStatus: no persisted row for logical node %q", nodeID)
}

// reapNode is the supervisor callback fired when a node's OS process exits
// without a workspace-driven Stop/Kill. It:
//  1. Under w.mu: removes the runtime from w.nodes if it is still present.
//     The invariant against reaping a NEW node that reused the logical id is
//     held by two complementary guards: (a) the Supervisor's sync.Once ensures
//     onExit fires at most once per Watch registration, and (b) the watcher
//     goroutine only deletes its own entry from s.entries when
//     s.entries[nodeID] == e, so a superseding Watch cannot be accidentally
//     cancelled or double-fired.
//  2. Drops the log ring for the node.
//  3. Writes a terminal persisted status ("crashed" for a failed exit,
//     "exited" otherwise). The write is idempotent-safe against a concurrent
//     StopNode/KillNode write (last-writer-wins, both write a terminal status).
//
// reapNode must NOT block while holding w.mu (mirrors the park-outside-lock
// discipline in StopNode). All non-trivial operations run after the lock is
// released.
func (w *Workspace) reapNode(nodeID string) {
	w.mu.Lock()
	rt, exists := w.nodes[nodeID]
	if exists {
		delete(w.nodes, nodeID)
	}
	w.mu.Unlock()

	if !exists || rt == nil {
		// Already removed by a concurrent StopNode/KillNode or Close drain.
		return
	}

	// Drop the log ring for the reaped node.
	w.dropLogRing(nodeID)

	// Derive the terminal status from the process exit state.
	// ipc.ProcessFailed → "crashed"; ipc.ProcessExited → "exited".
	status := "exited"
	if proc := rt.Process(); proc != nil {
		state := proc.Info().State
		if state == "failed" {
			status = "crashed"
		}
	}

	// Persist the terminal status. Use background context since reapNode is
	// called from the supervisor goroutine (no request context available).
	ctx := context.Background()
	_ = w.setNodeStatusByLogicalID(ctx, nodeID, status)
}

// NodeLiveness returns a live liveness verdict string for the given nodeID.
// The verdict is derived from the active-set membership and the process state:
//
//   - "running"   — the node is in the active set and its process is alive.
//   - "crashed"   — the node is in the active set but its process has failed.
//   - "stopped"   — the node is NOT active; its persisted status is "stopped".
//   - "exited"    — the node is NOT active; its persisted status is "exited".
//   - "detached"  — the node is NOT active and its persisted row is still
//     "running" (the supervisor has not yet reconciled it, or this is a fresh
//     Open with a stale row) — or no persisted row exists at all.
//
// NodeLiveness is the single source of truth called by handleNodesInfo and the
// Starlark node.status attribute so the HTTP and Starlark surfaces always agree.
func (w *Workspace) NodeLiveness(ctx context.Context, nodeID string) string {
	rt, live := w.Node(nodeID)
	if live {
		st := rt.State()
		if st.Process.State == "exited" || st.Process.State == "failed" {
			return "crashed"
		}
		return "running"
	}
	// Not active — consult the most-recent persisted row.
	// A persisted "running" status with no active entry means the supervisor
	// has not yet reconciled the row (or this is a fresh Open with a stale
	// "running" row). Expose this as "detached" so callers can distinguish
	// "cleanly stopped" from "process gone but row not yet updated".
	nodes, err := w.store().ListNodes(ctx, w.id)
	if err != nil {
		return "detached"
	}
	for _, n := range nodes {
		if n.NodeID == nodeID {
			if n.Status == "running" {
				return "detached"
			}
			return n.Status
		}
	}
	return "detached"
}
