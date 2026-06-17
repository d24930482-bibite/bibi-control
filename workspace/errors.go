package workspace

import (
	"errors"
	"fmt"
)

// ErrNotRematerializable is returned by any blob-dependent path (Load/OpenWorld,
// ReloadNode) when the target revision has been evicted to mirror_only
// (blob_present=0): its bytes are gone and there is NO mirror→blob fallback
// (non-rematerialization is structural, workspace_plan.md#g4). Callers match it
// with errors.Is; E1 surfaces it as a clean Starlark error.
var ErrNotRematerializable = errors.New("workspace: revision is mirror_only (blob evicted) and cannot be rematerialized")

// notRematerializable wraps ErrNotRematerializable with actionable context
// (operation, world id, revision id) so that errors.Is still matches via %w
// and a log line carries enough information to diagnose without re-querying.
func notRematerializable(worldID string, revID int64, op string) error {
	return fmt.Errorf("workspace: %s world %q head revision %d is mirror_only (blob evicted): %w",
		op, worldID, revID, ErrNotRematerializable)
}
