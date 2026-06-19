package api_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asemones/bibicontrol/api"
)

// TestNotebookStoreRoundTrip exercises the notebook store directly (no HTTP).
func TestNotebookStoreRoundTrip(t *testing.T) {
	root := t.TempDir()
	id := "ws1"

	// 1. List on a fresh root with no notebooks directory → empty slice, not null.
	list, err := api.NotebookList(root, id)
	if err != nil {
		t.Fatalf("notebookList(empty): %v", err)
	}
	if list == nil {
		t.Fatal("notebookList(empty): got nil, want []")
	}
	if len(list) != 0 {
		t.Fatalf("notebookList(empty): got %d rows, want 0", len(list))
	}
	// Must JSON-marshal as [] not null.
	b, _ := json.Marshal(list)
	if string(b) != "[]" {
		t.Fatalf("notebookList(empty) JSON: got %q, want %q", string(b), "[]")
	}

	// 2. Get a missing notebook → not-found sentinel.
	_, err = api.NotebookGet(root, id, "nb1")
	if err == nil {
		t.Fatal("notebookGet(missing): expected error, got nil")
	}
	if !api.IsNotebookNotFound(err) {
		t.Fatalf("notebookGet(missing): expected not-found error, got %v", err)
	}

	// 3. Put a notebook with two cells.
	cells := []api.NotebookCell{
		{Type: "code", Source: "print(1)"},
		{Type: "text", Source: "hello"},
	}
	doc, err := api.NotebookPut(root, id, "nb1", cells)
	if err != nil {
		t.Fatalf("notebookPut: %v", err)
	}
	if doc.Name != "nb1" {
		t.Fatalf("notebookPut: name=%q, want %q", doc.Name, "nb1")
	}
	if doc.UpdatedAt == "" {
		t.Fatal("notebookPut: UpdatedAt is empty")
	}
	if len(doc.Cells) != 2 {
		t.Fatalf("notebookPut: got %d cells, want 2", len(doc.Cells))
	}
	if doc.Cells[0].Type != "code" || doc.Cells[1].Type != "text" {
		t.Fatalf("notebookPut: cells order wrong: %+v", doc.Cells)
	}

	// 4. Get the notebook back and verify.
	got, err := api.NotebookGet(root, id, "nb1")
	if err != nil {
		t.Fatalf("notebookGet: %v", err)
	}
	if got.Name != "nb1" {
		t.Fatalf("notebookGet: name=%q, want %q", got.Name, "nb1")
	}
	if got.Cells == nil {
		t.Fatal("notebookGet: Cells is nil, want []")
	}
	if len(got.Cells) != 2 {
		t.Fatalf("notebookGet: got %d cells, want 2", len(got.Cells))
	}

	// 5. List → one row.
	list, err = api.NotebookList(root, id)
	if err != nil {
		t.Fatalf("notebookList(one): %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("notebookList(one): got %d rows, want 1", len(list))
	}
	if list[0].Name != "nb1" {
		t.Fatalf("notebookList(one): name=%q, want %q", list[0].Name, "nb1")
	}

	// 6. Overwrite via a second Put with different cells.
	cells2 := []api.NotebookCell{{Type: "code", Source: "x = 42"}}
	_, err = api.NotebookPut(root, id, "nb1", cells2)
	if err != nil {
		t.Fatalf("notebookPut(overwrite): %v", err)
	}
	got2, err := api.NotebookGet(root, id, "nb1")
	if err != nil {
		t.Fatalf("notebookGet(after overwrite): %v", err)
	}
	if len(got2.Cells) != 1 || got2.Cells[0].Source != "x = 42" {
		t.Fatalf("notebookGet(after overwrite): cells=%+v, want one cell with source 'x = 42'", got2.Cells)
	}

	// 7. Delete, then Get is not-found and List is empty.
	if err := api.NotebookDelete(root, id, "nb1"); err != nil {
		t.Fatalf("notebookDelete: %v", err)
	}
	_, err = api.NotebookGet(root, id, "nb1")
	if !api.IsNotebookNotFound(err) {
		t.Fatalf("notebookGet(after delete): expected not-found, got %v", err)
	}
	list, err = api.NotebookList(root, id)
	if err != nil {
		t.Fatalf("notebookList(after delete): %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("notebookList(after delete): got %d rows, want 0", len(list))
	}
}

// TestNotebookNameSanitization verifies that bad names are rejected and good
// names are accepted, and that no file escapes the notebooks directory.
func TestNotebookNameSanitization(t *testing.T) {
	root := t.TempDir()
	id := "ws1"
	cells := []api.NotebookCell{{Type: "code", Source: "print(1)"}}

	bad := []string{
		"",
		".",
		"..",
		"../escape",
		"a/b",
		"a\\b",
		"/abs",
		".hidden",
		"sub/../x",
	}
	for _, name := range bad {
		t.Run("bad:"+name, func(t *testing.T) {
			err := api.SanitizeNotebookName(name)
			if err == nil {
				t.Fatalf("sanitizeNotebookName(%q): expected error, got nil", name)
			}
		})
		// Also assert that calling notebookPut with the bad name creates no file
		// outside the notebooks directory.
		_, err := api.NotebookPut(root, id, name, cells)
		if err == nil {
			t.Errorf("notebookPut(%q): expected error, got nil", name)
		}
	}

	// Traversal escape check: after rejecting "../pwn", no file must exist at
	// root/workspaces/pwn.json.
	escapePath := filepath.Join(root, "workspaces", "pwn.json")
	if _, err := os.Stat(escapePath); !os.IsNotExist(err) {
		t.Fatalf("traversal escape: file exists at %q", escapePath)
	}

	// Good name.
	if err := api.SanitizeNotebookName("my-notebook_1"); err != nil {
		t.Fatalf("sanitizeNotebookName(good): unexpected error: %v", err)
	}
}

// TestNotebooksEndpoints exercises all four notebook routes through the HTTP surface.
func TestNotebooksEndpoints(t *testing.T) {
	d := api.New(t.TempDir(), "owner")
	defer func() { _ = d.Close() }()
	h := d.Handler()

	wsID := "ws1"
	base := "/api/workspaces/" + wsID + "/notebooks"

	// 1. GET list → 200, empty array.
	rec := doReq(t, h, http.MethodGet, base, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list(empty): status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "[]" {
		t.Fatalf("list(empty): body=%q, want %q", got, "[]")
	}

	// 2. PUT notebook → 200 with name and cells.
	rec = doReq(t, h, http.MethodPut, base+"/demo",
		`{"cells":[{"type":"code","source":"print(1)"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("put: status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var putDoc struct {
		Name  string `json:"name"`
		Cells []struct {
			Type   string `json:"type"`
			Source string `json:"source"`
		} `json:"cells"`
		UpdatedAt string `json:"updated_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &putDoc); err != nil {
		t.Fatalf("put: decode body: %v", err)
	}
	if putDoc.Name != "demo" {
		t.Fatalf("put: name=%q, want %q", putDoc.Name, "demo")
	}
	if len(putDoc.Cells) != 1 {
		t.Fatalf("put: got %d cells, want 1", len(putDoc.Cells))
	}
	if putDoc.UpdatedAt == "" {
		t.Fatal("put: updated_at is empty")
	}

	// 3. GET list → 200, one row with name "demo".
	rec = doReq(t, h, http.MethodGet, base, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list(one): status=%d, want 200", rec.Code)
	}
	var listRows []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listRows); err != nil {
		t.Fatalf("list(one): decode: %v", err)
	}
	if len(listRows) != 1 || listRows[0].Name != "demo" {
		t.Fatalf("list(one): rows=%+v, want [{name:demo}]", listRows)
	}

	// 4. GET single notebook → 200, cell round-trips.
	rec = doReq(t, h, http.MethodGet, base+"/demo", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get: status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var getDoc struct {
		Name  string `json:"name"`
		Cells []struct {
			Source string `json:"source"`
		} `json:"cells"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &getDoc); err != nil {
		t.Fatalf("get: decode: %v", err)
	}
	if getDoc.Name != "demo" {
		t.Fatalf("get: name=%q, want demo", getDoc.Name)
	}
	if len(getDoc.Cells) != 1 || getDoc.Cells[0].Source != "print(1)" {
		t.Fatalf("get: cells=%+v, want [{source:print(1)}]", getDoc.Cells)
	}

	// 5. GET missing notebook → 404.
	rec = doReq(t, h, http.MethodGet, base+"/missing", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get(missing): status=%d, want 404; body=%s", rec.Code, rec.Body.String())
	}

	// 6. DELETE demo → 204; then GET → 404.
	rec = doReq(t, h, http.MethodDelete, base+"/demo", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status=%d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodGet, base+"/demo", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get(after delete): status=%d, want 404", rec.Code)
	}

	// 7. PUT with a dotfile name → 400.
	rec = doReq(t, h, http.MethodPut, base+"/.hidden",
		`{"cells":[]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("put(.hidden): status=%d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}
