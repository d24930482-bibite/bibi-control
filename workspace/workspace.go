// Package workspace ties the revision store, content-addressed blobstore, and
// per-workspace DuckDB analytics mirror together for a single owner-scoped
// workspace. B2 ships only the lifecycle skeleton (Create/Open/Close); the
// world, node, and query operations are layered onto this struct by later
// tickets (C1/C2/C3/C4/D*/E*).
package workspace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/asemones/bibicontrol/blobstore"
	"github.com/asemones/bibicontrol/duckdb"
	"github.com/asemones/bibicontrol/noderuntime"
	"github.com/asemones/bibicontrol/revisionstore"
	"github.com/asemones/bibicontrol/script/thebibites"
)

// Workspace owns the three stores backing one owner-scoped workspace: the
// shared SQLite revision registry (operational truth for all workspaces under a
// root), the shared content-addressed blobstore (dedups bytes across
// workspaces), and the per-workspace DuckDB analytics file (per-file isolation).
type Workspace struct {
	root  string
	id    string
	owner string
	name  string

	registry *revisionstore.Store
	// blobs is the concrete filesystem store. It is held concretely (not via
	// the blobstore.Store interface) so Close can reach FSStore.Close, which
	// the interface does not expose.
	blobsStore *blobstore.FSStore
	duckDB     *sql.DB // per-workspace DuckDB handle (analytics)

	// mu serializes mutating operations against the single DuckDB writer per
	// workspace. B2's Create/Open/Close do not lock it (they are not
	// concurrent mutators); later tickets' AddWorld/CommitWorld/IngestAutosave/
	// ReloadNode (C3/D3) acquire it.
	mu sync.Mutex
	// worlds is the in-memory working set (worldID -> loaded save). Allocated
	// empty here; populated by C1/C2.
	worlds map[string]*thebibites.LoadedSave
	// nodes is the active-node set (nodeID -> runtime). Allocated empty here;
	// populated by D1.
	nodes map[string]*noderuntime.Runtime

	// catalogFP / catalogBuilt cache the last successfully rebuilt mirror_saves
	// catalog state so refreshMirrorCatalog can short-circuit the whole rebuild
	// when the registry has not changed (C4b: kills the rebuild-on-every-query
	// N+1). They are read and written ONLY inside refreshMirrorCatalog, which is
	// always called holding w.mu, so they need no extra synchronization.
	//
	// catalogBuilt distinguishes "never built" (zero fingerprint of a brand-new
	// empty workspace) from "built and the fingerprint legitimately is the zero
	// value", forcing the first build. catalogFP is written ONLY after the
	// rebuild COMMIT so a failed/rolled-back rebuild never poisons the cache.
	catalogFP    revisionstore.CatalogFingerprint
	catalogBuilt bool
	// rebuildCount is a test-only seam: it is incremented exactly once per
	// ACTUAL rebuild (after a successful COMMIT), never on a cache-hit early
	// return. Tests assert N repeat queries trigger exactly one rebuild. It has
	// no production behavior.
	rebuildCount int64
	// scenesReadCount / insertExecCount are test-only perf-shape seams: they
	// count, per rebuild, the set-based scenes reads (must be exactly one) and
	// the chunked INSERT ExecContext calls (must be ceil(R/catalogInsertChunk),
	// not R). They prove the per-revision DuckDB N+1 is gone, not relocated.
	scenesReadCount int64
	insertExecCount int64
}

func registryPath(root string) string     { return filepath.Join(root, "metadata.sqlite") }
func blobsRoot(root string) string        { return filepath.Join(root, "blobs") }
func workspaceDir(root, id string) string { return filepath.Join(root, "workspaces", id) }
func duckPath(root, id string) string {
	return filepath.Join(workspaceDir(root, id), "analytics.duckdb")
}

// ID returns the workspace registry id.
func (w *Workspace) ID() string {
	if w == nil {
		return ""
	}
	return w.id
}

// Owner returns the owner namespace this workspace is scoped to.
func (w *Workspace) Owner() string {
	if w == nil {
		return ""
	}
	return w.owner
}

// Name returns the workspace display name.
func (w *Workspace) Name() string {
	if w == nil {
		return ""
	}
	return w.name
}

// store returns the shared revision registry handle. Unexported: only
// in-package callers (the later W/N/C ticket methods) reach through it.
func (w *Workspace) store() *revisionstore.Store {
	if w == nil {
		return nil
	}
	return w.registry
}

// blobs returns the shared content-addressed blobstore as the Store interface.
func (w *Workspace) blobs() blobstore.Store {
	if w == nil {
		return nil
	}
	return w.blobsStore
}

// duck returns the per-workspace DuckDB handle.
func (w *Workspace) duck() *sql.DB {
	if w == nil {
		return nil
	}
	return w.duckDB
}

// Create establishes the on-disk layout under root, allocates a new workspaces
// registry row, opens and migrates a fresh per-workspace DuckDB file, and opens
// the shared blobstore. Partial failures close any handle opened so far so a
// failed Create leaks nothing; it does not delete files or rows.
func Create(ctx context.Context, root, owner, name string) (*Workspace, error) {
	if root == "" {
		return nil, fmt.Errorf("workspace: root is required")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("workspace: create root: %w", err)
	}

	registry, err := revisionstore.Open(registryPath(root))
	if err != nil {
		return nil, fmt.Errorf("workspace: open registry: %w", err)
	}

	blobs, err := blobstore.NewFSStore(blobsRoot(root))
	if err != nil {
		_ = registry.Close()
		return nil, fmt.Errorf("workspace: open blobstore: %w", err)
	}

	ws, err := registry.CreateWorkspace(ctx, revisionstore.WorkspaceInput{Owner: owner, Name: name})
	if err != nil {
		_ = blobs.Close()
		_ = registry.Close()
		return nil, fmt.Errorf("workspace: create registry row: %w", err)
	}

	if err := os.MkdirAll(workspaceDir(root, ws.ID), 0o755); err != nil {
		_ = blobs.Close()
		_ = registry.Close()
		return nil, fmt.Errorf("workspace: create workspace dir: %w", err)
	}

	duck, err := openDuck(ctx, root, ws.ID)
	if err != nil {
		_ = blobs.Close()
		_ = registry.Close()
		return nil, err
	}

	return &Workspace{
		root:       root,
		id:         ws.ID,
		owner:      ws.Owner,
		name:       ws.Name,
		registry:   registry,
		blobsStore: blobs,
		duckDB:     duck,
		worlds:     make(map[string]*thebibites.LoadedSave),
		nodes:      make(map[string]*noderuntime.Runtime),
	}, nil
}

// Open re-attaches to an existing workspace: it looks up the registry row by id
// (rather than creating one) and opens the existing per-workspace DuckDB file,
// still applying the idempotent migrations. Partial failures close any handle
// opened so far.
func Open(ctx context.Context, root, id string) (*Workspace, error) {
	if root == "" {
		return nil, fmt.Errorf("workspace: root is required")
	}
	if id == "" {
		return nil, fmt.Errorf("workspace: id is required")
	}

	registry, err := revisionstore.Open(registryPath(root))
	if err != nil {
		return nil, fmt.Errorf("workspace: open registry: %w", err)
	}

	ws, err := registry.GetWorkspace(ctx, id)
	if err != nil {
		_ = registry.Close()
		if revisionstore.IsNotFound(err) {
			return nil, fmt.Errorf("workspace: workspace %q not found: %w", id, err)
		}
		return nil, fmt.Errorf("workspace: get workspace %q: %w", id, err)
	}

	blobs, err := blobstore.NewFSStore(blobsRoot(root))
	if err != nil {
		_ = registry.Close()
		return nil, fmt.Errorf("workspace: open blobstore: %w", err)
	}

	duck, err := openDuck(ctx, root, ws.ID)
	if err != nil {
		_ = blobs.Close()
		_ = registry.Close()
		return nil, err
	}

	return &Workspace{
		root:       root,
		id:         ws.ID,
		owner:      ws.Owner,
		name:       ws.Name,
		registry:   registry,
		blobsStore: blobs,
		duckDB:     duck,
		worlds:     make(map[string]*thebibites.LoadedSave),
		nodes:      make(map[string]*noderuntime.Runtime),
	}, nil
}

// openDuck opens the per-workspace DuckDB file and applies the idempotent
// migrations. On Create the file is fresh; on Open it already exists; in both
// cases the CREATE ... IF NOT EXISTS migrations are correct and cheap.
func openDuck(ctx context.Context, root, id string) (*sql.DB, error) {
	duck, err := duckdb.Open(duckPath(root, id))
	if err != nil {
		return nil, fmt.Errorf("workspace: open duckdb: %w", err)
	}
	if err := duckdb.ApplyMigrations(ctx, duck); err != nil {
		_ = duck.Close()
		return nil, fmt.Errorf("workspace: apply duckdb migrations: %w", err)
	}
	return duck, nil
}

// Close releases every handle the workspace owns, joining any errors. It is
// safe on a nil or partially constructed workspace, and idempotent (handles are
// niled after closing so a second Close is a no-op).
//
// D1 drain: active node runtimes are killed and closed before the DuckDB /
// registry handles are torn down. Status rows are not updated here (the
// registry handle may already be closing); best-effort kill is the contract.
func (w *Workspace) Close() error {
	if w == nil {
		return nil
	}
	var errs []error
	// D1: drain active nodes before closing the DB handles.
	for nodeID, rt := range w.nodes {
		errs = append(errs, rt.Kill())
		errs = append(errs, rt.Close())
		delete(w.nodes, nodeID)
	}
	if w.duckDB != nil {
		errs = append(errs, w.duckDB.Close())
		w.duckDB = nil
	}
	if w.registry != nil {
		errs = append(errs, w.registry.Close())
		w.registry = nil
	}
	if w.blobsStore != nil {
		errs = append(errs, w.blobsStore.Close())
		w.blobsStore = nil
	}
	return errors.Join(errs...)
}
