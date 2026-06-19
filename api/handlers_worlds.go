package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/asemones/bibicontrol/revisionstore"
)

// worldDTO is the JSON shape for a single world in GET /api/workspaces/{id}/worlds.
type worldDTO struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	HeadRevision *int64   `json:"head_revision"`
	SimTime      *float64 `json:"sim_time"`
	LiveNode     *string  `json:"live_node"`
}

// revisionDTO is the JSON shape for a single revision in
// GET /api/workspaces/{id}/worlds/{wid}/history.
type revisionDTO struct {
	ID         int64  `json:"id"`
	ParentID   *int64 `json:"parent_id"`
	CreatedAt  string `json:"created_at"`
	SourcePath string `json:"source_path"`
	IsHead     bool   `json:"is_head"`
}

// handleWorlds handles GET /api/workspaces/{id}/worlds.
// It returns every world in the workspace with its head-revision id, sim_time,
// and a live-node indicator derived from the in-memory active set (never from
// the persisted status column).
func (d *Daemon) handleWorlds(w http.ResponseWriter, r *http.Request) {
	ws, err := d.ws(r.Context(), r.PathValue("id"))
	if err != nil {
		if revisionstore.IsNotFound(err) {
			writeError(w, http.StatusNotFound, err)
		} else {
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}

	worlds, err := ws.ListWorlds(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	// Build live-node map: worldID -> logical nodeID, keyed on the in-memory
	// active set (never the persisted status row). This mirrors
	// activeNodeForWorldLocked in workspace/node.go:279.
	liveWorld := make(map[string]string)
	nodes, err := ws.PersistedNodes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	// Map logical node_id -> world_id from persisted rows.
	persistedWorld := make(map[string]string, len(nodes))
	for _, n := range nodes {
		persistedWorld[n.NodeID] = n.WorldID
	}
	// Intersect with the in-memory active set to determine liveness.
	for _, rt := range ws.Nodes() {
		nid := rt.NodeID()
		if wid, ok := persistedWorld[nid]; ok {
			liveWorld[wid] = nid
		}
	}

	out := make([]worldDTO, 0, len(worlds))
	for _, world := range worlds {
		dto := worldDTO{
			ID:           world.ID,
			Name:         world.Name,
			HeadRevision: world.HeadRevisionID,
			SimTime:      world.SimTime,
		}
		if nodeID, ok := liveWorld[world.ID]; ok {
			dto.LiveNode = &nodeID
		}
		out = append(out, dto)
	}

	writeJSON(w, http.StatusOK, out)
}

// handleWorldHistory handles GET /api/workspaces/{id}/worlds/{wid}/history.
// It returns the ordered revision lineage for one world (oldest→newest),
// marking which revision is the world's head.
func (d *Daemon) handleWorldHistory(w http.ResponseWriter, r *http.Request) {
	ws, err := d.ws(r.Context(), r.PathValue("id"))
	if err != nil {
		if revisionstore.IsNotFound(err) {
			writeError(w, http.StatusNotFound, err)
		} else {
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}

	wid := r.PathValue("wid")

	// Find the world to get its HeadRevisionID. ListWorlds is one bounded query.
	worlds, err := ws.ListWorlds(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	var headID *int64
	found := false
	for _, world := range worlds {
		if world.ID == wid {
			headID = world.HeadRevisionID
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, fmt.Errorf("world %q not found", wid))
		return
	}

	revs, err := ws.RevisionsForWorld(r.Context(), wid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	out := make([]revisionDTO, 0, len(revs))
	for _, rev := range revs {
		out = append(out, revisionDTO{
			ID:         rev.ID,
			ParentID:   rev.ParentID,
			CreatedAt:  rev.CreatedAt.Format(time.RFC3339),
			SourcePath: rev.SourcePath,
			IsHead:     headID != nil && rev.ID == *headID,
		})
	}

	writeJSON(w, http.StatusOK, out)
}
