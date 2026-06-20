package thebibites

// transfer_bridge.go is the thin exported seam that wires the merged F1/F3
// cross-world transfer engine (savemutator/thebibites) over two *LoadedSave
// handles. It lives IN package thebibites so it can read the unexported
// ls.session directly; nothing here leaks the *mutator.Session type outside the
// package. The function signature exposes only *LoadedSave + entry names + a
// count, which keeps the savemutator import out of the workspace transfer
// binding.
//
// The bridge stages — it never commits. Every grafted entry passes through
// mutator.NewTransfer / CollectEntry / AppendEntry so the F1/F3 identity
// reconcile + per-world species remap always fire; skipping AppendEntry would
// silently conflate species (the exact F1 v1 bug). The caller (workspace) commits
// the staged grafts onto the SAME dst *LoadedSave through the unchanged
// CommitWorldLoaded path.

import (
	"fmt"

	mutator "github.com/asemones/bibicontrol/savemutator/thebibites"
)

// TransferOptions carries the opt-in toggles for a cross-world transfer.
type TransferOptions struct {
	// Move, when true, stages a SOURCE-side delete for every grafted entry (so the
	// entity ends up in exactly one world) IN ADDITION to the dst graft. The bridge
	// only STAGES the source deletes; the WORKSPACE layer owns the two commits and
	// their dst-first ordering. Default false is a copy (source left intact).
	Move bool
	// RemapIDs threads through to mutator.GraftOptions.RemapIDs: a body.id collision
	// is resolved by minting a fresh dest id instead of failing loudly.
	RemapIDs bool
}

// TransferEntries grafts the named source entries from srcLS onto dstLS by
// routing each through the cross-world transfer engine (NewTransfer over the two
// ls.session's, then CollectEntry -> AppendEntry per entry). It stages onto the
// destination session; for opts.Move it ALSO stages the corresponding source-side
// deletes on srcLS (reusing srcLS.stageEntityDelete, the SAME guarded delete path
// Entity.delete()/where(...).delete() use, so move inherits the identical
// cascade/referential guard). It commits NOTHING and advances no head — the caller
// drives the commits (dst first, then the source-delete commit for move).
//
// Atomicity is at the COMMIT boundary: AppendEntry guarantees 0 staged ops on a
// single rejected graft, but this loop stages many. If graft k fails after
// 0..k-1 staged, this returns the error WITHOUT touching the count it would have
// reported, and the caller MUST NOT commit — the partial stages (on BOTH sessions
// for move) die in-memory with the sessions when the automation cycle ends (or the
// handle is reloaded), so no head advances on a partial transfer. The source delete
// for entry k is staged only AFTER entry k's dst graft succeeds, so a graft failure
// never leaves an orphan source delete staged ahead of its (absent) dst graft. Each
// successful graft bumps dstLS.stagedOps and (for move) srcLS.stagedOps (matching
// entity.go's stageEntityDelete) so the eventual CommitLoadedWorld willWrite gate
// (ls.stagedOps > 0) fires on each side and neither commit is treated as a no-op.
//
// It returns the number of entries grafted (== len(entryNames) on success); for
// move that is also the number of source deletes staged.
func TransferEntries(srcLS, dstLS *LoadedSave, entryNames []string, opts TransferOptions) (int, error) {
	if srcLS == nil {
		return 0, fmt.Errorf("transfer: source loaded save is nil")
	}
	if dstLS == nil {
		return 0, fmt.Errorf("transfer: destination loaded save is nil")
	}
	if srcLS == dstLS {
		return 0, fmt.Errorf("transfer: source and destination are the same loaded save")
	}
	// The source must be in a readable, projection-valid state: CollectEntry reads
	// the per-entry parsed JSON off the source archive, so a consumed/committed
	// handle (StateApplied) would graft stale bytes — refuse loudly instead.
	if srcLS.session.State() == mutator.StateApplied {
		return 0, fmt.Errorf("transfer: source save has already been applied/committed and cannot be a transfer source")
	}
	if dstLS.session.State() == mutator.StateApplied {
		return 0, fmt.Errorf("transfer: destination save has already been applied/committed and cannot stage a transfer")
	}

	tr, err := mutator.NewTransfer(srcLS.session, dstLS.session)
	if err != nil {
		return 0, fmt.Errorf("transfer: %w", err)
	}

	graftOpts := mutator.GraftOptions{RemapIDs: opts.RemapIDs}

	for _, name := range entryNames {
		el, err := tr.CollectEntry(name)
		if err != nil {
			return 0, fmt.Errorf("transfer: collect %q: %w", name, err)
		}
		if err := tr.AppendEntry(el, graftOpts); err != nil {
			return 0, fmt.Errorf("transfer: append %q: %w", name, err)
		}
		// One structural stage per graft (mirrors entity.go's stageEntityDelete).
		dstLS.stagedOps++
		dstLS.markStructuralStaged()

		if opts.Move {
			// Stage the source-side delete for THIS entry, only after its dst graft
			// succeeded. CollectEntry classified the entry into el.Table
			// ("bibites"/"eggs"); map it back to stageEntityDelete's kind string.
			kind, err := transferTableKind(el.Table)
			if err != nil {
				return 0, fmt.Errorf("transfer: move %q: %w", name, err)
			}
			// prune=false: move deletes the entity, not its lineage; the referential
			// guard inside stageEntityDelete still fires loudly at commit if the delete
			// would orphan a parent link (identical to a manual delete).
			if err := srcLS.stageEntityDelete(kind, name, false); err != nil {
				return 0, fmt.Errorf("transfer: move %q: stage source delete: %w", name, err)
			}
			srcLS.stagedOps++
			srcLS.markStructuralStaged()
		}
	}
	return len(entryNames), nil
}

// transferTableKind maps a CollectedElement table ("bibites"/"eggs") back to the
// singular kind string stageEntityDelete expects ("bibite"/"egg").
func transferTableKind(table string) (string, error) {
	switch table {
	case "bibites":
		return "bibite", nil
	case "eggs":
		return "egg", nil
	default:
		return "", fmt.Errorf("table %q is not bibites or eggs", table)
	}
}
