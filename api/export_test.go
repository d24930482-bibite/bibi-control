package api

import "github.com/asemones/bibicontrol/workspace"

// SeedWorkspace injects ws into the daemon's open-workspace cache under id.
// It is used exclusively by api_test to make the daemon return the exact
// *workspace.Workspace handle that owns a live node's active set, without
// opening the workspace a second time (the "never open twice" invariant).
func (d *Daemon) SeedWorkspace(id string, ws *workspace.Workspace) {
	d.mu.Lock()
	d.open[id] = ws
	d.mu.Unlock()
}
