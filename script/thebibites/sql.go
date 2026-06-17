package thebibites

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"go.starlark.net/starlark"

	"github.com/asemones/bibicontrol/duckdb"
)

// sql.go is the T5 analytics path: the raw save.sql escape hatch plus the
// push-down query builder that compiles collection narrowing + aggregates into
// DuckDB SELECTs. Nothing here materializes Entity values — computation runs in
// DuckDB and only scalars/dicts come back.

// quoteIdent double-quotes a SQL identifier for DuckDB, escaping embedded quotes.
// Table and column names come from generated metadata (safe), but quoting keeps
// the builder correct for any identifier and case-exact against the migration's
// lowercase column names.
func quoteIdent(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

// identityTable returns the identity (one-row-per-entity) table backing a kind.
func identityTable(kind string) (string, error) {
	tables := entityTables[kind]
	if len(tables) == 0 {
		return "", fmt.Errorf("unknown entity kind %q", kind)
	}
	return tables[0], nil
}

// query opens DuckDB lazily, flushes any pending mutation mirror (no-op in T5),
// and runs a query. flushMirror sits at the head of every query so a later T6
// read-after-write observes staged sets without a reparse.
func (ls *LoadedSave) query(q string, args ...any) (*sql.Rows, error) {
	ctx := ls.queryCtx()
	db, err := ls.openDB(ctx)
	if err != nil {
		return nil, err
	}
	if err := ls.flushMirror(ctx); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	return rows, nil
}

// sqlBuiltin implements save.sql(query) -> list[dict]. The raw escape hatch: the
// caller writes literal DuckDB SQL against the normalized tables; no friendly
// name resolution happens here.
func (s *Save) sqlBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var query string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "query", &query); err != nil {
		return nil, err
	}
	rows, err := s.ls.query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRowsToDicts(rows)
}

// scanRowsToDicts materializes a result set as a Starlark list of dicts (one per
// row, column name -> value), preserving column order. Driver scalars are
// converted by fromSQLValue (SQL NULL -> None).
func scanRowsToDicts(rows *sql.Rows) (*starlark.List, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	values := make([]any, len(cols))
	targets := make([]any, len(cols))
	for i := range values {
		targets[i] = &values[i]
	}

	var items []starlark.Value
	for rows.Next() {
		for i := range values {
			values[i] = nil
		}
		if err := rows.Scan(targets...); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		d := starlark.NewDict(len(cols))
		for i, name := range cols {
			v, err := fromSQLValue(values[i])
			if err != nil {
				return nil, fmt.Errorf("column %q: %w", name, err)
			}
			if err := d.SetKey(starlark.String(name), v); err != nil {
				return nil, err
			}
		}
		items = append(items, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return starlark.NewList(items), nil
}

// aggCall describes one aggregate request from a collection method. fn is the
// friendly aggregate name; col is the friendly column (empty for count); q is the
// fraction for quantile.
type aggCall struct {
	fn  string
	col string
	q   float64
}

// resolveColumn maps a friendly column name to its qualified DuckDB reference
// "<table>"."<column>" via the generated-metadata registry — the same resolution
// table that powers reads. It returns the owning table so the caller can JOIN it.
// Unknown column -> clean diagnostic. The friendly name (the registry key, which
// may be an alias) resolves to spec.sourceColumn — the real generated DuckDB
// column — so an aliased column queries the column that actually exists.
func resolveColumn(kind, col string) (qualified, table string, err error) {
	spec, ok := attrRegistry()[kind][col]
	if !ok {
		return "", "", fmt.Errorf("unknown column %q for %s", col, kind)
	}
	return quoteIdent(spec.table) + "." + quoteIdent(spec.sourceColumn), spec.table, nil
}

// aggExpr builds the SQL aggregate expression and reports the sub-table (if any)
// it references so the caller can include it in the FROM joins.
func aggExpr(kind string, agg aggCall) (expr, table string, err error) {
	if agg.fn == "count" {
		return "count(*)", "", nil
	}
	qualified, table, err := resolveColumn(kind, agg.col)
	if err != nil {
		return "", "", err
	}
	switch agg.fn {
	case "sum":
		expr = "sum(" + qualified + ")"
	case "mean":
		expr = "avg(" + qualified + ")"
	case "median":
		expr = "median(" + qualified + ")"
	case "min":
		expr = "min(" + qualified + ")"
	case "max":
		expr = "max(" + qualified + ")"
	case "quantile":
		expr = "quantile_cont(" + qualified + ", " + strconv.FormatFloat(agg.q, 'g', -1, 64) + ")"
	default:
		return "", "", fmt.Errorf("unknown aggregate %q", agg.fn)
	}
	return expr, table, nil
}

// oneToManyTables is the set of tables registered as 1:many element
// sub-collections (brain nodes/synapses, stomach contents) across all entity
// kinds. fromClause asserts no such table is scalar-joined: a LEFT JOIN against a
// 1:many table multiplies identity rows and silently skews every count/sum/median.
// Today entityTables (1:1) and entitySubCollections (1:many) are disjoint by
// design; this turns a future save-format revision that lists a 1:many table in
// entityTables into a loud, localized error instead of wrong analytics.
func oneToManyTables() map[string]bool {
	out := map[string]bool{}
	for _, subs := range entitySubCollections {
		for _, info := range subs {
			out[info.table] = true
		}
	}
	return out
}

// fromClause builds "FROM <identity> [LEFT JOIN <subtable> ON …]" including only
// the sub-tables actually referenced (in entityTables order, for determinism).
// Every sub-table is 1:1 with identity on (save_id, entry_name), so LEFT JOIN
// preserves cardinality and aggregates stay correct.
func fromClause(kind string, needed map[string]bool) (string, error) {
	tables := entityTables[kind]
	if len(tables) == 0 {
		return "", fmt.Errorf("unknown entity kind %q", kind)
	}
	oneToMany := oneToManyTables()
	identity := tables[0]
	var b strings.Builder
	b.WriteString("FROM ")
	b.WriteString(quoteIdent(identity))
	for _, t := range tables[1:] {
		if !needed[t] {
			continue
		}
		if oneToMany[t] {
			return "", fmt.Errorf("internal: refusing to scalar-join 1:many table %q for %s "+
				"(would inflate aggregate rows); it must be exposed as an element sub-collection, not a scalar attribute table", t, kind)
		}
		b.WriteString(" LEFT JOIN ")
		b.WriteString(quoteIdent(t))
		b.WriteString(" ON ")
		b.WriteString(quoteIdent(identity) + ".save_id = " + quoteIdent(t) + ".save_id")
		b.WriteString(" AND ")
		b.WriteString(quoteIdent(identity) + ".entry_name = " + quoteIdent(t) + ".entry_name")
	}
	return b.String(), nil
}

// whereClause builds "WHERE <identity>.save_id = ? [AND (<predicate>)]" and its
// args. The predicate is already friendly-column-rewritten.
func (ls *LoadedSave) whereClause(kind, predicate string) (string, []any, error) {
	identity, err := identityTable(kind)
	if err != nil {
		return "", nil, err
	}
	clause := "WHERE " + quoteIdent(identity) + ".save_id = ?"
	args := []any{ls.saveID}
	if strings.TrimSpace(predicate) != "" {
		clause += " AND (" + predicate + ")"
	}
	return clause, args, nil
}

// combineWhere AND-combines two raw predicates (either may be empty).
func combineWhere(existing, add string) string {
	switch {
	case existing == "":
		return add
	case add == "":
		return existing
	default:
		return "(" + existing + ") AND (" + add + ")"
	}
}

// scalarAgg compiles and runs a single-scalar aggregate over a (possibly
// narrowed) collection, returning the scalar Starlark value (None for SQL NULL,
// e.g. an aggregate over the empty set).
func (ls *LoadedSave) scalarAgg(kind, where string, agg aggCall) (starlark.Value, error) {
	needed := map[string]bool{}
	expr, aggTable, err := aggExpr(kind, agg)
	if err != nil {
		return nil, err
	}
	if aggTable != "" {
		needed[aggTable] = true
	}
	predicate, predTables, err := ls.rewritePredicate(kind, where)
	if err != nil {
		return nil, err
	}
	for t := range predTables {
		needed[t] = true
	}
	from, err := fromClause(kind, needed)
	if err != nil {
		return nil, err
	}
	whereSQL, args, err := ls.whereClause(kind, predicate)
	if err != nil {
		return nil, err
	}
	q := "SELECT " + expr + " " + from + " " + whereSQL

	rows, err := ls.query(q, args...)
	if err != nil {
		return nil, wrapWhere(where, err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, wrapWhere(where, err)
		}
		return starlark.None, nil
	}
	var v any
	if err := rows.Scan(&v); err != nil {
		return nil, fmt.Errorf("scan aggregate: %w", err)
	}
	return fromSQLValue(v)
}

// groupedAgg compiles and runs "SELECT <group>, <agg> … GROUP BY <group>" and
// returns a dict keyed by group value.
func (ls *LoadedSave) groupedAgg(kind, where, groupCol string, agg aggCall) (*starlark.Dict, error) {
	needed := map[string]bool{}
	groupQual, groupTable, err := resolveColumn(kind, groupCol)
	if err != nil {
		return nil, err
	}
	needed[groupTable] = true
	expr, aggTable, err := aggExpr(kind, agg)
	if err != nil {
		return nil, err
	}
	if aggTable != "" {
		needed[aggTable] = true
	}
	predicate, predTables, err := ls.rewritePredicate(kind, where)
	if err != nil {
		return nil, err
	}
	for t := range predTables {
		needed[t] = true
	}
	from, err := fromClause(kind, needed)
	if err != nil {
		return nil, err
	}
	whereSQL, args, err := ls.whereClause(kind, predicate)
	if err != nil {
		return nil, err
	}
	q := "SELECT " + groupQual + " AS grp, " + expr + " AS val " + from + " " + whereSQL + " GROUP BY " + groupQual

	rows, err := ls.query(q, args...)
	if err != nil {
		return nil, wrapWhere(where, err)
	}
	defer rows.Close()

	out := starlark.NewDict(0)
	for rows.Next() {
		var groupVal, aggVal any
		if err := rows.Scan(&groupVal, &aggVal); err != nil {
			return nil, fmt.Errorf("scan grouped aggregate: %w", err)
		}
		key, err := fromSQLValue(groupVal)
		if err != nil {
			return nil, fmt.Errorf("group key: %w", err)
		}
		val, err := fromSQLValue(aggVal)
		if err != nil {
			return nil, fmt.Errorf("group value: %w", err)
		}
		if err := out.SetKey(key, val); err != nil {
			return nil, err
		}
	}
	if err := rows.Err(); err != nil {
		return nil, wrapWhere(where, err)
	}
	return out, nil
}

// bulkSet stages a scalar constant onto every entity matching the (narrowed)
// collection, as one batched set. It selects the locators + current value for the
// matched rows (push-down: prior staged sets are flushed first so guards read the
// live value), converts each result row to a SQLValueRef via duckdb.ScanSQLRefs,
// and stages a stale-value-guarded StageSQLSet per row. Each row is written
// through to its in-memory row and recorded in the per-(table,column) mirror
// buffer, so the whole set later flushes as ONE UPDATE, not N point-updates.
// Returns the number of rows staged.
func (ls *LoadedSave) bulkSet(kind, where, column string, value starlark.Value) (int, error) {
	spec, ok := attrRegistry()[kind][column]
	if !ok {
		return 0, fmt.Errorf("unknown column %q for %s", column, kind)
	}
	if !spec.writable {
		return 0, fmt.Errorf("%s.%s is read-only (derived or locator column, not writable)", kind, column)
	}
	goVal, err := fromStarlark(value)
	if err != nil {
		return 0, fmt.Errorf("%s.%s: %w", kind, column, err)
	}
	// Validate once per column, before the query runs: a bad value is rejected up
	// front (and even when the predicate matches zero rows), not discovered per row.
	if err := validateSet(spec, goVal); err != nil {
		return 0, fmt.Errorf("%s.%s: %w", kind, column, err)
	}

	q, args, err := ls.bulkSetQuery(kind, where, spec)
	if err != nil {
		return 0, err
	}
	rows, err := ls.query(q, args...)
	if err != nil {
		return 0, wrapWhere(where, err)
	}
	refs, err := duckdb.ScanSQLRefs(rows, duckdb.SQLRefScanSpec{Table: spec.table, Column: spec.sourceColumn})
	rows.Close()
	if err != nil {
		return 0, wrapWhere(where, err)
	}

	for _, r := range refs {
		staged := goVal
		if row, ok := ls.rowForEntry(spec.table, r.Ref.EntryName); ok {
			coerced, err := setRowField(row, spec.fieldIndex, goVal)
			if err != nil {
				return 0, fmt.Errorf("%s.%s: %w", kind, column, err)
			}
			staged = coerced
		}
		if err := ls.session.StageSQLSet(r.Ref.WithExpected(r.CurrentValue), staged); err != nil {
			return 0, fmt.Errorf("%s.%s: %w", kind, column, err)
		}
		ls.stagedOps++
		ls.recordMirror(spec.table, spec.sourceColumn, spec.sqlType, r.Ref.EntryName, staged)
	}
	return len(refs), nil
}

// bulkSetQuery builds the locator+value SELECT for a bulk set: identity-table
// locators (entry_name + the kind's id guard) plus the target column, with the
// predicate's sub-tables and the column's table LEFT JOINed. Reuses the same
// builders as the analytics push-down so friendly columns and collisions resolve
// identically.
func (ls *LoadedSave) bulkSetQuery(kind, where string, spec attrSpec) (string, []any, error) {
	identity, err := identityTable(kind)
	if err != nil {
		return "", nil, err
	}
	needed := map[string]bool{spec.table: true}
	predicate, predTables, err := ls.rewritePredicate(kind, where)
	if err != nil {
		return "", nil, err
	}
	for t := range predTables {
		needed[t] = true
	}
	from, err := fromClause(kind, needed)
	if err != nil {
		return "", nil, err
	}
	whereSQL, args, err := ls.whereClause(kind, predicate)
	if err != nil {
		return "", nil, err
	}
	locators, err := locatorSelect(kind, identity)
	if err != nil {
		return "", nil, err
	}
	valueCol := quoteIdent(spec.table) + "." + quoteIdent(spec.sourceColumn) + " AS " + quoteIdent(spec.sourceColumn)
	q := "SELECT " + locators + ", " + valueCol + " " + from + " " + whereSQL
	return q, args, nil
}

// bulkDelete deletes every entity matching the predicate — the structural-op
// counterpart of bulkSet. EntityCollection iteration ignores the where predicate, so
// matches are resolved via the same push-down query path bulkSet uses, then a
// whole-entity delete is staged per match (reusing stageEntityDelete, so the cascade
// and id guard are identical to a single b.delete()). Structural: staged but NOT
// mirrored (an in-run query still sees the rows until commit). Each bibite is its own
// entry keyed by entry_name + an id guard, so the N staged deletes are
// order-independent (no array-index shift). Returns the number staged for deletion.
func (ls *LoadedSave) bulkDelete(kind, where string, prune bool) (int, error) {
	names, err := ls.matchingEntryNames(kind, where)
	if err != nil {
		return 0, err
	}
	for _, name := range names {
		if err := ls.stageEntityDelete(kind, name, prune); err != nil {
			return 0, err
		}
		ls.stagedOps++
	}
	return len(names), nil
}

// matchingEntryNames runs the predicate as a push-down SELECT of identity-table
// entry_names (the same rewrite/from/where builders bulkSet uses), returning the
// matched entities' entry_names. Only the predicate's sub-tables are LEFT JOINed, all
// 1:1 with identity, so the result is one row per matching entity.
func (ls *LoadedSave) matchingEntryNames(kind, where string) ([]string, error) {
	identity, err := identityTable(kind)
	if err != nil {
		return nil, err
	}
	predicate, predTables, err := ls.rewritePredicate(kind, where)
	if err != nil {
		return nil, err
	}
	needed := make(map[string]bool, len(predTables))
	for t := range predTables {
		needed[t] = true
	}
	from, err := fromClause(kind, needed)
	if err != nil {
		return nil, err
	}
	whereSQL, args, err := ls.whereClause(kind, predicate)
	if err != nil {
		return nil, err
	}
	rows, err := ls.query("SELECT "+quoteIdent(identity)+".entry_name "+from+" "+whereSQL, args...)
	if err != nil {
		return nil, wrapWhere(where, err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// locatorSelect projects the locator columns ScanSQLRefs needs (entry_name plus
// the kind's id guard), aliased to their bare names, from the identity table.
func locatorSelect(kind, identity string) (string, error) {
	id := quoteIdent(identity)
	switch kind {
	case "bibite":
		return id + ".entry_name AS entry_name, " + id + ".body_id AS body_id, " + id + ".has_body_id AS has_body_id", nil
	case "egg":
		return id + ".entry_name AS entry_name, " + id + ".egg_id AS egg_id, " + id + ".has_egg_id AS has_egg_id", nil
	default:
		return "", fmt.Errorf("unknown entity kind %q", kind)
	}
}

// wrapWhere names the predicate in an analytics error so an unknown column inside
// a .where() string surfaces a diagnostic that points at the predicate.
func wrapWhere(where string, err error) error {
	if strings.TrimSpace(where) == "" {
		return err
	}
	return fmt.Errorf("analytics query failed (where %q): %w", where, err)
}

// rewritePredicate qualifies friendly column names inside a raw .where() string
// with their owning table, so awkward/colliding generated column names resolve
// unambiguously across the joined sub-tables. It is a lightweight identifier
// tokenizer, NOT a SQL parser: it rewrites only bare identifiers that (a) are not
// already dotted, (b) are not function calls (followed by "("), and (c) match a
// registry friendly column for kind. Everything else — SQL keywords, operators,
// numeric/string literals, quoted identifiers, already-qualified refs — passes
// through verbatim, so no hand-maintained keyword list is needed. It returns the
// rewritten predicate and the set of sub-tables its columns reference.
func (ls *LoadedSave) rewritePredicate(kind, expr string) (string, map[string]bool, error) {
	reg := attrRegistry()[kind]
	tables := map[string]bool{}
	if strings.TrimSpace(expr) == "" {
		return "", tables, nil
	}

	var out strings.Builder
	var prev byte // last significant (non-space) byte emitted; for dotted-ref detection
	writeStr := func(s string) {
		out.WriteString(s)
		for i := len(s) - 1; i >= 0; i-- {
			if !isSpace(s[i]) {
				prev = s[i]
				break
			}
		}
	}
	writeByte := func(b byte) {
		out.WriteByte(b)
		if !isSpace(b) {
			prev = b
		}
	}

	s := expr
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == '\'':
			// single-quoted string literal: copy verbatim, honoring '' escape.
			writeByte(c)
			i++
			for i < len(s) {
				writeByte(s[i])
				if s[i] == '\'' {
					if i+1 < len(s) && s[i+1] == '\'' {
						writeByte(s[i+1])
						i += 2
						continue
					}
					i++
					goto nextToken
				}
				i++
			}
		case c == '"':
			// double-quoted identifier: caller qualified it explicitly; copy as-is.
			writeByte(c)
			i++
			for i < len(s) {
				writeByte(s[i])
				if s[i] == '"' {
					i++
					goto nextToken
				}
				i++
			}
		case isIdentStart(c):
			j := i + 1
			for j < len(s) && isIdentPart(s[j]) {
				j++
			}
			word := s[i:j]
			dotted := prev == '.'
			// function call if the next non-space char is '('.
			k := j
			for k < len(s) && isSpace(s[k]) {
				k++
			}
			isCall := k < len(s) && s[k] == '('
			if !dotted && !isCall {
				if spec, ok := reg[strings.ToLower(word)]; ok {
					writeStr(quoteIdent(spec.table) + "." + quoteIdent(spec.sourceColumn))
					tables[spec.table] = true
					i = j
					continue
				}
			}
			writeStr(word)
			i = j
		default:
			writeByte(c)
			i++
		}
		continue
	nextToken:
	}
	return out.String(), tables, nil
}

func isSpace(b byte) bool      { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }
func isIdentStart(b byte) bool { return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }
func isIdentPart(b byte) bool  { return isIdentStart(b) || (b >= '0' && b <= '9') }
