package thebibites

import (
	"fmt"
	"math"
	"math/big"
	"reflect"

	"go.starlark.net/starlark"

	"github.com/asemones/bibicontrol/duckdb"
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
// rows come back as driver scalars, and SQL NULL arrives as a nil interface,
// surfaced as None. The driver-scalar coercion ([]byte->string, narrow signed
// ints->int64, unsigned ints->int64/uint64, float32->float64) is delegated to
// duckdb.NormalizeSQLScanValue so a single normalizer serves both the SQLValueRef
// scan path and this converter; this function only adds the Starlark mapping plus
// the *big.Int (HUGEINT) case the normalizer passes through untouched.
func fromSQLValue(v any) (starlark.Value, error) {
	switch x := duckdb.NormalizeSQLScanValue(v).(type) {
	case nil:
		return starlark.None, nil
	case bool:
		return starlark.Bool(x), nil
	case string:
		return starlark.String(x), nil
	case int64:
		return starlark.MakeInt64(x), nil
	case uint64:
		// Only a uint64 that overflows int64 survives normalization as uint64;
		// smaller unsigned values arrive as int64 above. MakeUint64 preserves the
		// full magnitude either way.
		return starlark.MakeUint64(x), nil
	case float64:
		return starlark.Float(x), nil
	case *big.Int:
		// DuckDB returns HUGEINT (128-bit) for sum() over an integer column (and
		// for raw HUGEINT columns); the duckdb driver scans those into *big.Int,
		// which the normalizer passes through unchanged. MakeBigInt yields an
		// arbitrary-precision Starlark Int.
		if x == nil {
			return starlark.None, nil
		}
		return starlark.MakeBigInt(x), nil
	default:
		return nil, fmt.Errorf("unsupported SQL value type %T", v)
	}
}

// fromStarlark converts a Starlark scalar into the Go scalar staged into a
// mutation (int64/float64/bool/string). It is the write-direction counterpart of
// toStarlark and rejects non-scalar values (lists, dicts, None) with a clean
// error. It does NOT validate that the scalar's type matches the target column —
// that (range/enum/type-match) is validateSet in guards.go; this only ensures the
// value can become a Go scalar at all.
func fromStarlark(v starlark.Value) (any, error) {
	switch x := v.(type) {
	case starlark.Int:
		n, ok := x.Int64()
		if !ok {
			return nil, fmt.Errorf("integer %s overflows int64", x.String())
		}
		return n, nil
	case starlark.Float:
		return float64(x), nil
	case starlark.Bool:
		return bool(x), nil
	case starlark.String:
		return string(x), nil
	default:
		return nil, fmt.Errorf("cannot set attribute to %s", v.Type())
	}
}

// goScalar reads a reflected Go scalar (off a normalized row) into a plain Go
// value, used to capture the current value for the stale-value guard. Numeric
// kinds collapse to int64/uint64/float64; comparison downstream is numeric-type
// agnostic (jsonValuesEqual via big.Rat), so the exact width does not matter.
func goScalar(v reflect.Value) (any, error) {
	switch v.Kind() {
	case reflect.Float32, reflect.Float64:
		return v.Float(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint(), nil
	case reflect.Bool:
		return v.Bool(), nil
	case reflect.String:
		return v.String(), nil
	default:
		return nil, fmt.Errorf("unsupported field type %s", v.Type())
	}
}

// setRowField coerces goVal (an int64/float64/bool/string from fromStarlark) to
// the kind of the row field at fieldIndex, writes it through to the in-memory row
// (so a later plain attribute read observes the change), and returns the coerced
// Go value to stage. It errors — never panics — on an incompatible kind. This is
// the only "typing" done at write time (memory safety, type fidelity of the staged
// JSON value); full value validation (range/enum/type-match) is validateSet in
// guards.go, run before this.
func setRowField(row reflect.Value, fieldIndex []int, goVal any) (any, error) {
	field := row.FieldByIndex(fieldIndex)
	if !field.CanSet() {
		return nil, fmt.Errorf("field is not settable")
	}
	switch field.Kind() {
	case reflect.Float32, reflect.Float64:
		f, ok := asFloat64(goVal)
		if !ok {
			return nil, fmt.Errorf("cannot assign %T to numeric field", goVal)
		}
		field.SetFloat(f)
		return field.Float(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, ok := asInt64(goVal)
		if !ok {
			return nil, fmt.Errorf("cannot assign %T to integer field", goVal)
		}
		field.SetInt(n)
		return field.Int(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, ok := asInt64(goVal)
		if !ok || n < 0 {
			return nil, fmt.Errorf("cannot assign %T to unsigned field", goVal)
		}
		field.SetUint(uint64(n))
		return field.Uint(), nil
	case reflect.Bool:
		b, ok := goVal.(bool)
		if !ok {
			return nil, fmt.Errorf("cannot assign %T to bool field", goVal)
		}
		field.SetBool(b)
		return b, nil
	case reflect.String:
		s, ok := goVal.(string)
		if !ok {
			return nil, fmt.Errorf("cannot assign %T to string field", goVal)
		}
		field.SetString(s)
		return s, nil
	default:
		return nil, fmt.Errorf("unsupported field kind %s", field.Kind())
	}
}

func asFloat64(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int64:
		return float64(x), true
	default:
		return 0, false
	}
}

func asInt64(v any) (int64, bool) {
	switch x := v.(type) {
	case int64:
		return x, true
	case float64:
		if x == math.Trunc(x) {
			return int64(x), true
		}
		return 0, false
	default:
		return 0, false
	}
}

// scalarValueColumn names, for a ScalarType, the typed DuckDB value column and its
// SQL type. Shared by gene and settings-value writes, whose rows both carry
// number/bool/string value columns selected by a ScalarType. Null/unknown is not
// settable (the JSON cell holds no scalar to guard or replace).
func scalarValueColumn(t tb.ScalarType) (column, sqlType string, err error) {
	switch t {
	case tb.ScalarNumber:
		return "number_value", "DOUBLE", nil
	case tb.ScalarBool:
		return "bool_value", "BOOLEAN", nil
	case tb.ScalarString:
		return "string_value", "TEXT", nil
	default:
		return "", "", fmt.Errorf("value of type %q is not settable", t)
	}
}

// applyScalarValue coerces goVal (from fromStarlark) to the type named by t,
// captures the current value (for the stale-value guard), writes the new value
// through the supplied gene/settings field pointers, and returns the old + staged
// values. validateValue(scalarTypeRule(t), …) must run first; the ok-checks here
// are the same memory-safety belt setRowField provides for entity scalars.
func applyScalarValue(t tb.ScalarType, goVal any, num *float64, bl *bool, str *string) (old, staged any, err error) {
	switch t {
	case tb.ScalarNumber:
		f, ok := asFloat64(goVal)
		if !ok {
			return nil, nil, fmt.Errorf("cannot assign %s to a number value", goScalarName(goVal))
		}
		old, *num, staged = *num, f, f
	case tb.ScalarBool:
		b, ok := goVal.(bool)
		if !ok {
			return nil, nil, fmt.Errorf("cannot assign %s to a boolean value", goScalarName(goVal))
		}
		old, *bl, staged = *bl, b, b
	case tb.ScalarString:
		s, ok := goVal.(string)
		if !ok {
			return nil, nil, fmt.Errorf("cannot assign %s to a string value", goScalarName(goVal))
		}
		old, *str, staged = *str, s, s
	default:
		return nil, nil, fmt.Errorf("value of type %q is not settable", t)
	}
	return old, staged, nil
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
