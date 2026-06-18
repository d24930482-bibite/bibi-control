# C4b Review — query catalog: batch mirror_saves rebuild + cache/invalidate (fix N+1)

Reviewer verdict on the canonical perf ticket. Perf lens applied as a first-class gate.

## Scope / spec faithfulness
Source spec `docs/workspace_plan.md#c4` (the mirror_saves catalog: per-revision
sha256 -> world_id, sim_time, tier attribution; read-only enforced). The plan
(C4b.plan.md) faithfully narrows to the perf problem only — batch the rebuild +
fingerprint-cache it — with the explicit constraint that query *results* are
unchanged and the read-only gate + dual-key semantics stay byte-for-byte. The
implementation matches the plan; no requirement dropped or contradicted.

## 1. The N+1 is GONE, not relocated (perf lens)
Read `refreshMirrorCatalog` (workspace/query.go) end to end. Per rebuild it does:
- ONE fingerprint aggregate: `Store.CatalogFingerprint` is a single `QueryRowContext`
  over the `save_revisions JOIN worlds WHERE workspace_id=?` aggregate (store.go) —
  not a per-revision scan.
- ONE workspace revision read: `RevisionsForWorkspace` is one JOIN (replaces
  ListWorlds + per-world RevisionsForWorld loop); no per-world loop remains.
- ONE set-based scenes read: `readSceneSimTimes` runs a single
  `SELECT save_id, simulated_time FROM scenes WHERE has_simulated_time` into a
  `map[string]float64`. No `QueryRowContext` in a loop anywhere.
- CHUNKED multi-row INSERTs: one `ExecContext` per `catalogInsertChunk=500`-row
  chunk (binding 2500 params/chunk, well under the bound-param ceiling), i.e.
  ceil(R/500) execs, never R.

No residual per-revision round-trip hides in a helper (verified both helpers).
The perf-shape test `TestBatchedRebuildSingleScenesRead` instruments
`scenesReadCount` and `insertExecCount` and asserts exactly 1 scenes read and
ceil(R/chunk) inserts; its failure messages name the per-revision counts (R) it
would see if the loop returned, so it genuinely fails on regression. Verified the
counters are bumped only in the actual rebuild path.

## 2. Cache / invalidation correctness (the real risk)
- Cache hit does ZERO duckdb work: gate returns before `db.Conn` when
  `catalogBuilt && fp == catalogFP`. `TestCatalogCachedAcrossRepeatQueries` proves
  K=5 repeat queries => rebuildCount==1.
- New revision forces rebuild: AUTOINCREMENT id moves MaxID+Count;
  `TestCatalogRebuildsOnNewRevision` proves 1 -> 2.
- G2 in-place flip forces rebuild AND yields correct row:
  `TestCatalogRebuildsOnTierFlip` evicts a non-head revision and asserts
  rebuildCount 1->2 AND the rebuilt mirror_saves row shows tier='mirror_only',
  blob_present=false. This is the correctness proof a relocated-but-stale cache
  would fail.
- StateSum is id-weighted: per-row contribution is
  `id*(tier in {1,2}) + id*(blob_present in {4,8})`. tier/blob_present are
  schema-constrained to {full,mirror_only}/{0,1}, so the CASE branches are exact.
  A real G2 evict (full,1 -> mirror_only,0) is delta +5*id; promote is -5*id —
  always nonzero for id>=1. A genuine two-row SWAP is (a-b)*(m(S2)-m(S1)), which
  is nonzero for distinct ids and distinct states — the swap-to-zero the ticket
  flagged is provably impossible. `TestCatalogFingerprintMovesOnFlip` pins that an
  in-place flip moves StateSum while Count/MaxID stay constant.

  Residual (not a blocker, noted): the linear id-weighted sum is not collision-free
  against >=2 SIMULTANEOUS flips between consecutive queries whose id-weighted
  deltas cancel (e.g. starting from mirror_only={3}, promoting id 3 while evicting
  ids 1+2 nets the same StateSum since 3 == 1+2). This requires >=3 revisions and
  multiple flips in one query-gap with id-sums cancelling — far outside the
  realistic single-op G2/G3 eviction/promote pattern, and the plan explicitly
  specifies this exact id-weighted form and only requires SWAP-safety (met). Flagged
  for the record; does not violate the stated invariant.

## 3. Safety seams untouched
Diff touches only revisionstore/store.go(+test), workspace/query.go,
workspace/workspace.go, workspace/query_test.go. ensureReadOnly/scanForbidden/
forbiddenVerbs, the world_saves CTE prepend, and sql.go/commit.go/import.go/
eviction.go are NOT in the diff. `mirrorCatalogDDL` table shape (save_id, world_id,
tier, blob_present, sim_time) is byte-unchanged — only its doc comment changed.
Dual-key sha256 semantics preserved (rebuild reads sha256-keyed revisions; working
key never enters mirror_saves). NULL sim_time fidelity preserved via map-miss
(absent save_id -> nil -> SQL NULL); `TestHistoryQueryCarriesSimTime` passes
unchanged. Cache fields written ONLY after COMMIT (no poisoning on rollback).
catalogBuilt forces the first/empty-workspace build. INSERT chunk (2500 params)
under the param ceiling. Scenes rows are fully drained into the map before
BEGIN TRANSACTION on the same conn (no busy-conn hazard).

## 4. Tests
Re-ran myself (GOMODCACHE/GOCACHE set):
- `go build ./...` clean.
- `go test -timeout 900s ./revisionstore/... ./workspace/...` => both ok
  (workspace 466s — slow but well under the raised timeout).
- Full `go test -timeout 1200s ./...` => all packages ok.
- Verbose run confirms all 6 C4b tests + the 6 named C4 regression tests execute
  and PASS (not skipped).

VERDICT: PASS
