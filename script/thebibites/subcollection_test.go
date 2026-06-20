package thebibites

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	mutator "github.com/asemones/bibicontrol/savemutator/thebibites"
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
// ops: an append is staged but not mirrored. An in-run DuckDB read after it would
// observe the pre-append set, so the read now fails LOUDLY (M7 bullet 4) instead of
// silently returning the stale count; the change is present after commit.
func TestSubCollectionStructuralNotVisibleInRun(t *testing.T) {
	ls := loadFixture(t)
	name := bibiteWithSub(t, ls, "bibite_brain_synapses")

	synCountErr := func() error {
		rows, err := ls.query("SELECT count(*) FROM bibite_brain_synapses WHERE save_id = ? AND entry_name = ?", ls.saveID, name)
		if err != nil {
			return err
		}
		rows.Close()
		return nil
	}

	before := subCollection(t, ls, "bibite", name, "synapses").Len()
	// A read BEFORE the append works and opens DuckDB once.
	if err := synCountErr(); err != nil {
		t.Fatalf("pre-append count query: %v", err)
	}
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

	// A read AFTER the staged append must refuse loudly, not return stale data.
	if err := synCountErr(); err == nil {
		t.Fatalf("in-run read after staged append returned nil error (silent stale read), want a loud refusal")
	} else if !strings.Contains(err.Error(), "structural edit") {
		t.Errorf("post-append read error = %q, want it to mention the structural edit", err.Error())
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
s = open()

def mutate():
    for b in s.bibites:
        if b.entry_name == %q:
            b.synapses.append(enabled=True, innovation=42, node_in=0, node_out=1, weight=0.75)
            break
    return s.commit(%q)

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

// TestSubCollectionGuardColumnNotBool: the delete stale-guard must be a
// higher-cardinality column, never a boolean. For synapses the sorted writable set
// leads with the bool `enabled`, which is a near-constant true and so a useless
// guard against a shifted index. chooseGuardColumn must skip it. Every
// sub-collection with a writable column must land a guard whose derived type is not
// kindBool (unless, defensively, all its writable columns are bool).
func TestSubCollectionGuardColumnNotBool(t *testing.T) {
	syn := subCollectionRegistry()["bibite"]["synapses"]
	if syn == nil {
		t.Fatal("no synapses sub-collection registered")
	}
	if syn.guardColumn == "enabled" {
		t.Errorf("synapse guard column = %q (the bool); want a higher-cardinality column", syn.guardColumn)
	}
	if syn.guardColumn == "" {
		t.Fatal("synapse guard column is empty; want a writable guard")
	}
	if got := deriveType(syn.elementAttrs[syn.guardColumn].sqlType); got == kindBool {
		t.Errorf("synapse guard column %q is a boolean type; want non-bool", syn.guardColumn)
	}

	for kind, subs := range subCollectionRegistry() {
		for attr, sc := range subs {
			if len(sc.writableCols) == 0 {
				continue
			}
			allBool := true
			for _, col := range sc.writableCols {
				if deriveType(sc.elementAttrs[col].sqlType) != kindBool {
					allBool = false
					break
				}
			}
			if allBool {
				continue // defensive fallback to writableCols[0] is acceptable
			}
			if got := deriveType(sc.elementAttrs[sc.guardColumn].sqlType); got == kindBool {
				t.Errorf("%s.%s guard column %q is bool despite a non-bool writable column existing",
					kind, attr, sc.guardColumn)
			}
		}
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

// buildNavLS builds a minimal LoadedSave with a synthetic brain for the given
// entryName, where NodeRowIndex and NodeIndex intentionally DIFFER for node 0:
//
//	node[0]: NodeRowIndex=0, NodeIndex=nodeIdx0
//	node[1]: NodeRowIndex=1, NodeIndex=nodeIdx1
//	synapse[0]: NodeIn=nodeIdx0, NodeOut=nodeIdx1
//
// This forces the test to use NodeIndex for the join; a slot-based (NodeRowIndex)
// lookup for nodeIn=nodeIdx0 would find the wrong node or none at all when
// nodeIdx0 != 0.
func buildNavLS(entryName string, nodeIdx0, nodeIdx1 int64) *LoadedSave {
	ls := &LoadedSave{
		session:    mutator.NewSession(nil),
		willCommit: false,
		tables: tb.ExtractedSave{
			Bibites: []tb.BibiteRow{
				{EntryName: entryName},
			},
			BibiteBrainNodes: []tb.BrainNodeRow{
				{EntryName: entryName, NodeRowIndex: 0, NodeIndex: nodeIdx0},
				{EntryName: entryName, NodeRowIndex: 1, NodeIndex: nodeIdx1},
			},
			BibiteBrainSynapses: []tb.BrainSynapseRow{
				{EntryName: entryName, SynapseRowIndex: 0, NodeIn: nodeIdx0, NodeOut: nodeIdx1, Weight: 0.5, Enabled: true, Innovation: 7},
			},
		},
	}
	ls.buildAccess()
	return ls
}

// synElemAt returns the idx-th synapse ArrayElement for entryName.
func synElemAt(t *testing.T, ls *LoadedSave, entryName string, idx int) *ArrayElement {
	t.Helper()
	ec := subCollection(t, ls, "bibite", entryName, "synapses")
	return elementAt(t, ec, idx)
}

// nodeElemAt returns the idx-th node ArrayElement for entryName.
func nodeElemAt(t *testing.T, ls *LoadedSave, entryName string, idx int) *ArrayElement {
	t.Helper()
	ec := subCollection(t, ls, "bibite", entryName, "nodes")
	return elementAt(t, ec, idx)
}

// attrInt64 reads a starlark int attr as int64, failing on error.
func attrInt64(t *testing.T, e *ArrayElement, name string) int64 {
	t.Helper()
	v, err := e.Attr(name)
	if err != nil {
		t.Fatalf("Attr(%q): %v", name, err)
	}
	if v == nil {
		t.Fatalf("Attr(%q) returned nil", name)
	}
	n, ok := v.(starlark.Int)
	if !ok {
		t.Fatalf("Attr(%q) = %T, want Int", name, v)
	}
	i64, ok := n.Int64()
	if !ok {
		t.Fatalf("Attr(%q) overflows int64", name)
	}
	return i64
}

// TestSynapseSourceTargetByNodeIndex verifies syn.source and syn.target resolve by
// logical NodeIndex, NOT the array slot (NodeRowIndex). The fixture is constructed
// so NodeRowIndex=0 has NodeIndex=99 — a slot-based lookup for node_in=99 would
// search array slot 99 (out of bounds in a 2-node brain) and fail or hit the wrong
// element, making the test fail if the implementation uses the wrong field.
func TestSynapseSourceTargetByNodeIndex(t *testing.T) {
	const entry = "bibites/syntest.bb8"
	const nodeIdx0, nodeIdx1 = int64(99), int64(200) // intentionally != NodeRowIndex (0, 1)
	ls := buildNavLS(entry, nodeIdx0, nodeIdx1)

	syn := synElemAt(t, ls, entry, 0)

	// syn.source must resolve to the node whose node_index == node_in (99)
	srcV, err := syn.Attr("source")
	if err != nil {
		t.Fatalf("syn.source: %v", err)
	}
	src, ok := srcV.(*ArrayElement)
	if !ok {
		t.Fatalf("syn.source returned %T, want *ArrayElement", srcV)
	}
	// The resolved node's node_index must equal node_in (99)
	if got := attrInt64(t, src, "node_index"); got != nodeIdx0 {
		t.Errorf("syn.source.node_index = %d, want %d", got, nodeIdx0)
	}
	// Array ordinal (index attr) must be 0 — the actual array slot of nodeIdx0
	if got := attrInt64(t, src, "index"); got != 0 {
		t.Errorf("syn.source.index (array ordinal) = %d, want 0", got)
	}

	// syn.target must resolve to the node whose node_index == node_out (200)
	dstV, err := syn.Attr("target")
	if err != nil {
		t.Fatalf("syn.target: %v", err)
	}
	dst, ok := dstV.(*ArrayElement)
	if !ok {
		t.Fatalf("syn.target returned %T, want *ArrayElement", dstV)
	}
	if got := attrInt64(t, dst, "node_index"); got != nodeIdx1 {
		t.Errorf("syn.target.node_index = %d, want %d", got, nodeIdx1)
	}
	if got := attrInt64(t, dst, "index"); got != 1 {
		t.Errorf("syn.target.index (array ordinal) = %d, want 1", got)
	}

	// Slot-based confusion guard: if implementation had used array-slot lookup,
	// node_in=99 would try slot 99 in a 2-node array — that would fail.  But we
	// also verify explicitly: the returned node's index attr is 0, not 99.
	if srcIdx := attrInt64(t, src, "index"); srcIdx == nodeIdx0 {
		t.Errorf("syn.source.index == node_index (%d) — likely resolved by NodeIndex as slot, not by NodeIndex→ordinal lookup", nodeIdx0)
	}
}

// TestNodeInputsOutputs verifies n.inputs / n.outputs return the correct synapse
// handles with the correct ordinals and endpoint values.
func TestNodeInputsOutputs(t *testing.T) {
	ls := loadFixture(t)
	name := bibiteWithSub(t, ls, "bibite_brain_synapses")

	// Find a node that has at least one input or output synapse using direct scan.
	nodeRows := ls.subRowsFor("bibite_brain_nodes", name)
	synRows := ls.subRowsFor("bibite_brain_synapses", name)
	if len(nodeRows) == 0 {
		t.Skip("fixture bibite has no nodes")
	}

	// Pick node[0] and compute expected inputs/outputs by brute-force scan.
	nodeRow := nodeRows[0]
	niSpec := subCollectionRegistry()["bibite"]["nodes"].elementAttrs["node_index"]
	synSpec := subCollectionRegistry()["bibite"]["synapses"]
	nodeIndex := nodeRow.FieldByIndex(niSpec.fieldIndex).Int()

	nodeInSpec := synSpec.elementAttrs["node_in"]
	nodeOutSpec := synSpec.elementAttrs["node_out"]

	var wantInputOrdinals, wantOutputOrdinals []int
	for i, row := range synRows {
		if row.FieldByIndex(nodeOutSpec.fieldIndex).Int() == nodeIndex {
			wantInputOrdinals = append(wantInputOrdinals, i)
		}
		if row.FieldByIndex(nodeInSpec.fieldIndex).Int() == nodeIndex {
			wantOutputOrdinals = append(wantOutputOrdinals, i)
		}
	}

	nodeElem := nodeElemAt(t, ls, name, 0)

	// --- n.inputs ---
	inputsV, err := nodeElem.Attr("inputs")
	if err != nil {
		t.Fatalf("n.inputs: %v", err)
	}
	inputsList, ok := inputsV.(*starlark.List)
	if !ok {
		t.Fatalf("n.inputs returned %T, want *starlark.List", inputsV)
	}
	if inputsList.Len() != len(wantInputOrdinals) {
		t.Errorf("n.inputs len = %d, want %d", inputsList.Len(), len(wantInputOrdinals))
	}
	gotInputOrdinals := make([]int, inputsList.Len())
	for i := 0; i < inputsList.Len(); i++ {
		elem := inputsList.Index(i).(*ArrayElement)
		gotInputOrdinals[i] = int(elem.index)
		// Each element's node_out must equal this node's node_index
		nodeOutVal := elem.row.FieldByIndex(nodeOutSpec.fieldIndex).Int()
		if nodeOutVal != nodeIndex {
			t.Errorf("inputs[%d].node_out = %d, want %d", i, nodeOutVal, nodeIndex)
		}
	}
	sort.Ints(gotInputOrdinals)
	sort.Ints(wantInputOrdinals)
	if len(gotInputOrdinals) != len(wantInputOrdinals) {
		t.Errorf("inputs ordinals len mismatch: got %v, want %v", gotInputOrdinals, wantInputOrdinals)
	} else {
		for i := range gotInputOrdinals {
			if gotInputOrdinals[i] != wantInputOrdinals[i] {
				t.Errorf("inputs ordinals[%d] = %d, want %d", i, gotInputOrdinals[i], wantInputOrdinals[i])
			}
		}
	}

	// --- n.outputs ---
	outputsV, err := nodeElem.Attr("outputs")
	if err != nil {
		t.Fatalf("n.outputs: %v", err)
	}
	outputsList, ok := outputsV.(*starlark.List)
	if !ok {
		t.Fatalf("n.outputs returned %T, want *starlark.List", outputsV)
	}
	if outputsList.Len() != len(wantOutputOrdinals) {
		t.Errorf("n.outputs len = %d, want %d", outputsList.Len(), len(wantOutputOrdinals))
	}
	gotOutputOrdinals := make([]int, outputsList.Len())
	for i := 0; i < outputsList.Len(); i++ {
		elem := outputsList.Index(i).(*ArrayElement)
		gotOutputOrdinals[i] = int(elem.index)
		// Each element's node_in must equal this node's node_index
		nodeInVal := elem.row.FieldByIndex(nodeInSpec.fieldIndex).Int()
		if nodeInVal != nodeIndex {
			t.Errorf("outputs[%d].node_in = %d, want %d", i, nodeInVal, nodeIndex)
		}
	}
	sort.Ints(gotOutputOrdinals)
	sort.Ints(wantOutputOrdinals)
	if len(gotOutputOrdinals) != len(wantOutputOrdinals) {
		t.Errorf("outputs ordinals len mismatch: got %v, want %v", gotOutputOrdinals, wantOutputOrdinals)
	} else {
		for i := range gotOutputOrdinals {
			if gotOutputOrdinals[i] != wantOutputOrdinals[i] {
				t.Errorf("outputs ordinals[%d] = %d, want %d", i, gotOutputOrdinals[i], wantOutputOrdinals[i])
			}
		}
	}
}

// TestSynapseSourceMissIsLoud verifies that a synapse referencing a non-existent
// node_index returns a non-nil error (loud), never (None, nil).
func TestSynapseSourceMissIsLoud(t *testing.T) {
	const entry = "bibites/misstest.bb8"
	// nodeIdx=999 is absent from nodes (only nodeIdx 10 and 11 exist).
	ls := buildNavLS(entry, 10, 11)

	// Manually inject a dangling synapse: node_in=999 which has no matching node.
	ls.tables.BibiteBrainSynapses = append(ls.tables.BibiteBrainSynapses, tb.BrainSynapseRow{
		EntryName: entry, SynapseRowIndex: 1, NodeIn: 999, NodeOut: 10,
	})
	// Reset the sub-row index so it picks up the extra synapse.
	ls.subRowOnce = sync.Once{}
	ls.subRowIdx = nil
	ls.nodeByIndexOnce = sync.Once{}
	ls.nodeByIndexMap = nil

	// The dangling synapse is at index 1.
	syn := synElemAt(t, ls, entry, 1)
	_, err := syn.Attr("source")
	if err == nil {
		t.Fatal("syn.source with missing node_index returned nil error, want loud error")
	}
}

// TestNavigationAttrNames verifies the four nav attrs appear only on the matching
// element kind: synapse elements have source/target; node elements have
// inputs/outputs; stomach elements have none of the four.
func TestNavigationAttrNames(t *testing.T) {
	reg := subCollectionRegistry()["bibite"]

	synSpec := reg["synapses"]
	nodeSpec := reg["nodes"]
	stomachSpec := reg["stomach"]

	if synSpec == nil || nodeSpec == nil || stomachSpec == nil {
		t.Fatal("expected bibite to have synapses, nodes, and stomach sub-collections")
	}

	// Build dummy ArrayElements just to call AttrNames (no real rows needed here —
	// AttrNames only checks e.spec.attr).
	synElem := &ArrayElement{spec: synSpec}
	nodeElem := &ArrayElement{spec: nodeSpec}
	stomachElem := &ArrayElement{spec: stomachSpec}

	toSet := func(names []string) map[string]bool {
		m := make(map[string]bool, len(names))
		for _, n := range names {
			m[n] = true
		}
		return m
	}

	synNames := toSet(synElem.AttrNames())
	nodeNames := toSet(nodeElem.AttrNames())
	stomachNames := toSet(stomachElem.AttrNames())

	// Synapse: must have source + target, must NOT have inputs/outputs.
	for _, want := range []string{"source", "target"} {
		if !synNames[want] {
			t.Errorf("synapse AttrNames missing %q", want)
		}
	}
	for _, notWant := range []string{"inputs", "outputs"} {
		if synNames[notWant] {
			t.Errorf("synapse AttrNames unexpectedly contains %q", notWant)
		}
	}

	// Node: must have inputs + outputs, must NOT have source/target.
	for _, want := range []string{"inputs", "outputs"} {
		if !nodeNames[want] {
			t.Errorf("node AttrNames missing %q", want)
		}
	}
	for _, notWant := range []string{"source", "target"} {
		if nodeNames[notWant] {
			t.Errorf("node AttrNames unexpectedly contains %q", notWant)
		}
	}

	// Stomach: none of the four.
	for _, notWant := range []string{"source", "target", "inputs", "outputs"} {
		if stomachNames[notWant] {
			t.Errorf("stomach AttrNames unexpectedly contains %q", notWant)
		}
	}
}

// TestEggBrainNavigation verifies source/target/outputs resolution works for the
// egg kind, proving kind-generic dispatch. Skips cleanly if the fixture has no egg
// with a brain that has both nodes and synapses.
func TestEggBrainNavigation(t *testing.T) {
	ls := loadFixture(t)
	ta := ls.access["eggs"]
	if ta == nil || len(ta.order) == 0 {
		t.Skip("fixture has no eggs")
	}
	var eggName string
	for _, n := range ta.order {
		if len(ls.subRowsFor("egg_brain_synapses", n)) > 0 &&
			len(ls.subRowsFor("egg_brain_nodes", n)) > 0 {
			eggName = n
			break
		}
	}
	if eggName == "" {
		t.Skip("fixture has no egg with both nodes and synapses")
	}

	// Get the first synapse and read its source and target nodes.
	eggSynSpec := subCollectionRegistry()["egg"]["synapses"]
	eggSynRows := ls.subRowsFor("egg_brain_synapses", eggName)
	if len(eggSynRows) == 0 {
		t.Skip("no synapse rows for egg")
	}
	synElem := &ArrayElement{
		ls:        ls,
		kind:      "egg",
		entryName: eggName,
		spec:      eggSynSpec,
		row:       eggSynRows[0],
		index:     0,
	}

	// syn.source must return a node element.
	srcV, err := synElem.Attr("source")
	if err != nil {
		t.Fatalf("egg syn.source: %v", err)
	}
	src, ok := srcV.(*ArrayElement)
	if !ok {
		t.Fatalf("egg syn.source returned %T, want *ArrayElement", srcV)
	}
	if src.spec.attr != "nodes" {
		t.Errorf("egg syn.source.spec.attr = %q, want nodes", src.spec.attr)
	}

	// syn.target must return a node element.
	dstV, err := synElem.Attr("target")
	if err != nil {
		t.Fatalf("egg syn.target: %v", err)
	}
	dst, ok := dstV.(*ArrayElement)
	if !ok {
		t.Fatalf("egg syn.target returned %T, want *ArrayElement", dstV)
	}
	if dst.spec.attr != "nodes" {
		t.Errorf("egg syn.target.spec.attr = %q, want nodes", dst.spec.attr)
	}

	// The source node's .outputs must include the original synapse (round-trip).
	// src is the source (node_in side), so this synapse should appear in src.outputs
	// (synapses whose node_in == src.node_index).
	outputsV, err := src.Attr("outputs")
	if err != nil {
		t.Fatalf("egg src.outputs: %v", err)
	}
	outputsList, ok := outputsV.(*starlark.List)
	if !ok {
		t.Fatalf("egg src.outputs returned %T, want *starlark.List", outputsV)
	}
	if outputsList.Len() == 0 {
		t.Error("egg src.outputs is empty; expected at least the original synapse")
	}

	// The target node's .inputs must include the original synapse.
	// dst is the target (node_out side), so the synapse should appear in dst.inputs
	// (synapses whose node_out == dst.node_index).
	inputsV, err := dst.Attr("inputs")
	if err != nil {
		t.Fatalf("egg dst.inputs: %v", err)
	}
	inputsList, ok := inputsV.(*starlark.List)
	if !ok {
		t.Fatalf("egg dst.inputs returned %T, want *starlark.List", inputsV)
	}
	if inputsList.Len() == 0 {
		t.Error("egg dst.inputs is empty; expected at least the original synapse")
	}
}

// attrString reads a starlark string attr as a Go string, failing on error or
// wrong type.
func attrString(t *testing.T, e *ArrayElement, name string) string {
	t.Helper()
	v, err := e.Attr(name)
	if err != nil {
		t.Fatalf("Attr(%q): %v", name, err)
	}
	if v == nil {
		t.Fatalf("Attr(%q) returned nil", name)
	}
	s, ok := starlark.AsString(v)
	if !ok {
		t.Fatalf("Attr(%q) = %T, want String", name, v)
	}
	return s
}

// buildDescNavLS builds a synthetic brain with two nodes that have distinct
// Desc fields: node0 Desc="Accelerate" (NodeIndex=10), node1 Desc="Hidden0"
// (NodeIndex=11), and one synapse. NodeRowIndex intentionally equals the array
// ordinal (0, 1) for simplicity.
func buildDescNavLS(entryName string) *LoadedSave {
	ls := &LoadedSave{
		session:    mutator.NewSession(nil),
		willCommit: false,
		tables: tb.ExtractedSave{
			Bibites: []tb.BibiteRow{
				{EntryName: entryName},
			},
			BibiteBrainNodes: []tb.BrainNodeRow{
				{EntryName: entryName, NodeRowIndex: 0, NodeIndex: 10, Desc: "Accelerate"},
				{EntryName: entryName, NodeRowIndex: 1, NodeIndex: 11, Desc: "Hidden0"},
			},
			BibiteBrainSynapses: []tb.BrainSynapseRow{
				{EntryName: entryName, SynapseRowIndex: 0, NodeIn: 10, NodeOut: 11, Weight: 0.5, Enabled: true, Innovation: 1},
			},
		},
	}
	ls.buildAccess()
	return ls
}

// buildAmbigLS builds a brain with two nodes whose Descs differ only by case,
// used to test foldLookup ambiguity.
func buildAmbigLS(entryName string) *LoadedSave {
	ls := &LoadedSave{
		session:    mutator.NewSession(nil),
		willCommit: false,
		tables: tb.ExtractedSave{
			Bibites: []tb.BibiteRow{
				{EntryName: entryName},
			},
			BibiteBrainNodes: []tb.BrainNodeRow{
				{EntryName: entryName, NodeRowIndex: 0, NodeIndex: 20, Desc: "Foo"},
				{EntryName: entryName, NodeRowIndex: 1, NodeIndex: 21, Desc: "foo"},
			},
			BibiteBrainSynapses: []tb.BrainSynapseRow{},
		},
	}
	ls.buildAccess()
	return ls
}

// TestNodeLookupByDescExact: b.nodes["Accelerate"] returns the node whose
// node_desc == "Accelerate" at array ordinal 0.
func TestNodeLookupByDescExact(t *testing.T) {
	const entry = "bibites/desctest.bb8"
	ls := buildDescNavLS(entry)
	ec := subCollection(t, ls, "bibite", entry, "nodes")

	v, ok, err := ec.Get(starlark.String("Accelerate"))
	if err != nil {
		t.Fatalf("Get(Accelerate): %v", err)
	}
	if !ok {
		t.Fatal("Get(Accelerate) found=false, want true")
	}
	elem, isElem := v.(*ArrayElement)
	if !isElem {
		t.Fatalf("Get returned %T, want *ArrayElement", v)
	}
	if got := attrString(t, elem, "node_desc"); got != "Accelerate" {
		t.Errorf("node_desc = %q, want Accelerate", got)
	}
	if got := attrInt64(t, elem, "index"); got != 0 {
		t.Errorf("index (array ordinal) = %d, want 0", got)
	}
}

// TestNodeLookupByDescCaseFold: case-insensitive variants resolve to the same node.
func TestNodeLookupByDescCaseFold(t *testing.T) {
	const entry = "bibites/desctest.bb8"
	ls := buildDescNavLS(entry)
	ec := subCollection(t, ls, "bibite", entry, "nodes")

	for _, query := range []string{"accelerate", "ACCELERATE", "aCcElErAtE"} {
		v, ok, err := ec.Get(starlark.String(query))
		if err != nil {
			t.Fatalf("Get(%q): %v", query, err)
		}
		if !ok {
			t.Fatalf("Get(%q) found=false, want true", query)
		}
		elem := v.(*ArrayElement)
		if got := attrString(t, elem, "node_desc"); got != "Accelerate" {
			t.Errorf("Get(%q).node_desc = %q, want Accelerate", query, got)
		}
		if got := attrInt64(t, elem, "index"); got != 0 {
			t.Errorf("Get(%q).index = %d, want 0", query, got)
		}
	}
}

// TestNodeLookupHiddenName: hidden node names (e.g. "Hidden0") are ordinary
// Desc values — no special-casing, fold works the same way.
func TestNodeLookupHiddenName(t *testing.T) {
	const entry = "bibites/desctest.bb8"
	ls := buildDescNavLS(entry)
	ec := subCollection(t, ls, "bibite", entry, "nodes")

	v, ok, err := ec.Get(starlark.String("hidden0"))
	if err != nil {
		t.Fatalf("Get(hidden0): %v", err)
	}
	if !ok {
		t.Fatal("Get(hidden0) found=false, want true")
	}
	elem := v.(*ArrayElement)
	if got := attrString(t, elem, "node_desc"); got != "Hidden0" {
		t.Errorf("node_desc = %q, want Hidden0", got)
	}
	if got := attrInt64(t, elem, "index"); got != 1 {
		t.Errorf("index = %d, want 1", got)
	}
}

// TestNodeLookupMissIsLoud: a miss returns (nil, false, nil) causing a Starlark
// KeyError — not a silent None.
func TestNodeLookupMissIsLoud(t *testing.T) {
	const entry = "bibites/desctest.bb8"
	ls := buildDescNavLS(entry)
	ec := subCollection(t, ls, "bibite", entry, "nodes")

	v, ok, err := ec.Get(starlark.String("nope"))
	if err != nil {
		t.Fatalf("Get(nope) returned unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("Get(nope) found=true, want false (miss)")
	}
	if v != nil {
		t.Fatalf("Get(nope) value=%v, want nil", v)
	}
}

// TestNodeLookupAmbiguousIsLoud: two nodes whose Descs differ only by case make
// a fold query with no exact match return a non-nil error (ambiguity), not a
// silent pick.
func TestNodeLookupAmbiguousIsLoud(t *testing.T) {
	const entry = "bibites/ambigtest.bb8"
	ls := buildAmbigLS(entry)
	ec := subCollection(t, ls, "bibite", entry, "nodes")

	// "FOO" has no exact match; both "Foo" and "foo" fold-match, causing ambiguity.
	v, ok, err := ec.Get(starlark.String("FOO"))
	if err == nil {
		t.Fatalf("Get(FOO) error=nil, want ambiguity error (v=%v, ok=%v)", v, ok)
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("ambiguity error = %q, want it to mention 'ambiguous'", err.Error())
	}
}

// TestNodeLookupByIntPositional: integer subscripts do positional access.
func TestNodeLookupByIntPositional(t *testing.T) {
	const entry = "bibites/desctest.bb8"
	ls := buildDescNavLS(entry)
	ec := subCollection(t, ls, "bibite", entry, "nodes")

	// nodes[0] -> Accelerate (ordinal 0)
	v0, ok0, err0 := ec.Get(starlark.MakeInt(0))
	if err0 != nil || !ok0 {
		t.Fatalf("Get(0): err=%v ok=%v", err0, ok0)
	}
	elem0 := v0.(*ArrayElement)
	if got := attrString(t, elem0, "node_desc"); got != "Accelerate" {
		t.Errorf("nodes[0].node_desc = %q, want Accelerate", got)
	}

	// nodes[1] -> Hidden0 (ordinal 1)
	v1, ok1, err1 := ec.Get(starlark.MakeInt(1))
	if err1 != nil || !ok1 {
		t.Fatalf("Get(1): err=%v ok=%v", err1, ok1)
	}
	elem1 := v1.(*ArrayElement)
	if got := attrString(t, elem1, "node_desc"); got != "Hidden0" {
		t.Errorf("nodes[1].node_desc = %q, want Hidden0", got)
	}

	// nodes[-1] -> last node (Hidden0, ordinal 1)
	vN, okN, errN := ec.Get(starlark.MakeInt(-1))
	if errN != nil || !okN {
		t.Fatalf("Get(-1): err=%v ok=%v", errN, okN)
	}
	elemN := vN.(*ArrayElement)
	if got := attrInt64(t, elemN, "index"); got != 1 {
		t.Errorf("nodes[-1].index = %d, want 1", got)
	}

	// nodes[99] -> out-of-range loud error
	_, _, errOOB := ec.Get(starlark.MakeInt(99))
	if errOOB == nil {
		t.Fatal("Get(99) error=nil, want out-of-range error")
	}
	if !strings.Contains(errOOB.Error(), "out of range") {
		t.Errorf("out-of-range error = %q, want mention of 'out of range'", errOOB.Error())
	}
}

// TestNodeLookupStringOnNonNodes: a string key on synapses or stomach is a loud
// error (string subscript is only valid on nodes).
func TestNodeLookupStringOnNonNodes(t *testing.T) {
	const entry = "bibites/desctest.bb8"
	ls := buildDescNavLS(entry)

	for _, attr := range []string{"synapses"} {
		ec := subCollection(t, ls, "bibite", entry, attr)
		_, _, err := ec.Get(starlark.String("x"))
		if err == nil {
			t.Errorf("%s[\"x\"] error=nil, want loud error", attr)
		}
	}
}

// TestNodeLookupResolvedHandleNavigates: a Desc-resolved node element supports
// M5 navigation (.inputs / .outputs), proving the returned handle is a full node
// handle with the correct spec.
func TestNodeLookupResolvedHandleNavigates(t *testing.T) {
	const entry = "bibites/desctest.bb8"
	ls := buildDescNavLS(entry)
	ec := subCollection(t, ls, "bibite", entry, "nodes")

	v, ok, err := ec.Get(starlark.String("Accelerate"))
	if err != nil || !ok {
		t.Fatalf("Get(Accelerate): err=%v ok=%v", err, ok)
	}
	elem := v.(*ArrayElement)

	// .outputs on the source node (NodeIndex=10) should find the synapse.
	outputsV, err := elem.Attr("outputs")
	if err != nil {
		t.Fatalf("Accelerate.outputs: %v", err)
	}
	outputsList, ok2 := outputsV.(*starlark.List)
	if !ok2 {
		t.Fatalf("outputs returned %T, want *starlark.List", outputsV)
	}
	if outputsList.Len() == 0 {
		t.Error("Accelerate.outputs is empty; expected the one synapse (NodeIn=10)")
	}

	// .inputs on the target node (NodeIndex=11, Desc="Hidden0") should find the same synapse.
	vHidden, ok3, err3 := ec.Get(starlark.String("Hidden0"))
	if err3 != nil || !ok3 {
		t.Fatalf("Get(Hidden0): err=%v ok=%v", err3, ok3)
	}
	inputsV, err := vHidden.(*ArrayElement).Attr("inputs")
	if err != nil {
		t.Fatalf("Hidden0.inputs: %v", err)
	}
	inputsList, ok4 := inputsV.(*starlark.List)
	if !ok4 {
		t.Fatalf("inputs returned %T, want *starlark.List", inputsV)
	}
	if inputsList.Len() == 0 {
		t.Error("Hidden0.inputs is empty; expected the one synapse (NodeOut=11)")
	}
}

// TestNodeLookupViaScript: end-to-end Starlark interpreter test that b.nodes["accelerate"].desc
// works through the evaluator.
func TestNodeLookupViaScript(t *testing.T) {
	ls := loadFixture(t)
	// Find a bibite that has nodes with non-empty Desc values.
	nodeRows := ls.subRowsFor("bibite_brain_nodes", "")
	var (
		targetEntry string
		targetDesc  string
	)
	descAttr := subCollectionRegistry()["bibite"]["nodes"].elementAttrs["node_desc"]
	for _, name := range ls.access["bibites"].order {
		rows := ls.subRowsFor("bibite_brain_nodes", name)
		for _, row := range rows {
			d := row.FieldByIndex(descAttr.fieldIndex).String()
			if d != "" {
				targetEntry = name
				targetDesc = d
				break
			}
		}
		if targetEntry != "" {
			break
		}
	}
	_ = nodeRows // suppress unused warning
	if targetEntry == "" {
		t.Skip("fixture has no bibite with a named node")
	}

	program := []byte(fmt.Sprintf(`
s = open()

def check():
    for b in s.bibites:
        if b.entry_name == %q:
            n = b.nodes[%q]
            got = n.node_desc
            if got != %q:
                fail("expected desc %%r, got %%r" %% (%q, got))
            return got
    fail("entry not found")

print("desc=%%s" %% check())
`, targetEntry, strings.ToLower(targetDesc), targetDesc, targetDesc))

	res, err := script.Run(context.Background(), program, Globals(ls), script.Options{Filename: "desc_lookup.star"})
	if err != nil {
		t.Fatalf("script.Run: %v (%+v)", err, res.Diagnostics)
	}
}
