package thebibites

// transfer_identity.go isolates the high-risk identity/species reconciliation for
// the whole-entry graft (transfer.AppendEntry). The headline corruption risk is
// silently grafting an entry whose body.id collides with a destination bibite, or
// whose genes.speciesID cannot be linked into the destination without a remap.
// The F1 policy here is conservative and loud: REJECT every case that is not
// trivially safe, naming the offending id/field in the error.
//
// Species handling is FAIL-LOUD. speciesID is a per-world LINEAR id space, so a
// destination independently reuses the same small ids for biologically different
// species. Any grafted entity that carries a genes.speciesID is therefore refused:
//   - the id is already present in the destination -> we cannot prove it is the
//     same species, so linking would conflate distinct species (the conflation
//     risk); refuse.
//   - the id is absent from the destination -> safely linking it would require
//     fabricating/importing a species record; refuse.
// F1 NEVER adds to activeSpeciesList, never fabricates a recordedSpecies record,
// and never adopts the destination's coincidental same-id species. These refusals
// are conservative ON PURPOSE: cross-world species REMAP (allocate a fresh dest
// species id, import the source species record, rewrite refs) is ticket F3, which
// lifts the restriction. Do not "fix" these refusals back into an add. An entity
// that carries no genes.speciesID has nothing to link and grafts cleanly.

import (
	"fmt"
	"strconv"
	"strings"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

// reconcileGraftIdentity validates the identity of a grafted bibite/egg JSON
// value against the destination archive. It is a pure CHECK with no staging, so
// it must be called BEFORE any StageAppendBibite: a rejected graft then leaves
// the destination with 0 staged ops by construction (no Apply-atomicity unwind).
//
// The guards it enforces:
//   - body.id collision (bibites): rejected loudly (F1 does not remap ids).
//   - parent/child links (bibites): a grafted body.eggLayer.children entry that
//     references an id not present in the destination is a dangling cross-world
//     link; rejected loudly (F1 does not strip or remap children).
//   - species (bibites and eggs): any genes.speciesID is refused loudly because
//     linking it without remap is unsafe (see the file header). The only
//     non-refusing species path is an entity that carries no genes.speciesID.
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

	// Species check applies to both bibites and eggs (both carry genes.speciesID).
	if kind == tb.EntryBibite || kind == tb.EntryEgg {
		if err := t.refuseSpeciesGraft(value); err != nil {
			return err
		}
	}
	return nil
}

// refuseSpeciesGraft refuses any graft whose entity carries a genes.speciesID,
// with a distinct named reason for the two sub-cases (already-present conflation
// vs absent-needs-import). It returns nil only when the entity carries no species
// id (nothing to link). It NEVER stages anything: this is the F1 fail-loud rule,
// lifted by F3's remap. See the file header for why a per-world linear id cannot
// be safely linked here.
func (t *transfer) refuseSpeciesGraft(value any) error {
	sid, ok := entitySpeciesID(value)
	if !ok {
		// No species id to link; the graft proceeds.
		return nil
	}

	entry := t.dst.Archive().Entry(SpeciesEntryName)
	if entry == nil || entry.JSON == nil {
		// The destination has no decoded species table, so we cannot prove the id
		// links to anything. We refuse rather than silently graft a
		// species-bearing entity into a save with no usable species table; a remap
		// would have to import the record (F3).
		return fmt.Errorf("transfer: append entry: grafted genes.speciesID %d cannot be linked: the destination has no decoded species table to link or import into - cross-world species remap is F3", sid)
	}

	if activeSpeciesIndexOf(entry.JSON, sid) >= 0 || t.dstEntityHasSpecies(sid) {
		// Present in the destination. speciesID is a per-world linear id, so we
		// cannot prove the grafted entity shares the destination's same-id
		// species; adopting it would conflate biologically distinct species.
		return fmt.Errorf("transfer: append entry: grafted genes.speciesID %d already exists in the destination; cannot prove it is the same species (speciesID is a per-world linear id), so linking would conflate distinct species - cross-world species remap is F3", sid)
	}

	// Absent from the destination: safely linking it would require fabricating /
	// importing a species record into speciesData.json (F3).
	return fmt.Errorf("transfer: append entry: grafted genes.speciesID %d is absent from the destination; safely linking it requires importing the source species record (cross-world species remap is F3)", sid)
}

// dstEntityHasSpecies reports whether any destination bibite or egg entry carries
// genes.speciesID == sid. This treats an id used by a live destination entity as
// "present" even if it is not in activeSpeciesList, so the conflation guard fires
// on any coincidental same-linear-id member.
func (t *transfer) dstEntityHasSpecies(sid int64) bool {
	entries := t.dst.Archive().Entries
	for i := range entries {
		entry := &entries[i]
		if entry.Kind != tb.EntryBibite && entry.Kind != tb.EntryEgg {
			continue
		}
		if entry.JSON == nil {
			continue
		}
		if other, ok := entitySpeciesID(entry.JSON); ok && other == sid {
			return true
		}
	}
	return false
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
// dst: bibites/bibite_<n>.bb8 or eggs/egg_<n>.bb8 where n = 1 + the max existing
// numeric index of that kind (or 0 when none exist). The result matches the
// parser's bibiteEntryRE/eggEntryRE so applyAppendEntry's kind classification
// accepts it.
func nextEntryName(dst *tb.Archive, kind tb.EntryKind) (string, error) {
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
	for i := range dst.Entries {
		entry := &dst.Entries[i]
		if entry.Kind != kind {
			continue
		}
		idx, ok := entryIndexToken(entry.Name, prefix, suffix)
		if !ok {
			continue
		}
		if idx > max {
			max = idx
		}
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
