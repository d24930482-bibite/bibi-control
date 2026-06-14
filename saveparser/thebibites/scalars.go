package thebibites

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

func collectScalars(entryName, ownerKind, ownerID, prefix string, value any) []Scalar {
	scalars := make([]Scalar, 0)
	collectScalarsInto(&scalars, entryName, ownerKind, ownerID, prefix, value)
	return scalars
}

func collectScalarsInto(scalars *[]Scalar, entryName, ownerKind, ownerID, prefix string, value any) {
	switch v := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			childPath := key
			if prefix != "" {
				childPath = prefix + "." + key
			}
			collectScalarsInto(scalars, entryName, ownerKind, ownerID, childPath, v[key])
		}
	case []any:
		for i, item := range v {
			childPath := fmt.Sprintf("[%d]", i)
			if prefix != "" {
				childPath = fmt.Sprintf("%s[%d]", prefix, i)
			}
			collectScalarsInto(scalars, entryName, ownerKind, ownerID, childPath, item)
		}
	case nil:
		*scalars = append(*scalars, Scalar{
			EntryName: entryName,
			OwnerKind: ownerKind,
			OwnerID:   ownerID,
			Path:      prefix,
			Type:      ScalarNull,
			RawJSON:   "null",
		})
	case bool:
		*scalars = append(*scalars, Scalar{
			EntryName: entryName,
			OwnerKind: ownerKind,
			OwnerID:   ownerID,
			Path:      prefix,
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
			Path:        prefix,
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
			Path:        prefix,
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
				Path:        prefix,
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
			Path:        prefix,
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
