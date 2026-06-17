package workspace

import (
	"context"
	"errors"
	"testing"
)

// TestLoad_MirrorOnlyHeadRefused verifies that Load returns ErrNotRematerializable
// (errors.Is-matchable) when the world's head revision has been flipped to
// mirror_only via a direct catalog UPDATE (the G4 guard site in working_set.go).
// This uses forceHeadMirrorOnly (flag-only flip, no byte deletion) which is the
// cheap counterpart to eviction_test.go's full-eviction proof.
func TestLoad_MirrorOnlyHeadRefused(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	world, err := ws.AddWorld(ctx, fixturePath(t, fixtureA), "world-a")
	if err != nil {
		t.Fatalf("AddWorld: %v", err)
	}

	// Flip the head to mirror_only without deleting bytes (flag-only, G4 guard fires
	// on blob_present=0 before any blobstore Get).
	forceHeadMirrorOnly(t, ctx, ws, world.ID)

	// Evict the in-memory handle so Load must re-read the registry row.
	if err := ws.Unload(world.ID); err != nil {
		t.Fatalf("Unload: %v", err)
	}

	_, loadErr := ws.Load(ctx, world.ID)
	if !errors.Is(loadErr, ErrNotRematerializable) {
		t.Fatalf("Load on mirror_only head: err = %v, want ErrNotRematerializable", loadErr)
	}
}

// TestOpenWorld_MirrorOnlyHeadRefused verifies that OpenWorld propagates
// ErrNotRematerializable when the world's head is mirror_only. OpenWorld delegates
// to Load on a cache miss, so the %w wrap chain must carry the sentinel through.
func TestOpenWorld_MirrorOnlyHeadRefused(t *testing.T) {
	ctx := context.Background()
	ws := newWorkspace(t, ctx)

	world, err := ws.AddWorld(ctx, fixturePath(t, fixtureA), "world-b")
	if err != nil {
		t.Fatalf("AddWorld: %v", err)
	}

	// Flip the head to mirror_only.
	forceHeadMirrorOnly(t, ctx, ws, world.ID)

	// Ensure the world is not already loaded so OpenWorld takes the slow path.
	if err := ws.Unload(world.ID); err != nil {
		t.Fatalf("Unload: %v", err)
	}

	_, openErr := ws.OpenWorld(ctx, world.ID)
	if !errors.Is(openErr, ErrNotRematerializable) {
		t.Fatalf("OpenWorld on mirror_only head: err = %v, want ErrNotRematerializable", openErr)
	}
}
