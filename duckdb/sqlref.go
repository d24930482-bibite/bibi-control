package duckdb

import (
	"database/sql"
	"fmt"
	"math"
	"strconv"
	"strings"

	mutator "github.com/asemones/bibicontrol/savemutator/thebibites"
)

// SQLRefScanSpec describes the normalized SQL cell being read from query rows.
// Table and Column identify the target cell; ValueColumn defaults to Column
// when the current value is selected under the same name.
type SQLRefScanSpec struct {
	Table       string
	Column      string
	ValueColumn string
}

// SQLRefRow is one query result converted into a mutator ref and the current
// selected cell value to use as an expected-value guard.
type SQLRefRow struct {
	Ref          mutator.SQLValueRef
	CurrentValue any
}

// ScanSQLRefs converts DuckDB query rows into SQLValueRef values. It infers
// only locator fields from returned column names; JSON path resolution remains
// in savemutator/thebibites.ResolveSQLValueRef.
func ScanSQLRefs(rows *sql.Rows, spec SQLRefScanSpec) ([]SQLRefRow, error) {
	if rows == nil {
		return nil, fmt.Errorf("duckdb: rows is nil")
	}
	if spec.Table == "" {
		return nil, fmt.Errorf("duckdb: SQL ref table is required")
	}
	if spec.Column == "" {
		return nil, fmt.Errorf("duckdb: SQL ref column is required")
	}

	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	columnIndex, err := scanColumnIndex(columns)
	if err != nil {
		return nil, err
	}

	valueColumn := spec.ValueColumn
	if valueColumn == "" {
		valueColumn = spec.Column
	}
	valueIndex, ok := columnIndex[sqlColumnKey(valueColumn)]
	if !ok {
		return nil, fmt.Errorf("duckdb: current value column %q is required", valueColumn)
	}

	values := make([]any, len(columns))
	targets := make([]any, len(columns))
	out := make([]SQLRefRow, 0)
	rowIndex := 0
	for rows.Next() {
		for i := range values {
			values[i] = nil
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			return nil, fmt.Errorf("duckdb: scan SQL ref row %d: %w", rowIndex, err)
		}

		ref, err := sqlRefFromRow(spec, columnIndex, values)
		if err != nil {
			return nil, fmt.Errorf("duckdb: SQL ref row %d: %w", rowIndex, err)
		}
		if _, _, err := mutator.ResolveSQLValueRef(ref); err != nil {
			return nil, fmt.Errorf("duckdb: SQL ref row %d: %w", rowIndex, err)
		}

		out = append(out, SQLRefRow{
			Ref:          ref,
			CurrentValue: normalizeSQLScanValue(values[valueIndex]),
		})
		rowIndex++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func scanColumnIndex(columns []string) (map[string]int, error) {
	out := make(map[string]int, len(columns))
	for i, column := range columns {
		key := sqlColumnKey(column)
		if key == "" {
			return nil, fmt.Errorf("duckdb: result column %d has empty name", i)
		}
		if _, exists := out[key]; exists {
			return nil, fmt.Errorf("duckdb: duplicate result column %q", column)
		}
		out[key] = i
	}
	return out, nil
}

func sqlRefFromRow(spec SQLRefScanSpec, columns map[string]int, values []any) (mutator.SQLValueRef, error) {
	ref := mutator.SQLValueRef{
		Table:  spec.Table,
		Column: spec.Column,
	}

	if value, ok, err := rowString(columns, values, "entry_name"); err != nil {
		return ref, err
	} else if ok {
		ref.EntryName = value
	}
	bodyIDOK := false
	if value, ok, err := rowInt64(columns, values, "body_id"); err != nil {
		return ref, err
	} else if ok {
		ref.BodyID = value
		bodyIDOK = true
	}
	if value, ok, err := rowBool(columns, values, "has_body_id"); err != nil {
		return ref, err
	} else if ok && value {
		if !bodyIDOK {
			return ref, fmt.Errorf("has_body_id is true but body_id is missing")
		}
		ref.HasBodyID = true
	}
	eggIDOK := false
	if value, ok, err := rowInt64(columns, values, "egg_id"); err != nil {
		return ref, err
	} else if ok {
		ref.EggID = value
		eggIDOK = true
	}
	if value, ok, err := rowBool(columns, values, "has_egg_id"); err != nil {
		return ref, err
	} else if ok && value {
		if !eggIDOK {
			return ref, fmt.Errorf("has_egg_id is true but egg_id is missing")
		}
		ref.HasEggID = true
	}

	if value, ok, err := rowString(columns, values, "owner_kind"); err != nil {
		return ref, err
	} else if ok {
		ref.OwnerKind = value
	}
	if value, ok, err := rowString(columns, values, "owner_id"); err != nil {
		return ref, err
	} else if ok {
		ref.OwnerID = value
	}
	if value, ok, err := rowString(columns, values, "path"); err != nil {
		return ref, err
	} else if ok {
		ref.Path = value
	}

	if value, ok, err := rowInt(columns, values, "content_index"); err != nil {
		return ref, err
	} else if ok {
		ref.ContentIndex = value
		ref.HasContentIndex = true
	}
	if value, ok, err := rowInt(columns, values, "group_index"); err != nil {
		return ref, err
	} else if ok {
		ref.GroupIndex = value
		ref.HasGroupIndex = true
	}
	if value, ok, err := rowInt(columns, values, "group_pellet_index"); err != nil {
		return ref, err
	} else if ok {
		ref.GroupPelletIndex = value
		ref.HasGroupPelletIndex = true
	}
	if value, ok, err := rowString(columns, values, "zone"); err != nil {
		return ref, err
	} else if ok {
		ref.Zone = value
		ref.HasZone = true
	}
	if value, ok, err := rowInt(columns, values, "pheromone_index"); err != nil {
		return ref, err
	} else if ok {
		ref.PheromoneIndex = value
		ref.HasPheromoneIndex = true
	}
	if value, ok, err := rowInt(columns, values, "node_row_index"); err != nil {
		return ref, err
	} else if ok {
		ref.NodeRowIndex = value
		ref.HasNodeRowIndex = true
	}
	if value, ok, err := rowInt(columns, values, "synapse_row_index"); err != nil {
		return ref, err
	} else if ok {
		ref.SynapseRowIndex = value
		ref.HasSynapseRowIndex = true
	}
	if value, ok, err := rowInt(columns, values, "zone_index"); err != nil {
		return ref, err
	} else if ok {
		ref.ZoneIndex = value
		ref.HasZoneIndex = true
	}
	zoneIDOK := false
	if value, ok, err := rowInt64(columns, values, "zone_id"); err != nil {
		return ref, err
	} else if ok {
		ref.ZoneID = value
		zoneIDOK = true
	}
	if value, ok, err := rowBool(columns, values, "has_zone_id"); err != nil {
		return ref, err
	} else if ok && value {
		if !zoneIDOK {
			return ref, fmt.Errorf("has_zone_id is true but zone_id is missing")
		}
		ref.HasZoneID = true
	}

	return ref, nil
}

func rowString(columns map[string]int, values []any, column string) (string, bool, error) {
	value, ok := rowValue(columns, values, column)
	if !ok {
		return "", false, nil
	}
	if value == nil {
		return "", false, nil
	}
	switch v := normalizeSQLScanValue(value).(type) {
	case string:
		return v, true, nil
	default:
		return "", false, fmt.Errorf("column %q is %T, want string", column, value)
	}
}

func rowBool(columns map[string]int, values []any, column string) (bool, bool, error) {
	value, ok := rowValue(columns, values, column)
	if !ok {
		return false, false, nil
	}
	if value == nil {
		return false, false, nil
	}
	switch v := normalizeSQLScanValue(value).(type) {
	case bool:
		return v, true, nil
	default:
		return false, false, fmt.Errorf("column %q is %T, want bool", column, value)
	}
}

func rowInt(columns map[string]int, values []any, column string) (int, bool, error) {
	value, ok, err := rowInt64(columns, values, column)
	if !ok || err != nil {
		return 0, ok, err
	}
	if value < math.MinInt || value > math.MaxInt {
		return 0, false, fmt.Errorf("column %q value %d overflows int", column, value)
	}
	return int(value), true, nil
}

func rowInt64(columns map[string]int, values []any, column string) (int64, bool, error) {
	value, ok := rowValue(columns, values, column)
	if !ok {
		return 0, false, nil
	}
	if value == nil {
		return 0, false, nil
	}
	normalized := normalizeSQLScanValue(value)
	switch v := normalized.(type) {
	case int64:
		return v, true, nil
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return 0, false, fmt.Errorf("column %q value %q is not an integer: %w", column, v, err)
		}
		return n, true, nil
	default:
		return 0, false, fmt.Errorf("column %q is %T, want integer", column, value)
	}
}

func rowValue(columns map[string]int, values []any, column string) (any, bool) {
	i, ok := columns[sqlColumnKey(column)]
	if !ok {
		return nil, false
	}
	return values[i], true
}

func normalizeSQLScanValue(value any) any {
	switch v := value.(type) {
	case []byte:
		return string(v)
	case int:
		return int64(v)
	case int8:
		return int64(v)
	case int16:
		return int64(v)
	case int32:
		return int64(v)
	case int64:
		return v
	case uint:
		if uint64(v) <= uint64(math.MaxInt64) {
			return int64(v)
		}
		return v
	case uint8:
		return int64(v)
	case uint16:
		return int64(v)
	case uint32:
		return int64(v)
	case uint64:
		if v <= uint64(math.MaxInt64) {
			return int64(v)
		}
		return v
	case float32:
		return float64(v)
	default:
		return value
	}
}

func sqlColumnKey(column string) string {
	return strings.ToLower(strings.TrimSpace(column))
}
