package workspace

// query.go — read-only query methods for the workspace analytics mirror.
//
// C4 ships three read-only entry-points:
//   - Query: SELECT over all history partitions in this workspace's DuckDB file.
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
// mirror_saves is a native DuckDB table (rebuilt from SQLite registry rows on
// every query call; never authored in DuckDB directly). The rebuild-on-every-query
// strategy guarantees correctness after a concurrent G2 eviction flips a revision's
// tier or blob_present without requiring a change notification bus.
//
// sim_time is populated via a sub-select from scenes.simulated_time (keyed by
// sha256 = save_id) so every revision carries its own sim_time, not just the head.
// save_revisions has no sim_time column (schema.sql:33/40-55); only worlds.sim_time
// holds the head value. Sourcing from scenes gives per-revision history sim_times
// suitable for time-series queries.
const mirrorCatalogDDL = `
CREATE TABLE IF NOT EXISTS mirror_saves (
	save_id      TEXT,
	world_id     TEXT,
	tier         TEXT,
	blob_present BOOLEAN,
	sim_time     DOUBLE
)`

// refreshMirrorCatalog rebuilds the mirror_saves catalog from the registry.
// Must be called under w.mu. The caller is responsible for locking/unlocking.
func (w *Workspace) refreshMirrorCatalog(ctx context.Context) error {
	db := w.duck()
	if db == nil {
		return fmt.Errorf("workspace: duckdb handle is nil")
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

	// Enumerate every world in this workspace and insert one row per revision.
	worlds, err := w.store().ListWorlds(ctx, w.ID())
	if err != nil {
		return fmt.Errorf("workspace: list worlds for catalog refresh: %w", err)
	}

	for _, world := range worlds {
		revisions, err := w.store().RevisionsForWorld(ctx, world.ID)
		if err != nil {
			return fmt.Errorf("workspace: list revisions for world %q: %w", world.ID, err)
		}
		for _, rev := range revisions {
			// Per-revision sim_time: source from scenes.simulated_time keyed by
			// the history sha256 (= save_id). This gives the actual sim_time at
			// that snapshot, not the head's current value. NULL when the revision's
			// scene row has has_simulated_time = false or no scenes row exists.
			//
			// We use a sub-select rather than a Go-side lookup because scenes is
			// a DuckDB table — the value is already there from ImportExtractedSave.
			var simTime *float64
			simErr := conn.QueryRowContext(ctx,
				"SELECT simulated_time FROM scenes WHERE save_id = ? AND has_simulated_time LIMIT 1",
				rev.SHA256,
			).Scan(&simTime)
			if simErr != nil && !errors.Is(simErr, sql.ErrNoRows) {
				return fmt.Errorf("workspace: read sim_time for revision %q: %w", rev.SHA256, simErr)
			}
			// simTime remains nil when ErrNoRows (no scene or time absent).

			_, err := conn.ExecContext(ctx,
				"INSERT INTO mirror_saves (save_id, world_id, tier, blob_present, sim_time) VALUES (?, ?, ?, ?, ?)",
				rev.SHA256, rev.WorldID, rev.Tier, rev.BlobPresent, simTime,
			)
			if err != nil {
				return fmt.Errorf("workspace: insert mirror_saves row for revision %q: %w", rev.SHA256, err)
			}
		}
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("workspace: commit catalog refresh: %w", err)
	}
	committed = true
	return nil
}

// Query runs a read-only SELECT over all history partitions in this workspace's
// DuckDB file (every world, no world filter). The caller can JOIN mirror_saves
// for world_id / sim_time / tier attribution. No multi-workspace federation.
//
// The query is validated by ensureReadOnly before touching the database. Any
// non-SELECT statement (including chained statements and leading-comment bypasses)
// is rejected with ErrReadOnlyQuery.
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

	rows, err := w.duck().QueryContext(ctx, query)
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

// ensureReadOnly validates that query is a read-only SELECT (or WITH…SELECT).
// It rejects any non-SELECT leading keyword and any chained statement (a second
// statement after a ';'). This is the primary gate protecting the shared
// read-write DuckDB handle; see design note at top of file.
//
// Accept: SELECT …, WITH … SELECT …, trailing semicolon.
// Reject: INSERT, UPDATE, DELETE, DROP, CREATE, ALTER, ATTACH, DETACH, COPY,
// PRAGMA, INSTALL, LOAD, SET, CALL, EXPORT, IMPORT, BEGIN, COMMIT, ROLLBACK,
// VACUUM, CHECKPOINT, TRUNCATE, REPLACE, and any chained statement.
func ensureReadOnly(query string) error {
	tok, rest := nextToken(query)
	upper := strings.ToUpper(tok)

	switch upper {
	case "SELECT":
		// Plain SELECT: reject if a second statement follows.
		return rejectChained(rest)
	case "WITH":
		// WITH … (SELECT | recursive CTE): scan forward to the closing paren of
		// all CTEs then expect SELECT. We accept any WITH because the CTE body
		// cannot be a mutation when the outer statement is SELECT — we trust the
		// DB to enforce that. Reject chained statements after the full query.
		return rejectChained(rest)
	case "":
		// Empty or all-whitespace/comments.
		return fmt.Errorf("%w: empty query", ErrReadOnlyQuery)
	default:
		return fmt.Errorf("%w: statement begins with %q", ErrReadOnlyQuery, tok)
	}
}

// rejectChained returns ErrReadOnlyQuery if s (the remainder after the leading
// keyword) contains a second statement — i.e. a ';' followed by any further
// non-comment, non-whitespace token.
func rejectChained(s string) error {
	// Scan through s looking for an unquoted ';'. If found, check whether
	// anything meaningful follows.
	i := 0
	for i < len(s) {
		c := s[i]
		switch c {
		case '\'':
			// Single-quoted string: skip to closing quote.
			i++
			for i < len(s) {
				if s[i] == '\'' {
					i++
					if i < len(s) && s[i] == '\'' {
						// Escaped quote ''
						i++
						continue
					}
					break
				}
				i++
			}
		case '"':
			// Double-quoted identifier: skip to closing quote.
			i++
			for i < len(s) {
				if s[i] == '"' {
					i++
					if i < len(s) && s[i] == '"' {
						// Escaped quote ""
						i++
						continue
					}
					break
				}
				i++
			}
		case '-':
			// Line comment: skip rest of line.
			if i+1 < len(s) && s[i+1] == '-' {
				for i < len(s) && s[i] != '\n' {
					i++
				}
				continue
			}
			i++
		case '/':
			// Block comment: skip to */.
			if i+1 < len(s) && s[i+1] == '*' {
				i += 2
				for i+1 < len(s) && !(s[i] == '*' && s[i+1] == '/') {
					i++
				}
				if i+1 < len(s) {
					i += 2 // skip */
				}
				continue
			}
			i++
		case ';':
			// Semicolon found: check if a meaningful token follows.
			rest := s[i+1:]
			tok, _ := nextToken(rest)
			if tok != "" {
				return fmt.Errorf("%w: chained statement after ';'", ErrReadOnlyQuery)
			}
			return nil
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
