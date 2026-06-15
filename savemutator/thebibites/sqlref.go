package thebibites

import (
	"fmt"
)

// SQLValueRef identifies one normalized SQL cell well enough to resolve it
// back to a guarded archive JSON path. Only allowlisted table/column pairs are
// writable; normalized rows remain query projections, not editable state.
type SQLValueRef struct {
	Table  string
	Column string

	EntryName string

	BodyID    int64
	HasBodyID bool

	EggID    int64
	HasEggID bool

	OwnerKind string
	OwnerID   string
	Path      string

	SettingName    string
	Scope          string
	TargetKey      string
	ValueType      string
	WrapperRawJSON string

	ContentIndex    int
	HasContentIndex bool

	GroupIndex          int
	HasGroupIndex       bool
	GroupPelletIndex    int
	HasGroupPelletIndex bool
	Zone                string
	HasZone             bool
	PheromoneIndex      int
	HasPheromoneIndex   bool
	NodeRowIndex        int
	HasNodeRowIndex     bool
	SynapseRowIndex     int
	HasSynapseRowIndex  bool
	ZoneIndex           int
	HasZoneIndex        bool
	ZoneID              int64
	HasZoneID           bool
	ChangerIndex        int
	HasChangerIndex     bool
	Expected            any
	HasExpected         bool
}

// WithExpected adds a stale-value guard for the resolved JSON path.
func (r SQLValueRef) WithExpected(value any) SQLValueRef {
	r.Expected = value
	r.HasExpected = true
	return r
}

// StageSQLSet resolves ref into a guarded JSON set operation and stages it.
func (s *Session) StageSQLSet(ref SQLValueRef, value any) error {
	op, err := SQLSet(ref, value)
	if err != nil {
		return err
	}
	return s.Stage(op)
}

// SQLSet resolves ref into a guarded JSON set operation.
func SQLSet(ref SQLValueRef, value any) (Operation, error) {
	target, path, err := ResolveSQLValueRef(ref)
	if err != nil {
		return Operation{}, err
	}
	if ref.HasExpected {
		target.Guards = append(target.Guards, Require(path, ref.Expected))
	}
	return Set(target, path, value), nil
}

// ResolveSQLValueRef resolves a normalized SQL cell to an archive target and
// JSON path. Unsupported cells return an error instead of guessing.
func ResolveSQLValueRef(ref SQLValueRef) (Target, string, error) {
	if ref.Table == "" {
		return Target{}, "", fmt.Errorf("sql value ref table is required")
	}
	if ref.Column == "" {
		return Target{}, "", fmt.Errorf("sql value ref column is required")
	}

	spec, ok := writableSQLRefTable(ref.Table)
	if !ok {
		return Target{}, "", unsupportedSQLValueRef(ref)
	}
	return spec.resolve(ref)
}

// SQLRefOp names the kind of mutation a SQL ref is being resolved for.
type SQLRefOp string

const (
	SQLRefOpSet    SQLRefOp = "set"
	SQLRefOpDelete SQLRefOp = "delete"
	SQLRefOpAppend SQLRefOp = "append"
)

// StageSQLDelete resolves ref into a delete operation and stages it.
func (s *Session) StageSQLDelete(ref SQLValueRef) error {
	op, err := SQLDelete(ref)
	if err != nil {
		return err
	}
	return s.Stage(op)
}

// StageSQLAppend resolves ref into an append operation for value and stages it.
func (s *Session) StageSQLAppend(ref SQLValueRef, value any) error {
	op, err := SQLAppend(ref, value)
	if err != nil {
		return err
	}
	return s.Stage(op)
}

// SQLDelete resolves ref into a delete operation: an array-element delete for
// synapse/pellet/zone refs, or a whole-entry delete for bibite/egg refs. The
// entry form refuses to orphan parent/child references; use StageDeleteBibite
// with options for prune control.
func SQLDelete(ref SQLValueRef) (Operation, error) {
	spec, err := mutableSQLRefTable(ref)
	if err != nil {
		return Operation{}, err
	}
	if spec.deleteArray != nil {
		target, elementPath, err := spec.deleteArray(ref)
		if err != nil {
			return Operation{}, err
		}
		if ref.HasExpected {
			if field, err := sqlRefColumnValue(ref, spec.columns); err == nil {
				target.Guards = append(target.Guards, Require(elementPath+"."+field, ref.Expected))
			}
		}
		op := Delete(target, elementPath)
		op.SceneCount = spec.sceneCount
		return op, nil
	}
	if spec.entry != nil {
		target, err := spec.entry(ref)
		if err != nil {
			return Operation{}, err
		}
		return DeleteEntry(target), nil
	}
	return Operation{}, unsupportedSQLRefOp(ref, SQLRefOpDelete)
}

// SQLAppend resolves ref into an append operation that appends value to the
// referenced array. Entry-level append (a whole bibite/egg) requires a
// cross-save workspace and is not supported through a single-save ref.
func SQLAppend(ref SQLValueRef, value any) (Operation, error) {
	spec, err := mutableSQLRefTable(ref)
	if err != nil {
		return Operation{}, err
	}
	if spec.appendArray != nil {
		target, container, err := spec.appendArray(ref)
		if err != nil {
			return Operation{}, err
		}
		op := Append(target, container, value)
		op.SceneCount = spec.sceneCount
		return op, nil
	}
	if spec.entry != nil {
		return Operation{}, fmt.Errorf("sql value ref %s entry-level append requires a cross-save workspace and is not supported in a single-save session", ref.Table)
	}
	return Operation{}, unsupportedSQLRefOp(ref, SQLRefOpAppend)
}

// ValidateSQLRefForOp reports whether ref resolves for op, without building a
// staged operation. Used by query-result scanners to fail fast.
func ValidateSQLRefForOp(ref SQLValueRef, op SQLRefOp) error {
	switch op {
	case SQLRefOpSet:
		_, _, err := ResolveSQLValueRef(ref)
		return err
	case SQLRefOpDelete:
		_, err := SQLDelete(ref)
		return err
	case SQLRefOpAppend:
		_, err := SQLAppend(ref, nil)
		return err
	default:
		return fmt.Errorf("unsupported sql ref op %q", op)
	}
}

func mutableSQLRefTable(ref SQLValueRef) (sqlRefTableSpec, error) {
	if ref.Table == "" {
		return sqlRefTableSpec{}, fmt.Errorf("sql value ref table is required")
	}
	spec, ok := writableSQLRefTable(ref.Table)
	if !ok {
		return sqlRefTableSpec{}, unsupportedSQLValueRef(ref)
	}
	return spec, nil
}

func unsupportedSQLRefOp(ref SQLValueRef, op SQLRefOp) error {
	return fmt.Errorf("sql value ref table %s does not support %s", ref.Table, op)
}
