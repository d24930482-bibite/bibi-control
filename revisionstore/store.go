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
	"github.com/google/uuid"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// ErrRevisionIsHead is returned by EvictRevisionBlob when the revision is a
// world's current head; a head is never evictable.
var ErrRevisionIsHead = errors.New("revisionstore: revision is a world head")

// ErrBlobStillReferenced is returned by EvictRevisionBlob when other live
// references to the blob bytes remain (refcount > 1).
var ErrBlobStillReferenced = errors.New("revisionstore: blob is still referenced")

// Store records script runs and immutable save revisions in SQLite.
type Store struct {
	db *sql.DB
}

// sqlExec is the ExecContext surface shared by *sql.DB and *sql.Tx so the
// revision INSERT lives in a single place regardless of the transaction handle.
type sqlExec interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
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
	WorldID     string
	SourcePath  string
	BlobRef     blobstore.Ref
	ScriptRunID int64
	CreatedAt   time.Time
}

// Revision is a persisted save revision.
type Revision struct {
	ID                  int64
	SHA256              string
	Size                int64
	ParentID            *int64
	WorldID             string
	SourcePath          string
	BlobRef             blobstore.Ref
	ScriptRunID         int64
	CreatedAt           time.Time
	Tier                string
	BlobPresent         bool
	Refcount            int64
	MirrorSchemaVersion *int64
}

// WorkspaceInput describes a workspace to create.
type WorkspaceInput struct {
	Owner     string
	Name      string
	CreatedAt time.Time
}

// Workspace is a persisted workspace registry row.
type Workspace struct {
	ID        string
	Owner     string
	Name      string
	CreatedAt time.Time
}

// WorldInput describes a world to create within a workspace.
type WorldInput struct {
	WorkspaceID string
	Name        string
	CreatedAt   time.Time
}

// World is a persisted world. HeadRevisionID and SimTime are NULL until the
// first revision advances the head.
type World struct {
	ID             string
	WorkspaceID    string
	Name           string
	HeadRevisionID *int64
	SimTime        *float64
	CreatedAt      time.Time
}

// NodeInput describes a node (a compat-mode runner binding) to create.
type NodeInput struct {
	WorkspaceID string
	WorldID     string
	NodeID      string
	RunID       string
	Status      string
	CompatAddr  string
	DropPath    string
	CreatedAt   time.Time
}

// Node is a persisted node. WorldID is "" when the node is unbound (SQL NULL).
type Node struct {
	ID          string
	WorkspaceID string
	WorldID     string
	NodeID      string
	RunID       string
	Status      string
	CompatAddr  string
	DropPath    string
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

	id, err := insertRevisionTx(ctx, s.db, revision, blobRefJSON, inlineBlob)
	if err != nil {
		return Revision{}, err
	}
	return s.RevisionByID(ctx, id)
}

// RecordRevisionAdvancingHead inserts a revision and advances the world's head
// (head_revision_id + sim_time) in a single SQLite transaction so a crash can
// never leave a world head pointing at a missing revision. input.WorldID is
// forced to worldID so the caller cannot desync the two; input.ParentID is
// whatever the caller passes (the world's current head, or nil for the first
// revision). The committed revision is re-read and returned.
func (s *Store) RecordRevisionAdvancingHead(ctx context.Context, worldID string, simTime *float64, input RevisionInput) (Revision, error) {
	if s == nil || s.db == nil {
		return Revision{}, fmt.Errorf("revisionstore: Store is nil")
	}
	ctx, err := usableContext(ctx)
	if err != nil {
		return Revision{}, err
	}
	if worldID == "" {
		return Revision{}, fmt.Errorf("revisionstore: world id is required")
	}
	input.WorldID = worldID

	revision, blobRefJSON, inlineBlob, err := normalizeRevision(input)
	if err != nil {
		return Revision{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Revision{}, fmt.Errorf("revisionstore: begin tx: %w", err)
	}
	defer tx.Rollback()

	id, err := insertRevisionTx(ctx, tx, revision, blobRefJSON, inlineBlob)
	if err != nil {
		return Revision{}, err
	}
	if err := execSetWorldHead(ctx, tx, worldID, id, simTime); err != nil {
		return Revision{}, err
	}
	if err := tx.Commit(); err != nil {
		return Revision{}, fmt.Errorf("revisionstore: commit advancing-head tx: %w", err)
	}

	return s.RevisionByID(ctx, id)
}

// insertRevisionTx inserts a save revision row via q (either *sql.DB or *sql.Tx)
// and returns the new row id. The tier/blob_present/refcount/mirror_schema_version
// columns are intentionally omitted so the schema defaults ('full'/1/0/NULL)
// apply.
func insertRevisionTx(ctx context.Context, q sqlExec, revision Revision, blobRefJSON string, inlineBlob []byte) (int64, error) {
	parentID := sql.NullInt64{}
	if revision.ParentID != nil {
		parentID = sql.NullInt64{Int64: *revision.ParentID, Valid: true}
	}
	worldID := sql.NullString{}
	if revision.WorldID != "" {
		worldID = sql.NullString{String: revision.WorldID, Valid: true}
	}
	result, err := q.ExecContext(ctx, `
		INSERT INTO save_revisions (
			sha256,
			size,
			parent_id,
			source_path,
			blob_ref,
			inline_blob,
			script_run_id,
			created_at,
			world_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		revision.SHA256,
		revision.Size,
		parentID,
		revision.SourcePath,
		blobRefJSON,
		inlineBlob,
		revision.ScriptRunID,
		formatTime(revision.CreatedAt),
		worldID,
	)
	if err != nil {
		return 0, fmt.Errorf("revisionstore: insert revision: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("revisionstore: revision id: %w", err)
	}
	return id, nil
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
		SELECT id, sha256, size, parent_id, source_path, blob_ref, inline_blob, script_run_id, created_at,
			world_id, tier, blob_present, refcount, mirror_schema_version
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
		SELECT id, sha256, size, parent_id, source_path, blob_ref, inline_blob, script_run_id, created_at,
			world_id, tier, blob_present, refcount, mirror_schema_version
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

// RevisionsForWorld returns every revision recorded against worldID, ordered by
// insertion id (the lineage/history order). It uses save_revisions_world_id_idx.
func (s *Store) RevisionsForWorld(ctx context.Context, worldID string) ([]Revision, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("revisionstore: Store is nil")
	}
	ctx, err := usableContext(ctx)
	if err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, sha256, size, parent_id, source_path, blob_ref, inline_blob, script_run_id, created_at,
			world_id, tier, blob_present, refcount, mirror_schema_version
		FROM save_revisions
		WHERE world_id = ?
		ORDER BY id
	`, worldID)
	if err != nil {
		return nil, fmt.Errorf("revisionstore: query revisions for world: %w", err)
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
		return nil, fmt.Errorf("revisionstore: iterate revisions for world: %w", err)
	}
	return revisions, nil
}

// CreateWorkspace inserts a workspace with a freshly allocated UUID id and
// returns the stored row.
func (s *Store) CreateWorkspace(ctx context.Context, input WorkspaceInput) (Workspace, error) {
	if s == nil || s.db == nil {
		return Workspace{}, fmt.Errorf("revisionstore: Store is nil")
	}
	ctx, err := usableContext(ctx)
	if err != nil {
		return Workspace{}, err
	}
	createdAt := normalizeTime(input.CreatedAt)
	if createdAt.IsZero() {
		createdAt = nowUTC()
	}
	id := uuid.NewString()
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO workspaces (id, owner, name, created_at) VALUES (?, ?, ?, ?)
	`, id, input.Owner, input.Name, formatTime(createdAt)); err != nil {
		return Workspace{}, fmt.Errorf("revisionstore: insert workspace: %w", err)
	}
	return s.GetWorkspace(ctx, id)
}

// GetWorkspace returns the workspace with id. sql.ErrNoRows is returned when no
// workspace exists.
func (s *Store) GetWorkspace(ctx context.Context, id string) (Workspace, error) {
	if s == nil || s.db == nil {
		return Workspace{}, fmt.Errorf("revisionstore: Store is nil")
	}
	ctx, err := usableContext(ctx)
	if err != nil {
		return Workspace{}, err
	}
	return scanWorkspace(s.db.QueryRowContext(ctx, `
		SELECT id, owner, name, created_at FROM workspaces WHERE id = ?
	`, id))
}

// ListWorkspaces returns all workspaces ordered by created_at then id.
func (s *Store) ListWorkspaces(ctx context.Context) ([]Workspace, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("revisionstore: Store is nil")
	}
	ctx, err := usableContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, owner, name, created_at FROM workspaces ORDER BY created_at, id
	`)
	if err != nil {
		return nil, fmt.Errorf("revisionstore: query workspaces: %w", err)
	}
	defer rows.Close()

	var workspaces []Workspace
	for rows.Next() {
		ws, err := scanWorkspace(rows)
		if err != nil {
			return nil, err
		}
		workspaces = append(workspaces, ws)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("revisionstore: iterate workspaces: %w", err)
	}
	return workspaces, nil
}

// CreateWorld inserts a world with a freshly allocated UUID id. Its head and
// sim_time start NULL until the first revision advances them. The workspace_id
// must reference an existing workspace or the FK rejects the insert.
func (s *Store) CreateWorld(ctx context.Context, input WorldInput) (World, error) {
	if s == nil || s.db == nil {
		return World{}, fmt.Errorf("revisionstore: Store is nil")
	}
	ctx, err := usableContext(ctx)
	if err != nil {
		return World{}, err
	}
	if input.WorkspaceID == "" {
		return World{}, fmt.Errorf("revisionstore: world workspace id is required")
	}
	createdAt := normalizeTime(input.CreatedAt)
	if createdAt.IsZero() {
		createdAt = nowUTC()
	}
	id := uuid.NewString()
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO worlds (id, workspace_id, name, created_at) VALUES (?, ?, ?, ?)
	`, id, input.WorkspaceID, input.Name, formatTime(createdAt)); err != nil {
		return World{}, fmt.Errorf("revisionstore: insert world: %w", err)
	}
	return s.GetWorld(ctx, id)
}

// GetWorld returns the world with id. sql.ErrNoRows is returned when no world
// exists.
func (s *Store) GetWorld(ctx context.Context, id string) (World, error) {
	if s == nil || s.db == nil {
		return World{}, fmt.Errorf("revisionstore: Store is nil")
	}
	ctx, err := usableContext(ctx)
	if err != nil {
		return World{}, err
	}
	return scanWorld(s.db.QueryRowContext(ctx, `
		SELECT id, workspace_id, name, head_revision_id, sim_time, created_at
		FROM worlds WHERE id = ?
	`, id))
}

// ListWorlds returns the worlds in workspaceID ordered by created_at then id.
func (s *Store) ListWorlds(ctx context.Context, workspaceID string) ([]World, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("revisionstore: Store is nil")
	}
	ctx, err := usableContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, workspace_id, name, head_revision_id, sim_time, created_at
		FROM worlds WHERE workspace_id = ? ORDER BY created_at, id
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("revisionstore: query worlds: %w", err)
	}
	defer rows.Close()

	var worlds []World
	for rows.Next() {
		world, err := scanWorld(rows)
		if err != nil {
			return nil, err
		}
		worlds = append(worlds, world)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("revisionstore: iterate worlds: %w", err)
	}
	return worlds, nil
}

// SetWorldHead advances worldID's head_revision_id and sim_time. It is the
// standalone head-advance for callers that advance head outside a record. An
// unknown world (0 rows affected) is an error.
func (s *Store) SetWorldHead(ctx context.Context, worldID string, revisionID int64, simTime *float64) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("revisionstore: Store is nil")
	}
	ctx, err := usableContext(ctx)
	if err != nil {
		return err
	}
	return execSetWorldHead(ctx, s.db, worldID, revisionID, simTime)
}

// execSetWorldHead runs the head-advance UPDATE via q (either *sql.DB or
// *sql.Tx) and errors when the world does not exist.
func execSetWorldHead(ctx context.Context, q sqlExec, worldID string, revisionID int64, simTime *float64) error {
	simTimeArg := sql.NullFloat64{}
	if simTime != nil {
		simTimeArg = sql.NullFloat64{Float64: *simTime, Valid: true}
	}
	result, err := q.ExecContext(ctx, `
		UPDATE worlds SET head_revision_id = ?, sim_time = ? WHERE id = ?
	`, revisionID, simTimeArg, worldID)
	if err != nil {
		return fmt.Errorf("revisionstore: set world head: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("revisionstore: set world head rows affected: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("revisionstore: set world head: unknown world %q", worldID)
	}
	return nil
}

// CreateNode inserts a node with a freshly allocated UUID id. WorldID is stored
// as SQL NULL when empty.
func (s *Store) CreateNode(ctx context.Context, input NodeInput) (Node, error) {
	if s == nil || s.db == nil {
		return Node{}, fmt.Errorf("revisionstore: Store is nil")
	}
	ctx, err := usableContext(ctx)
	if err != nil {
		return Node{}, err
	}
	if input.WorkspaceID == "" {
		return Node{}, fmt.Errorf("revisionstore: node workspace id is required")
	}
	createdAt := normalizeTime(input.CreatedAt)
	if createdAt.IsZero() {
		createdAt = nowUTC()
	}
	id := uuid.NewString()
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO nodes (id, workspace_id, world_id, node_id, run_id, status, compat_addr, drop_path, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, id, input.WorkspaceID, nullString(input.WorldID), input.NodeID, input.RunID, input.Status, input.CompatAddr, input.DropPath, formatTime(createdAt)); err != nil {
		return Node{}, fmt.Errorf("revisionstore: insert node: %w", err)
	}
	return s.GetNode(ctx, id)
}

// GetNode returns the node with id. sql.ErrNoRows is returned when no node
// exists.
func (s *Store) GetNode(ctx context.Context, id string) (Node, error) {
	if s == nil || s.db == nil {
		return Node{}, fmt.Errorf("revisionstore: Store is nil")
	}
	ctx, err := usableContext(ctx)
	if err != nil {
		return Node{}, err
	}
	return scanNode(s.db.QueryRowContext(ctx, `
		SELECT id, workspace_id, world_id, node_id, run_id, status, compat_addr, drop_path, created_at
		FROM nodes WHERE id = ?
	`, id))
}

// ListNodes returns the nodes in workspaceID ordered by created_at then id.
func (s *Store) ListNodes(ctx context.Context, workspaceID string) ([]Node, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("revisionstore: Store is nil")
	}
	ctx, err := usableContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, workspace_id, world_id, node_id, run_id, status, compat_addr, drop_path, created_at
		FROM nodes WHERE workspace_id = ? ORDER BY created_at, id
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("revisionstore: query nodes: %w", err)
	}
	defer rows.Close()

	var nodes []Node
	for rows.Next() {
		node, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("revisionstore: iterate nodes: %w", err)
	}
	return nodes, nil
}

// BindNode binds nodeID to worldID (or clears the binding when worldID is "").
// An unknown node (0 rows affected) is an error.
func (s *Store) BindNode(ctx context.Context, nodeID, worldID string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("revisionstore: Store is nil")
	}
	ctx, err := usableContext(ctx)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE nodes SET world_id = ? WHERE id = ?
	`, nullString(worldID), nodeID)
	if err != nil {
		return fmt.Errorf("revisionstore: bind node: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("revisionstore: bind node rows affected: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("revisionstore: bind node: unknown node %q", nodeID)
	}
	return nil
}

// SetNodeStatus updates nodeID's status. An unknown node (0 rows affected) is
// an error.
func (s *Store) SetNodeStatus(ctx context.Context, nodeID, status string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("revisionstore: Store is nil")
	}
	ctx, err := usableContext(ctx)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE nodes SET status = ? WHERE id = ?
	`, status, nodeID)
	if err != nil {
		return fmt.Errorf("revisionstore: set node status: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("revisionstore: set node status rows affected: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("revisionstore: set node status: unknown node %q", nodeID)
	}
	return nil
}

// IncBlobRef increments the refcount of every revision sharing sha256 (refcount
// is per content hash, shared across revisions/dedup). It returns the number of
// rows affected so the caller can detect an unknown hash.
func (s *Store) IncBlobRef(ctx context.Context, sha256 string) (int64, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("revisionstore: Store is nil")
	}
	ctx, err := usableContext(ctx)
	if err != nil {
		return 0, err
	}
	if err := validateSHA256(sha256); err != nil {
		return 0, err
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE save_revisions SET refcount = refcount + 1 WHERE sha256 = ?
	`, sha256)
	if err != nil {
		return 0, fmt.Errorf("revisionstore: inc blob ref: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("revisionstore: inc blob ref rows affected: %w", err)
	}
	return affected, nil
}

// DecBlobRef decrements the refcount of every revision sharing sha256, floored
// at 0 (the WHERE refcount > 0 guard keeps the schema CHECK (refcount >= 0)
// from ever being violated). It returns the number of rows affected.
func (s *Store) DecBlobRef(ctx context.Context, sha256 string) (int64, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("revisionstore: Store is nil")
	}
	ctx, err := usableContext(ctx)
	if err != nil {
		return 0, err
	}
	if err := validateSHA256(sha256); err != nil {
		return 0, err
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE save_revisions SET refcount = refcount - 1 WHERE sha256 = ? AND refcount > 0
	`, sha256)
	if err != nil {
		return 0, fmt.Errorf("revisionstore: dec blob ref: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("revisionstore: dec blob ref rows affected: %w", err)
	}
	return affected, nil
}

// EvictRevisionBlob demotes a revision from the 'full' tier to 'mirror_only'
// (blob_present=0) in one transaction. It refuses to evict a world head
// (ErrRevisionIsHead) or a revision whose bytes still have other live
// references (ErrBlobStillReferenced). Eviction never deletes blobstore bytes
// (G2's job) and never deletes the row (history is retained).
//
// Ref-accounting convention: this revision's own ref is counted, so a refcount
// of 1 (or 0) means no other live references remain and the bytes are
// evictable; refcount > 1 means another reference still needs them.
func (s *Store) EvictRevisionBlob(ctx context.Context, revisionID int64) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("revisionstore: Store is nil")
	}
	ctx, err := usableContext(ctx)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("revisionstore: begin evict tx: %w", err)
	}
	defer tx.Rollback()

	var refcount int64
	if err := tx.QueryRowContext(ctx, `
		SELECT refcount FROM save_revisions WHERE id = ?
	`, revisionID).Scan(&refcount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return fmt.Errorf("revisionstore: load revision for eviction: %w", err)
	}

	var isHead int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(1) FROM worlds WHERE head_revision_id = ?
	`, revisionID).Scan(&isHead); err != nil {
		return fmt.Errorf("revisionstore: check revision head: %w", err)
	}
	if isHead > 0 {
		return ErrRevisionIsHead
	}

	if refcount > 1 {
		return ErrBlobStillReferenced
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE save_revisions SET tier = 'mirror_only', blob_present = 0 WHERE id = ?
	`, revisionID); err != nil {
		return fmt.Errorf("revisionstore: evict revision blob: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("revisionstore: commit evict tx: %w", err)
	}
	return nil
}

// UnreferencedBlobs returns the distinct sha256 digests with refcount 0 that
// have no remaining blob_present=1 revision needing them. It is the GC
// candidate list G3 consumes; A2 only reports, never deletes.
func (s *Store) UnreferencedBlobs(ctx context.Context) ([]string, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("revisionstore: Store is nil")
	}
	ctx, err := usableContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT sha256 FROM save_revisions
		WHERE refcount = 0
		  AND sha256 NOT IN (SELECT sha256 FROM save_revisions WHERE blob_present = 1)
		ORDER BY sha256
	`)
	if err != nil {
		return nil, fmt.Errorf("revisionstore: query unreferenced blobs: %w", err)
	}
	defer rows.Close()

	var digests []string
	for rows.Next() {
		var digest string
		if err := rows.Scan(&digest); err != nil {
			return nil, fmt.Errorf("revisionstore: scan unreferenced blob: %w", err)
		}
		digests = append(digests, digest)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("revisionstore: iterate unreferenced blobs: %w", err)
	}
	return digests, nil
}

// PromoteRevision re-promotes a 'mirror_only' revision back to 'full'
// (blob_present=1) when its bytes reappear by hash. It is idempotent: 0 rows
// affected on an already-'full' revision is not an error.
func (s *Store) PromoteRevision(ctx context.Context, revisionID int64) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("revisionstore: Store is nil")
	}
	ctx, err := usableContext(ctx)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE save_revisions SET tier = 'full', blob_present = 1
		WHERE id = ? AND tier = 'mirror_only'
	`, revisionID); err != nil {
		return fmt.Errorf("revisionstore: promote revision: %w", err)
	}
	return nil
}

func scanWorkspace(row rowScanner) (Workspace, error) {
	var ws Workspace
	var createdAt string
	if err := row.Scan(&ws.ID, &ws.Owner, &ws.Name, &createdAt); err != nil {
		return Workspace{}, err
	}
	parsed, err := parseTime("created_at", createdAt)
	if err != nil {
		return Workspace{}, err
	}
	ws.CreatedAt = parsed
	return ws, nil
}

func scanWorld(row rowScanner) (World, error) {
	var world World
	var headRevisionID sql.NullInt64
	var simTime sql.NullFloat64
	var createdAt string
	if err := row.Scan(&world.ID, &world.WorkspaceID, &world.Name, &headRevisionID, &simTime, &createdAt); err != nil {
		return World{}, err
	}
	if headRevisionID.Valid {
		v := headRevisionID.Int64
		world.HeadRevisionID = &v
	}
	if simTime.Valid {
		v := simTime.Float64
		world.SimTime = &v
	}
	parsed, err := parseTime("created_at", createdAt)
	if err != nil {
		return World{}, err
	}
	world.CreatedAt = parsed
	return world, nil
}

func scanNode(row rowScanner) (Node, error) {
	var node Node
	var worldID sql.NullString
	var createdAt string
	if err := row.Scan(
		&node.ID,
		&node.WorkspaceID,
		&worldID,
		&node.NodeID,
		&node.RunID,
		&node.Status,
		&node.CompatAddr,
		&node.DropPath,
		&createdAt,
	); err != nil {
		return Node{}, err
	}
	if worldID.Valid {
		node.WorldID = worldID.String
	}
	parsed, err := parseTime("created_at", createdAt)
	if err != nil {
		return Node{}, err
	}
	node.CreatedAt = parsed
	return node, nil
}

func nullString(value string) sql.NullString {
	if value == "" {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: value, Valid: true}
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
		WorldID:     input.WorldID,
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
	var worldID sql.NullString
	var blobPresent int64
	var mirrorSchemaVersion sql.NullInt64
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
		&worldID,
		&revision.Tier,
		&blobPresent,
		&revision.Refcount,
		&mirrorSchemaVersion,
	); err != nil {
		return Revision{}, err
	}
	if parentID.Valid {
		revision.ParentID = &parentID.Int64
	}
	if worldID.Valid {
		revision.WorldID = worldID.String
	}
	revision.BlobPresent = blobPresent != 0
	if mirrorSchemaVersion.Valid {
		v := mirrorSchemaVersion.Int64
		revision.MirrorSchemaVersion = &v
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
