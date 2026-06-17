PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS script_runs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	script_sha256 TEXT NOT NULL,
	started_at TEXT NOT NULL,
	finished_at TEXT NULL,
	status TEXT NOT NULL,
	error TEXT NOT NULL DEFAULT '',
	output TEXT NOT NULL DEFAULT '',
	staged_ops INTEGER NOT NULL DEFAULT 0 CHECK (staged_ops >= 0),
	dry_run INTEGER NOT NULL DEFAULT 0 CHECK (dry_run IN (0, 1))
);

CREATE INDEX IF NOT EXISTS script_runs_script_sha256_idx
	ON script_runs(script_sha256);

CREATE INDEX IF NOT EXISTS script_runs_started_at_idx
	ON script_runs(started_at);

CREATE TABLE IF NOT EXISTS workspaces (
	id TEXT PRIMARY KEY,
	owner TEXT NOT NULL DEFAULT '',
	name TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS worlds (
	id TEXT PRIMARY KEY,
	workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE RESTRICT,
	name TEXT NOT NULL DEFAULT '',
	head_revision_id INTEGER NULL REFERENCES save_revisions(id) ON DELETE RESTRICT,
	sim_time REAL NULL,
	created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS worlds_workspace_id_idx
	ON worlds(workspace_id);

CREATE TABLE IF NOT EXISTS save_revisions (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	sha256 TEXT NOT NULL,
	size INTEGER NOT NULL CHECK (size >= 0),
	parent_id INTEGER NULL REFERENCES save_revisions(id) ON DELETE RESTRICT,
	source_path TEXT NOT NULL DEFAULT '',
	blob_ref TEXT NOT NULL,
	inline_blob BLOB NULL,
	script_run_id INTEGER NOT NULL REFERENCES script_runs(id) ON DELETE RESTRICT,
	created_at TEXT NOT NULL,
	world_id TEXT NULL REFERENCES worlds(id) ON DELETE RESTRICT,
	tier TEXT NOT NULL DEFAULT 'full' CHECK (tier IN ('full', 'mirror_only')),
	blob_present INTEGER NOT NULL DEFAULT 1 CHECK (blob_present IN (0, 1)),
	refcount INTEGER NOT NULL DEFAULT 0 CHECK (refcount >= 0),
	mirror_schema_version INTEGER NULL
);

CREATE INDEX IF NOT EXISTS save_revisions_sha256_idx
	ON save_revisions(sha256);

CREATE INDEX IF NOT EXISTS save_revisions_parent_id_idx
	ON save_revisions(parent_id);

CREATE INDEX IF NOT EXISTS save_revisions_script_run_id_idx
	ON save_revisions(script_run_id);

CREATE INDEX IF NOT EXISTS save_revisions_world_id_idx
	ON save_revisions(world_id);

CREATE TABLE IF NOT EXISTS nodes (
	id TEXT PRIMARY KEY,
	workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE RESTRICT,
	world_id TEXT NULL REFERENCES worlds(id) ON DELETE SET NULL,
	node_id TEXT NOT NULL DEFAULT '',
	run_id TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT '',
	compat_addr TEXT NOT NULL DEFAULT '',
	drop_path TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS nodes_workspace_id_idx
	ON nodes(workspace_id);

CREATE INDEX IF NOT EXISTS nodes_world_id_idx
	ON nodes(world_id);
