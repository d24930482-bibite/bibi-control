// Package workspace — automation.go
//
// This file is the E1 effectful automation binding layer. It exposes the
// workspace's Go methods (worlds, nodes, info/ingest/reload, read-only queries)
// to Starlark scripts so an operator can automate the control loop from a single
// hermetic Starlark invocation.
//
// Trust boundary: this surface is effectful and host-trusted (it does IO, IPC,
// and database writes). It is entirely separate from the sandboxed save DSL
// (script/thebibites.Globals). A single RunAutomation call is one hermetic cycle
// — Starlark is non-looping; the host re-invokes on a timer or event
// (workspace_plan.md:430-432).
//
// Deferred names: workspace.gc() is NOT bound here (G3). Callers that reference
// that name receive a normal Starlark "has no .X attribute" error from the nil
// return in Attr. workspace.transfer() is bound (F2): it grafts the selected
// source bibites/eggs (a where-collection or a single entity — the object DSL,
// never raw SQL) into a destination world and commits an advancing-head revision.
package workspace

import (
	"context"
	"fmt"
	"math/big"

	"github.com/asemones/bibicontrol/duckdb"
	"github.com/asemones/bibicontrol/ipc"
	"github.com/asemones/bibicontrol/revisionstore"
	"github.com/asemones/bibicontrol/script"
	"github.com/asemones/bibicontrol/script/thebibites"
	"go.starlark.net/starlark"
)

// RunAutomation executes a Starlark automation program against the given
// workspace. It builds the globals dict ({"workspace": &workspaceValue{...}})
// and delegates to script.Run. The run context is threaded into every binding
// via the value graph so callers can cancel long-running operations.
func RunAutomation(ctx context.Context, ws *Workspace, program []byte, opts script.Options) (script.Result, error) {
	return script.Run(ctx, program, AutomationGlobals(ctx, ws), opts)
}

// AutomationGlobals returns the predeclared Starlark globals for an automation
// run. The returned dict has one key: "workspace", bound to a *workspaceValue
// that carries the context and delegates all methods to ws.
func AutomationGlobals(ctx context.Context, ws *Workspace) starlark.StringDict {
	return starlark.StringDict{
		"workspace": &workspaceValue{ctx: ctx, ws: ws},
	}
}

// ---------------------------------------------------------------------------
// workspaceValue — the root Starlark object
// ---------------------------------------------------------------------------

type workspaceValue struct {
	ctx context.Context
	ws  *Workspace
}

var (
	_ starlark.Value    = (*workspaceValue)(nil)
	_ starlark.HasAttrs = (*workspaceValue)(nil)
)

func (v *workspaceValue) String() string        { return "workspace" }
func (v *workspaceValue) Type() string          { return "workspace" }
func (v *workspaceValue) Freeze()               {}
func (v *workspaceValue) Truth() starlark.Bool  { return starlark.True }
func (v *workspaceValue) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable type: workspace") }

func (v *workspaceValue) AttrNames() []string {
	return []string{"add_world", "node", "nodes", "query", "start_node", "transfer", "world", "worlds"}
}

func (v *workspaceValue) Attr(name string) (starlark.Value, error) {
	switch name {
	case "worlds":
		return starlark.NewBuiltin("worlds", v.worldsBuiltin), nil
	case "world":
		return starlark.NewBuiltin("world", v.worldBuiltin), nil
	case "add_world":
		return starlark.NewBuiltin("add_world", v.addWorldBuiltin), nil
	case "nodes":
		return starlark.NewBuiltin("nodes", v.nodesBuiltin), nil
	case "node":
		return starlark.NewBuiltin("node", v.nodeBuiltin), nil
	case "start_node":
		return starlark.NewBuiltin("start_node", v.startNodeBuiltin), nil
	case "query":
		return starlark.NewBuiltin("query", v.queryBuiltin), nil
	case "transfer":
		return starlark.NewBuiltin("transfer", v.transferBuiltin), nil
	default:
		// Deferred (G3) names return (nil, nil) — Starlark reports "has no .X attribute".
		return nil, nil
	}
}

// workspace.worlds() → list of worldValue
func (v *workspaceValue) worldsBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(b.Name(), args, kwargs); err != nil {
		return nil, err
	}
	worlds, err := v.ws.store().ListWorlds(v.ctx, v.ws.ID())
	if err != nil {
		return nil, fmt.Errorf("workspace.worlds: %w", err)
	}
	elems := make([]starlark.Value, len(worlds))
	for i, w := range worlds {
		elems[i] = &worldValue{ctx: v.ctx, ws: v.ws, world: w}
	}
	return starlark.NewList(elems), nil
}

// workspace.world(id) → worldValue or error
func (v *workspaceValue) worldBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var id string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "id", &id); err != nil {
		return nil, err
	}
	w, err := v.ws.store().GetWorld(v.ctx, id)
	if err != nil {
		if revisionstore.IsNotFound(err) {
			return nil, fmt.Errorf("workspace.world: world %q not found", id)
		}
		return nil, fmt.Errorf("workspace.world: %w", err)
	}
	return &worldValue{ctx: v.ctx, ws: v.ws, world: w}, nil
}

// workspace.add_world(path, name) → worldValue
func (v *workspaceValue) addWorldBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var path, name string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "path", &path, "name", &name); err != nil {
		return nil, err
	}
	w, err := v.ws.AddWorld(v.ctx, path, name)
	if err != nil {
		return nil, fmt.Errorf("workspace.add_world: %w", err)
	}
	return &worldValue{ctx: v.ctx, ws: v.ws, world: w}, nil
}

// workspace.nodes() → list of nodeValue
func (v *workspaceValue) nodesBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(b.Name(), args, kwargs); err != nil {
		return nil, err
	}
	nodes, err := v.ws.PersistedNodes(v.ctx)
	if err != nil {
		return nil, fmt.Errorf("workspace.nodes: %w", err)
	}
	elems := make([]starlark.Value, len(nodes))
	for i, n := range nodes {
		elems[i] = &nodeValue{ctx: v.ctx, ws: v.ws, node: n}
	}
	return starlark.NewList(elems), nil
}

// workspace.node(id) → nodeValue or error
func (v *workspaceValue) nodeBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var id string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "id", &id); err != nil {
		return nil, err
	}
	nodes, err := v.ws.PersistedNodes(v.ctx)
	if err != nil {
		return nil, fmt.Errorf("workspace.node: %w", err)
	}
	for _, n := range nodes {
		if n.NodeID == id {
			return &nodeValue{ctx: v.ctx, ws: v.ws, node: n}, nil
		}
	}
	return nil, fmt.Errorf("workspace.node: node %q not found", id)
}

// workspace.start_node(world=, path=, compat_addr=, drop_path=, node_id=, run_id=, connect=) → nodeValue
func (v *workspaceValue) startNodeBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var worldID, path, compatAddr, dropPath, nodeID, runID string
	var connect bool
	if err := starlark.UnpackArgs(b.Name(), args, kwargs,
		"world", &worldID,
		"path?", &path,
		"compat_addr?", &compatAddr,
		"drop_path?", &dropPath,
		"node_id?", &nodeID,
		"run_id?", &runID,
		"connect?", &connect,
	); err != nil {
		return nil, err
	}
	spec := StartNodeSpec{
		WorldID:        worldID,
		NodeID:         nodeID,
		RunID:          runID,
		CompatAddr:     compatAddr,
		DropPath:       dropPath,
		ConnectOnStart: connect,
		Process: ipc.ProcessSpec{
			Path: path,
		},
	}
	_, node, err := v.ws.StartNode(v.ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("workspace.start_node: %w", err)
	}
	return &nodeValue{ctx: v.ctx, ws: v.ws, node: node}, nil
}

// workspace.query(sql) → list of dicts
func (v *workspaceValue) queryBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var sql string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "sql", &sql); err != nil {
		return nil, err
	}
	rows, err := v.ws.Query(v.ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("workspace.query: %w", err)
	}
	return mapsToStarlark(rows)
}

// workspace.transfer(selector, dst=<worldID or world handle>) → dict
// {transferred, committed, revision_id, sha256}. selector is the object DSL — a
// bibites/eggs collection (src.bibites.where(...)) or a single bibite/egg Entity
// — naming WHAT to graft; the user never writes SQL/JOINs. dst names the
// destination world (a bare id string OR a world handle). The selected entries are
// grafted into dst via the merged F1/F3 transfer engine (identity reconcile +
// per-world species remap) and committed as one advancing-head dst revision.
//
// committed=False with transferred=0 when nothing was selected (a clean no-op that
// leaves the dst head unchanged). On any graft failure the whole transfer fails
// loudly and nothing is committed (all-or-nothing at the commit boundary).
func (v *workspaceValue) transferBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var selector, dst starlark.Value
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "selector", &selector, "dst", &dst); err != nil {
		return nil, err
	}

	// Resolve the selection from the object DSL value. The script passes the BARE
	// thebibites types (world.open().bibites.where(...) / a single Entity), not a
	// workspace wrapper — match the concrete types. A grouped/element collection, a
	// non-bibite/egg kind, or any other value is rejected loudly (no SQL escape
	// hatch — the selector is the object DSL only).
	var srcLS *thebibites.LoadedSave
	var names []string
	switch sel := selector.(type) {
	case *thebibites.EntityCollection:
		srcLS = sel.SourceLoadedSave()
		n, err := sel.EntryNames()
		if err != nil {
			return nil, fmt.Errorf("workspace.transfer: %w", err)
		}
		names = n
	case *thebibites.Entity:
		srcLS = sel.SourceLoadedSave()
		names = []string{sel.EntryName()}
	default:
		return nil, fmt.Errorf("workspace.transfer: selector must be a bibites/eggs collection or a single bibite/egg, got %s", selector.Type())
	}

	// Resolve dst to a world id: a bare id string OR a world handle.
	var dstWorldID string
	switch d := dst.(type) {
	case starlark.String:
		dstWorldID = string(d)
	case *worldValue:
		dstWorldID = d.world.ID
	default:
		return nil, fmt.Errorf("workspace.transfer: dst must be a world id string or a world handle, got %s", dst.Type())
	}

	rev, err := v.ws.Transfer(v.ctx, srcLS, names, dstWorldID)
	if err != nil {
		return nil, fmt.Errorf("workspace.transfer: %w", err)
	}

	committed := rev.ID != 0
	transferred := 0
	if committed {
		transferred = len(names)
	}
	res := starlark.NewDict(4)
	_ = res.SetKey(starlark.String("transferred"), starlark.MakeInt(transferred))
	_ = res.SetKey(starlark.String("committed"), starlark.Bool(committed))
	_ = res.SetKey(starlark.String("revision_id"), starlark.MakeInt64(rev.ID))
	_ = res.SetKey(starlark.String("sha256"), starlark.String(rev.SHA256))
	return res, nil
}

// ---------------------------------------------------------------------------
// worldValue — handle for a world row
// ---------------------------------------------------------------------------

type worldValue struct {
	ctx   context.Context
	ws    *Workspace
	world revisionstore.World
}

var (
	_ starlark.Value    = (*worldValue)(nil)
	_ starlark.HasAttrs = (*worldValue)(nil)
)

func (v *worldValue) String() string        { return fmt.Sprintf("world(%q)", v.world.ID) }
func (v *worldValue) Type() string          { return "world" }
func (v *worldValue) Freeze()               {}
func (v *worldValue) Truth() starlark.Bool  { return starlark.True }
func (v *worldValue) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable type: world") }

func (v *worldValue) AttrNames() []string {
	return []string{"evict_history", "head", "history_query", "id", "load", "name", "open", "query", "sim_time", "unload"}
}

func (v *worldValue) Attr(name string) (starlark.Value, error) {
	switch name {
	case "id":
		return starlark.String(v.world.ID), nil
	case "name":
		return starlark.String(v.world.Name), nil
	case "head":
		if v.world.HeadRevisionID == nil {
			return starlark.None, nil
		}
		return starlark.MakeInt64(*v.world.HeadRevisionID), nil
	case "sim_time":
		if v.world.SimTime == nil {
			return starlark.None, nil
		}
		return starlark.Float(*v.world.SimTime), nil
	case "history_query":
		return starlark.NewBuiltin("history_query", v.historyQueryBuiltin), nil
	case "evict_history":
		return starlark.NewBuiltin("evict_history", v.evictHistoryBuiltin), nil
	case "load":
		return starlark.NewBuiltin("load", v.loadBuiltin), nil
	case "unload":
		return starlark.NewBuiltin("unload", v.unloadBuiltin), nil
	case "open":
		return starlark.NewBuiltin("open", v.openBuiltin), nil
	case "query":
		return starlark.NewBuiltin("query", v.queryBuiltin), nil
	default:
		return nil, nil
	}
}

// world.open() → saveValue. It returns a Save object wrapping the world's
// already-loaded working copy (OpenWorld lazy-loads it if absent). The object
// exposes the proven sandboxed read/mutation surface (s.bibites/s.eggs/s.settings/
// s.zones/s.pellets/s.sql + .where().set()/.delete()) via delegation to the
// thebibites.Save value, plus an E2-owned head-advancing s.commit().
func (v *worldValue) openBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(b.Name(), args, kwargs); err != nil {
		return nil, err
	}
	ls, err := v.ws.OpenWorld(v.ctx, v.world.ID)
	if err != nil {
		return nil, fmt.Errorf("world.open: %w", err)
	}
	return &saveValue{ctx: v.ctx, ws: v.ws, worldID: v.world.ID, save: thebibites.NewSaveValue(ls)}, nil
}

// world.query(sql) → list of dicts. A read-only SELECT over the OPEN world's
// working partition (save_id == worldID). The read-only gate (ensureReadOnly) is
// applied at this binding — reusing the C4 gate with zero duplication — BEFORE
// the scoped working-copy query runs. A staged-but-uncommitted set is visible to
// this read (working-copy read-after-write via flushMirror in ls.query); the
// scoping CTE (working_saves) lives in LoadedSave.Query.
func (v *worldValue) queryBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var sql string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "sql", &sql); err != nil {
		return nil, err
	}
	if err := ensureReadOnly(sql); err != nil {
		return nil, fmt.Errorf("world.query: %w", err)
	}
	ls, err := v.ws.OpenWorld(v.ctx, v.world.ID)
	if err != nil {
		return nil, fmt.Errorf("world.query: %w", err)
	}
	rows, err := ls.Query(v.ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("world.query: %w", err)
	}
	return mapsToStarlark(rows)
}

// world.history_query(sql) → list of dicts
func (v *worldValue) historyQueryBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var sql string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "sql", &sql); err != nil {
		return nil, err
	}
	rows, err := v.ws.HistoryQuery(v.ctx, v.world.ID, sql)
	if err != nil {
		return nil, fmt.Errorf("world.history_query: %w", err)
	}
	return mapsToStarlark(rows)
}

// world.evict_history(keep_last=N | older_than=T) → dict
func (v *worldValue) evictHistoryBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var keepLast starlark.Value = starlark.None
	var olderThan starlark.Value = starlark.None
	if err := starlark.UnpackArgs(b.Name(), args, kwargs,
		"keep_last?", &keepLast,
		"older_than?", &olderThan,
	); err != nil {
		return nil, err
	}

	var policy EvictPolicy
	switch {
	case keepLast != starlark.None && olderThan != starlark.None:
		return nil, fmt.Errorf("world.evict_history: specify keep_last or older_than, not both")
	case keepLast != starlark.None:
		n, ok := keepLast.(starlark.Int)
		if !ok {
			return nil, fmt.Errorf("world.evict_history: keep_last must be an int, got %s", keepLast.Type())
		}
		nv, ok2 := n.Int64()
		if !ok2 {
			return nil, fmt.Errorf("world.evict_history: keep_last overflows int64")
		}
		policy = KeepLastN(int(nv))
	case olderThan != starlark.None:
		t, ok := olderThan.(starlark.Float)
		if !ok {
			if ti, ok2 := olderThan.(starlark.Int); ok2 {
				iv, _ := ti.Int64()
				policy = OlderThanSimTime(float64(iv))
				break
			}
			return nil, fmt.Errorf("world.evict_history: older_than must be a number, got %s", olderThan.Type())
		}
		policy = OlderThanSimTime(float64(t))
	default:
		return nil, fmt.Errorf("world.evict_history: specify keep_last or older_than")
	}

	result, err := v.ws.EvictWorldHistory(v.ctx, v.world.ID, policy)
	if err != nil {
		return nil, fmt.Errorf("world.evict_history: %w", err)
	}
	return evictResultToDict(result), nil
}

// world.load() → None
func (v *worldValue) loadBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(b.Name(), args, kwargs); err != nil {
		return nil, err
	}
	if _, err := v.ws.OpenWorld(v.ctx, v.world.ID); err != nil {
		return nil, fmt.Errorf("world.load: %w", err)
	}
	return starlark.None, nil
}

// world.unload() → None
func (v *worldValue) unloadBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(b.Name(), args, kwargs); err != nil {
		return nil, err
	}
	if err := v.ws.Unload(v.world.ID); err != nil {
		return nil, fmt.Errorf("world.unload: %w", err)
	}
	return starlark.None, nil
}

// ---------------------------------------------------------------------------
// saveValue — an open working copy of a world (world.open())
// ---------------------------------------------------------------------------

// saveValue is the Starlark object returned by world.open(). It wraps the
// world's cached working copy (a thebibites.Save over the *LoadedSave) and
// delegates every read/mutation attribute (bibites/eggs/settings/zones/pellets/
// sql/where/…) to that proven DSL value, re-implementing nothing. Only commit is
// E2-owned: s.commit() advances the world head over the already-staged session
// (CommitWorldLoaded), as opposed to the sandboxed DSL's file-write commit.
//
// It carries worldID (NOT a detached *LoadedSave copy) so s.commit() re-resolves
// the cached handle under w.mu — the staged session lives on w.worlds[worldID],
// and CommitWorldLoaded commits THAT same pointer.
type saveValue struct {
	ctx     context.Context
	ws      *Workspace
	worldID string
	save    *thebibites.Save
}

var (
	_ starlark.Value    = (*saveValue)(nil)
	_ starlark.HasAttrs = (*saveValue)(nil)
)

func (v *saveValue) String() string        { return fmt.Sprintf("save(%q)", v.worldID) }
func (v *saveValue) Type() string          { return "save" }
func (v *saveValue) Freeze()               {}
func (v *saveValue) Truth() starlark.Bool  { return starlark.True }
func (v *saveValue) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable type: save") }

func (v *saveValue) AttrNames() []string {
	// Delegate the surface to the proven Save value; commit is already present in
	// Save.AttrNames, and E2's commit shadows it, so the name appears once.
	return v.save.AttrNames()
}

func (v *saveValue) Attr(name string) (starlark.Value, error) {
	// commit is E2-owned (head-advancing); it shadows the DSL's file-write commit.
	if name == "commit" {
		return starlark.NewBuiltin("commit", v.commitBuiltin), nil
	}
	// Every other attribute delegates to the proven Save value (the entire
	// read/mutation surface — bibites/eggs/settings/zones/pellets/sql/where/…).
	return v.save.Attr(name)
}

// s.commit() → dict {committed, revision_id, sha256}. It commits the mutations
// already staged on the open working copy and advances the world head to a new
// revision (CommitWorldLoaded — NOT a program re-run). A no-op (nothing staged /
// dry-run / autocommit off) returns committed=False with revision_id=0 and an
// empty sha256, and the head does not move.
func (v *saveValue) commitBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(b.Name(), args, kwargs); err != nil {
		return nil, err
	}
	rev, err := v.ws.CommitWorldLoaded(v.ctx, v.worldID, thebibites.RunOptions{})
	if err != nil {
		return nil, fmt.Errorf("save.commit: %w", err)
	}
	d := starlark.NewDict(3)
	committed := rev.ID != 0
	_ = d.SetKey(starlark.String("committed"), starlark.Bool(committed))
	_ = d.SetKey(starlark.String("revision_id"), starlark.MakeInt64(rev.ID))
	_ = d.SetKey(starlark.String("sha256"), starlark.String(rev.SHA256))
	return d, nil
}

// ---------------------------------------------------------------------------
// nodeValue — handle for a persisted node row
// ---------------------------------------------------------------------------

type nodeValue struct {
	ctx  context.Context
	ws   *Workspace
	node revisionstore.Node
}

var (
	_ starlark.Value    = (*nodeValue)(nil)
	_ starlark.HasAttrs = (*nodeValue)(nil)
)

func (v *nodeValue) String() string        { return fmt.Sprintf("node(%q)", v.node.NodeID) }
func (v *nodeValue) Type() string          { return "node" }
func (v *nodeValue) Freeze()               {}
func (v *nodeValue) Truth() starlark.Bool  { return starlark.True }
func (v *nodeValue) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable type: node") }

func (v *nodeValue) AttrNames() []string {
	return []string{"id", "ingest_autosave", "info", "kill", "reload", "resume", "run_id", "state", "status", "stop", "world"}
}

func (v *nodeValue) Attr(name string) (starlark.Value, error) {
	switch name {
	case "id":
		return starlark.String(v.node.NodeID), nil
	case "run_id":
		return starlark.String(v.node.RunID), nil
	case "world":
		return starlark.String(v.node.WorldID), nil
	case "status":
		return starlark.String(v.node.Status), nil
	case "info":
		return starlark.NewBuiltin("info", v.infoBuiltin), nil
	case "state":
		return starlark.NewBuiltin("state", v.stateBuiltin), nil
	case "stop":
		return starlark.NewBuiltin("stop", v.stopBuiltin), nil
	case "resume":
		return starlark.NewBuiltin("resume", v.resumeBuiltin), nil
	case "reload":
		return starlark.NewBuiltin("reload", v.reloadBuiltin), nil
	case "ingest_autosave":
		return starlark.NewBuiltin("ingest_autosave", v.ingestAutosaveBuiltin), nil
	case "kill":
		return starlark.NewBuiltin("kill", v.killBuiltin), nil
	default:
		return nil, nil
	}
}

// node.info() → dict
func (v *nodeValue) infoBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(b.Name(), args, kwargs); err != nil {
		return nil, err
	}
	result, err := v.ws.NodeInfo(v.ctx, v.node.NodeID)
	if err != nil {
		return nil, fmt.Errorf("node.info: %w", err)
	}
	return infoResultToDict(result), nil
}

// node.state() → dict
func (v *nodeValue) stateBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(b.Name(), args, kwargs); err != nil {
		return nil, err
	}
	ns, err := v.ws.NodeState(v.ctx, v.node.NodeID)
	if err != nil {
		return nil, fmt.Errorf("node.state: %w", err)
	}
	return nodeStateToDict(ns), nil
}

// node.stop() → dict
func (v *nodeValue) stopBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(b.Name(), args, kwargs); err != nil {
		return nil, err
	}
	result, err := v.ws.NodeStop(v.ctx, v.node.NodeID)
	if err != nil {
		return nil, fmt.Errorf("node.stop: %w", err)
	}
	return stopResultToDict(result), nil
}

// node.resume(scale) → dict
func (v *nodeValue) resumeBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var scale starlark.Float
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "scale", &scale); err != nil {
		return nil, err
	}
	result, err := v.ws.NodeResume(v.ctx, v.node.NodeID, float64(scale))
	if err != nil {
		return nil, fmt.Errorf("node.resume: %w", err)
	}
	return resumeResultToDict(result), nil
}

// node.reload() → dict
func (v *nodeValue) reloadBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(b.Name(), args, kwargs); err != nil {
		return nil, err
	}
	result, err := v.ws.ReloadNode(v.ctx, v.node.NodeID)
	if err != nil {
		// ErrNotRematerializable and any other Go error reach the script as a clean
		// Starlark error via (nil, err) — never swallowed, never panicked.
		return nil, fmt.Errorf("node.reload: %w", err)
	}
	return reloadResultToDict(result), nil
}

// node.ingest_autosave(path=None) → dict {ingested, revision_id, sha256}
func (v *nodeValue) ingestAutosaveBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var path string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "path?", &path); err != nil {
		return nil, err
	}
	rev, ingested, err := v.ws.IngestAutosave(v.ctx, v.node.NodeID, path)
	if err != nil {
		return nil, fmt.Errorf("node.ingest_autosave: %w", err)
	}
	d := starlark.NewDict(3)
	_ = d.SetKey(starlark.String("ingested"), starlark.Bool(ingested))
	if ingested {
		_ = d.SetKey(starlark.String("revision_id"), starlark.MakeInt64(rev.ID))
		_ = d.SetKey(starlark.String("sha256"), starlark.String(rev.SHA256))
	} else {
		_ = d.SetKey(starlark.String("revision_id"), starlark.MakeInt64(0))
		_ = d.SetKey(starlark.String("sha256"), starlark.String(""))
	}
	return d, nil
}

// node.kill() → None
func (v *nodeValue) killBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(b.Name(), args, kwargs); err != nil {
		return nil, err
	}
	if err := v.ws.KillNode(v.ctx, v.node.NodeID); err != nil {
		return nil, fmt.Errorf("node.kill: %w", err)
	}
	return starlark.None, nil
}

// ---------------------------------------------------------------------------
// Conversion helpers
// ---------------------------------------------------------------------------

// sqlScalarToStarlark converts a value scanned from a DuckDB result column into
// a Starlark value. It mirrors script/thebibites/convert.go:fromSQLValue but is
// re-implemented locally so E1 does not need to touch the thebibites serialization
// point. nil → None; bool/string/int64/uint64/float64/*big.Int → typed Starlark.
// Driver-scalar coercion ([]byte→string, narrow ints→int64) is delegated to
// duckdb.NormalizeSQLScanValue exactly as fromSQLValue does.
func sqlScalarToStarlark(v any) (starlark.Value, error) {
	switch x := duckdb.NormalizeSQLScanValue(v).(type) {
	case nil:
		return starlark.None, nil
	case bool:
		return starlark.Bool(x), nil
	case string:
		return starlark.String(x), nil
	case int64:
		return starlark.MakeInt64(x), nil
	case uint64:
		return starlark.MakeUint64(x), nil
	case float64:
		return starlark.Float(x), nil
	case *big.Int:
		if x == nil {
			return starlark.None, nil
		}
		return starlark.MakeBigInt(x), nil
	default:
		return nil, fmt.Errorf("unsupported SQL value type %T", v)
	}
}

// mapsToStarlark converts []map[string]any (from Query/HistoryQuery) into a
// *starlark.List of *starlark.Dict, one dict per row.
func mapsToStarlark(rows []map[string]any) (starlark.Value, error) {
	elems := make([]starlark.Value, 0, len(rows))
	for _, row := range rows {
		d := starlark.NewDict(len(row))
		for k, v := range row {
			sv, err := sqlScalarToStarlark(v)
			if err != nil {
				return nil, fmt.Errorf("convert column %q: %w", k, err)
			}
			if err := d.SetKey(starlark.String(k), sv); err != nil {
				return nil, err
			}
		}
		elems = append(elems, d)
	}
	return starlark.NewList(elems), nil
}

// infoResultToDict converts ipc.InfoResult to a *starlark.Dict with stable
// snake_case keys: tps, real_tps, paused, sim_time, last_autosave.
// last_autosave is a nested dict {path, name, modified_unix, time} or None.
func infoResultToDict(r ipc.InfoResult) *starlark.Dict {
	d := starlark.NewDict(5)
	_ = d.SetKey(starlark.String("tps"), starlark.Float(r.TPS))
	_ = d.SetKey(starlark.String("real_tps"), starlark.Float(r.RealTPS))
	_ = d.SetKey(starlark.String("paused"), starlark.Bool(r.Paused))
	_ = d.SetKey(starlark.String("sim_time"), starlark.Float(r.SimTime))
	if r.LastAutosave == nil {
		_ = d.SetKey(starlark.String("last_autosave"), starlark.None)
	} else {
		la := starlark.NewDict(4)
		_ = la.SetKey(starlark.String("path"), starlark.String(r.LastAutosave.Path))
		_ = la.SetKey(starlark.String("name"), starlark.String(r.LastAutosave.Name))
		_ = la.SetKey(starlark.String("modified_unix"), starlark.MakeInt64(r.LastAutosave.ModifiedUnix))
		_ = la.SetKey(starlark.String("time"), starlark.String(r.LastAutosave.Time))
		_ = d.SetKey(starlark.String("last_autosave"), la)
	}
	return d
}

// stopResultToDict converts ipc.StopResult to a *starlark.Dict.
// Keys: previous_time_scale.
func stopResultToDict(r ipc.StopResult) *starlark.Dict {
	d := starlark.NewDict(1)
	_ = d.SetKey(starlark.String("previous_time_scale"), starlark.Float(r.PreviousTimeScale))
	return d
}

// resumeResultToDict converts ipc.ResumeResult to a *starlark.Dict.
// Keys: time_scale.
func resumeResultToDict(r ipc.ResumeResult) *starlark.Dict {
	d := starlark.NewDict(1)
	_ = d.SetKey(starlark.String("time_scale"), starlark.Float(r.TimeScale))
	return d
}

// reloadResultToDict converts ipc.ReloadResult to a *starlark.Dict.
// Keys: save, ok.
func reloadResultToDict(r ipc.ReloadResult) *starlark.Dict {
	d := starlark.NewDict(2)
	_ = d.SetKey(starlark.String("save"), starlark.String(r.Save))
	_ = d.SetKey(starlark.String("ok"), starlark.Bool(r.Ok))
	return d
}

// nodeStateToDict converts NodeState to a *starlark.Dict.
// Keys: connected, info (nested dict or None).
func nodeStateToDict(ns NodeState) *starlark.Dict {
	d := starlark.NewDict(2)
	_ = d.SetKey(starlark.String("connected"), starlark.Bool(ns.Connected))
	if ns.Info == nil {
		_ = d.SetKey(starlark.String("info"), starlark.None)
	} else {
		_ = d.SetKey(starlark.String("info"), infoResultToDict(*ns.Info))
	}
	return d
}

// evictResultToDict converts EvictResult to a *starlark.Dict.
// Keys: candidates, demoted, bytes_deleted, refused_head, refused_shared.
func evictResultToDict(r EvictResult) *starlark.Dict {
	d := starlark.NewDict(5)
	_ = d.SetKey(starlark.String("candidates"), starlark.MakeInt(r.Candidates))
	_ = d.SetKey(starlark.String("demoted"), starlark.MakeInt(r.Demoted))
	_ = d.SetKey(starlark.String("bytes_deleted"), starlark.MakeInt(r.BytesDeleted))
	_ = d.SetKey(starlark.String("refused_head"), starlark.MakeInt(r.RefusedHead))
	_ = d.SetKey(starlark.String("refused_shared"), starlark.MakeInt(r.RefusedShared))
	return d
}
