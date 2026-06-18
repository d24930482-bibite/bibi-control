package workspace

// spanning.go — E3 binding glue for the spanning entity collections
// (world.bibites / world.eggs / world.pellets and workspace.bibites / .eggs /
// .pellets). These expose the SAME object DSL (count/sum/mean/median/min/max/
// quantile/group_by/where) as a single open world's s.bibites, but scoped across
// a broader save-partition set: one world's whole retained history, or every
// world's history in the workspace.
//
// Scoping is BY CONSTRUCTION: the collection carries a read-only thebibites
// SaveScope whose WHERE injects `save_id IN (SELECT save_id FROM mirror_saves
// [WHERE world_id = ?])`. The script never writes save_id / a JOIN. The scope is
// read-only — mutation and Entity iteration are rejected (mutation cannot reach
// committed history or other worlds; it stays per-save via world.open()).
//
// The spanning read runs over the HISTORY partitions (mirror_saves carries only
// sha256 history keys, never the world-id working key), so a spanning read sees
// committed history — never another world's uncommitted staged working partition.
//
// Perf shape: spanningReader refreshes the mirror_saves catalog ONCE per call
// (refreshMirrorCatalog, fingerprint-gated under w.mu — a cache hit when nothing
// changed), then hands out the scoped collection. The later .count()/.mean()/…
// runs ONE aggregate SELECT on the shared workspace DuckDB handle. There is no
// per-revision query and no catalog rebuild per .where()/.count().

import (
	"context"
	"fmt"

	"github.com/asemones/bibicontrol/script/thebibites"
)

// spanningReader refreshes the mirror_saves catalog once and returns a
// thebibites.SpanningReader bound to the shared workspace DuckDB handle under the
// given read-only scope. The catalog refresh runs under w.mu (it writes DuckDB);
// the mutex is released before the reader's later aggregate SELECT runs, mirroring
// the HistoryQuery lock-then-release shape so the read does not serialize against
// concurrent mutators.
func (w *Workspace) spanningReader(ctx context.Context, scope thebibites.SaveScope) (*thebibites.SpanningReader, error) {
	w.mu.Lock()
	if err := w.refreshMirrorCatalog(ctx); err != nil {
		w.mu.Unlock()
		return nil, fmt.Errorf("workspace: refresh mirror catalog: %w", err)
	}
	w.mu.Unlock()

	reader, err := thebibites.NewSpanningReader(w.duck(), scope)
	if err != nil {
		return nil, fmt.Errorf("workspace: spanning reader: %w", err)
	}
	return reader, nil
}

// spanningCollection refreshes the catalog once and returns the named spanning
// collection ("bibites"/"eggs"/"pellets") over the given scope. It is the single
// helper both worldValue and workspaceValue route their bibites/eggs/pellets
// attributes through, so the refresh-once-per-call shape lives in one place.
func (w *Workspace) spanningCollection(ctx context.Context, scope thebibites.SaveScope, name string) (*thebibites.EntityCollection, error) {
	reader, err := w.spanningReader(ctx, scope)
	if err != nil {
		return nil, err
	}
	return reader.Collection(name)
}
