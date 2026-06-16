package thebibites

import (
	"fmt"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

// Value-validation guards (T10). These run on a writable scalar mutation *before*
// it is staged (Entity.SetField, the bulk where(...).set path), rejecting values
// that are the wrong type, out of range, or not an allowed enum member. They are a
// binding-layer concern: the mutator's stale-value guard (WithExpected) and
// allowlist gate are orthogonal and untouched.
//
// Per the generation philosophy, the *type* rule is derived 1:1 from the column's
// generated SQLType — there is no per-column type allowlist. Only the handful of
// semantic facts metadata cannot express (non-negative quantities) live in the
// tiny semanticRules map below. The validator is pure so T7 (settings writes) can
// reuse validateValue with its own rules.

// valueKind is the scalar domain a column accepts, derived from its SQLType.
type valueKind int

const (
	// kindUnknown is the zero value: an unrecognized SQLType gets no derived type
	// check (a new generated type stays writable rather than being wrongly
	// rejected; deriveType is then taught the new type).
	kindUnknown valueKind = iota
	kindNumber            // DOUBLE  — accepts a Starlark int or float
	kindInt               // BIGINT  — accepts an int, or a float with an integral value
	kindUint              // UBIGINT — kindInt and value >= 0
	kindBool              // BOOLEAN — bool only
	kindString            // TEXT    — string only
)

func (k valueKind) String() string {
	switch k {
	case kindNumber:
		return "a number"
	case kindInt:
		return "an integer"
	case kindUint:
		return "a non-negative integer"
	case kindBool:
		return "a boolean"
	case kindString:
		return "a string"
	default:
		return "a value"
	}
}

// Rule is the complete value-domain constraint for one writable column: the
// derived Type plus optional semantic bounds/enum. validateValue checks against it.
type Rule struct {
	Type valueKind
	Min  *float64 // inclusive lower bound (numeric kinds); nil = no lower bound
	Max  *float64 // inclusive upper bound (numeric kinds); nil = no upper bound
	Enum []string // allowed values (kindString); nil/empty = unconstrained
}

// deriveType maps a generated SQLType to the value kind the column accepts. This
// is the entire type policy — no per-column allowlist. The kinds mirror what
// setRowField/asFloat64/asInt64 (convert.go) will accept so the guard and the
// writer never disagree.
func deriveType(sqlType string) valueKind {
	switch sqlType {
	case "DOUBLE":
		return kindNumber
	case "BIGINT":
		return kindInt
	case "UBIGINT":
		return kindUint
	case "BOOLEAN":
		return kindBool
	case "TEXT":
		return kindString
	default:
		return kindUnknown
	}
}

var zeroMin = 0.0

// nonNegativeColumns are the writable signed (DOUBLE/BIGINT) columns that are
// physically non-negative, so a negative value is staging nonsense. Keyed by the
// generated source column (attrSpec.sourceColumn) because the constraint is a
// property of the underlying quantity, kind-independent and alias-independent (a
// bibite's and an egg's energy are both >= 0; an alias like hp->health still
// inherits health's bound). UBIGINT columns are non-negative by type and are
// intentionally absent here (no redundant entries). species_id is included as the
// chosen "shape-only" rule — a non-negative id; full referential existence (must
// match a species row) is a deliberate seam, not implemented here.
//
// Grounded against the writable columns in tb.NormalizedTables for the entity
// tables; TestSemanticRulesReferenceLiveColumns fails loudly if a save-format
// rename/removal orphans an entry.
var nonNegativeColumns = []string{
	// bibites / eggs identity
	"energy", "health", "time_alive", "generation", "species_id", "transform_scale", "hatch_progress",
	// bibite_body
	"d2_size", "fat_reserves_amount", "attacked_dmg", "times_attacked", "total_damage_suffered",
	"brain_ticks_count", "vision_lookup_count", "vision_sensing_count",
	// bibite_mouth
	"bibites_bitten", "bite_progress", "murdered_area", "total_damage_dealt", "total_murders",
	// bibite_pheromone_emitters / egg_layers / control
	"progress", "egg_progress", "n_eggs_laid", "total_travel",
}

// semanticRules holds the few value constraints generated metadata cannot express,
// keyed by the generated source column (attrSpec.sourceColumn). Type and read-only
// are NOT here — they are derived. Today it is exactly the non-negative set;
// explicit Min/Max/Enum entries for special columns can be added later by
// extending this map.
var semanticRules = func() map[string]Rule {
	m := make(map[string]Rule, len(nonNegativeColumns))
	for _, col := range nonNegativeColumns {
		m[col] = Rule{Min: &zeroMin}
	}
	return m
}()

// ruleFor builds the effective Rule for a column: its derived Type merged with any
// semantic override (bounds/enum). The column argument is the generated source
// column (attrSpec.sourceColumn), so semantic bounds follow the real quantity
// regardless of any friendly alias.
func ruleFor(column, sqlType string) Rule {
	r := Rule{Type: deriveType(sqlType)}
	if o, ok := semanticRules[column]; ok {
		r.Min, r.Max, r.Enum = o.Min, o.Max, o.Enum
	}
	return r
}

// validateSet checks goVal (the int64/float64/bool/string produced by
// fromStarlark) against the column's effective Rule, before staging. Callers wrap
// the returned reason with the "%s.%s: %w" attribute context they already use.
func validateSet(spec attrSpec, goVal any) error {
	return validateValue(ruleFor(spec.sourceColumn, spec.sqlType), goVal)
}

// scalarTypeRule is the value-validation Rule for a gene or settings value, keyed
// by its parsed ScalarType (type only — these carry no semantic bounds; a gene or
// setting may legitimately be negative). It lets gene/settings writes reuse
// validateValue exactly as entity scalars do. Null/unknown yields kindUnknown (no
// type check); scalarValueColumn separately rejects the unsettable null cell.
func scalarTypeRule(t tb.ScalarType) Rule {
	switch t {
	case tb.ScalarNumber:
		return Rule{Type: kindNumber}
	case tb.ScalarBool:
		return Rule{Type: kindBool}
	case tb.ScalarString:
		return Rule{Type: kindString}
	default:
		return Rule{Type: kindUnknown}
	}
}

// validateValue is the pure check: type, then range (numeric), then enum (string).
// It reuses asFloat64/asInt64 (convert.go) so its type acceptance is identical to
// what setRowField will coerce.
func validateValue(r Rule, goVal any) error {
	switch r.Type {
	case kindNumber:
		if _, ok := asFloat64(goVal); !ok {
			return typeError(r.Type, goVal)
		}
	case kindInt:
		if _, ok := asInt64(goVal); !ok {
			return typeError(r.Type, goVal)
		}
	case kindUint:
		n, ok := asInt64(goVal)
		if !ok {
			return typeError(r.Type, goVal)
		}
		if n < 0 {
			return fmt.Errorf("expects %s, got %v", r.Type, n)
		}
	case kindBool:
		if _, ok := goVal.(bool); !ok {
			return typeError(r.Type, goVal)
		}
	case kindString:
		if _, ok := goVal.(string); !ok {
			return typeError(r.Type, goVal)
		}
	case kindUnknown:
		// Unrecognized SQLType: no derived type constraint; bounds/enum (if any
		// override exists) still apply below.
	}

	if r.Min != nil || r.Max != nil {
		if f, ok := asFloat64(goVal); ok {
			if r.Min != nil && f < *r.Min {
				return fmt.Errorf("value %v is below minimum %v", f, *r.Min)
			}
			if r.Max != nil && f > *r.Max {
				return fmt.Errorf("value %v is above maximum %v", f, *r.Max)
			}
		}
	}

	if len(r.Enum) > 0 {
		s, ok := goVal.(string)
		if !ok {
			return typeError(kindString, goVal)
		}
		for _, allowed := range r.Enum {
			if s == allowed {
				return nil
			}
		}
		return fmt.Errorf("value %q is not one of %v", s, r.Enum)
	}
	return nil
}

// typeError reports an expected-vs-got mismatch in friendly terms.
func typeError(want valueKind, goVal any) error {
	return fmt.Errorf("expects %s, got %s", want, goScalarName(goVal))
}

// goScalarName names a fromStarlark scalar for diagnostics.
func goScalarName(v any) string {
	switch v.(type) {
	case int64:
		return "an integer"
	case float64:
		return "a number"
	case bool:
		return "a boolean"
	case string:
		return "a string"
	default:
		return fmt.Sprintf("%T", v)
	}
}
