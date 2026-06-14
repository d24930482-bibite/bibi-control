package thebibites

import (
	"fmt"
	"strconv"
	"strings"
)

type pathPart struct {
	key      string
	index    int
	isIndex  bool
	original string
}

func parsePath(path string) ([]pathPart, error) {
	if path == "" {
		return nil, fmt.Errorf("path is empty")
	}

	parts := make([]pathPart, 0, strings.Count(path, ".")+1)
	for i := 0; i < len(path); {
		switch path[i] {
		case '.':
			return nil, fmt.Errorf("empty path segment at byte %d", i)
		case '[':
			end := strings.IndexByte(path[i:], ']')
			if end < 0 {
				return nil, fmt.Errorf("unterminated index at byte %d", i)
			}
			end += i
			raw := path[i+1 : end]
			if raw == "" {
				return nil, fmt.Errorf("empty index at byte %d", i)
			}
			index, err := strconv.Atoi(raw)
			if err != nil || index < 0 {
				return nil, fmt.Errorf("invalid index %q at byte %d", raw, i)
			}
			parts = append(parts, pathPart{index: index, isIndex: true, original: path[i : end+1]})
			i = end + 1
			if i < len(path) {
				if path[i] == '.' {
					i++
					if i == len(path) {
						return nil, fmt.Errorf("trailing dot")
					}
				} else if path[i] != '[' {
					return nil, fmt.Errorf("expected dot or index at byte %d", i)
				}
			}
		default:
			start := i
			for i < len(path) && path[i] != '.' && path[i] != '[' {
				i++
			}
			if start == i {
				return nil, fmt.Errorf("empty path segment at byte %d", start)
			}
			key := path[start:i]
			parts = append(parts, pathPart{key: key, original: key})
			if i < len(path) && path[i] == '.' {
				i++
				if i == len(path) {
					return nil, fmt.Errorf("trailing dot")
				}
			}
		}
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("path is empty")
	}
	return parts, nil
}

func getJSONPath(root any, path string) (any, bool, error) {
	parts, err := parsePath(path)
	if err != nil {
		return nil, false, err
	}

	current := root
	for _, part := range parts {
		if part.isIndex {
			values, ok := current.([]any)
			if !ok {
				return nil, false, fmt.Errorf("%s expected array", part.original)
			}
			if part.index >= len(values) {
				return nil, false, nil
			}
			current = values[part.index]
			continue
		}

		values, ok := current.(map[string]any)
		if !ok {
			return nil, false, fmt.Errorf("%s expected object", part.original)
		}
		value, ok := values[part.key]
		if !ok {
			return nil, false, nil
		}
		current = value
	}
	return current, true, nil
}

func setJSONPath(root any, path string, value any, options SetOptions) error {
	parts, err := parsePath(path)
	if err != nil {
		return err
	}
	if len(parts) == 0 {
		return fmt.Errorf("path is empty")
	}

	current := root
	for i, part := range parts {
		last := i == len(parts)-1
		if part.isIndex {
			values, ok := current.([]any)
			if !ok {
				return fmt.Errorf("%s expected array", renderPath(parts[:i+1]))
			}
			if part.index >= len(values) {
				return fmt.Errorf("%s index out of range", renderPath(parts[:i+1]))
			}
			if last {
				values[part.index] = normalizeJSONValue(value)
				return nil
			}
			current = values[part.index]
			continue
		}

		values, ok := current.(map[string]any)
		if !ok {
			return fmt.Errorf("%s expected object", renderPath(parts[:i+1]))
		}
		if last {
			if _, ok := values[part.key]; !ok && !options.CreateMissing {
				return fmt.Errorf("%s is missing", renderPath(parts[:i+1]))
			}
			values[part.key] = normalizeJSONValue(value)
			return nil
		}
		next, ok := values[part.key]
		if !ok {
			return fmt.Errorf("%s is missing", renderPath(parts[:i+1]))
		}
		current = next
	}
	return nil
}

func renderPath(parts []pathPart) string {
	var out strings.Builder
	for i, part := range parts {
		if part.isIndex {
			out.WriteString(part.original)
			continue
		}
		if i > 0 {
			out.WriteByte('.')
		}
		out.WriteString(part.key)
	}
	return out.String()
}
