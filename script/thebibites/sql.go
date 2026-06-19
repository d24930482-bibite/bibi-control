package thebibites

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"go.starlark.net/starlark"

	"github.com/asemones/bibicontrol/duckdb"
	mutator "github.com/asemones/bibicontrol/savemutator/thebibites"
)

// sql.go is the T5 analytics path: the raw save.sql escape hatch plus the
// push-down query builder that compiles collection narrowing + aggregates into
// DuckDB SELECTs. Nothing here materializes Entity values — computation runs in
// DuckDB and only scalars/dicts come back.

// quoteIdent double-quotes a SQL identifier for DuckDB, escaping embedded quotes.
// Table and column names come from generated metadata (safe), but quoting keeps
// the builder correct for any identifier and case-exact against the migration's
// lowercase column names. It delegates to duckdb.QuoteIdent so the import path and
// the analytics push-down builder quote identifiers identically (one source of
// truth); this package-local alias keeps the many call sites here and in mirror.go
// terse.
func quoteIdent(identifier string) string {
	return duckdb.QuoteIdent(identifier)
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

// Query runs a working-copy SELECT scoped to this loaded save's working
// partition (save_id == ls.saveID == worldID) and materializes the result as
// []map[string]any for a Go caller (the workspace automation layer owns the
// Starlark conversion via mapsToStarlark — this returns plain Go scalars, NOT
// Starlark values). It is the world.query() executor.
//
// Scoping (load-bearing, ENFORCED BY CONSTRUCTION): ls.query runs raw SQL over
// the SHARED workspace DuckDB handle whose tables hold EVERY world's
// working/history partitions keyed by save_id. A naive `SELECT … FROM bibites`
// over the raw tables would read all save_ids across all worlds (the leak this
// guards against). To keep the working-copy read isolated WITHOUT requiring the
// caller to write a JOIN, every normalized base table is shadowed by a CTE that
// re-defines it as only this save's working partition:
//
//	WITH bibites AS (SELECT * FROM bibites WHERE save_id = '<saveID>'),
//	     bibite_body AS (SELECT * FROM bibite_body WHERE save_id = '<saveID>'),
//	     … (one per normalized table) …
//	<user query>
//
// DuckDB resolves a CTE name ahead of the catalog table of the same name, so an
// un-joined user query like `SELECT count(*) FROM bibites` binds to the scoped
// CTE and returns ONLY the open world's rows — cross-world reads are not possible
// through this surface (they belong to workspace.query / world.history_query).
// This is the working-partition analogue of the world_saves CTE scoping
// HistoryQuery uses for the history partition. flushMirror runs at the head of
// ls.query, so a staged-but-uncommitted set is visible to a working-copy
// read-after-write — proving working-copy semantics, not a stale committed
// projection. The read-only gate lives at the workspace binding
// (worldValue.queryBuiltin reuses ensureReadOnly before calling Query); do NOT
// duplicate it here.
func (ls *LoadedSave) Query(ctx context.Context, query string) ([]map[string]any, error) {
	rows, err := ls.query(ls.scopedQuery(query))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRowsToMaps(rows)
}

// scopedQuery wraps a raw user query with one shadowing CTE per normalized base
// table, each filtered to this save's working partition (save_id = ls.saveID).
// Because DuckDB binds CTE names before catalog tables, a bare `FROM <table>` in
// the user query resolves to the per-world CTE — enforcing working-copy isolation
// by construction so the caller never has to JOIN a scope key. ls.saveID is a
// content/world identifier with no quote-injection surface in practice, but it is
// embedded as a single-quoted literal with doubled quotes for defense in depth.
func (ls *LoadedSave) scopedQuery(query string) string {
	id := strings.ReplaceAll(ls.saveID, "'", "''")
	tables := allNormalizedTableNames()
	var b strings.Builder
	b.WriteString("WITH ")
	for i, t := range tables {
		if i > 0 {
			b.WriteString(", ")
		}
		qt := quoteIdent(t)
		b.WriteString(qt)
		b.WriteString(" AS (SELECT * FROM ")
		b.WriteString(qt)
		b.WriteString(" WHERE save_id = '")
		b.WriteString(id)
		b.WriteString("') ")
	}
	b.WriteString(query)
	return b.String()
}

// scanRowsToMaps materializes a result set as one map[string]any per row (column
// name -> raw driver scalar), preserving column order semantics. It is the Go-API
// analogue of scanRowsToDicts (which returns Starlark values); the automation
// layer converts these maps to Starlark via its own sqlScalarToStarlark path.
func scanRowsToMaps(rows *sql.Rows) ([]map[string]any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
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
			return nil, fmt.Errorf("scan row: %w", err)
		}
		m := make(map[string]any, len(cols))
		for i, name := range cols {
			m[name] = values[i]
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
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
//
// catalogCols (non-nil only for a spanning scope) lets the scope's friendly
// catalog columns (world_id / sim_time) resolve to mirror_saves.<col>. They come
// from the catalog DDL, NOT a parallel entity allowlist; when resolved, the owning
// table returned is the catalog sentinel so the caller pulls the catalog JOIN into
// scope. Entity columns continue to resolve from attrRegistry().
func resolveColumn(kind, col string, catalogCols map[string]string) (qualified, table string, err error) {
	if catalogCols != nil {
		if src, ok := catalogCols[col]; ok {
			return quoteIdent(catalogTable) + "." + quoteIdent(src), catalogTable, nil
		}
	}
	// Fold the column to lowercase before the registry read: registry keys are
	// already lowercase snake_case (generated metadata), so this is a no-op for
	// correctly-cased inputs and allows user-supplied mixed-case column names to
	// resolve cleanly. No collision is possible (all registry keys are distinct
	// lowercase), so no foldLookup is needed here.
	spec, ok := attrRegistry()[kind][strings.ToLower(col)]
	if !ok {
		return "", "", fmt.Errorf("unknown column %q for %s", col, kind)
	}
	return quoteIdent(spec.table) + "." + quoteIdent(spec.sourceColumn), spec.table, nil
}

// aggExpr builds the SQL aggregate expression and reports the sub-table (if any)
// it references so the caller can include it in the FROM joins. catalogCols (a
// spanning scope's catalog columns, else nil) lets an aggregate over world_id /
// sim_time resolve to the catalog table.
func aggExpr(kind string, agg aggCall, catalogCols map[string]string) (expr, table string, err error) {
	if agg.fn == "count" {
		return "count(*)", "", nil
	}
	qualified, table, err := resolveColumn(kind, agg.col, catalogCols)
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

var (
	oneToManyOnce sync.Once
	oneToManySet  map[string]bool
)

// oneToManyTables is the set of tables registered as 1:many element
// sub-collections (brain nodes/synapses, stomach contents) across all entity
// kinds. fromClause asserts no such table is scalar-joined: a LEFT JOIN against a
// 1:many table multiplies identity rows and silently skews every count/sum/median.
// Today entityTables (1:1) and entitySubCollections (1:many) are disjoint by
// design; this turns a future save-format revision that lists a 1:many table in
// entityTables into a loud, localized error instead of wrong analytics.
//
// entitySubCollections is static, so the set is built once via sync.Once (matching
// the registry singletons in attr_registry.go) rather than reallocated on every
// fromClause() push-down query. The returned map is shared read-only — callers must
// not mutate it.
func oneToManyTables() map[string]bool {
	oneToManyOnce.Do(func() {
		oneToManySet = map[string]bool{}
		for _, subs := range entitySubCollections {
			for _, info := range subs {
				oneToManySet[info.table] = true
			}
		}
	})
	return oneToManySet
}

// fromClause builds "FROM <identity> [LEFT JOIN <subtable> ON …][catalogJoin]"
// including only the sub-tables actually referenced (in entityTables order, for
// determinism). Every sub-table is 1:1 with identity on (save_id, entry_name), so
// LEFT JOIN preserves cardinality and aggregates stay correct. catalogJoin (the
// active scope's catalog LEFT JOIN, or "") is appended last when a spanning scope
// needs world_id / sim_time to resolve — the catalog is keyed 1:1 by save_id on
// the identity table, so it never inflates aggregate rows.
func fromClause(kind string, needed map[string]bool, catalogJoin string) (string, error) {
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
	b.WriteString(catalogJoin)
	return b.String(), nil
}

// whereClause builds "WHERE <scope clause> [AND (<predicate>)]" and its args. The
// scope supplies the save-partition restriction (working: "<identity>.save_id =
// ?"/[saveID] — byte-identical to the pre-E3 hardcoded clause; spanning:
// "<identity>.save_id IN (SELECT save_id FROM mirror_saves …)"). The predicate is
// already friendly-column-rewritten. Routing the partition filter through the
// scope is what keeps every push-down query scoped BY CONSTRUCTION.
func (ls *LoadedSave) whereClause(kind, predicate string, scope SaveScope) (string, []any, error) {
	identity, err := identityTable(kind)
	if err != nil {
		return "", nil, err
	}
	scopeSQL, args := scope.scopeClause(identity)
	clause := "WHERE " + scopeSQL
	if strings.TrimSpace(predicate) != "" {
		clause += " AND (" + predicate + ")"
	}
	return clause, args, nil
}

// workingScope returns this save's single-working-partition scope. The mutation
// and entry-name match builders are single-save by construction (mutation cannot
// reach history/all-worlds), so they always use this scope; its clause is
// byte-identical to the pre-E3 hardcoded "<identity>.save_id = ?" / [saveID].
func (ls *LoadedSave) workingScope() SaveScope {
	return workingScope{saveID: ls.saveID}
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
// narrowed) collection under the given scope, returning the scalar Starlark value
// (None for SQL NULL, e.g. an aggregate over the empty set). scope supplies the
// save-partition filter + any catalog columns/JOIN (working scope for single-save;
// spanning scope for world/workspace reads).
func (ls *LoadedSave) scalarAgg(kind, where string, scope SaveScope, agg aggCall) (starlark.Value, error) {
	scope = scopeFor(ls, scope)
	catalogCols := scope.catalogCols()
	needed := map[string]bool{}
	expr, aggTable, err := aggExpr(kind, agg, catalogCols)
	if err != nil {
		return nil, err
	}
	if aggTable != "" {
		needed[aggTable] = true
	}
	predicate, predTables, err := ls.rewritePredicate(kind, where, catalogCols)
	if err != nil {
		return nil, err
	}
	for t := range predTables {
		needed[t] = true
	}
	identity, err := identityTable(kind)
	if err != nil {
		return nil, err
	}
	from, err := fromClause(kind, needed, catalogJoinIfNeeded(scope, identity, needed))
	if err != nil {
		return nil, err
	}
	whereSQL, args, err := ls.whereClause(kind, predicate, scope)
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

// catalogJoinIfNeeded returns the scope's catalog LEFT JOIN fragment when the
// referenced-tables set includes the catalog (a world_id / sim_time column was
// used), else "". This is the single place that decides whether mirror_saves is
// pulled into the FROM, so a spanning aggregate that touches no catalog column
// never pays the JOIN.
func catalogJoinIfNeeded(scope SaveScope, identity string, needed map[string]bool) string {
	if needed[catalogTable] {
		return scope.catalogJoin(identity)
	}
	return ""
}

// groupedAgg compiles and runs "SELECT <group>, <agg> … GROUP BY <group>" under
// the given scope and returns a dict keyed by group value. group_by('world_id') /
// group_by('sim_time') resolve through the spanning scope's catalog columns, so a
// cross-world breakdown needs no JOIN in the script.
func (ls *LoadedSave) groupedAgg(kind, where, groupCol string, scope SaveScope, agg aggCall) (*starlark.Dict, error) {
	scope = scopeFor(ls, scope)
	catalogCols := scope.catalogCols()
	needed := map[string]bool{}
	groupQual, groupTable, err := resolveColumn(kind, groupCol, catalogCols)
	if err != nil {
		return nil, err
	}
	needed[groupTable] = true
	expr, aggTable, err := aggExpr(kind, agg, catalogCols)
	if err != nil {
		return nil, err
	}
	if aggTable != "" {
		needed[aggTable] = true
	}
	predicate, predTables, err := ls.rewritePredicate(kind, where, catalogCols)
	if err != nil {
		return nil, err
	}
	for t := range predTables {
		needed[t] = true
	}
	identity, err := identityTable(kind)
	if err != nil {
		return nil, err
	}
	from, err := fromClause(kind, needed, catalogJoinIfNeeded(scope, identity, needed))
	if err != nil {
		return nil, err
	}
	whereSQL, args, err := ls.whereClause(kind, predicate, scope)
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
		if err := ls.writeThroughAndStage(spec, r.Ref, r.CurrentValue, goVal, kind, column); err != nil {
			return 0, err
		}
	}
	return len(refs), nil
}

// writeThroughAndStage applies one per-row scalar set for a bulk operation: it
// captures the row's prior value, writes writeVal through to the in-memory row
// (coercing it into the column's Go kind), and stages a stale-value-guarded set
// keyed on entry_name with the concrete coerced value mirrored, so the deferred
// UPDATE never diverges from the guarded value. If the in-memory row is missing
// (only staged), writeVal is staged directly. On stage failure the write-through
// is rolled back so a rejected stage never leaves a phantom in-memory value (same
// class as the scalar SetField path). Errors are wrapped "%s.%s: %w" with kind and
// column. Shared verbatim between bulkSet (constant) and bulkSetExpr (per-row
// computed value) so the rollback/guard invariant lives in one place.
func (ls *LoadedSave) writeThroughAndStage(spec attrSpec, ref mutator.SQLValueRef, currentVal, writeVal any, kind, column string) error {
	staged := writeVal
	var (
		wroteRow   reflect.Value
		restorePri any
		restore    bool
	)
	if row, ok := ls.rowForEntry(spec.table, ref.EntryName); ok {
		prior, err := goScalar(row.FieldByIndex(spec.fieldIndex))
		if err != nil {
			return fmt.Errorf("%s.%s: %w", kind, column, err)
		}
		coerced, err := setRowField(row, spec.fieldIndex, writeVal)
		if err != nil {
			return fmt.Errorf("%s.%s: %w", kind, column, err)
		}
		staged = coerced
		wroteRow, restorePri, restore = row, prior, true
	}
	// Roll back this row's write-through on stage failure: a rejected stage must
	// not leave a phantom in-memory value (same class as the scalar SetField path).
	var rollback func()
	if restore {
		rollback = func() { _, _ = setRowField(wroteRow, spec.fieldIndex, restorePri) }
	}
	if err := ls.stageScalarSet(ref, currentVal, staged, spec.table, spec.sourceColumn, spec.sqlType,
		[]mirrorLocator{{column: "entry_name", value: ref.EntryName}}, rollback); err != nil {
		return fmt.Errorf("%s.%s: %w", kind, column, err)
	}
	return nil
}

// bulkSetExpr stages a per-row SQL expression onto every entity matching the
// (narrowed) collection: the analogue of bulkSet, but each matched row receives a
// distinct value computed by evaluating `expr` against that row in DuckDB, not one
// shared constant. It pushes down a 3-column SELECT (locators, current value, the
// computed __new_val), scans each row via ScanSQLRefsWithNewValue, then per row:
// coerces the raw result into the column's Go kind (coerceExprResult), re-validates
// it against the column's full Rule POST-compute (validateSet — so e.g. the energy
// >= 0 bound rejects "energy - 1e12"), writes it through to the in-memory row, and
// stages a stale-value-guarded set + mirror intent (the concrete computed scalar,
// so the deferred mirror UPDATE never diverges from the guarded value). The first
// invalid row fails the whole call: earlier rows' in-memory write-throughs are not
// applied to the session (nothing persists until commit), consistent with how
// bulkSet returns mid-loop. Returns the number of rows staged.
func (ls *LoadedSave) bulkSetExpr(kind, where, column, expr string) (int, error) {
	spec, ok := attrRegistry()[kind][column]
	if !ok {
		return 0, fmt.Errorf("unknown column %q for %s", column, kind)
	}
	if !spec.writable {
		return 0, fmt.Errorf("%s.%s is read-only (derived or locator column, not writable)", kind, column)
	}
	if err := validateExprSafety(expr); err != nil {
		return 0, fmt.Errorf("%s.%s: %w", kind, column, err)
	}
	// Structurally reject an unknown column referenced in the expression BEFORE the
	// query runs, so the clean diagnostic does not depend on DuckDB's binder wording
	// (which wrapExpr still string-matches as a fallback for shapes this can't see).
	if err := validateExprColumns(kind, expr); err != nil {
		return 0, fmt.Errorf("%s.%s: %w", kind, column, err)
	}

	q, args, err := ls.bulkSetExprQuery(kind, where, spec, expr)
	if err != nil {
		return 0, err
	}
	rows, err := ls.query(q, args...)
	if err != nil {
		return 0, wrapExpr(expr, wrapWhere(where, err))
	}
	refs, err := duckdb.ScanSQLRefsWithNewValue(rows,
		duckdb.SQLRefScanSpec{Table: spec.table, Column: spec.sourceColumn}, exprNewValueColumn)
	rows.Close()
	if err != nil {
		return 0, wrapExpr(expr, wrapWhere(where, err))
	}

	for _, r := range refs {
		coerced, err := coerceExprResult(spec, r.NewValue)
		if err != nil {
			return 0, fmt.Errorf("%s.%s: %w", kind, column, err)
		}
		// Re-validate POST-compute, per row: type/range/enum all apply to the
		// computed value (e.g. a non-negative bound rejects a row that went negative).
		if err := validateSet(spec, coerced); err != nil {
			return 0, fmt.Errorf("%s.%s: %w", kind, column, err)
		}
		if err := ls.writeThroughAndStage(spec, r.Ref, r.CurrentValue, coerced, kind, column); err != nil {
			return 0, err
		}
	}
	return len(refs), nil
}

// exprNewValueColumn is the projection alias the set_expr push-down evaluates the
// user expression into; the scanner reads the computed per-row value from it.
const exprNewValueColumn = "__new_val"

// bulkSetExprQuery builds the 3-column SELECT for a per-row expression set:
// identity-table locators + the kind's id guard, the target column's CURRENT value
// (the stale-value guard), and the user expression evaluated as __new_val. The
// expression is qualified through rewritePredicate (the same tokenizer .where()
// uses), so a bare friendly column inside it resolves to <table>.<sourceColumn> and
// any sub-table it references is fed into the JOIN needed-set — e.g. "energy +
// d2_size" LEFT JOINs bibite_body. Reuses bulkSetQuery's builders so resolution is
// identical to the constant-set path.
func (ls *LoadedSave) bulkSetExprQuery(kind, where string, spec attrSpec, expr string) (string, []any, error) {
	identity, err := identityTable(kind)
	if err != nil {
		return "", nil, err
	}
	needed := map[string]bool{spec.table: true}

	predicate, predTables, err := ls.rewritePredicate(kind, where, nil)
	if err != nil {
		return "", nil, err
	}
	for t := range predTables {
		needed[t] = true
	}

	// Qualify the expression with the same tokenizer .where() uses and pull its
	// referenced sub-tables into the JOIN set so a cross-table expression resolves.
	exprSQL, exprTables, err := ls.rewritePredicate(kind, expr, nil)
	if err != nil {
		return "", nil, err
	}
	if strings.TrimSpace(exprSQL) == "" {
		return "", nil, fmt.Errorf("set_expr expression is empty")
	}
	for t := range exprTables {
		needed[t] = true
	}

	from, err := fromClause(kind, needed, "")
	if err != nil {
		return "", nil, err
	}
	whereSQL, args, err := ls.whereClause(kind, predicate, ls.workingScope())
	if err != nil {
		return "", nil, err
	}
	locators, err := locatorSelect(kind, identity)
	if err != nil {
		return "", nil, err
	}
	valueCol := quoteIdent(spec.table) + "." + quoteIdent(spec.sourceColumn) + " AS " + quoteIdent(spec.sourceColumn)
	newValCol := "(" + exprSQL + ") AS " + quoteIdent(exprNewValueColumn)
	q := "SELECT " + locators + ", " + valueCol + ", " + newValCol + " " + from + " " + whereSQL
	return q, args, nil
}

// validateExprSafety rejects shapes a single-projection SQL expression must never
// contain:
//   - a raw statement terminator (';' outside a string literal, which could chain
//     a second statement);
//   - a subquery ('(' immediately followed by a case-insensitive SELECT);
//   - a SQL comment — line ('--') or block ('/* */') — which would comment out the
//     rest of the generated single-line SELECT (including the trailing save_id
//     placeholder), turning a clean diagnostic into a low-level parse error and
//     opening a comment-injection seam.
//
// It reuses the same literal-aware scan as rewritePredicate so quoted strings and
// their doubled-quote escapes are honored — a '--', '/*' or ';' inside a string
// literal is fine. The
// expression is still strictly weaker than the raw save.sql() hatch — it is one
// SELECT projection over the in-memory save — so this is defense in depth, not the
// only gate.
func validateExprSafety(expr string) error {
	s := expr
	i := 0
	for i < len(s) {
		c := s[i]
		switch c {
		case '\'':
			i++
			for i < len(s) {
				if s[i] == '\'' {
					if i+1 < len(s) && s[i+1] == '\'' {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
		case '"':
			i++
			for i < len(s) {
				if s[i] == '"' {
					i++
					break
				}
				i++
			}
		case ';':
			return fmt.Errorf("expression must not contain ';'")
		case '-':
			// SQL line comment: '--' comments out the rest of the generated
			// single-line SELECT, including the save_id placeholder.
			if i+1 < len(s) && s[i+1] == '-' {
				return fmt.Errorf("expression must not contain a SQL comment ('--')")
			}
			i++
		case '/':
			// SQL block comment: '/* ... */' hides part of the generated SELECT.
			if i+1 < len(s) && s[i+1] == '*' {
				return fmt.Errorf("expression must not contain a SQL comment ('/* */')")
			}
			i++
		case '(':
			j := i + 1
			for j < len(s) && isSpace(s[j]) {
				j++
			}
			if j+6 <= len(s) && strings.EqualFold(s[j:j+6], "select") {
				return fmt.Errorf("expression must not contain a subquery")
			}
			i++
		default:
			i++
		}
	}
	return nil
}

// exprColumnLiterals are the bare value-keywords a column-position identifier may
// legitimately be even though they are not registry columns. They are excluded
// from the structural unknown-column check so e.g. set_expr(col, "NULL") still
// reaches the post-compute coerce/validate gate (where a NULL is rejected) rather
// than being misreported here as an unknown column.
var exprColumnLiterals = map[string]bool{
	"null": true, "true": true, "false": true,
}

// validateExprColumns structurally rejects a bare identifier referenced in a
// value position of a set_expr expression that is not a registry column for kind,
// BEFORE the query runs — so the unknown-column diagnostic no longer depends on
// DuckDB's binder error wording (wrapExpr keeps the substring match only as a
// fallback for shapes this scanner deliberately does not flag).
//
// It reuses rewritePredicate's literal-aware tokenizer rules: it ignores quoted
// strings/identifiers, already-dotted (qualified) refs, function calls (name
// followed by '('), and numeric literals. A bare identifier is treated as a
// column reference ONLY when it stands alone in a value slot — i.e. both its
// nearest significant neighbors are non-word (operator/paren/comma/comparison, or
// the start/end of the expression). This conservatively skips keyword constructs
// (CASE/WHEN/THEN/ELSE/END, IS NULL, x AND y), whose tokens are word-adjacent, so
// the check fires for the dominant failure mode — a misspelled column like
// "enrgy + 1" — without rejecting expressions DuckDB would otherwise accept.
func validateExprColumns(kind, expr string) error {
	reg := attrRegistry()[kind]
	s := expr
	i := 0
	var prev byte // last significant byte before the current token (0 == start)
	setPrev := func(b byte) {
		if !isSpace(b) {
			prev = b
		}
	}
	for i < len(s) {
		c := s[i]
		switch {
		case c == '\'':
			setPrev(c)
			i++
			for i < len(s) {
				if s[i] == '\'' {
					if i+1 < len(s) && s[i+1] == '\'' {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			prev = '\''
		case c == '"':
			setPrev(c)
			i++
			for i < len(s) {
				if s[i] == '"' {
					i++
					break
				}
				i++
			}
			prev = '"'
		case isIdentStart(c):
			j := i + 1
			for j < len(s) && isIdentPart(s[j]) {
				j++
			}
			word := s[i:j]
			dotted := prev == '.'
			// next significant byte after the identifier
			k := j
			for k < len(s) && isSpace(s[k]) {
				k++
			}
			var next byte // 0 == end of expression
			if k < len(s) {
				next = s[k]
			}
			isCall := next == '('
			lower := strings.ToLower(word)
			_, known := reg[lower]
			// Word-adjacent on either side => part of a keyword construct, not a
			// lone value-slot column reference; leave it for DuckDB / the fallback.
			wordAdjacent := isIdentPart(prev) || isIdentStart(next)
			if !dotted && !isCall && !known && !wordAdjacent && !exprColumnLiterals[lower] {
				return fmt.Errorf("unknown column %q in expression", word)
			}
			// prev for the token following this identifier is its last byte.
			prev = s[j-1]
			i = j
		default:
			setPrev(c)
			i++
		}
	}
	return nil
}

// wrapExpr names the set_expr expression in an error and rewrites the raw DuckDB
// "column ... does not exist" / "Referenced column ... not found" binder error
// into a clean "unknown column in expression" diagnostic, the set_expr analogue of
// wrapWhere for the projected expression. validateExprColumns now catches the
// common unknown-column case structurally before the query runs; this substring
// match remains as a fallback for shapes that pre-check deliberately does not flag
// (e.g. an unknown column buried inside a keyword construct).
func wrapExpr(expr string, err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "Referenced column") || strings.Contains(msg, "does not exist") ||
		strings.Contains(msg, "not found in FROM clause") || strings.Contains(msg, "Binder Error") {
		return fmt.Errorf("unknown column in expression %q: %w", expr, err)
	}
	return fmt.Errorf("set_expr (%q): %w", expr, err)
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
	predicate, predTables, err := ls.rewritePredicate(kind, where, nil)
	if err != nil {
		return "", nil, err
	}
	for t := range predTables {
		needed[t] = true
	}
	from, err := fromClause(kind, needed, "")
	if err != nil {
		return "", nil, err
	}
	whereSQL, args, err := ls.whereClause(kind, predicate, ls.workingScope())
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
	predicate, predTables, err := ls.rewritePredicate(kind, where, nil)
	if err != nil {
		return nil, err
	}
	needed := make(map[string]bool, len(predTables))
	for t := range predTables {
		needed[t] = true
	}
	from, err := fromClause(kind, needed, "")
	if err != nil {
		return nil, err
	}
	whereSQL, args, err := ls.whereClause(kind, predicate, ls.workingScope())
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
//
// catalogCols (a spanning scope's catalog columns, else nil) lets a bare world_id
// / sim_time token rewrite to mirror_saves.<col> and pulls the catalog table into
// the referenced-tables set, so the caller appends the catalog JOIN. The catalog
// columns take precedence over an identically named entity column (none collide
// today) and come from the catalog DDL, not a parallel entity allowlist.
func (ls *LoadedSave) rewritePredicate(kind, expr string, catalogCols map[string]string) (string, map[string]bool, error) {
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
				lower := strings.ToLower(word)
				if catalogCols != nil {
					if src, ok := catalogCols[lower]; ok {
						writeStr(quoteIdent(catalogTable) + "." + quoteIdent(src))
						tables[catalogTable] = true
						i = j
						continue
					}
				}
				if spec, ok := reg[lower]; ok {
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
