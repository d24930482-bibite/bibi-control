package workspace

import (
	"context"
	"os"
	"testing"

	"github.com/asemones/bibicontrol/revisionstore"
)

func TestCreateWorkspaceEstablishesLayout(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	ws, err := Create(ctx, root, "alice", "demo")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer ws.Close()

	if ws.ID() == "" {
		t.Fatalf("ID() is empty")
	}
	if got, want := ws.Owner(), "alice"; got != want {
		t.Fatalf("Owner() = %q, want %q", got, want)
	}
	if got, want := ws.Name(), "demo"; got != want {
		t.Fatalf("Name() = %q, want %q", got, want)
	}

	// On-disk layout: shared registry + shared blobs dir + per-workspace duck file.
	if _, err := os.Stat(registryPath(root)); err != nil {
		t.Fatalf("stat registry: %v", err)
	}
	if _, err := os.Stat(blobsRoot(root)); err != nil {
		t.Fatalf("stat blobs root: %v", err)
	}
	if _, err := os.Stat(duckPath(root, ws.ID())); err != nil {
		t.Fatalf("stat duck file: %v", err)
	}

	// The DuckDB handle is usable.
	var one int
	if err := ws.duck().QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("SELECT 1: %v", err)
	}
	if one != 1 {
		t.Fatalf("SELECT 1 = %d, want 1", one)
	}

	// Migrations applied: schema tables exist, and the bibites table is empty.
	var tableCount int
	if err := ws.duck().QueryRowContext(ctx,
		"SELECT count(*) FROM information_schema.tables").Scan(&tableCount); err != nil {
		t.Fatalf("count tables: %v", err)
	}
	if tableCount == 0 {
		t.Fatalf("no tables after migrations: ApplyMigrations did not run")
	}
	var bibites int
	if err := ws.duck().QueryRowContext(ctx, "SELECT count(*) FROM bibites").Scan(&bibites); err != nil {
		t.Fatalf("count bibites: %v", err)
	}
	if bibites != 0 {
		t.Fatalf("bibites count = %d, want 0", bibites)
	}

	// Working-set/active-node maps allocated empty.
	if ws.worlds == nil {
		t.Fatalf("worlds map is nil")
	}
	if len(ws.worlds) != 0 {
		t.Fatalf("worlds map non-empty: %d", len(ws.worlds))
	}
	if ws.nodes == nil {
		t.Fatalf("nodes map is nil")
	}
	if len(ws.nodes) != 0 {
		t.Fatalf("nodes map non-empty: %d", len(ws.nodes))
	}
}

func TestCreateThenOpenRoundTrips(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	created, err := Create(ctx, root, "bob", "round-trip")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id := created.ID()
	if err := created.Close(); err != nil {
		t.Fatalf("Close created: %v", err)
	}

	reopened, err := Open(ctx, root, id)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer reopened.Close()

	if got := reopened.ID(); got != id {
		t.Fatalf("reopened ID() = %q, want %q", got, id)
	}
	if got, want := reopened.Owner(), "bob"; got != want {
		t.Fatalf("reopened Owner() = %q, want %q", got, want)
	}
	if got, want := reopened.Name(), "round-trip"; got != want {
		t.Fatalf("reopened Name() = %q, want %q", got, want)
	}
}

func TestOpenUnknownWorkspace(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	// Create one so the registry exists, then open a bogus id.
	created, err := Create(ctx, root, "carol", "exists")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := created.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	ws, err := Open(ctx, root, "does-not-exist")
	if err == nil {
		t.Fatalf("Open of unknown workspace returned nil error")
	}
	if ws != nil {
		t.Fatalf("Open of unknown workspace returned non-nil workspace")
	}
	if !revisionstore.IsNotFound(err) {
		t.Fatalf("error %v does not wrap IsNotFound", err)
	}
}

func TestSharedRegistryAndBlobsAcrossWorkspaces(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	ws1, err := Create(ctx, root, "owner", "one")
	if err != nil {
		t.Fatalf("Create ws1: %v", err)
	}
	defer ws1.Close()
	ws2, err := Create(ctx, root, "owner", "two")
	if err != nil {
		t.Fatalf("Create ws2: %v", err)
	}
	defer ws2.Close()

	if ws1.ID() == ws2.ID() {
		t.Fatalf("both workspaces got id %q", ws1.ID())
	}

	// Distinct per-workspace DuckDB files.
	if duckPath(root, ws1.ID()) == duckPath(root, ws2.ID()) {
		t.Fatalf("duck paths collide: %s", duckPath(root, ws1.ID()))
	}
	if _, err := os.Stat(duckPath(root, ws1.ID())); err != nil {
		t.Fatalf("stat ws1 duck: %v", err)
	}
	if _, err := os.Stat(duckPath(root, ws2.ID())); err != nil {
		t.Fatalf("stat ws2 duck: %v", err)
	}

	// Single shared registry + blobs dir.
	if _, err := os.Stat(registryPath(root)); err != nil {
		t.Fatalf("stat shared registry: %v", err)
	}
	if _, err := os.Stat(blobsRoot(root)); err != nil {
		t.Fatalf("stat shared blobs: %v", err)
	}

	// Both rows landed in the one shared registry.
	reg, err := revisionstore.Open(registryPath(root))
	if err != nil {
		t.Fatalf("reopen registry: %v", err)
	}
	defer reg.Close()
	rows, err := reg.ListWorkspaces(ctx)
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("ListWorkspaces returned %d rows, want 2", len(rows))
	}
	got := map[string]bool{}
	for _, r := range rows {
		got[r.ID] = true
	}
	if !got[ws1.ID()] || !got[ws2.ID()] {
		t.Fatalf("registry missing one of the workspace ids: %v", got)
	}
}

func TestCloseIsIdempotentAndNilSafe(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	ws, err := Create(ctx, root, "dave", "close-me")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := ws.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	// Second Close must not panic and should return nil (handles niled).
	if err := ws.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	if err := (*Workspace)(nil).Close(); err != nil {
		t.Fatalf("nil Close: %v", err)
	}
}
