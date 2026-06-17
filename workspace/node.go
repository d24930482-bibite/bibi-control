package workspace

import (
	"context"
	"fmt"
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

	for activeID, _ := range w.nodes {
		// We need to look up the world binding for each active runtime via the
		// persisted store. But since we hold w.mu we cannot block on a
		// potentially slow DB call; use PersistedNodes (which acquires no
		// separate lock) while still holding w.mu. The store is safe for
		// concurrent reads.
		_ = activeID
	}

	// Scan active node ids against their persisted world bindings to enforce
	// the one-node-per-world invariant. We do this under the lock so no
	// concurrent StartNode can race us.
	if len(w.nodes) > 0 {
		persisted, err := w.store().ListNodes(ctx, w.id)
		if err != nil {
			w.mu.Unlock()
			return nil, revisionstore.Node{}, fmt.Errorf("workspace: StartNode: list nodes for world check: %w", err)
		}
		// Build a map from logical node_id to world_id for persisted running rows.
		persistedWorld := make(map[string]string, len(persisted))
		for _, n := range persisted {
			persistedWorld[n.NodeID] = n.WorldID
		}
		for activeID := range w.nodes {
			if wid, ok := persistedWorld[activeID]; ok && wid == spec.WorldID {
				w.mu.Unlock()
				return nil, revisionstore.Node{}, fmt.Errorf("workspace: StartNode: world %q is already bound to active node %q", spec.WorldID, activeID)
			}
		}
	}

	w.mu.Unlock()
	// --- end pre-start checks ---

	// Build the noderuntime spec and launch the process. This may block if
	// ConnectOnStart is true, so we do it outside the lock.
	ns := noderuntime.Spec{
		NodeID:         nodeID,
		RunID:          spec.RunID,
		Process:        spec.Process,
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

	// Re-acquire the lock and re-check uniqueness before persisting. Another
	// concurrent StartNode may have snuck in while we were blocked in Start.
	w.mu.Lock()

	if _, exists := w.nodes[nodeID]; exists {
		w.mu.Unlock()
		// Orphan-process cleanup: kill the just-started process.
		_ = rt.Kill()
		_ = rt.Close()
		return nil, revisionstore.Node{}, fmt.Errorf("workspace: StartNode: node %q became active concurrently", nodeID)
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

	_ = rt.Kill()
	_ = rt.Close()

	w.mu.Lock()
	delete(w.nodes, nodeID)
	w.mu.Unlock()

	return w.setNodeStatusByLogicalID(ctx, nodeID, "stopped")
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
