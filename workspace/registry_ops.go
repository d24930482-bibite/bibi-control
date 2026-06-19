package workspace

import (
	"context"
	"fmt"
	"os"

	"github.com/asemones/bibicontrol/revisionstore"
)

// ListWorkspaces returns every workspace registered under root. It opens its own
// short-lived registry handle (open -> one query -> close) so it does not hold
// the registry's single connection across requests, and does not depend on any
// cached *Workspace existing. The result is unfiltered by owner (single-owner
// per root); the daemon maps the rows to its JSON shape.
func ListWorkspaces(ctx context.Context, root string) ([]revisionstore.Workspace, error) {
	if root == "" {
		return nil, fmt.Errorf("workspace: root is required")
	}
	registry, err := revisionstore.Open(registryPath(root))
	if err != nil {
		return nil, fmt.Errorf("workspace: open registry: %w", err)
	}
	defer registry.Close()

	return registry.ListWorkspaces(ctx)
}

// RenameWorkspace sets the display name of the workspace with id. It opens its
// own short-lived registry handle and propagates the registry error untouched so
// an unknown id (revisionstore.ErrNoRows) is still detectable via
// revisionstore.IsNotFound.
func RenameWorkspace(ctx context.Context, root, id, name string) error {
	if root == "" {
		return fmt.Errorf("workspace: root is required")
	}
	registry, err := revisionstore.Open(registryPath(root))
	if err != nil {
		return fmt.Errorf("workspace: open registry: %w", err)
	}
	defer registry.Close()

	return registry.RenameWorkspace(ctx, id, name)
}

// DeleteWorkspace removes the workspace with id from the registry (via the U1
// atomic cascade) and then removes its on-disk directory. The order is
// load-bearing: the registry rows are deleted FIRST so a failed registry delete
// leaves the directory intact (and thus recoverable); the bytes are only removed
// once the authoritative row is gone. The registry error is propagated untouched
// so an unknown id (revisionstore.ErrNoRows) is still detectable via
// revisionstore.IsNotFound, and os.RemoveAll of a missing dir is a no-op so this
// is safe even if the dir was never created.
//
// Callers that may hold an open handle to this workspace (the daemon's
// *Workspace cache) MUST evict and Close it before calling this — the
// per-workspace DuckDB file lives under the directory removed here, and removing
// it out from under an open writer races.
func DeleteWorkspace(ctx context.Context, root, id string) error {
	if root == "" {
		return fmt.Errorf("workspace: root is required")
	}
	registry, err := revisionstore.Open(registryPath(root))
	if err != nil {
		return fmt.Errorf("workspace: open registry: %w", err)
	}

	if err := registry.DeleteWorkspace(ctx, id); err != nil {
		_ = registry.Close()
		return err
	}
	if err := registry.Close(); err != nil {
		return fmt.Errorf("workspace: close registry: %w", err)
	}

	if err := os.RemoveAll(workspaceDir(root, id)); err != nil {
		return fmt.Errorf("workspace: remove workspace dir: %w", err)
	}
	return nil
}
