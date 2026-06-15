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
