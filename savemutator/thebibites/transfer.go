package thebibites

// transfer.go implements the cross-save coordinator that the Workspace interface
// (workspace.go) describes: it opens a source and a destination Session (each
// wrapping a parsed *tb.Archive), collects an element from the source, and stages
// it onto the destination Session. It is a pure-archive mechanism: it reads the
// source's parsed archive JSON directly and never runs a query, opens a database,
// touches a revision store, or commits/advances a world head. F1 stops at
// staging onto the destination Session; the caller (the workspace layer, F2)
// decides when to Commit and how to advance the head.
//
// Method-to-DSL-surface map (so F2 knows which call backs which binding):
//   - SetFromCollected → settings copy (a scalar set; the DSL "settings copy"
//     target). The Workspace interface does not cover a scalar set, so this is a
//     concrete helper on *transfer rather than an interface method.
//   - AppendArray      → array-element feed (synapses / brain nodes / stomach
//     contents / pellets / settings zones). Routes through StageSQLAppend, which
//     reuses the existing append resolvers and SceneCount reconciliation.
//   - AppendEntry      → whole bibite/egg graft. This reconciles identity
//     (body.id collision, dangling child refs stay loud) and REMAPS any
//     species-bearing graft across the per-world-linear speciesID space:
//     allocate a fresh non-colliding dest species id, import the source species
//     record, and rewrite genes.speciesID on the grafted entity (and its egg).
//     See transfer_identity.go for the allocator and the conflation invariant.

import (
	"fmt"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

// transfer is the concrete Workspace implementer: a coordinator over a source
// and a destination Session. It stages collected source elements onto dst and
// never commits.
type transfer struct {
	src *Session
	dst *Session
}

// compile-time assertion that *transfer satisfies the Workspace seam.
var _ Workspace = (*transfer)(nil)

// NewTransfer builds a cross-save coordinator over a source and a destination
// session. Both must be non-nil and wrap a decoded archive.
func NewTransfer(src, dst *Session) (*transfer, error) {
	if src == nil {
		return nil, fmt.Errorf("transfer: source session is nil")
	}
	if dst == nil {
		return nil, fmt.Errorf("transfer: destination session is nil")
	}
	if src.Archive() == nil {
		return nil, fmt.Errorf("transfer: source session has no archive")
	}
	if dst.Archive() == nil {
		return nil, fmt.Errorf("transfer: destination session has no archive")
	}
	return &transfer{src: src, dst: dst}, nil
}

// Source returns the session that elements are collected from.
func (t *transfer) Source() *Session { return t.src }

// Destination returns the session that collected elements are appended into.
func (t *transfer) Destination() *Session { return t.dst }

// CollectSettingsValue resolves ref against the SOURCE archive and reads the JSON
// value at the resolved path, packaging it as a CollectedElement. This is the
// simplest canonical target (the settings copy). It fails loudly if the source
// entry is missing, has no decoded JSON, or the value is absent at the path.
func (t *transfer) CollectSettingsValue(ref SQLValueRef) (CollectedElement, error) {
	target, path, err := ResolveSQLValueRef(ref)
	if err != nil {
		return CollectedElement{}, fmt.Errorf("transfer: collect settings value: %w", err)
	}
	entry := t.src.Archive().Entry(target.EntryName)
	if entry == nil {
		return CollectedElement{}, fmt.Errorf("transfer: collect settings value: source entry %q not found", target.EntryName)
	}
	if entry.JSON == nil {
		return CollectedElement{}, fmt.Errorf("transfer: collect settings value: source entry %q has no decoded JSON", target.EntryName)
	}
	value, ok, err := getJSONPath(entry.JSON, path)
	if err != nil {
		return CollectedElement{}, fmt.Errorf("transfer: collect settings value: source path %q: %w", path, err)
	}
	if !ok {
		return CollectedElement{}, fmt.Errorf("transfer: collect settings value: source path %q is missing", path)
	}
	return CollectedElement{
		SourcePath: target.EntryName,
		Table:      ref.Table,
		JSON:       cloneJSON(value),
	}, nil
}

// CollectArrayElement resolves ref against the SOURCE archive (an array-element
// ref such as a brain node, synapse, stomach content, or pellet) and reads the
// whole element JSON, packaging it as a CollectedElement suitable for
// AppendArray. The element path is taken from the delete resolver (which
// addresses the whole element, e.g. brain.Synapses[3]) rather than the set
// resolver (which addresses one column inside it). It fails loudly if the element
// is missing or undecoded.
func (t *transfer) CollectArrayElement(ref SQLValueRef) (CollectedElement, error) {
	op, err := SQLDelete(ref)
	if err != nil {
		return CollectedElement{}, fmt.Errorf("transfer: collect array element: %w", err)
	}
	if op.Kind != OperationDelete {
		return CollectedElement{}, fmt.Errorf("transfer: collect array element: ref %s.%s does not address an array element", ref.Table, ref.Column)
	}
	target, path := op.Target, op.Path
	entry := t.src.Archive().Entry(target.EntryName)
	if entry == nil {
		return CollectedElement{}, fmt.Errorf("transfer: collect array element: source entry %q not found", target.EntryName)
	}
	if entry.JSON == nil {
		return CollectedElement{}, fmt.Errorf("transfer: collect array element: source entry %q has no decoded JSON", target.EntryName)
	}
	value, ok, err := getJSONPath(entry.JSON, path)
	if err != nil {
		return CollectedElement{}, fmt.Errorf("transfer: collect array element: source path %q: %w", path, err)
	}
	if !ok {
		return CollectedElement{}, fmt.Errorf("transfer: collect array element: source path %q is missing", path)
	}
	return CollectedElement{
		SourcePath: target.EntryName,
		Table:      ref.Table,
		JSON:       cloneJSON(value),
	}, nil
}

// CollectEntry looks up a whole bibite/egg entry in the SOURCE archive and deep
// copies its JSON into a CollectedElement suitable for AppendEntry. It fails
// loudly if the entry is missing, undecoded, or does not classify as a
// bibite/egg.
func (t *transfer) CollectEntry(entryName string) (CollectedElement, error) {
	entry := t.src.Archive().Entry(entryName)
	if entry == nil {
		return CollectedElement{}, fmt.Errorf("transfer: collect entry: source entry %q not found", entryName)
	}
	if entry.JSON == nil {
		return CollectedElement{}, fmt.Errorf("transfer: collect entry: source entry %q has no decoded JSON", entryName)
	}
	kind := tb.ClassifyEntry(entryName)
	var table string
	switch kind {
	case tb.EntryBibite:
		table = "bibites"
	case tb.EntryEgg:
		table = "eggs"
	default:
		return CollectedElement{}, fmt.Errorf("transfer: collect entry: source entry %q classifies as %q, want bibite or egg", entryName, kind)
	}
	return CollectedElement{
		SourcePath: entryName,
		Table:      table,
		JSON:       cloneJSON(entry.JSON),
	}, nil
}

// SetFromCollected stages a scalar set on the destination cell dstRef using the
// collected source value. This is the settings-copy path. It is not part of the
// Workspace interface (which only models array/entry appends) because a settings
// copy is a set, not an append.
func (t *transfer) SetFromCollected(dstRef SQLValueRef, element CollectedElement) error {
	if element.JSON == nil {
		return fmt.Errorf("transfer: set from collected: element has no JSON")
	}
	if element.Table != dstRef.Table {
		return fmt.Errorf("transfer: set from collected: element table %q does not match destination table %q", element.Table, dstRef.Table)
	}
	return t.dst.StageSQLSet(dstRef, element.JSON)
}

// AppendArray appends a collected array element to the destination cell dst. It
// routes through StageSQLAppend, reusing the existing array-append resolvers and
// SceneCount reconciliation (e.g. pellets). The collected element's table must
// match the destination cell's table.
func (t *transfer) AppendArray(dst SQLValueRef, element CollectedElement) error {
	if element.JSON == nil {
		return fmt.Errorf("transfer: append array: element has no JSON")
	}
	if element.Table != dst.Table {
		return fmt.Errorf("transfer: append array: element table %q does not match destination table %q", element.Table, dst.Table)
	}
	return t.dst.StageSQLAppend(dst, element.JSON)
}

// AppendEntry grafts a collected whole bibite/egg entry into the destination
// save. It reconciles non-species identity (body.id collision, dangling child
// refs stay loud) and, when the entity carries a genes.speciesID, REMAPS it
// across the per-world-linear species space: allocate a fresh non-colliding dest
// species id, import the source species record into speciesData.json under that
// id, and rewrite the grafted entity's genes.speciesID. It then allocates a
// fresh entry name and stages the append.
//
// Atomicity: every check that can fail (identity guard, fresh-id allocation,
// source-record lookup) runs BEFORE any StageAppend, so a rejected graft leaves
// the destination with 0 staged ops by construction. All mutation is staged
// through the dst Session (the species-table appends AND the entry append), so it
// rides Apply()'s all-or-nothing atomicity; nothing is committed here (F2).
func (t *transfer) AppendEntry(element CollectedElement) error {
	if element.JSON == nil {
		return fmt.Errorf("transfer: append entry: element has no JSON")
	}
	var kind tb.EntryKind
	switch element.Table {
	case "bibites":
		kind = tb.EntryBibite
	case "eggs":
		kind = tb.EntryEgg
	default:
		return fmt.Errorf("transfer: append entry: element table %q is not bibites or eggs", element.Table)
	}

	// Deep clone so a later source mutation can never alias into the staged
	// destination payload.
	cloned := cloneJSON(element.JSON)

	// Non-species identity reconciliation is a silent-corruption surface: validate
	// it FIRST, before staging, so a rejected graft never half-mutates the dest.
	if err := t.reconcileGraftIdentity(kind, cloned); err != nil {
		return err
	}

	// Species remap (covers both bibites and eggs via genes.speciesID). A
	// species-less entity has nothing to remap and grafts cleanly. Everything
	// below that can fail does so BEFORE any StageAppend, preserving the
	// 0-staged-ops-on-rejection invariant.
	if sid, ok := entitySpeciesID(cloned); ok {
		fresh, err := t.freshDstSpeciesID()
		if err != nil {
			return err
		}
		record, found := sourceSpeciesRecord(t.src.Archive(), sid)
		if !found {
			return fmt.Errorf("transfer: append entry: cannot remap species: the grafted entity carries genes.speciesID %d but the source has no recordedSpecies record for it to import", sid)
		}
		if err := setJSONPath(cloned, "genes.speciesID", fresh, SetOptions{}); err != nil {
			return fmt.Errorf("transfer: append entry: rewrite grafted genes.speciesID: %w", err)
		}
		if err := t.stageSpeciesImport(record, fresh); err != nil {
			return err
		}
	}

	// Allocate a fresh entry name that collides with neither the archive's existing
	// entries NOR entries already STAGED for append on the dst session this run.
	// Staged appends are not reflected in dst.Archive().Entries until Apply, so a
	// multi-entry graft loop (the F2 cross-world transfer surface) must account for
	// the names handed out to earlier grafts in the same session — otherwise every
	// graft would reuse bibite_<n+1> and Apply would reject the duplicate.
	name, err := nextEntryName(t.dst.Archive(), kind, t.dstStagedAppendNames(kind)...)
	if err != nil {
		return fmt.Errorf("transfer: append entry: %w", err)
	}

	payload := EntryPayload{Name: name, Kind: kind, JSON: cloned}
	return t.dst.StageAppendBibite(payload)
}

// dstStagedAppendNames returns the entry names already staged for append on the
// dst session for the given kind. The name allocator unions these with the live
// archive entries so a multi-graft loop within one session never reuses a name
// (staged appends are invisible to dst.Archive().Entries until Apply).
func (t *transfer) dstStagedAppendNames(kind tb.EntryKind) []string {
	var names []string
	for _, op := range t.dst.StagedOperations() {
		if op.Kind == OperationAppendEntry && op.EntryPayload.Kind == kind {
			names = append(names, op.EntryPayload.Name)
		}
	}
	return names
}

// stageSpeciesImport stages the import of a source species record under the fresh
// dest id: it rewrites the record's speciesID to freshID and resets its parentID
// to freshID. parentID points into the SOURCE linear id space and has no dest
// counterpart; treating the import as its own lineage root (parentID == self)
// keeps it from being a dangling cross-world reference. We deliberately do NOT
// carry the source parentID through and do NOT remap an ancestry chain (cross-ref
// reconciliation beyond identity/species is out of scope).
//
// It stages an Append of the record onto recordedSpecies and of freshID onto
// activeSpeciesList (both on speciesData.json; entryUpdate coalesces them onto one
// working value during Apply, and the append resolvers re-read the live array, so
// batch order is safe), then bumps nextSpeciesID to freshID+1 when that field
// exists (no CreateMissing: a save without the counter is left as-is).
func (t *transfer) stageSpeciesImport(record any, freshID int64) error {
	if err := setJSONPath(record, "speciesID", freshID, SetOptions{}); err != nil {
		return fmt.Errorf("transfer: append entry: stamp imported speciesID: %w", err)
	}
	// parentID is cross-world; reset to self (lineage root). Only when present, so
	// a record without the field is left as-is rather than fabricating one.
	if _, ok := getJSONPathPresent(record, "parentID"); ok {
		if err := setJSONPath(record, "parentID", freshID, SetOptions{}); err != nil {
			return fmt.Errorf("transfer: append entry: reset imported parentID to lineage root: %w", err)
		}
	}
	if err := t.dst.StageAppend(SpeciesTarget(), "recordedSpecies", record); err != nil {
		return err
	}
	if err := t.dst.StageAppend(SpeciesTarget(), "activeSpeciesList", freshID); err != nil {
		return err
	}
	if _, ok := jsonInt64Path(t.dst.Archive().Entry(SpeciesEntryName).JSON, "nextSpeciesID"); ok {
		if err := t.dst.StageSet(SpeciesTarget(), "nextSpeciesID", freshID+1); err != nil {
			return err
		}
	}
	return nil
}

// getJSONPathPresent reports whether path exists in root (ignoring read errors,
// which mean the path could not be navigated and is therefore treated as absent).
func getJSONPathPresent(root any, path string) (any, bool) {
	value, ok, err := getJSONPath(root, path)
	if err != nil || !ok {
		return nil, false
	}
	return value, true
}
