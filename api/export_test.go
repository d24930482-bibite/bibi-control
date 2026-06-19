// export_test.go exposes internal symbols for use by the api_test package.
// This file is only compiled during testing.
package api

import "github.com/asemones/bibicontrol/workspace"

// NotebookCell is the exported alias for notebookCell used in tests.
type NotebookCell = notebookCell

// NotebookList wraps the unexported notebookList for package-external tests.
func NotebookList(root, id string) ([]notebookMeta, error) {
	return notebookList(root, id)
}

// NotebookGet wraps the unexported notebookGet for package-external tests.
func NotebookGet(root, id, name string) (*notebookDoc, error) {
	return notebookGet(root, id, name)
}

// NotebookPut wraps the unexported notebookPut for package-external tests.
func NotebookPut(root, id, name string, cells []notebookCell) (*notebookDoc, error) {
	return notebookPut(root, id, name, cells)
}

// NotebookDelete wraps the unexported notebookDelete for package-external tests.
func NotebookDelete(root, id, name string) error {
	return notebookDelete(root, id, name)
}

// SanitizeNotebookName exposes sanitizeNotebookName for package-external tests.
// It returns a non-nil error if the name is invalid.
func SanitizeNotebookName(name string) error {
	_, err := sanitizeNotebookName(name)
	return err
}

// IsNotebookNotFound returns true when err is the not-found sentinel from the
// notebook store.
func IsNotebookNotFound(err error) bool {
	return err == errNotebookNotFound
}

// SeedWorkspace injects ws into the daemon's open-workspace cache under id.
// It is used exclusively by api_test to make the daemon return the exact
// *workspace.Workspace handle that owns a live node's active set, without
// opening the workspace a second time (the "never open twice" invariant).
func (d *Daemon) SeedWorkspace(id string, ws *workspace.Workspace) {
	d.mu.Lock()
	d.open[id] = ws
	d.mu.Unlock()
}
