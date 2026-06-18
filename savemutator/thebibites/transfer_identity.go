package thebibites

// transfer_identity.go isolates the high-risk identity/species reconciliation for
// the whole-entry graft (transfer.AppendEntry). The headline corruption risk is
// silently grafting an entry whose body.id collides with a destination bibite, or
// whose genes.speciesID conflates the grafted entity into the destination's
// coincidental same-id species.
//
// body.id collisions and dangling parent/child links stay FAIL-LOUD: F3 does not
// remap entity ids or cross-world child references, so reconcileGraftIdentity still
// refuses those cases, naming the offending id, BEFORE anything is staged.
//
// Species handling is now a cross-world REMAP (this is what F3 adds, replacing F1's
// loud species refusal). speciesID is a per-world LINEAR id space, so a destination
// independently reuses the same small ids for biologically different species. We
// therefore never reuse the source id and never adopt the destination's
// coincidental same-id species. Instead AppendEntry (transfer.go) allocates a FRESH
// dest species id that beats every id already in use (activeSpeciesList,
// recordedSpecies records, and every live dest entity's genes.speciesID), imports
// the source species RECORD into the dest table under that fresh id, and rewrites
// genes.speciesID on the grafted bibite/egg. The allocator and the conflation
// invariant live here (freshDstSpeciesID / dstSpeciesIDUsage); the staging lives in
// transfer.go. An entity that carries no genes.speciesID has nothing to remap and
// grafts cleanly.

import (
	"fmt"
	"strconv"
	"strings"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

// reconcileGraftIdentity validates the NON-species identity of a grafted
// bibite/egg JSON value against the destination archive. It is a pure CHECK with
// no staging, so it must be called BEFORE any StageAppendBibite: a rejected graft
// then leaves the destination with 0 staged ops by construction (no
// Apply-atomicity unwind).
//
// The guards it enforces (species is handled separately by the remap path in
// transfer.AppendEntry, NOT here):
//   - body.id collision (bibites): rejected loudly (F3 does not remap entity ids).
//   - parent/child links (bibites): a grafted body.eggLayer.children entry that
//     references an id not present in the destination is a dangling cross-world
//     link; rejected loudly (F3 does not strip or remap children).
func (t *transfer) reconcileGraftIdentity(kind tb.EntryKind, value any) error {
	if kind == tb.EntryBibite {
		if id, ok := bibiteBodyID(value); ok {
			if name, collides := t.dstBibiteWithBodyID(id); collides {
				return fmt.Errorf("transfer: append entry: grafted body.id %d already exists in destination entry %q; cross-world id remap is not supported", id, name)
			}
		}
		if dangling, ok := t.danglingChildRefs(value); ok {
			return fmt.Errorf("transfer: append entry: grafted body.eggLayer.children references id(s) %v absent from destination; cross-world child links are not supported", dangling)
		}
	}
	return nil
}

// freshDstSpeciesID allocates a species id for the destination that collides with
// NOTHING in use. This is the load-bearing conflation guard: speciesID is a
// per-world linear id, so reusing the source id (or trusting only one counter)
// would conflate the graft into the dest's coincidental same-id species. The fresh
// id beats the max over EVERY id space at once:
//   - speciesData.json#activeSpeciesList,
//   - speciesData.json#recordedSpecies[*].speciesID (record-only ids the active
//     list can omit),
//   - every live dest bibite/egg's genes.speciesID, and
//   - nextSpeciesID, the engine's monotonic counter.
// Using max(...) rather than nextSpeciesID alone is what defends against a stale
// counter (a save whose nextSpeciesID is behind an in-use id). The destination
// MUST have a decoded species table or we fail loudly: there is nowhere to land
// the imported record otherwise.
func (t *transfer) freshDstSpeciesID() (int64, error) {
	entry := t.dst.Archive().Entry(SpeciesEntryName)
	if entry == nil || entry.JSON == nil {
		return 0, fmt.Errorf("transfer: append entry: cannot remap species: the destination has no decoded species table (speciesData.json) to import a record into")
	}

	maxUsed, hasUsed := t.dstSpeciesIDUsage()

	fresh := int64(0)
	if hasUsed {
		fresh = maxUsed + 1
	}
	if next, ok := jsonInt64Path(entry.JSON, "nextSpeciesID"); ok && next > fresh {
		fresh = next
	}
	return fresh, nil
}

// dstSpeciesIDUsage returns the max species id observed across the dest species
// table (activeSpeciesList and recordedSpecies) and every live dest bibite/egg
// entity. ok is false only when no species id is observed anywhere. The allocator
// and any conflation reasoning share this one traversal so they cannot drift.
func (t *transfer) dstSpeciesIDUsage() (max int64, ok bool) {
	consider := func(id int64) {
		if !ok || id > max {
			max, ok = id, true
		}
	}

	if entry := t.dst.Archive().Entry(SpeciesEntryName); entry != nil && entry.JSON != nil {
		for _, id := range jsonInt64Array(entry.JSON, "activeSpeciesList") {
			consider(id)
		}
		if records, present, err := getJSONPath(entry.JSON, "recordedSpecies"); err == nil && present {
			if list, isArray := records.([]any); isArray {
				for _, rec := range list {
					if id, found := jsonInt64Path(rec, "speciesID"); found {
						consider(id)
					}
				}
			}
		}
	}

	entries := t.dst.Archive().Entries
	for i := range entries {
		entry := &entries[i]
		if entry.Kind != tb.EntryBibite && entry.Kind != tb.EntryEgg {
			continue
		}
		if entry.JSON == nil {
			continue
		}
		if id, found := entitySpeciesID(entry.JSON); found {
			consider(id)
		}
	}

	// Species ids already STAGED for import this session (earlier grafts in a
	// multi-entry transfer loop) are not yet reflected in the dst archive JSON —
	// staged appends/sets apply only at Apply time. Consider the activeSpeciesList
	// appends staged on the dst session so each graft in the loop allocates a
	// DISTINCT fresh id; without this, every graft re-reads the same pre-Apply state
	// and conflates multiple distinct source species under one dest id.
	for _, op := range t.dst.StagedOperations() {
		if op.Kind != OperationAppend || op.Target.EntryName != SpeciesEntryName || op.Path != "activeSpeciesList" {
			continue
		}
		if id, found := jsonNumberToInt64(op.Value); found {
			consider(id)
		}
	}
	return max, ok
}

// sourceSpeciesRecord locates the source species RECORD for sid in the source
// archive's speciesData.json#recordedSpecies and returns a DEEP COPY of it (never
// an alias into source bytes). ok is false when the source has no decoded species
// table or no record with that id. Importing the genome/metadata is the point of
// the remap, so AppendEntry treats a missing record as a loud failure rather than
// fabricating a backing-less active id.
func sourceSpeciesRecord(srcArchive *tb.Archive, sid int64) (any, bool) {
	entry := srcArchive.Entry(SpeciesEntryName)
	if entry == nil || entry.JSON == nil {
		return nil, false
	}
	records, ok, err := getJSONPath(entry.JSON, "recordedSpecies")
	if err != nil || !ok {
		return nil, false
	}
	list, ok := records.([]any)
	if !ok {
		return nil, false
	}
	for _, rec := range list {
		if id, found := jsonInt64Path(rec, "speciesID"); found && id == sid {
			return cloneJSON(rec), true
		}
	}
	return nil, false
}

// jsonInt64Path reads an integer at path from root, returning ok=false when the
// path is absent or not an integer.
func jsonInt64Path(root any, path string) (int64, bool) {
	value, ok, err := getJSONPath(root, path)
	if err != nil || !ok {
		return 0, false
	}
	return jsonNumberToInt64(value)
}

// jsonInt64Array reads the integer elements of the JSON array at path, skipping
// any non-integer element. A missing path yields an empty slice.
func jsonInt64Array(root any, path string) []int64 {
	value, ok, err := getJSONPath(root, path)
	if err != nil || !ok {
		return nil
	}
	list, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]int64, 0, len(list))
	for _, elem := range list {
		if id, ok := jsonNumberToInt64(elem); ok {
			out = append(out, id)
		}
	}
	return out
}

// dstBibiteWithBodyID returns the name of a destination bibite entry whose
// body.id equals id, reusing the bibiteBodyID reader. ok is false when no
// destination bibite carries id.
func (t *transfer) dstBibiteWithBodyID(id int64) (string, bool) {
	entries := t.dst.Archive().Entries
	for i := range entries {
		entry := &entries[i]
		if entry.Kind != tb.EntryBibite || entry.JSON == nil {
			continue
		}
		if other, ok := bibiteBodyID(entry.JSON); ok && other == id {
			return entry.Name, true
		}
	}
	return "", false
}

// danglingChildRefs reports any body.eggLayer.children ids in value that do not
// correspond to a bibite present in the destination. A grafted child link that
// points at a source-world id with no destination counterpart is a dangling
// reference. ok is true when at least one dangling id is found.
func (t *transfer) danglingChildRefs(value any) ([]int64, bool) {
	children, ok, err := getJSONPath(value, "body.eggLayer.children")
	if err != nil || !ok {
		return nil, false
	}
	ids, ok := children.([]any)
	if !ok || len(ids) == 0 {
		return nil, false
	}
	present := t.dstBibiteBodyIDs()
	var dangling []int64
	for _, child := range ids {
		id, ok := jsonNumberToInt64(child)
		if !ok {
			continue
		}
		if _, found := present[id]; !found {
			dangling = append(dangling, id)
		}
	}
	return dangling, len(dangling) > 0
}

// dstBibiteBodyIDs collects the set of body.id values present among destination
// bibite entries.
func (t *transfer) dstBibiteBodyIDs() map[int64]struct{} {
	out := make(map[int64]struct{})
	entries := t.dst.Archive().Entries
	for i := range entries {
		entry := &entries[i]
		if entry.Kind != tb.EntryBibite || entry.JSON == nil {
			continue
		}
		if id, ok := bibiteBodyID(entry.JSON); ok {
			out[id] = struct{}{}
		}
	}
	return out
}

// nextEntryName allocates a fresh, non-colliding archive entry name for kind in
// dst: bibites/bibite_<n>.bb8 or eggs/egg_<n>.bb8 where n = 1 + the max numeric
// index of that kind observed across dst.Entries AND any reserved names (entry
// names already staged for append this session but not yet in dst.Entries — see
// transfer.AppendEntry). n is 0 when none exist anywhere. The result matches the
// parser's bibiteEntryRE/eggEntryRE so applyAppendEntry's kind classification
// accepts it.
func nextEntryName(dst *tb.Archive, kind tb.EntryKind, reserved ...string) (string, error) {
	var prefix, suffix string
	switch kind {
	case tb.EntryBibite:
		prefix, suffix = "bibites/bibite_", ".bb8"
	case tb.EntryEgg:
		prefix, suffix = "eggs/egg_", ".bb8"
	default:
		return "", fmt.Errorf("cannot allocate entry name for kind %q", kind)
	}

	max := int64(-1)
	consider := func(name string) {
		idx, ok := entryIndexToken(name, prefix, suffix)
		if ok && idx > max {
			max = idx
		}
	}
	for i := range dst.Entries {
		if dst.Entries[i].Kind != kind {
			continue
		}
		consider(dst.Entries[i].Name)
	}
	for _, name := range reserved {
		consider(name)
	}
	return fmt.Sprintf("%s%d%s", prefix, max+1, suffix), nil
}

// entryIndexToken extracts the numeric token n from an entry name of the form
// prefix + n + suffix. ok is false when name does not have that exact shape.
func entryIndexToken(name, prefix, suffix string) (int64, bool) {
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return 0, false
	}
	token := name[len(prefix) : len(name)-len(suffix)]
	if token == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(token, 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}
