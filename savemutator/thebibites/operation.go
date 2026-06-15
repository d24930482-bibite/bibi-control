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
	// OperationAppend appends Value to the JSON array at Path (the array
	// container, e.g. brain.Synapses).
	OperationAppend OperationKind = "append"
	// OperationDelete removes the JSON array element at Path. Path must end in
	// an array index, e.g. brain.Synapses[3].
	OperationDelete OperationKind = "delete"
	// OperationDeleteEntry removes a whole bibite/egg archive entry identified
	// by Target and maintains the scene bibite count.
	OperationDeleteEntry OperationKind = "delete_entry"
	// OperationAppendEntry adds a whole bibite/egg archive entry from
	// EntryPayload and maintains the scene bibite count.
	OperationAppendEntry OperationKind = "append_entry"
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

	SetOptions    SetOptions
	DeleteOptions DeleteOptions
	EntryPayload  EntryPayload

	// SceneCount names a scene integer field to reconcile when this append/delete
	// changes a counted collection (e.g. "nPellets"). Append adds one, delete
	// subtracts one. Empty means the op touches no scene count.
	SceneCount string
}

// SetOptions controls set operation behavior.
type SetOptions struct {
	// CreateMissing permits missing object keys on the final path segment.
	// Intermediate creation is intentionally not implemented yet.
	CreateMissing bool
}

// DeleteOptions controls delete_entry behavior.
type DeleteOptions struct {
	// PruneParentLinks removes the deleted bibite/egg id from any other bibite's
	// body.eggLayer.children array instead of refusing the delete. Without it, a
	// delete that would orphan a parent/child reference is rejected.
	PruneParentLinks bool
}

// EntryPayload carries a whole archive entry for OperationAppendEntry.
type EntryPayload struct {
	Name string
	Kind tb.EntryKind
	JSON any
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

// Append returns an operation that appends value to the JSON array at the
// container path inside target.
func Append(target Target, containerPath string, value any) Operation {
	return Operation{
		Kind:   OperationAppend,
		Target: target,
		Path:   containerPath,
		Value:  value,
	}
}

// Delete returns an operation that removes the JSON array element at
// elementPath (which must end in an array index) inside target.
func Delete(target Target, elementPath string) Operation {
	return Operation{
		Kind:   OperationDelete,
		Target: target,
		Path:   elementPath,
	}
}

// DeleteEntry returns an operation that removes a whole bibite/egg entry.
func DeleteEntry(target Target) Operation {
	return DeleteEntryWithOptions(target, DeleteOptions{})
}

// DeleteEntryWithOptions is DeleteEntry with explicit options.
func DeleteEntryWithOptions(target Target, options DeleteOptions) Operation {
	return Operation{
		Kind:          OperationDeleteEntry,
		Target:        target,
		DeleteOptions: options,
	}
}

// AppendEntry returns an operation that adds a whole bibite/egg entry.
func AppendEntry(payload EntryPayload) Operation {
	return Operation{
		Kind:         OperationAppendEntry,
		EntryPayload: payload,
	}
}

func validateOperationShape(op Operation) error {
	if op.Kind == "" {
		return fmt.Errorf("operation kind is required")
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
	case OperationSet, OperationAppend, OperationDelete:
		if op.Target.EntryName == "" {
			return fmt.Errorf("%s operation target entry name is required", op.Kind)
		}
		if op.Path == "" {
			return fmt.Errorf("%s operation path is required", op.Kind)
		}
		parts, err := parsePath(op.Path)
		if err != nil {
			return fmt.Errorf("%s operation path %q: %w", op.Kind, op.Path, err)
		}
		if op.Kind == OperationDelete && !parts[len(parts)-1].isIndex {
			return fmt.Errorf("delete operation path %q must end in an array index", op.Path)
		}
	case OperationDeleteEntry:
		if op.Target.EntryName == "" {
			return fmt.Errorf("%s operation target entry name is required", op.Kind)
		}
		if op.Target.Kind != tb.EntryBibite && op.Target.Kind != tb.EntryEgg {
			return fmt.Errorf("delete_entry operation target kind = %q, want bibite or egg", op.Target.Kind)
		}
	case OperationAppendEntry:
		if op.EntryPayload.Name == "" {
			return fmt.Errorf("append_entry operation payload name is required")
		}
		if op.EntryPayload.Kind != tb.EntryBibite && op.EntryPayload.Kind != tb.EntryEgg {
			return fmt.Errorf("append_entry operation payload kind = %q, want bibite or egg", op.EntryPayload.Kind)
		}
		if op.EntryPayload.JSON == nil {
			return fmt.Errorf("append_entry operation payload JSON is required")
		}
	default:
		return fmt.Errorf("unsupported operation kind %q", op.Kind)
	}
	return nil
}
