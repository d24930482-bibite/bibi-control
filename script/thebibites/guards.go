package thebibites

import (
	"fmt"
	"math"
	"math/big"

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

// speciesIDColumn is the generated source column carrying a species id. The
// referential guard (validateSpeciesID) keys off it so the same check applies to
// the scalar SetField path and the bulkSet/bulkSetExpr paths, exactly as
// nonNegativeColumns does for the shape rule.
const speciesIDColumn = "species_id"

// validateSpeciesID is the referential existence check for a species_id write:
// the id must match an actual species row in this save, not merely be non-negative
// (the existing shape rule). A nonexistent id would stage a dangling reference, so
// it is rejected loudly with a diagnostic naming the column and the bad id. The
// species set is one row per species (small), so a linear scan over
// ls.tables.Species is fine — no perf concern. Only rows with HasSpeciesID match
// (a species row without a parsed id cannot be a valid target). Shared by the
// entity scalar SetField path and both bulk write paths.
func (ls *LoadedSave) validateSpeciesID(n int64) error {
	for i := range ls.tables.Species {
		s := &ls.tables.Species[i]
		if s.HasSpeciesID && s.SpeciesID == n {
			return nil
		}
	}
	return fmt.Errorf("species_id %d does not match any species in this save", n)
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
	// A non-finite float (NaN/±Inf) is the wrong kind of number for every numeric
	// column: it slips past the Min/Max comparison below (NaN < min and NaN > max
	// are both false; +Inf has no Max on the unbounded columns) yet aborts the whole
	// commit later at json.Marshal ("unsupported value: NaN"), far from this set.
	// Reject it here so the diagnostic is localized to the offending value.
	if f, ok := goVal.(float64); ok && (math.IsNaN(f) || math.IsInf(f, 0)) {
		return fmt.Errorf("expects a finite number, got %v", f)
	}

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

// coerceExprResult maps a per-row SQL expression result (the set_expr path) into
// the int64/float64/bool/string that validateSet and setRowField consume, driven
// by the target column's derived kind. The input is a value scanned from DuckDB
// and already run through duckdb.NormalizeSQLScanValue, so it is one of:
// int64/uint64/float64/bool/string/*big.Int/nil. This is the set_expr analogue of
// coercePelletScalar, but it must also absorb the wider scalar set a raw SQL
// expression can yield (HUGEINT -> *big.Int, an unsigned sum -> uint64, a NULL
// result -> nil) and turn an out-of-range or mistyped result into a clean,
// column-named diagnostic instead of a downstream panic. Callers wrap the
// returned reason with their "%s.%s: %w" attribute context.
func coerceExprResult(spec attrSpec, v any) (any, error) {
	col, sqlType := spec.sourceColumn, spec.sqlType
	if v == nil {
		return nil, fmt.Errorf("expression produced NULL for %s (%s)", col, sqlType)
	}
	kind := deriveType(sqlType)
	switch kind {
	case kindInt, kindUint:
		n, err := exprToInt64(col, sqlType, v)
		if err != nil {
			return nil, err
		}
		if kind == kindUint && n < 0 {
			return nil, fmt.Errorf("expression produced %d for %s (%s), which is negative", n, col, sqlType)
		}
		return n, nil
	case kindNumber:
		return exprToFloat64(col, sqlType, v)
	case kindBool:
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("expression for %s (%s) produced %s, want a boolean", col, sqlType, goScalarName(v))
		}
		return b, nil
	case kindString:
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("expression for %s (%s) produced %s, want a string", col, sqlType, goScalarName(v))
		}
		return s, nil
	default:
		// kindUnknown: no derived target kind (a new generated SQLType). Pass the
		// normalized scalar through; validateSet/setRowField apply what checks exist.
		return v, nil
	}
}

// exprToInt64 narrows a scanned expression scalar to an int64 for an integer
// target column, rejecting a non-integer result and an out-of-int64-range
// magnitude (uint64 above MaxInt64, or a HUGEINT *big.Int) with a column-named
// overflow error rather than silently truncating.
func exprToInt64(col, sqlType string, v any) (int64, error) {
	switch x := v.(type) {
	case int64:
		return x, nil
	case uint64:
		if x > uint64(math.MaxInt64) {
			return 0, fmt.Errorf("value overflows column %s (%s)", col, sqlType)
		}
		return int64(x), nil
	case *big.Int:
		if !x.IsInt64() {
			return 0, fmt.Errorf("value overflows column %s (%s)", col, sqlType)
		}
		return x.Int64(), nil
	case float64:
		if n, ok := asInt64(x); ok {
			return n, nil
		}
		if math.IsNaN(x) || math.IsInf(x, 0) || x != math.Trunc(x) {
			return 0, fmt.Errorf("expression for %s (%s) produced %v, want an integer", col, sqlType, x)
		}
		return 0, fmt.Errorf("value overflows column %s (%s)", col, sqlType)
	default:
		return 0, fmt.Errorf("expression for %s (%s) produced %s, want an integer", col, sqlType, goScalarName(v))
	}
}

// exprToFloat64 widens a scanned expression scalar to a float64 for a DOUBLE
// target column. A HUGEINT/uint64 that exceeds int64 is still a valid double
// magnitude (precision loss is inherent to the column type), so it converts
// rather than erroring; a non-numeric result is a clean column-named error.
func exprToFloat64(col, sqlType string, v any) (float64, error) {
	switch x := v.(type) {
	case float64:
		return x, nil
	case int64:
		return float64(x), nil
	case uint64:
		return float64(x), nil
	case *big.Int:
		f, _ := new(big.Float).SetInt(x).Float64()
		return f, nil
	default:
		return 0, fmt.Errorf("expression for %s (%s) produced %s, want a number", col, sqlType, goScalarName(v))
	}
}

// typeError reports an expected-vs-got mismatch in friendly terms.
func typeError(want valueKind, goVal any) error {
	return fmt.Errorf("expects %s, got %s", want, goScalarName(goVal))
}

// goScalarName names a scalar for diagnostics. It covers the fromStarlark scalar
// set plus the wider scalars a raw SQL expression result can carry (uint64 and
// *big.Int from coerceExprResult), so a mistyped expression result reads cleanly.
func goScalarName(v any) string {
	switch v.(type) {
	case int64, uint64, *big.Int:
		return "an integer"
	case float64:
		return "a number"
	case bool:
		return "a boolean"
	case string:
		return "a string"
	case nil:
		return "NULL"
	default:
		return fmt.Sprintf("%T", v)
	}
}
