package thebibites

import (
	"strings"
	"testing"

	"go.starlark.net/starlark"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

// TestGeneGet is the contract proof for the tolerant gene read that replaced the
// removed gene() point read: b.genes.get(key, default=None). It pins:
//   - a hit returns the value-direct scalar (exact and recased);
//   - a genuine miss returns the default (None, or the supplied default);
//   - a fold-collision is loud (NOT swallowed into the default);
//   - b.genes["absent"] stays loud (found=false -> Starlark KeyError), the
//     asymmetry §4 wants preserved;
//   - b.Attr("gene") is gone (regression guard so the point read cannot creep back).
func TestGeneGet(t *testing.T) {
	ls := loadFixture(t)
	entry, gene := firstNumberGene(t, ls)

	c := &GeneCollection{ls: ls, kind: "bibite", entryName: entry}

	// Baseline: subscript value-direct read for the exact name.
	want, found, err := c.Get(starlark.String(gene))
	if err != nil || !found {
		t.Fatalf("Get(%q): found=%v err=%v", gene, found, err)
	}

	// .get("Name") returns the same value-direct scalar as the subscript.
	got, err := callMethod(t, c, "get", starlark.String(gene))
	if err != nil {
		t.Fatalf("genes.get(%q): %v", gene, err)
	}
	if got != want {
		t.Errorf("genes.get(%q)=%v, Get(%q)=%v, want same", gene, got, gene, want)
	}

	// .get recased resolves the same value (case-fold inherited from M3).
	upper := strings.ToUpper(gene)
	gotUp, err := callMethod(t, c, "get", starlark.String(upper))
	if err != nil {
		t.Fatalf("genes.get(%q) recased: %v", upper, err)
	}
	if gotUp != want {
		t.Errorf("genes.get(%q) recased=%v, want %v", upper, gotUp, want)
	}

	// .get("absent") returns None (default default).
	absent, err := callMethod(t, c, "get", starlark.String("DefinitelyAbsent_M4"))
	if err != nil {
		t.Fatalf("genes.get(absent): %v", err)
	}
	if absent != starlark.None {
		t.Errorf("genes.get(absent)=%v, want None", absent)
	}

	// .get("absent", 0.0) returns the supplied default (default arg is plumbed).
	withDefault, err := callMethod(t, c, "get", starlark.String("DefinitelyAbsent_M4"), starlark.Float(0.0))
	if err != nil {
		t.Fatalf("genes.get(absent, 0.0): %v", err)
	}
	if withDefault != starlark.Float(0.0) {
		t.Errorf("genes.get(absent, 0.0)=%v, want 0.0", withDefault)
	}

	// b.genes["absent"] stays loud: found=false -> Starlark KeyError. Never None.
	if v, miss, err := c.Get(starlark.String("DefinitelyAbsent_M4")); err != nil || miss || v != nil {
		t.Errorf("genes[absent]: value=%v found=%v err=%v, want nil/false/nil (loud KeyError)", v, miss, err)
	}

	// b.Attr("gene") is gone: the point read must not creep back.
	if v, err := (&Entity{ls: ls, kind: "bibite", entryName: entry}).Attr("gene"); err != nil || v != nil {
		t.Errorf("Entity.Attr(\"gene\")=(%v, %v), want (nil, nil) — gene() must be removed", v, err)
	}
}

// TestGeneGetCollisionLoud verifies .get propagates a fold-collision as a loud
// error and never resolves it to the default. A collision is "≥2 canonical names
// fold equal and the query exact-matches neither" — distinct from a genuine miss,
// which .get tolerates. Mirrors the collision fixture in name_fold_test.go.
func TestGeneGetCollisionLoud(t *testing.T) {
	ls := loadFixture(t)
	entry := firstBibiteEntry(t, ls)

	const (
		canon1 = "M4FooGene" // folds to "m4foogene"
		canon2 = "m4FooGene" // also folds to "m4foogene"
		query  = "m4foogene" // fold query: exact-matches neither
	)
	backing := []tb.GeneRow{
		{EntryName: entry, GeneName: canon1, Type: tb.ScalarNumber, NumberValue: 1.0},
		{EntryName: entry, GeneName: canon2, Type: tb.ScalarNumber, NumberValue: 2.0},
	}
	set := &geneSet{
		backing: backing,
		order:   []int{0, 1},
		byName:  map[string]int{canon1: 0, canon2: 1},
	}

	ls.geneOnce.Do(ls.buildGeneIndex)
	ls.geneIdx["bibite"][entry] = set

	c := &GeneCollection{ls: ls, kind: "bibite", entryName: entry}

	// .get must error on the collision, NOT swallow it into the default.
	_, err := callMethod(t, c, "get", starlark.String(query))
	if err == nil {
		t.Fatalf("genes.get(%q) collision: want error, got nil (default-swallowed)", query)
	}
	if !strings.Contains(err.Error(), canon1) || !strings.Contains(err.Error(), canon2) {
		t.Errorf("collision error %q should name both %q and %q", err.Error(), canon1, canon2)
	}

	// Even with an explicit default supplied, the collision is still loud.
	if _, err := callMethod(t, c, "get", starlark.String(query), starlark.Float(99)); err == nil {
		t.Errorf("genes.get(%q, default) collision: want error, got nil", query)
	}
}
