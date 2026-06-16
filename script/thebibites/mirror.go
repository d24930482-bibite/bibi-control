package thebibites

import (
	"context"
	"fmt"
	"strings"
)

// mirror.go is the T6 in-run consistency core: a deferred buffer of scalar
// mutations that DuckDB has not yet observed. Each set is staged on the Session
// for the eventual single write AND recorded here; the buffer is flushed into the
// open DuckDB as one batched UPDATE per (table, column) the next time a query or
// aggregate runs (see flushMirror in loadedsave.go). This is what lets an in-run
// `query -> set -> query` observe its own mutation with no reparse and no
// re-import — only incremental UPDATEs. Structural ops (T11) will mark the buffer
// "structurally deferred" instead of mirroring; T6 mirrors scalar sets only.

// mirrorKey identifies a normalized (table, column) cell family. All values for
// one key share a SQL type, so a flush emits one type-homogeneous UPDATE per key.
type mirrorKey struct {
	table  string
	column string
}

// mirrorColumn buffers pending writes for one (table, column): entry_name -> new
// value, last-write-wins, plus the column's DuckDB type for the flush CAST.
type mirrorColumn struct {
	sqlType string
	rows    map[string]any // entry_name -> value
}

// mirrorBuffer accumulates pending scalar mutations grouped by (table, column).
type mirrorBuffer struct {
	cols map[mirrorKey]*mirrorColumn
}

// record buffers one scalar set, keyed by entry_name within its (table, column)
// so N row-by-row sets of the same cell collapse to a single VALUES tuple.
func (b *mirrorBuffer) record(table, column, sqlType, entryName string, value any) {
	if b.cols == nil {
		b.cols = make(map[mirrorKey]*mirrorColumn)
	}
	key := mirrorKey{table: table, column: column}
	col := b.cols[key]
	if col == nil {
		col = &mirrorColumn{sqlType: sqlType, rows: make(map[string]any)}
		b.cols[key] = col
	}
	col.rows[entryName] = value
}

func (b *mirrorBuffer) empty() bool { return len(b.cols) == 0 }

func (b *mirrorBuffer) reset() { b.cols = nil }

// recordMirror buffers a pending scalar mutation and marks DuckDB dirty so the
// next query/aggregate flushes it.
func (ls *LoadedSave) recordMirror(table, column, sqlType, entryName string, value any) {
	ls.mirror.record(table, column, sqlType, entryName, value)
	ls.mirrorDirty = true
}

// flushMirrorColumn applies one (table, column)'s buffered writes as a single
// set-based UPDATE: the new values arrive as an inline VALUES relation joined on
// entry_name, so the whole column updates in one statement regardless of row
// count. The val is CAST to the column's DuckDB type so untyped placeholders in
// VALUES resolve unambiguously.
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

	args := make([]any, 0, len(col.rows)*2+1)
	i := 0
	for entryName, val := range col.rows {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("(?, CAST(? AS " + castType + "))")
		args = append(args, entryName, val)
		i++
	}
	b.WriteString(") AS v(entry_name, val) WHERE ")
	b.WriteString(quoteIdent(key.table) + ".save_id = ? AND ")
	b.WriteString(quoteIdent(key.table) + ".entry_name = v.entry_name")
	args = append(args, ls.saveID)

	if _, err := ls.db.ExecContext(ctx, b.String(), args...); err != nil {
		return fmt.Errorf("mirror %s.%s: %w", key.table, key.column, err)
	}
	return nil
}
