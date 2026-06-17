package thebibites

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/asemones/bibicontrol/blobstore"
	"github.com/asemones/bibicontrol/revisionstore"
	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

// newStores opens an isolated FSStore (rooted at root) and an in-memory revision
// store for one test.
func newStores(t *testing.T) (string, *blobstore.FSStore, *revisionstore.Store) {
	t.Helper()
	root := t.TempDir()
	blobs, err := blobstore.NewFSStore(root)
	if err != nil {
		t.Fatalf("NewFSStore: %v", err)
	}
	t.Cleanup(func() { _ = blobs.Close() })
	revs, err := revisionstore.Open(":memory:")
	if err != nil {
		t.Fatalf("revisionstore.Open: %v", err)
	}
	t.Cleanup(func() { _ = revs.Close() })
	return root, blobs, revs
}

// setEnergyProgram returns a pure-mutation program that sets one bibite's energy.
// It never calls save.commit — the host owns persistence.
func setEnergyProgram(entry string, energy float64) []byte {
	return []byte(fmt.Sprintf(`
def mutate():
    for b in save.bibites:
        if b.entry_name == %q:
            b.energy = %v
            break

mutate()
`, entry, energy))
}

// countObjectBlobs counts content-addressed object files (64-hex names) under root,
// independent of the store's sharding layout.
func countObjectBlobs(t *testing.T, root string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && len(d.Name()) == blobstore.SHA256HexLength {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk store root: %v", err)
	}
	return n
}

// secondBibiteEntry returns a bibite entry distinct from the first (for unrelated
// SHA256 stability checks).
func secondBibiteEntry(t *testing.T, ls *LoadedSave) string {
	t.Helper()
	ta := ls.access["bibites"]
	if ta == nil || len(ta.order) < 2 {
		t.Fatal("fixture needs at least two bibites")
	}
	return ta.order[1]
}

// TestRunAndCommitProducesBlobAndProvenance: a mutation run writes a blob, records
// a linked revision + script run, and the produced save shows the change while an
// unrelated entry stays byte-identical.
func TestRunAndCommitProducesBlobAndProvenance(t *testing.T) {
	ctx := context.Background()
	root, blobs, revs := newStores(t)
	ls := loadFixture(t)
	target := firstBibiteEntry(t, ls)
	other := secondBibiteEntry(t, ls)

	orig, err := tb.ParseFile(fixture, nil)
	if err != nil {
		t.Fatalf("parse original: %v", err)
	}

	res, err := runLoaded(ctx, ls, setEnergyProgram(target, 4321.0), blobs, revs, RunOptions{Filename: "mutate.star"})
	if err != nil {
		t.Fatalf("runLoaded: %v (%+v)", err, res.Diagnostics)
	}
	if res.RevisionRef == "" {
		t.Fatal("RevisionRef empty, want a committed revision")
	}
	if res.StagedOps != 1 {
		t.Errorf("StagedOps = %d, want 1", res.StagedOps)
	}
	if res.DryRun {
		t.Error("DryRun = true, want false")
	}

	// Provenance: revision recorded, linked to a succeeded, non-dry script run.
	revsBySHA, err := revs.RevisionsBySHA256(ctx, res.RevisionRef)
	if err != nil {
		t.Fatalf("RevisionsBySHA256: %v", err)
	}
	if len(revsBySHA) != 1 {
		t.Fatalf("got %d revisions for sha, want 1", len(revsBySHA))
	}
	rev := revsBySHA[0]
	if rev.SourcePath != fixture {
		t.Errorf("revision source_path = %q, want %q", rev.SourcePath, fixture)
	}
	run, err := revs.ScriptRunByID(ctx, rev.ScriptRunID)
	if err != nil {
		t.Fatalf("ScriptRunByID(%d): %v", rev.ScriptRunID, err)
	}
	if run.Status != "succeeded" {
		t.Errorf("run status = %q, want succeeded", run.Status)
	}
	if run.StagedOps != 1 || run.DryRun {
		t.Errorf("run staged_ops=%d dry_run=%v, want 1/false", run.StagedOps, run.DryRun)
	}

	// Blob present and its bytes are the produced save.
	has, err := blobs.Has(ctx, rev.BlobRef)
	if err != nil || !has {
		t.Fatalf("blobs.Has = %v, %v; want true, nil", has, err)
	}
	if got := countObjectBlobs(t, root); got != 1 {
		t.Errorf("object blobs on disk = %d, want 1", got)
	}
	data, err := blobs.Get(ctx, rev.BlobRef)
	if err != nil {
		t.Fatalf("blobs.Get: %v", err)
	}

	out := filepath.Join(t.TempDir(), "produced.zip")
	if err := os.WriteFile(out, data, 0o600); err != nil {
		t.Fatalf("write produced save: %v", err)
	}
	re, err := tb.ParseFile(out, nil)
	if err != nil {
		t.Fatalf("reparse produced save: %v", err)
	}
	if got := bibiteRowEnergy(t, tb.ExtractTables(re.SHA256, re), target); got != 4321.0 {
		t.Errorf("committed energy = %v, want 4321.0", got)
	}
	if orig.Entry(other).SHA256 != re.Entry(other).SHA256 {
		t.Errorf("unrelated entry %q SHA256 changed: %s -> %s", other, orig.Entry(other).SHA256, re.Entry(other).SHA256)
	}
}

// TestRunAndCommitDryRun: the host DryRun override records a run with dry_run=1 and
// writes no blob.
func TestRunAndCommitDryRun(t *testing.T) {
	ctx := context.Background()
	root, blobs, revs := newStores(t)
	ls := loadFixture(t)
	target := firstBibiteEntry(t, ls)

	res, err := runLoaded(ctx, ls, setEnergyProgram(target, 4321.0), blobs, revs, RunOptions{DryRun: true})
	if err != nil {
		t.Fatalf("runLoaded: %v (%+v)", err, res.Diagnostics)
	}
	if res.RevisionRef != "" {
		t.Errorf("RevisionRef = %q, want empty under dry-run", res.RevisionRef)
	}
	if !res.DryRun {
		t.Error("DryRun = false, want true")
	}
	if got := countObjectBlobs(t, root); got != 0 {
		t.Errorf("object blobs on disk = %d, want 0 under dry-run", got)
	}

	// The run was still recorded (first row id == 1) with dry_run=1 and the staged op.
	run, err := revs.ScriptRunByID(ctx, 1)
	if err != nil {
		t.Fatalf("ScriptRunByID(1): %v", err)
	}
	if !run.DryRun {
		t.Error("recorded run dry_run = false, want true")
	}
	if run.StagedOps != 1 {
		t.Errorf("recorded run staged_ops = %d, want 1", run.StagedOps)
	}
}

// TestRunAndCommitAutocommitOptOut: a script declaring autocommit(False) stages
// mutations but produces no revision; the run is recorded as dry (no revision by
// intent).
func TestRunAndCommitAutocommitOptOut(t *testing.T) {
	ctx := context.Background()
	root, blobs, revs := newStores(t)
	ls := loadFixture(t)
	target := firstBibiteEntry(t, ls)

	program := []byte(fmt.Sprintf(`
autocommit(False)

def mutate():
    for b in save.bibites:
        if b.entry_name == %q:
            b.energy = 4321.0
            break

mutate()
`, target))

	res, err := runLoaded(ctx, ls, program, blobs, revs, RunOptions{})
	if err != nil {
		t.Fatalf("runLoaded: %v (%+v)", err, res.Diagnostics)
	}
	if res.RevisionRef != "" {
		t.Errorf("RevisionRef = %q, want empty after autocommit(False)", res.RevisionRef)
	}
	if !res.DryRun {
		t.Error("DryRun = false, want true (no revision by intent)")
	}
	if res.StagedOps != 1 {
		t.Errorf("StagedOps = %d, want 1 (mutation still staged)", res.StagedOps)
	}
	if got := countObjectBlobs(t, root); got != 0 {
		t.Errorf("object blobs on disk = %d, want 0", got)
	}
	run, err := revs.ScriptRunByID(ctx, 1)
	if err != nil {
		t.Fatalf("ScriptRunByID(1): %v", err)
	}
	if !run.DryRun || run.StagedOps != 1 {
		t.Errorf("recorded run dry_run=%v staged_ops=%d, want true/1", run.DryRun, run.StagedOps)
	}
}

// TestRunAndCommitFailureRecordsNonSucceededRun: when the commit work fails (here a
// nil blob store), the run must NOT be recorded as "succeeded" with no revision
// behind it. The recorded status reflects the commit failure and no revision row is
// produced (no phantom provenance).
func TestRunAndCommitFailureRecordsNonSucceededRun(t *testing.T) {
	ctx := context.Background()
	root, _, revs := newStores(t)
	ls := loadFixture(t)
	target := firstBibiteEntry(t, ls)

	// nil blob store forces prepareCommit to fail on an otherwise-committing run.
	res, err := runLoaded(ctx, ls, setEnergyProgram(target, 4321.0), nil, revs, RunOptions{})
	if err == nil {
		t.Fatal("runLoaded succeeded, want a commit failure from the nil blob store")
	}
	if res.RevisionRef != "" {
		t.Errorf("RevisionRef = %q, want empty when the commit failed", res.RevisionRef)
	}

	// The run was still recorded (record-every-run invariant) but NOT as succeeded.
	run, rerr := revs.ScriptRunByID(ctx, 1)
	if rerr != nil {
		t.Fatalf("ScriptRunByID(1): %v", rerr)
	}
	if run.Status == "succeeded" {
		t.Errorf("run status = %q, want a non-succeeded status for a failed commit", run.Status)
	}
	if run.Status != "commit_failed" {
		t.Errorf("run status = %q, want commit_failed", run.Status)
	}
	// No blob produced, so nothing the run could falsely claim a revision over.
	if got := countObjectBlobs(t, root); got != 0 {
		t.Errorf("object blobs on disk = %d, want 0 (commit never produced a blob)", got)
	}
}

// TestRunAndCommitChurn is the headline assertion: a pure-mutation commit performs
// exactly one WriteArchive, zero reparses, and never opens DuckDB.
func TestRunAndCommitChurn(t *testing.T) {
	ctx := context.Background()
	_, blobs, revs := newStores(t)
	ls := loadFixture(t)
	target := firstBibiteEntry(t, ls)

	res, err := runLoaded(ctx, ls, setEnergyProgram(target, 4321.0), blobs, revs, RunOptions{})
	if err != nil {
		t.Fatalf("runLoaded: %v (%+v)", err, res.Diagnostics)
	}
	if ls.writeArchiveCount != 1 {
		t.Errorf("writeArchiveCount = %d, want 1", ls.writeArchiveCount)
	}
	if ls.reparseCount != 0 {
		t.Errorf("reparseCount = %d, want 0 (no verify)", ls.reparseCount)
	}
	if ls.dbOpenCount != 0 {
		t.Errorf("dbOpenCount = %d, want 0 (pure mutation never opens DuckDB)", ls.dbOpenCount)
	}
}

// TestRunAndCommitVerify: opt-in verify reparses the produced save exactly once and
// the round-trip succeeds.
func TestRunAndCommitVerify(t *testing.T) {
	ctx := context.Background()
	_, blobs, revs := newStores(t)
	ls := loadFixture(t)
	target := firstBibiteEntry(t, ls)

	res, err := runLoaded(ctx, ls, setEnergyProgram(target, 4321.0), blobs, revs, RunOptions{Verify: true})
	if err != nil {
		t.Fatalf("runLoaded (verify): %v (%+v)", err, res.Diagnostics)
	}
	if res.RevisionRef == "" {
		t.Fatal("RevisionRef empty under verify")
	}
	if ls.reparseCount != 1 {
		t.Errorf("reparseCount = %d, want 1 under verify", ls.reparseCount)
	}
	if ls.writeArchiveCount != 1 {
		t.Errorf("writeArchiveCount = %d, want 1", ls.writeArchiveCount)
	}
}
