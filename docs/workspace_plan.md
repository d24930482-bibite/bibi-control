# Workspace + Worlds + Node Automation — Ticketed Implementation Plan

## Context

bibicontrol has a mature **single-save** pipeline but no operational layer above it, and
its persistence stores don't know about each other:

- `blobstore` — content-addressed save bytes, global, no save identity.
- `revisionstore` (SQLite) — `script_runs` + `save_revisions`; revisions chain via
  `parent_id → save_revisions(id)`, but there is **no logical grouping, no head pointer,
  no owner/world columns** (and `RevisionInput.ParentID` is always nil — `loadedsave.go:583`).
- `duckdb` — opened **lazily in-memory per run** (`loadedsave.go:383` `OpenAndImport(ctx,"",…)`),
  keyed by `save_id`. The schema already carries `save_id` on every row and
  `ReplaceExtractedSave` deletes-by-`save_id` then inserts, so it is *already multi-save
  capable inside one DB* (`duckdb/import.go:92`).
- `noderuntime` + `ipc` + `simctl` — a complete process/compat layer (`noderuntime.Start`,
  `Runtime.Stop/Connect/Request`, `simctl.Client` STOP/RESUME/INFO/RELOAD) that "intentionally
  does not own workspace layout / persistence / save semantics" (`runtime.go:20`).
- `control/controller.go`, `api/api.go` — empty stubs. `savemutator/thebibites/workspace.go`
  — an unimplemented cross-save-append interface (DSL feature #8 / P3 of `DSL_completion_plan.md`).

The **Workspace** is the missing operational container the architecture doc describes
("SQLite as the operational source of truth"; "associate a running process with a save
workspace"; "never mutate a save in place — new revision, parent, provenance"). It ties the
stores + the process layer together under a user-scoped collection of **worlds** and **nodes**.

**Intended outcome:** a `workspace/` package whose Go API + a new Starlark automation surface
support: create/open a workspace; add/load/unload **worlds**; mutate→commit advancing a world
head (one save at a time); **query a world's full revision history and the whole workspace
read-only**; clone worlds / apply the same change across worlds (git-for-worlds); start/stop/
inspect game **nodes** bound to a world; ship a head to a node and `RELOAD`; ingest a game
autosave back into a world's chain; cross-world **transfer**; and **tier memory** by evicting
old revisions' blobs while keeping their queryable (but non-rematerializable) DuckDB mirror.

## Core model & locked decisions

- **A save-chain IS a world.** A *world* = a stable opaque id + mutable `head_revision_id`
  + `name` + `sim_time` (the head's `scene.simulated_time`) + optional bound node, over an
  immutable `parent_id` revision chain. Like a **git branch**: `worlds` is a mutable ref into
  the existing immutable `save_revisions` DAG.
- **Dual-key DuckDB mirror (revised from the old world-id-only key).** The persistent mirror
  holds **two kinds of partition**, both in the `save_id` column (the generated schema is not
  renamed — it just carries whichever id):
  - **History partitions — one per committed revision, keyed by the revision content `sha256`.**
    Imported once at commit and **retained forever** (never replaced on later commits). This is
    what makes *all of save history* and *whole-workspace* queries possible. This reverses the
    old "replace the world's rows on commit / head-only mirror" decision.
  - **A working partition — one per *loaded* world, keyed by the world id** (`save_id=worldID`).
    Mutable: seeded from the head on load, absorbs staged mirror write-through, re-seeded on
    commit. This is the query/mutation target for an open world and preserves the existing
    push-down + churn DoD (`ls.saveID = worldID`). Staged edits **never** touch history
    partitions, so history stays immutable.
- **`mirror_saves` catalog** maps each history `save_id (sha256) → world_id, sim_time, tier,
  blob_present, mirror_schema_version`. Stored in the SQLite registry and **ATTACH-ed** into the
  workspace DuckDB so world-history / workspace queries `JOIN` it (no duplication, one source of
  truth for world/sim_time/tier scoping). The 46 normalized tables stay unchanged.
- **The mirror is a lossy, read-only projection; non-rematerializability is *structural*.** Only
  `blob → tb.ExtractTables → mirror` exists; **no mirror→blob path is ever written.** So a save
  whose blob is evicted can be *queried* but can never be reconstructed into a runnable save —
  by construction, not by a flag that could regress.
- **Two-tier eviction with refcounted GC (to save memory).** A revision is `full` (blob+mirror)
  or `mirror_only` (blob evicted, history rows kept, queryable but not loadable). Eviction
  "forgets the bytes, keeps the identity+rows": delete the blob (refcount-gated), set
  `tier=mirror_only` + `blob_present=false`; the `save_revisions` row and history partition stay.
  Blobs are **refcounted** across worlds/revisions (clones + content dedup share one sha256); GC
  frees only bytes no live ref needs. Because the sha256 identity is retained, a reappearing blob
  **re-promotes** `mirror_only → full`. A world's **head is never evictable**.
- **User scope = namespace only.** An `owner` string column scopes workspaces. No auth/login.
- **One persistent DuckDB file per workspace** (`duckdb.Open(path)` takes a file path;
  `ApplyMigrations` is idempotent `CREATE IF NOT EXISTS`). Per-file = workspace isolation +
  per-file eviction + cheap workspace delete (`rm`).
- **Broadest query scope = a single workspace (all its worlds).** A "whole-workspace" query spans
  every world in the one workspace's DuckDB file (C4). Federating across *multiple* workspaces is
  out of scope — a workspace is the analytics boundary.
- **Shared SQLite registry** (extend `revisionstore`) = operational truth for all workspaces.
- **Shared content-addressed `blobstore`** (dedups bytes across workspaces; access gated by
  registry rows).
- **A node binds to a world; the head lives on the world** (`worlds.head_revision_id`). One
  head, single source of truth. A world is bound to at most one active node at a time.
- **IPC = orchestrate existing commands** (STOP/RESUME/INFO/RELOAD via `noderuntime`+`simctl`);
  **no new wire verbs**, no DLL changes.
- **A save is an OBJECT, not a global.** The predeclared `save` global is removed. Opening a
  world (or an ad-hoc path) **returns a `Save` object** (`world.open()` /
  `workspace.open(path)`); all reads/mutations/commit are methods on that object. The existing
  `Save` type (`script/thebibites/save_value.go`) is already a `starlark.Value`/`HasAttrs` — it
  is simply handed out as a value instead of injected. `Globals(ls)` is removed; **hard cutover**,
  existing `script/thebibites` tests rewritten to the object form (no compatibility shim).
- **Two Starlark surfaces, two trust levels — both object-based:**
  - The **save-transform** operations (`s.bibites`, `s.sql`, `s.where().set()`, `s.commit()`)
    stay the existing staged-write pipeline (sandboxed/pure stage + commit, churn-bounded);
    they are now invoked on a `Save` *object* obtained from `open()`, not an ambient global.
  - `workspace` — a **new, effectful, host-trusted root object** (worlds + nodes +
    info/ingest/reload + `open()`). This is the "bindings to automate the loop." Scheduling
    (when to run) stays host-driven (Starlark is non-looping/hermetic per invocation); the
    *operations* are fully scriptable through these bindings.

## Architecture (after)

```
                    Workspace (owner-scoped)  ── new package workspace/
                     │
        ┌────────────┼─────────────────────────────┐
        │            │                              │
   registry      per-workspace                  active nodes
 (SQLite, shared) DuckDB file (1)            (in-mem map id→*noderuntime.Runtime)
   revisionstore   duckdb                         noderuntime + simctl + ipc
        │            │                              │
   workspaces        DUAL-KEY mirror:                nodes(world_id) ── bound ──┐
   worlds(head,  ┐   ├ history: 1 partition/revision (save_id=sha256, RETAINED) │
    sim_time)    │   ├ working: 1 partition/loaded world (save_id=worldID)      │
   nodes         │   └ mirror_saves catalog (ATTACH SQLite): world_id, sim_time,│
   save_revisions│       tier, blob_present                                     │
   (world_id,    │   history + whole-workspace queries are READ-ONLY      ship head
    parent, tier,│                                                            + RELOAD
    refcount)    └── blobstore (shared, refcounted) ←── ingest autosave ───────┘
                      evict blob → tier=mirror_only: keeps mirror, not bytes

Starlark:  workspace (root object) → world.open()/workspace.open(path) RETURNS a Save object
           Save object → s.bibites / s.sql / s.where().set() / s.commit()   (no `save` global)
           node.info() / node.ingest_autosave() / node.reload()  (effectful automation)
```

## Dependency graph (tickets)

```mermaid
graph TD
  A1[A1 registry schema: worlds/nodes + world_id on revisions] --> A2[A2 revisionstore methods + parent threading]
  A2 --> C1
  B1[B1 LoadedSave seam: explicit world id + injected DB] --> C2
  B3[B3 remove `save` global: open() returns a Save object; rewrite tests] --> E1
  B3 --> C3
  A2 --> WS[B2 workspace pkg skeleton: Open/Create + per-ws DuckDB file]
  B2 --> C1[C1 AddWorld: import → world + 1st revision + DuckDB import]
  C1 --> C2[C2 Load/Unload/OpenWorld working set]
  C2 --> C3[C3 Commit advances head + re-import]
  C1 --> C4[C4 Query working-copy + full history + whole workspace]
  WS --> D1[D1 node lifecycle: start/persist/bind + active set]
  D1 --> D2[D2 simctl passthrough: stop/resume/info + State]
  C3 --> D3[D3 reload: ship head→drop path + RELOAD; IngestAutosave → append+advance head]
  D2 --> D3
  C3 --> E1[E1 workspace automation Starlark globals]
  D3 --> E1
  C4 --> E1
  E1 --> E2[E2 world.open returns a Save object; s.commit advances head]
  C4 --> F1[F1 implement mutator Workspace iface: cross-world transfer]
  F1 --> F2[F2 expose transfer via workspace surface; replace DSL P3 stub]
  A2 --> G1[G1 blob refcounting]
  C3 --> G2[G2 EvictWorldHistory: blob-evict, keep mirror, crash-safe]
  G1 --> G2
  G2 --> G3[G3 GC sweep + re-promotion]
  C2 --> G4[G4 guardrails: ErrNotRematerializable on blob-dependent paths]
  G2 --> G4
  G4 --> E1
```

## Parallelization (streams, waves, critical path)

**Independent streams** (each can be owned by a separate agent/person; ordered within a stream):

| Stream | Tickets | Starts after | Primary packages (contention scope) |
|---|---|---|---|
| **R — registry/core** | A1 → A2 | — (root) | `revisionstore/` |
| **S — save-object refactor** | B1, B3 | — (root) | `script/thebibites/` |
| **W — world ops** | B2 → C1 → C2 → C4; C3 | R (A2) | `workspace/` (new); C2/C3/C4 touch `script/thebibites/` |
| **N — nodes** | D1 → D2 → D3 | W (B2) | `workspace/` + `noderuntime`/`simctl` (reuse) |
| **T — tiering/GC** | G1 → G2 → G3; G4 | R (A2) + W (C2/C3) | `revisionstore/` + `workspace/` |
| **X — transfer** | F1 → F2 | W (C4) | `savemutator/thebibites/` + `workspace/` |
| **A — automation** | E1 → E2 | W+N+T+S converge | `workspace/automation.go` (new) |

**Execution waves** (everything in a wave runs concurrently once prior waves land):

- **Wave 0 (3 parallel roots):** `A1` · `B1` · `B3`  — start all immediately. `B3` is the
  largest single ticket (test-wide refactor); front-loading it keeps it off the critical path.
- **Wave 1:** `A2`
- **Wave 2 (∥):** `B2` · `G1`
- **Wave 3 (∥):** `C1` · `D1`
- **Wave 4 (∥):** `C2` · `C4` · `D2`
- **Wave 5 (∥):** `C3` · `F1`
- **Wave 6 (∥):** `D3` · `G2` · `F2`
- **Wave 7 (∥):** `G3` · `G4`
- **Wave 8:** `E1`
- **Wave 9:** `E2`

**Critical path:** `A1 → A2 → B2 → C1 → C2 → C3 → G2 → G4 → E1 → E2` (10).
- **Shorten it:** the `G4 → E1` edge is the tiering-guardrail gate. If `E1` ships first and `G4`
  integrates when ready, the path drops to **9** via `A1→A2→B2→C1→C2→C3→D3→E1→E2`. `B1`/`B3`/`F*`
  are never on the critical path.

**File-contention caveats (matter more than the logical DAG for merge friction):**
- **`script/thebibites/` is a serialization point.** `B1`, `B3`, and the `C2/C3/C4` edits all
  touch it. Land `B3` (the global→object refactor) **first or as the package's single owner**,
  then layer `B1` and the C-track edits — do **not** run `B3` concurrently with C-track on the
  same files, or the refactor churn collides. Treat S-stream as a barrier for that package.
- **`revisionstore/store.go` is shared by `A2` and `G1`.** Easiest: fold `G1`'s refcount columns
  into `A1`'s schema and its methods into `A2`, so `G1` becomes "wire up call sites" only.
- **`workspace/` is new**, so W/N/T/A edits there are mostly additive (low contention) once `B2`
  defines the type — but all mutate the same struct; keep ticket diffs method-scoped.
- Runtime (not dev) serialization: one DuckDB writer per workspace (workspace mutex) — a
  correctness constraint, not a parallel-development one.

---

## Track A — Registry & world model (SQLite)

### A1 — Schema additions (`revisionstore/schema.sql`)
- `workspaces(id TEXT PK, owner TEXT, name TEXT, created_at TEXT)` — `id` opaque UUID
  (`github.com/google/uuid`, already an indirect dep; promote to direct).
- `worlds(id TEXT PK, workspace_id TEXT FK, name TEXT, head_revision_id INTEGER NULL
  REFERENCES save_revisions(id), sim_time REAL NULL, created_at TEXT)` — `id` = **stable world
  id** (working-partition key). `head_revision_id` = mutable head over the immutable chain;
  `sim_time` denormalizes the head's `scene.simulated_time` for cheap listing/sorting.
- `nodes(id TEXT PK, workspace_id TEXT FK, world_id TEXT NULL FK, node_id TEXT, run_id TEXT,
  status TEXT, compat_addr TEXT, drop_path TEXT, created_at TEXT)` — persistent record of a
  process node; `world_id` is the bound world. (In-memory `*noderuntime.Runtime` handles are
  *not* persisted; only the binding + identity are.)
- Add to **`save_revisions`**: `world_id TEXT NULL REFERENCES worlds(id)` (which world the
  revision belongs to; keep `parent_id` for lineage), `tier TEXT NOT NULL DEFAULT 'full'`
  (`full` | `mirror_only`), `blob_present INTEGER NOT NULL DEFAULT 1`, `refcount INTEGER NOT
  NULL DEFAULT 0` (live refs to the blob bytes), `mirror_schema_version INTEGER NULL` (the
  schema the history partition was imported at — frozen once `mirror_only`). Add indexes on
  `worlds(workspace_id)`, `nodes(workspace_id)`, `save_revisions(world_id)`,
  `save_revisions(sha256)` (already exists — reused for refcount/dedup lookups).
- `mirror_saves` view/table is the catalog the DuckDB mirror ATTACH-joins: it is simply a
  projection of `save_revisions(sha256 AS save_id, world_id, tier, blob_present,
  mirror_schema_version)` + the head `sim_time` — no separate store; SQLite stays the single
  source of truth.
- Schema is additive + `IF NOT EXISTS`; existing rows (world_id NULL, tier defaulting `full`)
  keep working.

### A2 — Store methods + parent threading (`revisionstore/store.go`)
- Add structs/methods alongside the existing `RecordScriptRun`/`RecordRevision`/`RevisionByID`:
  `CreateWorkspace`, `GetWorkspace`, `ListWorkspaces`; `CreateWorld`, `GetWorld`, `ListWorlds`,
  `SetWorldHead(ctx, worldID, revisionID)` (also updates `worlds.sim_time`), `RevisionsForWorld`;
  `CreateNode`, `GetNode`, `ListNodes`, `BindNode(node→world)`, `SetNodeStatus`.
- **Blob lifecycle / refcount methods** (Track G): `IncBlobRef`/`DecBlobRef(sha256)` (refcount is
  per *content hash*, shared across revisions/worlds); `EvictRevisionBlob(ctx, revisionID)`
  (tier→`mirror_only`, `blob_present=0` — refuses if the revision is any world's head or
  `refcount>0` after the world's own ref is accounted); `UnreferencedBlobs()` (sha256s with
  `refcount=0` and no `full` revision needing them) for GC; `PromoteRevision(ctx, revisionID)`
  (`mirror_only → full` when the blob reappears by hash). All tier/refcount/head mutations run in
  one SQLite tx.
- Extend `RevisionInput` with `WorldID string` (and reuse existing `ParentID *int64`). In
  `RecordRevision` (store.go:171) persist `world_id`. **Reconcile the always-nil parent**: the
  workspace passes the world's current `head_revision_id` as `ParentID` and advances
  `head_revision_id` to the new revision after a successful record (done atomically in one
  SQLite tx alongside the insert — add a small `RecordRevisionAdvancingHead` helper that wraps
  insert + `SetWorldHead`).
- Model tests on `revisionstore/store_test.go`.

---

## Track B — Per-workspace DuckDB & LoadedSave seam

### B1 — LoadedSave seam (`script/thebibites/loadedsave.go`)
Today `Load(path)` derives `saveID` from `archive.SHA256` (loadedsave.go:125) and `openDB`
opens an **in-memory** DB via `OpenAndImport(ctx,"",…)` (loadedsave.go:383). Add a constructor
that the workspace drives:
- `LoadInto(path, worldID string, db *sql.DB) (*LoadedSave, error)` — sets `ls.saveID = worldID`
  (explicit, stable) and `ls.db = db` (the shared per-workspace handle). Because `openDB`
  early-returns when `ls.db != nil` (loadedsave.go:380), the injected handle short-circuits the
  in-memory import — the world projection is already present from C1/C3.
- Keep `Load(path)` as-is for the standalone path.
- **No mirror change needed:** `flushMirrorColumn` already scopes its UPDATE by
  `… save_id = ?` with `ls.saveID` (mirror.go:171). With `ls.saveID = worldID`, mirror
  write-through is correctly world-partitioned in the shared DB. (This corrects the draft's
  "key fix" — it's already done; the only requirement is that `ls.saveID` be the world id.)

### B3 — Remove the `save` global; `open()` returns a `Save` object (`script/thebibites`)
The `save` predeclared global goes; a save becomes a value returned by opening.
- Delete `Globals(ls)` (`bindings.go:10`) and its `save`-injection. The engine no longer
  predeclares a save. `Save` (`save_value.go:13`, already `starlark.Value`/`HasAttrs`) is
  handed out as a value from `open()` (Track E: `world.open()` / `workspace.open(path)`), and
  from `runLoaded` for the standalone path.
- `RunAndCommit` (`run.go:39`) keeps loading + committing, but the program receives the save as
  an **object** (returned by `workspace.open(path)` in the new surface), not an ambient global.
  The staged-write pipeline (`s.where().set()`, `s.commit()`, mirror, churn DoD) is unchanged —
  only how the program *obtains* the save changes.
- **Hard cutover:** rewrite every `script/thebibites` test that assumes the `save` global
  (`mutation_test.go`, `commit_test.go`, `analytics_test.go`, `settings_value_test.go`,
  `bindings_test.go`, `zones_test.go`, `pellets_test.go`, …) to obtain the save via `open()`. No
  back-compat shim. This is the largest mechanical change; isolate it in its own ticket so the
  refactor is reviewable independent of the new functionality.

### B2 — `workspace/` package skeleton
- `workspace/workspace.go` — `Workspace` type holding `owner`, `id`, `*revisionstore.Store`,
  `blobstore.Store`, the per-workspace `*sql.DB` (DuckDB), an in-memory working set
  `map[worldID]*thebibites.LoadedSave`, and an active-node set
  `map[nodeID]*noderuntime.Runtime`. A `sync.Mutex` serializes mutating ops (single writer per
  workspace DuckDB).
- `Open`/`Create(root, owner, name)` — create/lookup the `workspaces` row; open
  `<root>/workspaces/<id>/analytics.duckdb` via `duckdb.Open` + `duckdb.ApplyMigrations`; open
  the shared registry + blobstore (`blobstore.NewFSStore(<root>/blobs)`).
- Registry CRUD lives in `revisionstore` (A2); the workspace package calls it.

---

## Track C — World ops

### C1 — `AddWorld(ctx, srcPath, name) (World, error)`
The "import a save file as a new world" op:
1. `tb.ParseFile(srcPath, nil)` → archive; allocate a **stable world id** (UUID);
   `CreateWorld`.
2. `tb.ExtractTables(worldID, archive)` (pass the **world id**, not `archive.SHA256`).
3. `blobstore.Put(bytes)` → ref; `RecordScriptRun`(import) + `RecordRevisionAdvancingHead`
   (`world_id`=worldID, `parent_id`=nil, blob ref) → sets head to the first revision.
4. `duckdb.ImportExtractedSave(ctx, wsDB, extracted)` — `ReplaceExtractedSave` deletes-by-world-id
   then inserts, composing inside the shared per-workspace DB.
- `ParseFile` is path-only; add an `AddWorldBytes` variant that writes a temp file first
  (mirror `verifyRoundTrip`'s temp-file pattern, loadedsave.go:595).

### C2 — `Load`/`Unload`/`OpenWorld`
- `Load(ctx, worldID)` — read the world's head revision's blob → temp file → `LoadInto(tmp,
  worldID, wsDB)`; stash in the working set. (Head bytes come from `blobstore.Get` via the
  revision's `BlobRef`.)
- `Unload(worldID)` — drop the in-memory `LoadedSave`; registry/DuckDB/blobs persist.
- `OpenWorld(worldID)` — return the working-set handle for reads/mutations (lazy-Load if absent).

### C3 — Commit advancing head (dual-key import)
- `CommitWorld` runs the save program against the loaded working copy and commits a revision that
  **threads parent = current head**, advances head, then updates **both** mirror partitions:
  1. `ls.prepareCommit` (blob.Put, loadedsave.go:545) → `RecordRevisionAdvancingHead`
     (parent=head, world_id, `IncBlobRef(newSha)`) → new revision id + content hash.
  2. **History import (retained):** `duckdb.ReplaceExtractedSave(ctx, wsDB, tablesKeyed(newSha))`
     imports the new revision under `save_id = newSha`. `ReplaceExtractedSave` deletes-by-that-
     `save_id` then inserts, so it composes additively — **prior revisions' partitions are left
     intact** (history accumulates). Record `mirror_schema_version`.
  3. **Working partition re-seed:** refresh `save_id = worldID` rows to the new head (so the open
     world's push-down/mirror keep working).
- **As shipped (C3):** rather than generalizing `runLoaded` / widening `script.Result`, the
  commit path is a new `thebibites.RunAndCommitWorld` returning a package-local `WorldCommit`
  (carrying the new revision id/sha), so `script.Result` and the standalone run path stay
  unchanged. The recorder (`RecordRevisionAdvancingHead`) self-refs the blob in-tx, so there is
  **no separate `IncBlobRef`** call (the C1/C3 invariant; a second increment would break
  eviction). Churn DoD is the counter-based one: `writeArchiveCount <= 1`, `ls.reparseCount == 0`,
  one history import + one working re-seed per commit. The dual-key re-import does require **one
  reparse of the committed blob** per commit (`reparseCommitted`): `Session.Apply` rewrites entry
  bytes but leaves the typed parser projections stale (session.go:21-23), so `ExtractTables` on
  the in-memory archive would mirror pre-mutation rows. "Zero reparses" therefore holds for the
  LoadedSave serialization path, **not** the workspace mirror projection.

### C4 — Query (working copy + full history + whole workspace), read-only enforced
- **Working copy (current):** existing push-down (`sql.go`, `collection.go`, aggregates) via the
  working-set `LoadedSave` (`ls.saveID=worldID` scopes every `whereClause`/`fromClause`/mirror to
  the open world — sql.go:237). Unchanged.
- **Full world history:** `world.history_query(ctx, sql)` over the retained per-revision history
  partitions, `JOIN mirror_saves` (ATTACH-ed) `WHERE world_id = ?` — enables time-series across
  revisions / `sim_time`. Excludes the mutable working partition.
- **Whole workspace (all worlds, broadest scope):** `Query(ctx, sql)` over all history partitions
  in the workspace DuckDB (no world filter), join the catalog for world/sim_time/tier
  attribution. Reuses `duckdb.QuoteIdent` + `scanRowsToDicts` (sql.go:80). This is the widest
  query scope; no multi-workspace federation.
- **Read-only enforcement (required for the shared persistent DB):** history/workspace query
  paths (`world.history_query`, `workspace.query`, and raw `s.sql`) run on a **read-only DuckDB
  connection / reject non-SELECT**. A stray `UPDATE`/`DELETE` in raw SQL would corrupt sibling
  worlds' partitions and diverge from the blob source-of-truth (harmless in the old in-memory-
  per-run model, dangerous now). Mutations flow only through the staged-write pipeline + tiering
  ops, never raw SQL.

---

## Track D — Node management (orchestrate existing IPC)

### D1 — Node lifecycle
- `StartNode(ctx, spec)` — wrap `noderuntime.Start` (process + optional compat connect);
  persist a `nodes` row (`CreateNode`), `BindNode` to a world, store the `*noderuntime.Runtime`
  in the active set. `ListNodes`/`Node(id)` expose handles. `StopNode`/`KillNode` →
  `Runtime.Stop`/`Kill`, update status.

### D2 — simctl passthrough
- `NodeInfo/NodeStop/NodeResume` construct `simctl.New(runtime)` (the `Runtime` already satisfies
  `simctl.Requester`) and call `Info`/`Stop`/`Resume`. Surface a `NodeState` combining
  `Runtime.State()` + last `InfoResult`.

### D3 — Reload + IngestAutosave (ship/receive)
- `ReloadNode(ctx, nodeID)` — materialize the bound world's head blob to the node's
  `drop_path` (the reload/drop path), then `simctl.Reload` (game reloads the new save).
- `IngestAutosave(ctx, nodeID, path)` — the game→workspace direction: stabilize (size/mtime
  settle per the architecture doc), copy, `tb.ParseFile`, **dedup by content hash** (skip if the
  head's `sha256` already matches), then `blobstore.Put` + `RecordRevisionAdvancingHead`
  (parent=head, world_id) + `ReplaceExtractedSave` → head advances to the ingested autosave.
  `path` defaults to the node's current `InfoResult.LastAutosave.Path`.
- These are Go methods; **automation/scheduling is driven from Starlark (Track E)**, not a
  built-in watcher.

---

## Track E — Starlark automation bindings (the "bindings to automate")

A **new, effectful** binding layer — separate package/file (e.g. `workspace/automation.go`,
producing a `workspace` predeclared global), reusing the existing engine `script.Run(ctx,
program, globals, opts)` with an effectful globals dict (NOT the sandboxed `save` Globals).

### E1 — `workspace` / `node` / `world` globals
Bindings (host-trusted; each maps to a Track C/D Go method). `workspace` is the injected root
object; **`open()` returns a `Save` object** — there is no `save` global:
```python
workspace.worlds()                 # -> [world handles]
workspace.world(id)                # -> world handle
workspace.add_world(path, name)    # -> world (C1)
workspace.open(path)               # -> Save object (ad-hoc / ephemeral one-world)
workspace.nodes() / workspace.node(id)
workspace.start_node(world=id, path=exe, compat_addr=...)   # D1
workspace.transfer(src=worldA, dst=worldB, ...)             # F2
workspace.query(sql)               # READ-ONLY across all worlds in this workspace (C4)
workspace.gc()                     # GC unreferenced blobs (G3)

world.id / world.name / world.head / world.sim_time
world.open()                       # -> Save OBJECT bound to this world's head (E2)
world.query(sql)                   # working-copy read (C4)
world.history_query(sql)           # READ-ONLY across all this world's revisions (C4)
world.evict_history(keep_last=N)   # blob-evict old revisions, keep mirror (G2)
world.unload()

# Save OBJECT (the former `save` global), obtained from open():
s = world.open()
s.bibites.where("energy > 100").set("energy", 50)
s.sql("SELECT count(*) FROM bibites")
s.commit()                         # advances the world head (E2)

node.id / node.run_id / node.world
node.info()                        # simctl INFO -> dict incl. last_autosave (D2)
node.stop() / node.resume(scale)   # D2
node.reload()                      # ship head -> drop path + RELOAD (D3)
node.ingest_autosave(path=None)    # append autosave -> world head (D3)
node.kill()
```
- Effectful builtins return Starlark dicts/values (reuse `fromSQLValue`/`scanRowsToDicts`
  conversions). Errors surface as clean Starlark errors.
- Document the trust boundary: this surface does IO/IPC and is host-trusted; the **Save object's**
  transform/commit methods remain the sandboxed staged-write pipeline. A single automation
  invocation performs **one cycle**; the host re-invokes it on a timer or autosave event
  (Starlark stays non-looping/hermetic).

### E2 — `world.open()` returns a Save object; `commit()` advances head
- `world.open()` materializes the world head into a `Save` object (Track C2: blob → `LoadInto`
  with the world id + shared DuckDB). The object is the former `save` global, now a value.
- `s.commit()` on a world-bound Save runs the existing prepare-commit + records a revision
  **threading parent = current head**, advances `head_revision_id` (C3), and re-imports the
  projection. Churn DoD preserved.
- A full automation cycle, all object-based:
  ```python
  n = workspace.node("alpha")
  if n.info()["last_autosave"]:
      n.ingest_autosave()              # live game state -> world head
  s = n.world.open()                   # Save OBJECT (not a global)
  s.bibites.where("age > 3600").delete()
  s.commit()                           # advance world head
  n.reload()                           # ship new head -> game reloads
  ```

---

## Track F — Cross-world transfer (capstone) + DSL surface

### F1 — Implement `savemutator/thebibites/workspace.go`
Realize the `Workspace` interface (`Destination()/AppendArray/AppendEntry` + `CollectedElement`):
open source + dest worlds, **query/collect** elements from source (reusing the push-down +
`CollectedElement{SourcePath,Table,JSON}`), `StageSQLAppend`/`StageAppendBibite` into the dest
session (`session.go:332`, `sqlref.go:121`), commit dest → new head (C3). **Start with the
simplest canonical target (settings copy, per the P3 stub), then whole-bibite append.**

### F2 — Expose transfer + replace the DSL P3 stub
- `workspace.transfer(src, dst, …)` in the automation surface (E1) drives F1.
- Replace the erroring P3 stub reserved in `DSL_completion_plan.md` (the `workspace`/`open()`
  binding) — transfer now lives on the real workspace automation surface.

---

## Track G — Tiered eviction & refcounted GC (memory reclamation)

The "delete the save, keep the queryable mirror, never rematerialize" capability. Two tiers
(`full → mirror_only`); the mirror is a structurally one-way projection.

### G1 — Blob refcounting
- A blob (by `sha256`) is referenced by every revision pointing at it (clones + content dedup
  share one blob). On `RecordRevision`/`AddWorld`/`IngestAutosave` call `IncBlobRef(sha)`; on
  hard revision delete (out of scope v1) `DecBlobRef`. Refcount lives on `save_revisions`/a
  `blob_refs` view keyed by sha256 (A2). This is the safety net for eviction + GC.

### G2 — `EvictWorldHistory(ctx, worldID, policy)` / `EvictRevisionBlob(ctx, revisionID)`
- The memory-saving op. Selects eviction candidates (e.g. policy = "all but last N revisions",
  or "older than sim_time T") **excluding the world head and any other world's head**, then per
  candidate: ensure the history partition is present (it always is), `EvictRevisionBlob`
  (tier→`mirror_only`, `blob_present=0`) and `blobstore`-delete the bytes **only if refcount
  drops to 0**.
- **Crash-safe ordering:** flip catalog (`tier`/`blob_present`) + commit the SQLite tx (fsync)
  **before** deleting blob bytes; the blobstore delete is idempotent. A startup
  `ReconcileBlobs` repairs catalog-vs-blobstore drift (mark `mirror_only` any `full` revision
  whose blob is missing; re-`full` any `mirror_only` whose blob is present).
- History rows are **never** deleted here — `mirror_only` saves stay fully queryable.

### G3 — GC sweep + re-promotion
- `GCUnreferencedBlobs(ctx)` deletes blobstore objects whose sha256 has `refcount=0` and no
  `full` revision needs them (orphans from the current no-delete model + future hard deletes).
  Manual/host-triggered, not automatic.
- `PromoteRevision` runs opportunistically: if a `mirror_only` revision's blob reappears by hash
  (re-import, ingest, or shared by another world), restore `tier=full`, `blob_present=1` —
  eviction forgets bytes, not identity.

### G4 — Guardrails on the blob-dependent paths
- Every op needing bytes — `Load`/`OpenWorld` (C2), `ReloadNode` (D3), transfer source read (F1),
  any future export — checks `blob_present`; on a `mirror_only` save it returns a typed
  `ErrNotRematerializable` surfaced as a clean Starlark error. There is **no** mirror→blob
  fallback (structural).
- **Schema-freeze rule:** persistent-mirror migrations must be **additive/nullable**. A `full`
  revision can be re-derived from its blob on schema change; a `mirror_only` revision is frozen
  at its `mirror_schema_version`, so cross-history queries null-fill columns newer than a row's
  version. Document this as the price of eviction.

## Key risks / subtle failure modes (difficult-tier)

- **Identity/species re-linking on transfer (F1)** — a grafted bibite's `body.id`, species id,
  parent links, brain refs must be reconciled to the dest world's id space; species must be
  dedup/merged. The hard part — phase it, fail loudly on unhandled cases.
- **Dual-key discipline** — history partitions are keyed by **revision `sha256`** (immutable,
  retained); the working partition is keyed by **world id** (mutable). A commit must import the
  new revision under its hash *without* replacing prior revisions, and re-seed only the world-id
  working partition. Confusing the two either corrupts history (mutating a hash partition) or
  loses it (replacing history on commit).
- **Eviction must never strand the head or a shared blob** — head-protection + refcount checks
  before any blob delete; crash-safe ordering (catalog fsync before byte delete);
  `ReconcileBlobs` on startup. A mirror→blob path must never be introduced (keeps
  non-rematerialization structural).
- **Schema-freeze** — once a revision is `mirror_only` its mirror rows can't be re-derived;
  persistent-mirror migrations stay additive/nullable and cross-history queries null-fill.
- **Head/parent lineage atomicity** — insert revision + `SetWorldHead` in one tx
  (`RecordRevisionAdvancingHead`); a crash must not leave head pointing at a missing revision.
- **DuckDB single-writer** — one workspace DuckDB handle; serialize mutating ops with the
  workspace mutex (AddWorld/Commit/Ingest/ReplaceExtractedSave).
- **Autosave stability (D3)** — never parse a file the game is still writing; stabilize then
  hash-dedup before recording a revision.
- **Automation trust boundary (E)** — the effectful `workspace` surface must stay separate from
  the sandboxed `save` Globals; do not leak IO/IPC builtins into the save DSL.

## Verification

- **Save-object cutover (B3):** the whole `script/thebibites` suite, rewritten to obtain the
  save via `open()`, stays green — proves the `save` global removal is behavior-preserving
  (same staged-write/commit semantics, just object-delivered).
- **Registry (A):** CRUD round-trips (model on `revisionstore/store_test.go`); world head
  advances on record; revision carries `world_id` + parent chain.
- **Multi-world isolation (C):** two worlds imported into one workspace DuckDB; assert
  `save_id`-filtered counts (model on `duckdb/import_test.go`); a `set` on world A's working copy
  is invisible to a query on world B (mirror scoping).
- **History retention + dual-key (C3/C4):** commit a world 3× → assert **3 history partitions
  persist** (one per revision sha256) and a `history_query` returns the time-series across
  `sim_time`; a commit does **not** delete prior revisions' rows; the working partition reflects
  only the head.
- **Tiering (G):** evict an old revision → blob gone from blobstore, `tier=mirror_only`,
  history rows still queryable, `world.open()` on that revision (or transfer/reload using it)
  fails with `ErrNotRematerializable`; head and a clone-shared blob are **refused** for eviction;
  re-introducing the blob by hash re-promotes to `full`; GC frees only refcount-0 orphans;
  kill-after-catalog-flip-before-byte-delete is reconciled to `mirror_only` on restart.
- **Working set (C2/C3):** load/unload lifecycle; commit advances head + re-imports; **churn
  DoD** — `writeArchiveCount ≤ 1`, `reparseCount == 0`, one DuckDB import per AddWorld/commit
  (`DSL_completion_plan.md`).
- **Nodes (D):** with a fake compat server (reuse the `simctl` test fake pattern) assert
  start→bind→info→stop; `ReloadNode` writes head bytes to the drop path + sends RELOAD;
  `IngestAutosave` appends a revision and advances head; hash-dedup skips an unchanged autosave.
- **Automation (E):** a `workspace` program drives `node.info → world.run → node.reload`
  against fakes; assert head advanced and the dest file equals the committed head.
- **Transfer (F):** create workspace → AddWorld ×2 → cross-world `Query` → transfer (settings,
  then a bibite) → reparse dest, assert grafted values + reconciled ids/counts.
- **Whole suite:** `go test ./...`.

## Out of scope (v1)

Auth/multi-tenant; HTTP/`api` + `control` agent surface; always-on autosave file-watcher (the
ingest *primitive* ships; the *scheduler* is the automation script's job); distributed
coordinator; new IPC wire verbs / DLL changes; brain-graph integrity on transfer;
cross-reference reconciliation beyond identity/species; renaming the generated DuckDB `save_id`
column. **Tiering scope (v1):** two tiers only — a third `metadata_only` tier (dropping a
revision's mirror rows too) and a Parquet/cold-tier mirror backend are deferred; hard-deleting
revisions; backfilling the mirror schema of already-evicted (`mirror_only`) revisions; automatic
(policy-daemon) eviction — eviction/GC are explicit host/script-triggered ops in v1.

## Future extensions (noted, not v1)

- **Specimen library — storing individual bibites in the workspace.** A bibite is already a
  self-contained `bibites/bibite_N.bb8` JSON entry: parsed standalone (`parse_entities.go:5`),
  round-tripped losslessly via `Bibite.Raw` (`session.go:419`), with a self-contained 11-table
  DuckDB projection (`bibites` + `bibite_genes`/`_body`/`_brain_nodes`/`_brain_synapses`/… keyed
  by `save_id+entry_name+body_id`). A future track would store specimens as **content-addressed
  blobs in the existing blobstore** (dedup by sha256), cataloged in a `specimens` SQLite table
  (`id, workspace_id, kind, name, source world/revision/entry_name, blob_ref`), and **projected
  into the same workspace DuckDB** under a synthetic `save_id` (= specimen hash) with a `kind`
  column on `mirror_saves` (`world_revision | specimen`) — so stored bibites are queryable and
  **tier-evictable exactly like world revisions**.
  - **Fidelity: bibite + species template bundle (self-contained organism).** Store the `.bb8`
    *plus* its species record so injection adds the species if missing. Requires **new
    single-species extraction** from the array-based `speciesData.json` (not implemented today),
    and a thin reader for a bare on-disk `.bb8` export (the parser handles `.bb8` *entries inside
    a zip*, not a standalone file).
  - **This generalizes Track F:** world→world transfer becomes *extract → (store as specimen) →
    inject*. Ops would be `world.extract_bibite(ref) → specimen` (collect side of F1) and
    `world.inject(specimen)` (`StageAppendBibite` `session.go:331` + identity/species re-linking),
    advancing the destination head. The same id-remap hard problem as F applies (fail loudly on
    unhandled cases).