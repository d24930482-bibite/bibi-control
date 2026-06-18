package thebibites

import (
	"fmt"
	"sort"
	"strconv"

	// Imported as `json` so the `case json.Number:` type-switch matches the
	// concrete type the goccy decoder emits under UseNumber() (see
	// json_helpers.go). Mixing encoding/json.Number here would silently route
	// every number into the default branch and change RawJSON output.
	json "github.com/goccy/go-json"
)

func collectScalars(entryName, ownerKind, ownerID, prefix string, value any) []Scalar {
	scalars := make([]Scalar, 0, scalarCount(value))
	// path is a reusable byte buffer seeded with prefix; children append onto it
	// and truncate back, so leaf paths are materialized into strings exactly once
	// instead of via fmt.Sprintf / repeated concatenation at every tree level.
	path := make([]byte, 0, len(prefix)+32)
	path = append(path, prefix...)
	collectScalarsInto(&scalars, entryName, ownerKind, ownerID, path, value)
	return scalars
}

// scalarCount counts the leaf scalars a value will produce so collectScalars can
// size its result slice once instead of growing it by repeated reallocation.
func scalarCount(value any) int {
	switch v := value.(type) {
	case map[string]any:
		n := 0
		for _, child := range v {
			n += scalarCount(child)
		}
		return n
	case []any:
		n := 0
		for _, item := range v {
			n += scalarCount(item)
		}
		return n
	default:
		return 1
	}
}

// collectScalarsInto walks value depth-first, appending leaf scalars. path is the
// JSON path accumulated so far as raw bytes; each recursion appends its own
// segment and restores the buffer length on return (so a single backing array is
// reused for the whole subtree). The string for a path is allocated only at a
// leaf, matching the previous fmt.Sprintf-built paths byte-for-byte.
func collectScalarsInto(scalars *[]Scalar, entryName, ownerKind, ownerID string, path []byte, value any) {
	switch v := value.(type) {
	case map[string]any:
		// Most JSON objects in a save are small (brain nodes ~9 keys, pellets ~5),
		// so collect keys into a stack-backed array to avoid a heap allocation per
		// map node; fall back to the heap only for unusually wide objects.
		var keyBuf [16]string
		var keys []string
		if len(v) <= len(keyBuf) {
			keys = keyBuf[:0]
		} else {
			keys = make([]string, 0, len(v))
		}
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		base := len(path)
		for _, key := range keys {
			path = path[:base]
			if base != 0 {
				path = append(path, '.')
			}
			path = append(path, key...)
			collectScalarsInto(scalars, entryName, ownerKind, ownerID, path, v[key])
		}
	case []any:
		base := len(path)
		for i, item := range v {
			path = path[:base]
			path = append(path, '[')
			path = strconv.AppendInt(path, int64(i), 10)
			path = append(path, ']')
			collectScalarsInto(scalars, entryName, ownerKind, ownerID, path, item)
		}
	case nil:
		*scalars = append(*scalars, Scalar{
			EntryName: entryName,
			OwnerKind: ownerKind,
			OwnerID:   ownerID,
			Path:      string(path),
			Type:      ScalarNull,
			RawJSON:   "null",
		})
	case bool:
		*scalars = append(*scalars, Scalar{
			EntryName: entryName,
			OwnerKind: ownerKind,
			OwnerID:   ownerID,
			Path:      string(path),
			Type:      ScalarBool,
			BoolValue: v,
			RawJSON:   strconv.FormatBool(v),
		})
	case string:
		raw, _ := json.Marshal(v)
		*scalars = append(*scalars, Scalar{
			EntryName:   entryName,
			OwnerKind:   ownerKind,
			OwnerID:     ownerID,
			Path:        string(path),
			Type:        ScalarString,
			StringValue: v,
			RawJSON:     string(raw),
		})
	case json.Number:
		f, ok := toFloat(v)
		raw := v.String()
		if !ok {
			raw = ""
		}
		*scalars = append(*scalars, Scalar{
			EntryName:   entryName,
			OwnerKind:   ownerKind,
			OwnerID:     ownerID,
			Path:        string(path),
			Type:        ScalarNumber,
			NumberValue: f,
			RawJSON:     raw,
		})
	default:
		if f, ok := toFloat(v); ok {
			*scalars = append(*scalars, Scalar{
				EntryName:   entryName,
				OwnerKind:   ownerKind,
				OwnerID:     ownerID,
				Path:        string(path),
				Type:        ScalarNumber,
				NumberValue: f,
				RawJSON:     strconv.FormatFloat(f, 'g', -1, 64),
			})
			return
		}
		raw, _ := json.Marshal(v)
		*scalars = append(*scalars, Scalar{
			EntryName:   entryName,
			OwnerKind:   ownerKind,
			OwnerID:     ownerID,
			Path:        string(path),
			Type:        ScalarString,
			StringValue: fmt.Sprint(v),
			RawJSON:     string(raw),
		})
	}
}

func ownerIDFromInt(id int64, ok bool, fallback string) string {
	if !ok {
		return fallback
	}
	return strconv.FormatInt(id, 10)
}
