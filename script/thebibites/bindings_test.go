package thebibites

import (
	"context"
	"strings"
	"testing"

	"go.starlark.net/starlark"

	"github.com/asemones/bibicontrol/script"
)

const fixture = "../../testdata/saves/the-bibites/autosave_20260228004041.zip"

func loadFixture(t *testing.T) *LoadedSave {
	t.Helper()
	ls, err := Load(fixture)
	if err != nil {
		t.Fatalf("Load(%s): %v", fixture, err)
	}
	return ls
}

// collect drains an EntityCollection into a slice of *Entity for assertions.
func collect(t *testing.T, c *EntityCollection) []*Entity {
	t.Helper()
	var out []*Entity
	it := c.Iterate()
	defer it.Done()
	var v starlark.Value
	for it.Next(&v) {
		e, ok := v.(*Entity)
		if !ok {
			t.Fatalf("iterator yielded %T, want *Entity", v)
		}
		out = append(out, e)
	}
	return out
}

func TestLoadCollectionCounts(t *testing.T) {
	ls := loadFixture(t)
	bibites := &EntityCollection{ls: ls, kind: "bibite"}
	if got, want := bibites.Len(), len(ls.tables.Bibites); got != want {
		t.Errorf("bibites Len()=%d, want %d", got, want)
	}
	eggs := &EntityCollection{ls: ls, kind: "egg"}
	if got, want := eggs.Len(), len(ls.tables.Eggs); got != want {
		t.Errorf("eggs Len()=%d, want %d", got, want)
	}
}

func TestAttrReadsMatchRows(t *testing.T) {
	ls := loadFixture(t)
	entities := collect(t, &EntityCollection{ls: ls, kind: "bibite"})
	if len(entities) != len(ls.tables.Bibites) {
		t.Fatalf("enumerated %d bibites, want %d", len(entities), len(ls.tables.Bibites))
	}
	for i, e := range entities {
		row := ls.tables.Bibites[i]

		if got := attrFloat(t, e, "energy"); got != row.Energy {
			t.Errorf("bibite[%d].energy=%v, want %v", i, got, row.Energy)
		}
		if got := attrFloat(t, e, "health"); got != row.Health {
			t.Errorf("bibite[%d].health=%v, want %v", i, got, row.Health)
		}
		if got := attrInt(t, e, "species_id"); got != row.SpeciesID {
			t.Errorf("bibite[%d].species_id=%d, want %d", i, got, row.SpeciesID)
		}
	}
}

// TestSubTableAttr exercises a 1:1 sub-table column (bibite_body) resolved
// through the same registry as identity-table columns.
func TestSubTableAttr(t *testing.T) {
	ls := loadFixture(t)
	entities := collect(t, &EntityCollection{ls: ls, kind: "bibite"})

	byEntry := make(map[string]float64, len(ls.tables.BibiteBody))
	for _, b := range ls.tables.BibiteBody {
		byEntry[b.EntryName] = b.FatReservesAmount
	}
	for _, e := range entities {
		want, ok := byEntry[e.entryName]
		if !ok {
			continue
		}
		if got := attrFloat(t, e, "fat_reserves_amount"); got != want {
			t.Errorf("%s.fat_reserves_amount=%v, want %v", e.entryName, got, want)
		}
	}
}

func TestGeneRead(t *testing.T) {
	ls := loadFixture(t)
	first := collect(t, &EntityCollection{ls: ls, kind: "bibite"})[0]

	v := callGene(t, first, "ClockSpeed")
	f, ok := v.(starlark.Float)
	if !ok {
		t.Fatalf("gene ClockSpeed is %T, want Float", v)
	}
	if float64(f) != 1.0 {
		t.Errorf("gene ClockSpeed=%v, want 1", float64(f))
	}

	if got := callGene(t, first, "NoSuchGene"); got != starlark.None {
		t.Errorf("missing gene returned %v, want None", got)
	}
}

func TestMissingAttr(t *testing.T) {
	ls := loadFixture(t)
	e := collect(t, &EntityCollection{ls: ls, kind: "bibite"})[0]
	v, err := e.Attr("definitely_not_a_field")
	if err != nil {
		t.Fatalf("Attr unknown returned err: %v", err)
	}
	if v != nil {
		t.Errorf("Attr unknown returned %v, want nil (clean AttributeError)", v)
	}
}

// TestScriptRunReads exercises the full path through script.Run and asserts the
// in-memory read path is used — DuckDB is never opened.
func TestScriptRunReads(t *testing.T) {
	ls := loadFixture(t)
	program := []byte(`
def count():
    n = 0
    for b in save.bibites:
        n += 1
        _ = b.energy
    return n
print("count=%d" % count())
`)
	res, err := script.Run(context.Background(), program, Globals(ls), script.Options{Filename: "read.star"})
	if err != nil {
		t.Fatalf("script.Run: %v (diagnostics: %+v)", err, res.Diagnostics)
	}
	want := "count=" + itoa(len(ls.tables.Bibites))
	if !strings.Contains(res.Output, want) {
		t.Errorf("output %q does not contain %q", res.Output, want)
	}
	if ls.db != nil {
		t.Errorf("DuckDB was opened during a pure read run; db should stay nil")
	}
}

// TestScriptMissingAttrDiagnostic confirms a bad attribute surfaces as a clean
// diagnostic, not a panic.
func TestScriptMissingAttrDiagnostic(t *testing.T) {
	ls := loadFixture(t)
	program := []byte(`
def show():
    for b in save.bibites:
        print(b.nonsense)
show()
`)
	res, err := script.Run(context.Background(), program, Globals(ls), script.Options{Filename: "bad.star"})
	if err == nil {
		t.Fatalf("expected error for unknown attribute, got none (output %q)", res.Output)
	}
	if len(res.Diagnostics) == 0 {
		t.Errorf("expected a diagnostic for unknown attribute")
	}
}

func attrFloat(t *testing.T, e *Entity, name string) float64 {
	t.Helper()
	v, err := e.Attr(name)
	if err != nil {
		t.Fatalf("Attr(%q): %v", name, err)
	}
	f, ok := v.(starlark.Float)
	if !ok {
		t.Fatalf("Attr(%q) is %T, want Float", name, v)
	}
	return float64(f)
}

func attrInt(t *testing.T, e *Entity, name string) int64 {
	t.Helper()
	v, err := e.Attr(name)
	if err != nil {
		t.Fatalf("Attr(%q): %v", name, err)
	}
	i, ok := v.(starlark.Int)
	if !ok {
		t.Fatalf("Attr(%q) is %T, want Int", name, v)
	}
	n, _ := i.Int64()
	return n
}

func callGene(t *testing.T, e *Entity, name string) starlark.Value {
	t.Helper()
	attr, err := e.Attr("gene")
	if err != nil {
		t.Fatalf("Attr(gene): %v", err)
	}
	fn, ok := attr.(*starlark.Builtin)
	if !ok {
		t.Fatalf("gene attr is %T, want *Builtin", attr)
	}
	v, err := fn.CallInternal(&starlark.Thread{}, starlark.Tuple{starlark.String(name)}, nil)
	if err != nil {
		t.Fatalf("gene(%q): %v", name, err)
	}
	return v
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
