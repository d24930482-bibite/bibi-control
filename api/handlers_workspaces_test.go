package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/asemones/bibicontrol/api"
)

// doReq drives a request through the daemon's handler and returns the recorder.
func doReq(t *testing.T, h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Buffer
	if body != "" {
		rdr = bytes.NewBufferString(body)
	} else {
		rdr = bytes.NewBuffer(nil)
	}
	req := httptest.NewRequest(method, target, rdr)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestWorkspacesRoundTrip exercises the full create -> list -> rename -> delete
// lifecycle through the HTTP surface, plus the not-found and bad-input edges.
//
// The created workspace is deleted through the daemon that created it, so
// d.open[id] is populated and the DELETE handler's evict+close branch (the
// route's core ordering invariant) is actually exercised — a fresh daemon that
// never opened the id would silently skip that path.
func TestWorkspacesRoundTrip(t *testing.T) {
	d := api.New(t.TempDir(), "owner")
	defer func() { _ = d.Close() }()
	h := d.Handler()

	// 1. Empty root lists as [] (not null).
	rec := doReq(t, h, http.MethodGet, "/api/workspaces", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list(empty): status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := bytes_TrimSpace(rec.Body.Bytes()); got != "[]" {
		t.Fatalf("list(empty): body=%q, want %q", got, "[]")
	}

	// 2. Create.
	rec = doReq(t, h, http.MethodPost, "/api/workspaces", `{"name":"alpha"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status=%d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Owner string `json:"owner"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("create: decode body: %v", err)
	}
	if created.ID == "" {
		t.Fatal("create: empty id")
	}
	if created.Name != "alpha" {
		t.Fatalf("create: name=%q, want %q", created.Name, "alpha")
	}
	if created.Owner != "owner" {
		t.Fatalf("create: owner=%q, want %q", created.Owner, "owner")
	}
	id := created.ID

	// 3. List shows the created workspace.
	rec = doReq(t, h, http.MethodGet, "/api/workspaces", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status=%d, want 200", rec.Code)
	}
	list := decodeList(t, rec.Body.Bytes())
	if len(list) != 1 || list[0].ID != id || list[0].Name != "alpha" {
		t.Fatalf("list: got %+v, want one alpha row with id %q", list, id)
	}

	// 4. Rename, then confirm via list.
	rec = doReq(t, h, http.MethodPatch, "/api/workspaces/"+id, `{"name":"beta"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodGet, "/api/workspaces", "")
	list = decodeList(t, rec.Body.Bytes())
	if len(list) != 1 || list[0].Name != "beta" {
		t.Fatalf("rename: list=%+v, want one beta row", list)
	}

	// 5. Delete (exercises evict+close of the cached handle), then list is empty.
	rec = doReq(t, h, http.MethodDelete, "/api/workspaces/"+id, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status=%d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodGet, "/api/workspaces", "")
	if got := bytes_TrimSpace(rec.Body.Bytes()); got != "[]" {
		t.Fatalf("list(after delete): body=%q, want %q", got, "[]")
	}

	// 5b. Rename/Delete of the now-deleted id are 404.
	rec = doReq(t, h, http.MethodPatch, "/api/workspaces/"+id, `{"name":"gamma"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("rename(deleted): status=%d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodDelete, "/api/workspaces/"+id, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete(deleted): status=%d, want 404; body=%s", rec.Code, rec.Body.String())
	}

	// 6. Create with empty name is 400.
	rec = doReq(t, h, http.MethodPost, "/api/workspaces", `{"name":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create(empty name): status=%d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

type wsRow struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Owner string `json:"owner"`
}

func decodeList(t *testing.T, b []byte) []wsRow {
	t.Helper()
	var list []wsRow
	if err := json.Unmarshal(b, &list); err != nil {
		t.Fatalf("decode list: %v (body=%s)", err, string(b))
	}
	return list
}

// bytes_TrimSpace trims trailing/leading whitespace (the JSON encoder appends a
// newline) so an empty-array body compares cleanly to "[]".
func bytes_TrimSpace(b []byte) string {
	return string(bytes.TrimSpace(b))
}
