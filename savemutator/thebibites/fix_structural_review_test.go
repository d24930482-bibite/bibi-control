package thebibites

import (
	"archive/zip"
	"path/filepath"
	"testing"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

// parseMultiSynapseArchive builds a bibite whose brain carries a synapse array of
// `count` elements, each tagged with a unique Inov (its original index) so a
// delete that hits the wrong element is detectable. The synapse delete guard is a
// non-boolean writable column (weight here), so descending-order application must
// keep each guard valid too.
func parseMultiSynapseArchive(t *testing.T, count int) *tb.Archive {
	t.Helper()

	synapses := make([]any, count)
	for i := 0; i < count; i++ {
		synapses[i] = map[string]any{
			"Inov":    i, // unique marker == original index
			"NodeIn":  0,
			"NodeOut": 1,
			"Weight":  i,
			"En":      true,
		}
	}
	root := map[string]any{
		"body":  map[string]any{"id": 42},
		"brain": map[string]any{"Nodes": []any{}, "Synapses": synapses},
	}
	raw, err := encodeJSON(root, true)
	if err != nil {
		t.Fatalf("encodeJSON(bibite) error = %v", err)
	}

	sourcePath := filepath.Join(t.TempDir(), "multisynapse.zip")
	scene := append([]byte(nil), utf8BOM...)
	scene = append(scene, []byte(`{"nBibites":1}`)...)
	archive := &tb.Archive{
		Entries: []tb.Entry{
			{Index: 0, Name: "scene.bb8scene", Kind: tb.EntryScene, Method: zip.Deflate, Raw: scene},
			{Index: 1, Name: "bibites/bibite_0.bb8", Kind: tb.EntryBibite, Method: zip.Deflate, Raw: raw},
		},
	}
	if err := tb.WriteArchive(sourcePath, archive); err != nil {
		t.Fatalf("WriteArchive(multisynapse) error = %v", err)
	}
	parsed, err := tb.ParseFile(sourcePath, nil)
	if err != nil {
		t.Fatalf("ParseFile(multisynapse) error = %v", err)
	}
	return parsed
}

// synapseMarkers reads brain.Synapses[*].Inov for the bibite, the original-index
// markers the synthetic archive assigns.
func synapseMarkers(t *testing.T, root any) []int64 {
	t.Helper()
	value, ok, err := getJSONPath(root, "brain.Synapses")
	if err != nil || !ok {
		t.Fatalf("getJSONPath(brain.Synapses) = %v/%t/%v", value, ok, err)
	}
	arr, ok := value.([]any)
	if !ok {
		t.Fatalf("brain.Synapses = %T, want array", value)
	}
	out := make([]int64, 0, len(arr))
	for i, el := range arr {
		obj, ok := el.(map[string]any)
		if !ok {
			t.Fatalf("synapse %d = %T, want object", i, el)
		}
		v, ok, err := getJSONPath(obj, "Inov")
		if err != nil || !ok {
			t.Fatalf("synapse %d Inov = %v/%t/%v", i, v, ok, err)
		}
		n, ok := jsonNumberToInt64(v)
		if !ok {
			t.Fatalf("synapse %d Inov %v not an integer", i, v)
		}
		out = append(out, n)
	}
	return out
}

// synapseDeleteRef stages a synapse element delete located by load-time positional
// index and guarded by that element's current Weight, exactly as the script
// ArrayElement.delete path does.
func synapseDeleteRef(t *testing.T, root any, idx int) SQLValueRef {
	t.Helper()
	value, ok, err := getJSONPath(root, "brain.Synapses["+itoa(idx)+"].Weight")
	if err != nil || !ok {
		t.Fatalf("getJSONPath(weight[%d]) = %v/%t/%v", idx, value, ok, err)
	}
	weight, ok := jsonNumberToInt64(value)
	if !ok {
		t.Fatalf("synapse %d Weight %v not an integer", idx, value)
	}
	return SQLValueRef{
		Table:              "bibite_brain_synapses",
		Column:             "weight",
		EntryName:          "bibites/bibite_0.bb8",
		BodyID:             42,
		HasBodyID:          true,
		SynapseRowIndex:    idx,
		HasSynapseRowIndex: true,
	}.WithExpected(weight)
}

func wantMarkers(t *testing.T, got []int64, want ...int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("synapse markers = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("synapse markers = %v, want %v", got, want)
		}
	}
}

// TestApplyMultiElementDeleteDescendingOrder is the deep-fix check for review #2:
// staging two (or three) element deletes against the SAME array by their
// load-time indices must remove exactly those original elements, regardless of
// stage order. Without descending-order application the first delete shifts the
// array and a later delete addresses the wrong element.
func TestApplyMultiElementDeleteDescendingOrder(t *testing.T) {
	t.Run("deletes [2,3] staged ascending remove exactly those originals", func(t *testing.T) {
		archive := parseMultiSynapseArchive(t, 5) // markers 0..4
		session := NewSession(archive)
		root := archive.Entry("bibites/bibite_0.bb8").JSON
		// Stage in ASCENDING order (the order that breaks naive in-order apply).
		if err := session.StageSQLDelete(synapseDeleteRef(t, root, 2)); err != nil {
			t.Fatalf("StageSQLDelete(2) error = %v", err)
		}
		if err := session.StageSQLDelete(synapseDeleteRef(t, root, 3)); err != nil {
			t.Fatalf("StageSQLDelete(3) error = %v", err)
		}
		fresh, err := session.Commit(filepath.Join(t.TempDir(), "del23.zip"))
		if err != nil {
			t.Fatalf("Commit() error = %v", err)
		}
		// Originals 2 and 3 gone; 0,1,4 remain in order.
		wantMarkers(t, synapseMarkers(t, fresh.Entry("bibites/bibite_0.bb8").JSON), 0, 1, 4)
	})

	t.Run("deletes [1,2,3] staged ascending remove exactly those originals", func(t *testing.T) {
		archive := parseMultiSynapseArchive(t, 5) // markers 0..4
		session := NewSession(archive)
		root := archive.Entry("bibites/bibite_0.bb8").JSON
		for _, idx := range []int{1, 2, 3} {
			if err := session.StageSQLDelete(synapseDeleteRef(t, root, idx)); err != nil {
				t.Fatalf("StageSQLDelete(%d) error = %v", idx, err)
			}
		}
		fresh, err := session.Commit(filepath.Join(t.TempDir(), "del123.zip"))
		if err != nil {
			t.Fatalf("Commit() error = %v", err)
		}
		wantMarkers(t, synapseMarkers(t, fresh.Entry("bibites/bibite_0.bb8").JSON), 0, 4)
	})

	t.Run("descending stage order still removes exactly those originals", func(t *testing.T) {
		archive := parseMultiSynapseArchive(t, 5)
		session := NewSession(archive)
		root := archive.Entry("bibites/bibite_0.bb8").JSON
		for _, idx := range []int{3, 2} { // already descending
			if err := session.StageSQLDelete(synapseDeleteRef(t, root, idx)); err != nil {
				t.Fatalf("StageSQLDelete(%d) error = %v", idx, err)
			}
		}
		fresh, err := session.Commit(filepath.Join(t.TempDir(), "del32.zip"))
		if err != nil {
			t.Fatalf("Commit() error = %v", err)
		}
		wantMarkers(t, synapseMarkers(t, fresh.Entry("bibites/bibite_0.bb8").JSON), 0, 1, 4)
	})
}

// TestOrderElementDeletesSlotPreserving checks that the re-order is a slot-
// preserving permutation: only same-array element deletes swap among the slots
// they already occupy, and deletes against a different array keep their position.
func TestOrderElementDeletesSlotPreserving(t *testing.T) {
	target := BibiteTarget(BibiteRef{EntryName: "bibites/bibite_0.bb8", BodyID: 42})
	// Two arrays interleaved: synapses[1], nodes[0], synapses[2], synapses[3].
	ops := []Operation{
		Delete(target, "brain.Synapses[1]"),
		Delete(target, "brain.Nodes[0]"),
		Delete(target, "brain.Synapses[2]"),
		Delete(target, "brain.Synapses[3]"),
	}
	out := orderElementDeletes(ops)
	wantPaths := []string{
		"brain.Synapses[3]", // synapse slots (0,2,3) now hold 3,2,1 descending
		"brain.Nodes[0]",    // untouched slot
		"brain.Synapses[2]",
		"brain.Synapses[1]",
	}
	for i, want := range wantPaths {
		if out[i].Path != want {
			t.Fatalf("ordered op %d path = %q, want %q (full = [%q %q %q %q])", i, out[i].Path, want,
				out[0].Path, out[1].Path, out[2].Path, out[3].Path)
		}
	}
	// Original slice is not mutated.
	if ops[0].Path != "brain.Synapses[1]" {
		t.Fatalf("orderElementDeletes mutated the input slice")
	}
}

// TestApplyMultiPelletDeleteDescendingOrder confirms the deep fix also covers the
// pellet element-delete path (StageDeletePellet), which carries a scene-count
// reconciliation alongside each delete.
func TestApplyMultiPelletDeleteDescendingOrder(t *testing.T) {
	archive := parseSyntheticArchive(t)
	session := NewSession(archive)
	// Grow group 0 to four pellets so we can delete two by original index.
	for i := 0; i < 3; i++ {
		pellet := map[string]any{"pellet": map[string]any{"material": "Plant", "amount": 10 + i}}
		if err := session.StageAppendPellet(0, pellet); err != nil {
			t.Fatalf("StageAppendPellet(%d) error = %v", i, err)
		}
	}
	fresh, err := session.Commit(filepath.Join(t.TempDir(), "grown.zip"))
	if err != nil {
		t.Fatalf("Commit(grow) error = %v", err)
	}
	if got := jsonArrayLen(t, fresh.Entry("pellets.bb8scene").JSON, "pellets[0].pellets"); got != 4 {
		t.Fatalf("pellet count after grow = %d, want 4", got)
	}

	amount := func(root any, i int) int64 {
		v, ok, err := getJSONPath(root, "pellets[0].pellets["+itoa(i)+"].pellet.amount")
		if err != nil || !ok {
			t.Fatalf("amount[%d] = %v/%t/%v", i, v, ok, err)
		}
		n, ok := jsonNumberToInt64(v)
		if !ok {
			t.Fatalf("amount[%d] %v not an integer", i, v)
		}
		return n
	}
	before := []int64{
		amount(fresh.Entry("pellets.bb8scene").JSON, 0),
		amount(fresh.Entry("pellets.bb8scene").JSON, 1),
		amount(fresh.Entry("pellets.bb8scene").JSON, 2),
		amount(fresh.Entry("pellets.bb8scene").JSON, 3),
	}

	session = NewSession(fresh)
	// Ascending stage order against original indices 1 and 2.
	if err := session.StageDeletePellet(0, 1); err != nil {
		t.Fatalf("StageDeletePellet(0,1) error = %v", err)
	}
	if err := session.StageDeletePellet(0, 2); err != nil {
		t.Fatalf("StageDeletePellet(0,2) error = %v", err)
	}
	fresh, err = session.Commit(filepath.Join(t.TempDir(), "pdel.zip"))
	if err != nil {
		t.Fatalf("Commit(delete) error = %v", err)
	}
	if got := jsonArrayLen(t, fresh.Entry("pellets.bb8scene").JSON, "pellets[0].pellets"); got != 2 {
		t.Fatalf("pellet count after delete = %d, want 2", got)
	}
	// Originals 0 and 3 survive (1 and 2 removed).
	gotAfter := []int64{
		amount(fresh.Entry("pellets.bb8scene").JSON, 0),
		amount(fresh.Entry("pellets.bb8scene").JSON, 1),
	}
	if gotAfter[0] != before[0] || gotAfter[1] != before[3] {
		t.Fatalf("surviving amounts = %v, want originals [%v %v]", gotAfter, before[0], before[3])
	}
	if got := scenePelletCount(t, fresh); got != 2 {
		t.Fatalf("scene nPellets after delete = %d, want 2", got)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
