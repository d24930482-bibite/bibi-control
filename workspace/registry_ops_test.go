package workspace

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/asemones/bibicontrol/revisionstore"
)

func opsCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// createClosed creates a workspace under root and immediately closes its handle,
// returning its id. Closing releases the registry's single connection so the
// short-lived ops helpers can reopen it without contention.
func createClosed(t *testing.T, ctx context.Context, root, name string) string {
	t.Helper()
	ws, err := Create(ctx, root, "owner", name)
	if err != nil {
		t.Fatalf("Create(%q): %v", name, err)
	}
	id := ws.ID()
	if err := ws.Close(); err != nil {
		t.Fatalf("Close(%q): %v", name, err)
	}
	return id
}

func TestListWorkspaces(t *testing.T) {
	ctx := opsCtx(t)

	// Empty root: no error, no rows.
	empty := t.TempDir()
	rows, err := ListWorkspaces(ctx, empty)
	if err != nil {
		t.Fatalf("ListWorkspaces(empty): %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("ListWorkspaces(empty): got %d rows, want 0", len(rows))
	}

	root := t.TempDir()
	idA := createClosed(t, ctx, root, "alpha")
	idB := createClosed(t, ctx, root, "beta")

	rows, err = ListWorkspaces(ctx, root)
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("ListWorkspaces: got %d rows, want 2", len(rows))
	}
	byID := map[string]revisionstore.Workspace{}
	for _, ws := range rows {
		byID[ws.ID] = ws
	}
	if got, ok := byID[idA]; !ok || got.Name != "alpha" {
		t.Fatalf("ListWorkspaces: missing/wrong alpha row: %+v (ok=%v)", got, ok)
	}
	if got, ok := byID[idB]; !ok || got.Name != "beta" {
		t.Fatalf("ListWorkspaces: missing/wrong beta row: %+v (ok=%v)", got, ok)
	}
}

func TestRenameWorkspace(t *testing.T) {
	ctx := opsCtx(t)
	root := t.TempDir()
	id := createClosed(t, ctx, root, "before")

	if err := RenameWorkspace(ctx, root, id, "renamed"); err != nil {
		t.Fatalf("RenameWorkspace: %v", err)
	}

	rows, err := ListWorkspaces(ctx, root)
	if err != nil {
		t.Fatalf("ListWorkspaces after rename: %v", err)
	}
	var found bool
	for _, ws := range rows {
		if ws.ID == id {
			found = true
			if ws.Name != "renamed" {
				t.Fatalf("RenameWorkspace: name=%q, want %q", ws.Name, "renamed")
			}
		}
	}
	if !found {
		t.Fatalf("RenameWorkspace: id %q not found after rename", id)
	}

	// Renaming an unknown id is a not-found error.
	if err := RenameWorkspace(ctx, root, "does-not-exist", "x"); !revisionstore.IsNotFound(err) {
		t.Fatalf("RenameWorkspace(bogus): err=%v, want IsNotFound", err)
	}
}

func TestDeleteWorkspace(t *testing.T) {
	ctx := opsCtx(t)
	root := t.TempDir()
	id := createClosed(t, ctx, root, "doomed")

	dir := workspaceDir(root, id)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("workspace dir should exist before delete: %v", err)
	}

	if err := DeleteWorkspace(ctx, root, id); err != nil {
		t.Fatalf("DeleteWorkspace: %v", err)
	}

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("workspace dir should be gone after delete: stat err=%v", err)
	}

	rows, err := ListWorkspaces(ctx, root)
	if err != nil {
		t.Fatalf("ListWorkspaces after delete: %v", err)
	}
	for _, ws := range rows {
		if ws.ID == id {
			t.Fatalf("DeleteWorkspace: id %q still present after delete", id)
		}
	}

	// Deleting an unknown id is a not-found error.
	if err := DeleteWorkspace(ctx, root, "does-not-exist"); !revisionstore.IsNotFound(err) {
		t.Fatalf("DeleteWorkspace(bogus): err=%v, want IsNotFound", err)
	}
}
