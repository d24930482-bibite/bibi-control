package api

import (
	"context"
	"net/http"
	"time"

	"github.com/asemones/bibicontrol/ipc"
	"github.com/asemones/bibicontrol/revisionstore"
	"github.com/asemones/bibicontrol/workspace"
)

// nodeInfoResponse is the JSON shape for one node returned by GET
// /api/workspaces/{id}/nodes/info.
//
// Liveness is derived from workspace.NodeLiveness (the shared helper used by
// both this handler and the Starlark node.status attribute) so the two surfaces
// always agree. Possible values: "running", "crashed", "stopped", "exited",
// "detached". The persisted Status is surfaced as an informational field.
type nodeInfoResponse struct {
	ID       string `json:"id"`
	WorldID  string `json:"world_id"`
	RunID    string `json:"run_id"`
	Liveness string `json:"liveness"` // "running", "crashed", "stopped", "exited", "detached"
	Status   string `json:"status"`   // persisted, informational only

	// Telemetry — present only when liveness=="running" and INFO succeeded.
	TPS          *float64          `json:"tps,omitempty"`
	RealTPS      *float64          `json:"real_tps,omitempty"`
	Paused       *bool             `json:"paused,omitempty"`
	SimTime      *float64          `json:"sim_time,omitempty"`
	LastAutosave *ipc.AutosaveInfo `json:"last_autosave,omitempty"`

	// ExitCode — present only when liveness=="crashed" and ExitCode is known.
	ExitCode *int `json:"exit_code,omitempty"`
}

// logsResponse is the JSON shape for GET /api/workspaces/{id}/nodes/{nid}/logs.
type logsResponse struct {
	Lines []logLineJSON `json:"lines"`
}

// logLineJSON is one captured line from the node's output ring buffer.
type logLineJSON struct {
	Time  string `json:"time"`  // RFC3339Nano
	Level string `json:"level"` // "info" or "error"
	Text  string `json:"text"`
}

// handleNodesInfo handles GET /api/workspaces/{id}/nodes/info.
//
// It returns a JSON array of every persisted node row, each annotated with a
// liveness verdict from workspace.NodeLiveness (the shared helper also used by
// the Starlark node.status attribute) and a short non-blocking INFO round-trip
// for active nodes. The liveness verdict is always derived from active-set
// membership first; the persisted Status column is consulted only for inactive
// nodes (to distinguish "stopped" from "exited" from "detached").
func (d *Daemon) handleNodesInfo(w http.ResponseWriter, r *http.Request) {
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

	rows, err := ws.PersistedNodes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	out := make([]nodeInfoResponse, 0, len(rows))
	for _, row := range rows {
		obj := nodeInfoResponse{
			ID:      row.NodeID,
			WorldID: row.WorldID,
			RunID:   row.RunID,
			Status:  row.Status,
		}

		// Liveness verdict from the shared helper — same logic as Starlark
		// node.status so the two surfaces always agree.
		obj.Liveness = ws.NodeLiveness(r.Context(), row.NodeID)

		// Telemetry and exit-code are only meaningful for active nodes.
		// Re-check active membership after NodeLiveness to avoid a second
		// lock round-trip on the inactive path.
		if obj.Liveness == "running" || obj.Liveness == "crashed" {
			rt, live := ws.Node(row.NodeID)
			if live {
				st := rt.State()
				if st.Process.State == ipc.ProcessExited || st.Process.State == ipc.ProcessFailed {
					obj.ExitCode = st.Process.ExitCode
				} else if rt.Connected() {
					// Telemetry only when a compat session is established and
					// the process is still running (liveness == "running").
					infoCtx, cancel := context.WithTimeout(r.Context(), 750*time.Millisecond)
					info, infoErr := ws.NodeInfo(infoCtx, row.NodeID)
					cancel()
					if infoErr == nil {
						tps := info.TPS
						realTPS := info.RealTPS
						paused := info.Paused
						simTime := info.SimTime
						obj.TPS = &tps
						obj.RealTPS = &realTPS
						obj.Paused = &paused
						obj.SimTime = &simTime
						obj.LastAutosave = info.LastAutosave
					}
					// On INFO error (timeout, transient): keep liveness=="running"
					// but omit telemetry — do not downgrade to crashed.
				}
			}
		}

		out = append(out, obj)
	}

	writeJSON(w, http.StatusOK, out)
}

// handleNodeLogs handles GET /api/workspaces/{id}/nodes/{nid}/logs.
//
// It returns the per-node ring buffer snapshot from Workspace.NodeLogs.
// The ?follow query parameter is accepted but no-op (prototype returns a
// single buffer snapshot; streaming will be added when required).
func (d *Daemon) handleNodeLogs(w http.ResponseWriter, r *http.Request) {
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

	nid := r.PathValue("nid")
	// ?follow is accepted but no-op (prototype = snapshot only).
	_ = r.URL.Query().Has("follow")

	lines, err := ws.NodeLogs(nid)
	if err != nil {
		// No log buffer exists for nid (never started, pruned, or unknown).
		// Return 404 so the frontend can distinguish "no logs" from a node with
		// zero captured lines (which returns 200 {"lines":[]}).
		writeError(w, http.StatusNotFound, err)
		return
	}

	// Shape workspace.LogLine into the wire type. Always emit a non-nil lines
	// array so the frontend can iterate unconditionally.
	shaped := make([]logLineJSON, 0, len(lines))
	for _, ln := range lines {
		shaped = append(shaped, logLineJSON{
			Time:  ln.Time.Format(time.RFC3339Nano),
			Level: ln.Level,
			Text:  ln.Text,
		})
	}

	writeJSON(w, http.StatusOK, logsResponse{Lines: shaped})
}

// handleDeleteNode handles DELETE /api/workspaces/{id}/nodes/{nid}.
//
// It removes the persisted node row from the workspace registry. This is a
// row-only deletion — it does NOT stop a live process and is intended for
// detached / stale rows. On success it returns HTTP 204. On an unknown nid it
// returns 404 via revisionstore.IsNotFound; all other errors return 500.
func (d *Daemon) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	nid := r.PathValue("nid")

	if err := workspace.DeleteNode(r.Context(), d.root, nid); err != nil {
		if revisionstore.IsNotFound(err) {
			writeError(w, http.StatusNotFound, err)
		} else {
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}
	_ = id // workspace id is used for routing; the node id is globally unique in the registry
	w.WriteHeader(http.StatusNoContent)
}
