package thebibites

import (
	"fmt"
	"reflect"

	"go.starlark.net/starlark"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

// toStarlark converts a reflected Go scalar (as read off a normalized row) into
// a Starlark value. Named string/number kinds (e.g. ScalarType, EntryKind) are
// handled by underlying Kind, so the conversion stays a single site shared by
// every entity reader.
func toStarlark(v reflect.Value) (starlark.Value, error) {
	switch v.Kind() {
	case reflect.Float32, reflect.Float64:
		return starlark.Float(v.Float()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return starlark.MakeInt64(v.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return starlark.MakeUint64(v.Uint()), nil
	case reflect.Bool:
		return starlark.Bool(v.Bool()), nil
	case reflect.String:
		return starlark.String(v.String()), nil
	default:
		return nil, fmt.Errorf("unsupported attribute type %s", v.Type())
	}
}

// fromSQLValue converts a value scanned from a DuckDB result column into a
// Starlark value. It is the counterpart of toStarlark for the analytics path:
// rows come back as driver scalars (the duckdb driver scans into the concrete Go
// types below), and SQL NULL arrives as a nil interface, surfaced as None. It
// folds in the type coercion duckdb.normalizeSQLScanValue performs internally
// ([]byte->string, narrow ints/floats->int64/float64) so a single converter
// serves both raw save.sql rows and aggregate scalars.
func fromSQLValue(v any) (starlark.Value, error) {
	switch x := v.(type) {
	case nil:
		return starlark.None, nil
	case bool:
		return starlark.Bool(x), nil
	case string:
		return starlark.String(x), nil
	case []byte:
		return starlark.String(x), nil
	case int:
		return starlark.MakeInt64(int64(x)), nil
	case int8:
		return starlark.MakeInt64(int64(x)), nil
	case int16:
		return starlark.MakeInt64(int64(x)), nil
	case int32:
		return starlark.MakeInt64(int64(x)), nil
	case int64:
		return starlark.MakeInt64(x), nil
	case uint:
		return starlark.MakeUint64(uint64(x)), nil
	case uint8:
		return starlark.MakeUint64(uint64(x)), nil
	case uint16:
		return starlark.MakeUint64(uint64(x)), nil
	case uint32:
		return starlark.MakeUint64(uint64(x)), nil
	case uint64:
		return starlark.MakeUint64(x), nil
	case float32:
		return starlark.Float(float64(x)), nil
	case float64:
		return starlark.Float(x), nil
	default:
		return nil, fmt.Errorf("unsupported SQL value type %T", v)
	}
}

// geneValueToStarlark converts a typed gene cell into a Starlark value following
// the gene's ScalarType. Null genes read as None.
func geneValueToStarlark(g tb.GeneRow) starlark.Value {
	switch g.Type {
	case tb.ScalarNumber:
		return starlark.Float(g.NumberValue)
	case tb.ScalarBool:
		return starlark.Bool(g.BoolValue)
	case tb.ScalarString:
		return starlark.String(g.StringValue)
	default:
		return starlark.None
	}
}
