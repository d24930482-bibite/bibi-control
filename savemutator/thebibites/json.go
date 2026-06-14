package thebibites

import (
	"encoding/json"
	"math/big"
	"reflect"
	"strconv"
)

var utf8BOM = []byte{0xef, 0xbb, 0xbf}

func encodeJSON(value any, withBOM bool) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if !withBOM {
		return raw, nil
	}
	out := make([]byte, 0, len(utf8BOM)+len(raw))
	out = append(out, utf8BOM...)
	out = append(out, raw...)
	return out, nil
}

func cloneJSON(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, value := range v {
			out[key] = cloneJSON(value)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, value := range v {
			out[i] = cloneJSON(value)
		}
		return out
	default:
		return v
	}
}

func normalizeJSONValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, value := range v {
			out[key] = normalizeJSONValue(value)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, value := range v {
			out[i] = normalizeJSONValue(value)
		}
		return out
	case []string:
		out := make([]any, len(v))
		for i, value := range v {
			out[i] = value
		}
		return out
	case []int:
		out := make([]any, len(v))
		for i, value := range v {
			out[i] = value
		}
		return out
	case []int64:
		out := make([]any, len(v))
		for i, value := range v {
			out[i] = value
		}
		return out
	case []float64:
		out := make([]any, len(v))
		for i, value := range v {
			out[i] = value
		}
		return out
	case []bool:
		out := make([]any, len(v))
		for i, value := range v {
			out[i] = value
		}
		return out
	default:
		return v
	}
}

func jsonValuesEqual(a, b any) bool {
	if ar, ok := numberRat(a); ok {
		if br, ok := numberRat(b); ok {
			return ar.Cmp(br) == 0
		}
	}
	if av, ok := normalizeComparableScalar(a); ok {
		if bv, ok := normalizeComparableScalar(b); ok {
			return reflect.DeepEqual(av, bv)
		}
	}
	return reflect.DeepEqual(a, b)
}

func normalizeComparableScalar(value any) (any, bool) {
	switch v := value.(type) {
	case nil, bool, string:
		return v, true
	default:
		return nil, false
	}
}

func numberRat(value any) (*big.Rat, bool) {
	switch v := value.(type) {
	case json.Number:
		return ratFromString(v.String())
	case int:
		return big.NewRat(int64(v), 1), true
	case int8:
		return big.NewRat(int64(v), 1), true
	case int16:
		return big.NewRat(int64(v), 1), true
	case int32:
		return big.NewRat(int64(v), 1), true
	case int64:
		return big.NewRat(v, 1), true
	case uint:
		return ratFromUint64(uint64(v))
	case uint8:
		return ratFromUint64(uint64(v))
	case uint16:
		return ratFromUint64(uint64(v))
	case uint32:
		return ratFromUint64(uint64(v))
	case uint64:
		return ratFromUint64(v)
	case float32:
		return ratFromFloat(float64(v))
	case float64:
		return ratFromFloat(v)
	default:
		return nil, false
	}
}

func ratFromString(value string) (*big.Rat, bool) {
	rat, ok := new(big.Rat).SetString(value)
	return rat, ok
}

func ratFromUint64(value uint64) (*big.Rat, bool) {
	rat := new(big.Rat).SetUint64(value)
	return rat, true
}

func ratFromFloat(value float64) (*big.Rat, bool) {
	return ratFromString(strconv.FormatFloat(value, 'g', -1, 64))
}
