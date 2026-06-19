package workspace

// query.go — read-only query methods for the workspace analytics mirror.
//
// C4 ships three read-only entry-points:
//   - Query: SELECT over all history partitions in this workspace's DuckDB file,
//     with a prepended all-worlds world_saves CTE (save_id/world_id/sim_time) so
//     callers can JOIN world_saves for world_id attribution (M1).
//   - HistoryQuery: SELECT scoped to one world's history partitions, with a
//     prepended world_saves CTE so callers can JOIN world_saves to stay in scope.
//   - ensureReadOnly: the primary gate that rejects any non-SELECT statement
//     before it touches the shared read-write DuckDB handle.
//
// Plus a mirror_saves catalog: a native DuckDB table (not a SQLite ATTACH —
// see "Catalog: DuckDB-native" in the plan) rebuilt from the Go-side revision
// registry on every query call. The catalog maps save_id (= revision sha256) →
// world_id, tier, blob_present, sim_time so callers can JOIN mirror_saves for
// attribution.
//
// Read-only enforcement design note: the workspace owns one read-write DuckDB
// handle (workspace.go:39, openDuck:221). DuckDB single-file locking forbids a
// concurrent RW+RO handle on the same file, so a second access_mode=read_only
// handle would fail to open. Statement rejection on the RW handle (ensureReadOnly)
// is the correct design. Do NOT "fix" this into a second handle.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/asemones/bibicontrol/revisionstore"
)

// ErrReadOnlyQuery is the sentinel returned when a caller submits a non-SELECT
// statement to Query or HistoryQuery. Wrapped with the offending leading keyword
// so E1 can surface a clean Starlark diagnostic.
var ErrReadOnlyQuery = errors.New("workspace: only read-only SELECT queries are allowed")

// mirrorCatalogDDL is the CREATE TABLE statement for the mirror_saves catalog.
// mirror_saves is a native DuckDB table (rebuilt from SQLite registry rows;
// never authored in DuckDB directly).
//
// sim_time is populated from scenes.simulated_time (keyed by sha256 = save_id)
// so every revision carries its own sim_time, not just the head. save_revisions
// has no sim_time column (schema.sql:33/40-55); only worlds.sim_time holds the
// head value. Sourcing from scenes gives per-revision history sim_times suitable
// for time-series queries.
const mirrorCatalogDDL = `
CREATE TABLE IF NOT EXISTS mirror_saves (
	save_id      TEXT,
	world_id     TEXT,
	tier         TEXT,
	blob_present BOOLEAN,
	sim_time     DOUBLE
)`

// catalogInsertChunk is the number of mirror_saves rows packed into one
// multi-row INSERT. Each row binds 5 parameters, so a chunk binds
// catalogInsertChunk*5 = 2500 parameters — comfortably below the driver's
// bound-parameter ceiling even as retained history (R) grows over the system's
// life. Chunking keeps the rebuild at O(R/chunk) INSERT round-trips, never the
// O(R) per-revision pattern that was the original N+1.
const catalogInsertChunk = 500

// refreshMirrorCatalog brings the mirror_saves catalog current with the
// registry. Must be called under w.mu (the caller locks/unlocks); the cache
// fields it reads/writes are therefore unsynchronized by design.
//
// It is fingerprint-gated: it computes a cheap CatalogFingerprint (one SQLite
// aggregate) and, if the catalog has been built at least once and the
// fingerprint is unchanged, returns immediately with NO DuckDB work — this is
// the cache hit that kills rebuild-on-every-query (C4b). The fingerprint covers
// new revisions (Count/MaxID) AND in-place tier/blob_present flips (StateSum),
// so every catalog-affecting mutation moves it for free without per-mutator
// invalidation hooks.
//
// When a rebuild IS needed it is O(1) registry/DuckDB round-trips plus work
// linear in row count inside chunked statements, not O(R) round-trips:
//   - one RevisionsForWorkspace JOIN (replaces ListWorlds + per-world loop),
//   - one set-based scenes read into a map (replaces the per-revision SELECT),
//   - chunked multi-row INSERTs (replaces the per-revision INSERT).
//
// The cache fields (w.catalogFP/w.catalogBuilt) are written ONLY after the
// DuckDB COMMIT succeeds, so a failed/rolled-back rebuild never poisons the
// cache into thinking it is current.
func (w *Workspace) refreshMirrorCatalog(ctx context.Context) error {
	db := w.duck()
	if db == nil {
		return fmt.Errorf("workspace: duckdb handle is nil")
	}

	// Fingerprint gate: skip the rebuild entirely when nothing catalog-affecting
	// has changed since the last successful build. The very first build (or a
	// brand-new empty workspace, whose zero fingerprint must NOT read as a hit)
	// is forced by w.catalogBuilt being false.
	fp, err := w.store().CatalogFingerprint(ctx, w.ID())
	if err != nil {
		return fmt.Errorf("workspace: catalog fingerprint: %w", err)
	}
	if w.catalogBuilt && fp == w.catalogFP {
		return nil
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("workspace: get duckdb conn for catalog refresh: %w", err)
	}
	defer conn.Close()

	// Ensure the table exists.
	if _, err := conn.ExecContext(ctx, mirrorCatalogDDL); err != nil {
		return fmt.Errorf("workspace: create mirror_saves table: %w", err)
	}

	// One workspace-scoped revision read (replaces ListWorlds + per-world
	// RevisionsForWorld). This is sha256-keyed history only; the world-id working
	// key never appears, preserving the dual-key mirror semantics.
	revs, err := w.store().RevisionsForWorkspace(ctx, w.ID())
	if err != nil {
		return fmt.Errorf("workspace: revisions for workspace catalog refresh: %w", err)
	}

	// One set-based scenes read for every per-revision sim_time (replaces the
	// per-revision SELECT — the N+1's DuckDB half). The WHERE has_simulated_time
	// predicate matches the old per-revision filter, so a revision absent from
	// the map (no scene row OR has_simulated_time = false) inserts SQL NULL via
	// map-miss, reproducing the old *float64 nil semantics exactly.
	simByID, err := w.readSceneSimTimes(ctx, conn)
	if err != nil {
		return err
	}

	// Wrap the full rebuild in one transaction so the catalog is never
	// half-rebuilt (mirrors the ReplaceExtractedSave transaction shape,
	// import.go:97-122).
	if _, err := conn.ExecContext(ctx, "BEGIN TRANSACTION"); err != nil {
		return fmt.Errorf("workspace: begin catalog refresh tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	// Clear the existing catalog.
	if _, err := conn.ExecContext(ctx, "DELETE FROM mirror_saves"); err != nil {
		return fmt.Errorf("workspace: clear mirror_saves: %w", err)
	}

	// Chunked multi-row INSERT (replaces the per-revision INSERT — the N+1's
	// other DuckDB half). Each chunk is one ExecContext binding chunkRows*5
	// parameters, kept below the driver's parameter ceiling by catalogInsertChunk.
	for start := 0; start < len(revs); start += catalogInsertChunk {
		end := start + catalogInsertChunk
		if end > len(revs) {
			end = len(revs)
		}
		chunk := revs[start:end]

		placeholders := make([]string, 0, len(chunk))
		args := make([]any, 0, len(chunk)*5)
		for _, rev := range chunk {
			placeholders = append(placeholders, "(?, ?, ?, ?, ?)")
			// map-miss -> nil -> SQL NULL (NULL sim_time fidelity).
			var simTime any
			if v, ok := simByID[rev.SHA256]; ok {
				simTime = v
			}
			args = append(args, rev.SHA256, rev.WorldID, rev.Tier, rev.BlobPresent, simTime)
		}

		stmt := "INSERT INTO mirror_saves (save_id, world_id, tier, blob_present, sim_time) VALUES " +
			strings.Join(placeholders, ", ")
		if _, err := conn.ExecContext(ctx, stmt, args...); err != nil {
			return fmt.Errorf("workspace: batch insert mirror_saves rows: %w", err)
		}
		w.insertExecCount++ // test-only perf-shape seam
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("workspace: commit catalog refresh: %w", err)
	}
	committed = true

	// Cache the fingerprint ONLY after a successful COMMIT so a failed rebuild
	// never marks itself current.
	w.catalogFP = fp
	w.catalogBuilt = true
	w.rebuildCount++ // test-only seam: bumped once per actual rebuild, never on a cache hit
	return nil
}

// readSceneSimTimes reads every per-revision sim_time in ONE set-based DuckDB
// query into a map keyed by save_id (= revision sha256). The WHERE
// has_simulated_time predicate matches the catalog's old per-revision filter, so
// a save_id absent from the returned map (no scene row or has_simulated_time
// false) maps to SQL NULL at insert time — preserving the *float64 nil
// semantics. It increments the test-only scenesReadCount seam exactly once so
// tests can prove there is no per-revision scenes loop.
func (w *Workspace) readSceneSimTimes(ctx context.Context, conn *sql.Conn) (map[string]float64, error) {
	w.scenesReadCount++ // test-only perf-shape seam
	rows, err := conn.QueryContext(ctx,
		"SELECT save_id, simulated_time FROM scenes WHERE has_simulated_time")
	if err != nil {
		return nil, fmt.Errorf("workspace: read scene sim_times: %w", err)
	}
	defer rows.Close()

	simByID := make(map[string]float64)
	for rows.Next() {
		var saveID string
		var simTime float64
		if err := rows.Scan(&saveID, &simTime); err != nil {
			return nil, fmt.Errorf("workspace: scan scene sim_time: %w", err)
		}
		simByID[saveID] = simTime
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workspace: iterate scene sim_times: %w", err)
	}
	return simByID, nil
}

// Query runs a read-only SELECT over all history partitions in this workspace's
// DuckDB file (every world, no world filter). The caller can JOIN mirror_saves
// for world_id / sim_time / tier attribution. No multi-workspace federation.
//
// The query is validated by ensureReadOnly before touching the database. Any
// non-SELECT statement (including chained statements and leading-comment bypasses)
// is rejected with ErrReadOnlyQuery.
//
// world_id attribution (M1): like HistoryQuery, the user query is wrapped with a
// prepended CTE
//
//	WITH world_saves AS (SELECT save_id, world_id, sim_time FROM mirror_saves) <query>
//
// so an all-worlds caller can `JOIN world_saves USING (save_id)` for automatic
// world_id / sim_time attribution WITHOUT hand-writing the mirror_saves JOIN. The
// wrapper is prepend-only — a leading WITH that ensureReadOnly already accepts — and
// runs AFTER ensureReadOnly so the gate sees the user's verbatim query, not the
// wrapped form. The catalog is refreshed before the wrap so world_saves is current.
// world_saves matches the name HistoryQuery uses (the all-worlds variant carries the
// extra world_id/sim_time columns for attribution); a user CTE of the same name would
// shadow it (pre-existing behavior, not new). The raw mirror_saves JOIN still works
// for callers that prefer it.
func (w *Workspace) Query(ctx context.Context, query string) ([]map[string]any, error) {
	if err := ensureReadOnly(query); err != nil {
		return nil, err
	}

	w.mu.Lock()
	if err := w.refreshMirrorCatalog(ctx); err != nil {
		w.mu.Unlock()
		return nil, fmt.Errorf("workspace: refresh mirror catalog: %w", err)
	}
	w.mu.Unlock()

	// Prepend-only wrapper (mirrors HistoryQuery, query.go): world_saves is added as a
	// leading WITH so a caller can JOIN world_saves USING (save_id) without writing the
	// mirror_saves JOIN. A query that already opens with its own WITH is the
	// power-user/raw escape-hatch case — it keeps using the explicit `JOIN mirror_saves`
	// form (the wrapper is the convenience path, not a CTE rewriter; see the
	// HistoryQuery precedent). ensureReadOnly has already validated the verbatim query.
	wrapped := "WITH world_saves AS (SELECT save_id, world_id, sim_time FROM mirror_saves) " + query

	rows, err := w.duck().QueryContext(ctx, wrapped)
	if err != nil {
		return nil, fmt.Errorf("workspace: query: %w", err)
	}
	defer rows.Close()
	return scanRowsToMaps(rows)
}

// HistoryQuery runs a read-only SELECT over the retained per-revision history
// partitions of one world (the partitions keyed by revision sha256). The user
// query is wrapped with a prepended CTE:
//
//	WITH world_saves AS (SELECT save_id FROM mirror_saves WHERE world_id = ?)
//
// so callers can JOIN world_saves USING (save_id) to scope results to this
// world's revisions. The CTE ensures the world scope is enforced structurally
// rather than by caller convention.
//
// worldID must be a valid, known world. The working partition (keyed by world id,
// not sha256) is excluded by the catalog join: mirror_saves only carries sha256
// revision keys (history keys), never the world-id working key.
func (w *Workspace) HistoryQuery(ctx context.Context, worldID, query string) ([]map[string]any, error) {
	if worldID == "" {
		return nil, fmt.Errorf("workspace: worldID is required")
	}
	if err := ensureReadOnly(query); err != nil {
		return nil, err
	}

	// Confirm the world exists; surface a clean not-found error.
	if _, err := w.store().GetWorld(ctx, worldID); err != nil {
		if revisionstore.IsNotFound(err) {
			return nil, fmt.Errorf("workspace: world %q not found: %w", worldID, err)
		}
		return nil, fmt.Errorf("workspace: get world %q: %w", worldID, err)
	}

	// Refresh the catalog under the mutex (write to DuckDB), then release so
	// the following SELECT does not serialize reads against C2/D2 mutators.
	w.mu.Lock()
	if err := w.refreshMirrorCatalog(ctx); err != nil {
		w.mu.Unlock()
		return nil, fmt.Errorf("workspace: refresh mirror catalog: %w", err)
	}
	w.mu.Unlock()

	// Prepend the world_saves CTE so callers can JOIN world_saves USING (save_id)
	// to scope to this world's history revisions. The worldID is a bound parameter
	// to prevent injection.
	wrapped := "WITH world_saves AS (SELECT save_id FROM mirror_saves WHERE world_id = ?) " + query

	rows, err := w.duck().QueryContext(ctx, wrapped, worldID)
	if err != nil {
		return nil, fmt.Errorf("workspace: history query: %w", err)
	}
	defer rows.Close()
	return scanRowsToMaps(rows)
}

// forbiddenVerbs are the SQL keywords that, if they appear as a bare token
// anywhere in the statement (outside quoted literals/identifiers and comments),
// indicate a mutating or otherwise non-read-only operation. The scan is
// depth-AGNOSTIC: a verb inside a CTE body still counts. This is deliberately
// broader than a depth-0-only check because DuckDB executes CTE-wrapped
// mutations — both the tail form `WITH x AS (SELECT 1) DELETE FROM t` and the
// CTE-body form `WITH x AS (DELETE FROM t RETURNING *) SELECT ...` mutate the
// database. A SELECT can never legitimately need any of these bare keywords, so
// rejecting them whenever they appear (not just leading) closes the bypass
// without false-positives on real read-only queries. (Bare-token matching means
// a column or string literal containing these words — e.g. a value 'DELETE' or a
// quoted identifier "delete" — is not flagged, because those are skipped by the
// literal/identifier handling in the scanner.)
var forbiddenVerbs = map[string]bool{
	"INSERT": true, "UPDATE": true, "DELETE": true, "CREATE": true,
	"DROP": true, "ALTER": true, "ATTACH": true, "DETACH": true,
	"COPY": true, "PRAGMA": true, "INSTALL": true, "LOAD": true,
	"SET": true, "CALL": true, "EXPORT": true, "IMPORT": true,
	"BEGIN": true, "COMMIT": true, "ROLLBACK": true, "VACUUM": true,
	"CHECKPOINT": true, "TRUNCATE": true, "REPLACE": true, "MERGE": true,
	"UPSERT": true,
}

// ensureReadOnly validates that query is a read-only SELECT (or WITH…SELECT).
// This is the primary gate protecting the shared read-write DuckDB handle; see
// design note at top of file.
//
// Three checks, all literal/identifier/comment-aware:
//  1. The first significant token must be SELECT or WITH (DuckDB accepts a
//     leading WITH RECURSIVE too, which still begins with the WITH token).
//  2. No chained statement: a ';' may only be followed by whitespace/comments.
//  3. No forbidden verb appears as a bare token ANYWHERE in the statement. This
//     is what closes the CTE-wrapped-mutation bypass — a leading WITH no longer
//     waves through `WITH x AS (SELECT 1) DELETE FROM t`, because DELETE is a
//     bare top-level token. A real WITH…SELECT carries none of these verbs.
//
// Design note (M1): the bare-keyword blocklist deliberately ERRS SAFE. A read-only
// query that happens to contain a forbidden word as a bare token in a position the
// scanner cannot prove is benign (e.g. an unquoted identifier that collides with a
// verb) is rejected rather than risk waving a mutation through. The literal/identifier/
// comment-aware scan keeps the realistic false-positive rate near zero (verbs inside
// strings/quoted identifiers are skipped), and the caller can always double-quote a
// colliding identifier. This is an intentional, documented trade — NOT a bug to "fix"
// by rewriting the keyword scanner into a full SQL parser (out of scope, high-risk).
func ensureReadOnly(query string) error {
	tok, _ := nextToken(query)
	upper := strings.ToUpper(tok)

	switch upper {
	case "SELECT", "WITH":
		// Leading keyword is acceptable; fall through to the deeper scans.
	case "":
		// Empty or all-whitespace/comments.
		return fmt.Errorf("%w: empty query", ErrReadOnlyQuery)
	default:
		return fmt.Errorf("%w: statement begins with %q", ErrReadOnlyQuery, tok)
	}

	// Reject any forbidden bare verb (incl. CTE-wrapped mutations) and any
	// chained statement. scanForbidden walks the whole string once,
	// comment/literal-aware.
	return scanForbidden(query)
}

// scanForbidden walks the entire statement once, skipping single-quoted strings,
// double-quoted identifiers, line comments (--) and block comments (/* */). It
// returns ErrReadOnlyQuery if (a) any bare alpha token equals a forbidden verb,
// or (b) a ';' is followed by any further significant (non-comment, non-ws)
// content (a chained statement). Bare-token matching ensures a forbidden word
// inside a literal or quoted identifier is not flagged.
func scanForbidden(s string) error {
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == '\'':
			// Single-quoted string literal: skip to closing quote ('' escapes).
			i++
			for i < len(s) {
				if s[i] == '\'' {
					i++
					if i < len(s) && s[i] == '\'' {
						i++
						continue
					}
					break
				}
				i++
			}
		case c == '"':
			// Double-quoted identifier: skip to closing quote ("" escapes).
			i++
			for i < len(s) {
				if s[i] == '"' {
					i++
					if i < len(s) && s[i] == '"' {
						i++
						continue
					}
					break
				}
				i++
			}
		case c == '-' && i+1 < len(s) && s[i+1] == '-':
			// Line comment: skip to end of line.
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			// Block comment: skip to */.
			i += 2
			for i+1 < len(s) && !(s[i] == '*' && s[i+1] == '/') {
				i++
			}
			if i+1 < len(s) {
				i += 2
			}
		case c == ';':
			// Chained statement: reject if any significant token follows.
			tok, _ := nextToken(s[i+1:])
			if tok != "" {
				return fmt.Errorf("%w: chained statement after ';'", ErrReadOnlyQuery)
			}
			i++
		case isAlpha(c):
			// Collect a bare identifier/keyword token and check it.
			j := i
			for j < len(s) && isAlphaNum(s[j]) {
				j++
			}
			word := s[i:j]
			if forbiddenVerbs[strings.ToUpper(word)] {
				return fmt.Errorf("%w: statement contains forbidden keyword %q", ErrReadOnlyQuery, word)
			}
			i = j
		default:
			i++
		}
	}
	return nil
}

// nextToken skips leading whitespace and SQL comments (-- and /* … */) and
// returns the first bare identifier/keyword token and the remainder of the string.
func nextToken(s string) (tok, rest string) {
	i := 0
	for i < len(s) {
		// Skip whitespace.
		if s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r' {
			i++
			continue
		}
		// Skip line comment.
		if i+1 < len(s) && s[i] == '-' && s[i+1] == '-' {
			for i < len(s) && s[i] != '\n' {
				i++
			}
			continue
		}
		// Skip block comment.
		if i+1 < len(s) && s[i] == '/' && s[i+1] == '*' {
			i += 2
			for i+1 < len(s) && !(s[i] == '*' && s[i+1] == '/') {
				i++
			}
			if i+1 < len(s) {
				i += 2 // consume */
			}
			continue
		}
		// Reached the first non-comment, non-whitespace byte. Collect an
		// identifier/keyword token (letters and underscores only; stop at anything
		// else so e.g. "SELECT(" is split into "SELECT" + "(…").
		if isAlpha(s[i]) {
			j := i
			for j < len(s) && isAlphaNum(s[j]) {
				j++
			}
			return s[i:j], s[j:]
		}
		// Non-alpha first byte (e.g. '(' or digit): return empty to signal no
		// recognizable keyword.
		return "", s[i:]
	}
	return "", ""
}

func isAlpha(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

func isAlphaNum(c byte) bool {
	return isAlpha(c) || (c >= '0' && c <= '9')
}

// scanRowsToMaps iterates rows and returns one map[string]any per row with
// column names as keys and driver scalar values as values. It is the Go-API
// analogue of script/thebibites/sql.go:scanRowsToDicts; E1 owns the Starlark
// conversion via the existing fromSQLValue/scanRowsToDicts path.
func scanRowsToMaps(rows *sql.Rows) ([]map[string]any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("workspace: columns: %w", err)
	}

	values := make([]any, len(cols))
	targets := make([]any, len(cols))
	for i := range values {
		targets[i] = &values[i]
	}

	var out []map[string]any
	for rows.Next() {
		for i := range values {
			values[i] = nil
		}
		if err := rows.Scan(targets...); err != nil {
			return nil, fmt.Errorf("workspace: scan row: %w", err)
		}
		m := make(map[string]any, len(cols))
		for i, name := range cols {
			m[name] = values[i]
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workspace: rows error: %w", err)
	}
	return out, nil
}
