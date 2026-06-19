package thebibites

import (
	"archive/zip"
	"encoding/json"
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
			{Index: 2, Name: "speciesData.json", Kind: tb.EntrySpecies, Method: zip.Deflate, Raw: withBOM(`{"nextSpeciesID":9,"activeSpeciesList":[7,8],"recordedSpecies":[{"speciesID":7,"parentID":5,"name":"src-seven","template":{"genes":{"SizeRatio":7.7}}},{"speciesID":8,"parentID":6,"name":"src-eight","template":{"genes":{"SizeRatio":8.8}}}]}`)},
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
// source so the species remap is skipped entirely, isolating the mechanics (fresh
// name, scene count, body.id) from the species path (which is exercised by the F3
// remap tests).
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
// incremented, and the grafted body.id is present. A species-less entity skips
// the remap entirely, so this isolates the graft mechanics from the species path.
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

	if err := tr.AppendEntry(element, GraftOptions{}); err != nil {
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
	if err := tr.AppendEntry(element, GraftOptions{}); err == nil {
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

// speciesSizeRatio reads template.genes.SizeRatio from the recordedSpecies record
// whose speciesID == sid, the recognizable per-species marker the fixtures use.
// ok is false when no record carries sid.
func speciesSizeRatio(t *testing.T, archive *tb.Archive, sid int64) (float64, bool) {
	t.Helper()
	records, ok, err := getJSONPath(archive.Entry("speciesData.json").JSON, "recordedSpecies")
	if err != nil || !ok {
		t.Fatalf("getJSONPath(recordedSpecies) = %v/%t/%v", records, ok, err)
	}
	list, ok := records.([]any)
	if !ok {
		t.Fatalf("recordedSpecies = %T, want array", records)
	}
	for _, rec := range list {
		id, found := jsonInt64Path(rec, "speciesID")
		if !found || id != sid {
			continue
		}
		ratio, ok, err := getJSONPath(rec, "template.genes.SizeRatio")
		if err != nil || !ok {
			t.Fatalf("getJSONPath(record %d template.genes.SizeRatio) = %v/%t/%v", sid, ratio, ok, err)
		}
		n, ok := jsonNumberToFloat64(ratio)
		if !ok {
			t.Fatalf("record %d SizeRatio = %v, not a number", sid, ratio)
		}
		return n, true
	}
	return 0, false
}

func jsonNumberToFloat64(value any) (float64, bool) {
	switch v := value.(type) {
	case json.Number:
		if f, err := v.Float64(); err == nil {
			return f, true
		}
	case float64:
		return v, true
	}
	return 0, false
}

// recordParentID reads parentID off the recordedSpecies record with speciesID sid.
func recordParentID(t *testing.T, archive *tb.Archive, sid int64) int64 {
	t.Helper()
	records, ok, err := getJSONPath(archive.Entry("speciesData.json").JSON, "recordedSpecies")
	if err != nil || !ok {
		t.Fatalf("getJSONPath(recordedSpecies) = %v/%t/%v", records, ok, err)
	}
	list, _ := records.([]any)
	for _, rec := range list {
		if id, found := jsonInt64Path(rec, "speciesID"); found && id == sid {
			parent, ok := jsonInt64Path(rec, "parentID")
			if !ok {
				t.Fatalf("record %d has no parentID", sid)
			}
			return parent
		}
	}
	t.Fatalf("no record for species %d", sid)
	return 0
}

// entitySpeciesIDOf reads genes.speciesID off a committed entry, failing the test
// if the entry or the id is missing.
func entitySpeciesIDOf(t *testing.T, archive *tb.Archive, entryName string) int64 {
	t.Helper()
	entry := archive.Entry(entryName)
	if entry == nil {
		t.Fatalf("entry %q missing; entries = %v", entryName, entryNames(archive))
	}
	id, ok := entitySpeciesID(entry.JSON)
	if !ok {
		t.Fatalf("entry %q has no genes.speciesID", entryName)
	}
	return id
}

// Case 6 (F3 headline): a colliding per-world-linear species id REMAPS rather than
// conflating. This is the exact scenario F1 refused: source body 500 retagged to
// species 2, where dest species 2 is the unrelated pair bibite_1/bibite_2.
// speciesID is per-world LINEAR, so an id collision does NOT mean the grafted body
// is the same species. F3 must SUCCEED with a fresh non-colliding dest id, import
// the SOURCE species record (NOT adopt dest species 2), leave the dest's own
// species 2 byte-for-byte intact, and not reassign the dest members.
func TestTransferAppendEntryRemapsCollidingSpecies(t *testing.T) {
	src := NewSession(parseTransferSource(t))
	// Retag source bibite_0 (body 500) to species 2 and give the source a matching
	// record so the colliding-linear-id case has a record to import.
	b := src.Archive().Entry("bibites/bibite_0.bb8")
	if err := setJSONPath(b.JSON, "genes.speciesID", int64(2), SetOptions{}); err != nil {
		t.Fatalf("setJSONPath(speciesID) error = %v", err)
	}
	srcSpecies := src.Archive().Entry("speciesData.json")
	if err := appendJSONArray(srcSpecies.JSON, "recordedSpecies", map[string]any{
		"speciesID": int64(2), "parentID": int64(1), "name": "src-two", "template": map[string]any{"genes": map[string]any{"SizeRatio": 22.0}},
	}); err != nil {
		t.Fatalf("append source record error = %v", err)
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
	if err := tr.AppendEntry(element, GraftOptions{}); err != nil {
		t.Fatalf("AppendEntry(colliding species) error = %v, want successful remap", err)
	}
	fresh, err := dst.Commit(filepath.Join(t.TempDir(), "remap_colliding.zip"))
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	graftedID := entitySpeciesIDOf(t, fresh, "bibites/bibite_3.bb8")
	if graftedID == 2 {
		t.Fatalf("grafted genes.speciesID = 2 (conflated into dest species 2), want a fresh id")
	}
	for _, used := range []int64{1, 2, 3} {
		if graftedID == used {
			t.Fatalf("grafted genes.speciesID = %d, collides with pre-graft dest id", graftedID)
		}
	}
	// activeSpeciesList gained exactly the fresh id.
	wantActiveSpecies(t, activeSpeciesIDs(t, fresh), 1, 2, 3, graftedID)

	// The imported record exists under the fresh id with the SOURCE species-2
	// marker (22.0), proving the source record was imported, not dest species 2.
	ratio, ok := speciesSizeRatio(t, fresh, graftedID)
	if !ok {
		t.Fatalf("no recordedSpecies record for fresh id %d", graftedID)
	}
	if ratio != 22.0 {
		t.Fatalf("imported record SizeRatio = %v, want 22 (source species 2 marker)", ratio)
	}
	// The imported record's parentID is reset to self (lineage root), not the
	// source parentID (1) which has no dest counterpart.
	if parent := recordParentID(t, fresh, graftedID); parent != graftedID {
		t.Fatalf("imported record parentID = %d, want %d (self lineage root)", parent, graftedID)
	}

	// The dest's ORIGINAL species 2 record is untouched (marker 2.2).
	destRatio, ok := speciesSizeRatio(t, fresh, 2)
	if !ok {
		t.Fatalf("dest species 2 record vanished after remap")
	}
	if destRatio != 2.2 {
		t.Fatalf("dest species 2 record SizeRatio = %v, want 2.2 (unchanged)", destRatio)
	}
	// The dest members of species 2 still carry species 2 (no conflation).
	if got := entitySpeciesIDOf(t, fresh, "bibites/bibite_1.bb8"); got != 2 {
		t.Fatalf("dest bibite_1 genes.speciesID = %d, want 2 (unchanged)", got)
	}
	if got := entitySpeciesIDOf(t, fresh, "bibites/bibite_2.bb8"); got != 2 {
		t.Fatalf("dest bibite_2 genes.speciesID = %d, want 2 (unchanged)", got)
	}
}

// Case 7 (F3): an ABSENT species id remaps. Source bibite_0 carries species 7,
// absent from dest [1,2,3]. The graft succeeds with a fresh id, the source-7
// record is imported, and dest records 1/2/3 are untouched.
func TestTransferAppendEntryRemapsAbsentSpecies(t *testing.T) {
	tr, _, dst := newTransferPair(t)
	// source bibite_0 carries species 7, absent from the destination [1,2,3].
	element, err := tr.CollectEntry("bibites/bibite_0.bb8")
	if err != nil {
		t.Fatalf("CollectEntry() error = %v", err)
	}
	if err := tr.AppendEntry(element, GraftOptions{}); err != nil {
		t.Fatalf("AppendEntry(absent species) error = %v, want successful remap", err)
	}
	fresh, err := dst.Commit(filepath.Join(t.TempDir(), "remap_absent.zip"))
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	graftedID := entitySpeciesIDOf(t, fresh, "bibites/bibite_3.bb8")
	for _, used := range []int64{1, 2, 3} {
		if graftedID == used {
			t.Fatalf("grafted genes.speciesID = %d, collides with pre-graft dest id", graftedID)
		}
	}
	wantActiveSpecies(t, activeSpeciesIDs(t, fresh), 1, 2, 3, graftedID)

	ratio, ok := speciesSizeRatio(t, fresh, graftedID)
	if !ok {
		t.Fatalf("no recordedSpecies record for fresh id %d", graftedID)
	}
	if ratio != 7.7 {
		t.Fatalf("imported record SizeRatio = %v, want 7.7 (source species 7 marker)", ratio)
	}
	// dest records 1/2/3 untouched.
	for sid, want := range map[int64]float64{1: 1.1, 2: 2.2, 3: 3.3} {
		got, ok := speciesSizeRatio(t, fresh, sid)
		if !ok || got != want {
			t.Fatalf("dest species %d record SizeRatio = %v/%t, want %v", sid, got, ok, want)
		}
	}
}

// Case 8 (F3): the egg path rewrites genes.speciesID on the egg, not only on
// bibites. Graft source egg_0 (species 8); assert the appended egg's species id is
// the fresh id and the source-8 record was imported.
func TestTransferAppendEntryRemapsEggSpecies(t *testing.T) {
	tr, _, dst := newTransferPair(t)
	element, err := tr.CollectEntry("eggs/egg_0.bb8")
	if err != nil {
		t.Fatalf("CollectEntry(egg) error = %v", err)
	}
	if element.Table != "eggs" {
		t.Fatalf("collected egg table = %q, want eggs", element.Table)
	}
	if err := tr.AppendEntry(element, GraftOptions{}); err != nil {
		t.Fatalf("AppendEntry(egg) error = %v, want successful remap", err)
	}
	fresh, err := dst.Commit(filepath.Join(t.TempDir(), "remap_egg.zip"))
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	// parseSpeciesArchive has egg_0, so the fresh egg name is egg_1.
	graftedID := entitySpeciesIDOf(t, fresh, "eggs/egg_1.bb8")
	if graftedID == 8 {
		t.Fatalf("grafted egg genes.speciesID = 8 (source id reused), want a fresh dest id")
	}
	for _, used := range []int64{1, 2, 3} {
		if graftedID == used {
			t.Fatalf("grafted egg genes.speciesID = %d, collides with pre-graft dest id", graftedID)
		}
	}
	wantActiveSpecies(t, activeSpeciesIDs(t, fresh), 1, 2, 3, graftedID)
	ratio, ok := speciesSizeRatio(t, fresh, graftedID)
	if !ok || ratio != 8.8 {
		t.Fatalf("imported record SizeRatio = %v/%t, want 8.8 (source species 8 marker)", ratio, ok)
	}
}

// Case 9 (F3 invariant): the allocator beats a STALE nextSpeciesID. Build a dest
// whose nextSpeciesID (1) is lower than an in-use id (activeSpeciesList has 3 and
// a record-only id 50); the fresh id must beat the real max (50), never collide.
// This locks in the max(...) guard the original conflation bug depended on.
func TestTransferAppendEntryAllocatorBeatsStaleNextSpeciesID(t *testing.T) {
	src := NewSession(parseTransferSource(t))
	dst := NewSession(parseSpeciesArchive(t))
	// Stale counter behind the in-use ids, plus a record-only id (50) that is NOT
	// in activeSpeciesList — the allocator must still beat it.
	species := dst.Archive().Entry("speciesData.json")
	if err := setJSONPath(species.JSON, "nextSpeciesID", int64(1), SetOptions{}); err != nil {
		t.Fatalf("setJSONPath(nextSpeciesID) error = %v", err)
	}
	if err := appendJSONArray(species.JSON, "recordedSpecies", map[string]any{
		"speciesID": int64(50), "parentID": int64(0), "name": "record-only", "template": map[string]any{"genes": map[string]any{"SizeRatio": 50.0}},
	}); err != nil {
		t.Fatalf("append record-only species error = %v", err)
	}

	tr, err := NewTransfer(src, dst)
	if err != nil {
		t.Fatalf("NewTransfer() error = %v", err)
	}
	element, err := tr.CollectEntry("bibites/bibite_0.bb8") // species 7
	if err != nil {
		t.Fatalf("CollectEntry() error = %v", err)
	}
	if err := tr.AppendEntry(element, GraftOptions{}); err != nil {
		t.Fatalf("AppendEntry() error = %v, want successful remap", err)
	}
	fresh, err := dst.Commit(filepath.Join(t.TempDir(), "remap_stale.zip"))
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	graftedID := entitySpeciesIDOf(t, fresh, "bibites/bibite_3.bb8")
	if graftedID <= 50 {
		t.Fatalf("grafted genes.speciesID = %d, want > 50 (the real max in-use id); stale nextSpeciesID was not beaten", graftedID)
	}
}

// Case 9b (F3 conflation invariant, load-bearing): the allocator MUST consult
// every live dest entity's genes.speciesID. This pins the one id source that can
// hold an id present in NEITHER activeSpeciesList NOR recordedSpecies — the exact
// silent-conflation shape of the original F1 bug. A live dest bibite is retagged
// to species 60 while the species TABLE tops out at 50 (a record-only id) and
// activeSpeciesList stays [1,2,3] (60 appears in no species-table field). The
// fresh id must beat 60, which is only possible if dstSpeciesIDUsage scans live
// entities; this test fails if that traversal is removed.
func TestTransferAppendEntryAllocatorBeatsLiveEntitySpeciesID(t *testing.T) {
	src := NewSession(parseTransferSource(t))
	dst := NewSession(parseSpeciesArchive(t))
	species := dst.Archive().Entry("speciesData.json")
	// Species table tops out at 50, and 50 lives only in recordedSpecies (not in
	// activeSpeciesList). nextSpeciesID is stale so it cannot mask the gap.
	if err := setJSONPath(species.JSON, "nextSpeciesID", int64(1), SetOptions{}); err != nil {
		t.Fatalf("setJSONPath(nextSpeciesID) error = %v", err)
	}
	if err := appendJSONArray(species.JSON, "recordedSpecies", map[string]any{
		"speciesID": int64(50), "parentID": int64(0), "name": "record-only", "template": map[string]any{"genes": map[string]any{"SizeRatio": 50.0}},
	}); err != nil {
		t.Fatalf("append record-only species error = %v", err)
	}
	// A LIVE dest bibite carries species 60 — present in NO species-table field
	// (not activeSpeciesList, not recordedSpecies). Only the live-entity scan can
	// surface it as the global max.
	if err := setJSONPath(dst.Archive().Entry("bibites/bibite_0.bb8").JSON, "genes.speciesID", int64(60), SetOptions{}); err != nil {
		t.Fatalf("setJSONPath(live entity speciesID) error = %v", err)
	}

	tr, err := NewTransfer(src, dst)
	if err != nil {
		t.Fatalf("NewTransfer() error = %v", err)
	}
	element, err := tr.CollectEntry("bibites/bibite_0.bb8") // source species 7
	if err != nil {
		t.Fatalf("CollectEntry() error = %v", err)
	}
	if err := tr.AppendEntry(element, GraftOptions{}); err != nil {
		t.Fatalf("AppendEntry() error = %v, want successful remap", err)
	}
	fresh, err := dst.Commit(filepath.Join(t.TempDir(), "remap_live_entity.zip"))
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	graftedID := entitySpeciesIDOf(t, fresh, "bibites/bibite_3.bb8")
	if graftedID <= 60 {
		t.Fatalf("grafted genes.speciesID = %d, want > 60 (the live-entity max id); the live-entity traversal in dstSpeciesIDUsage was not consulted — silent-conflation seam", graftedID)
	}
}

// Case 10 (F3): a species-bearing graft whose source has NO matching record fails
// loudly naming the id and leaves the dest with 0 staged ops.
func TestTransferAppendEntrySourceRecordAbsentLoud(t *testing.T) {
	src := NewSession(parseTransferSource(t))
	// Empty the source records so species 7 has no backing record to import.
	srcSpecies := src.Archive().Entry("speciesData.json")
	if err := setJSONPath(srcSpecies.JSON, "recordedSpecies", []any{}, SetOptions{}); err != nil {
		t.Fatalf("setJSONPath(recordedSpecies) error = %v", err)
	}
	dst := NewSession(parseSpeciesArchive(t))
	tr, err := NewTransfer(src, dst)
	if err != nil {
		t.Fatalf("NewTransfer() error = %v", err)
	}
	element, err := tr.CollectEntry("bibites/bibite_0.bb8") // species 7
	if err != nil {
		t.Fatalf("CollectEntry() error = %v", err)
	}
	err = tr.AppendEntry(element, GraftOptions{})
	if err == nil {
		t.Fatalf("AppendEntry(no source record) error = nil, want loud failure")
	}
	if !strings.Contains(err.Error(), "7") {
		t.Fatalf("AppendEntry error = %q, want it to name species 7", err)
	}
	if dst.Archive().Entry("bibites/bibite_3.bb8") != nil {
		t.Fatalf("failed graft still appended bibites/bibite_3.bb8")
	}
	assertDestUnstaged(t, dst)
}

// Case 11 (F3): a dest with NO species table fails loudly for a species-bearing
// graft (the imported record has nowhere to land). Mirrors the F1 named refusal.
func TestTransferAppendEntryNoDestSpeciesTableLoud(t *testing.T) {
	src := NewSession(parseTransferSource(t))
	dst := NewSession(parseSpeciesArchive(t))
	// Drop the dest species table to model a save with nowhere to import into.
	species := dst.Archive().Entry("speciesData.json")
	species.JSON = nil

	tr, err := NewTransfer(src, dst)
	if err != nil {
		t.Fatalf("NewTransfer() error = %v", err)
	}
	element, err := tr.CollectEntry("bibites/bibite_0.bb8") // species 7
	if err != nil {
		t.Fatalf("CollectEntry() error = %v", err)
	}
	err = tr.AppendEntry(element, GraftOptions{})
	if err == nil {
		t.Fatalf("AppendEntry(no dest species table) error = nil, want loud failure")
	}
	if !strings.Contains(err.Error(), "species table") {
		t.Fatalf("AppendEntry error = %q, want it to name the missing species table", err)
	}
	if got := len(dst.StagedOperations()); got != 0 {
		t.Fatalf("destination has %d staged ops after refused graft, want 0", got)
	}
	if dst.Archive().Entry("bibites/bibite_3.bb8") != nil {
		t.Fatalf("refused graft still appended bibites/bibite_3.bb8")
	}
}

// Case 12 (F3): a species-less graft skips the remap entirely and grafts cleanly,
// leaving the species table untouched.
func TestTransferAppendEntrySpeciesLessGraftsCleanly(t *testing.T) {
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
	if err := tr.AppendEntry(element, GraftOptions{}); err != nil {
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
	if err := tr.AppendEntry(element, GraftOptions{}); err == nil {
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
	if err := tr.AppendEntry(CollectedElement{Table: "scene", JSON: map[string]any{}}, GraftOptions{}); err == nil {
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

// bibiteBodyIDOf reads body.id off a committed bibite entry, failing the test when
// the entry or its body.id is absent.
func bibiteBodyIDOf(t *testing.T, archive *tb.Archive, entryName string) int64 {
	t.Helper()
	entry := archive.Entry(entryName)
	if entry == nil {
		t.Fatalf("entry %q not found in committed archive; entries = %v", entryName, entryNames(archive))
	}
	id, ok := bibiteBodyID(entry.JSON)
	if !ok {
		t.Fatalf("entry %q has no body.id", entryName)
	}
	return id
}

// M6 Case A: a source bibite whose body.id collides with the destination is
// REMAPPED (not refused) when GraftOptions.RemapIDs is set. The graft succeeds and
// the staged entry's body.id is a fresh id that beats every dest body.id (so it
// cannot re-collide).
func TestTransferAppendEntryRemapsCollidingBodyID(t *testing.T) {
	// Mirror the collision setup: retag source bibite_0 to body.id 42 (== dest
	// bibite_0). The source carries no species record for its species id, so strip
	// the species to keep the body.id remap the single axis under test.
	src := NewSession(parseTransferSource(t))
	colliding := src.Archive().Entry("bibites/bibite_0.bb8")
	if colliding == nil {
		t.Fatalf("source bibite missing")
	}
	if err := setJSONPath(colliding.JSON, "body.id", int64(42), SetOptions{}); err != nil {
		t.Fatalf("setJSONPath(body.id) error = %v", err)
	}
	deleteSpeciesID(t, colliding.JSON)

	dst := NewSession(parseSpeciesArchive(t)) // dest body ids 42/43/44
	tr, err := NewTransfer(src, dst)
	if err != nil {
		t.Fatalf("NewTransfer() error = %v", err)
	}
	element, err := tr.CollectEntry("bibites/bibite_0.bb8")
	if err != nil {
		t.Fatalf("CollectEntry() error = %v", err)
	}
	if err := tr.AppendEntry(element, GraftOptions{RemapIDs: true}); err != nil {
		t.Fatalf("AppendEntry(colliding body.id, RemapIDs=true) error = %v, want successful remap", err)
	}
	fresh, err := dst.Commit(filepath.Join(t.TempDir(), "remap_body_id.zip"))
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	graftedID := bibiteBodyIDOf(t, fresh, "bibites/bibite_3.bb8")
	if graftedID == 42 {
		t.Fatalf("grafted body.id = 42 (collided), want a fresh remapped id")
	}
	// The fresh id must beat every original dest body.id (42/43/44) so it cannot
	// re-collide with the destination.
	for _, used := range []int64{42, 43, 44} {
		if graftedID == used {
			t.Fatalf("grafted body.id = %d collides with a pre-graft dest body.id", graftedID)
		}
	}
	if graftedID <= 44 {
		t.Fatalf("grafted body.id = %d, want > 44 (max dest body.id)", graftedID)
	}
	// The dest's original bibites still carry their body.ids (no clobber).
	for entry, want := range map[string]int64{
		"bibites/bibite_0.bb8": 42, "bibites/bibite_1.bb8": 43, "bibites/bibite_2.bb8": 44,
	} {
		if got := bibiteBodyIDOf(t, fresh, entry); got != want {
			t.Fatalf("dest %q body.id = %d, want %d (unchanged)", entry, got, want)
		}
	}
}

// M6 Case B: RemapIDs=false keeps today's loud body.id-collision failure with 0
// staged ops — a regression guard re-asserting the existing behavior under the new
// options struct.
func TestTransferAppendEntryRemapDisabledStillLoud(t *testing.T) {
	src := NewSession(parseTransferSource(t))
	colliding := src.Archive().Entry("bibites/bibite_0.bb8")
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
	if err := tr.AppendEntry(element, GraftOptions{RemapIDs: false}); err == nil {
		t.Fatalf("AppendEntry(body.id collision, RemapIDs=false) error = nil, want loud failure")
	}
	if got := len(dst.StagedOperations()); got != 0 {
		t.Fatalf("destination has %d staged ops after refused graft, want 0", got)
	}
	if dst.State() != StateClean {
		t.Fatalf("destination state = %q after refused graft, want clean", dst.State())
	}
}

// M6 Case C (the invariant a single-graft test misses): two source bibites BOTH
// colliding with the dest in ONE transfer loop must remap to DISTINCT fresh
// body.ids. This proves the staged-fold in dstBodyIDUsage — without it, the second
// graft re-reads the same pre-Apply max and remaps to the SAME id as the first.
func TestTransferMultiGraftRemapsDistinctBodyIDs(t *testing.T) {
	// Two source bibites whose body.ids both collide with the dest (42 and 43).
	src := NewSession(parseTwoBibiteSource(t))
	b0 := src.Archive().Entry("bibites/bibite_0.bb8")
	b1 := src.Archive().Entry("bibites/bibite_1.bb8")
	if err := setJSONPath(b0.JSON, "body.id", int64(42), SetOptions{}); err != nil {
		t.Fatalf("setJSONPath(b0 body.id) error = %v", err)
	}
	if err := setJSONPath(b1.JSON, "body.id", int64(43), SetOptions{}); err != nil {
		t.Fatalf("setJSONPath(b1 body.id) error = %v", err)
	}
	// Strip species so body.id remap is the single axis under test.
	deleteSpeciesID(t, b0.JSON)
	deleteSpeciesID(t, b1.JSON)

	dst := NewSession(parseSpeciesArchive(t)) // dest body ids 42/43/44
	tr, err := NewTransfer(src, dst)
	if err != nil {
		t.Fatalf("NewTransfer() error = %v", err)
	}
	for _, name := range []string{"bibites/bibite_0.bb8", "bibites/bibite_1.bb8"} {
		el, err := tr.CollectEntry(name)
		if err != nil {
			t.Fatalf("CollectEntry(%q) error = %v", name, err)
		}
		if err := tr.AppendEntry(el, GraftOptions{RemapIDs: true}); err != nil {
			t.Fatalf("AppendEntry(%q, RemapIDs=true) error = %v", name, err)
		}
	}
	fresh, err := dst.Commit(filepath.Join(t.TempDir(), "multi_remap_body_id.zip"))
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	id3 := bibiteBodyIDOf(t, fresh, "bibites/bibite_3.bb8")
	id4 := bibiteBodyIDOf(t, fresh, "bibites/bibite_4.bb8")
	if id3 == id4 {
		t.Fatalf("both grafts remapped to the SAME body.id %d, want distinct (staged-fold missing)", id3)
	}
	for _, id := range []int64{id3, id4} {
		for _, used := range []int64{42, 43, 44} {
			if id == used {
				t.Fatalf("grafted body.id %d collides with a pre-graft dest body.id", id)
			}
		}
	}
}

// M6 Case D: the dangling-child guard stays LOUD even with RemapIDs=true. Remapping
// body.id does not fix a cross-world body.eggLayer.children reference, so a dangling
// child is still a hard failure (remap_ids must NOT loosen it).
func TestTransferAppendEntryRemapKeepsDanglingChildLoud(t *testing.T) {
	src := NewSession(parseTransferSource(t))
	b := src.Archive().Entry("bibites/bibite_0.bb8")
	// Collide the body.id (would be remapped) AND add a dangling child ref (must
	// still fail) so the test proves remap does not paper over the dangling guard.
	if err := setJSONPath(b.JSON, "body.id", int64(42), SetOptions{}); err != nil {
		t.Fatalf("setJSONPath(body.id) error = %v", err)
	}
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
	if err := tr.AppendEntry(element, GraftOptions{RemapIDs: true}); err == nil {
		t.Fatalf("AppendEntry(dangling child, RemapIDs=true) error = nil, want loud failure")
	}
	if got := len(dst.StagedOperations()); got != 0 {
		t.Fatalf("destination staged ops after rejected dangling graft = %d, want 0", got)
	}
}

// parseTwoBibiteSource builds a source with two bibites (body 500/501, species
// 7/8), both non-colliding with parseSpeciesArchive's ids, so two same-kind grafts
// into one dst session can be exercised.
func parseTwoBibiteSource(t *testing.T) *tb.Archive {
	t.Helper()
	sourcePath := filepath.Join(t.TempDir(), "two_bibite_source.zip")
	archive := &tb.Archive{
		Entries: []tb.Entry{
			{Index: 0, Name: "scene.bb8scene", Kind: tb.EntryScene, Method: zip.Deflate, Raw: withBOM(`{"nBibites":2}`)},
			{Index: 1, Name: "speciesData.json", Kind: tb.EntrySpecies, Method: zip.Deflate, Raw: withBOM(`{"nextSpeciesID":9,"activeSpeciesList":[7,8],"recordedSpecies":[{"speciesID":7,"parentID":0,"name":"src-seven","template":{"genes":{"SizeRatio":7.7}}},{"speciesID":8,"parentID":0,"name":"src-eight","template":{"genes":{"SizeRatio":8.8}}}]}`)},
			{Index: 2, Name: "bibites/bibite_0.bb8", Kind: tb.EntryBibite, Method: zip.Deflate, Raw: withBOM(`{"body":{"id":500},"genes":{"speciesID":7,"gen":1},"brain":{"Nodes":[],"Synapses":[]}}`)},
			{Index: 3, Name: "bibites/bibite_1.bb8", Kind: tb.EntryBibite, Method: zip.Deflate, Raw: withBOM(`{"body":{"id":501},"genes":{"speciesID":8,"gen":1},"brain":{"Nodes":[],"Synapses":[]}}`)},
		},
	}
	if err := tb.WriteArchive(sourcePath, archive); err != nil {
		t.Fatalf("WriteArchive(two-bibite source) error = %v", err)
	}
	parsed, err := tb.ParseFile(sourcePath, nil)
	if err != nil {
		t.Fatalf("ParseFile(two-bibite source) error = %v", err)
	}
	return parsed
}

// Multi-graft (F2 cross-world surface): grafting two same-kind entries into ONE
// dst session must allocate DISTINCT entry names. Staged appends are invisible to
// dst.Archive().Entries until Apply, so the name allocator must also consider the
// names handed to earlier grafts — otherwise both would reuse bibite_3 and Apply
// would reject "already staged for append".
func TestTransferAppendEntryMultiGraftDistinctNames(t *testing.T) {
	src := NewSession(parseTwoBibiteSource(t))
	dst := NewSession(parseSpeciesArchive(t)) // has bibite_0..2, egg_0
	tr, err := NewTransfer(src, dst)
	if err != nil {
		t.Fatalf("NewTransfer() error = %v", err)
	}
	for _, name := range []string{"bibites/bibite_0.bb8", "bibites/bibite_1.bb8"} {
		el, err := tr.CollectEntry(name)
		if err != nil {
			t.Fatalf("CollectEntry(%q) error = %v", name, err)
		}
		if err := tr.AppendEntry(el, GraftOptions{}); err != nil {
			t.Fatalf("AppendEntry(%q) error = %v", name, err)
		}
	}
	fresh, err := dst.Commit(filepath.Join(t.TempDir(), "multi_graft.zip"))
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	// Both grafts landed under distinct, fresh names (bibite_3 and bibite_4).
	if fresh.Entry("bibites/bibite_3.bb8") == nil {
		t.Fatalf("first graft missing: no bibites/bibite_3.bb8 in committed archive")
	}
	if fresh.Entry("bibites/bibite_4.bb8") == nil {
		t.Fatalf("second graft missing: no bibites/bibite_4.bb8 in committed archive")
	}
	// Their remapped species ids are distinct fresh dest ids (no conflation).
	id3 := entitySpeciesIDOf(t, fresh, "bibites/bibite_3.bb8")
	id4 := entitySpeciesIDOf(t, fresh, "bibites/bibite_4.bb8")
	if id3 == id4 {
		t.Fatalf("both grafts got the same fresh species id %d, want distinct", id3)
	}
	for _, id := range []int64{id3, id4} {
		for _, used := range []int64{1, 2, 3} {
			if id == used {
				t.Fatalf("grafted species id %d collides with a pre-graft dest id", id)
			}
		}
	}
}
