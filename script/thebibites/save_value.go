package thebibites

import (
	"fmt"

	"go.starlark.net/starlark"
)

// Save is the top-level Starlark value returned by open(). It exposes read-only
// entity collections (s.bibites/s.eggs), analytics (s.sql + collection
// push-down), the settings namespace (s.settings), and commit (s.commit).
// s.zones/s.pellets attach here in P2.
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
	case "sql":
		return starlark.NewBuiltin("sql", s.sqlBuiltin), nil
	case "settings":
		return &Settings{ls: s.ls}, nil
	case "zones":
		return &Zones{ls: s.ls}, nil
	case "pellets":
		return &Pellets{ls: s.ls}, nil
	case "commit":
		return starlark.NewBuiltin("commit", s.commitBuiltin), nil
	default:
		return nil, nil
	}
}

func (s *Save) AttrNames() []string {
	return []string{"bibites", "commit", "eggs", "pellets", "settings", "sql", "zones"}
}

// commitBuiltin implements save.commit(path) -> staged op count. It applies the
// staged mutations and writes the corrected save zip with no reparse (T6's
// commit-to-file). Under dry-run it stages everything but writes nothing. T8 adds
// the host-driven content-addressed commit on top of LoadedSave.WriteSave.
func (s *Save) commitBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var path string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "path", &path); err != nil {
		return nil, err
	}
	if !s.ls.dryRun {
		if err := s.ls.WriteSave(path); err != nil {
			return nil, err
		}
	}
	return starlark.MakeInt(s.ls.stagedOps), nil
}
