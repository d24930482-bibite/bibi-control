package thebibites

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"go.starlark.net/starlark"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
	"github.com/asemones/bibicontrol/script"
)

// firstNumberGene returns the first bibite entry that has a numeric gene, plus that
// gene's name — picked from the fixture rather than hardcoded.
func firstNumberGene(t *testing.T, ls *LoadedSave) (entry, gene string) {
	t.Helper()
	entry = firstBibiteEntry(t, ls)
	set := ls.genesFor("bibite", entry)
	if set == nil {
		t.Skip("first bibite has no genes")
	}
	for _, idx := range set.order {
		if set.backing[idx].Type == tb.ScalarNumber {
			return entry, set.backing[idx].GeneName
		}
	}
	t.Skip("first bibite has no numeric gene")
	return "", ""
}

// findGene locates a gene row by (entry_name, gene_name) in a reparsed set.
func findGene(rows []tb.GeneRow, entry, name string) (tb.GeneRow, bool) {
	for _, r := range rows {
		if r.EntryName == entry && r.GeneName == name {
			return r, true
		}
	}
	return tb.GeneRow{}, false
}

// geneNumberSQL reads one gene's number_value back through DuckDB, so it observes a
// mirrored in-run write.
func geneNumberSQL(t *testing.T, ls *LoadedSave, entry, gene string) float64 {
	t.Helper()
	rows, err := ls.query(
		"SELECT number_value FROM bibite_genes WHERE save_id = ? AND entry_name = ? AND gene_name = ?",
		ls.saveID, entry, gene)
	if err != nil {
		t.Fatalf("gene query: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("no gene row for %s/%s", entry, gene)
	}
	var v float64
	if err := rows.Scan(&v); err != nil {
		t.Fatalf("scan gene: %v", err)
	}
	return v
}

// TestGeneWritePersists: b.genes["x"] = v stages and persists through reparse; the
// new value reads back via both b.genes.get("x") and b.genes["x"] (write-through).
func TestGeneWritePersists(t *testing.T) {
	ls := loadFixture(t)
	entry, gene := firstNumberGene(t, ls)

	c := &GeneCollection{ls: ls, kind: "bibite", entryName: entry}
	const want = 0.4242
	if err := c.SetKey(starlark.String(gene), starlark.Float(want)); err != nil {
		t.Fatalf("SetKey: %v", err)
	}

	// Read-back via b.genes.get("x") and b.genes["x"] (in-memory write-through).
	pt, err := callMethod(t, c, "get", starlark.String(gene))
	if err != nil {
		t.Fatalf("genes.get(): %v", err)
	}
	if got := mustFloat(t, pt); got != want {
		t.Errorf("b.genes.get(%q) = %v, want %v", gene, got, want)
	}
	mv, found, err := c.Get(starlark.String(gene))
	if err != nil || !found {
		t.Fatalf("b.genes[%q]: found=%v err=%v", gene, found, err)
	}
	if got := mustFloat(t, mv); got != want {
		t.Errorf("b.genes[%q] = %v, want %v", gene, got, want)
	}

	tmp := filepath.Join(t.TempDir(), "out.zip")
	if err := ls.WriteSave(tmp); err != nil {
		t.Fatalf("WriteSave: %v", err)
	}
	re, err := tb.ParseFile(tmp, nil)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	tables := tb.ExtractTables(re.SHA256, re)
	got, ok := findGene(tables.BibiteGenes, entry, gene)
	if !ok {
		t.Fatalf("gene %s/%s missing after reparse", entry, gene)
	}
	if got.NumberValue != want {
		t.Errorf("reparsed gene %q = %v, want %v", gene, got.NumberValue, want)
	}
}

// TestGeneWriteMirrorEverything: a gene write is visible to an in-run save.sql over
// bibite_genes in the same run (keyed by entry_name + gene_name), DuckDB opened
// once and the change applied as a single mirror UPDATE.
func TestGeneWriteMirrorEverything(t *testing.T) {
	ls := loadFixture(t)
	entry, gene := firstNumberGene(t, ls)

	_ = geneNumberSQL(t, ls, entry, gene) // open DuckDB (snapshot)
	c := &GeneCollection{ls: ls, kind: "bibite", entryName: entry}
	const want = 1234.5
	if err := c.SetKey(starlark.String(gene), starlark.Float(want)); err != nil {
		t.Fatalf("SetKey: %v", err)
	}
	if got := geneNumberSQL(t, ls, entry, gene); got != want {
		t.Errorf("in-run SQL gene = %v, want %v (mirror not applied)", got, want)
	}
	if ls.dbOpenCount != 1 {
		t.Errorf("dbOpenCount = %d, want 1", ls.dbOpenCount)
	}
	if ls.flushStmtCount != 1 {
		t.Errorf("flushStmtCount = %d, want 1 (single mirror UPDATE)", ls.flushStmtCount)
	}
}

// geneNumberSQLByPath reads one gene's number_value back through DuckDB keyed by
// path, so colliding leaf gene_names across the two gene nesting levels are
// distinguishable — exactly the discriminator the mirror keys on.
func geneNumberSQLByPath(t *testing.T, ls *LoadedSave, entry, path string) float64 {
	t.Helper()
	rows, err := ls.query(
		"SELECT number_value FROM bibite_genes WHERE save_id = ? AND entry_name = ? AND path = ?",
		ls.saveID, entry, path)
	if err != nil {
		t.Fatalf("gene query: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("no gene row for %s/%s", entry, path)
	}
	var v float64
	if err := rows.Scan(&v); err != nil {
		t.Fatalf("scan gene: %v", err)
	}
	return v
}

// TestGeneWriteMirrorKeyedByPath: when two gene rows on one entity share a leaf
// gene_name but differ by path (the two JSON nesting levels flatten into one table
// sharing the gene_name namespace), an in-run write to one mirrors exactly that
// path's row in DuckDB and leaves the sibling untouched. Keying the mirror on
// gene_name instead would rewrite both rows — the bug this guards.
func TestGeneWriteMirrorKeyedByPath(t *testing.T) {
	ls := loadFixture(t)
	entry, gene := firstNumberGene(t, ls)

	// Force a leaf-name collision: clone the target row under a second, distinct
	// path with the SAME gene_name, in both the in-memory backing and DuckDB.
	set := ls.genesFor("bibite", entry)
	idx := set.byName[gene]
	target := set.backing[idx]
	targetPath := target.Path

	sibling := target
	sibling.Path = target.Path + "::collision"
	const siblingBaseline = -7.0
	sibling.NumberValue = siblingBaseline
	set.backing = append(set.backing, sibling)
	// Re-point byName at the appended sibling's backing index, then back, so both
	// indices are valid; the write below uses the original target index explicitly.
	siblingIdx := len(set.backing) - 1
	set.order = append(set.order, siblingIdx)

	_ = geneNumberSQL(t, ls, entry, gene) // open DuckDB (snapshot)
	if _, err := ls.db.ExecContext(context.Background(),
		"INSERT INTO bibite_genes (save_id, entry_name, gene_name, path, number_value) VALUES (?, ?, ?, ?, ?)",
		ls.saveID, entry, gene, sibling.Path, siblingBaseline); err != nil {
		t.Fatalf("seed sibling row: %v", err)
	}

	const want = 555.5
	if err := ls.setGeneValue("bibite", &set.backing[idx], starlark.Float(want)); err != nil {
		t.Fatalf("setGeneValue: %v", err)
	}

	if got := geneNumberSQLByPath(t, ls, entry, targetPath); got != want {
		t.Errorf("target path gene = %v, want %v (mirror missed target)", got, want)
	}
	if got := geneNumberSQLByPath(t, ls, entry, sibling.Path); got != siblingBaseline {
		t.Errorf("sibling path gene = %v, want %v (mirror keyed on gene_name, not path)", got, siblingBaseline)
	}
}

// TestGeneWriteUnknownRejected: writing an unknown gene name is rejected and stages
// nothing (genes are addressed by names already present, not created).
func TestGeneWriteUnknownRejected(t *testing.T) {
	ls := loadFixture(t)
	entry := firstBibiteEntry(t, ls)
	c := &GeneCollection{ls: ls, kind: "bibite", entryName: entry}
	if err := c.SetKey(starlark.String("DefinitelyNotAGene"), starlark.Float(1)); err == nil {
		t.Fatal("expected unknown-gene write to be rejected, got nil")
	}
	if ls.stagedOps != 0 {
		t.Errorf("stagedOps = %d after rejected write, want 0", ls.stagedOps)
	}
}

// TestGeneWriteViaScript: the end-to-end Starlark surface b.genes["x"] = v stages
// and persists.
func TestGeneWriteViaScript(t *testing.T) {
	ls := loadFixture(t)
	entry, gene := firstNumberGene(t, ls)
	tmp := filepath.Join(t.TempDir(), "out.zip")

	program := []byte(fmt.Sprintf(`
s = open()

def mutate():
    for b in s.bibites:
        if b.entry_name == %q:
            b.genes[%q] = 88.0
            break
    return s.commit(%q)

print("staged=%%d" %% mutate())
`, entry, gene, tmp))

	res, err := script.Run(context.Background(), program, Globals(ls), script.Options{Filename: "gene.star"})
	if err != nil {
		t.Fatalf("script.Run: %v (%+v)", err, res.Diagnostics)
	}

	re, err := tb.ParseFile(tmp, nil)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	tables := tb.ExtractTables(re.SHA256, re)
	got, ok := findGene(tables.BibiteGenes, entry, gene)
	if !ok {
		t.Fatalf("gene %s/%s missing after reparse", entry, gene)
	}
	if got.NumberValue != 88.0 {
		t.Errorf("scripted gene value = %v, want 88.0", got.NumberValue)
	}
}
