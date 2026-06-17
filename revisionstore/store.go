// Package revisionstore persists save revision and script provenance metadata.
package revisionstore

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/asemones/bibicontrol/blobstore"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// Store records script runs and immutable save revisions in SQLite.
type Store struct {
	db *sql.DB
}

// ScriptRunInput describes a script execution to persist.
type ScriptRunInput struct {
	ScriptSHA256 string
	StartedAt    time.Time
	FinishedAt   *time.Time
	Status       string
	Error        string
	Output       string
	StagedOps    int64
	DryRun       bool
}

// ScriptRun is a persisted script execution.
type ScriptRun struct {
	ID           int64
	ScriptSHA256 string
	StartedAt    time.Time
	FinishedAt   *time.Time
	Status       string
	Error        string
	Output       string
	StagedOps    int64
	DryRun       bool
}

// RevisionInput describes a produced save revision to persist.
type RevisionInput struct {
	ParentID    *int64
	SourcePath  string
	BlobRef     blobstore.Ref
	ScriptRunID int64
	CreatedAt   time.Time
}

// Revision is a persisted save revision.
type Revision struct {
	ID          int64
	SHA256      string
	Size        int64
	ParentID    *int64
	SourcePath  string
	BlobRef     blobstore.Ref
	ScriptRunID int64
	CreatedAt   time.Time
}

// Open opens a SQLite metadata store at path and applies the schema. Pass
// ":memory:" for an in-memory store.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("revisionstore: path is required")
	}

	// foreign_keys is connection-scoped in SQLite, so it must be set per
	// connection via the DSN rather than relying on a one-time migration exec.
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := ApplyMigrations(context.Background(), db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// ApplyMigrations creates the SQLite schema used by the revision store.
func ApplyMigrations(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("revisionstore: db is nil")
	}
	ctx, err := usableContext(ctx)
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("revisionstore migration schema.sql: %w", err)
	}
	return nil
}

// Close closes the underlying SQLite handle.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// RecordScriptRun inserts a script run and returns the stored row.
func (s *Store) RecordScriptRun(ctx context.Context, input ScriptRunInput) (ScriptRun, error) {
	if s == nil || s.db == nil {
		return ScriptRun{}, fmt.Errorf("revisionstore: Store is nil")
	}
	ctx, err := usableContext(ctx)
	if err != nil {
		return ScriptRun{}, err
	}
	run, err := normalizeScriptRun(input)
	if err != nil {
		return ScriptRun{}, err
	}

	finishedAt := sql.NullString{}
	if run.FinishedAt != nil {
		finishedAt = sql.NullString{String: formatTime(*run.FinishedAt), Valid: true}
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO script_runs (
			script_sha256,
			started_at,
			finished_at,
			status,
			error,
			output,
			staged_ops,
			dry_run
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`,
		run.ScriptSHA256,
		formatTime(run.StartedAt),
		finishedAt,
		run.Status,
		run.Error,
		run.Output,
		run.StagedOps,
		boolInt(run.DryRun),
	)
	if err != nil {
		return ScriptRun{}, fmt.Errorf("revisionstore: insert script run: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return ScriptRun{}, fmt.Errorf("revisionstore: script run id: %w", err)
	}
	return s.ScriptRunByID(ctx, id)
}

// RecordRevision inserts a produced save revision and returns the stored row.
func (s *Store) RecordRevision(ctx context.Context, input RevisionInput) (Revision, error) {
	if s == nil || s.db == nil {
		return Revision{}, fmt.Errorf("revisionstore: Store is nil")
	}
	ctx, err := usableContext(ctx)
	if err != nil {
		return Revision{}, err
	}
	revision, blobRefJSON, inlineBlob, err := normalizeRevision(input)
	if err != nil {
		return Revision{}, err
	}

	parentID := sql.NullInt64{}
	if revision.ParentID != nil {
		parentID = sql.NullInt64{Int64: *revision.ParentID, Valid: true}
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO save_revisions (
			sha256,
			size,
			parent_id,
			source_path,
			blob_ref,
			inline_blob,
			script_run_id,
			created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`,
		revision.SHA256,
		revision.Size,
		parentID,
		revision.SourcePath,
		blobRefJSON,
		inlineBlob,
		revision.ScriptRunID,
		formatTime(revision.CreatedAt),
	)
	if err != nil {
		return Revision{}, fmt.Errorf("revisionstore: insert revision: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Revision{}, fmt.Errorf("revisionstore: revision id: %w", err)
	}
	return s.RevisionByID(ctx, id)
}

// ScriptRunByID returns the script run with id. sql.ErrNoRows is returned when
// no run exists.
func (s *Store) ScriptRunByID(ctx context.Context, id int64) (ScriptRun, error) {
	if s == nil || s.db == nil {
		return ScriptRun{}, fmt.Errorf("revisionstore: Store is nil")
	}
	ctx, err := usableContext(ctx)
	if err != nil {
		return ScriptRun{}, err
	}
	return scanScriptRun(s.db.QueryRowContext(ctx, `
		SELECT id, script_sha256, started_at, finished_at, status, error, output, staged_ops, dry_run
		FROM script_runs
		WHERE id = ?
	`, id))
}

// RevisionByID returns the save revision with id. sql.ErrNoRows is returned
// when no revision exists.
func (s *Store) RevisionByID(ctx context.Context, id int64) (Revision, error) {
	if s == nil || s.db == nil {
		return Revision{}, fmt.Errorf("revisionstore: Store is nil")
	}
	ctx, err := usableContext(ctx)
	if err != nil {
		return Revision{}, err
	}
	return scanRevision(s.db.QueryRowContext(ctx, `
		SELECT id, sha256, size, parent_id, source_path, blob_ref, inline_blob, script_run_id, created_at
		FROM save_revisions
		WHERE id = ?
	`, id))
}

// RevisionsBySHA256 returns all revisions whose save bytes have sha256, ordered
// by insertion id.
func (s *Store) RevisionsBySHA256(ctx context.Context, sha256 string) ([]Revision, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("revisionstore: Store is nil")
	}
	ctx, err := usableContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateSHA256(sha256); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, sha256, size, parent_id, source_path, blob_ref, inline_blob, script_run_id, created_at
		FROM save_revisions
		WHERE sha256 = ?
		ORDER BY id
	`, sha256)
	if err != nil {
		return nil, fmt.Errorf("revisionstore: query revisions by sha256: %w", err)
	}
	defer rows.Close()

	var revisions []Revision
	for rows.Next() {
		revision, err := scanRevisionRows(rows)
		if err != nil {
			return nil, err
		}
		revisions = append(revisions, revision)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("revisionstore: iterate revisions by sha256: %w", err)
	}
	return revisions, nil
}

func normalizeScriptRun(input ScriptRunInput) (ScriptRun, error) {
	if err := validateSHA256(input.ScriptSHA256); err != nil {
		return ScriptRun{}, fmt.Errorf("revisionstore: script sha256: %w", err)
	}
	if input.Status == "" {
		return ScriptRun{}, fmt.Errorf("revisionstore: script run status is required")
	}
	if input.StagedOps < 0 {
		return ScriptRun{}, fmt.Errorf("revisionstore: staged ops %d is negative", input.StagedOps)
	}
	startedAt := normalizeTime(input.StartedAt)
	if startedAt.IsZero() {
		startedAt = nowUTC()
	}

	var finishedAt *time.Time
	if input.FinishedAt != nil {
		normalized := normalizeTime(*input.FinishedAt)
		if normalized.IsZero() {
			return ScriptRun{}, fmt.Errorf("revisionstore: finished_at cannot be zero when provided")
		}
		finishedAt = &normalized
	}

	return ScriptRun{
		ScriptSHA256: input.ScriptSHA256,
		StartedAt:    startedAt,
		FinishedAt:   finishedAt,
		Status:       input.Status,
		Error:        input.Error,
		Output:       input.Output,
		StagedOps:    input.StagedOps,
		DryRun:       input.DryRun,
	}, nil
}

func normalizeRevision(input RevisionInput) (Revision, string, []byte, error) {
	if err := input.BlobRef.Validate(); err != nil {
		return Revision{}, "", nil, err
	}
	if input.ScriptRunID <= 0 {
		return Revision{}, "", nil, fmt.Errorf("revisionstore: script_run_id %d is invalid", input.ScriptRunID)
	}
	if input.ParentID != nil && *input.ParentID <= 0 {
		return Revision{}, "", nil, fmt.Errorf("revisionstore: parent_id %d is invalid", *input.ParentID)
	}
	createdAt := normalizeTime(input.CreatedAt)
	if createdAt.IsZero() {
		createdAt = nowUTC()
	}

	blobRefJSON, inlineBlob, err := encodeBlobRef(input.BlobRef)
	if err != nil {
		return Revision{}, "", nil, err
	}
	ref := blobstore.Ref{
		SHA256: input.BlobRef.SHA256,
		Size:   input.BlobRef.Size,
		Inline: cloneBytes(input.BlobRef.Inline),
	}
	revision := Revision{
		SHA256:      input.BlobRef.SHA256,
		Size:        input.BlobRef.Size,
		ParentID:    cloneInt64Ptr(input.ParentID),
		SourcePath:  input.SourcePath,
		BlobRef:     ref,
		ScriptRunID: input.ScriptRunID,
		CreatedAt:   createdAt,
	}
	return revision, blobRefJSON, inlineBlob, nil
}

type storedBlobRef struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

func encodeBlobRef(ref blobstore.Ref) (string, []byte, error) {
	if err := ref.Validate(); err != nil {
		return "", nil, err
	}
	encoded, err := json.Marshal(storedBlobRef{
		SHA256: ref.SHA256,
		Size:   ref.Size,
	})
	if err != nil {
		return "", nil, fmt.Errorf("revisionstore: encode blob ref: %w", err)
	}
	return string(encoded), cloneBytes(ref.Inline), nil
}

func decodeBlobRef(blobRefJSON string, inlineBlob []byte) (blobstore.Ref, error) {
	var stored storedBlobRef
	if err := json.Unmarshal([]byte(blobRefJSON), &stored); err != nil {
		return blobstore.Ref{}, fmt.Errorf("revisionstore: decode blob ref: %w", err)
	}
	ref := blobstore.Ref{
		SHA256: stored.SHA256,
		Size:   stored.Size,
		Inline: cloneBytes(inlineBlob),
	}
	if err := ref.Validate(); err != nil {
		return blobstore.Ref{}, err
	}
	return ref, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanScriptRun(row rowScanner) (ScriptRun, error) {
	var run ScriptRun
	var startedAt string
	var finishedAt sql.NullString
	var dryRun int64
	if err := row.Scan(
		&run.ID,
		&run.ScriptSHA256,
		&startedAt,
		&finishedAt,
		&run.Status,
		&run.Error,
		&run.Output,
		&run.StagedOps,
		&dryRun,
	); err != nil {
		return ScriptRun{}, err
	}

	parsedStartedAt, err := parseTime("started_at", startedAt)
	if err != nil {
		return ScriptRun{}, err
	}
	run.StartedAt = parsedStartedAt
	if finishedAt.Valid {
		parsedFinishedAt, err := parseTime("finished_at", finishedAt.String)
		if err != nil {
			return ScriptRun{}, err
		}
		run.FinishedAt = &parsedFinishedAt
	}
	run.DryRun = dryRun != 0
	return run, nil
}

func scanRevision(row rowScanner) (Revision, error) {
	return scanRevisionRows(row)
}

func scanRevisionRows(row rowScanner) (Revision, error) {
	var revision Revision
	var parentID sql.NullInt64
	var blobRefJSON string
	var inlineBlob []byte
	var createdAt string
	if err := row.Scan(
		&revision.ID,
		&revision.SHA256,
		&revision.Size,
		&parentID,
		&revision.SourcePath,
		&blobRefJSON,
		&inlineBlob,
		&revision.ScriptRunID,
		&createdAt,
	); err != nil {
		return Revision{}, err
	}
	if parentID.Valid {
		revision.ParentID = &parentID.Int64
	}

	ref, err := decodeBlobRef(blobRefJSON, inlineBlob)
	if err != nil {
		return Revision{}, err
	}
	if revision.SHA256 != ref.SHA256 {
		return Revision{}, fmt.Errorf("revisionstore: revision sha256 %q does not match blob ref %q", revision.SHA256, ref.SHA256)
	}
	if revision.Size != ref.Size {
		return Revision{}, fmt.Errorf("revisionstore: revision size %d does not match blob ref %d", revision.Size, ref.Size)
	}
	revision.BlobRef = ref

	parsedCreatedAt, err := parseTime("created_at", createdAt)
	if err != nil {
		return Revision{}, err
	}
	revision.CreatedAt = parsedCreatedAt
	return revision, nil
}

func parseTime(field, value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("revisionstore: parse %s: %w", field, err)
	}
	return parsed.UTC(), nil
}

func formatTime(t time.Time) string {
	return normalizeTime(t).Format(time.RFC3339Nano)
}

func normalizeTime(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	return t.UTC().Round(0)
}

func nowUTC() time.Time {
	return time.Now().UTC().Round(0)
}

func boolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func cloneBytes(in []byte) []byte {
	if in == nil {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

func cloneInt64Ptr(in *int64) *int64 {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func validateSHA256(digest string) error {
	if len(digest) != blobstore.SHA256HexLength {
		return fmt.Errorf("sha256 digest length %d, want %d", len(digest), blobstore.SHA256HexLength)
	}
	for _, c := range digest {
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') {
			continue
		}
		return fmt.Errorf("sha256 digest %q is not lowercase hex", digest)
	}
	return nil
}

func usableContext(ctx context.Context) (context.Context, error) {
	if ctx == nil {
		return context.Background(), nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return ctx, nil
}

// IsNotFound reports whether err is the no-row result returned by lookup methods.
func IsNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
