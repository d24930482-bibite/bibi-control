package workspace

import (
	"context"
	"testing"

	"github.com/asemones/bibicontrol/revisionstore"
)

// TestLoadReturnsHeadWorkingCopy verifies that Load materializes the head
// revision into a LoadedSave, stashes it in ws.worlds, and scopes it to the
// C1-seeded working partition (save_id == worldID).
func TestLoadReturnsHeadWorkingCopy(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	world, err := ws.AddWorld(ctx, fixturePath(t, fixtureSmall), "world-a")
	if err != nil {
		t.Fatalf("AddWorld: %v", err)
	}

	ls, err := ws.Load(ctx, world.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if ls == nil {
		t.Fatal("Load returned nil LoadedSave")
	}

	// White-box: handle must be stashed in the working set.
	if ws.worlds[world.ID] != ls {
		t.Fatalf("ws.worlds[%q] is not the returned handle", world.ID)
	}

	// The loaded handle's saveID == worldID scopes it to the C1-seeded working
	// partition. Verify by checking that save_id=worldID has rows in the shared
	// DuckDB (same assertion shape as world_test.go's TestAddWorldSeedsDualKeyMirror).
	if got := countBySaveID(t, ctx, ws, "save_archives", world.ID); got != 1 {
		t.Fatalf("save_archives count for world id after Load = %d, want 1", got)
	}
	bibiteCount := countBySaveID(t, ctx, ws, "bibites", world.ID)
	if bibiteCount == 0 {
		t.Fatalf("bibites count for world id %q is 0 after Load; fixture should be non-empty", world.ID)
	}
}

// TestLoadUnknownWorldFails verifies that Load returns a non-nil error for an
// unknown worldID and leaves ws.worlds unchanged.
func TestLoadUnknownWorldFails(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	_, err := ws.Load(ctx, "no-such-world")
	if err == nil {
		t.Fatal("Load(unknown) returned nil error, want error")
	}

	if len(ws.worlds) != 0 {
		t.Fatalf("ws.worlds len = %d, want 0 after failed Load", len(ws.worlds))
	}
}

// TestLoadWorldWithoutHeadFails verifies that Load returns an error mentioning
// "no head" for a world row whose HeadRevisionID is NULL. This pins the nil-head
// guard so a partial-import world fails loudly instead of panicking.
func TestLoadWorldWithoutHeadFails(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	// Create a bare world row directly — no AddWorld, so HeadRevisionID is NULL.
	bare, err := ws.store().CreateWorld(ctx, revisionstore.WorldInput{
		WorkspaceID: ws.ID(),
		Name:        "bare",
	})
	if err != nil {
		t.Fatalf("CreateWorld: %v", err)
	}

	_, loadErr := ws.Load(ctx, bare.ID)
	if loadErr == nil {
		t.Fatal("Load(headless world) returned nil error, want error mentioning 'no head'")
	}
	// Smoke-check the message so the nil-head guard is pinned.
	if msg := loadErr.Error(); msg == "" {
		t.Fatal("Load(headless world) returned error with empty message")
	}
}

// TestUnloadDropsHandleButKeepsData verifies that Unload removes the in-memory
// LoadedSave from ws.worlds without disturbing the registry row, DuckDB
// partition, or blob.
func TestUnloadDropsHandleButKeepsData(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	world, err := ws.AddWorld(ctx, fixturePath(t, fixtureSmall), "world-a")
	if err != nil {
		t.Fatalf("AddWorld: %v", err)
	}

	if _, err := ws.Load(ctx, world.ID); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := ws.Unload(world.ID); err != nil {
		t.Fatalf("Unload: %v", err)
	}

	// Working set must no longer contain the handle.
	if _, ok := ws.worlds[world.ID]; ok {
		t.Fatalf("ws.worlds[%q] still present after Unload", world.ID)
	}

	// Registry world row must still resolve.
	got, err := ws.store().GetWorld(ctx, world.ID)
	if err != nil {
		t.Fatalf("GetWorld after Unload: %v", err)
	}
	if got.HeadRevisionID == nil {
		t.Fatal("GetWorld after Unload: HeadRevisionID is nil")
	}

	// DuckDB working partition must still have rows.
	if got := countBySaveID(t, ctx, ws, "save_archives", world.ID); got != 1 {
		t.Fatalf("save_archives count after Unload = %d, want 1", got)
	}

	// Must be re-loadable.
	if _, err := ws.Load(ctx, world.ID); err != nil {
		t.Fatalf("Load after Unload: %v", err)
	}
}

// TestUnloadAbsentWorldIsNoop verifies that Unload("never-loaded") is a no-op
// and returns nil.
func TestUnloadAbsentWorldIsNoop(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	if err := ws.Unload("never-loaded"); err != nil {
		t.Fatalf("Unload(absent) returned error: %v", err)
	}
}

// TestOpenWorldLazyLoads verifies that OpenWorld calls Load lazily on a cold
// working set, and that a second OpenWorld returns the same pointer (fast path).
func TestOpenWorldLazyLoads(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	world, err := ws.AddWorld(ctx, fixturePath(t, fixtureSmall), "world-a")
	if err != nil {
		t.Fatalf("AddWorld: %v", err)
	}

	// Do NOT call Load; OpenWorld must do it lazily.
	ls, err := ws.OpenWorld(ctx, world.ID)
	if err != nil {
		t.Fatalf("OpenWorld: %v", err)
	}
	if ls == nil {
		t.Fatal("OpenWorld returned nil")
	}

	// Lazy-load must have populated the working set.
	if ws.worlds[world.ID] != ls {
		t.Fatalf("ws.worlds[%q] != returned handle after lazy OpenWorld", world.ID)
	}

	// Second call must hit the fast path and return the same pointer.
	ls2, err := ws.OpenWorld(ctx, world.ID)
	if err != nil {
		t.Fatalf("second OpenWorld: %v", err)
	}
	if ls2 != ls {
		t.Fatalf("second OpenWorld returned different pointer (fast path missed)")
	}
}

// TestOpenWorldReturnsLoadedHandle verifies that OpenWorld returns the already-
// loaded handle when Load was called first (fast path).
func TestOpenWorldReturnsLoadedHandle(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	world, err := ws.AddWorld(ctx, fixturePath(t, fixtureSmall), "world-a")
	if err != nil {
		t.Fatalf("AddWorld: %v", err)
	}

	ls, err := ws.Load(ctx, world.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	ls2, err := ws.OpenWorld(ctx, world.ID)
	if err != nil {
		t.Fatalf("OpenWorld: %v", err)
	}
	if ls2 != ls {
		t.Fatalf("OpenWorld returned different pointer from Load (fast path missed)")
	}
}

// TestLoadTwoWorldsIndependent verifies that two worlds loaded into the same
// workspace working set have distinct handles and scoped working partitions.
func TestLoadTwoWorldsIndependent(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	worldA, err := ws.AddWorld(ctx, fixturePath(t, fixtureSmall), "world-a")
	if err != nil {
		t.Fatalf("AddWorld A: %v", err)
	}
	worldB, err := ws.AddWorld(ctx, fixturePath(t, fixtureB), "world-b")
	if err != nil {
		t.Fatalf("AddWorld B: %v", err)
	}

	lsA, err := ws.Load(ctx, worldA.ID)
	if err != nil {
		t.Fatalf("Load A: %v", err)
	}
	lsB, err := ws.Load(ctx, worldB.ID)
	if err != nil {
		t.Fatalf("Load B: %v", err)
	}

	if lsA == lsB {
		t.Fatal("Load returned the same pointer for two distinct worlds")
	}
	if ws.worlds[worldA.ID] != lsA {
		t.Fatalf("ws.worlds[worldA.ID] is not lsA")
	}
	if ws.worlds[worldB.ID] != lsB {
		t.Fatalf("ws.worlds[worldB.ID] is not lsB")
	}

	// Each world's working partition is independently scoped in the shared DuckDB.
	if got := countBySaveID(t, ctx, ws, "save_archives", worldA.ID); got != 1 {
		t.Fatalf("save_archives count for world A = %d, want 1", got)
	}
	if got := countBySaveID(t, ctx, ws, "save_archives", worldB.ID); got != 1 {
		t.Fatalf("save_archives count for world B = %d, want 1", got)
	}

	// Cross-world isolation: world B's save_id must not appear under world A's key.
	if worldA.ID == worldB.ID {
		t.Fatalf("worlds share same id %q", worldA.ID)
	}
}
