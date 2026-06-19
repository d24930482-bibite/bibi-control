package api

import (
	"context"
	"net/http"
	"time"

	"github.com/asemones/bibicontrol/ipc"
	"github.com/asemones/bibicontrol/revisionstore"
)

// nodeInfoResponse is the JSON shape for one node returned by GET
// /api/workspaces/{id}/nodes/info.
//
// Liveness is derived from the active-set membership (ws.Node), not
// row.Status. The persisted Status is surfaced only as an informational
// field so the frontend can distinguish "running" rows whose process
// is gone (detached) from cleanly stopped rows.
type nodeInfoResponse struct {
	ID       string `json:"id"`
	WorldID  string `json:"world_id"`
	RunID    string `json:"run_id"`
	Liveness string `json:"liveness"` // "alive", "crashed", "detached"
	Status   string `json:"status"`   // persisted, informational only

	// Telemetry — present only when liveness=="alive" and INFO succeeded.
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
// liveness verdict derived from the live active set and a short non-blocking
// INFO round-trip — never from the persisted Status column.
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

		rt, live := ws.Node(row.NodeID)
		if !live {
			// No active handle — stale or cleanly-stopped row. The persisted
			// status is surfaced via Status; the liveness verdict is detached.
			obj.Liveness = "detached"
		} else {
			// Active handle exists: inspect the process state.
			st := rt.State()
			if st.Process.State == ipc.ProcessExited || st.Process.State == ipc.ProcessFailed {
				obj.Liveness = "crashed"
				obj.ExitCode = st.Process.ExitCode
			} else {
				// Process is running.
				obj.Liveness = "alive"

				// Telemetry only when a compat session is established.
				if rt.Connected() {
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
					// On INFO error (timeout, transient): keep liveness=="alive"
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
