package thebibites

import (
	"fmt"

	"go.starlark.net/starlark"
)

// Save is the top-level Starlark binding (the predeclared `save` global). T4
// exposes read-only entity collections; save.settings (T7) and save.sql (T5)
// attach here later.
type Save struct {
	ls *LoadedSave
}

var (
	_ starlark.Value    = (*Save)(nil)
	_ starlark.HasAttrs = (*Save)(nil)
)

func (s *Save) String() string        { return "save" }
func (s *Save) Type() string          { return "save" }
func (s *Save) Freeze()               {}
func (s *Save) Truth() starlark.Bool  { return starlark.True }
func (s *Save) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable type: save") }

func (s *Save) Attr(name string) (starlark.Value, error) {
	switch name {
	case "bibites":
		return &EntityCollection{ls: s.ls, kind: "bibite"}, nil
	case "eggs":
		return &EntityCollection{ls: s.ls, kind: "egg"}, nil
	default:
		return nil, nil
	}
}

func (s *Save) AttrNames() []string {
	return []string{"bibites", "eggs"}
}
