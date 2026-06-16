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
