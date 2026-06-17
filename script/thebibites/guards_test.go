package thebibites

import (
	"math"
	"strings"
	"testing"

	"go.starlark.net/starlark"
)

// TestSetFieldRejectsOutOfRange: a negative value on a non-negative column is
// rejected before staging, while a valid value (including the inclusive 0 bound and
// an int that promotes to a float column) is accepted.
func TestSetFieldRejectsOutOfRange(t *testing.T) {
	ls := loadFixture(t)
	e := &Entity{ls: ls, kind: "bibite", entryName: firstBibiteEntry(t, ls)}

	for _, tc := range []struct {
		attr string
		val  starlark.Value
	}{
		{"energy", starlark.Float(-5)},
		{"health", starlark.Float(-1)},
		{"generation", starlark.MakeInt(-2)}, // BIGINT non-negative
		{"species_id", starlark.MakeInt(-1)}, // shape-only non-negative
	} {
		if err := e.SetField(tc.attr, tc.val); err == nil {
			t.Errorf("%s = %v: expected rejection, got nil", tc.attr, tc.val)
		}
	}
	if ls.stagedOps != 0 {
		t.Errorf("stagedOps = %d after rejected sets, want 0", ls.stagedOps)
	}

	// Accepted: inclusive 0 bound, a positive float, and an int promoted to float.
	for _, tc := range []struct {
		attr string
		val  starlark.Value
	}{
		{"energy", starlark.Float(0)},
		{"health", starlark.Float(12.5)},
		{"energy", starlark.MakeInt(5)},
	} {
		if err := e.SetField(tc.attr, tc.val); err != nil {
			t.Errorf("%s = %v: unexpected rejection: %v", tc.attr, tc.val, err)
		}
	}
}

// TestSetFieldRejectsWrongType: a value of the wrong scalar type is rejected with a
// diagnostic that names the attribute (the "friendly" requirement).
func TestSetFieldRejectsWrongType(t *testing.T) {
	ls := loadFixture(t)
	e := &Entity{ls: ls, kind: "bibite", entryName: firstBibiteEntry(t, ls)}

	for _, tc := range []struct {
		attr string
		val  starlark.Value
	}{
		{"energy", starlark.String("fast")}, // number column, string value
		{"dead", starlark.MakeInt(3)},       // bool column, int value
		{"dying", starlark.Float(1.5)},      // bool column, float value
	} {
		err := e.SetField(tc.attr, tc.val)
		if err == nil {
			t.Errorf("%s = %v: expected rejection, got nil", tc.attr, tc.val)
			continue
		}
		if !strings.Contains(err.Error(), tc.attr) {
			t.Errorf("%s: diagnostic %q does not name the attribute", tc.attr, err.Error())
		}
	}
	if ls.stagedOps != 0 {
		t.Errorf("stagedOps = %d after rejected sets, want 0", ls.stagedOps)
	}
}

// TestBulkSetValidatesBeforeQuery: the bulk path validates the value once, before
// running the query, so a bad value is rejected even when the predicate matches
// zero rows (proving the check is independent of row matching) and nothing stages.
func TestBulkSetValidatesBeforeQuery(t *testing.T) {
	ls := loadFixture(t)

	// Predicate that matches no bibite; a valid-but-out-of-range value still rejects.
	if _, err := ls.bulkSet("bibite", "species_id == -999999", "energy", starlark.Float(-3)); err == nil {
		t.Error("zero-match bulk set with negative energy: expected rejection, got nil")
	}
	// Predicate that matches rows; the same bad value is rejected before any stage.
	if _, err := ls.bulkSet("bibite", "species_id >= 0", "energy", starlark.Float(-3)); err == nil {
		t.Error("matching bulk set with negative energy: expected rejection, got nil")
	}
	if ls.stagedOps != 0 {
		t.Errorf("stagedOps = %d after rejected bulk sets, want 0", ls.stagedOps)
	}
	if ls.flushStmtCount != 0 {
		t.Errorf("flushStmtCount = %d after rejected bulk sets, want 0", ls.flushStmtCount)
	}
}

// TestDerivedTypeRuleNoAllowlist: every writable column gets a value kind derived
// purely from its generated SQLType — no per-column type allowlist. (kindUnknown
// would mean an unrecognized SQLType; none should appear among writable columns.)
func TestDerivedTypeRuleNoAllowlist(t *testing.T) {
	for kind, attrs := range attrRegistry() {
		for name, spec := range attrs {
			if !spec.writable {
				continue
			}
			if got := deriveType(spec.sqlType); got == kindUnknown {
				t.Errorf("%s.%s (SQLType %q) derived kindUnknown — deriveType needs the type",
					kind, name, spec.sqlType)
			}
		}
	}
}

// TestSemanticRulesReferenceLiveColumns: every hand-maintained override key must
// resolve to a live writable source column. semanticRules is keyed by the
// generated source column (attrSpec.sourceColumn), so this collects those rather
// than the friendly registry keys (which may be aliases). A save-format
// rename/removal that orphans an override fails here — loud and localized, per the
// churn strategy.
func TestSemanticRulesReferenceLiveColumns(t *testing.T) {
	writableSources := map[string]bool{}
	for _, attrs := range attrRegistry() {
		for _, spec := range attrs {
			if spec.writable {
				writableSources[spec.sourceColumn] = true
			}
		}
	}
	for col := range semanticRules {
		if !writableSources[col] {
			t.Errorf("semanticRules key %q is not a live writable source column (save-format drift?)", col)
		}
	}
}

// TestEnumRuleMechanism: the enum rule works end to end even though no entity scalar
// column seeds one today (it exists for T7 settings reuse).
func TestEnumRuleMechanism(t *testing.T) {
	r := Rule{Type: kindString, Enum: []string{"phero", "rad"}}
	if err := validateValue(r, "phero"); err != nil {
		t.Errorf("member value rejected: %v", err)
	}
	if err := validateValue(r, "lava"); err == nil {
		t.Error("non-member value accepted, want rejection")
	}
	if err := validateValue(r, int64(1)); err == nil {
		t.Error("non-string value accepted for string enum, want rejection")
	}
}

// TestValidateValueTypeMatrix pins the type-acceptance policy and that it mirrors
// setRowField/asFloat64/asInt64 (int<->float promotion, integral-float ints,
// unsigned non-negativity).
func TestValidateValueTypeMatrix(t *testing.T) {
	cases := []struct {
		kind  valueKind
		val   any
		valid bool
	}{
		{kindNumber, float64(1.5), true},
		{kindNumber, int64(2), true}, // int promotes to a number column
		{kindNumber, "x", false},
		{kindNumber, true, false},
		{kindInt, int64(3), true},
		{kindInt, float64(3), true},    // integral float accepted (asInt64)
		{kindInt, float64(3.5), false}, // non-integral float rejected
		{kindInt, "3", false},
		{kindUint, int64(4), true},
		{kindUint, int64(-1), false}, // unsigned rejects negative
		{kindBool, true, true},
		{kindBool, int64(1), false},
		{kindString, "ok", true},
		{kindString, int64(1), false},
		{kindUnknown, "anything", true}, // no derived constraint
		{kindUnknown, int64(9), true},
	}
	for _, tc := range cases {
		err := validateValue(Rule{Type: tc.kind}, tc.val)
		if tc.valid && err != nil {
			t.Errorf("%v with %T(%v): unexpected error %v", tc.kind, tc.val, tc.val, err)
		}
		if !tc.valid && err == nil {
			t.Errorf("%v with %T(%v): expected error, got nil", tc.kind, tc.val, tc.val)
		}
	}
}

// TestValidateValueRejectsNonFinite: NaN/±Inf slip past Min/Max comparisons (all of
// NaN<min, NaN>max, +Inf>max-on-unbounded are false) yet would abort the commit at
// json.Marshal. The guard must reject them up front for every numeric kind.
func TestValidateValueRejectsNonFinite(t *testing.T) {
	for _, kind := range []valueKind{kindNumber, kindInt, kindUint, kindUnknown} {
		for _, f := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
			if err := validateValue(Rule{Type: kind}, f); err == nil {
				t.Errorf("%v with %v: expected rejection of non-finite, got nil", kind, f)
			}
		}
	}
	// A bounded non-negative column (Min:0) must also reject NaN/+Inf, not let them
	// pass the f < min / f > max comparisons.
	bounded := Rule{Type: kindNumber, Min: &zeroMin}
	for _, f := range []float64{math.NaN(), math.Inf(1)} {
		if err := validateValue(bounded, f); err == nil {
			t.Errorf("bounded column with %v: expected rejection, got nil", f)
		}
	}
}

// TestSetFieldRejectsNonFinite: b.energy = float("nan")/inf is rejected at set time
// (localized) rather than aborting the later commit, and nothing stages.
func TestSetFieldRejectsNonFinite(t *testing.T) {
	ls := loadFixture(t)
	e := &Entity{ls: ls, kind: "bibite", entryName: firstBibiteEntry(t, ls)}

	for _, f := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if err := e.SetField("energy", starlark.Float(f)); err == nil {
			t.Errorf("energy = %v: expected rejection, got nil", f)
		}
	}
	if ls.stagedOps != 0 {
		t.Errorf("stagedOps = %d after non-finite sets, want 0", ls.stagedOps)
	}
}

// TestReadOnlyDiagnostic: setting a read-only locator column returns a friendly
// read-only diagnostic and stages nothing.
func TestReadOnlyDiagnostic(t *testing.T) {
	ls := loadFixture(t)
	e := &Entity{ls: ls, kind: "bibite", entryName: firstBibiteEntry(t, ls)}

	err := e.SetField("body_id", starlark.MakeInt(5))
	if err == nil {
		t.Fatal("setting read-only body_id: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Errorf("diagnostic %q does not mention read-only", err.Error())
	}
	if ls.stagedOps != 0 {
		t.Errorf("stagedOps = %d after read-only reject, want 0", ls.stagedOps)
	}
}
