package thebibites

import (
	"fmt"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

// OperationKind identifies a staged mutation operation.
type OperationKind string

const (
	// OperationSet updates an existing JSON value at Path.
	OperationSet OperationKind = "set"
)

const (
	SettingsEntryName   = "settings.bb8settings"
	SpeciesEntryName    = "speciesData.json"
	SceneEntryName      = "scene.bb8scene"
	VarsEntryName       = "vars.bb8scene"
	PelletsEntryName    = "pellets.bb8scene"
	PheromonesEntryName = "pheromones.bb8scene"
)

// Target identifies the archive entry to mutate and optional guards that must
// still match when the operation is applied.
type Target struct {
	EntryName string
	Kind      tb.EntryKind
	Guards    []Guard
}

// Guard requires an existing JSON path to equal a value before mutation.
type Guard struct {
	Path  string
	Equal any
}

// Operation is the generic staged mutation shape. New operation kinds can be
// added here without changing session lifecycle semantics.
type Operation struct {
	Kind   OperationKind
	Target Target
	Path   string
	Value  any

	SetOptions SetOptions
}

// SetOptions controls set operation behavior.
type SetOptions struct {
	// CreateMissing permits missing object keys on the final path segment.
	// Intermediate creation is intentionally not implemented yet.
	CreateMissing bool
}

// BibiteRef is a stable locator for a bibite entity inside an archive entry.
type BibiteRef struct {
	EntryName string
	BodyID    int64
}

// Require returns a JSON equality guard for a target.
func Require(path string, equal any) Guard {
	return Guard{Path: path, Equal: equal}
}

// EntryTarget returns a target for an archive entry.
func EntryTarget(entryName string, kind tb.EntryKind, guards ...Guard) Target {
	return Target{
		EntryName: entryName,
		Kind:      kind,
		Guards:    append([]Guard(nil), guards...),
	}
}

// SettingsTarget returns a target for settings.bb8settings.
func SettingsTarget(guards ...Guard) Target {
	return EntryTarget(SettingsEntryName, tb.EntrySettings, guards...)
}

// SpeciesTarget returns a target for speciesData.json.
func SpeciesTarget(guards ...Guard) Target {
	return EntryTarget(SpeciesEntryName, tb.EntrySpecies, guards...)
}

// SceneTarget returns a target for scene.bb8scene.
func SceneTarget(guards ...Guard) Target {
	return EntryTarget(SceneEntryName, tb.EntryScene, guards...)
}

// VarsTarget returns a target for vars.bb8scene.
func VarsTarget(guards ...Guard) Target {
	return EntryTarget(VarsEntryName, tb.EntryVars, guards...)
}

// PelletsTarget returns a target for pellets.bb8scene.
func PelletsTarget(guards ...Guard) Target {
	return EntryTarget(PelletsEntryName, tb.EntryPellets, guards...)
}

// PheromonesTarget returns a target for pheromones.bb8scene.
func PheromonesTarget(guards ...Guard) Target {
	return EntryTarget(PheromonesEntryName, tb.EntryPheromones, guards...)
}

// BibiteTarget returns a bibite target guarded by body.id.
func BibiteTarget(ref BibiteRef) Target {
	return EntryTarget(ref.EntryName, tb.EntryBibite, Require("body.id", ref.BodyID))
}

// Set returns a generic set operation.
func Set(target Target, path string, value any) Operation {
	return SetWithOptions(target, path, value, SetOptions{})
}

// SetWithOptions returns a generic set operation with explicit options.
func SetWithOptions(target Target, path string, value any, options SetOptions) Operation {
	return Operation{
		Kind:       OperationSet,
		Target:     target,
		Path:       path,
		Value:      value,
		SetOptions: options,
	}
}

func validateOperationShape(op Operation) error {
	if op.Kind == "" {
		return fmt.Errorf("operation kind is required")
	}
	if op.Target.EntryName == "" {
		return fmt.Errorf("%s operation target entry name is required", op.Kind)
	}
	for _, guard := range op.Target.Guards {
		if guard.Path == "" {
			return fmt.Errorf("%s operation target guard path is required", op.Kind)
		}
		if _, err := parsePath(guard.Path); err != nil {
			return fmt.Errorf("%s operation target guard path %q: %w", op.Kind, guard.Path, err)
		}
	}

	switch op.Kind {
	case OperationSet:
		if op.Path == "" {
			return fmt.Errorf("set operation path is required")
		}
		if _, err := parsePath(op.Path); err != nil {
			return fmt.Errorf("set operation path %q: %w", op.Path, err)
		}
	default:
		return fmt.Errorf("unsupported operation kind %q", op.Kind)
	}
	return nil
}
