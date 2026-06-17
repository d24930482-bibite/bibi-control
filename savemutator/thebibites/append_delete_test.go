package thebibites

import (
	"archive/zip"
	"path/filepath"
	"testing"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

func jsonArrayLen(t *testing.T, root any, path string) int {
	t.Helper()
	value, ok, err := getJSONPath(root, path)
	if err != nil {
		t.Fatalf("getJSONPath(%q) error = %v", path, err)
	}
	if !ok {
		t.Fatalf("getJSONPath(%q) missing", path)
	}
	array, ok := value.([]any)
	if !ok {
		t.Fatalf("getJSONPath(%q) = %T, want array", path, value)
	}
	return len(array)
}

func TestStageAppendAndDeleteSynapse(t *testing.T) {
	archive := parseSyntheticArchive(t)
	session := NewSession(archive)
	target := BibiteTarget(BibiteRef{EntryName: "bibites/bibite_0.bb8", BodyID: 42})

	synapse := map[string]any{"Inov": 100, "NodeIn": 0, "NodeOut": 1, "Weight": 0.5, "En": true}
	if err := session.StageAppend(target, "brain.Synapses", synapse); err != nil {
		t.Fatalf("StageAppend() error = %v", err)
	}
	fresh, err := session.Commit(filepath.Join(t.TempDir(), "appended.zip"))
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if got := jsonArrayLen(t, fresh.Entry("bibites/bibite_0.bb8").JSON, "brain.Synapses"); got != 1 {
		t.Fatalf("synapse count after append = %d, want 1", got)
	}
	weight, ok, err := getJSONPath(fresh.Entry("bibites/bibite_0.bb8").JSON, "brain.Synapses[0].Weight")
	if err != nil || !ok {
		t.Fatalf("getJSONPath(weight) = %v/%t/%v", weight, ok, err)
	}
	if !jsonValuesEqual(weight, 0.5) {
		t.Fatalf("appended synapse weight = %v, want 0.5", weight)
	}

	session = NewSession(fresh)
	if err := session.StageDelete(target, "brain.Synapses[0]"); err != nil {
		t.Fatalf("StageDelete() error = %v", err)
	}
	fresh, err = session.Commit(filepath.Join(t.TempDir(), "deleted.zip"))
	if err != nil {
		t.Fatalf("Commit(delete) error = %v", err)
	}
	if got := jsonArrayLen(t, fresh.Entry("bibites/bibite_0.bb8").JSON, "brain.Synapses"); got != 0 {
		t.Fatalf("synapse count after delete = %d, want 0", got)
	}
}

func TestSQLAppendAndDeleteBrainNode(t *testing.T) {
	archive := parseSyntheticArchive(t)
	session := NewSession(archive)
	ref := SQLValueRef{Table: "bibite_brain_nodes", EntryName: "bibites/bibite_0.bb8", BodyID: 42, HasBodyID: true}

	node := map[string]any{"Index": 5, "Type": 0, "TypeName": "Input", "Value": 0.0}
	if err := session.StageSQLAppend(ref, node); err != nil {
		t.Fatalf("StageSQLAppend(node) error = %v", err)
	}
	fresh, err := session.Commit(filepath.Join(t.TempDir(), "node_appended.zip"))
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if got := jsonArrayLen(t, fresh.Entry("bibites/bibite_0.bb8").JSON, "brain.Nodes"); got != 1 {
		t.Fatalf("node count after append = %d, want 1", got)
	}

	session = NewSession(fresh)
	del := SQLValueRef{Table: "bibite_brain_nodes", EntryName: "bibites/bibite_0.bb8", BodyID: 42, HasBodyID: true, NodeRowIndex: 0, HasNodeRowIndex: true}
	if err := session.StageSQLDelete(del); err != nil {
		t.Fatalf("StageSQLDelete(node) error = %v", err)
	}
	fresh, err = session.Commit(filepath.Join(t.TempDir(), "node_deleted.zip"))
	if err != nil {
		t.Fatalf("Commit(delete) error = %v", err)
	}
	if got := jsonArrayLen(t, fresh.Entry("bibites/bibite_0.bb8").JSON, "brain.Nodes"); got != 0 {
		t.Fatalf("node count after delete = %d, want 0", got)
	}
}

func TestSQLAppendAndDeleteStomachContent(t *testing.T) {
	archive := parseSyntheticArchive(t)
	session := NewSession(archive)
	ref := SQLValueRef{Table: "bibite_stomach_contents", EntryName: "bibites/bibite_0.bb8", BodyID: 42, HasBodyID: true}

	content := map[string]any{"material": "Meat", "amount": 1.5, "averageChunkAmount": 0.5}
	if err := session.StageSQLAppend(ref, content); err != nil {
		t.Fatalf("StageSQLAppend(content) error = %v", err)
	}
	fresh, err := session.Commit(filepath.Join(t.TempDir(), "content_appended.zip"))
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	// The synthetic bibite starts with one stomach content element.
	if got := jsonArrayLen(t, fresh.Entry("bibites/bibite_0.bb8").JSON, "body.stomach.content"); got != 2 {
		t.Fatalf("content count after append = %d, want 2", got)
	}

	session = NewSession(fresh)
	del := SQLValueRef{Table: "bibite_stomach_contents", EntryName: "bibites/bibite_0.bb8", BodyID: 42, HasBodyID: true, ContentIndex: 1, HasContentIndex: true}
	if err := session.StageSQLDelete(del); err != nil {
		t.Fatalf("StageSQLDelete(content) error = %v", err)
	}
	fresh, err = session.Commit(filepath.Join(t.TempDir(), "content_deleted.zip"))
	if err != nil {
		t.Fatalf("Commit(delete) error = %v", err)
	}
	if got := jsonArrayLen(t, fresh.Entry("bibites/bibite_0.bb8").JSON, "body.stomach.content"); got != 1 {
		t.Fatalf("content count after delete = %d, want 1", got)
	}
}

func TestStageAppendAndDeleteZone(t *testing.T) {
	archive := parseSyntheticArchive(t)
	session := NewSession(archive)
	target := SettingsTarget()

	zone := map[string]any{"id": 8, "name": "Zone B", "material": "Plant", "fertility": map[string]any{"Value": 0.9}}
	if err := session.StageAppend(target, "zones", zone); err != nil {
		t.Fatalf("StageAppend(zone) error = %v", err)
	}
	fresh, err := session.Commit(filepath.Join(t.TempDir(), "zone_appended.zip"))
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if got := jsonArrayLen(t, fresh.Entry("settings.bb8settings").JSON, "zones"); got != 2 {
		t.Fatalf("zone count after append = %d, want 2", got)
	}

	session = NewSession(fresh)
	if err := session.StageDelete(target, "zones[1]"); err != nil {
		t.Fatalf("StageDelete(zone) error = %v", err)
	}
	fresh, err = session.Commit(filepath.Join(t.TempDir(), "zone_deleted.zip"))
	if err != nil {
		t.Fatalf("Commit(delete) error = %v", err)
	}
	if got := jsonArrayLen(t, fresh.Entry("settings.bb8settings").JSON, "zones"); got != 1 {
		t.Fatalf("zone count after delete = %d, want 1", got)
	}
}

func scenePelletCount(t *testing.T, archive *tb.Archive) int64 {
	t.Helper()
	value, ok, err := getJSONPath(archive.Entry("scene.bb8scene").JSON, "nPellets")
	if err != nil || !ok {
		t.Fatalf("getJSONPath(nPellets) = %v/%t/%v", value, ok, err)
	}
	n, ok := jsonNumberToInt64(value)
	if !ok {
		t.Fatalf("nPellets = %v, not an integer", value)
	}
	return n
}

func TestStageAppendAndDeletePellet(t *testing.T) {
	archive := parseSyntheticArchive(t)
	session := NewSession(archive)

	pellet := map[string]any{"pellet": map[string]any{"material": "Plant", "amount": 9}}
	if err := session.StageAppendPellet(0, pellet); err != nil {
		t.Fatalf("StageAppendPellet() error = %v", err)
	}
	fresh, err := session.Commit(filepath.Join(t.TempDir(), "pellet_appended.zip"))
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if got := jsonArrayLen(t, fresh.Entry("pellets.bb8scene").JSON, "pellets[0].pellets"); got != 2 {
		t.Fatalf("pellet count after append = %d, want 2", got)
	}
	if got := scenePelletCount(t, fresh); got != 2 {
		t.Fatalf("scene nPellets after append = %d, want 2", got)
	}

	session = NewSession(fresh)
	if err := session.StageDeletePellet(0, 0); err != nil {
		t.Fatalf("StageDeletePellet() error = %v", err)
	}
	fresh, err = session.Commit(filepath.Join(t.TempDir(), "pellet_deleted.zip"))
	if err != nil {
		t.Fatalf("Commit(delete) error = %v", err)
	}
	if got := jsonArrayLen(t, fresh.Entry("pellets.bb8scene").JSON, "pellets[0].pellets"); got != 1 {
		t.Fatalf("pellet count after delete = %d, want 1", got)
	}
	if got := scenePelletCount(t, fresh); got != 1 {
		t.Fatalf("scene nPellets after delete = %d, want 1", got)
	}
}

func TestSQLPelletAppendDeleteCarriesSceneCount(t *testing.T) {
	ref := SQLValueRef{
		Table: "pellets", EntryName: "pellets.bb8scene",
		GroupIndex: 0, HasGroupIndex: true, GroupPelletIndex: 0, HasGroupPelletIndex: true,
	}
	del, err := SQLDelete(ref)
	if err != nil {
		t.Fatalf("SQLDelete() error = %v", err)
	}
	if del.SceneCount != "nPellets" {
		t.Fatalf("SQLDelete scene count = %q, want nPellets", del.SceneCount)
	}
	app, err := SQLAppend(ref, map[string]any{"pellet": map[string]any{"material": "Plant", "amount": 1}})
	if err != nil {
		t.Fatalf("SQLAppend() error = %v", err)
	}
	if app.SceneCount != "nPellets" {
		t.Fatalf("SQLAppend scene count = %q, want nPellets", app.SceneCount)
	}
}

func TestStageDeleteBibiteEntry(t *testing.T) {
	archive := parseSyntheticArchive(t)
	settingsSHA := archive.Entry("settings.bb8settings").SHA256
	pelletsSHA := archive.Entry("pellets.bb8scene").SHA256

	session := NewSession(archive)
	if err := session.StageDeleteBibite(BibiteRef{EntryName: "bibites/bibite_0.bb8", BodyID: 42}); err != nil {
		t.Fatalf("StageDeleteBibite() error = %v", err)
	}
	fresh, err := session.Commit(filepath.Join(t.TempDir(), "bibite_deleted.zip"))
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if fresh.Entry("bibites/bibite_0.bb8") != nil {
		t.Fatalf("bibite entry still present after delete")
	}
	count, ok, err := getJSONPath(fresh.Entry("scene.bb8scene").JSON, "nBibites")
	if err != nil || !ok {
		t.Fatalf("getJSONPath(nBibites) = %v/%t/%v", count, ok, err)
	}
	if !jsonValuesEqual(count, 0) {
		t.Fatalf("scene nBibites after delete = %v, want 0", count)
	}
	if fresh.Entry("settings.bb8settings").SHA256 != settingsSHA {
		t.Fatalf("settings entry SHA changed by unrelated bibite delete")
	}
	if fresh.Entry("pellets.bb8scene").SHA256 != pelletsSHA {
		t.Fatalf("pellets entry SHA changed by unrelated bibite delete")
	}
}

func TestStageAppendBibiteEntry(t *testing.T) {
	archive := parseSyntheticArchive(t)
	session := NewSession(archive)

	payload := EntryPayload{
		Name: "bibites/bibite_1.bb8",
		Kind: tb.EntryBibite,
		JSON: map[string]any{"body": map[string]any{"id": 77, "energy": 5}, "brain": map[string]any{"Nodes": []any{}, "Synapses": []any{}}},
	}
	if err := session.StageAppendBibite(payload); err != nil {
		t.Fatalf("StageAppendBibite() error = %v", err)
	}
	fresh, err := session.Commit(filepath.Join(t.TempDir(), "bibite_appended.zip"))
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if fresh.Entry("bibites/bibite_1.bb8") == nil {
		t.Fatalf("appended bibite entry missing")
	}
	count, ok, err := getJSONPath(fresh.Entry("scene.bb8scene").JSON, "nBibites")
	if err != nil || !ok {
		t.Fatalf("getJSONPath(nBibites) = %v/%t/%v", count, ok, err)
	}
	if !jsonValuesEqual(count, 2) {
		t.Fatalf("scene nBibites after append = %v, want 2", count)
	}
}

func TestStageDeleteBibiteParentChildLink(t *testing.T) {
	// bibite_0 (id 42) is the parent of child id 99 (bibite_1). Deleting the
	// child must not silently orphan the parent's children list.
	newArchive := func(t *testing.T) *tb.Archive {
		return parseChildLinkArchive(t)
	}

	t.Run("refuses orphan by default", func(t *testing.T) {
		session := NewSession(newArchive(t))
		if err := session.StageDeleteBibite(BibiteRef{EntryName: "bibites/bibite_1.bb8", BodyID: 99}); err != nil {
			t.Fatalf("StageDeleteBibite() error = %v", err)
		}
		if err := session.Apply(); err == nil {
			t.Fatalf("Apply() error = nil, want orphan refusal")
		}
	})

	t.Run("prunes parent link when requested", func(t *testing.T) {
		archive := newArchive(t)
		session := NewSession(archive)
		if err := session.StageDeleteBibiteWithOptions(
			BibiteRef{EntryName: "bibites/bibite_1.bb8", BodyID: 99},
			DeleteOptions{PruneParentLinks: true},
		); err != nil {
			t.Fatalf("StageDeleteBibiteWithOptions() error = %v", err)
		}
		fresh, err := session.Commit(filepath.Join(t.TempDir(), "pruned.zip"))
		if err != nil {
			t.Fatalf("Commit() error = %v", err)
		}
		if fresh.Entry("bibites/bibite_1.bb8") != nil {
			t.Fatalf("child bibite still present after delete")
		}
		if got := jsonArrayLen(t, fresh.Entry("bibites/bibite_0.bb8").JSON, "body.eggLayer.children"); got != 0 {
			t.Fatalf("parent children after prune = %d, want 0", got)
		}
	})

	t.Run("unreferenced bibite deletes cleanly", func(t *testing.T) {
		session := NewSession(newArchive(t))
		// Deleting the parent (id 42) is not referenced as anyone's child.
		if err := session.StageDeleteBibite(BibiteRef{EntryName: "bibites/bibite_0.bb8", BodyID: 42}); err != nil {
			t.Fatalf("StageDeleteBibite() error = %v", err)
		}
		if err := session.Apply(); err != nil {
			t.Fatalf("Apply() error = %v, want clean delete", err)
		}
	})
}

func TestSQLDeleteResolvesTypedZones(t *testing.T) {
	cases := []struct {
		name string
		ref  SQLValueRef
		path string
	}{
		{
			name: "synapse",
			ref: SQLValueRef{
				Table: "bibite_brain_synapses", EntryName: "bibites/bibite_0.bb8",
				BodyID: 42, HasBodyID: true, SynapseRowIndex: 2, HasSynapseRowIndex: true,
			},
			path: "brain.Synapses[2]",
		},
		{
			name: "node",
			ref: SQLValueRef{
				Table: "bibite_brain_nodes", EntryName: "bibites/bibite_0.bb8",
				BodyID: 42, HasBodyID: true, NodeRowIndex: 1, HasNodeRowIndex: true,
			},
			path: "brain.Nodes[1]",
		},
		{
			name: "stomach_content",
			ref: SQLValueRef{
				Table: "bibite_stomach_contents", EntryName: "bibites/bibite_0.bb8",
				BodyID: 42, HasBodyID: true, ContentIndex: 0, HasContentIndex: true,
			},
			path: "body.stomach.content[0]",
		},
		{
			name: "pellet",
			ref: SQLValueRef{
				Table: "pellets", EntryName: "pellets.bb8scene",
				GroupIndex: 0, HasGroupIndex: true, GroupPelletIndex: 1, HasGroupPelletIndex: true,
			},
			path: "pellets[0].pellets[1]",
		},
		{
			name: "zone",
			ref: SQLValueRef{
				Table: "settings_zones", EntryName: "settings.bb8settings",
				ZoneIndex: 3, HasZoneIndex: true,
			},
			path: "zones[3]",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			op, err := SQLDelete(tc.ref)
			if err != nil {
				t.Fatalf("SQLDelete() error = %v", err)
			}
			if op.Kind != OperationDelete {
				t.Fatalf("op kind = %q, want delete", op.Kind)
			}
			if op.Path != tc.path {
				t.Fatalf("op path = %q, want %q", op.Path, tc.path)
			}
		})
	}
}

func TestSQLDeleteBibiteEntry(t *testing.T) {
	op, err := SQLDelete(SQLValueRef{
		Table: "bibites", EntryName: "bibites/bibite_0.bb8", BodyID: 42, HasBodyID: true,
	})
	if err != nil {
		t.Fatalf("SQLDelete() error = %v", err)
	}
	if op.Kind != OperationDeleteEntry {
		t.Fatalf("op kind = %q, want delete_entry", op.Kind)
	}
	if op.Target.EntryName != "bibites/bibite_0.bb8" || op.Target.Kind != tb.EntryBibite {
		t.Fatalf("op target = %+v, want bibite entry", op.Target)
	}
}

func TestSQLAppendArrayAndEntryRules(t *testing.T) {
	op, err := SQLAppend(SQLValueRef{
		Table: "bibite_brain_synapses", EntryName: "bibites/bibite_0.bb8", BodyID: 42, HasBodyID: true,
	}, map[string]any{"En": true})
	if err != nil {
		t.Fatalf("SQLAppend(synapse) error = %v", err)
	}
	if op.Kind != OperationAppend || op.Path != "brain.Synapses" {
		t.Fatalf("synapse append op = %q/%q, want append/brain.Synapses", op.Kind, op.Path)
	}

	node, err := SQLAppend(SQLValueRef{
		Table: "bibite_brain_nodes", EntryName: "bibites/bibite_0.bb8", BodyID: 42, HasBodyID: true,
	}, map[string]any{"Type": 0})
	if err != nil {
		t.Fatalf("SQLAppend(node) error = %v", err)
	}
	if node.Kind != OperationAppend || node.Path != "brain.Nodes" {
		t.Fatalf("node append op = %q/%q, want append/brain.Nodes", node.Kind, node.Path)
	}

	content, err := SQLAppend(SQLValueRef{
		Table: "bibite_stomach_contents", EntryName: "bibites/bibite_0.bb8", BodyID: 42, HasBodyID: true,
	}, map[string]any{"material": "Plant", "amount": 1})
	if err != nil {
		t.Fatalf("SQLAppend(content) error = %v", err)
	}
	if content.Kind != OperationAppend || content.Path != "body.stomach.content" {
		t.Fatalf("content append op = %q/%q, want append/body.stomach.content", content.Kind, content.Path)
	}

	if _, err := SQLAppend(SQLValueRef{
		Table: "bibites", EntryName: "bibites/bibite_0.bb8", BodyID: 42, HasBodyID: true,
	}, nil); err == nil {
		t.Fatalf("SQLAppend(bibite entry) error = nil, want cross-save not supported")
	}

	if _, err := SQLDelete(SQLValueRef{Table: "bibite_genes", Column: "number_value"}); err == nil {
		t.Fatalf("SQLDelete(non-typed table) error = nil, want unsupported")
	}
}

// TestSQLArrayCapabilityMatchesResolverKinds is the core-parity check: array and
// entry mutability must be derived from the generated resolver kinds, set for
// exactly the typed-zone tables and nil everywhere else.
func TestSQLArrayCapabilityMatchesResolverKinds(t *testing.T) {
	arrayTables := map[string]bool{
		"bibite_brain_synapses":   true,
		"egg_brain_synapses":      true,
		"bibite_brain_nodes":      true,
		"egg_brain_nodes":         true,
		"bibite_stomach_contents": true,
		"pellets":                 true,
		"settings_zones":          true,
	}
	entryTables := map[string]bool{
		"bibites":                   true,
		"bibite_body":               true,
		"bibite_mouth":              true,
		"bibite_pheromone_emitters": true,
		"bibite_egg_layers":         true,
		"bibite_control":            true,
		"eggs":                      true,
	}
	for _, spec := range writableSQLRefTables {
		wantArray := arrayTables[spec.table]
		wantEntry := entryTables[spec.table]
		hasArray := spec.appendArray != nil && spec.deleteArray != nil
		if hasArray != wantArray {
			t.Errorf("table %q array capability = %t, want %t", spec.table, hasArray, wantArray)
		}
		if (spec.appendArray == nil) != (spec.deleteArray == nil) {
			t.Errorf("table %q has only one of append/delete array capability", spec.table)
		}
		if (spec.entry != nil) != wantEntry {
			t.Errorf("table %q entry capability = %t, want %t", spec.table, spec.entry != nil, wantEntry)
		}
		if wantArray && wantEntry {
			t.Errorf("table %q is both array and entry mutable", spec.table)
		}
	}
}

func activeSpeciesIDs(t *testing.T, archive *tb.Archive) []int64 {
	t.Helper()
	value, ok, err := getJSONPath(archive.Entry("speciesData.json").JSON, "activeSpeciesList")
	if err != nil || !ok {
		t.Fatalf("getJSONPath(activeSpeciesList) = %v/%t/%v", value, ok, err)
	}
	ids, ok := value.([]any)
	if !ok {
		t.Fatalf("activeSpeciesList = %T, want array", value)
	}
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		n, ok := jsonNumberToInt64(id)
		if !ok {
			t.Fatalf("activeSpeciesList element %v not an integer", id)
		}
		out = append(out, n)
	}
	return out
}

func wantActiveSpecies(t *testing.T, got []int64, want ...int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("activeSpeciesList = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("activeSpeciesList = %v, want %v", got, want)
		}
	}
}

// parseSpeciesArchive builds a save with a speciesData.json (activeSpeciesList
// [1,2,3], a recordedSpecies record per id with a distinct template.genes/name,
// and a nextSpeciesID) and members so each branch of species reconciliation is
// exercised: species 1 has one bibite (bibite_0), species 2 has two bibites
// (bibite_1, bibite_2), and species 3 has one egg (egg_0). The non-empty records
// let the F3 remap tests observe that the dest's coincidental same-id record is
// left untouched (no conflation) and that nextSpeciesID is the allocator's
// preferred path.
func parseSpeciesArchive(t *testing.T) *tb.Archive {
	t.Helper()

	withBOM := func(body string) []byte {
		raw := append([]byte(nil), utf8BOM...)
		return append(raw, []byte(body)...)
	}

	speciesData := `{"nextSpeciesID":4,"activeSpeciesList":[1,2,3],"recordedSpecies":[` +
		`{"speciesID":1,"parentID":0,"name":"dest-alpha","template":{"genes":{"SizeRatio":1.1}}},` +
		`{"speciesID":2,"parentID":0,"name":"dest-beta","template":{"genes":{"SizeRatio":2.2}}},` +
		`{"speciesID":3,"parentID":0,"name":"dest-gamma","template":{"genes":{"SizeRatio":3.3}}}` +
		`]}`

	sourcePath := filepath.Join(t.TempDir(), "species.zip")
	archive := &tb.Archive{
		Entries: []tb.Entry{
			{Index: 0, Name: "scene.bb8scene", Kind: tb.EntryScene, Method: zip.Deflate, Raw: withBOM(`{"nBibites":3}`)},
			{Index: 1, Name: "speciesData.json", Kind: tb.EntrySpecies, Method: zip.Deflate, Raw: withBOM(speciesData)},
			{Index: 2, Name: "bibites/bibite_0.bb8", Kind: tb.EntryBibite, Method: zip.Deflate, Raw: withBOM(`{"body":{"id":42},"genes":{"speciesID":1},"brain":{"Nodes":[],"Synapses":[]}}`)},
			{Index: 3, Name: "bibites/bibite_1.bb8", Kind: tb.EntryBibite, Method: zip.Deflate, Raw: withBOM(`{"body":{"id":43},"genes":{"speciesID":2},"brain":{"Nodes":[],"Synapses":[]}}`)},
			{Index: 4, Name: "bibites/bibite_2.bb8", Kind: tb.EntryBibite, Method: zip.Deflate, Raw: withBOM(`{"body":{"id":44},"genes":{"speciesID":2},"brain":{"Nodes":[],"Synapses":[]}}`)},
			{Index: 5, Name: "eggs/egg_0.bb8", Kind: tb.EntryEgg, Method: zip.Deflate, Raw: withBOM(`{"egg":{"id":0,"energy":10},"genes":{"speciesID":3,"gen":1,"isReady":true}}`)},
		},
	}
	if err := tb.WriteArchive(sourcePath, archive); err != nil {
		t.Fatalf("WriteArchive(species) error = %v", err)
	}
	parsed, err := tb.ParseFile(sourcePath, nil)
	if err != nil {
		t.Fatalf("ParseFile(species) error = %v", err)
	}
	return parsed
}

func TestStageDeleteReconcilesActiveSpecies(t *testing.T) {
	t.Run("last member drops the species id", func(t *testing.T) {
		session := NewSession(parseSpeciesArchive(t))
		if err := session.StageDeleteBibite(BibiteRef{EntryName: "bibites/bibite_0.bb8", BodyID: 42}); err != nil {
			t.Fatalf("StageDeleteBibite() error = %v", err)
		}
		fresh, err := session.Commit(filepath.Join(t.TempDir(), "species_last.zip"))
		if err != nil {
			t.Fatalf("Commit() error = %v", err)
		}
		wantActiveSpecies(t, activeSpeciesIDs(t, fresh), 2, 3)
	})

	t.Run("non-last member keeps the species id", func(t *testing.T) {
		session := NewSession(parseSpeciesArchive(t))
		if err := session.StageDeleteBibite(BibiteRef{EntryName: "bibites/bibite_1.bb8", BodyID: 43}); err != nil {
			t.Fatalf("StageDeleteBibite() error = %v", err)
		}
		fresh, err := session.Commit(filepath.Join(t.TempDir(), "species_keep.zip"))
		if err != nil {
			t.Fatalf("Commit() error = %v", err)
		}
		wantActiveSpecies(t, activeSpeciesIDs(t, fresh), 1, 2, 3)
	})

	t.Run("egg as last member drops the species id", func(t *testing.T) {
		session := NewSession(parseSpeciesArchive(t))
		if err := session.StageSQLDelete(SQLValueRef{
			Table: "eggs", EntryName: "eggs/egg_0.bb8", EggID: 0, HasEggID: true,
		}); err != nil {
			t.Fatalf("StageSQLDelete(egg) error = %v", err)
		}
		fresh, err := session.Commit(filepath.Join(t.TempDir(), "species_egg.zip"))
		if err != nil {
			t.Fatalf("Commit() error = %v", err)
		}
		if fresh.Entry("eggs/egg_0.bb8") != nil {
			t.Fatalf("egg entry still present after delete")
		}
		wantActiveSpecies(t, activeSpeciesIDs(t, fresh), 1, 2)
	})

	t.Run("both members of a species in one batch drops the id", func(t *testing.T) {
		session := NewSession(parseSpeciesArchive(t))
		if err := session.StageDeleteBibite(BibiteRef{EntryName: "bibites/bibite_1.bb8", BodyID: 43}); err != nil {
			t.Fatalf("StageDeleteBibite(1) error = %v", err)
		}
		if err := session.StageDeleteBibite(BibiteRef{EntryName: "bibites/bibite_2.bb8", BodyID: 44}); err != nil {
			t.Fatalf("StageDeleteBibite(2) error = %v", err)
		}
		fresh, err := session.Commit(filepath.Join(t.TempDir(), "species_batch.zip"))
		if err != nil {
			t.Fatalf("Commit() error = %v", err)
		}
		wantActiveSpecies(t, activeSpeciesIDs(t, fresh), 1, 3)
	})
}

// TestStageDeleteWithUndecodedSpeciesData covers F6: a species entry that is
// present but has nil decoded JSON (the parser keeps it after a
// json_decode_failed diagnostic). Deleting the last member of a species must
// degrade quietly instead of aborting the whole delete in entryUpdate. We null
// the decoded JSON directly to model that state deterministically, independent
// of how the parser handles malformed bytes.
func TestStageDeleteWithUndecodedSpeciesData(t *testing.T) {
	archive := parseSpeciesArchive(t)
	species := archive.Entry("speciesData.json")
	if species == nil {
		t.Fatalf("species entry missing from synthetic archive")
	}
	species.JSON = nil

	session := NewSession(archive)
	// bibite_0 is the last (only) member of species 1.
	if err := session.StageDeleteBibite(BibiteRef{EntryName: "bibites/bibite_0.bb8", BodyID: 42}); err != nil {
		t.Fatalf("StageDeleteBibite() error = %v", err)
	}
	if err := session.Apply(); err != nil {
		t.Fatalf("Apply() error = %v, want quiet degrade on undecoded speciesData.json", err)
	}
	if session.Archive().Entry("bibites/bibite_0.bb8") != nil {
		t.Fatalf("bibite entry still present after delete")
	}
	// Other entries remain intact; the undecoded species entry is left as-is.
	if session.Archive().Entry("bibites/bibite_1.bb8") == nil {
		t.Fatalf("unrelated bibite_1 missing after delete")
	}
	leftover := session.Archive().Entry("speciesData.json")
	if leftover == nil {
		t.Fatalf("species entry dropped by delete")
	}
	if leftover.JSON != nil {
		t.Fatalf("species entry JSON = %v, want nil (left untouched)", leftover.JSON)
	}
}

func parseChildLinkArchive(t *testing.T) *tb.Archive {
	t.Helper()

	rawScene := append([]byte(nil), utf8BOM...)
	rawScene = append(rawScene, []byte(`{"nBibites":2}`)...)
	rawParent := append([]byte(nil), utf8BOM...)
	rawParent = append(rawParent, []byte(`{"body":{"id":42,"eggLayer":{"children":[99]}},"brain":{"Nodes":[],"Synapses":[]}}`)...)
	rawChild := append([]byte(nil), utf8BOM...)
	rawChild = append(rawChild, []byte(`{"body":{"id":99},"brain":{"Nodes":[],"Synapses":[]}}`)...)

	sourcePath := filepath.Join(t.TempDir(), "childlinks.zip")
	archive := &tb.Archive{
		Entries: []tb.Entry{
			{Index: 0, Name: "scene.bb8scene", Kind: tb.EntryScene, Method: zip.Deflate, Raw: rawScene},
			{Index: 1, Name: "bibites/bibite_0.bb8", Kind: tb.EntryBibite, Method: zip.Deflate, Raw: rawParent},
			{Index: 2, Name: "bibites/bibite_1.bb8", Kind: tb.EntryBibite, Method: zip.Deflate, Raw: rawChild},
		},
	}
	if err := tb.WriteArchive(sourcePath, archive); err != nil {
		t.Fatalf("WriteArchive(childlinks) error = %v", err)
	}
	parsed, err := tb.ParseFile(sourcePath, nil)
	if err != nil {
		t.Fatalf("ParseFile(childlinks) error = %v", err)
	}
	return parsed
}
