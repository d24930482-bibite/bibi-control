package thebibites

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/asemones/bibicontrol/blobstore"
	"github.com/asemones/bibicontrol/revisionstore"
	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
	"github.com/asemones/bibicontrol/script"
)

// WorldCommit is the result of running a save program against a loaded world
// copy and committing an advancing-head revision. Committed is false when the
// run was pure-analysis / dry-run / opted-out / staged nothing (no revision
// produced); Revision/Applied are then zero/nil and the caller skips the DuckDB
// re-import.
//
// Applied is the post-Apply in-memory archive prepareCommit serialized to the
// blob. Note that Session.Apply rewrites the mutated entries' JSON/Raw bytes but
// does NOT re-derive the typed parser projections (archive.Bibites/etc), so
// Applied's projection slices are STALE relative to the mutation (session.go
// "projections invalid until Commit reparses"). It is exposed as the committed
// artifact / a committed signal, NOT as a mirror-projection source: the workspace
// reparses the committed blob bytes once to get mutation-accurate projections
// (the LoadedSave reparse counter, the churn DoD's reparseCount, stays 0 — the
// reparse is the workspace mirror's, not this commit path's; the blob is produced
// by a single WriteArchive).
//
// SaveID is ls.saveID (== worldID); the workspace asserts that equality so a
// dual-key desync fails loudly.
type WorldCommit struct {
	Result    script.Result
	Committed bool
	Revision  revisionstore.Revision // the advancing-head revision (zero when !Committed)
	Applied   *tb.Archive            // ls.session.Archive() after Apply (stale projections; see doc); nil when !Committed
	SaveID    string                 // ls.saveID == worldID (the working-partition key)
}

// RunAndCommitWorld runs program against the ALREADY-LOADED working copy ls (the
// one whose ls.session prepareCommit serializes), records the script run, and —
// when the run staged a mutation, opted into commit, and is not dry-run —
// produces one content-addressed blob (one WriteArchive, zero reparses) and
// records a new revision that threads parent = parentID (the world's current
// head, supplied by the caller under the workspace lock) and advances
// worlds.head_revision_id (+ sim_time) atomically via RecordRevisionAdvancingHead.
//
// It mirrors runLoaded's run/record/commit ordering and status precedence, but:
//   - it runs against a caller-supplied ls (the world's working copy), never a
//     fresh Load — re-Loading would stage onto a throwaway save the eventual
//     commit would not serialize (bindings.go:27-32);
//   - it records the advancing-head revision (parent threaded, head advanced in
//     one SQLite tx) instead of the standalone parent-less RecordRevision; and
//   - it returns the post-commit projection material (Applied + SaveID) so the
//     workspace can run the dual-key DuckDB import under its own mutex (a
//     script/thebibites function has neither w.duck() nor w.mu).
//
// RecordRevisionAdvancingHead self-refs the blob inside its own SQLite tx
// (insertRevisionTx -> incBlobRefTx, store.go:337/380), so this MUST NOT call
// IncBlobRef separately — a second increment double-counts and breaks eviction.
func RunAndCommitWorld(ctx context.Context, ls *LoadedSave, program []byte, worldID string, parentID *int64, blobs blobstore.Store, revs *revisionstore.Store, opts RunOptions) (WorldCommit, error) {
	if revs == nil {
		return WorldCommit{}, fmt.Errorf("commit world: revision store is nil")
	}
	if blobs == nil {
		return WorldCommit{}, fmt.Errorf("commit world: blob store is nil")
	}
	if ls == nil {
		return WorldCommit{}, fmt.Errorf("commit world: loaded save is nil")
	}
	if worldID == "" {
		return WorldCommit{}, fmt.Errorf("commit world: worldID is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ls.dryRun = opts.DryRun

	scriptSHA := hex.EncodeToString(sha256Sum(program))
	startedAt := time.Now().UTC()
	res, runErr := script.Run(ctx, program, Globals(ls), script.Options{
		Filename:          opts.Filename,
		MaxExecutionSteps: opts.MaxExecutionSteps,
	})
	finishedAt := time.Now().UTC()

	status := "succeeded"
	errMsg := ""
	if runErr != nil {
		status = "failed"
		errMsg = runErr.Error()
	}

	// recordedDryRun captures intent: this run deliberately produced no revision,
	// whether by the host DryRun override or the script's autocommit(False).
	recordedDryRun := opts.DryRun || !ls.willCommit
	willWrite := runErr == nil && !opts.DryRun && ls.willCommit && ls.stagedOps > 0

	// Do the fallible commit work (serialize + blobs.Put) BEFORE recording the
	// run so the recorded status reflects the actual commit outcome (no phantom
	// "succeeded" provenance over a commit that never landed). The blob is
	// content-addressed, so a blob written without a following revision is a
	// harmless orphan. Same ordering as runLoaded (run.go:78-100).
	var (
		commitRef   blobstore.Ref
		commitReady bool
		commitErr   error
	)
	if willWrite {
		commitRef, commitErr = ls.prepareCommit(ctx, blobs, opts.Verify)
		if commitErr != nil {
			status = "commit_failed"
			errMsg = commitErr.Error()
		} else {
			commitReady = true
		}
	}

	run, recErr := revs.RecordScriptRun(ctx, revisionstore.ScriptRunInput{
		ScriptSHA256: scriptSHA,
		StartedAt:    startedAt,
		FinishedAt:   &finishedAt,
		Status:       status,
		Error:        errMsg,
		Output:       res.Output,
		StagedOps:    int64(ls.stagedOps),
		DryRun:       recordedDryRun,
	})

	res.StagedOps = ls.stagedOps
	res.DryRun = recordedDryRun

	if recErr != nil {
		if runErr != nil {
			return WorldCommit{Result: res, Committed: false}, runErr
		}
		return WorldCommit{Result: res, Committed: false}, fmt.Errorf("record script run: %w", recErr)
	}

	if commitErr != nil {
		return WorldCommit{Result: res, Committed: false}, commitErr
	}

	if !commitReady {
		// Pure-analysis / dry-run / opt-out / staged nothing: no revision, no head
		// movement. Surface any run-level error but report not committed.
		return WorldCommit{Result: res, Committed: false}, runErr
	}

	// Committed path: derive sim_time from the post-Apply archive (the same
	// in-memory archive prepareCommit serialized — Archive() is non-nil after
	// ensureApplied) exactly as importWorldFromArchive does (world.go:121-125).
	applied := ls.session.Archive()
	var simTime *float64
	if applied != nil && applied.Scene != nil && applied.Scene.HasTime {
		st := applied.Scene.SimulatedTime
		simTime = &st
	}

	// Record the advancing-head revision: insert + advance head + self-ref the
	// blob, all in one SQLite tx. parentID is the caller-supplied current head
	// (the workspace reads it under w.mu immediately before this call), so the
	// lineage parent is the true head at commit time. No separate IncBlobRef.
	rev, err := revs.RecordRevisionAdvancingHead(ctx, worldID, simTime, revisionstore.RevisionInput{
		ParentID:    parentID,
		WorldID:     worldID,
		SourcePath:  ls.path,
		BlobRef:     commitRef,
		ScriptRunID: run.ID,
	})
	if err != nil {
		return WorldCommit{Result: res, Committed: false}, fmt.Errorf("record advancing-head revision: %w", err)
	}
	res.RevisionRef = rev.BlobRef.SHA256

	return WorldCommit{
		Result:    res,
		Committed: true,
		Revision:  rev,
		Applied:   applied,
		SaveID:    ls.saveID,
	}, runErr
}
