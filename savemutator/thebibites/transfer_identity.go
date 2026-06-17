package thebibites

// transfer_identity.go isolates the high-risk identity/species reconciliation for
// the whole-entry graft (transfer.AppendEntry). The headline corruption risk is
// silently grafting an entry whose body.id collides with a destination bibite, or
// whose genes.speciesID is absent from the destination's activeSpeciesList. The
// v1 policy here is conservative and loud: reconcile the cases that are trivially
// safe (add a missing active species) and REJECT the cases that are not (body.id
// collision, dangling parent/child links), naming the offending id/field in the
// error. Species dedup/merge (treating two different ids as "the same species")
// is explicitly out of scope and never guessed.

import (
	"fmt"
	"strconv"
	"strings"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

// reconcileGraftIdentity validates and reconciles the identity of a grafted
// bibite/egg JSON value against the destination archive. It must be called
// BEFORE staging so a rejected graft leaves the destination unstaged.
//
// The reconciliation it performs:
//   - body.id collision (bibites): rejected loudly (v1 does not remap ids).
//   - parent/child links (bibites): a grafted body.eggLayer.children entry that
//     references an id not present in the destination is a dangling cross-world
//     link; rejected loudly (v1 does not strip or remap children).
//   - species linkage: if genes.speciesID is not already in the destination's
//     activeSpeciesList, stage adding it (the inverse of removeActiveSpecies).
//     This is the trivially-safe add; it never merges or dedups species.
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

	// Species linkage applies to both bibites and eggs (both carry
	// genes.speciesID). Add the species to the destination's activeSpeciesList if
	// missing; never merge or dedup.
	if kind == tb.EntryBibite || kind == tb.EntryEgg {
		if sid, ok := entitySpeciesID(value); ok {
			if err := t.ensureActiveSpecies(sid); err != nil {
				return err
			}
		}
	}
	return nil
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

// ensureActiveSpecies stages adding sid to the destination's
// speciesData.json activeSpeciesList when it is missing. It mirrors the inverse
// of removeActiveSpecies (session.go): a no-op when the species entry is absent
// or undecoded, or when sid is already active. The add is staged as a normal
// append on the species entry so it commits atomically with the graft.
func (t *transfer) ensureActiveSpecies(sid int64) error {
	entry := t.dst.Archive().Entry(SpeciesEntryName)
	if entry == nil {
		// No species entry to reconcile; the graft proceeds without species linkage.
		return nil
	}
	if entry.JSON == nil {
		// A present-but-undecoded species entry has no activeSpeciesList to touch.
		return nil
	}
	if activeSpeciesIndexOf(entry.JSON, sid) >= 0 {
		return nil
	}
	// activeSpeciesList may be absent (e.g. an empty species entry); only append
	// when the array exists so we never fabricate a structure the game did not
	// write.
	if _, ok, err := getJSONPath(entry.JSON, "activeSpeciesList"); err != nil || !ok {
		return fmt.Errorf("transfer: append entry: destination species entry has no activeSpeciesList to register species %d", sid)
	}
	return t.dst.StageAppend(SpeciesTarget(), "activeSpeciesList", sid)
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
