package thebibites

import (
	"context"
	"strings"
	"testing"

	"github.com/asemones/bibicontrol/script"
	"go.starlark.net/starlark"
)

// TestUnfilteredDeleteErrors is the #3 regression: delete() on an unfiltered
// collection must refuse rather than silently stage a whole-population delete.
func TestUnfilteredDeleteErrors(t *testing.T) {
	ls := loadFixture(t)
	bibites := &EntityCollection{ls: ls, kind: "bibite"}

	_, err := callMethod(t, bibites, "delete")
	if err == nil {
		t.Fatalf("unfiltered delete() returned nil error, want a refusal")
	}
	if !strings.Contains(err.Error(), "where") {
		t.Errorf("error %q does not mention scoping with where(...)", err.Error())
	}
	if ls.stagedOps != 0 {
		t.Errorf("stagedOps = %d, want 0 (refused delete must stage nothing)", ls.stagedOps)
	}
}

// TestExplicitDeleteAllOptIn: .where("true").delete() is the intentional opt-in and
// is allowed (it stages a delete for the whole population). prune=True avoids the
// referential guard for any non-leaf bibite.
func TestExplicitDeleteAllOptIn(t *testing.T) {
	ls := loadFixture(t)
	bibites := &EntityCollection{ls: ls, kind: "bibite"}
	total := bibites.Len()
	if total == 0 {
		t.Skip("fixture has no bibites")
	}

	all, err := callMethod(t, bibites, "where", starlark.String("true"))
	if err != nil {
		t.Fatalf("where(\"true\"): %v", err)
	}
	res, err := callMethod(t, all.(*EntityCollection), "delete", starlark.Bool(true))
	if err != nil {
		t.Fatalf("where(\"true\").delete(prune=True): %v", err)
	}
	if got := mustInt(t, res); int(got) != total {
		t.Errorf("delete-all staged %d, want %d", got, total)
	}
}

// TestUnfilteredSetStillAllowed: the #3 guard is delete-only; a bulk set over the
// whole population stays a supported operation.
func TestUnfilteredSetStillAllowed(t *testing.T) {
	ls := loadFixture(t)
	bibites := &EntityCollection{ls: ls, kind: "bibite"}
	total := bibites.Len()
	if total == 0 {
		t.Skip("fixture has no bibites")
	}
	res, err := callMethod(t, bibites, "set", starlark.String("energy"), starlark.Float(123))
	if err != nil {
		t.Fatalf("unfiltered set(): %v", err)
	}
	if got := mustInt(t, res); int(got) != total {
		t.Errorf("unfiltered set staged %d rows, want %d", got, total)
	}
}

// TestBadPredicateLenPanicsLoudly is the #4 regression at the Go level: a filtered
// collection over an unknown column must NOT silently report an empty set from
// Len(); it surfaces the resolution error loudly (here, a panic the script engine
// recovers into a diagnostic).
func TestBadPredicateLenPanicsLoudly(t *testing.T) {
	ls := loadFixture(t)
	bibites := &EntityCollection{ls: ls, kind: "bibite"}

	narrowedV, err := callMethod(t, bibites, "where", starlark.String("not_a_real_column > 5"))
	if err != nil {
		t.Fatalf("where(...) should not error (handle returned, error deferred): %v", err)
	}
	narrowed := narrowedV.(*EntityCollection)

	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("Len() over a bad predicate returned without panicking (silent empty set)")
		}
	}()
	_ = narrowed.Len() // must panic, not return 0
	t.Fatalf("unreachable: Len() did not panic")
}

// TestBadPredicateIterationDiagnostic is the #4 end-to-end contract: iterating a
// filtered collection over a typo'd column inside a Starlark program fails with a
// clear diagnostic instead of completing as if it processed nothing.
func TestBadPredicateIterationDiagnostic(t *testing.T) {
	ls := loadFixture(t)
	program := []byte(`
n = len(save.bibites.where("enrgy > 100"))
print("processed %d" % n)
`)
	res, err := script.Run(context.Background(), program, Globals(ls), script.Options{Filename: "bad.star"})
	if err == nil {
		t.Fatalf("script over a typo'd predicate succeeded (silent empty set); diagnostics=%+v", res.Diagnostics)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("Diagnostics = %#v, want one", res.Diagnostics)
	}
	if code := res.Diagnostics[0].Code; code != "panic" {
		t.Errorf("diagnostic code = %q, want panic (loud surfacing of the bad predicate)", code)
	}
}

// TestFilteredMemoRefreshesAfterMutation is the #6 regression: a filtered
// collection's memoized Len/Iterate must re-resolve after an in-run mutation
// changes which entities match, staying consistent with a fresh count(). A very
// high energy threshold matches nothing initially; a bulk set lifts every bibite
// over it, after which Len() must equal the total (and count()).
func TestFilteredMemoRefreshesAfterMutation(t *testing.T) {
	ls := loadFixture(t)
	bibites := &EntityCollection{ls: ls, kind: "bibite"}
	total := bibites.Len()
	if total == 0 {
		t.Skip("fixture has no bibites")
	}

	const threshold = 1e11
	highV, err := callMethod(t, bibites, "where", starlark.String("energy > 100000000000"))
	if err != nil {
		t.Fatalf("where: %v", err)
	}
	high := highV.(*EntityCollection)

	// Materialize the memo before the mutation. No realistic fixture has a bibite
	// at 100 billion energy; bail out if one somehow does so the test stays meaningful.
	if got := high.Len(); got != 0 {
		t.Skipf("fixture already has %d bibite(s) above %g energy", got, threshold)
	}

	// Lift every bibite's energy above the threshold (mirrored, so an in-run
	// re-query observes it). stagedOps advances, invalidating the memo.
	if _, err := callMethod(t, bibites, "set", starlark.String("energy"), starlark.Float(threshold*2)); err != nil {
		t.Fatalf("set: %v", err)
	}

	// Len() must re-resolve and agree with a fresh count(): now everything matches.
	gotLen := high.Len()
	countV, err := callMethod(t, high, "count")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	gotCount := int(mustFloat(t, countV))
	if gotLen != gotCount {
		t.Errorf("post-mutation Len()=%d disagrees with count()=%d (stale memo)", gotLen, gotCount)
	}
	if gotLen != total {
		t.Errorf("post-mutation Len()=%d, want %d (all bibites now match)", gotLen, total)
	}
}
