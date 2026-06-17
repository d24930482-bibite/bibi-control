package workspace

import (
	"context"
	"fmt"
	"os"

	"github.com/asemones/bibicontrol/revisionstore"
	"github.com/asemones/bibicontrol/script/thebibites"
)

// Load materializes the world's head revision blob to a temp file, builds a
// LoadedSave via LoadInto (so its saveID == worldID and it reuses the shared
// per-workspace DuckDB handle — the working partition C1 already seeded under
// save_id=worldID), stashes it in w.worlds, and returns it.
//
// Load is idempotent-ish: re-loading replaces the stashed handle. The old
// handle is discarded (LoadedSave owns no closeable resource; ls.db is the
// shared workspace handle that LoadInto never closes).
//
// Lock discipline: Load acquires w.mu for the entire body. This is the same
// whole-body-lock shape importWorldFromArchive uses (world.go:81). Load does a
// temp-file write + a ParseFile (inside LoadInto) but NO DuckDB write (the
// injected handle short-circuits the in-memory import, loadedsave.go:420-441),
// so it does not contend with the single-writer rule and holding the lock for
// the full body is correct and race-free.
func (w *Workspace) Load(ctx context.Context, worldID string) (*thebibites.LoadedSave, error) {
	if w == nil {
		return nil, fmt.Errorf("workspace: Load on nil workspace")
	}
	if worldID == "" {
		return nil, fmt.Errorf("workspace: worldID is required")
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// 1. Resolve the world row and its head revision id.
	world, err := w.store().GetWorld(ctx, worldID)
	if err != nil {
		if revisionstore.IsNotFound(err) {
			return nil, fmt.Errorf("workspace: world %q not found", worldID)
		}
		return nil, fmt.Errorf("workspace: get world %q: %w", worldID, err)
	}

	// 2. Head guard: a head-less world (e.g. partial C1 import) must fail
	// loudly instead of panicking on the nil dereference.
	if world.HeadRevisionID == nil {
		return nil, fmt.Errorf("workspace: world %q has no head revision", worldID)
	}

	// 3. Fetch the head revision metadata.
	rev, err := w.store().RevisionByID(ctx, *world.HeadRevisionID)
	if err != nil {
		return nil, fmt.Errorf("workspace: get head revision for world %q: %w", worldID, err)
	}

	// 4. Blob-present guard (defense-in-depth; a well-formed world's head is
	// never evictable, but guard anyway so a catalog-flipped head fails loud
	// with the typed sentinel rather than an opaque blobstore miss downstream).
	if !rev.BlobPresent {
		return nil, notRematerializable(worldID, rev.ID, "load")
	}

	// 5. Fetch the head blob bytes from the content-addressed store.
	data, err := w.blobs().Get(ctx, rev.BlobRef)
	if err != nil {
		return nil, fmt.Errorf("workspace: get blob for world %q revision %d: %w", worldID, rev.ID, err)
	}

	// 6. Materialize the blob to a temp file so LoadInto (path-only) can parse
	// it. Mirror the temp-file shape from AddWorldBytes (world.go:51-63) and
	// the verify pattern (loadedsave.go:643-655).
	//
	// Temp-file lifetime: LoadInto records ls.path = tmpPath, but
	// WriteSave/prepareCommit (loadedsave.go:561-618) serialize from the
	// in-memory session — they do NOT re-read ls.path. The parsed archive lives
	// in memory after LoadInto returns, so defer os.Remove is safe here.
	// (Verified against loadedsave.go:561-618: prepareCommit uses
	// WriteArchiveTo(&buf, ls.session.Archive()) — no disk read of ls.path.)
	tmp, err := os.CreateTemp("", "bibiload-*.zip")
	if err != nil {
		return nil, fmt.Errorf("workspace: create temp file for world %q: %w", worldID, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("workspace: write temp file for world %q: %w", worldID, err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("workspace: close temp file for world %q: %w", worldID, err)
	}

	// 7. Build the LoadedSave. LoadInto:
	//   (a) parses the bytes from the temp file (the one parse on the load path),
	//   (b) sets ls.saveID = worldID so every mirror locator/fromClause scopes to
	//       the save_id=worldID working partition C1 seeded,
	//   (c) injects w.duck() so the lazy openDB returns the shared handle without
	//       importing (loadedsave.go:427-428). LoadInto does NOT import any rows.
	ls, err := thebibites.LoadInto(tmpPath, worldID, w.duck())
	if err != nil {
		return nil, fmt.Errorf("workspace: LoadInto for world %q: %w", worldID, err)
	}

	// 8. Stash and return. If a handle was already loaded under worldID, overwrite
	// it (the new handle reflects the current head). The old handle is discarded —
	// LoadedSave owns no closeable resource of its own; ls.db is the caller-owned
	// shared handle that LoadInto never closes (loadedsave.go:145).
	w.worlds[worldID] = ls
	return ls, nil
}

// Unload drops the in-memory LoadedSave for worldID from the working set. The
// registry row, DuckDB partitions, and blobs all persist untouched.
// Unloading an absent world is a no-op. The error return is for symmetry and
// forward-compat; Unload cannot currently fail.
func (w *Workspace) Unload(worldID string) error {
	if w == nil {
		return fmt.Errorf("workspace: Unload on nil workspace")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	// delete on an absent key is a no-op in Go.
	delete(w.worlds, worldID)
	return nil
}

// OpenWorld returns the working-set handle for reads/mutations, lazily calling
// Load if the world is not already loaded.
//
// Lock discipline: OpenWorld takes w.mu only for the map peek, then releases it
// before delegating to Load (which re-takes w.mu). Never nest w.mu — it is a
// non-reentrant sync.Mutex; see the identical caution in node.go's StartNode
// (node.go:94-121).
//
// Benign race: two concurrent OpenWorld calls that both miss the fast path will
// both call Load. Load's final w.worlds[worldID] = ls is last-writer-wins under
// the lock; both callers get a valid head-loaded handle, and the loser's handle
// is GC'd. LoadedSave owns nothing closeable (ls.db is the shared workspace
// handle), so there is no leaked resource.
func (w *Workspace) OpenWorld(ctx context.Context, worldID string) (*thebibites.LoadedSave, error) {
	if w == nil {
		return nil, fmt.Errorf("workspace: OpenWorld on nil workspace")
	}
	if worldID == "" {
		return nil, fmt.Errorf("workspace: worldID is required")
	}

	// Fast path: world is already loaded.
	w.mu.Lock()
	if ls, ok := w.worlds[worldID]; ok {
		w.mu.Unlock()
		return ls, nil
	}
	w.mu.Unlock()

	// Lazy-load: Load acquires w.mu internally — do not hold it here.
	return w.Load(ctx, worldID)
}
