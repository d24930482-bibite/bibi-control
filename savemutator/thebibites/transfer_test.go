package thebibites

import (
	"archive/zip"
	"path/filepath"
	"strings"
	"testing"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

func withBOM(body string) []byte {
	raw := append([]byte(nil), utf8BOM...)
	return append(raw, []byte(body)...)
}

// parseTransferSource builds a small source world to graft elements out of. Its
// settings differ from the destination's so a settings copy is observable, and
// its bibite/egg carry body/species ids that do NOT collide with the destination
// (parseSpeciesArchive uses body ids 42/43/44, species 1/2/3). The source bibite
// has one synapse, one brain node, one stomach content, and one pellet group so
// every AppendArray surface can be exercised.
func parseTransferSource(t *testing.T) *tb.Archive {
	t.Helper()

	sourcePath := filepath.Join(t.TempDir(), "transfer_source.zip")
	archive := &tb.Archive{
		Entries: []tb.Entry{
			{Index: 0, Name: "scene.bb8scene", Kind: tb.EntryScene, Method: zip.Deflate, Raw: withBOM(`{"nBibites":1,"nPellets":1}`)},
			{Index: 1, Name: "settings.bb8settings", Kind: tb.EntrySettings, Method: zip.Deflate, Raw: withBOM(`{"pelletEnergy":{"Value":99},"worldLabel":{"Value":"source-world"},"zones":[],"zoneGroups":[],"bibites":[],"settingsChangers":[]}`)},
			{Index: 2, Name: "speciesData.json", Kind: tb.EntrySpecies, Method: zip.Deflate, Raw: withBOM(`{"activeSpeciesList":[7,8],"recordedSpecies":[]}`)},
			{Index: 3, Name: "pellets.bb8scene", Kind: tb.EntryPellets, Method: zip.Deflate, Raw: withBOM(`{"pellets":[{"zone":"Zone A","pellets":[{"transform":{"position":[3,4],"rotation":0,"scale":1},"rb2d":{"px":3,"py":4,"vx":0,"vy":0,"r":0},"pellet":{"material":"Plant","amount":5},"matterDecay":{"timeAlive":1,"rotAmount":2}}]}]}`)},
			{Index: 4, Name: "bibites/bibite_0.bb8", Kind: tb.EntryBibite, Method: zip.Deflate, Raw: withBOM(`{"body":{"id":500,"energy":7.5,"stomach":{"content":[{"material":"Meat","amount":3,"averageChunkAmount":1}]}},"genes":{"speciesID":7,"gen":1},"brain":{"Nodes":[{"Index":0,"Type":0,"TypeName":"Input","Value":0.0}],"Synapses":[{"Inov":11,"NodeIn":0,"NodeOut":0,"Weight":0.25,"En":true}]}}`)},
			{Index: 5, Name: "eggs/egg_0.bb8", Kind: tb.EntryEgg, Method: zip.Deflate, Raw: withBOM(`{"egg":{"id":900,"energy":12},"genes":{"speciesID":8,"gen":2,"isReady":true}}`)},
		},
	}
	if err := tb.WriteArchive(sourcePath, archive); err != nil {
		t.Fatalf("WriteArchive(transfer source) error = %v", err)
	}
	parsed, err := tb.ParseFile(sourcePath, nil)
	if err != nil {
		t.Fatalf("ParseFile(transfer source) error = %v", err)
	}
	return parsed
}

// newTransferPair builds a source/destination transfer over the synthetic source
// and the species destination archive.
func newTransferPair(t *testing.T) (*transfer, *Session, *Session) {
	t.Helper()
	src := NewSession(parseTransferSource(t))
	dst := NewSession(parseSpeciesArchive(t))
	tr, err := NewTransfer(src, dst)
	if err != nil {
		t.Fatalf("NewTransfer() error = %v", err)
	}
	return tr, src, dst
}

func TestNewTransferRejectsNil(t *testing.T) {
	src := NewSession(parseTransferSource(t))
	dst := NewSession(parseSpeciesArchive(t))
	if _, err := NewTransfer(nil, dst); err == nil {
		t.Fatalf("NewTransfer(nil src) error = nil, want failure")
	}
	if _, err := NewTransfer(src, nil); err == nil {
		t.Fatalf("NewTransfer(nil dst) error = nil, want failure")
	}
	if _, err := NewTransfer(NewSession(nil), dst); err == nil {
		t.Fatalf("NewTransfer(no-archive src) error = nil, want failure")
	}
}

// Case 1: settings copy round-trip. The source's pelletEnergy is 99; the
// destination's is 20. Collect the source value, set it on the destination, then
// Commit and assert the destination's resolved path now equals the source value.
func TestTransferSettingsCopyRoundTrip(t *testing.T) {
	src := NewSession(parseTransferSource(t))
	dst := NewSession(parseSyntheticArchive(t))
	tr, err := NewTransfer(src, dst)
	if err != nil {
		t.Fatalf("NewTransfer() error = %v", err)
	}

	ref := SQLValueRef{
		Table:          "settings_simulation_values",
		Column:         "number_value",
		EntryName:      SettingsEntryName,
		OwnerKind:      "settings",
		OwnerID:        "settings",
		SettingName:    "pelletEnergy",
		Path:           "settings.pelletEnergy",
		ValueType:      "number",
		WrapperRawJSON: `{"Value":20}`,
	}

	// Resolve the archive path the ref maps to so the assertion reads exactly the
	// cell the resolver mutates (the entry JSON is the raw settings object, not
	// wrapped under a "settings" key).
	_, archivePath, err := ResolveSQLValueRef(ref)
	if err != nil {
		t.Fatalf("ResolveSQLValueRef() error = %v", err)
	}

	// Precondition: destination differs from the source value we are copying.
	before, ok, err := getJSONPath(dst.Archive().Entry(SettingsEntryName).JSON, archivePath)
	if err != nil || !ok {
		t.Fatalf("precondition getJSONPath = %v/%t/%v", before, ok, err)
	}
	if !jsonValuesEqual(before, 20) {
		t.Fatalf("precondition: destination pelletEnergy = %v, want 20", before)
	}

	element, err := tr.CollectSettingsValue(ref)
	if err != nil {
		t.Fatalf("CollectSettingsValue() error = %v", err)
	}
	if !jsonValuesEqual(element.JSON, 99) {
		t.Fatalf("collected source pelletEnergy = %v, want 99", element.JSON)
	}
	if element.Table != ref.Table {
		t.Fatalf("collected element table = %q, want %q", element.Table, ref.Table)
	}

	if err := tr.SetFromCollected(ref, element); err != nil {
		t.Fatalf("SetFromCollected() error = %v", err)
	}
	fresh, err := dst.Commit(filepath.Join(t.TempDir(), "settings_copy.zip"))
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	got, ok, err := getJSONPath(fresh.Entry(SettingsEntryName).JSON, archivePath)
	if err != nil || !ok {
		t.Fatalf("getJSONPath(%q) = %v/%t/%v", archivePath, got, ok, err)
	}
	if !jsonValuesEqual(got, 99) {
		t.Fatalf("destination pelletEnergy after copy = %v, want 99 (source value)", got)
	}
}

// Case 2a: array append round-trip for a brain synapse.
func TestTransferAppendSynapseRoundTrip(t *testing.T) {
	src := NewSession(parseTransferSource(t))
	dst := NewSession(parseSyntheticArchive(t))
	tr, err := NewTransfer(src, dst)
	if err != nil {
		t.Fatalf("NewTransfer() error = %v", err)
	}

	srcRef := SQLValueRef{Table: "bibite_brain_synapses", EntryName: "bibites/bibite_0.bb8", BodyID: 500, HasBodyID: true, SynapseRowIndex: 0, HasSynapseRowIndex: true}
	element, err := tr.CollectArrayElement(srcRef)
	if err != nil {
		t.Fatalf("CollectArrayElement(synapse) error = %v", err)
	}

	// destination synthetic bibite is body id 42 with no synapses.
	dstRef := SQLValueRef{Table: "bibite_brain_synapses", EntryName: "bibites/bibite_0.bb8", BodyID: 42, HasBodyID: true}
	if err := tr.AppendArray(dstRef, element); err != nil {
		t.Fatalf("AppendArray(synapse) error = %v", err)
	}
	fresh, err := dst.Commit(filepath.Join(t.TempDir(), "synapse_graft.zip"))
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if got := jsonArrayLen(t, fresh.Entry("bibites/bibite_0.bb8").JSON, "brain.Synapses"); got != 1 {
		t.Fatalf("destination synapse count after graft = %d, want 1", got)
	}
	weight, ok, err := getJSONPath(fresh.Entry("bibites/bibite_0.bb8").JSON, "brain.Synapses[0].Weight")
	if err != nil || !ok {
		t.Fatalf("getJSONPath(grafted weight) = %v/%t/%v", weight, ok, err)
	}
	if !jsonValuesEqual(weight, 0.25) {
		t.Fatalf("grafted synapse weight = %v, want 0.25 (source value)", weight)
	}
}

// Case 2b: array append round-trip for a pellet, proving SceneCount reconciliation
// fires on the destination.
func TestTransferAppendPelletReconcilesSceneCount(t *testing.T) {
	src := NewSession(parseTransferSource(t))
	dst := NewSession(parseSyntheticArchive(t))
	tr, err := NewTransfer(src, dst)
	if err != nil {
		t.Fatalf("NewTransfer() error = %v", err)
	}

	srcRef := SQLValueRef{Table: "pellets", EntryName: PelletsEntryName, GroupIndex: 0, HasGroupIndex: true, GroupPelletIndex: 0, HasGroupPelletIndex: true}
	element, err := tr.CollectArrayElement(srcRef)
	if err != nil {
		t.Fatalf("CollectArrayElement(pellet) error = %v", err)
	}

	beforeCount, _, _ := getJSONPath(dst.Archive().Entry("scene.bb8scene").JSON, "nPellets")
	if !jsonValuesEqual(beforeCount, 1) {
		t.Fatalf("precondition: destination nPellets = %v, want 1", beforeCount)
	}

	dstRef := SQLValueRef{Table: "pellets", EntryName: PelletsEntryName, GroupIndex: 0, HasGroupIndex: true}
	if err := tr.AppendArray(dstRef, element); err != nil {
		t.Fatalf("AppendArray(pellet) error = %v", err)
	}
	fresh, err := dst.Commit(filepath.Join(t.TempDir(), "pellet_graft.zip"))
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if got := jsonArrayLen(t, fresh.Entry("pellets.bb8scene").JSON, "pellets[0].pellets"); got != 2 {
		t.Fatalf("destination pellet count after graft = %d, want 2", got)
	}
	afterCount, ok, err := getJSONPath(fresh.Entry("scene.bb8scene").JSON, "nPellets")
	if err != nil || !ok {
		t.Fatalf("getJSONPath(nPellets) = %v/%t/%v", afterCount, ok, err)
	}
	if !jsonValuesEqual(afterCount, 2) {
		t.Fatalf("destination nPellets after pellet graft = %v, want 2 (SceneCount reconciliation)", afterCount)
	}
}

// Case 2c: append-array table mismatch fails loudly.
func TestTransferAppendArrayTableMismatch(t *testing.T) {
	tr, _, _ := newTransferPair(t)
	element := CollectedElement{SourcePath: "x", Table: "bibite_brain_synapses", JSON: map[string]any{"x": 1}}
	dstRef := SQLValueRef{Table: "pellets", EntryName: PelletsEntryName, GroupIndex: 0, HasGroupIndex: true}
	if err := tr.AppendArray(dstRef, element); err == nil {
		t.Fatalf("AppendArray(table mismatch) error = nil, want failure")
	}
}

// deleteSpeciesID removes genes.speciesID from a parsed entry's JSON so the entry
// is species-less. The whole-entry graft mechanics tests use a species-less
// source: under the F1 fail-loud rule any genes.speciesID would refuse the graft
// (that refusal is exercised separately), so removing it isolates the mechanics
// (fresh name, scene count, body.id) from the species check.
func deleteSpeciesID(t *testing.T, root any) {
	t.Helper()
	obj, ok := root.(map[string]any)
	if !ok {
		t.Fatalf("deleteSpeciesID: root is %T, want map", root)
	}
	genes, ok := obj["genes"].(map[string]any)
	if !ok {
		t.Fatalf("deleteSpeciesID: genes is %T, want map", obj["genes"])
	}
	delete(genes, "speciesID")
	if _, present := entitySpeciesID(root); present {
		t.Fatalf("deleteSpeciesID: speciesID still present after delete")
	}
}

// Case 3: whole-entry append (mechanics only). Collect a SPECIES-LESS source
// bibite, graft it, Commit; assert a fresh non-colliding name exists, nBibites
// incremented, and the grafted body.id is present. A species-less entity is the
// only non-refusing species path under the F1 fail-loud rule, so this doubles as
// the "species-less entity grafts cleanly" boundary case.
func TestTransferAppendEntryRoundTrip(t *testing.T) {
	src := NewSession(parseTransferSource(t))
	// Drop genes.speciesID so the mechanics graft is not refused by the species rule.
	deleteSpeciesID(t, src.Archive().Entry("bibites/bibite_0.bb8").JSON)
	dst := NewSession(parseSpeciesArchive(t))
	tr, err := NewTransfer(src, dst)
	if err != nil {
		t.Fatalf("NewTransfer() error = %v", err)
	}

	element, err := tr.CollectEntry("bibites/bibite_0.bb8")
	if err != nil {
		t.Fatalf("CollectEntry() error = %v", err)
	}
	if element.Table != "bibites" {
		t.Fatalf("collected entry table = %q, want bibites", element.Table)
	}

	beforeCount, _, _ := getJSONPath(dst.Archive().Entry("scene.bb8scene").JSON, "nBibites")
	if !jsonValuesEqual(beforeCount, 3) {
		t.Fatalf("precondition: destination nBibites = %v, want 3", beforeCount)
	}

	if err := tr.AppendEntry(element); err != nil {
		t.Fatalf("AppendEntry() error = %v", err)
	}
	fresh, err := dst.Commit(filepath.Join(t.TempDir(), "entry_graft.zip"))
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	// parseSpeciesArchive has bibite_0..bibite_2, so the fresh name is bibite_3.
	const wantName = "bibites/bibite_3.bb8"
	grafted := fresh.Entry(wantName)
	if grafted == nil {
		t.Fatalf("grafted entry %q missing; entries = %v", wantName, entryNames(fresh))
	}
	// Source name must NOT be reused as-is (it would collide with dest bibite_0).
	if grafted.Name == element.SourcePath {
		t.Fatalf("grafted entry reused source name %q", element.SourcePath)
	}
	id, ok := bibiteBodyID(grafted.JSON)
	if !ok || id != 500 {
		t.Fatalf("grafted body.id = %v/%t, want 500", id, ok)
	}
	afterCount, ok, err := getJSONPath(fresh.Entry("scene.bb8scene").JSON, "nBibites")
	if err != nil || !ok {
		t.Fatalf("getJSONPath(nBibites) = %v/%t/%v", afterCount, ok, err)
	}
	if !jsonValuesEqual(afterCount, 4) {
		t.Fatalf("destination nBibites after graft = %v, want 4", afterCount)
	}
}

// Case 4: entry-name allocation skips gaps and picks max+1, not a collision.
func TestNextEntryNameAllocatesMaxPlusOne(t *testing.T) {
	withBOMb := withBOM
	build := func(names ...string) *tb.Archive {
		entries := []tb.Entry{
			{Index: 0, Name: "scene.bb8scene", Kind: tb.EntryScene, Raw: withBOMb(`{"nBibites":0}`)},
		}
		for i, name := range names {
			entries = append(entries, tb.Entry{Index: i + 1, Name: name, Kind: tb.ClassifyEntry(name)})
		}
		return &tb.Archive{Entries: entries}
	}

	t.Run("gap in indices uses max+1", func(t *testing.T) {
		got, err := nextEntryName(build("bibites/bibite_0.bb8", "bibites/bibite_3.bb8"), tb.EntryBibite)
		if err != nil {
			t.Fatalf("nextEntryName() error = %v", err)
		}
		if got != "bibites/bibite_4.bb8" {
			t.Fatalf("nextEntryName(gap) = %q, want bibites/bibite_4.bb8", got)
		}
	})

	t.Run("no existing entries starts at 0", func(t *testing.T) {
		got, err := nextEntryName(build(), tb.EntryBibite)
		if err != nil {
			t.Fatalf("nextEntryName() error = %v", err)
		}
		if got != "bibites/bibite_0.bb8" {
			t.Fatalf("nextEntryName(empty) = %q, want bibites/bibite_0.bb8", got)
		}
	})

	t.Run("eggs use the egg pattern", func(t *testing.T) {
		got, err := nextEntryName(build("eggs/egg_0.bb8", "eggs/egg_1.bb8"), tb.EntryEgg)
		if err != nil {
			t.Fatalf("nextEntryName() error = %v", err)
		}
		if got != "eggs/egg_2.bb8" {
			t.Fatalf("nextEntryName(eggs) = %q, want eggs/egg_2.bb8", got)
		}
	})
}

// Case 5: identity failures are loud and leave the destination unchanged.
func TestTransferAppendEntryBodyIDCollision(t *testing.T) {
	// A source bibite whose body.id (42) collides with the destination's bibite_0.
	src := NewSession(parseTransferSource(t))
	colliding := src.Archive().Entry("bibites/bibite_0.bb8")
	if colliding == nil {
		t.Fatalf("source bibite missing")
	}
	if err := setJSONPath(colliding.JSON, "body.id", int64(42), SetOptions{}); err != nil {
		t.Fatalf("setJSONPath(body.id) error = %v", err)
	}
	dst := NewSession(parseSpeciesArchive(t))
	tr, err := NewTransfer(src, dst)
	if err != nil {
		t.Fatalf("NewTransfer() error = %v", err)
	}

	element, err := tr.CollectEntry("bibites/bibite_0.bb8")
	if err != nil {
		t.Fatalf("CollectEntry() error = %v", err)
	}
	if err := tr.AppendEntry(element); err == nil {
		t.Fatalf("AppendEntry(body.id collision) error = nil, want loud failure")
	}
	// Destination must be unchanged: no staged op, no extra entry.
	if got := len(dst.StagedOperations()); got != 0 {
		t.Fatalf("destination has %d staged ops after rejected graft, want 0", got)
	}
	if dst.State() != StateClean {
		t.Fatalf("destination state = %q after rejected graft, want clean", dst.State())
	}
}

// Case 5b: an entry whose name classifies to the wrong kind is rejected at apply.
func TestTransferAppendEntryWrongKindName(t *testing.T) {
	dst := NewSession(parseSpeciesArchive(t))
	// Stage an append whose name is an egg name but kind is bibite: applyAppendEntry
	// re-validates name classification and must reject it at commit time.
	payload := EntryPayload{
		Name: "eggs/egg_9.bb8",
		Kind: tb.EntryBibite,
		JSON: map[string]any{"body": map[string]any{"id": 777}, "genes": map[string]any{"speciesID": 9}},
	}
	if err := dst.StageAppendBibite(payload); err != nil {
		t.Fatalf("StageAppendBibite() error = %v", err)
	}
	if err := dst.Apply(); err == nil {
		t.Fatalf("Apply(wrong-kind name) error = nil, want classification rejection")
	}
}

// assertDestUnstaged asserts the destination took no staged op, stayed clean, and
// kept its activeSpeciesList unchanged at [1,2,3] (the parseSpeciesArchive value).
func assertDestUnstaged(t *testing.T, dst *Session) {
	t.Helper()
	if got := len(dst.StagedOperations()); got != 0 {
		t.Fatalf("destination has %d staged ops after refused graft, want 0", got)
	}
	if dst.State() != StateClean {
		t.Fatalf("destination state = %q after refused graft, want clean", dst.State())
	}
	// The dest activeSpeciesList must be untouched by a refused graft.
	wantActiveSpecies(t, activeSpeciesIDs(t, dst.Archive()), 1, 2, 3)
}

// Case 6: species handling is FAIL-LOUD. Every species-bearing graft is refused —
// both an absent id (would need a record import) and a present/colliding id (would
// conflate distinct per-world-linear species). These refusals are CONSERVATIVE ON
// PURPOSE; ticket F3 lifts them via a cross-world species remap (allocate a fresh
// dest species id + import the source record + rewrite refs). Do NOT "fix" any of
// these refusals back into an add-to-activeSpeciesList: that is the silent
// conflation bug the original F1 shipped.
func TestTransferAppendEntryRefusesSpecies(t *testing.T) {
	t.Run("absent species id refuses loudly and leaves dest unstaged", func(t *testing.T) {
		tr, _, dst := newTransferPair(t)
		// source bibite_0 carries species 7, absent from the destination [1,2,3].
		element, err := tr.CollectEntry("bibites/bibite_0.bb8")
		if err != nil {
			t.Fatalf("CollectEntry() error = %v", err)
		}
		err = tr.AppendEntry(element)
		if err == nil {
			t.Fatalf("AppendEntry(absent species) error = nil, want loud refusal")
		}
		if !strings.Contains(err.Error(), "7") || !strings.Contains(err.Error(), "absent") {
			t.Fatalf("AppendEntry error = %q, want it to name species 7 and the absent/import reason", err)
		}
		assertDestUnstaged(t, dst)
	})

	t.Run("present colliding linear id refuses, NOT conflates (headline case)", func(t *testing.T) {
		src := NewSession(parseTransferSource(t))
		// Retag source bibite_0 (body 500) to species 2. In the destination, species
		// 2 is the unrelated pair bibite_1/bibite_2 (bodies 43/44). speciesID is a
		// per-world linear id, so this id collision does NOT mean the grafted body
		// 500 is the same species — F1 must REFUSE rather than adopt dest species 2.
		b := src.Archive().Entry("bibites/bibite_0.bb8")
		if err := setJSONPath(b.JSON, "genes.speciesID", int64(2), SetOptions{}); err != nil {
			t.Fatalf("setJSONPath(speciesID) error = %v", err)
		}
		dst := NewSession(parseSpeciesArchive(t))
		tr, err := NewTransfer(src, dst)
		if err != nil {
			t.Fatalf("NewTransfer() error = %v", err)
		}
		element, err := tr.CollectEntry("bibites/bibite_0.bb8")
		if err != nil {
			t.Fatalf("CollectEntry() error = %v", err)
		}
		err = tr.AppendEntry(element)
		if err == nil {
			t.Fatalf("AppendEntry(colliding species id) error = nil, want loud refusal")
		}
		if !strings.Contains(err.Error(), "2") || !strings.Contains(err.Error(), "conflate") {
			t.Fatalf("AppendEntry error = %q, want it to name species 2 and the conflation reason", err)
		}
		// The grafted bibite must NOT have been appended.
		if dst.Archive().Entry("bibites/bibite_3.bb8") != nil {
			t.Fatalf("refused graft still appended bibites/bibite_3.bb8")
		}
		assertDestUnstaged(t, dst)
	})

	t.Run("egg with absent species refuses loudly and leaves dest unstaged", func(t *testing.T) {
		tr, _, dst := newTransferPair(t)
		// source egg_0 carries species 8, absent from the destination [1,2,3].
		element, err := tr.CollectEntry("eggs/egg_0.bb8")
		if err != nil {
			t.Fatalf("CollectEntry(egg) error = %v", err)
		}
		if element.Table != "eggs" {
			t.Fatalf("collected egg table = %q, want eggs", element.Table)
		}
		err = tr.AppendEntry(element)
		if err == nil {
			t.Fatalf("AppendEntry(egg absent species) error = nil, want loud refusal")
		}
		if !strings.Contains(err.Error(), "8") {
			t.Fatalf("AppendEntry(egg) error = %q, want it to name species 8", err)
		}
		// No fresh egg entry should have been appended.
		if dst.Archive().Entry("eggs/egg_1.bb8") != nil {
			t.Fatalf("refused egg graft still appended eggs/egg_1.bb8")
		}
		assertDestUnstaged(t, dst)
	})

	t.Run("species-less bibite grafts cleanly (only non-refusing path)", func(t *testing.T) {
		src := NewSession(parseTransferSource(t))
		deleteSpeciesID(t, src.Archive().Entry("bibites/bibite_0.bb8").JSON)
		dst := NewSession(parseSpeciesArchive(t))
		tr, err := NewTransfer(src, dst)
		if err != nil {
			t.Fatalf("NewTransfer() error = %v", err)
		}
		element, err := tr.CollectEntry("bibites/bibite_0.bb8")
		if err != nil {
			t.Fatalf("CollectEntry() error = %v", err)
		}
		if err := tr.AppendEntry(element); err != nil {
			t.Fatalf("AppendEntry(species-less) error = %v, want clean graft", err)
		}
		fresh, err := dst.Commit(filepath.Join(t.TempDir(), "speciesless_graft.zip"))
		if err != nil {
			t.Fatalf("Commit() error = %v", err)
		}
		if fresh.Entry("bibites/bibite_3.bb8") == nil {
			t.Fatalf("species-less graft missing bibites/bibite_3.bb8; entries = %v", entryNames(fresh))
		}
		// The species table is untouched by a species-less graft.
		wantActiveSpecies(t, activeSpeciesIDs(t, fresh), 1, 2, 3)
	})
}

// Case: dangling parent/child link is rejected loudly.
func TestTransferAppendEntryDanglingChildRef(t *testing.T) {
	src := NewSession(parseTransferSource(t))
	b := src.Archive().Entry("bibites/bibite_0.bb8")
	// Give the source bibite a child id (1234) that does not exist in the
	// destination; the graft must refuse rather than create a dangling link.
	if err := setJSONPath(b.JSON, "body.eggLayer", map[string]any{"children": []any{int64(1234)}}, SetOptions{CreateMissing: true}); err != nil {
		t.Fatalf("setup child ref error = %v", err)
	}
	dst := NewSession(parseSpeciesArchive(t))
	tr, err := NewTransfer(src, dst)
	if err != nil {
		t.Fatalf("NewTransfer() error = %v", err)
	}
	element, err := tr.CollectEntry("bibites/bibite_0.bb8")
	if err != nil {
		t.Fatalf("CollectEntry() error = %v", err)
	}
	if err := tr.AppendEntry(element); err == nil {
		t.Fatalf("AppendEntry(dangling child) error = nil, want loud failure")
	}
	if len(dst.StagedOperations()) != 0 {
		t.Fatalf("destination staged ops after rejected dangling graft = %d, want 0", len(dst.StagedOperations()))
	}
}

// Case: missing source entry / wrong kind collection fails loudly.
func TestTransferCollectFailLoud(t *testing.T) {
	tr, _, _ := newTransferPair(t)
	if _, err := tr.CollectEntry("bibites/bibite_404.bb8"); err == nil {
		t.Fatalf("CollectEntry(missing) error = nil, want failure")
	}
	if _, err := tr.CollectEntry("scene.bb8scene"); err == nil {
		t.Fatalf("CollectEntry(non-bibite) error = nil, want failure")
	}
	if err := tr.AppendEntry(CollectedElement{Table: "scene", JSON: map[string]any{}}); err == nil {
		t.Fatalf("AppendEntry(bad table) error = nil, want failure")
	}
}

func entryNames(archive *tb.Archive) []string {
	out := make([]string, 0, len(archive.Entries))
	for i := range archive.Entries {
		out = append(out, archive.Entries[i].Name)
	}
	return out
}
