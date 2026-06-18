# C4b — Query catalog: batch mirror_saves rebuild + cache/invalidate (fix the N+1)

Tier: high — perf hot path on `Query`/`HistoryQuery` + a cache-invalidation
*correctness* invariant (a stale catalog after a G2 in-place tier flip silently
attributes the wrong tier/blob_present, which no relocated-N+1 check would catch) +
the read-only gate and dual-key mirror semantics must stay byte-for-byte untouched.
2 done dependents (C4, G2) but the live risk is the new invalidation keying.

Perf-review: `refreshMirrorCatalog` (workspace/query.go:61) rebuilds the entire
`mirror_saves` catalog on EVERY `Query`/`HistoryQuery`. Current cost is
`O(W + 2R)` SQLite/DuckDB round-trips per query where `W` = worlds and
`R` = retained revision history (R GROWS on every commit/ingest):
`ListWorlds(1)` + `RevisionsForWorld` per world (`W`) + a per-revision
`SELECT simulated_time FROM scenes WHERE save_id=?` (`R`) + a per-revision
`INSERT` (`R`). The reviewer MUST apply the perf lens and verify the N+1 is
**gone, not relocated** (e.g. the batched scenes read is a single set-based
DuckDB query, not a loop of `QueryRowContext`, and the cache short-circuits the
rebuild entirely on an unchanged fingerprint).

## Goal

Make the `mirror_saves` catalog cheap to keep current without changing any query
result. Two independent wins, both required by the user-approved scope:

1. **Batch the rebuild.** When a rebuild *is* needed, do it with a constant number
   of round-trips: one workspace-scoped revision read (replacing
   `ListWorlds` + per-world `RevisionsForWorld`), one set-based DuckDB scenes read
   (replacing the per-revision `scenes` `SELECT`), and one batched multi-row
   `INSERT` (replacing the per-revision `INSERT`). Target: `O(1)` round-trips +
   work linear in row count inside a single statement, not `O(R)` round-trips.
2. **Cache + invalidate.** Skip the rebuild entirely when the underlying revisions
   have not changed since the last rebuild, keyed on a cheap registry fingerprint
   that covers BOTH new revisions (commit/ingest/AddWorld) AND in-place
   tier/blob_present flips (G2 eviction, ReconcileBlobs promote/demote). Rebuild
   only when the fingerprint differs from the cached one.

"Done" = both queries return identical results to today; N commits trigger at most
N rebuilds (one per change), and N *repeat* queries with no intervening mutation
trigger exactly one rebuild total; the read-only gate and dual-key semantics are
untouched; tests prove the rebuild no longer scales per-revision.

## Files to change

- **`revisionstore/store.go`** — add two read-only `*Store` methods (no schema change):
  - `RevisionsForWorkspace(ctx, workspaceID) ([]Revision, error)` — one query that
    returns every revision whose `world_id` belongs to a world in `workspaceID`,
    ordered by `id` (lineage order). Replaces the `ListWorlds` + per-world
    `RevisionsForWorld` loop. SQL: select the same 14 columns
    `RevisionsForWorld` selects (store.go:472-478) `FROM save_revisions r
    JOIN worlds w ON r.world_id = w.id WHERE w.workspace_id = ? ORDER BY r.id`.
    Reuse `usableContext`, `scanRevisionRows`, the existing error-wrapping style.
  - `CatalogFingerprint(ctx, workspaceID) (CatalogFingerprint, error)` (struct or a
    comparable tuple) — ONE aggregate over the same workspace-scoped join that
    captures change in three orthogonal dimensions so any catalog-affecting mutation
    moves it:
      - `Count = COUNT(*)` (catches deletes if they ever happen / sanity),
      - `MaxID = COALESCE(MAX(r.id), 0)` (catches new revisions — ids are
        AUTOINCREMENT, store.go schema:41),
      - `StateSum` — a checksum over `(tier, blob_present)` that changes on an
        in-place flip even when count and max id are constant, e.g.
        `SUM(r.id * (CASE WHEN r.tier='full' THEN 1 ELSE 2 END) +
        r.id * CASE WHEN r.blob_present THEN 4 ELSE 8 END)` or equivalently
        `SUM((r.tier='full') + 2*(r.blob_present)) ...`. The exact expression is the
        executor's call; the REQUIREMENT is: it is deterministic, computed in one
        SQLite statement, and provably changes value when ANY row's `tier` or
        `blob_present` flips (a G2 evict flips `full,1 -> mirror_only,0`; a G3
        promote flips back). Multiply each row's state contribution by `r.id` (or
        another per-row distinguisher) so two rows swapping states do not cancel.
  - Define an exported `CatalogFingerprint` value type (e.g.
    `type CatalogFingerprint struct { Count, MaxID, StateSum int64 }`) that is
    comparable with `==` so the workspace can cache and compare it directly.

- **`workspace/query.go`** — the heart of the change:
  - Add cache fields to the rebuild flow (see Workspace struct change below).
  - Rewrite `refreshMirrorCatalog(ctx)` into a fingerprint-gated batched rebuild
    (signature unchanged: `func (w *Workspace) refreshMirrorCatalog(ctx) error`,
    still called under `w.mu`). New body:
      1. Compute `fp := w.store().CatalogFingerprint(ctx, w.ID())`.
      2. If the catalog has been built at least once AND `fp == w.catalogFP`,
         return nil (NO DuckDB work — this is the cache hit that kills
         rebuild-on-every-query).
      3. Otherwise rebuild: `revs := w.store().RevisionsForWorkspace(ctx, w.ID())`
         (one round-trip); read all per-revision sim_times in ONE DuckDB query:
         `SELECT save_id, simulated_time FROM scenes WHERE has_simulated_time`
         into a `map[string]float64` (one round-trip; the predicate matches the
         current per-revision `WHERE save_id=? AND has_simulated_time` so NULL/
         absent stays NULL via map-miss); then inside the existing
         BEGIN/DELETE/COMMIT transaction, batch the INSERT (one multi-row
         `INSERT ... VALUES (?,?,?,?,?),(?,?,?,?,?),...` built from `revs`, or a
         prepared statement reused across rows on the SAME conn — either is
         acceptable as long as it is not a fresh `ExecContext` round-trip pattern
         that re-derives sim_time per row). Watch DuckDB's bound-parameter ceiling:
         if `len(revs)*5` could exceed a safe batch size, chunk into fixed-size
         batches (e.g. 500 rows) — still `O(R/batch)` not `O(R)`, and document the
         chunk constant.
      4. On successful COMMIT, set `w.catalogFP = fp` and `w.catalogBuilt = true`.
         Set these ONLY after COMMIT so a failed/rolled-back rebuild does not poison
         the cache into thinking it is current.
  - `mirrorCatalogDDL` and the table shape (`save_id, world_id, tier, blob_present,
    sim_time`) are UNCHANGED. The map-miss-for-NULL behavior must reproduce the
    current `*float64` nil semantics exactly (sim_time DOUBLE, NULL when no scene
    row / `has_simulated_time` false).
  - `Query` (query.go:149) and `HistoryQuery` (query.go:182) bodies are UNCHANGED
    except that they keep calling `refreshMirrorCatalog` under `w.mu` exactly as
    today — the gate now no-ops on a cache hit. Do NOT move the lock, do NOT touch
    `ensureReadOnly`/`scanForbidden`, do NOT change the `world_saves` CTE wrap.

- **`workspace/workspace.go`** — add two unexported fields to `Workspace` (struct at
  workspace.go:28), guarded by the existing `w.mu` (the rebuild already runs under
  `w.mu`, so no new lock):
  - `catalogFP revisionstore.CatalogFingerprint` — last-built fingerprint.
  - `catalogBuilt bool` — whether a rebuild has happened (so the zero fingerprint of
    a brand-new empty workspace does not read as a false cache hit; alternatively
    fold this into the fingerprint being a pointer — executor's call, but the
    empty-workspace-first-query case MUST build at least once).
  No init needed in `Create`/`Open` (zero values are correct: not built, empty fp).

## Approach (step by step, reusing existing helpers)

1. **Pin the current shape** (already measured above): the loop at query.go:102-133
   is the N+1. The two registry round-trips per query are `ListWorlds` (query.go:97)
   + `RevisionsForWorld` per world (query.go:103); the two DuckDB round-trips per
   revision are the `scenes` `QueryRowContext` (query.go:116) + the `INSERT`
   (query.go:125).
2. **Add `RevisionsForWorkspace`** mirroring `RevisionsForWorld` (store.go:463) but
   with the `JOIN worlds` workspace filter. This collapses `W+1` registry
   round-trips into 1 and naturally excludes revisions whose world is in a
   *different* workspace sharing the registry (the registry is the shared
   `metadata.sqlite`, workspace.go:54 — scoping is load-bearing).
3. **Add `CatalogFingerprint`** as one aggregate over the same join.
4. **Rewrite `refreshMirrorCatalog`** per the steps above. Keep the
   `db.Conn(ctx)` + `BEGIN TRANSACTION`/`DELETE FROM mirror_saves`/`COMMIT` /
   deferred `ROLLBACK` transaction shape exactly (query.go:67-94, 135-139) so the
   catalog is never half-rebuilt; only the *enumeration* between DELETE and COMMIT
   changes from a per-revision loop to a batched read + batched insert. Reuse the
   `mirrorCatalogDDL` `CREATE TABLE IF NOT EXISTS` (query.go:74) unchanged.
5. **Wire the cache fields** and the fingerprint compare/short-circuit. Because the
   rebuild always runs under `w.mu` (held by `Query`/`HistoryQuery` around the
   call, query.go:154-159 / 200-205), the fields need no extra synchronization.

### Invalidation: who invalidates (the keying, not hooks)

We deliberately do NOT add a dirty flag to every mutator (CommitWorld,
appendIngestedRevision, importWorldFromArchive/AddWorld, EvictWorldHistory,
EvictRevisionBlob, ReconcileBlobs). Hooking each is fragile and a future mutator
would forget. Instead the **fingerprint IS the invalidation key**: every
catalog-affecting mutation already lands in `save_revisions` and so moves the
fingerprint for free:
  - new revision (CommitWorld via `RecordRevisionAdvancingHead` commit.go:81 path;
    IngestAutosave/`appendIngestedRevision` node_reload.go:353; AddWorld via
    `RecordRevisionAdvancingHead` world.go:131) → new max id + new count.
  - G2 evict (`EvictRevisionBlob` -> `tier='mirror_only', blob_present=0`,
    store.go:1012) and ReconcileBlobs `MarkMirrorOnly`/`PromoteRevision`
    (eviction.go:315/345, store.go:1101) → in-place flip caught by `StateSum`.
This is why C4b depends on G2: the in-place-flip dimension of the fingerprint is
the part that keeps the cache correct across an eviction. The plan keys on
`(Count, MaxID, StateSum)` precisely so all three mutation shapes are covered by
ONE read.

## Tests

White-box, package `workspace`, in `workspace/query_test.go` (reuses
`newWorkspace`, `fixturePath`, `fixtureA/B`, `countBySaveID` from world_test.go).
A counting seam is needed to prove "no per-revision round-trip". Add a small,
test-only instrumentation hook — the cleanest seam without touching prod
control flow is a package-level counter incremented inside `refreshMirrorCatalog`
ONLY on an actual rebuild (not on a cache-hit early return), e.g. an unexported
`int64` field on `Workspace` (`rebuildCount`) bumped right after a successful
COMMIT, or a free `var catalogRebuilds int64` package var. Prefer a per-Workspace
counter to avoid cross-test interference under `-race`/parallel. Document it as a
test seam in the prod comment.

Add these cases:
1. **`TestCatalogCachedAcrossRepeatQueries`** — AddWorld×2, then run `Query`
   (or `HistoryQuery`) K times (K=5) with NO intervening mutation. Assert
   `rebuildCount == 1` after all K calls (first builds, rest are cache hits).
   This is the core "N queries don't trigger N rebuilds" proof.
2. **`TestCatalogRebuildsOnNewRevision`** — AddWorld, Query (rebuild #1),
   Query again (cache hit, still #1), then CommitWorld a mutation (new revision),
   then Query → assert `rebuildCount == 2` (the new revision moved the fingerprint).
   Use the existing commit fixture/program pattern from `commit_test.go`. If a
   commit fixture is heavy, an alternate trigger is a second `AddWorld` (new
   world+revision) between queries — still proves a new revision invalidates.
3. **`TestCatalogRebuildsOnTierFlip`** — AddWorld with ≥2 revisions for one world
   (commit once so there is a non-head revision), Query (build), Query (cache hit),
   then `EvictRevisionBlob` the non-head revision (G2 in-place flip), then Query →
   assert `rebuildCount` increased AND the resulting `mirror_saves` row for that
   sha256 now shows `tier='mirror_only', blob_present=false`. This is the
   correctness proof that the `StateSum` dimension catches an in-place flip the
   count/max-id dimensions would MISS — the bug a relocated-but-still-stale cache
   would ship.
4. **`TestBatchedRebuildSingleScenesRead` (perf-shape assertion)** — the
   relocation guard. Either (a) instrument a second counter
   (`scenesReadCount`/`insertExecCount`) and assert that one rebuild over R
   revisions performs exactly ONE scenes read and at most `ceil(R/batch)` insert
   execs (NOT R) — proving the per-revision DuckDB loop is gone; or (b) if adding
   two counters is too invasive, assert `rebuildCount` invariance across many
   queries (case 1) AND keep a comment that the reviewer must read the rebuild body
   to confirm the scenes read is set-based. Prefer (a): it is the assertion that
   most directly proves the N+1 is gone, not relocated.
5. **Regression: existing C4 tests must still pass unchanged** —
   `TestQueryWholeWorkspaceSpansAllWorlds`, `TestQueryAttributesViaMirrorCatalog`,
   `TestHistoryQueryScopesToOneWorld`, `TestHistoryQueryCarriesSimTime`,
   `TestReadOnlyRejectsMutations`, `TestQueryRejectsBeforeTouchingDB`. The
   read-only and dual-key behavior is verified by these; do not modify them.
   In particular `TestHistoryQueryCarriesSimTime` pins that the batched scenes
   read reproduces the exact per-revision sim_time (sourced from scenes, not the
   SQLite head) — if the map-keyed read drops a value it fails here.

Also add a store-level test in `revisionstore/store_test.go`:
6. **`TestRevisionsForWorkspaceScopesByWorkspace`** — two workspaces in one
   registry, each with a world+revision; assert `RevisionsForWorkspace(wsA)`
   returns only wsA's revision (the shared-registry scoping invariant), ordered by
   id.
7. **`TestCatalogFingerprintMovesOnFlip`** — record a revision, snapshot the
   fingerprint, `EvictRevisionBlob` it (or MarkMirrorOnly), re-read the
   fingerprint, assert it differs even though count and max id are unchanged.

Command to run:
```
cd /home/asemones/Documents/personal_code/bibicontrol-wt/C4b && \
  go test ./workspace/... ./revisionstore/... && \
  go build ./...
```

## Risks / seams (do NOT break)

- **Read-only gate is sacred.** `ensureReadOnly`, `scanForbidden`, `nextToken`,
  `forbiddenVerbs` (query.go:233-404) and the `world_saves` CTE prepend
  (query.go:210) are untouched. The rebuild change is confined to
  `refreshMirrorCatalog`; the gate runs BEFORE the rebuild in both entry points and
  must keep running first.
- **Dual-key semantics.** `mirror_saves` carries only sha256 history keys, never the
  world-id working key (query.go:181). The batched rebuild reads revisions
  (sha256-keyed) exactly as the loop did; the working partition is owned by
  CommitWorld (commit.go:136) and must not leak into `mirror_saves`. The batched
  scenes read uses `save_id` = sha256, same as the per-revision query.
- **NULL sim_time fidelity.** The current code leaves `simTime` nil on
  `sql.ErrNoRows` / `has_simulated_time = false`. The batched map MUST reproduce
  this: a revision whose sha256 is absent from the scenes map (no row or
  `has_simulated_time` false, since the batched read filters
  `WHERE has_simulated_time`) inserts SQL NULL, not 0.0.
  `TestHistoryQueryCarriesSimTime` guards the present case; consider a fixture with
  a missing/absent sim_time to guard the NULL case if available.
- **Cache poisoning on partial rebuild.** Set `catalogFP`/`catalogBuilt` ONLY after
  the DuckDB COMMIT succeeds. If the rebuild errors mid-way (rolled back), leave the
  cache as-is so the next query retries — never mark a failed rebuild as current.
- **First-query / empty-workspace.** A brand-new workspace has the zero fingerprint;
  `catalogBuilt=false` must force the first build so `mirror_saves` exists (the DDL
  + an empty/rebuilt table). Do not let `fp == zero` short-circuit the very first
  build.
- **Concurrency / lock discipline.** The cache fields are read+written only inside
  `refreshMirrorCatalog`, which is always called holding `w.mu` (query.go:154/200).
  Do NOT read them outside the lock and do NOT take a second lock (w.mu is
  non-reentrant — commit.go:31). The fingerprint query and scenes read happen under
  `w.mu` like the current loop, so no new lock-ordering risk vs. C2/D2 mutators.
- **Bound-parameter ceiling.** A single mega-INSERT with `5*R` bound params can hit
  the driver's parameter limit as R grows over the system's life. Chunk the batched
  INSERT (fixed batch size) so the fix does not itself reintroduce a scaling cliff;
  this stays well below `O(R)` round-trips.
- **Fingerprint collisions.** The `StateSum` must be designed so two rows swapping
  states cannot net to zero (multiply each row's state by `r.id` or another
  per-row key). A naive `SUM(tier-as-int)` would miss an A↔B swap; the test in case
  7 + the chosen multiply-by-id form must rule this out. This is the subtle
  invariant the high tier is protecting.
- **Perf-review (reviewer must verify, lens ON):** confirm (1) the scenes read is a
  single set-based DuckDB query (no `QueryRowContext` in a loop), (2) the INSERT is
  batched/chunked (not one `ExecContext` per revision), (3) the cache short-circuits
  the entire rebuild on an unchanged fingerprint (the test proves N queries → 1
  rebuild), and critically (4) the N+1 is GONE, not RELOCATED — e.g. the fingerprint
  query itself is a single aggregate, not a per-revision scan, and
  `RevisionsForWorkspace` is one JOIN, not a loop.
