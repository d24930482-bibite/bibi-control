# 07. Workspace lifecycle (registry / working-set / eviction / GC / commit)

**Scope (files scanned):** `workspace/workspace.go`, `workspace/world.go`, `workspace/registry_ops.go`, `workspace/working_set.go`, `workspace/eviction.go`, `workspace/gc.go`, `workspace/errors.go`, `workspace/commit.go`. (Tests skipped.)

The `Workspace` struct binds three backing stores for one owner-scoped workspace: the **shared SQLite revision registry** (`registry *revisionstore.Store`, operational truth for all workspaces under a root), the **shared content-addressed blobstore** (`blobsStore *blobstore.FSStore`, dedups bytes across workspaces), and **one per-workspace DuckDB analytics file** (`duckDB *sql.DB`). `workspace.go:28-81`.

Two scopes interplay here: the DuckDB *handle* is per-WORKSPACE (one writer, keyed inside DuckDB by `save_id`), while every world/save is partitioned inside that one handle under a `save_id` key. The dual-key scheme: working partition keyed by **world id** (mutable), history partition keyed by **revision sha256** (immutable, accumulates). `world.go:169-189`, `commit.go:176-233`.

## Location map

### Struct / handles / keying
- `workspace.go:28-81` — [READ] [WORKSPACE] — `Workspace` struct: three stores + in-memory `worlds` working set, `nodes` set, log buffers, catalog-fingerprint cache, test-only counters.
- `workspace.go:34` — [READ] [WORKSPACE] — `registry` shared SQLite revision store (operational truth across all workspaces under root).
- `workspace.go:38` — [READ] [WORKSPACE] — `blobsStore` concrete FSStore (held concretely so `Close` can reach `FSStore.Close`).
- `workspace.go:39` — [READ|WRITE] [WORKSPACE] — `duckDB` the single per-workspace DuckDB writer handle (analytics mirror).
- `workspace.go:45-52` — [WORKSPACE] — `mu sync.Mutex` (single-DuckDB-writer lock) + `worlds map[worldID]*LoadedSave` working set + `nodes map[nodeID]*Runtime`.
- `workspace.go:55-56` — [WORKSPACE] — `logMu` + `nodeLogs` (separate lock so NodeLogs reads don't contend with mutators; node-control slice owns these).
- `workspace.go:68-80` — [READ|WRITE] [WORKSPACE] — `catalogFP`/`catalogBuilt`/`rebuildCount`/`scenesReadCount`/`insertExecCount`: mirror-catalog rebuild cache + test seams; read/written ONLY inside `refreshMirrorCatalog` under `w.mu` (that fn lives in the spanning/query slice, not here).
- `workspace.go:83-88` — [READ] [WORKSPACE] — path helpers: `registryPath` (shared `metadata.sqlite`), `blobsRoot` (shared `blobs/`), `workspaceDir`, `duckPath` (`workspaces/<id>/analytics.duckdb`).
- `workspace.go:90-112` — [READ] [WORKSPACE] — `ID`/`Owner`/`Name` accessors (nil-safe).
- `workspace.go:114-137` — [READ] [WORKSPACE] — unexported `store()`/`blobs()`/`duck()` handle accessors (in-package only).

### Workspace lifecycle: create / open / close
- `workspace.go:143-193` — [WRITE] [WORKSPACE] — `Create`: mkdir root, open shared registry, open shared blobstore, `registry.CreateWorkspace` (allocates row), mkdir workspace dir, `openDuck` (fresh DuckDB + migrations); partial-failure unwinds each opened handle; allocates empty `worlds`/`nodes` maps. Does NOT take `w.mu` (not a concurrent mutator).
- `workspace.go:199-245` — [READ] [WORKSPACE] — `Open`: re-attach by id; `registry.GetWorkspace` (lookup, not create) → not-found wrapped; open blobstore + `openDuck` (idempotent migrations on existing file); same empty-map init.
- `workspace.go:250-260` — [WRITE] [WORKSPACE] — `openDuck`: `duckdb.Open` + `duckdb.ApplyMigrations` (idempotent CREATE IF NOT EXISTS); closes the handle on migration failure.
- `workspace.go:269-297` — [WRITE] [WORKSPACE] — `Close`: D1 drain — Kill+Close every `nodes` runtime and delete from map, drop `nodeLogs` under `logMu`, then close DuckDB → registry → blobstore (niled after close ⇒ idempotent); `errors.Join`. NOT guarded by `w.mu`.

### Registry ops (short-lived own-handle helpers; no *Workspace required)
- `registry_ops.go:16-27` — [READ] [WORKSPACE] — `ListWorkspaces(root)`: open→`ListWorkspaces`→close own registry handle; unfiltered by owner (single-owner per root).
- `registry_ops.go:33-44` — [WRITE] [WORKSPACE] — `RenameWorkspace(root,id,name)`: own handle; propagates `IsNotFound` untouched.
- `registry_ops.go:57-68` — [WRITE] [WORKSPACE] — `DeleteNode(root,id)`: row-only `DeleteNodeByNodeID`; does NOT stop a running process (for detached/stale rows).
- `registry_ops.go:83-104` — [WRITE] [WORKSPACE] — `DeleteWorkspace(root,id)`: registry U1 atomic cascade delete FIRST, then `os.RemoveAll(workspaceDir)`. Order is load-bearing (failed row delete ⇒ dir recoverable). Doc-comment WARNS callers holding an open `*Workspace` MUST evict+`Close` it first (DuckDB file lives under the removed dir).

### World add / lookup / list
- `world.go:21-40` — [WRITE] [WORLD] — `AddWorld(srcPath,name)`: `tb.ParseFile` once + `os.ReadFile` → `importWorldFromArchive`. Public wrapper, does NOT lock.
- `world.go:46-71` — [WRITE] [WORLD] — `AddWorldBytes(data,name)`: stage bytes to temp file (no in-memory parser), parse once, pass through bytes as blob → `importWorldFromArchive`.
- `world.go:76-85` — [READ] [WORKSPACE] — `ListWorlds`: passthrough to `registry.ListWorlds(workspaceID)`, ordered created_at then id.
- `world.go:90-99` — [READ] [WORLD] — `RevisionsForWorld(worldID)`: passthrough to `registry.RevisionsForWorld` (lineage / insertion-id order).
- `world.go:108-198` — [WRITE] [WORLD] — `importWorldFromArchive`: the AddWorld core. **Holds `w.mu` whole-body** (single DuckDB writer; non-reentrant ⇒ wrappers don't lock). Steps: (1) `CreateWorld` row (UUID = working key, head NULL), (2) `blobs.Put` + assert `ref.SHA256==archive.SHA256` (loud fail on file-change-under-us), (3) `RecordScriptRun` status="imported", (4) derive sim_time from scene, (5) `RecordRevisionAdvancingHead` parent=nil (first revision, self-ref at refcount 1), (6) DuckDB dual-key: `ImportExtractedSave` working partition (= world id) FIRST, then `CopySavePartition` derives history partition (= sha256) in-DB, (7) re-read world for advanced head.

### Working set: load / unload / open
- `working_set.go:27-114` — [READ→memory WRITE] [WORLD] — `Load(worldID)`: **holds `w.mu` whole-body**. Resolve world row → head guard (head-less ⇒ loud) → fetch head revision → `BlobPresent` guard (`notRematerializable` sentinel if evicted) → `blobs.Get` head bytes → temp file → `thebibites.LoadInto(tmpPath, worldID, w.duck())` (injects shared handle, sets `saveID==worldID`, does NO DuckDB import) → stash in `w.worlds[worldID]` (overwrite/replace; old handle GC'd, owns nothing closeable).
- `working_set.go:120-129` — [memory WRITE] [WORLD] — `Unload(worldID)`: holds `w.mu`; `delete(w.worlds,...)` only (registry/DuckDB/blobs untouched); absent key = no-op; never errors today.
- `working_set.go:144-162` — [READ] [WORLD] — `OpenWorld(worldID)`: fast-path map peek under `w.mu` (released before delegating), else lazy `Load`. Never nests `w.mu` (non-reentrant). Benign double-load race: last-writer-wins, both callers get valid head handle.

### Eviction (memory reclamation; catalog + blobstore only, never DuckDB)
- `eviction.go:15` — [READ] [WORKSPACE] — `ErrHeadBlobMissing` sentinel (head with missing bytes = unrecoverable corruption).
- `eviction.go:17-55` — [READ] [WORLD] — `EvictKind`/`EvictPolicy` + `KeepLastN`/`OlderThanSimTime` constructors. Note: per-revision sim_time NOT stored; `OlderThanSimTime` approximates via `created_at` Unix seconds.
- `eviction.go:57-85` — [READ] [WORLD] — `EvictResult`/`ReconcileResult` outcome structs (Candidates/Demoted/BytesDeleted/RefusedHead/RefusedShared/DeleteErrors; Demoted/Promoted).
- `eviction.go:101-158` — [WRITE] [WORLD] — `EvictWorldHistory(worldID,policy)`: **holds `w.mu` whole sweep**. GetWorld → RevisionsForWorld → `selectCandidates` → per-candidate `evictRevisionBlobLocked`; `ErrRevisionIsHead`→RefusedHead, `ErrBlobStillReferenced`→RefusedShared (both continue), real DB error aborts. Demotes to `mirror_only`; never writes DuckDB (history partitions stay queryable).
- `eviction.go:163-198` — [READ] [WORLD] — `selectCandidates`: builds candidate list, always excludes head (defense in depth over G1).
- `eviction.go:204-215` — [WRITE] [WORLD] — `EvictRevisionBlob(revisionID)`: public single-revision lever; holds `w.mu`; delegates to locked core (result=nil).
- `eviction.go:239-268` — [WRITE] [WORLD] — `evictRevisionBlobLocked`: crash-safe core, caller holds `w.mu`. **Ordering is the whole point**: (1) `store.EvictRevisionBlob` SQLite tx flips tier=mirror_only/blob_present=0 and COMMITs (fsync) with head/refcount gates BEFORE (2) re-read BlobRef and (3) `blobs.Delete` (idempotent). Post-commit delete failure is SOFT (recorded in DeleteErrors, returns `(false,nil)` — catalog never undone; orphan left for GC).
- `eviction.go:283-354` — [WRITE] [WORKSPACE] — `ReconcileBlobs`: startup drift repair (host calls explicitly, NOT auto-wired into Open). Holds `w.mu`. Dir 1: `FullRevisions` whose bytes missing → non-head `MarkMirrorOnly` (head → `ErrHeadBlobMissing` loud). Dir 2: `MirrorOnlyRevisions` whose bytes reappeared AND still referenced by a full row of same sha → `PromoteRevision`; unreferenced-present = orphan, NOT resurrected.
- `eviction.go:359-370` — [READ] [WORKSPACE] — `shaHasFullReference(sha256)`: any revision of sha still `tier=full && blob_present` (gate for re-promotion / never-GC-live).

### GC (byte reclamation; no catalog writes)
- `gc.go:12-26` — [READ] [WORKSPACE] — `GCResult` struct (Candidates/BytesDeleted/Skipped/DeleteErrors).
- `gc.go:47-110` — [WRITE] [WORKSPACE] — `GCUnreferencedBlobs`: Track-G byte-only sweep, manual/host-triggered, writes NO catalog. **Holds `w.mu` whole sweep.** `OrphanedBlobs` (shas with no blob_present=1 row) → skip inline refs → re-verify `shaHasFullReference` under lock (defense-in-depth "never delete a live blob") → `blobs.Has` (don't recount already-absent) → `blobs.Delete` (soft error continues). Crash-safe + idempotent.
- `gc.go:124-160` — [WRITE] [WORKSPACE] — `PromoteReappearedBlob(ref)`: runtime re-promotion lever (re-ingest/re-import/shared). Holds `w.mu`. `blobs.Has` gate, then `PromoteRevision` for every mirror_only revision of ref's sha; returns count. No-resurrection rule (absent bytes ⇒ stays mirror_only).

### Errors
- `errors.go:13` — [READ] [WORLD] — `ErrNotRematerializable` sentinel: blob-dependent paths (Load/OpenWorld, ReloadNode) when revision is mirror_only; matched via `errors.Is`; no mirror→blob fallback (structural).
- `errors.go:18-21` — [READ] [WORLD] — `notRematerializable(worldID,revID,op)`: wraps the sentinel with context.

### Commit (workspace-level; threads head + dual-key DuckDB re-import)
- `commit.go:35-88` — [WRITE] [WORLD] — `CommitWorld(worldID,program,opts)`: program-run commit. (1) `OpenWorld` BEFORE `w.mu` (non-reentrant), (2) take `w.mu` whole body, (3) read CURRENT head INSIDE lock as parent (TOCTOU guard), (4) `thebibites.RunAndCommitWorld` (run→blob→advancing-head revision via shared script core), (5-7) `applyWorldCommit`. No-op run ⇒ zero Revision + nil err.
- `commit.go:113-161` — [WRITE] [WORLD] — `CommitWorldLoaded(worldID,opts)`: object-based counterpart (commits mutations ALREADY staged on cached `ls.session`, NO script.Run) — the automation `world.open()→s.commit()` surface. Same lock discipline; `thebibites.CommitLoadedWorld` then `applyWorldCommit`.
- `commit.go:170-244` — [WRITE] [WORLD] — `applyWorldCommit`: shared post-commit body, MUST hold `w.mu`. No-op short-circuit if `!wc.Committed`. Asserts `wc.SaveID==worldID` (dual-key desync guard). `reparseCommitted` (fresh projection — post-Apply archive has STALE typed projections). DuckDB: `ReplaceExtractedSave` re-seeds working partition (= world id) FIRST, then `CopySavePartition` derives history partition (= new sha256, additive). Finally `delete(w.worlds,worldID)` — consumed StateApplied copy dropped so next OpenWorld lazy-reloads fresh stageable head.
- `commit.go:257-282` — [READ] [WORLD] — `reparseCommitted(ref)`: `blobs.Get` committed bytes → temp file → `tb.ParseFile` once → fresh archive with mutation-accurate typed projections for the dual-key mirror import.

## Read paths

- **Workspace discovery / metadata:** `ListWorkspaces` (`registry_ops.go:16`) and the accessors (`workspace.go:90-112`) read registry-level facts. `ListWorlds`/`RevisionsForWorld` (`world.go:76,90`) are thin registry passthroughs.
- **World materialization:** `OpenWorld` (`working_set.go:144`) → fast-path `w.worlds` map hit, else `Load` (`working_set.go:27`) walks registry head → blob bytes → `LoadInto` against the shared DuckDB handle. This is the only path that pulls blob bytes back into memory; it is gated by `BlobPresent` and the `ErrNotRematerializable` sentinel.
- **Mirror/catalog reads** (spanning queries, `mirror_saves` catalog rebuild) live in the spanning slice (06) but mutate `w.catalogFP`/`rebuildCount` on this struct under `w.mu` (`workspace.go:68-80`).
- **Reconcile/GC stat reads:** `blobs.Has` + `shaHasFullReference` (`eviction.go:359`) drive both Reconcile and GC decisions.

## Mutation paths

- **Add a world:** `AddWorld`/`AddWorldBytes` → `importWorldFromArchive` (`world.go:108`). Registry+blob recorded BEFORE DuckDB import so a failed mirror leaves registry as source of truth (orphan blob / head-less row is reclaimable).
- **Commit a world:** `CommitWorld` (program run) or `CommitWorldLoaded` (staged session) → shared `applyWorldCommit`. The workspace-level commit wraps the **per-save commit** core (`thebibites.RunAndCommitWorld` / `CommitLoadedWorld`, in the save/script slices): the save layer produces the blob + advancing-head revision; the workspace layer owns the TOCTOU head read, the dual-key DuckDB re-projection, and dropping the consumed working copy. Head + blob are durable in SQLite/blobstore even if the DuckDB re-projection errors (mirror is a rebuildable projection, never rolled back against the committed head — `commit.go:216-233`).
- **Drop from working set:** `Unload` (`working_set.go:120`) explicit; `applyWorldCommit`'s `delete(w.worlds,...)` (`commit.go:241`) implicit on commit; `Close` drains nodes (not worlds — `worlds` hold nothing closeable).
- **Reclaim memory/bytes:** `EvictWorldHistory`/`EvictRevisionBlob` (catalog demote + byte delete, head/refcount double-gated) → `GCUnreferencedBlobs` (byte-only orphan sweep) → `ReconcileBlobs`/`PromoteReappearedBlob` (repair drift / re-promote reappeared bytes). All hold `w.mu`.
- **Remove a workspace:** `DeleteWorkspace` (`registry_ops.go:83`) registry cascade then dir removal — but it is an own-handle free function, NOT a method, so it cannot reach the in-memory `*Workspace` it warns must be evicted/closed first (see Missing seams).

## Locks — what they protect

- **`w.mu` (single per-workspace DuckDB writer + working-set + TOCTOU head guard):** held whole-body by `importWorldFromArchive`, `Load`, `Unload`, `EvictWorldHistory`, `EvictRevisionBlob`, `evictRevisionBlobLocked` (caller-held), `ReconcileBlobs`, `GCUnreferencedBlobs`, `PromoteReappearedBlob`, `CommitWorld`, `CommitWorldLoaded`, `applyWorldCommit` (caller-held). Held only for the map peek by `OpenWorld`. **Non-reentrant** — the recurring discipline is: call `OpenWorld` BEFORE taking `w.mu` (it takes its own lock); never nest. Public Add* wrappers do NOT lock (the core does). Create/Open/Close do NOT take it (not concurrent mutators). This is the fine-grained, per-workspace lock the memory notes describe — there is no global workspace lock.
- **`w.logMu` (separate):** guards `nodeLogs` only, so log reads don't contend with heavy mutators; touched here only in `Close` (`workspace.go:281-283`). Owned by the node-control slice (08).

## Missing seams

### DeleteWorkspace cannot evict the live in-memory handle it warns about
**What's missing.** `DeleteWorkspace` (`registry_ops.go:83-104`) is a free function with its own short-lived registry handle. Its own doc-comment (`registry_ops.go:79-83`) says callers "MUST evict and Close" any open `*Workspace` first because the per-workspace DuckDB file lives under the dir it `RemoveAll`s — but there is no method/registry of live `*Workspace` instances in this slice to enforce or even detect that. The contract is prose-only; nothing fails loud if a caller forgets.
**Consequence.** Deleting a workspace while the daemon holds an open handle removes `analytics.duckdb` out from under an open DuckDB writer (a race the comment names but cannot prevent here). The eviction responsibility is pushed entirely to the daemon/host cache (10).
**Where it lives.** `registry_ops.go:79-104`.

### No working-set eviction policy / bound (worlds accumulate in memory until explicit Unload)
**What's missing.** `w.worlds` (`workspace.go:48`) grows on every `Load`/`OpenWorld` and only shrinks on explicit `Unload` (`working_set.go:120`), commit-consumption (`commit.go:241`), or Load-overwrite. There is no size cap, LRU, or sweeper — the rich eviction/GC machinery in `eviction.go`/`gc.go` operates on **persisted revision blobs + catalog tiers**, NOT on the in-memory `worlds` map. "Working set / eviction" thus means two different things in two layers, and the in-memory layer has no automatic bound.
**Consequence.** A long-lived workspace that opens many worlds holds every `LoadedSave` (and its parsed in-memory archive) resident until something explicitly Unloads it. Harmless for resources (no closeable handles) but unbounded for memory.
**Where it lives.** `workspace.go:48`, `working_set.go:120-129`.

### Working-partition drift on a failed DuckDB re-seed is logged-as-error but not self-healing
**What's missing.** If `ReplaceExtractedSave` fails in `applyWorldCommit` (`commit.go:215-220`), the head+blob commit is already durable but the working partition (keyed by world id) lags at the prior head. The comment acknowledges "known, non-corrupting drift … until a later successful commit re-seeds it," but there is no reconcile path that re-projects a stale working partition from the current head on demand — `Load`/`OpenWorld` inject the handle and do NOT re-import the working partition (`working_set.go:97-106`).
**Consequence.** Spanning/working-partition reads (slice 06) can silently reflect the pre-commit head until the next successful commit. Overlaps with the spanning slice's mirror-freshness story.
**Where it lives.** `commit.go:215-220`, `working_set.go:97-106`.

### ReconcileBlobs is not auto-wired into Open
**What's missing.** `ReconcileBlobs` (`eviction.go:271-283`) is the documented startup drift repair but explicitly is NOT called by `Open` (`workspace.go:199`) — "the host calls it explicitly." Nothing in this slice runs it; an `Open` that skips it leaves catalog↔blobstore drift (e.g. a crash mid-evict) unrepaired and a stale head potentially un-flagged until a Load hits the `BlobPresent` guard.
**Consequence.** Post-crash correctness depends on an out-of-slice host calling Reconcile; a forgetful host opens a workspace with latent full-but-missing rows.
**Where it lives.** `eviction.go:271-283`, `workspace.go:199-245`.

### Eviction/GC/Promote are method-only on a live *Workspace — no own-handle admin variant
**What's missing.** Unlike the registry ops (`ListWorkspaces`/`Rename`/`Delete*`, which are own-handle free functions usable without a `*Workspace`), all the Track-G reclamation levers (`EvictWorldHistory`, `GCUnreferencedBlobs`, `ReconcileBlobs`, `PromoteReappearedBlob`) require a fully-opened `*Workspace` (they take `w.mu` and touch `w.blobs()`/`w.store()`). There is no offline/own-handle admin path to GC or reconcile a workspace that isn't currently held open.
**Consequence.** Maintenance/GC of a workspace forces a full `Open` (DuckDB + blobstore + registry handles) just to delete orphan bytes — and serializes against any live mutators via `w.mu`.
**Where it lives.** `eviction.go:101,204,283`, `gc.go:47,124`.
