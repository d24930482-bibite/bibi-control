package thebibites

import (
	"strings"
	"testing"

	"go.starlark.net/starlark"
)

// TestValidateExprSafetyRejectsComments covers review fix #7: the safety gate must
// reject SQL line ('--') and block ('/* */') comments outside string literals,
// while still allowing those byte sequences inside a string literal.
func TestValidateExprSafetyRejectsComments(t *testing.T) {
	rejected := []struct {
		name string
		expr string
	}{
		{"line comment trailing", "energy -- DROP"},
		{"line comment only", "energy + 1 --"},
		{"block comment", "energy /* hidden */ + 1"},
		{"block comment open", "energy /* hidden"},
		{"semicolon still rejected", "energy; DROP TABLE bibites"},
		{"subquery still rejected", "(SELECT max(energy) FROM bibites)"},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateExprSafety(tc.expr); err == nil {
				t.Fatalf("validateExprSafety(%q) = nil, want error", tc.expr)
			}
		})
	}

	allowed := []struct {
		name string
		expr string
	}{
		// '--' and '/*' inside a string literal must NOT trip the gate.
		{"dashes in string", "'-- not a comment'"},
		{"slash-star in string", "'/* not a comment */'"},
		// A bare subtraction is not a comment.
		{"single minus", "energy - 1"},
		// A bare division is not a comment.
		{"single slash", "energy / 2"},
		// NULL must pass the safety gate (rejected later by coerce/validate).
		{"null literal", "NULL"},
	}
	for _, tc := range allowed {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateExprSafety(tc.expr); err != nil {
				t.Fatalf("validateExprSafety(%q) = %v, want nil", tc.expr, err)
			}
		})
	}
}

// TestValidateExprColumnsStructural covers review fix #9: an unknown column in a
// value position is rejected structurally (before any query), while known columns,
// value-literals, function calls and keyword constructs pass through to DuckDB.
func TestValidateExprColumnsStructural(t *testing.T) {
	unknown := []string{
		"definitely_not_a_column + 1",
		"enrgy",
		"1 + nope",
		"energy + bogus_col",
	}
	for _, expr := range unknown {
		t.Run("reject "+expr, func(t *testing.T) {
			err := validateExprColumns("bibite", expr)
			if err == nil {
				t.Fatalf("validateExprColumns(bibite, %q) = nil, want unknown-column error", expr)
			}
			if !strings.Contains(err.Error(), "unknown column") {
				t.Fatalf("validateExprColumns(bibite, %q) = %v, want 'unknown column'", expr, err)
			}
		})
	}

	allowed := []string{
		"energy * 0.9",               // known column
		"energy + d2_size",           // two known columns
		"generation + 1",             // known column + literal
		"NULL",                       // value-literal
		"true",                       // value-literal
		"abs(energy)",                // function call
		"'hello'",                    // string literal (rejected later by coerce, not here)
		"energy + 1.5e3",             // numeric literal in scientific notation
		`"transform_position_x" + 1`, // explicitly quoted identifier
	}
	for _, expr := range allowed {
		t.Run("allow "+expr, func(t *testing.T) {
			if err := validateExprColumns("bibite", expr); err != nil {
				t.Fatalf("validateExprColumns(bibite, %q) = %v, want nil", expr, err)
			}
		})
	}
}

// TestOneToManyTablesMemoized covers review fix #10: oneToManyTables() returns the
// same memoized map instance across calls (sync.Once), not a fresh allocation.
func TestOneToManyTablesMemoized(t *testing.T) {
	a := oneToManyTables()
	b := oneToManyTables()
	if a == nil {
		t.Fatal("oneToManyTables() = nil")
	}
	// Same backing map => identical length and shared identity via a probe write
	// is unsafe (shared read-only), so compare element-wise plus a reference check
	// using a sentinel: mutating a's view would be visible in b only if shared.
	if len(a) != len(b) {
		t.Fatalf("len mismatch: %d vs %d", len(a), len(b))
	}
	// Reference identity: the two results must be the same map header (memoized).
	probe := "  __memo_probe__  "
	a[probe] = true
	defer delete(a, probe)
	if !b[probe] {
		t.Fatal("oneToManyTables() returned distinct map instances; expected a memoized singleton")
	}
}

// TestWriteThroughAndStageSharedBySetAndSetExpr covers review fix #8: both bulkSet
// (constant) and bulkSetExpr (per-row expression) drive the same extracted
// write-through/stage helper. This exercises both paths end-to-end so a regression
// in the shared helper is caught by either branch.
func TestWriteThroughAndStageSharedBySetAndSetExpr(t *testing.T) {
	t.Run("bulkSet constant", func(t *testing.T) {
		ls := loadFixture(t)
		name := firstBibiteEntry(t, ls)
		coll := &EntityCollection{ls: ls, kind: "bibite"}
		narrowed, err := callMethod(t, coll, "where", starlark.String("entry_name == "+sqlStr(name)))
		if err != nil {
			t.Fatalf("where: %v", err)
		}
		res, err := callMethod(t, narrowed.(*EntityCollection), "set",
			starlark.String("energy"), starlark.Float(123.5))
		if err != nil {
			t.Fatalf("set: %v", err)
		}
		if n := mustInt(t, res); n != 1 {
			t.Fatalf("set staged %d rows, want 1", n)
		}
		if got := bibiteEnergySQL(t, ls, name); !floatsClose(got, 123.5) {
			t.Errorf("energy after set = %v, want 123.5", got)
		}
	})

	t.Run("bulkSetExpr per-row", func(t *testing.T) {
		ls := loadFixture(t)
		name := firstBibiteEntry(t, ls)
		base := bibiteEnergySQL(t, ls, name)
		coll := &EntityCollection{ls: ls, kind: "bibite"}
		narrowed, err := callMethod(t, coll, "where", starlark.String("entry_name == "+sqlStr(name)))
		if err != nil {
			t.Fatalf("where: %v", err)
		}
		res, err := callMethod(t, narrowed.(*EntityCollection), "set_expr",
			starlark.String("energy"), starlark.String("energy + 1"))
		if err != nil {
			t.Fatalf("set_expr: %v", err)
		}
		if n := mustInt(t, res); n != 1 {
			t.Fatalf("set_expr staged %d rows, want 1", n)
		}
		if got := bibiteEnergySQL(t, ls, name); !floatsClose(got, base+1) {
			t.Errorf("energy after set_expr = %v, want %v", got, base+1)
		}
	})
}
