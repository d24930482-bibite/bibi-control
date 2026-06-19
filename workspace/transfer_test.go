package workspace

import (
	"archive/zip"
	"context"
	"encoding/json"
	"strings"
	"testing"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
	"github.com/asemones/bibicontrol/script/thebibites"
)

// utf8BOM is the byte-order mark the parser tolerates on save entries; the
// synthetic transfer fixtures stamp it like the savemutator/thebibites tests do.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

func withBOM(body string) []byte {
	raw := append([]byte(nil), utf8BOM...)
	return append(raw, []byte(body)...)
}

// writeTransferSource writes a small SOURCE save under path: one bibite (body
// 500) carrying genes.speciesID==sid with a matching recordedSpecies record (the
// SizeRatio marker proves the SOURCE record is what gets imported), plus one egg.
func writeTransferSource(t *testing.T, path string, sid int64, sizeMarker float64) {
	t.Helper()
	speciesData := `{"nextSpeciesID":` + itoa(sid+1) + `,"activeSpeciesList":[` + itoa(sid) +
		`],"recordedSpecies":[{"speciesID":` + itoa(sid) + `,"parentID":0,"name":"src-species","template":{"genes":{"SizeRatio":` + ftoa(sizeMarker) + `}}}]}`
	archive := &tb.Archive{
		Entries: []tb.Entry{
			{Index: 0, Name: "scene.bb8scene", Kind: tb.EntryScene, Method: zip.Deflate, Raw: withBOM(`{"nBibites":1}`)},
			{Index: 1, Name: "settings.bb8settings", Kind: tb.EntrySettings, Method: zip.Deflate, Raw: withBOM(`{"worldLabel":{"Value":"source-world"},"zones":[],"zoneGroups":[],"bibites":[],"settingsChangers":[]}`)},
			{Index: 2, Name: "speciesData.json", Kind: tb.EntrySpecies, Method: zip.Deflate, Raw: withBOM(speciesData)},
			{Index: 3, Name: "bibites/bibite_0.bb8", Kind: tb.EntryBibite, Method: zip.Deflate, Raw: withBOM(`{"body":{"id":500,"energy":42.0},"genes":{"speciesID":` + itoa(sid) + `,"gen":1},"brain":{"Nodes":[],"Synapses":[]}}`)},
			{Index: 4, Name: "eggs/egg_0.bb8", Kind: tb.EntryEgg, Method: zip.Deflate, Raw: withBOM(`{"egg":{"id":900,"energy":12},"genes":{"speciesID":` + itoa(sid) + `,"gen":2,"isReady":true}}`)},
		},
	}
	if err := tb.WriteArchive(path, archive); err != nil {
		t.Fatalf("WriteArchive(transfer source) error = %v", err)
	}
}

// writeTransferDest writes a small DEST save under path with species [1,2,3] whose
// member bibites carry distinct ids, modeled on the savemutator species fixture.
// The dest species record SizeRatio markers (1.1/2.2/3.3) prove they are
// untouched by a graft. nextSpeciesID is 4 so a fresh remap id is >= 4.
func writeTransferDest(t *testing.T, path string) {
	t.Helper()
	speciesData := `{"nextSpeciesID":4,"activeSpeciesList":[1,2,3],"recordedSpecies":[` +
		`{"speciesID":1,"parentID":0,"name":"dest-alpha","template":{"genes":{"SizeRatio":1.1}}},` +
		`{"speciesID":2,"parentID":0,"name":"dest-beta","template":{"genes":{"SizeRatio":2.2}}},` +
		`{"speciesID":3,"parentID":0,"name":"dest-gamma","template":{"genes":{"SizeRatio":3.3}}}` +
		`]}`
	archive := &tb.Archive{
		Entries: []tb.Entry{
			{Index: 0, Name: "scene.bb8scene", Kind: tb.EntryScene, Method: zip.Deflate, Raw: withBOM(`{"nBibites":3}`)},
			{Index: 1, Name: "settings.bb8settings", Kind: tb.EntrySettings, Method: zip.Deflate, Raw: withBOM(`{"worldLabel":{"Value":"dest-world"},"zones":[],"zoneGroups":[],"bibites":[],"settingsChangers":[]}`)},
			{Index: 2, Name: "speciesData.json", Kind: tb.EntrySpecies, Method: zip.Deflate, Raw: withBOM(speciesData)},
			{Index: 3, Name: "bibites/bibite_0.bb8", Kind: tb.EntryBibite, Method: zip.Deflate, Raw: withBOM(`{"body":{"id":42},"genes":{"speciesID":1},"brain":{"Nodes":[],"Synapses":[]}}`)},
			{Index: 4, Name: "bibites/bibite_1.bb8", Kind: tb.EntryBibite, Method: zip.Deflate, Raw: withBOM(`{"body":{"id":43},"genes":{"speciesID":2},"brain":{"Nodes":[],"Synapses":[]}}`)},
			{Index: 5, Name: "bibites/bibite_2.bb8", Kind: tb.EntryBibite, Method: zip.Deflate, Raw: withBOM(`{"body":{"id":44},"genes":{"speciesID":2},"brain":{"Nodes":[],"Synapses":[]}}`)},
		},
	}
	if err := tb.WriteArchive(path, archive); err != nil {
		t.Fatalf("WriteArchive(transfer dest) error = %v", err)
	}
}

// writeTransferSourceMulti writes a SOURCE save with `count` bibites whose body
// ids start at 700 (non-colliding with the dest fixture's 42/43/44) and whose
// genes.speciesID is a single source species (sid, marker 7.0) with a matching
// recordedSpecies record — so the cross-world graft never hits the body.id
// collision guard (which is loud-by-design and out of F2's scope).
func writeTransferSourceMulti(t *testing.T, path string, count int, sid int64) {
	t.Helper()
	entries := []tb.Entry{
		{Index: 0, Name: "scene.bb8scene", Kind: tb.EntryScene, Method: zip.Deflate, Raw: withBOM(`{"nBibites":` + itoa(int64(count)) + `}`)},
		{Index: 1, Name: "settings.bb8settings", Kind: tb.EntrySettings, Method: zip.Deflate, Raw: withBOM(`{"worldLabel":{"Value":"source-world"},"zones":[],"zoneGroups":[],"bibites":[],"settingsChangers":[]}`)},
		{Index: 2, Name: "speciesData.json", Kind: tb.EntrySpecies, Method: zip.Deflate, Raw: withBOM(`{"nextSpeciesID":` + itoa(sid+1) + `,"activeSpeciesList":[` + itoa(sid) + `],"recordedSpecies":[{"speciesID":` + itoa(sid) + `,"parentID":0,"name":"src-species","template":{"genes":{"SizeRatio":7.0}}}]}`)},
	}
	for i := 0; i < count; i++ {
		bodyID := int64(700 + i)
		entries = append(entries, tb.Entry{
			Index:  3 + i,
			Name:   "bibites/bibite_" + itoa(int64(i)) + ".bb8",
			Kind:   tb.EntryBibite,
			Method: zip.Deflate,
			Raw:    withBOM(`{"body":{"id":` + itoa(bodyID) + `,"energy":33.0},"genes":{"speciesID":` + itoa(sid) + `,"gen":1},"brain":{"Nodes":[],"Synapses":[]}}`),
		})
	}
	if err := tb.WriteArchive(path, &tb.Archive{Entries: entries}); err != nil {
		t.Fatalf("WriteArchive(transfer source multi) error = %v", err)
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// ftoa renders a float marker (e.g. 22.0 -> "22.0") for the synthetic fixtures.
func ftoa(f float64) string {
	whole := int64(f)
	frac := int64((f - float64(whole)) * 10)
	if frac < 0 {
		frac = -frac
	}
	return itoa(whole) + "." + itoa(frac)
}

// ---------------------------------------------------------------------------
// TestAutomation_TransferCollectionAdvancesDstHead — the where-collection path.
// ---------------------------------------------------------------------------

func TestAutomation_TransferCollectionAdvancesDstHead(t *testing.T) {
	ctx := testCtxAuto(t)
	ws := newWorkspace(t, ctx)

	// Synthetic non-colliding worlds: cross-world body.id collision is a loud,
	// out-of-scope engine guard, so the source bibites carry distinct body ids
	// (700+) from the dest's (42/43/44).
	tmp := t.TempDir()
	srcPath := tmp + "/coll_src.zip"
	dstPath := tmp + "/coll_dst.zip"
	const srcBibites = 3
	writeTransferSourceMulti(t, srcPath, srcBibites, 9)
	writeTransferDest(t, dstPath)

	worldA, err := ws.AddWorld(ctx, srcPath, "world-a")
	if err != nil {
		t.Fatalf("AddWorld(src): %v", err)
	}
	worldB, err := ws.AddWorld(ctx, dstPath, "world-b")
	if err != nil {
		t.Fatalf("AddWorld(dst): %v", err)
	}

	headBBefore := headRevision(t, ctx, ws, worldB.ID)
	bBefore := countBibitesWorking(t, ctx, ws, worldB.ID)
	aBefore := countBibitesWorking(t, ctx, ws, worldA.ID)

	prog := `
a = workspace.world("` + worldA.ID + `")
b = workspace.world("` + worldB.ID + `")
sa = a.open()
r = workspace.transfer(sa.bibites.where("energy >= 0"), dst="` + worldB.ID + `")
print(r["committed"])
print(r["transferred"])
print(len(r["sha256"]))
`
	res := mustRunAuto(t, ctx, ws, prog)
	lines := strings.Split(strings.TrimRight(res.Output, "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected >=3 output lines, got %v\nOutput:\n%s", lines, res.Output)
	}
	if lines[0] != "True" {
		t.Fatalf("committed = %q, want True\nOutput:\n%s", lines[0], res.Output)
	}
	if lines[1] != itoa(srcBibites) {
		t.Fatalf("transferred = %q, want %d", lines[1], srcBibites)
	}
	if lines[2] != "64" {
		t.Fatalf("len(sha256) = %q, want 64", lines[2])
	}
	transferred := atoiOrFatal(t, lines[1])

	// B's head advanced; parent is the prior B head; blob self-refed once.
	headBAfter := headRevision(t, ctx, ws, worldB.ID)
	if headBAfter.ID == headBBefore.ID {
		t.Fatalf("dst head did not advance: still %d", headBBefore.ID)
	}
	if headBAfter.ParentID == nil || *headBAfter.ParentID != headBBefore.ID {
		t.Fatalf("new dst head ParentID = %v, want prior head %d", headBAfter.ParentID, headBBefore.ID)
	}
	if headBAfter.Refcount != 1 {
		t.Errorf("new dst head Refcount = %d, want 1 (no double IncBlobRef)", headBAfter.Refcount)
	}

	// B's working bibite count grew by exactly the transferred count; A unchanged.
	bAfter := countBibitesWorking(t, ctx, ws, worldB.ID)
	if bAfter != bBefore+int64(transferred) {
		t.Fatalf("dst bibite count = %d, want %d (prior %d + transferred %d)", bAfter, bBefore+int64(transferred), bBefore, transferred)
	}
	aAfter := countBibitesWorking(t, ctx, ws, worldA.ID)
	if aAfter != aBefore {
		t.Fatalf("src bibite count changed: %d -> %d (cross-world isolation broken)", aBefore, aAfter)
	}
}

// ---------------------------------------------------------------------------
// TestAutomation_TransferSingleEntity — the single-Entity selector branch.
// ---------------------------------------------------------------------------

func TestAutomation_TransferSingleEntity(t *testing.T) {
	ctx := testCtxAuto(t)
	ws := newWorkspace(t, ctx)

	tmp := t.TempDir()
	srcPath := tmp + "/single_src.zip"
	dstPath := tmp + "/single_dst.zip"
	writeTransferSourceMulti(t, srcPath, 3, 9)
	writeTransferDest(t, dstPath)

	worldA, err := ws.AddWorld(ctx, srcPath, "world-a")
	if err != nil {
		t.Fatalf("AddWorld(src): %v", err)
	}
	worldB, err := ws.AddWorld(ctx, dstPath, "world-b")
	if err != nil {
		t.Fatalf("AddWorld(dst): %v", err)
	}

	headBBefore := headRevision(t, ctx, ws, worldB.ID)
	bBefore := countBibitesWorking(t, ctx, ws, worldB.ID)

	// Materialize a single Entity via a comprehension (top-level `for` is not
	// allowed in Starlark), then transfer just that one entity.
	prog := `
a = workspace.world("` + worldA.ID + `")
sa = a.open()
one = [x for x in sa.bibites.where("energy >= 0")][0]
r = workspace.transfer(one, dst="` + worldB.ID + `")
print(r["committed"])
print(r["transferred"])
`
	res := mustRunAuto(t, ctx, ws, prog)
	lines := strings.Split(strings.TrimRight(res.Output, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected >=2 output lines, got %v\nOutput:\n%s", lines, res.Output)
	}
	if lines[0] != "True" {
		t.Fatalf("committed = %q, want True\nOutput:\n%s", lines[0], res.Output)
	}
	if lines[1] != "1" {
		t.Fatalf("transferred = %q, want 1 (single entity)", lines[1])
	}

	headBAfter := headRevision(t, ctx, ws, worldB.ID)
	if headBAfter.ID == headBBefore.ID {
		t.Fatalf("dst head did not advance for single-entity transfer")
	}
	bAfter := countBibitesWorking(t, ctx, ws, worldB.ID)
	if bAfter != bBefore+1 {
		t.Fatalf("dst bibite count = %d, want %d (one entity grafted)", bAfter, bBefore+1)
	}
}

// ---------------------------------------------------------------------------
// TestTransferRemapsSpeciesEndToEnd — the F1/F3 species remap, reached through
// F2's surface. A SOURCE bibite's linear genes.speciesID collides with an
// unrelated DEST species; after transfer+commit the grafted bibite must land
// under a FRESH dest id (never the colliding id, never an existing dest id), the
// SOURCE species record must be imported under the fresh id, and the dest's own
// species rows must be untouched (no conflation).
// ---------------------------------------------------------------------------

func TestTransferRemapsSpeciesEndToEnd(t *testing.T) {
	ctx := testCtxAuto(t)
	ws := newWorkspace(t, ctx)

	tmp := t.TempDir()
	srcPath := tmp + "/transfer_src.zip"
	dstPath := tmp + "/transfer_dst.zip"
	// SOURCE bibite carries species 2 (collides with dest's species 2 — an
	// UNRELATED species in the per-world-linear id space), marker 22.0 so the
	// imported record is distinguishable from dest species 2 (marker 2.2).
	const collidingSID int64 = 2
	const srcMarker = 22.0
	writeTransferSource(t, srcPath, collidingSID, srcMarker)
	writeTransferDest(t, dstPath)

	worldSrc, err := ws.AddWorld(ctx, srcPath, "src-world")
	if err != nil {
		t.Fatalf("AddWorld(src): %v", err)
	}
	worldDst, err := ws.AddWorld(ctx, dstPath, "dst-world")
	if err != nil {
		t.Fatalf("AddWorld(dst): %v", err)
	}

	// Drive through Workspace.Transfer directly with the where-collection selection.
	srcLS, err := ws.OpenWorld(ctx, worldSrc.ID)
	if err != nil {
		t.Fatalf("OpenWorld(src): %v", err)
	}
	coll, err := selectAllBibites(srcLS)
	if err != nil {
		t.Fatalf("select source bibites: %v", err)
	}
	result, err := ws.Transfer(ctx, srcLS, worldSrc.ID, coll, worldDst.ID, TransferOptions{})
	if err != nil {
		t.Fatalf("Transfer: %v", err)
	}
	if result.DstRevision.ID == 0 {
		t.Fatalf("Transfer reported no commit (rev.ID == 0)")
	}

	// Re-read the committed dst head blob to inspect the actual saved JSON.
	headDst := headRevision(t, ctx, ws, worldDst.ID)
	archive, err := ws.reparseCommitted(ctx, headDst.BlobRef)
	if err != nil {
		t.Fatalf("reparseCommitted(dst head): %v", err)
	}

	// The grafted bibite is the first appended entry name. The dest had bibite_0..2,
	// so the graft lands at bibite_3.bb8.
	graftedSID := bibiteSpeciesID(t, archive, "bibites/bibite_3.bb8")
	if graftedSID == collidingSID {
		t.Fatalf("grafted speciesID = %d == colliding source id (CONFLATED into dest species 2)", graftedSID)
	}
	for _, used := range []int64{1, 2, 3} {
		if graftedSID == used {
			t.Fatalf("grafted speciesID = %d collides with a pre-existing dest species id", graftedSID)
		}
	}

	// The SOURCE species record was imported under the fresh id (marker 22.0),
	// proving it was imported, not adopted from dest species 2 (marker 2.2).
	gotMarker, ok := speciesMarker(t, archive, graftedSID)
	if !ok {
		t.Fatalf("no recordedSpecies record imported under fresh id %d", graftedSID)
	}
	if gotMarker != srcMarker {
		t.Fatalf("imported record SizeRatio = %v, want %v (source species marker)", gotMarker, srcMarker)
	}

	// The dest's OWN species 2 record is byte-untouched (marker 2.2) and its members
	// still carry species 2 (no reassignment / conflation).
	destMarker, ok := speciesMarker(t, archive, 2)
	if !ok {
		t.Fatalf("dest species 2 record vanished after remap")
	}
	if destMarker != 2.2 {
		t.Fatalf("dest species 2 SizeRatio = %v, want 2.2 (unchanged)", destMarker)
	}
	if got := bibiteSpeciesID(t, archive, "bibites/bibite_1.bb8"); got != 2 {
		t.Fatalf("dest bibite_1 speciesID = %d, want 2 (unchanged)", got)
	}
	if got := bibiteSpeciesID(t, archive, "bibites/bibite_2.bb8"); got != 2 {
		t.Fatalf("dest bibite_2 speciesID = %d, want 2 (unchanged)", got)
	}
}

// ---------------------------------------------------------------------------
// TestAutomation_TransferReplacesP3Stub — transfer is now a bound, callable
// attribute (the P3 stub is gone) and a valid selector/dst commits.
// ---------------------------------------------------------------------------

func TestAutomation_TransferReplacesP3Stub(t *testing.T) {
	ctx := testCtxAuto(t)
	ws := newWorkspace(t, ctx)

	tmp := t.TempDir()
	srcPath := tmp + "/p3_src.zip"
	dstPath := tmp + "/p3_dst.zip"
	writeTransferSourceMulti(t, srcPath, 2, 9)
	writeTransferDest(t, dstPath)

	worldA, err := ws.AddWorld(ctx, srcPath, "p3-src")
	if err != nil {
		t.Fatalf("AddWorld(src): %v", err)
	}
	worldB, err := ws.AddWorld(ctx, dstPath, "p3-dst")
	if err != nil {
		t.Fatalf("AddWorld(dst): %v", err)
	}

	// transfer is a bound attribute (not the deferred (nil,nil) AttributeError).
	prog := `
a = workspace.world("` + worldA.ID + `")
sa = a.open()
r = workspace.transfer(sa.bibites.where("energy >= 0"), dst="` + worldB.ID + `")
print(r["committed"])
`
	res := mustRunAuto(t, ctx, ws, prog)
	out := strings.TrimSpace(res.Output)
	if out != "True" {
		t.Fatalf("transfer committed = %q, want True\nOutput:\n%s", out, res.Output)
	}
}

// ---------------------------------------------------------------------------
// TestAutomation_TransferEmptySelectionNoOp — an empty selection is a clean
// no-op: committed=False, transferred=0, dst head unchanged.
// ---------------------------------------------------------------------------

func TestAutomation_TransferEmptySelectionNoOp(t *testing.T) {
	ctx := testCtxAuto(t)
	ws := newWorkspace(t, ctx)

	tmp := t.TempDir()
	srcPath := tmp + "/noop_src.zip"
	dstPath := tmp + "/noop_dst.zip"
	writeTransferSourceMulti(t, srcPath, 2, 9)
	writeTransferDest(t, dstPath)

	worldA, err := ws.AddWorld(ctx, srcPath, "noop-src")
	if err != nil {
		t.Fatalf("AddWorld(src): %v", err)
	}
	worldB, err := ws.AddWorld(ctx, dstPath, "noop-dst")
	if err != nil {
		t.Fatalf("AddWorld(dst): %v", err)
	}
	headBBefore := headRevision(t, ctx, ws, worldB.ID)

	// A predicate that matches nothing yields an empty selection.
	prog := `
a = workspace.world("` + worldA.ID + `")
sa = a.open()
r = workspace.transfer(sa.bibites.where("energy < -1000000"), dst="` + worldB.ID + `")
print(r["committed"])
print(r["transferred"])
`
	res := mustRunAuto(t, ctx, ws, prog)
	lines := strings.Split(strings.TrimRight(res.Output, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected >=2 output lines, got %v\nOutput:\n%s", lines, res.Output)
	}
	if lines[0] != "False" {
		t.Fatalf("committed = %q, want False (empty selection)", lines[0])
	}
	if lines[1] != "0" {
		t.Fatalf("transferred = %q, want 0", lines[1])
	}
	headBAfter := headRevision(t, ctx, ws, worldB.ID)
	if headBAfter.ID != headBBefore.ID {
		t.Fatalf("dst head moved on empty selection: %d -> %d", headBBefore.ID, headBAfter.ID)
	}
}

// ---------------------------------------------------------------------------
// TestAutomation_TransferSelfTransferRejected — src world == dst world is loud.
// ---------------------------------------------------------------------------

func TestAutomation_TransferSelfTransferRejected(t *testing.T) {
	ctx := testCtxAuto(t)
	ws := newWorkspace(t, ctx)

	srcPath := t.TempDir() + "/self_src.zip"
	writeTransferSourceMulti(t, srcPath, 2, 9)
	world, err := ws.AddWorld(ctx, srcPath, "self-world")
	if err != nil {
		t.Fatalf("AddWorld: %v", err)
	}
	headBefore := headRevision(t, ctx, ws, world.ID)

	prog := `
w = workspace.world("` + world.ID + `")
s = w.open()
workspace.transfer(s.bibites.where("energy >= 0"), dst="` + world.ID + `")
`
	_, runErr := runAuto(ctx, ws, prog)
	if runErr == nil {
		t.Fatalf("self-transfer: want loud error, got nil")
	}
	if !strings.Contains(strings.ToLower(runErr.Error()), "same world") {
		t.Fatalf("self-transfer error = %q, want 'same world'", runErr.Error())
	}
	// The head must not have moved on the rejected self-transfer.
	headAfter := headRevision(t, ctx, ws, world.ID)
	if headAfter.ID != headBefore.ID {
		t.Fatalf("head moved on rejected self-transfer: %d -> %d", headBefore.ID, headAfter.ID)
	}
}

// ---------------------------------------------------------------------------
// TestAutomation_TransferNonEntitySelectorRejected — a non-bibite/egg selector
// (a grouped collection or a bare string) is rejected loudly (no SQL escape).
// ---------------------------------------------------------------------------

func TestAutomation_TransferNonEntitySelectorRejected(t *testing.T) {
	ctx := testCtxAuto(t)
	ws := newWorkspace(t, ctx)

	tmp := t.TempDir()
	srcPath := tmp + "/bad_src.zip"
	dstPath := tmp + "/bad_dst.zip"
	writeTransferSourceMulti(t, srcPath, 2, 9)
	writeTransferDest(t, dstPath)

	worldA, err := ws.AddWorld(ctx, srcPath, "bad-src")
	if err != nil {
		t.Fatalf("AddWorld(src): %v", err)
	}
	worldB, err := ws.AddWorld(ctx, dstPath, "bad-dst")
	if err != nil {
		t.Fatalf("AddWorld(dst): %v", err)
	}

	// A grouped collection is not a graftable selector.
	groupedProg := `
a = workspace.world("` + worldA.ID + `")
sa = a.open()
workspace.transfer(sa.bibites.group_by("species_id"), dst="` + worldB.ID + `")
`
	_, gErr := runAuto(ctx, ws, groupedProg)
	if gErr == nil {
		t.Fatalf("grouped selector: want loud error, got nil")
	}
	if !strings.Contains(strings.ToLower(gErr.Error()), "collection or a single") {
		t.Fatalf("grouped selector error = %q, want 'collection or a single'", gErr.Error())
	}

	// A bare string is not a selector either.
	strProg := `workspace.transfer("not-a-selection", dst="` + worldB.ID + `")`
	_, sErr := runAuto(ctx, ws, strProg)
	if sErr == nil {
		t.Fatalf("string selector: want loud error, got nil")
	}
	if !strings.Contains(strings.ToLower(sErr.Error()), "collection or a single") {
		t.Fatalf("string selector error = %q, want 'collection or a single'", sErr.Error())
	}
}

// ---------------------------------------------------------------------------
// TestTransferAllOrNothingOnBadEntry — a graft loop that fails midway leaves the
// dst head unmoved and zero committed (all-or-nothing at the commit boundary).
// One valid entry name + one missing entry name → TransferEntries fails on the
// missing one AFTER staging the valid one, and the bad transfer never commits.
// ---------------------------------------------------------------------------

func TestTransferAllOrNothingOnBadEntry(t *testing.T) {
	ctx := testCtxAuto(t)
	ws := newWorkspace(t, ctx)

	tmp := t.TempDir()
	srcPath := tmp + "/aon_src.zip"
	dstPath := tmp + "/aon_dst.zip"
	writeTransferSource(t, srcPath, 5, 5.5)
	writeTransferDest(t, dstPath)

	worldSrc, err := ws.AddWorld(ctx, srcPath, "aon-src")
	if err != nil {
		t.Fatalf("AddWorld(src): %v", err)
	}
	worldDst, err := ws.AddWorld(ctx, dstPath, "aon-dst")
	if err != nil {
		t.Fatalf("AddWorld(dst): %v", err)
	}

	srcLS, err := ws.OpenWorld(ctx, worldSrc.ID)
	if err != nil {
		t.Fatalf("OpenWorld(src): %v", err)
	}
	headDstBefore := headRevision(t, ctx, ws, worldDst.ID)

	// First a valid entry that stages, then a missing one that fails the loop after
	// a partial stage. The whole transfer must fail and commit nothing.
	names := []string{"bibites/bibite_0.bb8", "bibites/does_not_exist.bb8"}
	_, err = ws.Transfer(ctx, srcLS, worldSrc.ID, names, worldDst.ID, TransferOptions{})
	if err == nil {
		t.Fatalf("Transfer with a bad entry: want loud error, got nil")
	}
	if !strings.Contains(err.Error(), "does_not_exist") {
		t.Fatalf("Transfer error = %q, want it to name the missing entry", err.Error())
	}

	// The dst head must NOT have advanced (nothing committed).
	headDstAfter := headRevision(t, ctx, ws, worldDst.ID)
	if headDstAfter.ID != headDstBefore.ID {
		t.Fatalf("dst head advanced on a failed transfer: %d -> %d", headDstBefore.ID, headDstAfter.ID)
	}

	// The dst working bibite count is unchanged (no partial graft leaked into the
	// committed projection).
	dstCount := countBibitesWorking(t, ctx, ws, worldDst.ID)
	if dstCount != 3 {
		t.Fatalf("dst bibite count = %d, want 3 (no partial graft committed)", dstCount)
	}
}

// ---------------------------------------------------------------------------
// TestAutomation_TransferMoveDeletesFromSource — move semantics: after the dst
// graft commits, the grafted entries are deleted from the SOURCE world too, so the
// entity ends up in exactly one world. Both heads advance; the result reports
// moved==True and source_committed==True.
// ---------------------------------------------------------------------------

func TestAutomation_TransferMoveDeletesFromSource(t *testing.T) {
	ctx := testCtxAuto(t)
	ws := newWorkspace(t, ctx)

	tmp := t.TempDir()
	srcPath := tmp + "/move_src.zip"
	dstPath := tmp + "/move_dst.zip"
	const srcBibites = 3
	writeTransferSourceMulti(t, srcPath, srcBibites, 9)
	writeTransferDest(t, dstPath)

	worldA, err := ws.AddWorld(ctx, srcPath, "move-src")
	if err != nil {
		t.Fatalf("AddWorld(src): %v", err)
	}
	worldB, err := ws.AddWorld(ctx, dstPath, "move-dst")
	if err != nil {
		t.Fatalf("AddWorld(dst): %v", err)
	}

	headABefore := headRevision(t, ctx, ws, worldA.ID)
	headBBefore := headRevision(t, ctx, ws, worldB.ID)
	aBefore := countBibitesWorking(t, ctx, ws, worldA.ID)
	bBefore := countBibitesWorking(t, ctx, ws, worldB.ID)

	prog := `
a = workspace.world("` + worldA.ID + `")
sa = a.open()
r = workspace.transfer(sa.bibites.where("energy >= 0"), dst="` + worldB.ID + `", move=True)
print(r["committed"])
print(r["transferred"])
print(r["moved"])
print(r["source_committed"])
`
	res := mustRunAuto(t, ctx, ws, prog)
	lines := strings.Split(strings.TrimRight(res.Output, "\n"), "\n")
	if len(lines) < 4 {
		t.Fatalf("expected >=4 output lines, got %v\nOutput:\n%s", lines, res.Output)
	}
	if lines[0] != "True" {
		t.Fatalf("committed = %q, want True\nOutput:\n%s", lines[0], res.Output)
	}
	if lines[1] != itoa(srcBibites) {
		t.Fatalf("transferred = %q, want %d", lines[1], srcBibites)
	}
	if lines[2] != "True" {
		t.Fatalf("moved = %q, want True", lines[2])
	}
	if lines[3] != "True" {
		t.Fatalf("source_committed = %q, want True", lines[3])
	}

	// DST grew by N; SOURCE shrank by N (the entity moved, not copied).
	bAfter := countBibitesWorking(t, ctx, ws, worldB.ID)
	if bAfter != bBefore+int64(srcBibites) {
		t.Fatalf("dst bibite count = %d, want %d (prior %d + moved %d)", bAfter, bBefore+int64(srcBibites), bBefore, srcBibites)
	}
	aAfter := countBibitesWorking(t, ctx, ws, worldA.ID)
	if aAfter != aBefore-int64(srcBibites) {
		t.Fatalf("src bibite count = %d, want %d (prior %d - moved %d); move did not delete from source", aAfter, aBefore-int64(srcBibites), aBefore, srcBibites)
	}

	// Both heads advanced: dst (graft commit) AND src (source-delete commit).
	headAAfter := headRevision(t, ctx, ws, worldA.ID)
	if headAAfter.ID == headABefore.ID {
		t.Fatalf("src head did not advance on move: still %d (source delete not committed)", headABefore.ID)
	}
	headBAfter := headRevision(t, ctx, ws, worldB.ID)
	if headBAfter.ID == headBBefore.ID {
		t.Fatalf("dst head did not advance on move: still %d", headBBefore.ID)
	}
}

// ---------------------------------------------------------------------------
// TestAutomation_TransferCopyLeavesSourceIntact — the default (no move) is a copy:
// the source is unchanged and the result reports moved==False.
// ---------------------------------------------------------------------------

func TestAutomation_TransferCopyLeavesSourceIntact(t *testing.T) {
	ctx := testCtxAuto(t)
	ws := newWorkspace(t, ctx)

	tmp := t.TempDir()
	srcPath := tmp + "/copy_src.zip"
	dstPath := tmp + "/copy_dst.zip"
	const srcBibites = 3
	writeTransferSourceMulti(t, srcPath, srcBibites, 9)
	writeTransferDest(t, dstPath)

	worldA, err := ws.AddWorld(ctx, srcPath, "copy-src")
	if err != nil {
		t.Fatalf("AddWorld(src): %v", err)
	}
	worldB, err := ws.AddWorld(ctx, dstPath, "copy-dst")
	if err != nil {
		t.Fatalf("AddWorld(dst): %v", err)
	}

	headABefore := headRevision(t, ctx, ws, worldA.ID)
	aBefore := countBibitesWorking(t, ctx, ws, worldA.ID)

	prog := `
a = workspace.world("` + worldA.ID + `")
sa = a.open()
r = workspace.transfer(sa.bibites.where("energy >= 0"), dst="` + worldB.ID + `")
print(r["moved"])
print(r["source_committed"])
`
	res := mustRunAuto(t, ctx, ws, prog)
	lines := strings.Split(strings.TrimRight(res.Output, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected >=2 output lines, got %v\nOutput:\n%s", lines, res.Output)
	}
	if lines[0] != "False" {
		t.Fatalf("moved = %q, want False (default copy)", lines[0])
	}
	if lines[1] != "False" {
		t.Fatalf("source_committed = %q, want False (default copy)", lines[1])
	}

	// The source is unchanged: count steady AND head not advanced.
	aAfter := countBibitesWorking(t, ctx, ws, worldA.ID)
	if aAfter != aBefore {
		t.Fatalf("src bibite count changed on copy: %d -> %d (copy must not delete from source)", aBefore, aAfter)
	}
	headAAfter := headRevision(t, ctx, ws, worldA.ID)
	if headAAfter.ID != headABefore.ID {
		t.Fatalf("src head advanced on a copy: %d -> %d", headABefore.ID, headAAfter.ID)
	}
}

// ---------------------------------------------------------------------------
// TestAutomation_TransferRemapIDsAcrossPopulatedDst — a source bibite whose body.id
// collides with a dest bibite. remap_ids=True succeeds end-to-end (dst grows);
// the default (remap_ids=False) fails loudly.
// ---------------------------------------------------------------------------

func TestAutomation_TransferRemapIDsAcrossPopulatedDst(t *testing.T) {
	ctx := testCtxAuto(t)
	ws := newWorkspace(t, ctx)

	tmp := t.TempDir()
	srcPath := tmp + "/remap_src.zip"
	dstPath := tmp + "/remap_dst.zip"
	// A source whose single bibite carries body.id 42 — colliding with the dest
	// fixture's bibite_0 (body 42). Species 9 with a matching record so the species
	// axis grafts cleanly and the body.id collision is the only obstacle.
	writeCollidingBodyIDSource(t, srcPath, 42, 9)
	writeTransferDest(t, dstPath)

	worldA, err := ws.AddWorld(ctx, srcPath, "remap-src")
	if err != nil {
		t.Fatalf("AddWorld(src): %v", err)
	}
	worldB, err := ws.AddWorld(ctx, dstPath, "remap-dst")
	if err != nil {
		t.Fatalf("AddWorld(dst): %v", err)
	}

	bBefore := countBibitesWorking(t, ctx, ws, worldB.ID)

	// Default (remap_ids=False): the body.id collision is a loud failure.
	loudProg := `
a = workspace.world("` + worldA.ID + `")
sa = a.open()
workspace.transfer(sa.bibites.where("energy >= 0"), dst="` + worldB.ID + `")
`
	if _, err := runAuto(ctx, ws, loudProg); err == nil {
		t.Fatalf("default transfer with a colliding body.id: want loud failure, got nil")
	}
	// The failed default must not have grown the dst.
	if got := countBibitesWorking(t, ctx, ws, worldB.ID); got != bBefore {
		t.Fatalf("dst bibite count = %d after failed default transfer, want %d (no partial graft)", got, bBefore)
	}

	// remap_ids=True: the colliding body.id is remapped and the graft commits.
	remapProg := `
a = workspace.world("` + worldA.ID + `")
sa = a.open()
r = workspace.transfer(sa.bibites.where("energy >= 0"), dst="` + worldB.ID + `", remap_ids=True)
print(r["committed"])
print(r["transferred"])
`
	res := mustRunAuto(t, ctx, ws, remapProg)
	lines := strings.Split(strings.TrimRight(res.Output, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected >=2 output lines, got %v\nOutput:\n%s", lines, res.Output)
	}
	if lines[0] != "True" {
		t.Fatalf("remap_ids committed = %q, want True\nOutput:\n%s", lines[0], res.Output)
	}
	if lines[1] != "1" {
		t.Fatalf("remap_ids transferred = %q, want 1", lines[1])
	}
	if got := countBibitesWorking(t, ctx, ws, worldB.ID); got != bBefore+1 {
		t.Fatalf("dst bibite count = %d after remap transfer, want %d (grew by 1)", got, bBefore+1)
	}
}

// ---------------------------------------------------------------------------
// TestAutomation_TransferMoveCommitsDstBeforeSource — the data-safety ordering
// invariant in the happy path: the dst graft commits FIRST and the source delete
// commits SECOND, so the only failure window leaves a recoverable duplicate (never
// data loss). We assert the ordering via revision ids on a single workspace store:
// RecordRevisionAdvancingHead allocates ids monotonically, so the dst move revision
// must have a SMALLER id than the source-delete revision.
// ---------------------------------------------------------------------------

func TestAutomation_TransferMoveCommitsDstBeforeSource(t *testing.T) {
	ctx := testCtxAuto(t)
	ws := newWorkspace(t, ctx)

	tmp := t.TempDir()
	srcPath := tmp + "/order_src.zip"
	dstPath := tmp + "/order_dst.zip"
	writeTransferSourceMulti(t, srcPath, 2, 9)
	writeTransferDest(t, dstPath)

	worldA, err := ws.AddWorld(ctx, srcPath, "order-src")
	if err != nil {
		t.Fatalf("AddWorld(src): %v", err)
	}
	worldB, err := ws.AddWorld(ctx, dstPath, "order-dst")
	if err != nil {
		t.Fatalf("AddWorld(dst): %v", err)
	}

	srcLS, err := ws.OpenWorld(ctx, worldA.ID)
	if err != nil {
		t.Fatalf("OpenWorld(src): %v", err)
	}
	coll, err := selectAllBibites(srcLS)
	if err != nil {
		t.Fatalf("select source bibites: %v", err)
	}

	result, err := ws.Transfer(ctx, srcLS, worldA.ID, coll, worldB.ID, TransferOptions{Move: true})
	if err != nil {
		t.Fatalf("Transfer(move): %v", err)
	}
	if !result.Moved || !result.SourceCommitted {
		t.Fatalf("move result moved=%t source_committed=%t, want both true", result.Moved, result.SourceCommitted)
	}
	// The DST commit must precede the SOURCE-delete commit: dst-first ordering means
	// the dst revision was recorded before the source revision, so its id is smaller.
	if result.DstRevision.ID == 0 {
		t.Fatalf("dst revision id is 0, want a real commit")
	}
	if result.SourceRevision.ID == 0 {
		t.Fatalf("source revision id is 0, want a real source-delete commit")
	}
	if !(result.DstRevision.ID < result.SourceRevision.ID) {
		t.Fatalf("dst revision id %d is not < source revision id %d; the dst commit must happen BEFORE the source delete (data-safety ordering)", result.DstRevision.ID, result.SourceRevision.ID)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// writeCollidingBodyIDSource writes a SOURCE save with a single bibite whose
// body.id is bodyID (chosen to collide with a dest bibite) carrying species sid
// with a matching recordedSpecies record, so the cross-world graft's only obstacle
// is the body.id collision.
func writeCollidingBodyIDSource(t *testing.T, path string, bodyID, sid int64) {
	t.Helper()
	speciesData := `{"nextSpeciesID":` + itoa(sid+1) + `,"activeSpeciesList":[` + itoa(sid) +
		`],"recordedSpecies":[{"speciesID":` + itoa(sid) + `,"parentID":0,"name":"src-species","template":{"genes":{"SizeRatio":9.9}}}]}`
	archive := &tb.Archive{
		Entries: []tb.Entry{
			{Index: 0, Name: "scene.bb8scene", Kind: tb.EntryScene, Method: zip.Deflate, Raw: withBOM(`{"nBibites":1}`)},
			{Index: 1, Name: "settings.bb8settings", Kind: tb.EntrySettings, Method: zip.Deflate, Raw: withBOM(`{"worldLabel":{"Value":"source-world"},"zones":[],"zoneGroups":[],"bibites":[],"settingsChangers":[]}`)},
			{Index: 2, Name: "speciesData.json", Kind: tb.EntrySpecies, Method: zip.Deflate, Raw: withBOM(speciesData)},
			{Index: 3, Name: "bibites/bibite_0.bb8", Kind: tb.EntryBibite, Method: zip.Deflate, Raw: withBOM(`{"body":{"id":` + itoa(bodyID) + `,"energy":33.0},"genes":{"speciesID":` + itoa(sid) + `,"gen":1},"brain":{"Nodes":[],"Synapses":[]}}`)},
		},
	}
	if err := tb.WriteArchive(path, archive); err != nil {
		t.Fatalf("WriteArchive(colliding body.id source) error = %v", err)
	}
}

// countBibitesWorking returns the working-partition (save_id == worldID) bibite
// row count for a world, reading the shared DuckDB mirror directly.
func countBibitesWorking(t *testing.T, ctx context.Context, ws *Workspace, worldID string) int64 {
	t.Helper()
	return countBySaveID(t, ctx, ws, "bibites", worldID)
}

// selectAllBibites resolves the source bibites collection's entry names through
// the exported object-DSL accessor (the same selection path the script binding
// uses), so the Go-level transfer test grafts via the object DSL, never SQL.
func selectAllBibites(ls *thebibites.LoadedSave) ([]string, error) {
	coll := thebibites.NewSaveValue(ls)
	bibitesAttr, err := coll.Attr("bibites")
	if err != nil {
		return nil, err
	}
	ec, ok := bibitesAttr.(*thebibites.EntityCollection)
	if !ok {
		return nil, errNotCollection
	}
	return ec.EntryNames()
}

var errNotCollection = &transferTestError{"save.bibites did not resolve to an EntityCollection"}

type transferTestError struct{ msg string }

func (e *transferTestError) Error() string { return e.msg }

// bibiteSpeciesID reads genes.speciesID off a parsed bibite entry's JSON.
func bibiteSpeciesID(t *testing.T, archive *tb.Archive, entryName string) int64 {
	t.Helper()
	entry := archive.Entry(entryName)
	if entry == nil {
		t.Fatalf("entry %q not found in committed archive", entryName)
	}
	root, ok := entry.JSON.(map[string]any)
	if !ok {
		t.Fatalf("entry %q has no JSON object", entryName)
	}
	genes, ok := root["genes"].(map[string]any)
	if !ok {
		t.Fatalf("entry %q has no genes object", entryName)
	}
	return jsonToInt64(t, genes["speciesID"])
}

// speciesMarker reads the SizeRatio template marker for the recordedSpecies
// record with the given speciesID, returning (marker, found).
func speciesMarker(t *testing.T, archive *tb.Archive, sid int64) (float64, bool) {
	t.Helper()
	entry := archive.Entry("speciesData.json")
	if entry == nil {
		t.Fatalf("speciesData.json not found in committed archive")
	}
	root, ok := entry.JSON.(map[string]any)
	if !ok {
		t.Fatalf("speciesData.json has no JSON object")
	}
	records, ok := root["recordedSpecies"].([]any)
	if !ok {
		t.Fatalf("speciesData.json has no recordedSpecies array")
	}
	for _, r := range records {
		rec, ok := r.(map[string]any)
		if !ok {
			continue
		}
		if jsonToInt64(t, rec["speciesID"]) != sid {
			continue
		}
		template, ok := rec["template"].(map[string]any)
		if !ok {
			return 0, false
		}
		genes, ok := template["genes"].(map[string]any)
		if !ok {
			return 0, false
		}
		f, ok := jsonToFloat64(genes["SizeRatio"])
		if !ok {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

// jsonToFloat64 coerces a parsed JSON number (json.Number under UseNumber, or a
// plain float64) to float64.
func jsonToFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}

func jsonToInt64(t *testing.T, v any) int64 {
	t.Helper()
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			t.Fatalf("json.Number %q not an int: %v", n, err)
		}
		return i
	default:
		t.Fatalf("value %v (%T) is not numeric", v, v)
		return 0
	}
}

func atoiOrFatal(t *testing.T, s string) int {
	t.Helper()
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			t.Fatalf("not a non-negative integer: %q", s)
		}
		n = n*10 + int(c-'0')
	}
	return n
}
