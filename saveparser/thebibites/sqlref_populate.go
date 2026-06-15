package thebibites

import (
	"reflect"
	"strconv"
	"strings"
)

// populateSQLRefFields fills every sqlref-tagged field of a normalized row
// directly from the entity's raw JSON, using the generated NormalizedTables
// metadata as the single source of each field's JSON location. This replaces
// hand-written per-field extraction so a field's path is declared exactly once
// (the sqlref tag) and used by both the read path (here) and the write path
// (the mutator's resolvers). rowPtr must be a pointer to the row struct; raw is
// the entity root the paths are relative to (e.g. a bibite/egg/pellet/zone JSON
// object). Missing or type-mismatched paths leave the field at its zero value,
// matching the previous floatAt/intAt/boolAt/stringAt behavior.
func populateSQLRefFields(rowPtr any, raw map[string]any, table string) {
	spec, ok := normalizedTableByName(table)
	if !ok || raw == nil {
		return
	}
	rv := reflect.ValueOf(rowPtr).Elem()
	for _, f := range spec.Fields {
		if f.SQLRefPath == "" {
			continue
		}
		value, ok := lookupJSONPath(raw, f.SQLRefPath)
		if !ok {
			continue
		}
		field := rv.FieldByName(f.Field)
		if !field.IsValid() || !field.CanSet() {
			continue
		}
		switch field.Kind() {
		case reflect.Float64, reflect.Float32:
			if x, ok := toFloat(value); ok {
				field.SetFloat(x)
			}
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			if x, ok := toInt(value); ok {
				field.SetInt(x)
			}
		case reflect.Bool:
			if b, ok := value.(bool); ok {
				field.SetBool(b)
			}
		case reflect.String:
			if s, ok := value.(string); ok {
				field.SetString(s)
			}
		}
	}
}

func normalizedTableByName(table string) (NormalizedTableSpec, bool) {
	for _, spec := range NormalizedTables {
		if spec.Table == table {
			return spec, true
		}
	}
	return NormalizedTableSpec{}, false
}

// lookupJSONPath resolves an entity-relative sqlref path against a decoded JSON
// object. Each dot-separated segment is an object key, optionally followed by a
// single array index, e.g. "transform.position[0]" or "body.mouth.biteProgress".
// It returns false if any segment is missing or has the wrong container type.
func lookupJSONPath(root map[string]any, path string) (any, bool) {
	var current any = root
	for _, seg := range strings.Split(path, ".") {
		key := seg
		index := -1
		if open := strings.IndexByte(seg, '['); open >= 0 {
			if !strings.HasSuffix(seg, "]") {
				return nil, false
			}
			n, err := strconv.Atoi(seg[open+1 : len(seg)-1])
			if err != nil || n < 0 {
				return nil, false
			}
			key = seg[:open]
			index = n
		}

		obj, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		value, ok := obj[key]
		if !ok {
			return nil, false
		}
		if index < 0 {
			current = value
			continue
		}
		arr, ok := value.([]any)
		if !ok || index >= len(arr) {
			return nil, false
		}
		current = arr[index]
	}
	return current, true
}
