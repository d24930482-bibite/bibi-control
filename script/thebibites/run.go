package thebibites

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/asemones/bibicontrol/blobstore"
	"github.com/asemones/bibicontrol/revisionstore"
	"github.com/asemones/bibicontrol/script"
)

// RunOptions configures a host-driven script run + commit. It is the surface a UI
// / editor backend sets; there is no CLI.
type RunOptions struct {
	// Filename labels diagnostics (defaults to the engine's <script>).
	Filename string
	// MaxExecutionSteps bounds the Starlark run (0 = engine default / unbounded
	// within Starlark's no-while hermeticity).
	MaxExecutionSteps uint64
	// DryRun stages mutations but never writes a revision (host-level override,
	// independent of the script's own autocommit() declaration).
	DryRun bool
	// Verify reparses the produced save bytes once and asserts the round-trip hash.
	Verify bool
}

// RunAndCommit loads the save once, runs the program against its scripting
// surface, records the script run, and — when the run succeeds, is not dry-run,
// the script left commit intent on (autocommit), and at least one mutation was
// staged — commits a content-addressed revision linked to that run.
//
// The script body stays pure logic: the host owns persistence. Result.RevisionRef
// is the produced blob's SHA256, empty when nothing was committed. A pure-mutation
// program therefore performs exactly one WriteArchive, zero reparses (unless
// Verify), and never opens DuckDB.
func RunAndCommit(ctx context.Context, savePath string, program []byte, blobs blobstore.Store, revs *revisionstore.Store, opts RunOptions) (script.Result, error) {
	ls, err := Load(savePath)
	if err != nil {
		return script.Result{}, err
	}
	return runLoaded(ctx, ls, program, blobs, revs, opts)
}

// runLoaded is the core shared by RunAndCommit and in-package tests that need to
// inspect the LoadedSave counters after a run.
func runLoaded(ctx context.Context, ls *LoadedSave, program []byte, blobs blobstore.Store, revs *revisionstore.Store, opts RunOptions) (script.Result, error) {
	if revs == nil {
		return script.Result{}, fmt.Errorf("run: revision store is nil")
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

	// Do the fallible commit work (serialize + blobs.Put) BEFORE recording the run,
	// so the recorded status reflects the actual commit outcome. Otherwise a run row
	// could claim status="succeeded" with no save_revisions blob behind it (phantom
	// provenance) when Commit fails after the run is already recorded. The produced
	// blob is content-addressed, so a blob written without a following revision is a
	// harmless orphan. Order is preserved for the FK: run is still recorded before
	// the revision (recordRevision below).
	var (
		commitRef   blobstore.Ref
		commitReady bool
		commitErr   error
	)
	if willWrite {
		commitRef, commitErr = ls.prepareCommit(ctx, blobs, opts.Verify)
		if commitErr != nil {
			// The commit failed: the run produced no revision. Record that truthfully
			// rather than letting "succeeded" stand for a commit that never landed.
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
			return res, runErr
		}
		return res, fmt.Errorf("record script run: %w", recErr)
	}

	if commitErr != nil {
		return res, commitErr
	}

	if commitReady {
		rev, err := ls.recordRevision(ctx, revs, commitRef, run.ID)
		if err != nil {
			return res, err
		}
		res.RevisionRef = rev.BlobRef.SHA256
	}

	return res, runErr
}

func sha256Sum(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}
