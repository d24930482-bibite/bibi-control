package thebibites

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
	"github.com/asemones/bibicontrol/script"
	"go.starlark.net/starlark"
)

// bibiteWithSub returns the first bibite entry that has at least one element in
// the named 1:many sub-table, skipping the test if the fixture has none.
func bibiteWithSub(t *testing.T, ls *LoadedSave, table string) string {
	t.Helper()
	for _, name := range ls.access["bibites"].order {
		if len(ls.subRowsFor(table, name)) > 0 {
			return name
		}
	}
	t.Skipf("fixture has no bibite with %s", table)
	return ""
}

func elementAt(t *testing.T, ec *ElementCollection, idx int) *ArrayElement {
	t.Helper()
	it := ec.Iterate()
	defer it.Done()
	var v starlark.Value
	i := 0
	for it.Next(&v) {
		if i == idx {
			e, ok := v.(*ArrayElement)
			if !ok {
				t.Fatalf("element %d is %T, want *ArrayElement", idx, v)
			}
			return e
		}
		i++
	}
	t.Fatalf("no element at index %d (len %d)", idx, ec.Len())
	return nil
}

func subCollection(t *testing.T, ls *LoadedSave, kind, entryName, attr string) *ElementCollection {
	t.Helper()
	e := &Entity{ls: ls, kind: kind, entryName: entryName}
	v, err := e.Attr(attr)
	if err != nil {
		t.Fatalf("Attr(%q): %v", attr, err)
	}
	ec, ok := v.(*ElementCollection)
	if !ok {
		t.Fatalf("%s.%s is %T, want *ElementCollection", kind, attr, v)
	}
	return ec
}

// TestSubCollectionRead: b.synapses iterates in array order, element fields match
// the normalized rows, `index` increments, and an unknown attr is a clean miss.
func TestSubCollectionRead(t *testing.T) {
	ls := loadFixture(t)
	name := bibiteWithSub(t, ls, "bibite_brain_synapses")
	rows := ls.subRowsFor("bibite_brain_synapses", name)

	ec := subCollection(t, ls, "bibite", name, "synapses")
	if ec.Len() != len(rows) {
		t.Fatalf("Len = %d, want %d", ec.Len(), len(rows))
	}

	for i := range rows {
		elem := elementAt(t, ec, i)
		idx, err := elem.Attr("index")
		if err != nil {
			t.Fatalf("Attr(index): %v", err)
		}
		if got, _ := starlark.AsInt32(idx); got != i {
			t.Errorf("element %d index attr = %d, want %d", i, got, i)
		}
		// node_in (BIGINT) read through the binding must match the normalized row's
		// NodeIn field read directly — proves the data-driven fieldIndex resolves.
		got, err := elem.Attr("node_in")
		if err != nil {
			t.Fatalf("Attr(node_in): %v", err)
		}
		gotN, convErr := starlark.AsInt32(got)
		if convErr != nil {
			t.Fatalf("node_in is %s, want int", got.Type())
		}
		wantN := rows[i].FieldByName("NodeIn").Int()
		if int64(gotN) != wantN {
			t.Errorf("element %d node_in = %d, want %d", i, gotN, wantN)
		}
	}

	miss, err := elementAt(t, ec, 0).Attr("does_not_exist")
	if err != nil || miss != nil {
		t.Errorf("unknown attr = (%v, %v), want (nil, nil)", miss, err)
	}
}

// TestSubCollectionAppendPersists: a friendly-kwarg synapse append stages one op,
// the reparsed save shows N+1 synapses with the appended values, and an unrelated
// entry stays byte-identical.
func TestSubCollectionAppendPersists(t *testing.T) {
	ls := loadFixture(t)
	orig, err := tb.ParseFile(fixture, nil)
	if err != nil {
		t.Fatalf("reference parse: %v", err)
	}
	name := bibiteWithSub(t, ls, "bibite_brain_synapses")
	var other string
	for _, n := range ls.access["bibites"].order {
		if n != name {
			other = n
			break
		}
	}

	before := subCollection(t, ls, "bibite", name, "synapses").Len()

	ec := subCollection(t, ls, "bibite", name, "synapses")
	fn := starlark.NewBuiltin("append", ec.appendBuiltin)
	kwargs := []starlark.Tuple{
		{starlark.String("enabled"), starlark.Bool(true)},
		{starlark.String("innovation"), starlark.MakeInt(999)},
		{starlark.String("node_in"), starlark.MakeInt(3)},
		{starlark.String("node_out"), starlark.MakeInt(5)},
		{starlark.String("weight"), starlark.Float(0.25)},
	}
	if _, err := fn.CallInternal(&starlark.Thread{}, nil, kwargs); err != nil {
		t.Fatalf("append: %v", err)
	}
	if ls.stagedOps != 1 {
		t.Errorf("stagedOps = %d, want 1", ls.stagedOps)
	}

	// In-memory re-read still shows the original count (structural, not applied).
	if mid := subCollection(t, ls, "bibite", name, "synapses").Len(); mid != before {
		t.Errorf("in-run synapse Len = %d, want %d (append not applied mid-run)", mid, before)
	}

	tmp := filepath.Join(t.TempDir(), "out.zip")
	if err := ls.WriteSave(tmp); err != nil {
		t.Fatalf("WriteSave: %v", err)
	}

	re, err := tb.ParseFile(tmp, nil)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if other != "" && orig.Entry(other).SHA256 != re.Entry(other).SHA256 {
		t.Errorf("unrelated entry %q SHA256 changed", other)
	}

	ls2, err := Load(tmp)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	ec2 := subCollection(t, ls2, "bibite", name, "synapses")
	if ec2.Len() != before+1 {
		t.Fatalf("synapse count after append = %d, want %d", ec2.Len(), before+1)
	}
	appended := elementAt(t, ec2, before) // appended at the end
	w, _ := appended.Attr("weight")
	if f, _ := starlark.AsFloat(w); f != 0.25 {
		t.Errorf("appended weight = %v, want 0.25", f)
	}
	ni, _ := appended.Attr("node_in")
	if n, _ := starlark.AsInt32(ni); n != 3 {
		t.Errorf("appended node_in = %d, want 3", n)
	}
}

// TestSubCollectionAppendValidation: unknown field, missing required field, and a
// wrong-typed value are each rejected before staging.
func TestSubCollectionAppendValidation(t *testing.T) {
	ls := loadFixture(t)
	name := bibiteWithSub(t, ls, "bibite_brain_synapses")
	ec := subCollection(t, ls, "bibite", name, "synapses")

	full := func() []starlark.Tuple {
		return []starlark.Tuple{
			{starlark.String("enabled"), starlark.Bool(true)},
			{starlark.String("innovation"), starlark.MakeInt(1)},
			{starlark.String("node_in"), starlark.MakeInt(0)},
			{starlark.String("node_out"), starlark.MakeInt(1)},
			{starlark.String("weight"), starlark.Float(0.5)},
		}
	}

	cases := []struct {
		name   string
		kwargs []starlark.Tuple
	}{
		{"unknown field", append(full(), starlark.Tuple{starlark.String("bogus"), starlark.MakeInt(1)})},
		{"missing field", full()[:4]},
		{"wrong type", func() []starlark.Tuple {
			k := full()
			k[4] = starlark.Tuple{starlark.String("weight"), starlark.String("heavy")} // string for DOUBLE
			return k
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fn := starlark.NewBuiltin("append", ec.appendBuiltin)
			if _, err := fn.CallInternal(&starlark.Thread{}, nil, tc.kwargs); err == nil {
				t.Fatalf("append(%s) error = nil, want rejection", tc.name)
			}
			if ls.stagedOps != 0 {
				t.Fatalf("stagedOps = %d after rejected append, want 0", ls.stagedOps)
			}
		})
	}
}

// TestSubCollectionDeletePersists: element.delete() stages one op and the reparsed
// save shows the element gone (N-1).
func TestSubCollectionDeletePersists(t *testing.T) {
	ls := loadFixture(t)
	name := bibiteWithSub(t, ls, "bibite_brain_synapses")
	before := subCollection(t, ls, "bibite", name, "synapses").Len()

	elem := elementAt(t, subCollection(t, ls, "bibite", name, "synapses"), 0)
	if _, err := callMethod(t, elem, "delete"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if ls.stagedOps != 1 {
		t.Errorf("stagedOps = %d, want 1", ls.stagedOps)
	}

	tmp := filepath.Join(t.TempDir(), "out.zip")
	if err := ls.WriteSave(tmp); err != nil {
		t.Fatalf("WriteSave: %v", err)
	}
	ls2, err := Load(tmp)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := subCollection(t, ls2, "bibite", name, "synapses").Len(); got != before-1 {
		t.Errorf("synapse count after delete = %d, want %d", got, before-1)
	}
}

// TestSubCollectionStructuralNotVisibleInRun is the consistency contract for array
// ops: an append is staged but not mirrored, so an in-run DuckDB query still sees
// the original count; only after commit is the change present.
func TestSubCollectionStructuralNotVisibleInRun(t *testing.T) {
	ls := loadFixture(t)
	name := bibiteWithSub(t, ls, "bibite_brain_synapses")

	synCount := func() int {
		rows, err := ls.query("SELECT count(*) FROM bibite_brain_synapses WHERE save_id = ? AND entry_name = ?", ls.saveID, name)
		if err != nil {
			t.Fatalf("count query: %v", err)
		}
		defer rows.Close()
		rows.Next()
		var n int
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		return n
	}

	before := synCount() // opens DuckDB
	ec := subCollection(t, ls, "bibite", name, "synapses")
	fn := starlark.NewBuiltin("append", ec.appendBuiltin)
	kwargs := []starlark.Tuple{
		{starlark.String("enabled"), starlark.Bool(true)},
		{starlark.String("innovation"), starlark.MakeInt(7)},
		{starlark.String("node_in"), starlark.MakeInt(0)},
		{starlark.String("node_out"), starlark.MakeInt(1)},
		{starlark.String("weight"), starlark.Float(0.1)},
	}
	if _, err := fn.CallInternal(&starlark.Thread{}, nil, kwargs); err != nil {
		t.Fatalf("append: %v", err)
	}
	during := synCount()

	if during != before {
		t.Errorf("in-run synapse count = %d, want %d (append not mirrored)", during, before)
	}
	if ls.dbOpenCount != 1 {
		t.Errorf("dbOpenCount = %d, want 1", ls.dbOpenCount)
	}
	if ls.flushStmtCount != 0 {
		t.Errorf("flushStmtCount = %d, want 0 (structural op does not mirror)", ls.flushStmtCount)
	}

	tmp := filepath.Join(t.TempDir(), "out.zip")
	if err := ls.WriteSave(tmp); err != nil {
		t.Fatalf("WriteSave: %v", err)
	}
	ls2, err := Load(tmp)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := subCollection(t, ls2, "bibite", name, "synapses").Len(); got != before+1 {
		t.Errorf("post-commit synapse count = %d, want %d", got, before+1)
	}
}

// TestSubCollectionStomachRoundTrip exercises a non-brain array (stomach contents)
// through append + delete.
func TestSubCollectionStomachRoundTrip(t *testing.T) {
	ls := loadFixture(t)
	name := bibiteWithSub(t, ls, "bibite_stomach_contents")
	before := subCollection(t, ls, "bibite", name, "stomach").Len()

	ec := subCollection(t, ls, "bibite", name, "stomach")
	fn := starlark.NewBuiltin("append", ec.appendBuiltin)
	kwargs := []starlark.Tuple{
		{starlark.String("amount"), starlark.Float(2.0)},
		{starlark.String("average_chunk_amount"), starlark.Float(0.5)},
		{starlark.String("material"), starlark.String("Meat")},
	}
	if _, err := fn.CallInternal(&starlark.Thread{}, nil, kwargs); err != nil {
		t.Fatalf("append: %v", err)
	}

	tmp := filepath.Join(t.TempDir(), "out.zip")
	if err := ls.WriteSave(tmp); err != nil {
		t.Fatalf("WriteSave: %v", err)
	}
	ls2, err := Load(tmp)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	ec2 := subCollection(t, ls2, "bibite", name, "stomach")
	if ec2.Len() != before+1 {
		t.Fatalf("stomach count after append = %d, want %d", ec2.Len(), before+1)
	}
	mat, _ := elementAt(t, ec2, before).Attr("material")
	if s, _ := starlark.AsString(mat); s != "Meat" {
		t.Errorf("appended material = %q, want Meat", s)
	}

	// delete the appended element back out
	elem := elementAt(t, ec2, before)
	if _, err := callMethod(t, elem, "delete"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	tmp2 := filepath.Join(t.TempDir(), "out2.zip")
	if err := ls2.WriteSave(tmp2); err != nil {
		t.Fatalf("WriteSave: %v", err)
	}
	ls3, err := Load(tmp2)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := subCollection(t, ls3, "bibite", name, "stomach").Len(); got != before {
		t.Errorf("stomach count after delete = %d, want %d", got, before)
	}
}

// TestSubCollectionEggSynapse covers the egg kind path: read + delete on an egg's
// brain synapses.
func TestSubCollectionEggSynapse(t *testing.T) {
	ls := loadFixture(t)
	ta := ls.access["eggs"]
	if ta == nil || len(ta.order) == 0 {
		t.Skip("fixture has no eggs")
	}
	var name string
	for _, n := range ta.order {
		if len(ls.subRowsFor("egg_brain_synapses", n)) > 0 {
			name = n
			break
		}
	}
	if name == "" {
		t.Skip("fixture has no egg with synapses")
	}
	before := subCollection(t, ls, "egg", name, "synapses").Len()

	elem := elementAt(t, subCollection(t, ls, "egg", name, "synapses"), 0)
	if _, err := callMethod(t, elem, "delete"); err != nil {
		t.Fatalf("egg synapse delete: %v", err)
	}
	tmp := filepath.Join(t.TempDir(), "out.zip")
	if err := ls.WriteSave(tmp); err != nil {
		t.Fatalf("WriteSave: %v", err)
	}
	ls2, err := Load(tmp)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := subCollection(t, ls2, "egg", name, "synapses").Len(); got != before-1 {
		t.Errorf("egg synapse count after delete = %d, want %d", got, before-1)
	}
}

// TestSubCollectionAppendViaScript: a Starlark program appends a synapse and
// commits; the written save, reloaded, holds the extra element.
func TestSubCollectionAppendViaScript(t *testing.T) {
	ls := loadFixture(t)
	name := bibiteWithSub(t, ls, "bibite_brain_synapses")
	before := subCollection(t, ls, "bibite", name, "synapses").Len()
	tmp := filepath.Join(t.TempDir(), "out.zip")

	program := []byte(fmt.Sprintf(`
def mutate():
    for b in save.bibites:
        if b.entry_name == %q:
            b.synapses.append(enabled=True, innovation=42, node_in=0, node_out=1, weight=0.75)
            break
    return save.commit(%q)

print("staged=%%d" %% mutate())
`, name, tmp))

	res, err := script.Run(context.Background(), program, Globals(ls), script.Options{Filename: "append.star"})
	if err != nil {
		t.Fatalf("script.Run: %v (%+v)", err, res.Diagnostics)
	}

	ls2, err := Load(tmp)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := subCollection(t, ls2, "bibite", name, "synapses").Len(); got != before+1 {
		t.Errorf("scripted append: synapse count = %d, want %d", got, before+1)
	}
}

// TestSetFieldRejectsSubCollection: assigning to a sub-collection name is a clean
// error (it is a collection, not a scalar).
func TestSetFieldRejectsSubCollection(t *testing.T) {
	ls := loadFixture(t)
	name := firstBibiteEntry(t, ls)
	e := &Entity{ls: ls, kind: "bibite", entryName: name}
	if err := e.SetField("synapses", starlark.MakeInt(1)); err == nil {
		t.Fatal("SetField(synapses) error = nil, want rejection")
	}
}
