package api

import (
	"encoding/json"
	"net/http"
)

// handleListNotebooks handles GET /api/workspaces/{id}/notebooks.
// Returns all notebooks as a JSON array sorted by name. An empty workspace
// returns [] (never null).
func (d *Daemon) handleListNotebooks(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	out, err := notebookList(d.root, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleGetNotebook handles GET /api/workspaces/{id}/notebooks/{name}.
// Returns 400 for an invalid name, 404 if the notebook does not exist.
func (d *Daemon) handleGetNotebook(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	name := r.PathValue("name")
	doc, err := notebookGet(d.root, id, name)
	if err != nil {
		if err == errNotebookNotFound {
			writeError(w, http.StatusNotFound, err)
		} else {
			writeError(w, http.StatusBadRequest, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

// handlePutNotebook handles PUT /api/workspaces/{id}/notebooks/{name}.
// Decodes {"cells":[...]} from the request body and upserts the notebook.
// Returns 400 for decode or sanitization errors, 200 with the written doc on success.
func (d *Daemon) handlePutNotebook(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	name := r.PathValue("name")

	var req struct {
		Cells []notebookCell `json:"cells"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	doc, err := notebookPut(d.root, id, name, req.Cells)
	if err != nil {
		// sanitizeNotebookName errors are path/input errors → 400.
		// Other errors (MkdirAll, write) → 500. We distinguish by checking
		// whether the error came before the filesystem op: sanitize is always
		// the first thing notebookPut calls, and its errors are plain strings.
		// For simplicity, treat sanitize errors (which contain "notebook name")
		// as 400, all others as 500.
		if isSanitizeError(err) {
			writeError(w, http.StatusBadRequest, err)
		} else {
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

// handleDeleteNotebook handles DELETE /api/workspaces/{id}/notebooks/{name}.
// Returns 400 for an invalid name, 404 if not found, 204 on success.
func (d *Daemon) handleDeleteNotebook(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	name := r.PathValue("name")
	if err := notebookDelete(d.root, id, name); err != nil {
		if err == errNotebookNotFound {
			writeError(w, http.StatusNotFound, err)
		} else if isSanitizeError(err) {
			writeError(w, http.StatusBadRequest, err)
		} else {
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// isSanitizeError returns true when err originated from sanitizeNotebookName.
// sanitizeNotebookName returns sentinel plain-string errors; since we own the
// error strings we match by checking whether they start with "notebook name".
func isSanitizeError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return len(s) >= 13 && s[:13] == "notebook name"
}
