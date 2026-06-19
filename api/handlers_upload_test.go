package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/asemones/bibicontrol/api"
	"github.com/asemones/bibicontrol/workspace"
)

// buildMultipart creates a multipart/form-data body with a single "file" part
// containing the given filename and content. It returns the body bytes and the
// Content-Type header value (which embeds the boundary).
func buildMultipart(t *testing.T, filename string, content []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("buildMultipart: CreateFormFile: %v", err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatalf("buildMultipart: Write: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("buildMultipart: Close: %v", err)
	}
	return &buf, mw.FormDataContentType()
}

// TestUploadWritesFile verifies that a valid multipart upload writes the file
// to root/workspaces/{id}/uploads/{name} and returns an absolute path.
func TestUploadWritesFile(t *testing.T) {
	ctx := testCtx(t)
	root := t.TempDir()

	ws, err := workspace.Create(ctx, root, "owner", "testws")
	if err != nil {
		t.Fatalf("workspace.Create: %v", err)
	}
	id := ws.ID()
	if err := ws.Close(); err != nil {
		t.Fatalf("ws.Close: %v", err)
	}

	d := api.New(root, "owner")
	defer func() { _ = d.Close() }()

	payload := []byte("fake zip bytes 1234")
	body, ct := buildMultipart(t, "save.zip", payload)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+id+"/upload", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	d.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("upload: got status %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("upload: decode response: %v", err)
	}

	gotPath := resp["path"]
	if gotPath == "" {
		t.Fatal("upload: response missing 'path' field")
	}
	if !filepath.IsAbs(gotPath) {
		t.Fatalf("upload: returned path %q is not absolute", gotPath)
	}
	if filepath.Base(gotPath) != "save.zip" {
		t.Fatalf("upload: returned path %q does not end in save.zip", gotPath)
	}

	uploadsDir := filepath.Join(root, "workspaces", id, "uploads")
	if !filepath.HasPrefix(gotPath, uploadsDir) {
		t.Fatalf("upload: returned path %q is not under uploads dir %q", gotPath, uploadsDir)
	}

	// Verify the file contents on disk match what was uploaded.
	got, err := os.ReadFile(gotPath)
	if err != nil {
		t.Fatalf("upload: ReadFile(%q): %v", gotPath, err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("upload: on-disk content %q, want %q", got, payload)
	}
}

// TestUploadRejectsTraversal verifies that a traversal filename is rejected
// with a 4xx and that no file is written outside the uploads directory.
func TestUploadRejectsTraversal(t *testing.T) {
	root := t.TempDir()

	d := api.New(root, "owner")
	defer func() { _ = d.Close() }()

	cases := []struct {
		filename string
		desc     string
	}{
		{"../../etc/passwd", "parent traversal"},
		{"..", "bare dotdot"},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			body, ct := buildMultipart(t, tc.filename, []byte("should not land"))

			req := httptest.NewRequest(http.MethodPost, "/api/workspaces/testid/upload", body)
			req.Header.Set("Content-Type", ct)
			rec := httptest.NewRecorder()
			d.Handler().ServeHTTP(rec, req)

			if rec.Code < 400 || rec.Code >= 500 {
				t.Fatalf("traversal %q: got status %d, want 4xx", tc.filename, rec.Code)
			}

			var resp map[string]string
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("traversal %q: decode response: %v", tc.filename, err)
			}
			if resp["error"] == "" {
				t.Fatalf("traversal %q: expected non-empty error field", tc.filename)
			}

			// Confirm nothing was written outside the uploads directory.
			escapedPath := filepath.Join(root, "etc", "passwd")
			if _, err := os.Stat(escapedPath); !os.IsNotExist(err) {
				t.Fatalf("traversal %q: file %q should not exist, got err: %v", tc.filename, escapedPath, err)
			}
		})
	}
}

// TestUploadMissingFilePart verifies that a multipart body without a "file"
// part returns 400 with a JSON error field.
func TestUploadMissingFilePart(t *testing.T) {
	root := t.TempDir()

	d := api.New(root, "owner")
	defer func() { _ = d.Close() }()

	// Build a multipart body with a different field name (not "file").
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("other", "file.zip")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := io.WriteString(fw, "data"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("mw.Close: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/testid/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	d.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing file part: got status %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("missing file part: decode response: %v", err)
	}
	if resp["error"] == "" {
		t.Fatal("missing file part: expected non-empty error field")
	}
}

// TestUploadCreatesUploadsDirForFreshId verifies that uploading to a workspace
// id whose directory does not yet exist creates the uploads directory and lands
// the file, documenting the MkdirAll behaviour.
func TestUploadCreatesUploadsDirForFreshId(t *testing.T) {
	root := t.TempDir()

	d := api.New(root, "owner")
	defer func() { _ = d.Close() }()

	freshID := "nonexistent-ws-id"
	payload := []byte("test data")
	body, ct := buildMultipart(t, "world.zip", payload)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+freshID+"/upload", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	d.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("fresh id upload: got status %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("fresh id upload: decode response: %v", err)
	}

	gotPath := resp["path"]
	if gotPath == "" {
		t.Fatal("fresh id upload: response missing 'path' field")
	}

	expectedDir := filepath.Join(root, "workspaces", freshID, "uploads")
	if !filepath.HasPrefix(gotPath, expectedDir) {
		t.Fatalf("fresh id upload: returned path %q is not under %q", gotPath, expectedDir)
	}

	got, err := os.ReadFile(gotPath)
	if err != nil {
		t.Fatalf("fresh id upload: ReadFile: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("fresh id upload: on-disk content %q, want %q", got, payload)
	}
}
