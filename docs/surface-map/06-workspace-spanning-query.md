# 06. Workspace spanning reads + query catalog

**Scope (files scanned):** `workspace/spanning.go`, `workspace/query.go`. (Cross-referenced for grounding only, not in slice: `script/thebibites/scope.go`, `workspace/automation.go`.)

## Location map

### `workspace/spanning.go` — the spanning binding glue
- `workspace/spanning.go:39` — [READ] [WORKSPACE] — `spanningReader(ctx, scope)`: refreshes the `mirror_saves` catalog once under `w.mu`, releases the lock, then builds a `thebibites.SpanningReader` over the shared DuckDB handle (`w.duck()`) bound to a read-only `SaveScope`. Lock-then-release mirrors `HistoryQuery` so the later aggregate SELECT does not serialize against mutators.
- `workspace/spanning.go:58` — [READ] [WORKSPACE]/[WORLD] — `spanningCollection(ctx, scope, name)`: single helper both `worldValue` and `workspaceValue` route their `bibites`/`eggs`/`pellets` attrs through; returns the named `*thebibites.EntityCollection` (`reader.Collection(name)`). WORLD vs WORKSPACE is decided entirely by which scope the caller passes.

### `workspace/query.go` — the catalog + raw-SQL escape hatch
- `workspace/query.go:48` — [—] [WORKSPACE] — `mirrorCatalogDDL`: `CREATE TABLE IF NOT EXISTS mirror_saves (save_id TEXT, world_id TEXT, tier TEXT, blob_present BOOLEAN, sim_time DOUBLE)`. The catalog schema; native DuckDB table, rebuilt from the SQLite revision registry, never authored directly.
- `workspace/query.go:86` — [WRITE→DuckDB] [WORKSPACE] — `refreshMirrorCatalog(ctx)`: rebuilds `mirror_saves` from the registry. Fingerprint-gated cache (`w.catalogFP`/`w.catalogBuilt`); skips all DuckDB work on a hit (line 100). On a miss: `RevisionsForWorkspace` (one read), `readSceneSimTimes` (one read), then `DELETE` + chunked multi-row `INSERT` inside one transaction. Must be called under `w.mu`. Writes DuckDB but is itself read-only w.r.t. user data — it only maintains the attribution catalog.
- `workspace/query.go:201` — [READ] [WORKSPACE] — `readSceneSimTimes(ctx, conn)`: one set-based `SELECT save_id, simulated_time FROM scenes WHERE has_simulated_time` into a map keyed by `save_id` (= sha256). A save absent from the map → SQL NULL `sim_time` (preserves `*float64` nil fidelity).
- `workspace/query.go:232` — [READ] [WORKSPACE] — `Query(ctx, query)`: the **raw-SQL escape hatch** over ALL history partitions, every world, no world filter. Gated by `ensureReadOnly`; refreshes the catalog; runs the user SELECT directly on `w.duck()`. Caller must hand-write `JOIN mirror_saves` for `world_id`/`sim_time`/`tier` attribution. No multi-workspace federation. This is `workspace.query(sql=...)`.
- `workspace/query.go:265` — [READ] [WORLD] — `HistoryQuery(ctx, worldID, query)`: raw-SQL escape hatch scoped to ONE world's history. Validates `worldID` via `GetWorld`, refreshes catalog, then prepends `WITH world_saves AS (SELECT save_id FROM mirror_saves WHERE world_id = ?)` (worldID bound, not interpolated) so the caller can `JOIN world_saves USING (save_id)`. Scope enforced structurally, but the user still writes the body SQL.
- `workspace/query.go:316` — [—] [WORKSPACE] — `forbiddenVerbs`: allowlist-by-rejection map of mutating/DDL keywords (INSERT/UPDATE/DELETE/CREATE/DROP/ATTACH/COPY/PRAGMA/BEGIN/COMMIT/MERGE/…) checked as bare tokens ANYWHERE in the statement (depth-agnostic, closes CTE-wrapped-mutation bypass).
- `workspace/query.go:338` — [—] [WORKSPACE] — `ensureReadOnly(query)`: the primary gate protecting the single RW DuckDB handle. Requires leading SELECT/WITH, then delegates to `scanForbidden`. Design note (lines 18-22): a second `read_only` handle can't coexist with the RW handle on one file, so statement rejection is the correct design — do not "fix" into a second handle.
- `workspace/query.go:364` — [—] [WORKSPACE] — `scanForbidden(s)`: single-pass, literal/identifier/comment-aware scanner; rejects any bare forbidden verb and any chained statement after `;`.
- `workspace/query.go:438` — [—] [WORKSPACE] — `nextToken` / `isAlpha` / `isAlphaNum` (lines 438/481/485): comment- and whitespace-skipping tokenizer the gate is built on.
- `workspace/query.go:493` — [READ] [WORKSPACE] — `scanRowsToMaps(rows)`: turns the SELECT result into `[]map[string]any` (column name → scalar). Go-API analogue of the Starlark `scanRowsToDicts`; raw driver values, no DSL typing.

## Read paths

**Spanning aggregate collections (`workspace.bibites` / `world.bibites`, etc.) — the intended DSL surface.**
1. Binding side (`workspace/automation.go`, out of slice but the entry): `workspaceValue.Attr("bibites"/"eggs"/"pellets")` calls `spanningCollection(ctx, thebibites.NewWorkspaceScope(), name)` (automation.go:84); the world equivalent passes `thebibites.NewWorldHistoryScope(v.world.ID)` (automation.go:388).
2. `spanningCollection` (spanning.go:58) → `spanningReader` (spanning.go:39) → `refreshMirrorCatalog` once under `w.mu`, then `thebibites.NewSpanningReader(w.duck(), scope)` → `reader.Collection(name)`.
3. Scoping is **by construction**: the returned `EntityCollection` carries the read-only `spanningScope` (script/thebibites/scope.go:84), whose `scopeClause` injects `<identity>.save_id IN (SELECT save_id FROM mirror_saves [WHERE world_id = ?])` (scope.go:90-98) and whose `catalogJoin` LEFT JOINs `mirror_saves` so `world_id`/`sim_time` resolve as friendly DSL columns (scope.go:102-107). The Starlark script never writes `save_id` or a JOIN.
4. The actual aggregate (`.count()/.mean()/.group_by("world_id")/.where(...)`) runs ONE SELECT on the shared handle when the script terminal op fires — no per-revision query, no catalog rebuild per `.where()` (spanning.go:20-24).

**`save_id` ↔ world mapping (the `mirror_saves` catalog).** `mirror_saves` (DDL at query.go:48) is the one map from `save_id` (= revision sha256) → `world_id` (+ `tier`, `blob_present`, `sim_time`). It carries ONLY sha256 history keys, never the world-id working key — so every spanning/scoped read sees committed history and never another world's uncommitted staged working partition (spanning.go:16-18, query.go:263-264, scope.go:23-25). It is rebuilt from the SQLite registry (`RevisionsForWorkspace` + `scenes.simulated_time`), fingerprint-gated so a no-op call is a cache hit (query.go:86-102). It is refreshed once before EVERY spanning/Query/HistoryQuery call.

**`save_id` partition scoping — where it actually happens.**
- WORKSPACE scope: `spanningScope{}` (worldID == "") → `save_id IN (SELECT save_id FROM mirror_saves)` — all worlds (scope.go:91-97, 120-122).
- WORLD scope: `spanningScope{worldID}` → `... WHERE world_id = ?` (scope.go:93-95, 113-115).
- Raw-SQL WORLD scope: `HistoryQuery` prepends the `world_saves` CTE (query.go:293), but enforcement depends on the caller actually joining it.
- The leak risk (MEMORY: per-world query scoping) is closed for the DSL surface by construction; for the raw hatch it's caller responsibility.

**Raw-SQL escape hatch.** `Query` (query.go:232, workspace-wide) and `HistoryQuery` (query.go:265, one-world). Both are read-only-enforced (`ensureReadOnly`, query.go:338) and expose internal columns/joins (`save_id`, `mirror_saves`, typed `*_value` columns) — i.e. they are the workaround the DSL is meant to replace, per missing.md preamble.

## Mutation paths

None of the **user-data** kind in this slice. Both files are read-only by contract:
- The spanning scope is read-only by construction: `spanningScope.writable()` returns false (scope.go:100) and `NewSpanningReader` rejects a writable scope (scope.go:160-162); spanning collections never mutate and never materialize Entity rows.
- `ensureReadOnly` rejects every non-SELECT before it reaches the handle (query.go:338).
- The only DuckDB *write* here is internal catalog maintenance — `refreshMirrorCatalog` (query.go:86) `DELETE`s + `INSERT`s into `mirror_saves` inside a transaction. It writes catalog metadata, never user save data, so it is not a user-facing mutation path. (Mutation of save data lives elsewhere — `world.open()` per-save, lifecycle/import slices.)

## Missing seams

### A. The spanning aggregate surface has no per-revision / time-series axis (you must drop to `Query` for history-over-time)
**What's missing.** `mirror_saves` carries `sim_time` per revision (query.go:48-55, sourced per-revision from `scenes` precisely so it's "suitable for time-series queries", query.go:43-47), and the spanning scope LEFT JOINs the catalog so `sim_time` resolves as a friendly column (scope.go:102-107, 33-36). But the DSL collection terminal ops exposed here are scalar/grouped aggregates only (`count/mean/group_by(col)/where`, spanning.go:6-8). There is no native time-bucketing / "by sim_time" / per-revision trajectory op on `workspace.bibites` / `world.bibites`. `group_by` takes a single friendly column, so `group_by("sim_time")` buckets by exact equality, not a time series.
**Consequence.** Any "population over time" / "average size as sim_time advances" question — the most natural use of retained history — forces the raw `Query`/`HistoryQuery` hatch (`SELECT ... JOIN mirror_saves ... GROUP BY sim_time`), re-leaking `save_id`/`mirror_saves`/typed columns exactly like missing.md §1, but for a different reason (no temporal op rather than no gene/brain kind).
**Where it lives.** `workspace/spanning.go:6-8` (the exposed op set), `workspace/query.go:43-47` (the per-revision sim_time that exists but is only reachable via raw SQL), `script/thebibites/scope.go:33-36,102-107` (sim_time resolvable but only as a flat group key).

### B. The raw-SQL hatch (`Query`) has no `world_id` / `mirror_saves` attribution wrapper — workspace-wide reads must hand-write the catalog JOIN
**What's missing.** `HistoryQuery` prepends a `world_saves` CTE so a one-world raw query can `JOIN ... USING (save_id)` (query.go:293), but the workspace-wide `Query` prepends nothing (query.go:232-250) — its doc explicitly says "The caller can JOIN mirror_saves for world_id / sim_time / tier attribution" (query.go:226-227). There is no symmetric `mirror_saves`-exposing CTE or helper for the all-worlds case.
**Consequence.** Every cross-world raw query (the exact shape in missing.md §1's example) must hand-write `JOIN mirror_saves m ON m.save_id = <tbl>.save_id`, hard-coding the catalog name and the partition key — the deepest form of the escape-hatch leak the design rule forbids. The asymmetry also means a user who learned `world_saves` from `HistoryQuery` has no equivalent at the workspace level.
**Where it lives.** `workspace/query.go:232-250` (`Query`, no wrapper) vs `workspace/query.go:290-295` (`HistoryQuery`'s `world_saves` CTE).

### C. `ensureReadOnly` rejects by keyword blocklist, not parse — read-only DuckDB read functions get false-positives
**What's missing.** The gate is a bare-token blocklist (`forbiddenVerbs`, query.go:316-324) over the raw string, not a parse of statement structure. Several listed verbs are also legitimate read-only constructs in DuckDB SQL: e.g. `SET` appears in window/`LIST` contexts and table functions, and there is no allowance for read-only table functions a user might reasonably call inside a SELECT. Any SELECT whose body contains one of these words as a bare keyword token is rejected even when read-only.
**Consequence.** Power users hitting the (already-leaky) escape hatch can be blocked from legitimate read-only analytics, with no override — pushing them toward materializing data in Go or abandoning the query. This is a usability sharp edge on the hatch, not a security hole (erring safe). Low severity; flagged for completeness.
**Where it lives.** `workspace/query.go:316-324` (blocklist), `workspace/query.go:364-434` (`scanForbidden`, string-scan not parse). Design note at query.go:18-22 justifies statement-rejection over a second handle but not the blocklist breadth.

> Note: missing.md §1 (genes/brains absent from the spanning collection surface) is the dominant gap touching this slice and is already filed — see `docs/missing.md` §1, which itself points at `workspace/spanning.go` and `script/thebibites/collection.go`. NOT re-filed here. Seam A is adjacent to §1 (both force the raw hatch) but is a distinct cause (no temporal op vs. no gene/brain kind).
