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
