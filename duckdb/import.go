// Package duckdb loads normalized save projections into DuckDB for analytics.
package duckdb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"sort"
	"strings"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"

	duckdbgo "github.com/duckdb/duckdb-go/v2"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Open opens a DuckDB database. Pass an empty path for an in-memory database.
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("duckdb", path)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// OpenAndImport opens a DuckDB database, applies migrations, and imports save.
// The caller owns the returned database handle.
func OpenAndImport(ctx context.Context, path string, save tb.ExtractedSave) (*sql.DB, error) {
	db, err := Open(path)
	if err != nil {
		return nil, err
	}
	if err := ImportExtractedSave(ctx, db, save); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// ApplyMigrations creates the DuckDB schema needed for ExtractedSave imports.
func ApplyMigrations(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("duckdb: db is nil")
	}

	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		path := "migrations/" + entry.Name()
		sqlBytes, err := migrationFiles.ReadFile(path)
		if err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, string(sqlBytes)); err != nil {
			return fmt.Errorf("duckdb migration %s: %w", entry.Name(), err)
		}
	}
	return nil
}

// ImportExtractedSave applies migrations and replaces any existing rows with
// the same save_id before inserting every row family in save.
func ImportExtractedSave(ctx context.Context, db *sql.DB, save tb.ExtractedSave) error {
	if err := ApplyMigrations(ctx, db); err != nil {
		return err
	}
	return ReplaceExtractedSave(ctx, db, save)
}

// ReplaceExtractedSave replaces rows for save.Archive.SaveID with save's
// normalized table rows. Archive entry bytes remain outside DuckDB.
func ReplaceExtractedSave(ctx context.Context, db *sql.DB, save tb.ExtractedSave) error {
	if db == nil {
		return fmt.Errorf("duckdb: db is nil")
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN TRANSACTION"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	if err := deleteSaveRows(ctx, conn, save.Archive.SaveID); err != nil {
		return err
	}
	if err := insertExtractedSave(ctx, conn, save); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}

type execContext interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func deleteSaveRows(ctx context.Context, execer execContext, saveID string) error {
	for _, table := range allTables {
		query := fmt.Sprintf("DELETE FROM %s WHERE save_id = ?", QuoteIdent(table))
		if _, err := execer.ExecContext(ctx, query, saveID); err != nil {
			return fmt.Errorf("delete %s rows for save_id %q: %w", table, saveID, err)
		}
	}
	return nil
}

type fieldSpec struct {
	field  string
	column string
}

var allTables = normalizedTableNames()

func normalizedTableNames() []string {
	out := make([]string, len(tb.NormalizedTables))
	for i, table := range tb.NormalizedTables {
		out[i] = table.Table
	}
	return out
}

func insertExtractedSave(ctx context.Context, conn *sql.Conn, save tb.ExtractedSave) error {
	saveValue := reflect.ValueOf(save)
	for _, table := range tb.NormalizedTables {
		if err := insertExtractedTable(ctx, conn, saveValue, table); err != nil {
			return err
		}
	}
	return nil
}

func insertExtractedTable(ctx context.Context, conn *sql.Conn, saveValue reflect.Value, table tb.NormalizedTableSpec) error {
	rows := saveValue.FieldByName(table.SaveField)
	if !rows.IsValid() {
		return fmt.Errorf("duckdb: ExtractedSave missing field %s for table %s", table.SaveField, table.Table)
	}

	switch rows.Kind() {
	case reflect.Pointer:
		if rows.IsNil() {
			if table.Optional {
				return nil
			}
			return fmt.Errorf("duckdb: %s row is nil", table.Table)
		}
	case reflect.Slice:
		if rows.Len() == 0 {
			return nil
		}
	case reflect.Struct:
	default:
		return fmt.Errorf("duckdb: ExtractedSave.%s is %s, want struct, pointer, or slice", table.SaveField, rows.Kind())
	}

	fields := fieldSpecs(table.Fields)
	return appendRows(ctx, conn, table.Table, fields, rows)
}

func fieldSpecs(fields []tb.NormalizedFieldSpec) []fieldSpec {
	if len(fields) == 0 {
		return nil
	}
	out := make([]fieldSpec, len(fields))
	for i, field := range fields {
		out[i] = fieldSpec{field: field.Field, column: field.Column}
	}
	return out
}

func appendRows(ctx context.Context, conn *sql.Conn, table string, fields []fieldSpec, rows reflect.Value) error {
	if rows.Kind() != reflect.Slice {
		single := reflect.MakeSlice(reflect.SliceOf(rows.Type()), 1, 1)
		single.Index(0).Set(rows)
		rows = single
	}

	// Resolve struct field names to index paths ONCE per table, not per row.
	// reflect.FieldByName is a linear string-compared scan over the struct's
	// fields; doing it per row turns into tens of millions of scans on a large
	// save. FieldByIndex with a precomputed []int is a direct offset lookup.
	plan, err := buildFieldIndices(rows.Type().Elem(), fields)
	if err != nil {
		return fmt.Errorf("duckdb: %s field plan: %w", table, err)
	}

	columns := fieldColumns(fields)

	// Reused across every row: AppendRow consumes the values synchronously into
	// the appender's column chunk, so the backing array is safe to refill.
	buf := make([]driver.Value, len(fields))

	return conn.Raw(func(rawConn any) error {
		driverConn, ok := rawConn.(driver.Conn)
		if !ok {
			return fmt.Errorf("duckdb: raw connection is %T, want driver.Conn", rawConn)
		}

		appender, err := duckdbgo.NewAppenderWithColumns(driverConn, "", "", table, columns)
		if err != nil {
			return fmt.Errorf("create appender %s: %w", table, err)
		}
		closed := false
		defer func() {
			if !closed {
				_ = appender.Clear()
				_ = appender.Close()
			}
		}()

		for i := 0; i < rows.Len(); i++ {
			if err := rowValuesInto(buf, table, rows.Index(i), plan); err != nil {
				return err
			}
			if err := appender.AppendRow(buf...); err != nil {
				clearErr := appender.Clear()
				closeErr := appender.Close()
				closed = true
				return fmt.Errorf("append %s row %d: %w", table, i, errors.Join(err, clearErr, closeErr))
			}
		}
		if err := appender.CloseWithCancel(ctx); err != nil {
			closed = true
			return fmt.Errorf("close appender %s: %w", table, err)
		}
		closed = true
		return nil
	})
}

func fieldColumns(fields []fieldSpec) []string {
	columns := make([]string, len(fields))
	for i, field := range fields {
		columns[i] = field.column
	}
	return columns
}

// buildFieldIndices resolves each fieldSpec to its struct field index path
// against the row element type. Done once per table; the result is reused for
// every row via FieldByIndex.
func buildFieldIndices(elem reflect.Type, fields []fieldSpec) ([][]int, error) {
	for elem.Kind() == reflect.Pointer {
		elem = elem.Elem()
	}
	if elem.Kind() != reflect.Struct {
		return nil, fmt.Errorf("element type %s is not a struct", elem)
	}
	plan := make([][]int, len(fields))
	for i, spec := range fields {
		sf, ok := elem.FieldByName(spec.field)
		if !ok {
			return nil, fmt.Errorf("missing field %s", spec.field)
		}
		// Copy: StructField.Index may share backing storage; we hold this long-term.
		idx := make([]int, len(sf.Index))
		copy(idx, sf.Index)
		plan[i] = idx
	}
	return plan, nil
}

// rowValuesInto fills buf with the row's field values using a precomputed index
// plan. buf must have len(plan) capacity and is overwritten in place.
func rowValuesInto(buf []driver.Value, table string, row reflect.Value, plan [][]int) error {
	for row.Kind() == reflect.Pointer {
		if row.IsNil() {
			return fmt.Errorf("duckdb: %s row is nil", table)
		}
		row = row.Elem()
	}
	if row.Kind() != reflect.Struct {
		return fmt.Errorf("duckdb: %s row is %s, want struct", table, row.Kind())
	}
	for i, idx := range plan {
		value, err := fieldValue(row.FieldByIndex(idx))
		if err != nil {
			return fmt.Errorf("duckdb: %s field %d: %w", table, i, err)
		}
		buf[i] = value
	}
	return nil
}

func fieldValue(value reflect.Value) (driver.Value, error) {
	for value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil, nil
		}
		value = value.Elem()
	}

	switch value.Kind() {
	case reflect.Bool:
		return value.Bool(), nil
	case reflect.String:
		return value.String(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return value.Int(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if value.Uint() > maxDriverInt64 {
			return nil, fmt.Errorf("uint value %d overflows int64", value.Uint())
		}
		return int64(value.Uint()), nil
	case reflect.Float32, reflect.Float64:
		return value.Float(), nil
	default:
		return nil, fmt.Errorf("unsupported kind %s", value.Kind())
	}
}

// QuoteIdent double-quotes a SQL identifier for DuckDB, escaping embedded quotes.
// Exported so the analytics query builder in script/thebibites reuses the exact
// same identifier quoting instead of keeping a private copy.
func QuoteIdent(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

const maxDriverInt64 = uint64(1<<63 - 1)
