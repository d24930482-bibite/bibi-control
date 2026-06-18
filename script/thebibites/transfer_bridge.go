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

// TransferEntries grafts the named source entries from srcLS onto dstLS by
// routing each through the cross-world transfer engine (NewTransfer over the two
// ls.session's, then CollectEntry -> AppendEntry per entry). It stages onto the
// destination session only; it commits nothing and advances no head — the caller
// drives CommitWorldLoaded over the SAME dstLS.
//
// Atomicity is at the COMMIT boundary: AppendEntry guarantees 0 staged ops on a
// single rejected graft, but this loop stages many. If graft k fails after
// 0..k-1 staged, this returns the error WITHOUT touching the count it would have
// reported, and the caller MUST NOT commit — the partial stages die in-memory
// with the dst session when the automation cycle ends (or the handle is
// reloaded), so the dst head never advances on a partial transfer. Each
// successful graft bumps dstLS.stagedOps (matching entity.go's
// stageEntityDelete) so the eventual CommitLoadedWorld willWrite gate
// (ls.stagedOps > 0) fires and the commit is not treated as a no-op.
//
// It returns the number of entries grafted (== len(entryNames) on success).
func TransferEntries(srcLS, dstLS *LoadedSave, entryNames []string) (int, error) {
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

	for _, name := range entryNames {
		el, err := tr.CollectEntry(name)
		if err != nil {
			return 0, fmt.Errorf("transfer: collect %q: %w", name, err)
		}
		if err := tr.AppendEntry(el); err != nil {
			return 0, fmt.Errorf("transfer: append %q: %w", name, err)
		}
		// One structural stage per graft (mirrors entity.go's stageEntityDelete).
		dstLS.stagedOps++
	}
	return len(entryNames), nil
}
