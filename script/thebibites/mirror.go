package thebibites

import (
	"context"
	"fmt"
	"strings"

	mutator "github.com/asemones/bibicontrol/savemutator/thebibites"
)

// mirror.go is the in-run consistency core: a deferred buffer of scalar mutations
// that DuckDB has not yet observed. Each set is staged on the Session for the
// eventual single write AND recorded here; the buffer is flushed into the open
// DuckDB as one batched UPDATE per (table, column) the next time a query or
// aggregate runs (see flushMirror in loadedsave.go). This is what lets an in-run
// `query -> set -> query` observe its own mutation with no reparse and no
// re-import — only incremental UPDATEs. Structural ops (append/delete) are NOT
// mirrored — they become visible only after commit (the consistency contract).
//
// A pending write is keyed by an ordered list of discriminator columns
// (mirrorLocator) so any cell can be addressed: entity scalars by entry_name
// alone, genes by (entry_name, gene_name), settings by (entry_name, path). The
// flush emits those discriminators into the VALUES relation and AND-joins them in
// the WHERE, so the single batched UPDATE per (table, column) is preserved
// regardless of how many discriminators a surface needs.

// mirrorLocator is one discriminator column = value pair. Together with the other
// locators recorded for a (table, column), it uniquely identifies the DuckDB row.
type mirrorLocator struct {
	column string
	value  any
}

// mirrorKey identifies a normalized (table, column) cell family. All values for
// one key share a SQL type, so a flush emits one type-homogeneous UPDATE per key.
type mirrorKey struct {
	table  string
	column string
}

// mirrorRow is one pending write: the discriminator values (aligned positionally
// with the owning mirrorColumn.locatorCols) and the new value.
type mirrorRow struct {
	locators []any
	value    any
}

// mirrorColumn buffers pending writes for one (table, column): a composite locator
// key -> pending row, last-write-wins. It carries the ordered discriminator column
// names (consistent across all rows for this key) and the column's DuckDB type for
// the flush CAST.
type mirrorColumn struct {
	sqlType     string
	locatorCols []string
	rows        map[string]mirrorRow
}

// mirrorBuffer accumulates pending scalar mutations grouped by (table, column).
type mirrorBuffer struct {
	cols map[mirrorKey]*mirrorColumn
}

// record buffers one scalar set, keyed within its (table, column) by the composite
// of its discriminator values so N row-by-row sets of the same cell collapse to a
// single VALUES tuple (last-write-wins).
func (b *mirrorBuffer) record(table, column, sqlType string, locators []mirrorLocator, value any) {
	if b.cols == nil {
		b.cols = make(map[mirrorKey]*mirrorColumn)
	}
	key := mirrorKey{table: table, column: column}
	col := b.cols[key]
	if col == nil {
		cols := make([]string, len(locators))
		for i, l := range locators {
			cols[i] = l.column
		}
		col = &mirrorColumn{sqlType: sqlType, locatorCols: cols, rows: make(map[string]mirrorRow)}
		b.cols[key] = col
	}
	vals := make([]any, len(locators))
	var rowKey strings.Builder
	for i, l := range locators {
		vals[i] = l.value
		if i > 0 {
			rowKey.WriteByte(0)
		}
		fmt.Fprintf(&rowKey, "%v", l.value)
	}
	col.rows[rowKey.String()] = mirrorRow{locators: vals, value: value}
}

func (b *mirrorBuffer) empty() bool { return len(b.cols) == 0 }

func (b *mirrorBuffer) reset() { b.cols = nil }

// recordMirrorRow buffers a pending scalar mutation keyed by an arbitrary set of
// discriminator columns (genes, settings) and marks DuckDB dirty.
func (ls *LoadedSave) recordMirrorRow(table, column, sqlType string, locators []mirrorLocator, value any) {
	ls.mirror.record(table, column, sqlType, locators, value)
	ls.mirrorDirty = true
}

// stageScalarSet owns the invariant tail every committed scalar-set call site
// shares: stamp the stale-value guard, stage the guarded set on the session, and —
// only on success — count it (stagedOps) and record its DuckDB mirror intent. The
// per-site variation is supplied by the caller: the fully-built ref (with its
// locators + Column set), the captured old value (guard) and coerced staged value,
// and the mirror's (table, column, sqlType, locators). Because validateSet runs and
// the in-memory write-through happens before this call, a rejected StageSQLSet must
// leave no phantom value: callers that wrote through first pass a rollback closure,
// which runs (and only runs) on stage failure to restore the prior in-memory value.
// Callers whose write-through is itself rolled back elsewhere (or who do not write
// through before staging) pass a nil rollback. The mirror is recorded with the
// staged value, keyed by sourceColumn (not the friendly alias) so an in-run SQL read
// addresses the real generated column.
func (ls *LoadedSave) stageScalarSet(ref mutator.SQLValueRef, old, staged any, table, column, sqlType string, locators []mirrorLocator, rollback func()) error {
	if err := ls.session.StageSQLSet(ref.WithExpected(old), staged); err != nil {
		if rollback != nil {
			rollback()
		}
		return err
	}
	ls.stagedOps++
	ls.recordMirrorRow(table, column, sqlType, locators, staged)
	return nil
}

// flushMirrorColumn applies one (table, column)'s buffered writes as a single
// set-based UPDATE: the new values arrive as an inline VALUES relation joined on
// every discriminator column, so the whole column updates in one statement
// regardless of row count. The val is CAST to the column's DuckDB type so untyped
// placeholders in VALUES resolve unambiguously.
func (ls *LoadedSave) flushMirrorColumn(ctx context.Context, key mirrorKey, col *mirrorColumn) error {
	if len(col.rows) == 0 {
		return nil
	}
	castType := col.sqlType
	if castType == "" {
		castType = "VARCHAR"
	}

	var b strings.Builder
	b.WriteString("UPDATE ")
	b.WriteString(quoteIdent(key.table))
	b.WriteString(" SET ")
	b.WriteString(quoteIdent(key.column))
	b.WriteString(" = v.val FROM (VALUES ")

	args := make([]any, 0, len(col.rows)*(len(col.locatorCols)+1)+1)
	i := 0
	for _, row := range col.rows {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteByte('(')
		for j := range col.locatorCols {
			b.WriteString("?, ")
			args = append(args, row.locators[j])
		}
		b.WriteString("CAST(? AS " + castType + "))")
		args = append(args, row.value)
		i++
	}

	b.WriteString(") AS v(")
	for _, c := range col.locatorCols {
		b.WriteString(quoteIdent(c))
		b.WriteString(", ")
	}
	b.WriteString("val) WHERE ")
	b.WriteString(quoteIdent(key.table) + ".save_id = ?")
	args = append(args, ls.saveID)
	for _, c := range col.locatorCols {
		b.WriteString(" AND " + quoteIdent(key.table) + "." + quoteIdent(c) + " = v." + quoteIdent(c))
	}

	if _, err := ls.db.ExecContext(ctx, b.String(), args...); err != nil {
		return fmt.Errorf("mirror %s.%s: %w", key.table, key.column, err)
	}
	return nil
}
