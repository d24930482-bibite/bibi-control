package thebibites

import (
	"math"
	"testing"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

// valueFieldScalarType maps a generated value-column field name (as it appears in
// tb.NormalizedTables) to the ScalarType scalarValueColumn selects that column for.
// This is the only hand-written coupling in this test: the column name and SQL type
// themselves come from the generated metadata, so a save-format rename of either is
// caught. A generated rename of the *field* (e.g. NumberValue -> NumValue) is caught
// too — the rename drops the field out of this map, which the liveness assertion
// below flags.
var valueFieldScalarType = map[string]tb.ScalarType{
	"NumberValue": tb.ScalarNumber,
	"BoolValue":   tb.ScalarBool,
	"StringValue": tb.ScalarString,
}

// generatedScalarColumns derives, from the generated tb.NormalizedTables metadata,
// the canonical (column, sqlType) each ScalarType's typed value column must use. It
// walks every value-column field spec across every normalized table and asserts the
// generator is internally consistent (the same ScalarType always resolves to the
// same column/type), so the source of truth scalarValueColumn is checked against is
// itself unambiguous.
func generatedScalarColumns(t *testing.T) map[tb.ScalarType][2]string {
	t.Helper()
	got := map[tb.ScalarType][2]string{}
	for _, table := range tb.NormalizedTables {
		for _, f := range table.Fields {
			st, ok := valueFieldScalarType[f.Field]
			if !ok {
				continue
			}
			pair := [2]string{f.Column, f.SQLType}
			if prev, seen := got[st]; seen && prev != pair {
				t.Fatalf("generated metadata is inconsistent for %s: table %q maps it to %v but an earlier table mapped it to %v",
					st, table.Table, pair, prev)
			}
			got[st] = pair
		}
	}
	return got
}

// TestScalarValueColumnMatchesGeneratedMetadata is the drift-guard for
// scalarValueColumn's hand-mapped (ScalarType -> column, sqlType) table. Like
// TestSemanticRulesReferenceLiveColumns in guards.go and the scalar-redundancy guard
// in saveparser, it pins a hand-list against the generated source of truth so a
// save-format churn fails loudly here instead of silently mutating the wrong column.
//
// The source of truth is tb.NormalizedTables (the generated normalize metadata): its
// value-column field specs already own each value column's name and SQL type, and
// scalarValueColumn must not duplicate those facts with a stale copy.
func TestScalarValueColumnMatchesGeneratedMetadata(t *testing.T) {
	generated := generatedScalarColumns(t)
	if len(generated) == 0 {
		t.Fatal("no value columns found in tb.NormalizedTables; the metadata source of truth is empty (generation drift?)")
	}

	// Every ScalarType the generator declares a value column for must be handled by
	// scalarValueColumn, with byte-identical column name and SQL type.
	for st, want := range generated {
		col, sqlType, err := scalarValueColumn(st)
		if err != nil {
			t.Errorf("scalarValueColumn(%s) returned error %v, but the generated metadata declares value column %q (%s)",
				st, err, want[0], want[1])
			continue
		}
		if col != want[0] || sqlType != want[1] {
			t.Errorf("scalarValueColumn(%s) = (%q, %q); generated metadata says (%q, %q): the hand-map drifted from the source of truth",
				st, col, sqlType, want[0], want[1])
		}
	}

	// Conversely, every ScalarType scalarValueColumn claims to handle must be backed
	// by a generated value column — no hand-map entry may outlive the metadata.
	for _, st := range []tb.ScalarType{tb.ScalarNumber, tb.ScalarBool, tb.ScalarString} {
		if _, ok := generated[st]; !ok {
			if col, _, err := scalarValueColumn(st); err == nil {
				t.Errorf("scalarValueColumn(%s) returns column %q, but the generated metadata declares no value column for it (dead hand-map entry?)",
					st, col)
			}
		}
	}

	// Sanity: an unmodeled scalar type is not settable.
	if _, _, err := scalarValueColumn(tb.ScalarNull); err == nil {
		t.Error("scalarValueColumn(ScalarNull) returned nil error; null/unknown must not be settable")
	}
}

// TestAsInt64RejectsOutOfRange pins the integral-float range guard in asInt64: an
// integral float beyond the int64 range is rejected (not-ok) rather than silently
// truncated by an implementation-defined int64() conversion, while every in-range
// integral float — including the int64 boundaries — keeps its existing behavior.
func TestAsInt64RejectsOutOfRange(t *testing.T) {
	// float64(math.MaxInt64) rounds up to 2^63 (one past MaxInt64); the largest
	// integral float strictly below it that is still a valid int64 is 2^63 - 1024 =
	// 9223372036854774784.
	const maxRepresentableInt64Float = float64(9223372036854774784) // 2^63 - 1024

	accept := []struct {
		in   float64
		want int64
	}{
		{0, 0},
		{1, 1},
		{-1, -1},
		{1e18, int64(1e18)},
		{float64(math.MinInt64), math.MinInt64},       // -2^63, exactly representable, valid
		{maxRepresentableInt64Float, 9223372036854774784},
	}
	for _, tc := range accept {
		got, ok := asInt64(tc.in)
		if !ok {
			t.Errorf("asInt64(%v): ok=false, want true", tc.in)
			continue
		}
		if got != tc.want {
			t.Errorf("asInt64(%v) = %d, want %d", tc.in, got, tc.want)
		}
	}

	reject := []float64{
		1e19,                          // > MaxInt64
		-1e19,                         // < MinInt64
		float64(math.MaxInt64),        // 2^63 — one past the max, must reject
		math.MaxFloat64,
		-math.MaxFloat64,
		1.5,                           // non-integral (pre-existing rejection)
	}
	for _, in := range reject {
		if got, ok := asInt64(in); ok {
			t.Errorf("asInt64(%v): ok=true (got %d), want false (out of int64 range or non-integral)", in, got)
		}
	}

	// int64 inputs are always accepted unchanged, including the boundaries.
	for _, n := range []int64{0, 1, -1, math.MaxInt64, math.MinInt64} {
		if got, ok := asInt64(n); !ok || got != n {
			t.Errorf("asInt64(int64(%d)) = (%d, %v), want (%d, true)", n, got, ok, n)
		}
	}
}
