package thebibites

import (
	"bytes"
	"fmt"
	"io"
	"strconv"

	// goccy/go-json is a drop-in for encoding/json with a much faster
	// decode-to-any path (the parser's hot path). It is imported as `json` for
	// the whole file so the decoder's UseNumber() output type (json.Number) and
	// the type-switch cases / json.Marshal calls below all refer to the SAME
	// package consistently. goccy preserves UseNumber/json.Number semantics, the
	// strict-EOF trailing-token check, and canonical Marshal output for the
	// scalar types rawJSON handles; the scalar/RawJSON fixture tests are the
	// arbiter of that equivalence.
	json "github.com/goccy/go-json"
)

var utf8BOM = []byte{0xef, 0xbb, 0xbf}

func decodeJSONWithBOM(raw []byte) (any, bool, error) {
	hasBOM := bytes.HasPrefix(raw, utf8BOM)
	if hasBOM {
		raw = raw[len(utf8BOM):]
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, hasBOM, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, hasBOM, fmt.Errorf("unexpected extra JSON value")
		}
		return nil, hasBOM, err
	}
	return value, hasBOM, nil
}

func asMap(value any) (map[string]any, bool) {
	m, ok := value.(map[string]any)
	return m, ok
}

func mapAt(m map[string]any, key string) (map[string]any, bool) {
	if m == nil {
		return nil, false
	}
	return asMap(m[key])
}

func listAt(m map[string]any, key string) ([]any, bool) {
	if m == nil {
		return nil, false
	}
	list, ok := m[key].([]any)
	return list, ok
}

func stringAt(m map[string]any, key string) (string, bool) {
	if m == nil {
		return "", false
	}
	s, ok := m[key].(string)
	return s, ok
}

func boolAt(m map[string]any, key string) (bool, bool) {
	if m == nil {
		return false, false
	}
	b, ok := m[key].(bool)
	return b, ok
}

func floatAt(m map[string]any, key string) (float64, bool) {
	if m == nil {
		return 0, false
	}
	return toFloat(m[key])
}

func intAt(m map[string]any, key string) (int64, bool) {
	if m == nil {
		return 0, false
	}
	return toInt(m[key])
}

func toFloat(value any) (float64, bool) {
	switch v := value.(type) {
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint64:
		return float64(v), true
	default:
		return 0, false
	}
}

func toInt(value any) (int64, bool) {
	switch v := value.(type) {
	case json.Number:
		i, err := v.Int64()
		if err == nil {
			return i, true
		}
		f, err := v.Float64()
		if err != nil {
			return 0, false
		}
		return int64(f), true
	case float64:
		return int64(v), true
	case float32:
		return int64(v), true
	case int:
		return int64(v), true
	case int64:
		return v, true
	case uint64:
		return int64(v), true
	default:
		return 0, false
	}
}

func scalarParts(value any) (ScalarType, float64, string, bool, string, bool) {
	switch v := value.(type) {
	case nil:
		return ScalarNull, 0, "", false, "null", true
	case bool:
		return ScalarBool, 0, "", v, strconv.FormatBool(v), true
	case string:
		return ScalarString, 0, v, false, rawJSON(v), true
	case json.Number:
		f, ok := toFloat(v)
		if !ok {
			return "", 0, "", false, "", false
		}
		return ScalarNumber, f, "", false, v.String(), true
	default:
		if f, ok := toFloat(v); ok {
			return ScalarNumber, f, "", false, strconv.FormatFloat(f, 'g', -1, 64), true
		}
		return "", 0, "", false, "", false
	}
}

func rawJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(raw)
}
