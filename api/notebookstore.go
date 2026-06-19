package api

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// notebookCell is one cell in a notebook: a type ("code" or "text") and its source.
type notebookCell struct {
	Type   string `json:"type"`
	Source string `json:"source"`
}

// notebookDoc is the persisted JSON shape and the GET-one response body.
type notebookDoc struct {
	Name      string         `json:"name"`
	Cells     []notebookCell `json:"cells"`
	UpdatedAt string         `json:"updated_at"`
}

// notebookMeta is the list-row shape returned by GET /api/workspaces/{id}/notebooks.
type notebookMeta struct {
	Name      string `json:"name"`
	UpdatedAt string `json:"updated_at"`
}

// errNotebookNotFound is the sentinel returned by notebookGet/notebookDelete when
// the notebook file does not exist. Handlers map it to HTTP 404.
var errNotebookNotFound = errors.New("notebook not found")

// notebooksDir returns the absolute path to the notebooks subdirectory for a
// workspace. Mirrors the workspaceDir join in workspace/workspace.go without
// importing the unexported helper.
func notebooksDir(root, id string) string {
	return filepath.Join(root, "workspaces", id, "notebooks")
}

// sanitizeNotebookName validates and returns the notebook name, or an error if
// the name could escape the notebooks directory via traversal, separators,
// absolute paths, or dotfiles. Call before any filesystem operation.
func sanitizeNotebookName(name string) (string, error) {
	if name == "" {
		return "", errors.New("notebook name must not be empty")
	}
	if strings.HasPrefix(name, ".") {
		return "", errors.New("notebook name must not start with '.'")
	}
	if strings.ContainsRune(name, '/') || strings.ContainsRune(name, '\\') || strings.ContainsRune(name, os.PathSeparator) {
		return "", errors.New("notebook name must not contain path separators")
	}
	if name == "." || name == ".." {
		return "", errors.New("notebook name must not be '.' or '..'")
	}
	if filepath.IsAbs(name) {
		return "", errors.New("notebook name must not be an absolute path")
	}
	// Belt-and-suspenders: Base must equal the raw name so no embedded separator
	// (e.g. on Windows) slipped through the checks above.
	if filepath.Base(name) != name {
		return "", errors.New("notebook name must be a plain filename with no directory components")
	}
	return name, nil
}

// notebookList returns all notebooks for a workspace, sorted by name.
// A missing notebooks directory is treated as an empty collection (not an error).
func notebookList(root, id string) ([]notebookMeta, error) {
	dir := notebooksDir(root, id)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return make([]notebookMeta, 0), nil
		}
		return nil, err
	}

	out := make([]notebookMeta, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var doc notebookDoc
		if err := json.Unmarshal(data, &doc); err != nil {
			return nil, err
		}
		out = append(out, notebookMeta{Name: doc.Name, UpdatedAt: doc.UpdatedAt})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// notebookGet retrieves a single notebook by name. Returns errNotebookNotFound
// when the file does not exist.
func notebookGet(root, id, name string) (*notebookDoc, error) {
	clean, err := sanitizeNotebookName(name)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(notebooksDir(root, id), clean+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errNotebookNotFound
		}
		return nil, err
	}
	var doc notebookDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	// Ensure Cells is never nil so JSON encodes as [] not null.
	if doc.Cells == nil {
		doc.Cells = make([]notebookCell, 0)
	}
	return &doc, nil
}

// notebookPut creates or updates a notebook atomically (temp-file + rename).
func notebookPut(root, id, name string, cells []notebookCell) (*notebookDoc, error) {
	clean, err := sanitizeNotebookName(name)
	if err != nil {
		return nil, err
	}
	dir := notebooksDir(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if cells == nil {
		cells = make([]notebookCell, 0)
	}
	doc := notebookDoc{
		Name:      clean,
		Cells:     cells,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}

	// Write atomically: temp file in the same dir, then rename.
	tmp, err := os.CreateTemp(dir, ".nb-tmp-")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	_, writeErr := tmp.Write(data)
	closeErr := tmp.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(tmpName)
		if writeErr != nil {
			return nil, writeErr
		}
		return nil, closeErr
	}

	target := filepath.Join(dir, clean+".json")
	if err := os.Rename(tmpName, target); err != nil {
		_ = os.Remove(tmpName)
		return nil, err
	}
	return &doc, nil
}

// notebookDelete removes a notebook file. Returns errNotebookNotFound when the
// file does not exist.
func notebookDelete(root, id, name string) error {
	clean, err := sanitizeNotebookName(name)
	if err != nil {
		return err
	}
	path := filepath.Join(notebooksDir(root, id), clean+".json")
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return errNotebookNotFound
		}
		return err
	}
	return nil
}
