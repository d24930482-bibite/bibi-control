package api

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// maxUploadBytes caps multipart upload bodies at 256 MiB. Bibite saves are zips
// (the largest fixture is ~3.2 MiB), so this leaves generous headroom.
const maxUploadBytes = 256 << 20

// handleUpload handles POST /api/workspaces/{id}/upload.
//
// The handler accepts a multipart/form-data body with a "file" part, sanitizes
// the supplied filename (rejecting path traversal and separators), streams the
// bytes to root/workspaces/{id}/uploads/{sanitized-name}, and returns
// {"path": "<absolute server path>"}.
//
// Layout note: the workspace package keeps its directory layout unexported
// (workspace/workspace.go). The api package reconstructs the
// root/workspaces/{id} path literally. If workspace.workspaceDir ever changes,
// this string must change to match.
//
// The handler does NOT open the workspace (no d.ws(...) call) — upload is a
// pure filesystem write scoped by the path id; opening a DuckDB writer for a
// file copy would waste resources and risk the "never open twice" invariant.
func (d *Daemon) handleUpload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Cap the request body to prevent runaway memory use on large uploads.
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	defer file.Close()

	// Go's multipart parser normalizes header.Filename (e.g. strips directory
	// components from "../../etc/passwd" → "passwd") before we can inspect it.
	// To enforce the rejection contract ("traversal → 4xx, never silently
	// rename"), we parse the raw Content-Disposition header ourselves and feed
	// the original client-supplied filename to sanitizeUploadName.
	rawFilename := rawContentDispositionFilename(header.Header.Get("Content-Disposition"))
	if rawFilename == "" {
		// Fall back to the Go-normalized name if raw parsing failed.
		rawFilename = header.Filename
	}

	name, err := sanitizeUploadName(rawFilename)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	// Build the destination path.
	dir := filepath.Join(d.root, "workspaces", id, "uploads")
	dst := filepath.Join(dir, name)

	// Defense in depth: verify the sanitized name did not escape the uploads dir.
	if filepath.Dir(dst) != filepath.Clean(dir) {
		writeError(w, http.StatusBadRequest, errors.New("invalid filename: path escapes uploads directory"))
		return
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	out, err := os.Create(dst)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	if _, err = io.Copy(out, file); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeError(w, http.StatusRequestEntityTooLarge, err)
		} else {
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}

	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	absPath, _ := filepath.Abs(dst)
	writeJSON(w, http.StatusOK, map[string]string{"path": absPath})
}

// rawContentDispositionFilename extracts the raw filename parameter from a
// Content-Disposition header string, preserving whatever the client sent
// (including any path separators in the value). Returns "" on parse failure.
func rawContentDispositionFilename(cd string) string {
	if cd == "" {
		return ""
	}
	_, params, err := mime.ParseMediaType(cd)
	if err != nil {
		return ""
	}
	return params["filename"]
}

// sanitizeUploadName validates and returns a safe base filename. It rejects
// names that are empty, ".", "..", contain path separators, or NUL bytes.
//
// The rejection (rather than silent rename) is load-bearing: callers that
// supply traversal paths (e.g. "../../etc/passwd") must receive a 4xx, not a
// silently rewritten filename. We check the raw input for separators first so
// that traversal attempts are rejected rather than silently stripped to their
// leaf component.
func sanitizeUploadName(name string) (string, error) {
	// Reject immediately if the raw name contains any path separator or NUL —
	// this catches traversal attempts like "../../etc/passwd" and "foo/bar".
	if strings.ContainsAny(name, "/\\\x00") {
		return "", errors.New("invalid filename: contains path separator or NUL")
	}
	base := filepath.Base(name)
	if base == "" || base == "." || base == ".." {
		return "", errors.New("invalid filename: empty or reserved name")
	}
	// Redundant guard for belt-and-suspenders: the base should also be clean.
	if strings.ContainsAny(base, "/\\\x00") {
		return "", errors.New("invalid filename: contains path separator or NUL")
	}
	return base, nil
}
