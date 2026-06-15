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
			if i+1 < len(path) && path[i+1] == '"' {
				end, key, err := parseQuotedKey(path, i)
				if err != nil {
					return nil, err
				}
				parts = append(parts, pathPart{key: key, original: path[i : end+1]})
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
				continue
			}

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

func parseQuotedKey(path string, start int) (int, string, error) {
	for i := start + 2; i < len(path); i++ {
		switch path[i] {
		case '\\':
			i++
		case '"':
			if i+1 >= len(path) || path[i+1] != ']' {
				return 0, "", fmt.Errorf("expected closing bracket after quoted key at byte %d", i)
			}
			key, err := strconv.Unquote(path[start+1 : i+1])
			if err != nil {
				return 0, "", fmt.Errorf("invalid quoted key at byte %d: %w", start, err)
			}
			if key == "" {
				return 0, "", fmt.Errorf("empty quoted key at byte %d", start)
			}
			return i + 1, key, nil
		}
	}
	return 0, "", fmt.Errorf("unterminated quoted key at byte %d", start)
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

// containerRef identifies one slot (a map key or an array index) inside a
// parent container so callers can read, then write back, the value at that slot.
// Array append/delete change a slice header, so the new slice must be written
// back into its parent; setJSONPath never needs this because it mutates a slot
// in place.
type containerRef struct {
	parentMap   map[string]any
	parentSlice []any
	key         string
	index       int
	isIndex     bool
}

func (c containerRef) get() (any, bool) {
	if c.isIndex {
		if c.index < 0 || c.index >= len(c.parentSlice) {
			return nil, false
		}
		return c.parentSlice[c.index], true
	}
	value, ok := c.parentMap[c.key]
	return value, ok
}

func (c containerRef) set(value any) {
	if c.isIndex {
		c.parentSlice[c.index] = value
		return
	}
	c.parentMap[c.key] = value
}

// resolveContainer walks parts[:len-1] from root and returns a containerRef for
// the slot named by the final part. It does not require the final slot to exist.
func resolveContainer(root any, parts []pathPart) (containerRef, error) {
	if len(parts) == 0 {
		return containerRef{}, fmt.Errorf("path is empty")
	}
	current := root
	for i := 0; i < len(parts)-1; i++ {
		part := parts[i]
		if part.isIndex {
			values, ok := current.([]any)
			if !ok {
				return containerRef{}, fmt.Errorf("%s expected array", renderPath(parts[:i+1]))
			}
			if part.index >= len(values) {
				return containerRef{}, fmt.Errorf("%s index out of range", renderPath(parts[:i+1]))
			}
			current = values[part.index]
			continue
		}
		values, ok := current.(map[string]any)
		if !ok {
			return containerRef{}, fmt.Errorf("%s expected object", renderPath(parts[:i+1]))
		}
		next, ok := values[part.key]
		if !ok {
			return containerRef{}, fmt.Errorf("%s is missing", renderPath(parts[:i+1]))
		}
		current = next
	}

	last := parts[len(parts)-1]
	if last.isIndex {
		values, ok := current.([]any)
		if !ok {
			return containerRef{}, fmt.Errorf("%s expected array", renderPath(parts))
		}
		return containerRef{parentSlice: values, index: last.index, isIndex: true}, nil
	}
	values, ok := current.(map[string]any)
	if !ok {
		return containerRef{}, fmt.Errorf("%s expected object", renderPath(parts))
	}
	return containerRef{parentMap: values, key: last.key}, nil
}

// appendJSONArray appends value to the array at containerPath, writing the grown
// slice back into its parent slot.
func appendJSONArray(root any, containerPath string, value any) error {
	parts, err := parsePath(containerPath)
	if err != nil {
		return err
	}
	ref, err := resolveContainer(root, parts)
	if err != nil {
		return err
	}
	existing, ok := ref.get()
	if !ok {
		return fmt.Errorf("%s is missing", containerPath)
	}
	array, ok := existing.([]any)
	if !ok {
		return fmt.Errorf("%s expected array", containerPath)
	}
	ref.set(append(array, normalizeJSONValue(value)))
	return nil
}

// deleteJSONArrayElement removes the element addressed by elementPath, which must
// end in an array index, writing the shortened slice back into its parent slot.
func deleteJSONArrayElement(root any, elementPath string) error {
	parts, err := parsePath(elementPath)
	if err != nil {
		return err
	}
	last := parts[len(parts)-1]
	if !last.isIndex {
		return fmt.Errorf("delete path %q must end in an array index", elementPath)
	}
	arrayParts := parts[:len(parts)-1]
	if len(arrayParts) == 0 {
		return fmt.Errorf("delete path %q cannot delete the root", elementPath)
	}

	ref, err := resolveContainer(root, arrayParts)
	if err != nil {
		return err
	}
	existing, ok := ref.get()
	if !ok {
		return fmt.Errorf("%s is missing", renderPath(arrayParts))
	}
	array, ok := existing.([]any)
	if !ok {
		return fmt.Errorf("%s expected array", renderPath(arrayParts))
	}
	if last.index < 0 || last.index >= len(array) {
		return fmt.Errorf("%s index out of range", elementPath)
	}
	ref.set(append(array[:last.index], array[last.index+1:]...))
	return nil
}

func renderPath(parts []pathPart) string {
	var out strings.Builder
	for i, part := range parts {
		if part.isIndex {
			out.WriteString(part.original)
			continue
		}
		if strings.HasPrefix(part.original, "[") {
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
