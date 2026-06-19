// Package api implements the bibid HTTP daemon. The Daemon type caches one
// *workspace.Workspace per id (lazily opened, never opened twice) and exposes
// Handler() which registers the full API route table plus an embedded SPA at /.
package api

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"sync"

	"github.com/asemones/bibicontrol/revisionstore"
	"github.com/asemones/bibicontrol/script"
	"github.com/asemones/bibicontrol/workspace"
)

//go:embed web
var webFS embed.FS

// Daemon is the single HTTP server for the bibid process. It owns a cache of
// open workspaces: each workspace is opened at most once per Daemon lifetime
// (the "never open twice" invariant — two DuckDB writers to one file would
// corrupt the database and leak handles).
type Daemon struct {
	root  string
	owner string
	mu    sync.Mutex
	open  map[string]*workspace.Workspace
}

// New constructs a Daemon that will lazily open workspaces under root, scoped
// to the given owner. No handles are opened until the first request arrives.
func New(root, owner string) *Daemon {
	return &Daemon{root: root, owner: owner, open: make(map[string]*workspace.Workspace)}
}

// ws returns the cached *Workspace for id, opening it via workspace.Open if it
// has not been seen before. The entire check-open-store sequence is performed
// under one d.mu hold so two concurrent requests for the same fresh id cannot
// each call Open (opening twice allocates two DuckDB writers to one file and
// leaks registry/blob handles).
func (d *Daemon) ws(ctx context.Context, id string) (*workspace.Workspace, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if ws, ok := d.open[id]; ok {
		return ws, nil
	}
	ws, err := workspace.Open(ctx, d.root, id)
	if err != nil {
		return nil, err
	}
	d.open[id] = ws
	return ws, nil
}

// Close releases every cached workspace handle. Errors are joined and returned
// but all Close calls are attempted regardless of individual failures.
func (d *Daemon) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	var errs []error
	for id, ws := range d.open {
		if err := ws.Close(); err != nil {
			errs = append(errs, err)
		}
		delete(d.open, id)
	}
	return errors.Join(errs...)
}

// notImplemented is the shared stub handler for all routes not yet implemented.
// Later ticket executors swap exactly one registration line each to replace it
// with the real handler — keeping one shared value (not per-route closures)
// means grepping for "notImplemented" finds all outstanding stubs.
var notImplemented http.HandlerFunc = func(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, errors.New("not implemented"))
}

// Handler builds and returns an http.ServeMux with every route from the
// HTTP-surface table registered. The /api/... patterns are more specific than /,
// so the ServeMux routes API calls to their handlers and everything else falls
// through to the embedded SPA file server.
func (d *Daemon) Handler() http.Handler {
	mux := http.NewServeMux()

	// Real endpoints.
	mux.HandleFunc("GET /api/health", d.handleHealth)
	mux.HandleFunc("POST /api/workspaces/{id}/run", d.handleRun)

	// Stubbed endpoints — swapped one-by-one in later tickets.
	mux.HandleFunc("GET /api/workspaces", notImplemented)
	mux.HandleFunc("POST /api/workspaces", notImplemented)
	mux.HandleFunc("PATCH /api/workspaces/{id}", notImplemented)
	mux.HandleFunc("DELETE /api/workspaces/{id}", notImplemented)
	mux.HandleFunc("GET /api/workspaces/{id}/worlds", notImplemented)
	mux.HandleFunc("GET /api/workspaces/{id}/worlds/{wid}/history", notImplemented)
	mux.HandleFunc("GET /api/workspaces/{id}/nodes/info", d.handleNodesInfo)
	mux.HandleFunc("GET /api/workspaces/{id}/nodes/{nid}/logs", d.handleNodeLogs)
	mux.HandleFunc("POST /api/workspaces/{id}/upload", notImplemented)
	mux.HandleFunc("GET /api/workspaces/{id}/notebooks", notImplemented)
	mux.HandleFunc("GET /api/workspaces/{id}/notebooks/{name}", notImplemented)
	mux.HandleFunc("PUT /api/workspaces/{id}/notebooks/{name}", notImplemented)
	mux.HandleFunc("DELETE /api/workspaces/{id}/notebooks/{name}", notImplemented)

	// Embedded SPA at /. /api/... patterns are more specific and win.
	sub, _ := fs.Sub(webFS, "web")
	mux.Handle("/", http.FileServerFS(sub))

	return mux
}

// handleHealth handles GET /api/health.
func (d *Daemon) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleRun handles POST /api/workspaces/{id}/run.
//
// Contract: a program with diagnostics is NOT an HTTP error. RunAutomation
// returns (Result, nil) for a clean run and (Result, *script.RunError) when the
// program itself failed, but diagnostics live on the Result either way. This
// handler always writes the Result as JSON with HTTP 200; only infrastructure
// failures (body decode, workspace resolve) produce non-200 responses.
//
// Workspace resolve: if workspace.Open wraps a revisionstore not-found error
// (checked via revisionstore.IsNotFound) the response is 404; all other resolve
// errors are 500.
func (d *Daemon) handleRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Program string `json:"program"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	id := r.PathValue("id")
	ws, err := d.ws(r.Context(), id)
	if err != nil {
		if revisionstore.IsNotFound(err) {
			writeError(w, http.StatusNotFound, err)
		} else {
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}

	res, _ := workspace.RunAutomation(r.Context(), ws, []byte(req.Program), script.Options{Filename: "notebook"})
	writeJSON(w, http.StatusOK, res)
}

// writeJSON sets Content-Type to application/json, writes the status code, and
// encodes v as JSON. Encoding errors are silently swallowed (the response header
// is already sent).
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON error response with the given status and message.
func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
