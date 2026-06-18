package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/asemones/bibicontrol/ipc"
	"github.com/asemones/bibicontrol/noderuntime"
	"github.com/asemones/bibicontrol/revisionstore"
	tb "github.com/asemones/bibicontrol/saveparser/thebibites"

	_ "modernc.org/sqlite"
)

// reloadFakeNode is a self-contained fake game node for D3's reload/ingest tests
// (kept in this file so no existing file is edited). Unlike node_control_test's
// fakeSimCtl, its INFO autosave path is configurable so the default-path ingest
// branch can be exercised against a real on-disk fixture. It records the last
// command for assertion.
type reloadFakeNode struct {
	sess *ipc.Session

	mu           sync.Mutex
	lastCommand  string
	autosavePath string
}

// newReloadFakeNode persists a nodes row (world_id + drop_path) for nodeID and
// registers a configurable fake runtime under the same logical id. autosavePath
// is the path returned by the fake's INFO LastAutosave (use "" for none).
func newReloadFakeNode(t *testing.T, ws *Workspace, nodeID, worldID, dropPath, autosavePath string) *reloadFakeNode {
	t.Helper()
	ctx := context.Background()
	if _, err := ws.store().CreateNode(ctx, revisionstore.NodeInput{
		WorkspaceID: ws.ID(),
		WorldID:     worldID,
		NodeID:      nodeID,
		RunID:       "run",
		Status:      "running",
		DropPath:    dropPath,
	}); err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	cConn, sConn := net.Pipe()
	fake := &reloadFakeNode{sess: ipc.NewSession(sConn, nil), autosavePath: autosavePath}
	go fake.serve()
	rt := noderuntime.Wrap(nodeID, "run", nil, ipc.NewSession(cConn, nil))

	ws.mu.Lock()
	ws.nodes[nodeID] = rt
	ws.mu.Unlock()

	t.Cleanup(func() {
		_ = rt.Close()
		_ = fake.sess.Close()
	})
	return fake
}

func (f *reloadFakeNode) serve() {
	for env := range f.sess.Events() {
		if env.Kind != ipc.KindRequest {
			continue
		}
		f.mu.Lock()
		f.lastCommand = env.Command
		autosave := f.autosavePath
		f.mu.Unlock()

		reply := ipc.Envelope{Kind: ipc.KindResponse, ReplyTo: env.ID}
		switch env.Command {
		case ipc.CommandInfo:
			info := ipc.InfoResult{TPS: 60, RealTPS: 60, Paused: true, SimTime: 1}
			if autosave != "" {
				info.LastAutosave = &ipc.AutosaveInfo{Path: autosave, Name: filepath.Base(autosave)}
			}
			reply.Payload = mustJSONReload(info)
		case ipc.CommandReload:
			reply.Payload = mustJSONReload(ipc.ReloadResult{Save: autosave, Ok: true})
		default:
			reply.Kind = ipc.KindError
			reply.Error = "unknown command: " + env.Command
		}
		_ = f.sess.Send(context.Background(), reply)
	}
}

func mustJSONReload(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// headRevision returns the world's current head revision.
func headRevision(t *testing.T, ctx context.Context, ws *Workspace, worldID string) revisionstore.Revision {
	t.Helper()
	world, err := ws.store().GetWorld(ctx, worldID)
	if err != nil {
		t.Fatalf("GetWorld: %v", err)
	}
	if world.HeadRevisionID == nil {
		t.Fatalf("world %q has no head", worldID)
	}
	rev, err := ws.store().RevisionByID(ctx, *world.HeadRevisionID)
	if err != nil {
		t.Fatalf("RevisionByID: %v", err)
	}
	return rev
}

// forceHeadMirrorOnly flips the head revision's blob_present to 0 via a direct
// SQLite UPDATE. EvictRevisionBlob refuses to evict a head, so the test reaches
// the registry file directly to simulate a mirror_only head (the G4 guard site).
func forceHeadMirrorOnly(t *testing.T, ctx context.Context, ws *Workspace, worldID string) {
	t.Helper()
	rev := headRevision(t, ctx, ws, worldID)
	db, err := sql.Open("sqlite", registryPath(ws.root)+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open registry sqlite: %v", err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx,
		`UPDATE save_revisions SET tier = 'mirror_only', blob_present = 0 WHERE id = ?`, rev.ID); err != nil {
		t.Fatalf("force mirror_only: %v", err)
	}
}

func TestReloadNode_ShipsHeadAndSendsReload(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	world, err := ws.AddWorld(ctx, fixturePath(t, fixtureSmall), "world-a")
	if err != nil {
		t.Fatalf("AddWorld: %v", err)
	}
	head := headRevision(t, ctx, ws, world.ID)
	wantBytes, err := ws.blobs().Get(ctx, head.BlobRef)
	if err != nil {
		t.Fatalf("blobs.Get head: %v", err)
	}

	dropPath := filepath.Join(t.TempDir(), "drops", "current.zip")
	fake := newReloadFakeNode(t, ws, "node-reload", world.ID, dropPath, "")

	res, err := ws.ReloadNode(ctx, "node-reload")
	if err != nil {
		t.Fatalf("ReloadNode: %v", err)
	}
	if !res.Ok {
		t.Fatalf("ReloadResult.Ok = false, want true")
	}

	// (a) The drop file exists and its bytes are the shipped head blob.
	gotBytes, err := os.ReadFile(dropPath)
	if err != nil {
		t.Fatalf("read drop file: %v", err)
	}
	if len(gotBytes) != len(wantBytes) {
		t.Fatalf("drop file size = %d, want %d", len(gotBytes), len(wantBytes))
	}
	for i := range gotBytes {
		if gotBytes[i] != wantBytes[i] {
			t.Fatalf("drop file bytes differ from head blob at index %d", i)
		}
	}

	// (b) The fake saw RELOAD.
	fake.mu.Lock()
	last := fake.lastCommand
	fake.mu.Unlock()
	if last != ipc.CommandReload {
		t.Fatalf("fake saw command %q, want %q", last, ipc.CommandReload)
	}
}

func TestReloadNode_NoDropPath(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	world, err := ws.AddWorld(ctx, fixturePath(t, fixtureSmall), "world-a")
	if err != nil {
		t.Fatalf("AddWorld: %v", err)
	}
	// Bound world but no drop_path.
	fake := newReloadFakeNode(t, ws, "node-nodrop", world.ID, "", "")

	if _, err := ws.ReloadNode(ctx, "node-nodrop"); err == nil {
		t.Fatalf("ReloadNode with no drop_path: want error, got nil")
	}
	// No RELOAD must have been sent.
	fake.mu.Lock()
	last := fake.lastCommand
	fake.mu.Unlock()
	if last == ipc.CommandReload {
		t.Fatalf("RELOAD was sent despite missing drop_path")
	}
}

func TestReloadNode_NoBoundWorld(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	dropPath := filepath.Join(t.TempDir(), "current.zip")
	// Persist a node row with no world binding, but a drop_path.
	fake := newReloadFakeNode(t, ws, "node-noworld", "", dropPath, "")

	if _, err := ws.ReloadNode(ctx, "node-noworld"); err == nil {
		t.Fatalf("ReloadNode with no bound world: want error, got nil")
	}
	if _, err := os.Stat(dropPath); err == nil {
		t.Fatalf("drop file was written despite no bound world")
	}
	fake.mu.Lock()
	last := fake.lastCommand
	fake.mu.Unlock()
	if last == ipc.CommandReload {
		t.Fatalf("RELOAD was sent despite no bound world")
	}
}

func TestReloadNode_UnknownNode(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	if _, err := ws.ReloadNode(ctx, "ghost"); err == nil {
		t.Fatalf("ReloadNode on unknown node: want error, got nil")
	}
}

func TestReloadNode_MirrorOnlyHeadRefused(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	world, err := ws.AddWorld(ctx, fixturePath(t, fixtureSmall), "world-a")
	if err != nil {
		t.Fatalf("AddWorld: %v", err)
	}
	forceHeadMirrorOnly(t, ctx, ws, world.ID)

	dropPath := filepath.Join(t.TempDir(), "current.zip")
	fake := newReloadFakeNode(t, ws, "node-mirror", world.ID, dropPath, "")

	_, reloadErr := ws.ReloadNode(ctx, "node-mirror")
	if !errors.Is(reloadErr, ErrNotRematerializable) {
		t.Fatalf("ReloadNode on mirror_only head: err = %v, want ErrNotRematerializable", reloadErr)
	}
	// The guard fires before any file write or RELOAD.
	if _, err := os.Stat(dropPath); err == nil {
		t.Fatalf("drop file written despite mirror_only head")
	}
	fake.mu.Lock()
	last := fake.lastCommand
	fake.mu.Unlock()
	if last == ipc.CommandReload {
		t.Fatalf("RELOAD was sent despite mirror_only head")
	}
}

func TestIngestAutosave_AppendsAndAdvancesHead(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	world, err := ws.AddWorld(ctx, fixturePath(t, fixtureSmall), "world-a")
	if err != nil {
		t.Fatalf("AddWorld: %v", err)
	}
	rev1 := headRevision(t, ctx, ws, world.ID)

	newReloadFakeNode(t, ws, "node-ingest", world.ID, filepath.Join(t.TempDir(), "drop.zip"), "")

	// Ingest a genuinely different save (fixtureB) by explicit path.
	rev2, ingested, err := ws.IngestAutosave(ctx, "node-ingest", fixturePath(t, fixtureB))
	if err != nil {
		t.Fatalf("IngestAutosave: %v", err)
	}
	if !ingested {
		t.Fatalf("ingested = false, want true for a changed save")
	}

	// (a) The new revision threads parent = rev1, is bound to the world, and is
	// at refcount 1 (no double IncBlobRef).
	if rev2.ParentID == nil || *rev2.ParentID != rev1.ID {
		t.Fatalf("rev2.ParentID = %v, want %d", rev2.ParentID, rev1.ID)
	}
	if rev2.WorldID != world.ID {
		t.Fatalf("rev2.WorldID = %q, want %q", rev2.WorldID, world.ID)
	}
	if rev2.Refcount != 1 {
		t.Fatalf("rev2.Refcount = %d, want 1 (no second IncBlobRef)", rev2.Refcount)
	}
	if rev2.SHA256 == rev1.SHA256 {
		t.Fatalf("rev2 sha256 equals rev1 sha256 — fixtures are not distinct")
	}

	// (b) The world head now points at the new revision.
	got, err := ws.store().GetWorld(ctx, world.ID)
	if err != nil {
		t.Fatalf("GetWorld: %v", err)
	}
	if got.HeadRevisionID == nil || *got.HeadRevisionID != rev2.ID {
		t.Fatalf("head = %v, want %d", got.HeadRevisionID, rev2.ID)
	}

	// (c) History retention: both rev1 and rev2 history partitions are present.
	if got := countBySaveID(t, ctx, ws, "save_archives", rev1.SHA256); got != 1 {
		t.Fatalf("rev1 history save_archives count = %d, want 1 (history must accumulate)", got)
	}
	if got := countBySaveID(t, ctx, ws, "save_archives", rev2.SHA256); got != 1 {
		t.Fatalf("rev2 history save_archives count = %d, want 1", got)
	}

	// (d) Working partition (keyed by world id) reflects the ingested save's rows.
	wantWorking := countBySaveID(t, ctx, ws, "save_archives", rev2.SHA256)
	gotWorking := countBySaveID(t, ctx, ws, "save_archives", world.ID)
	if gotWorking != wantWorking {
		t.Fatalf("working save_archives count = %d, want %d (re-seeded to ingested head)", gotWorking, wantWorking)
	}

	// Exactly two revisions exist for the world.
	revs, err := ws.store().RevisionsForWorld(ctx, world.ID)
	if err != nil {
		t.Fatalf("RevisionsForWorld: %v", err)
	}
	if len(revs) != 2 {
		t.Fatalf("RevisionsForWorld = %d, want 2", len(revs))
	}
}

func TestIngestAutosave_DedupSkipsUnchanged(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	world, err := ws.AddWorld(ctx, fixturePath(t, fixtureSmall), "world-a")
	if err != nil {
		t.Fatalf("AddWorld: %v", err)
	}
	rev1 := headRevision(t, ctx, ws, world.ID)

	newReloadFakeNode(t, ws, "node-dedup", world.ID, filepath.Join(t.TempDir(), "drop.zip"), "")

	// Re-ingest the SAME save as the head (fixtureSmall).
	rev, ingested, err := ws.IngestAutosave(ctx, "node-dedup", fixturePath(t, fixtureSmall))
	if err != nil {
		t.Fatalf("IngestAutosave: %v", err)
	}
	if ingested {
		t.Fatalf("ingested = true, want false (unchanged save must be a no-op skip)")
	}
	if rev.ID != 0 || rev.SHA256 != "" {
		t.Fatalf("dedup-skip returned non-zero revision: %+v", rev)
	}

	// Head unchanged, no new revision.
	got, err := ws.store().GetWorld(ctx, world.ID)
	if err != nil {
		t.Fatalf("GetWorld: %v", err)
	}
	if got.HeadRevisionID == nil || *got.HeadRevisionID != rev1.ID {
		t.Fatalf("head moved on a dedup-skip: head = %v, want %d", got.HeadRevisionID, rev1.ID)
	}
	revs, err := ws.store().RevisionsForWorld(ctx, world.ID)
	if err != nil {
		t.Fatalf("RevisionsForWorld: %v", err)
	}
	if len(revs) != 1 {
		t.Fatalf("RevisionsForWorld = %d, want 1 (no new revision on dedup)", len(revs))
	}
}

func TestIngestAutosave_DefaultPathFromInfo(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	world, err := ws.AddWorld(ctx, fixturePath(t, fixtureSmall), "world-a")
	if err != nil {
		t.Fatalf("AddWorld: %v", err)
	}
	rev1 := headRevision(t, ctx, ws, world.ID)

	// Copy fixtureB to a real temp file and point the fake's INFO autosave at it.
	srcBytes, err := os.ReadFile(fixturePath(t, fixtureB))
	if err != nil {
		t.Fatalf("read fixtureB: %v", err)
	}
	autosavePath := filepath.Join(t.TempDir(), "autosave.zip")
	if err := os.WriteFile(autosavePath, srcBytes, 0o644); err != nil {
		t.Fatalf("write autosave copy: %v", err)
	}

	newReloadFakeNode(t, ws, "node-default", world.ID, filepath.Join(t.TempDir(), "drop.zip"), autosavePath)

	rev2, ingested, err := ws.IngestAutosave(ctx, "node-default", "")
	if err != nil {
		t.Fatalf("IngestAutosave(default path): %v", err)
	}
	if !ingested {
		t.Fatalf("ingested = false, want true")
	}
	if rev2.ParentID == nil || *rev2.ParentID != rev1.ID {
		t.Fatalf("rev2.ParentID = %v, want %d", rev2.ParentID, rev1.ID)
	}

	got, err := ws.store().GetWorld(ctx, world.ID)
	if err != nil {
		t.Fatalf("GetWorld: %v", err)
	}
	if got.HeadRevisionID == nil || *got.HeadRevisionID != rev2.ID {
		t.Fatalf("head = %v, want %d (ingest from INFO-supplied path)", got.HeadRevisionID, rev2.ID)
	}

	// The ingested content matches fixtureB's digest.
	archive, err := tb.ParseFile(fixturePath(t, fixtureB), nil)
	if err != nil {
		t.Fatalf("ParseFile fixtureB: %v", err)
	}
	if rev2.SHA256 != archive.SHA256 {
		t.Fatalf("rev2 sha256 = %q, want %q (fixtureB)", rev2.SHA256, archive.SHA256)
	}
}

func TestIngestAutosave_StabilizeRejectsMissing(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	world, err := ws.AddWorld(ctx, fixturePath(t, fixtureSmall), "world-a")
	if err != nil {
		t.Fatalf("AddWorld: %v", err)
	}
	rev1 := headRevision(t, ctx, ws, world.ID)

	newReloadFakeNode(t, ws, "node-missing", world.ID, filepath.Join(t.TempDir(), "drop.zip"), "")

	missing := filepath.Join(t.TempDir(), "does-not-exist.zip")
	_, ingested, err := ws.IngestAutosave(ctx, "node-missing", missing)
	if err == nil {
		t.Fatalf("IngestAutosave on missing file: want error, got nil")
	}
	if ingested {
		t.Fatalf("ingested = true on missing file, want false")
	}

	// No head move, no new revision.
	got, err := ws.store().GetWorld(ctx, world.ID)
	if err != nil {
		t.Fatalf("GetWorld: %v", err)
	}
	if got.HeadRevisionID == nil || *got.HeadRevisionID != rev1.ID {
		t.Fatalf("head moved on a missing-file ingest: head = %v, want %d", got.HeadRevisionID, rev1.ID)
	}
	revs, err := ws.store().RevisionsForWorld(ctx, world.ID)
	if err != nil {
		t.Fatalf("RevisionsForWorld: %v", err)
	}
	if len(revs) != 1 {
		t.Fatalf("RevisionsForWorld = %d, want 1", len(revs))
	}
}

func TestIngestAutosave_NoBoundWorld(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	newReloadFakeNode(t, ws, "node-unbound", "", filepath.Join(t.TempDir(), "drop.zip"), "")

	if _, _, err := ws.IngestAutosave(ctx, "node-unbound", fixturePath(t, fixtureSmall)); err == nil {
		t.Fatalf("IngestAutosave with no bound world: want error, got nil")
	}
}

func TestIngestAutosave_UnknownNode(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	if _, _, err := ws.IngestAutosave(ctx, "ghost", fixturePath(t, fixtureSmall)); err == nil {
		t.Fatalf("IngestAutosave on unknown node: want error, got nil")
	}
}
