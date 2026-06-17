package thebibites

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	mutator "github.com/asemones/bibicontrol/savemutator/thebibites"
	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
	"github.com/asemones/bibicontrol/script"
	"go.starlark.net/starlark"
)

// leafBibite returns a bibite that is not referenced as anyone's child, so a
// delete without prune passes the mutator's referential guard at commit time.
// Opens DuckDB (reused thereafter).
func leafBibite(t *testing.T, ls *LoadedSave) string {
	t.Helper()
	rows, err := ls.query(
		"SELECT entry_name FROM bibites WHERE save_id = ? "+
			"AND body_id NOT IN (SELECT child_body_id FROM bibite_children) "+
			"ORDER BY entry_name LIMIT 1", ls.saveID)
	if err != nil {
		t.Fatalf("leaf query: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("fixture has no unreferenced bibite")
	}
	var name string
	if err := rows.Scan(&name); err != nil {
		t.Fatalf("scan leaf: %v", err)
	}
	return name
}

func bibiteCountSQL(t *testing.T, ls *LoadedSave) int {
	t.Helper()
	rows, err := ls.query("SELECT count(*) FROM bibites WHERE save_id = ?", ls.saveID)
	if err != nil {
		t.Fatalf("count query: %v", err)
	}
	defer rows.Close()
	rows.Next()
	var n int
	if err := rows.Scan(&n); err != nil {
		t.Fatalf("scan count: %v", err)
	}
	return n
}

// TestDeletePersistsAndIsolates: b.delete() on a leaf bibite removes the entry,
// decrements scene nBibites, and leaves an unrelated bibite byte-identical.
func TestDeletePersistsAndIsolates(t *testing.T) {
	ls := loadFixture(t)
	orig, err := tb.ParseFile(fixture, nil)
	if err != nil {
		t.Fatalf("reference parse: %v", err)
	}

	target := leafBibite(t, ls)
	var other string
	for _, name := range ls.access["bibites"].order {
		if name != target {
			other = name
			break
		}
	}
	if other == "" {
		t.Fatal("fixture needs a second bibite")
	}

	e := &Entity{ls: ls, kind: "bibite", entryName: target}
	if _, err := callMethod(t, e, "delete"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if ls.stagedOps != 1 {
		t.Errorf("stagedOps = %d, want 1", ls.stagedOps)
	}

	tmp := filepath.Join(t.TempDir(), "out.zip")
	if err := ls.WriteSave(tmp); err != nil {
		t.Fatalf("WriteSave: %v", err)
	}
	re, err := tb.ParseFile(tmp, nil)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}

	if re.Entry(target) != nil {
		t.Errorf("deleted entry %q still present after commit", target)
	}
	if orig.Entry(other).SHA256 != re.Entry(other).SHA256 {
		t.Errorf("unrelated entry %q SHA256 changed: %s -> %s", other, orig.Entry(other).SHA256, re.Entry(other).SHA256)
	}
	if orig.Scene == nil || re.Scene == nil {
		t.Fatal("missing scene state")
	}
	if re.Scene.NBibites != orig.Scene.NBibites-1 {
		t.Errorf("scene nBibites = %d, want %d", re.Scene.NBibites, orig.Scene.NBibites-1)
	}
}

// TestDeleteNotVisibleInRunButVisibleAfterCommit is the headline structural-op
// contract: a delete is staged but not mirrored, so an in-run query still sees
// the entity; only after commit is it gone.
func TestDeleteNotVisibleInRunButVisibleAfterCommit(t *testing.T) {
	ls := loadFixture(t)
	target := leafBibite(t, ls) // opens DuckDB

	before := bibiteCountSQL(t, ls)
	e := &Entity{ls: ls, kind: "bibite", entryName: target}
	if _, err := callMethod(t, e, "delete"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	during := bibiteCountSQL(t, ls)

	if during != before {
		t.Errorf("in-run bibite count = %d, want %d (delete not mirrored)", during, before)
	}
	if ls.dbOpenCount != 1 {
		t.Errorf("dbOpenCount = %d, want 1 (no re-import)", ls.dbOpenCount)
	}
	if ls.flushStmtCount != 0 {
		t.Errorf("flushStmtCount = %d, want 0 (structural op does not mirror)", ls.flushStmtCount)
	}

	tmp := filepath.Join(t.TempDir(), "out.zip")
	if err := ls.WriteSave(tmp); err != nil {
		t.Fatalf("WriteSave: %v", err)
	}
	re, err := tb.ParseFile(tmp, nil)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if re.Entry(target) != nil {
		t.Errorf("deleted entry %q still present after commit", target)
	}
}

// TestDeleteEgg: an egg deletes through the same method (no parent links / scene
// count) and is gone after commit.
func TestDeleteEgg(t *testing.T) {
	ls := loadFixture(t)
	ta := ls.access["eggs"]
	if ta == nil || len(ta.order) == 0 {
		t.Skip("fixture has no eggs")
	}
	target := ta.order[0]

	e := &Entity{ls: ls, kind: "egg", entryName: target}
	if _, err := callMethod(t, e, "delete"); err != nil {
		t.Fatalf("delete egg: %v", err)
	}
	if ls.stagedOps != 1 {
		t.Errorf("stagedOps = %d, want 1", ls.stagedOps)
	}

	tmp := filepath.Join(t.TempDir(), "out.zip")
	if err := ls.WriteSave(tmp); err != nil {
		t.Fatalf("WriteSave: %v", err)
	}
	re, err := tb.ParseFile(tmp, nil)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if re.Entry(target) != nil {
		t.Errorf("deleted egg %q still present after commit", target)
	}
}

// TestDeletePruneWiring is a white-box check that the prune kwarg reaches the
// staged operation's DeleteOptions, independent of fixture parent/child topology
// (the refuse-vs-prune behavior is covered in savemutator's
// TestStageDeleteBibiteParentChildLink).
func TestDeletePruneWiring(t *testing.T) {
	assertOp := func(t *testing.T, ls *LoadedSave, wantPrune bool) {
		t.Helper()
		ops := ls.session.StagedOperations()
		if len(ops) != 1 {
			t.Fatalf("staged ops = %d, want 1", len(ops))
		}
		if ops[0].Kind != mutator.OperationDeleteEntry {
			t.Errorf("op kind = %q, want delete_entry", ops[0].Kind)
		}
		if ops[0].DeleteOptions.PruneParentLinks != wantPrune {
			t.Errorf("PruneParentLinks = %t, want %t", ops[0].DeleteOptions.PruneParentLinks, wantPrune)
		}
	}

	t.Run("prune=True sets PruneParentLinks", func(t *testing.T) {
		ls := loadFixture(t)
		e := &Entity{ls: ls, kind: "bibite", entryName: firstBibiteEntry(t, ls)}
		if _, err := callMethod(t, e, "delete", starlark.Bool(true)); err != nil {
			t.Fatalf("delete(prune=True): %v", err)
		}
		assertOp(t, ls, true)
	})

	t.Run("default leaves PruneParentLinks false", func(t *testing.T) {
		ls := loadFixture(t)
		e := &Entity{ls: ls, kind: "bibite", entryName: firstBibiteEntry(t, ls)}
		if _, err := callMethod(t, e, "delete"); err != nil {
			t.Fatalf("delete(): %v", err)
		}
		assertOp(t, ls, false)
	})
}

// TestDeleteViaScript: a Starlark program deletes a bibite by entry_name and
// commits; the written save, reparsed, no longer holds the entry.
func TestDeleteViaScript(t *testing.T) {
	ls := loadFixture(t)
	name := leafBibite(t, ls)
	tmp := filepath.Join(t.TempDir(), "out.zip")

	program := []byte(fmt.Sprintf(`
s = open()

def mutate():
    for b in s.bibites:
        if b.entry_name == %q:
            b.delete()
            break
    return s.commit(%q)

print("staged=%%d" %% mutate())
`, name, tmp))

	res, err := script.Run(context.Background(), program, Globals(ls), script.Options{Filename: "delete.star"})
	if err != nil {
		t.Fatalf("script.Run: %v (%+v)", err, res.Diagnostics)
	}

	re, err := tb.ParseFile(tmp, nil)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if re.Entry(name) != nil {
		t.Errorf("scripted delete: entry %q still present", name)
	}
}

// TestBulkWhereDelete: where(predicate).delete() stages a whole-entity delete for
// exactly the matching entities — proving the predicate is applied (iteration alone
// ignores it) — and the reparsed save loses exactly those. Splits on median energy
// for a strict subset, and uses prune=True so a matched non-leaf bibite does not trip
// the referential guard at commit.
func TestBulkWhereDelete(t *testing.T) {
	ls := loadFixture(t)
	bibites := &EntityCollection{ls: ls, kind: "bibite"}
	total := bibites.Len()
	if total < 2 {
		t.Skip("need >= 2 bibites")
	}
	medV, err := callMethod(t, bibites, "median", starlark.String("energy"))
	if err != nil {
		t.Fatalf("median: %v", err)
	}
	pred := fmt.Sprintf("energy < %v", mustFloat(t, medV))

	filtered, err := callMethod(t, bibites, "where", starlark.String(pred))
	if err != nil {
		t.Fatalf("where: %v", err)
	}
	fc, ok := filtered.(*EntityCollection)
	if !ok {
		t.Fatalf("where returned %T, want *EntityCollection", filtered)
	}
	cntV, err := callMethod(t, fc, "count")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	want := mustInt(t, cntV)
	if want <= 0 || want >= int64(total) {
		t.Skipf("predicate %q matched %d of %d (need a strict subset)", pred, want, total)
	}

	res, err := callMethod(t, fc, "delete", starlark.Bool(true)) // prune=True
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got := mustInt(t, res); got != want {
		t.Errorf("delete returned %d, want %d (match count)", got, want)
	}
	if int64(ls.stagedOps) != want {
		t.Errorf("stagedOps = %d, want %d", ls.stagedOps, want)
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
	if got := len(tables.Bibites); got != total-int(want) {
		t.Errorf("bibite count after bulk delete = %d, want %d (deleted %d)", got, total-int(want), want)
	}
}
