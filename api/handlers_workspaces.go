package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/asemones/bibicontrol/revisionstore"
	"github.com/asemones/bibicontrol/workspace"
)

// workspaceJSON is the wire shape for a workspace in the collection routes.
type workspaceJSON struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Owner string `json:"owner"`
}

// handleListWorkspaces handles GET /api/workspaces. It reads every workspace
// row fresh from the registry (not the daemon's in-memory cache, whose names may
// be stale after a rename) and returns them as a JSON array. An empty root is
// rendered as [] rather than null.
func (d *Daemon) handleListWorkspaces(w http.ResponseWriter, r *http.Request) {
	rows, err := workspace.ListWorkspaces(r.Context(), d.root)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	out := make([]workspaceJSON, 0, len(rows))
	for _, row := range rows {
		out = append(out, workspaceJSON{ID: row.ID, Name: row.Name, Owner: row.Owner})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCreateWorkspace handles POST /api/workspaces. It creates a workspace
// scoped to the daemon's owner, caches the opened handle (Create already opened
// every handle, so caching it preserves the "never open twice" invariant), and
// returns the new workspace with 201.
func (d *Daemon) handleCreateWorkspace(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, errors.New("name is required"))
		return
	}

	ws, err := workspace.Create(r.Context(), d.root, d.owner, req.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	d.mu.Lock()
	d.open[ws.ID()] = ws
	d.mu.Unlock()

	writeJSON(w, http.StatusCreated, workspaceJSON{ID: ws.ID(), Name: ws.Name(), Owner: ws.Owner()})
}

// handleRenameWorkspace handles PATCH /api/workspaces/{id}. It renames the
// registry row; a cached *Workspace's in-memory name is left stale on purpose
// (no endpoint reads it authoritatively — list reads fresh from the registry).
func (d *Daemon) handleRenameWorkspace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, errors.New("name is required"))
		return
	}

	if err := workspace.RenameWorkspace(r.Context(), d.root, id, req.Name); err != nil {
		if revisionstore.IsNotFound(err) {
			writeError(w, http.StatusNotFound, err)
		} else {
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, workspaceJSON{ID: id, Name: req.Name, Owner: d.owner})
}

// handleDeleteWorkspace handles DELETE /api/workspaces/{id}.
//
// Ordering invariant (the core risk of this route): any cached handle for id is
// evicted from d.open and Close()d BEFORE the on-disk directory is removed,
// because the per-workspace DuckDB file lives under that directory and
// os.RemoveAll must not race an open writer. Close runs OUTSIDE d.mu (it can
// block draining nodes; do not hold the daemon lock across it). The registry
// rows are then deleted before the directory bytes (workspace.DeleteWorkspace),
// so a registry failure is recoverable.
func (d *Daemon) handleDeleteWorkspace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Evict the cached handle under the lock, then close it outside the lock.
	d.mu.Lock()
	ws, cached := d.open[id]
	if cached {
		delete(d.open, id)
	}
	d.mu.Unlock()
	if cached {
		if err := ws.Close(); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}

	if err := workspace.DeleteWorkspace(r.Context(), d.root, id); err != nil {
		if revisionstore.IsNotFound(err) {
			writeError(w, http.StatusNotFound, err)
		} else {
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
