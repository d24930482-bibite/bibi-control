# 08. Workspace automation + live node control

**Scope (files scanned):** `workspace/automation.go`, `workspace/node.go`, `workspace/node_control.go`, `workspace/node_logs.go`, `workspace/node_reload.go`, `noderuntime/runtime.go`, `simctl/simctl.go`, `control/controller.go`. (`workspace/workspace.go` read for the `Workspace` struct/locks; `working_set.go`/`commit.go`/`transfer.go`/`world.go` referenced only to locate methods the bindings delegate to — out of this slice.)

## Location map

### Starlark automation binding layer (`workspace/automation.go`)
- `automation.go:39` — [READ|WRITE] [WORKSPACE] — `RunAutomation`: one hermetic Starlark cycle (`script.Run`); host re-invokes on a timer/event (non-looping language).
- `automation.go:46` — [READ|WRITE] [WORKSPACE] — `AutomationGlobals`: builds the predeclared `{"workspace": *workspaceValue}` dict carrying `ctx`+`ws`.
- `automation.go:84` — [READ] [WORKSPACE] — `workspace.bibites/eggs/pellets`: aggregate-only spanning collections over EVERY world's history (object DSL, no raw SQL).
- `automation.go:114` — [READ] [WORKSPACE] — `workspace.worlds()` → `store().ListWorlds`.
- `automation.go:132` — [READ] [WORLD] — `workspace.world(id)`: `GetWorld` by id, then `worldByName` fallback.
- `automation.go:156` — [READ] [WORLD] — `worldByName`: case-sensitive `w.Name == name` scan; 0=error, 1=ok, >1=ambiguous (see missing.md §3, not re-filed).
- `automation.go:178` — [WRITE] [WORLD] — `workspace.add_world(path,name)` → `ws.AddWorld` (imports a save as a new world; impl out of slice).
- `automation.go:191` — [READ] [WORKSPACE] — `workspace.nodes()` → `PersistedNodes` (registry rows, active+historical).
- `automation.go:207` — [READ] [WORLD] — `workspace.node(id)`: linear scan of `PersistedNodes` for `NodeID == id`.
- `automation.go:225` — [WRITE] [WORLD] — `workspace.start_node(world=,path=,compat_addr=,drop_path=,node_id=,run_id=,connect=)` → `ws.StartNode`.
- `automation.go:262` — [READ] [WORKSPACE] — `workspace.query(sql)`: raw read-only SQL escape hatch across all-world history (`ws.Query`).
- `automation.go:285` — [WRITE] [WORLD] — `workspace.transfer(selector,dst)`: grafts DSL-selected bibites/eggs into dst world, commits one advancing-head revision (`ws.Transfer`; impl out of slice).
- `automation.go:367` — [READ] [WORLD] — `worldValue.Attr`: id/name/head/sim_time fields + bibites/eggs/pellets history collections + builtins.
- `automation.go:415` — [READ|WRITE] [WORLD] — `world.open()` → `ws.OpenWorld` (lazy-load working copy) wrapped as a `saveValue` (read+mutate+commit).
- `automation.go:432` — [READ] [WORLD] — `world.query(sql)`: read-only SELECT over the OPEN working partition; `ensureReadOnly` gate, sees staged-but-uncommitted rows.
- `automation.go:456` — [READ] [WORLD] — `world.history_query(sql)`: raw read-only SQL over this world's retained history (`ws.HistoryQuery`).
- `automation.go:469` — [WRITE] [WORLD] — `world.evict_history(keep_last=|older_than=)` → `ws.EvictWorldHistory` (impl out of slice).
- `automation.go:516` — [WRITE] [WORLD] — `world.load()` → `ws.OpenWorld` (materialize working copy, discard handle).
- `automation.go:527` — [WRITE] [WORLD] — `world.unload()` → `ws.Unload` (drop cached working copy).
- `automation.go:575` — [READ|WRITE] [WORLD] — `saveValue.Attr`: delegates whole DSL surface to `thebibites.Save`; `commit` is shadowed by E2's head-advancing commit.
- `automation.go:590` — [WRITE] [WORLD] — `save.commit()` → `ws.CommitWorldLoaded` (advances head over already-staged session; impl out of slice).
- `automation.go:631` — [READ] [WORLD] — `nodeValue.Attr`: id/run_id/world/status fields (from the persisted row snapshot) + control builtins.
- `automation.go:663` — [READ] [WORLD] — `node.info()` → `ws.NodeInfo` (live IPC telemetry).
- `automation.go:675` — [READ] [WORLD] — `node.state()` → `ws.NodeState` (runtime/connection + optional INFO).
- `automation.go:687` — [WRITE] [WORLD] — `node.stop()` → `ws.NodeStop` (PAUSE the sim via IPC; NOT process stop).
- `automation.go:699` — [WRITE] [WORLD] — `node.resume(scale)` → `ws.NodeResume` (run sim at time scale via IPC).
- `automation.go:712` — [WRITE] [WORLD] — `node.reload()` → `ws.ReloadNode` (ship head blob → game RELOAD).
- `automation.go:726` — [WRITE] [WORLD] — `node.ingest_autosave(path=None)` → `ws.IngestAutosave` (game save → new world revision).
- `automation.go:748` — [WRITE] [WORLD] — `node.kill()` → `ws.KillNode` (force-kill process, drop from active set).
- `automation.go:924` — [READ] [WORLD] — `node.wait(sim_time=,paused=,autosave_after=,timeout=,poll_every=)`: BLOCKS polling `NodeInfo` until predicate holds; graceful timeout.
- `automation.go:981` — [READ|WRITE] [WORLD] — `workspace.poll(do=,every=,timeout=,max_iters=)`: BLOCKS calling `do()` until truthy / timeout / max_iters (do() may mutate).
- `automation.go:1046` — [READ] [WORLD] — `buildWaitPredicate`: AND of sim_time>=target, paused==want, last_autosave.modified_unix>marker.

### Workspace node lifecycle (`workspace/node.go`)
- `node.go:17` — [n/a] [WORLD] — `StartNodeSpec`: world binding + process + compat/dial + drop_path; wraps `noderuntime.Spec`.
- `node.go:64` — [WRITE] [WORLD] — `StartNode`: launches process, persists `nodes` row (`CreateNode`, status "running"), records active runtime; double-checked uniqueness (dup-id + one-node-per-world) around the lock-dropping `noderuntime.Start`; orphan-kills on any post-start failure.
- `node.go:105/111` — [WRITE] [WORLD] — stdout/stderr wrapped with `logBufferWriter` (and MultiWriter if caller supplied a sink) so process output feeds the log ring.
- `node.go:191` — [READ] [WORKSPACE] — `Nodes()`: snapshot copy of the active-set runtimes (under w.mu).
- `node.go:206` — [READ] [WORLD] — `Node(nodeID)`: active runtime lookup (under w.mu); the lock-release point all IPC paths rely on.
- `node.go:218` — [READ] [WORKSPACE] — `PersistedNodes` → `ListNodes` (registry passthrough).
- `node.go:232` — [WRITE] [WORLD] — `StopNode`: `rt.Stop` (graceful, outside lock) + Close, delete from active set, `dropLogRing`, status → "stopped".
- `node.go:265` — [WRITE] [WORLD] — `KillNode`: `rt.Kill`+Close, delete from active set, `dropLogRing`, status → "stopped".
- `node.go:299` — [READ] [WORLD] — `activeNodeForWorldLocked`: one-node-per-world enforcement anchored on `w.nodes` membership (stale "running" rows don't block).
- `node.go:324` — [WRITE] [WORLD] — `setNodeStatusByLogicalID`: resolves PK from logical id via `ListNodes`, then `SetNodeStatus`.

### Live IPC control (`workspace/node_control.go`)
- `node_control.go:21` — [n/a] [WORLD] — `NodeState`: runtime State + hoisted `Connected` + nilable `*InfoResult`.
- `node_control.go:41` — [READ] [WORLD] — `NodeInfo`: `Node()` peek then `simctl.New(rt).Info` — IPC round-trip WITHOUT w.mu (the basis for park-outside-lock waits).
- `node_control.go:62` — [WRITE] [WORLD] — `NodeStop`: `simctl.Stop` (pause sim) without w.mu.
- `node_control.go:85` — [WRITE] [WORLD] — `NodeResume`: `simctl.Resume(timeScale)` without w.mu; >0 enforced server-side only.
- `node_control.go:111` — [READ] [WORLD] — `NodeState`: `rt.State()`+`rt.Connected()`; INFO only when connected (disconnected = success+nil Info, connected+INFO-fail = error).

### Node log capture (`workspace/node_logs.go`)
- `node_logs.go:11` — [n/a] [WORLD] — `LogLine` {Time, Level("info"/"error"), Text}.
- `node_logs.go:22` — [WRITE] [WORLD] — `logRing`: bounded (1000-line) thread-safe ring; front-trim eviction; `partial` holds bytes since last `\n`.
- `node_logs.go:60` — [WRITE] [WORLD] — `logBufferWriter.Write`: splits stdout/stderr on `\n` (strips `\r`), appends lines; never short-writes/errors (exec copier contract).
- `node_logs.go:101` — [WRITE] [WORLD] — `logRingFor`: lazy per-node ring under `w.logMu` (separate from w.mu).
- `node_logs.go:117` — [WRITE] [WORLD] — `dropLogRing`: removes a node's ring (called by Stop/Kill).
- `node_logs.go:131` — [READ] [WORLD] — `NodeLogs`: snapshot copy; error distinguishes "no buffer" from "zero lines".

### Reload + autosave ingest (`workspace/node_reload.go`)
- `node_reload.go:45` — [READ→WRITE] [WORLD] — `ReloadNode`: resolve runtime + row → fetch head blob → atomic drop-file write → `simctl.Reload`. Ordering load-bearing (durable bytes BEFORE RELOAD). Ships persisted head, not staged copy.
- `node_reload.go:102` — [READ] [WORLD] — `headBlobBytes`: under w.mu, `GetWorld`→`RevisionByID`→`blobs().Get`; `BlobPresent` guard → `notRematerializable`.
- `node_reload.go:138` — [WRITE] [WORLD] — `writeDropFileAtomic`: temp file → write → fsync → close → atomic rename (crash-safe drop).
- `node_reload.go:197` — [READ→WRITE] [WORLD] — `IngestAutosave`: default path from live INFO → stabilize file → parse → dedup vs head SHA256 (no-op skip) → append revision.
- `node_reload.go:298` — [WRITE] [WORLD] — `appendIngestedRevision`: the single-DuckDB-writer critical section under w.mu — blob Put, RecordScriptRun, re-read head as parent (TOCTOU), `RecordRevisionAdvancingHead`, dual-key DuckDB import (`ReplaceExtractedSave`+`CopySavePartition`), drop cached working copy. No reparse (archive is fresh from disk).
- `node_reload.go:405` — [READ] [WORLD] — `nodeRowByLogicalID`: logical-id → persisted row via `ListNodes` (outside w.mu).
- `node_reload.go:422` — [READ] [WORLD] — `waitForStableFile`: poll (Size,ModTime) until stable across 2 polls; loud on timeout/missing/cancel.

### Opaque runtime (`noderuntime/runtime.go`)
- `runtime.go:23` — [n/a] [WORLD] — `Spec`: process + optional compat TCP session config; package owns no persistence/save semantics.
- `runtime.go:47` — [n/a] [WORLD] — `Runtime`: `sync.RWMutex`-guarded `process`+`session`; the in-memory representation of one live node.
- `runtime.go:85` — [WRITE] [WORLD] — `Start`: `ipc.StartProcess` then optional `Connect` (dial+retry, kill on connect failure).
- `runtime.go:142` — [READ] [WORLD] — `State()`: PID/ProcessInfo/CompatAddr/Connected snapshot under RLock.
- `runtime.go:176` — [READ] [WORLD] — `Connected()`: `session != nil`.
- `runtime.go:189` — [WRITE] [WORLD] — `Connect`: `ipc.Dial` retry loop until ctx expiry; swaps in new session, closes old.
- `runtime.go:239/249` — [READ|WRITE] [WORLD] — `Request`/`Notify`: send over session or `ErrNoSession` (simctl rides Request).
- `runtime.go:271` — [READ] [WORLD] — `Wait`: block on process exit.
- `runtime.go:281` — [WRITE] [WORLD] — `Kill`: kill process.
- `runtime.go:294` — [WRITE] [WORLD] — `Stop`: optional graceful one-way command → wait grace period → kill-after-grace fallback.
- `runtime.go:339` — [WRITE] [WORLD] — `Close`: close + nil the session (process untouched).

### Typed sim-control client (`simctl/simctl.go`)
- `simctl.go:23` — [n/a] [WORLD] — `Requester`: minimal `Request` surface (Session/OpaqueNode/Runtime all satisfy it).
- `simctl.go:37` — [WRITE] [WORLD] — `Stop`: `ipc.CommandStop` (pause, returns previous time scale).
- `simctl.go:44` — [WRITE] [WORLD] — `Resume`: `ipc.CommandResume{TimeScale}`.
- `simctl.go:52` — [READ] [WORLD] — `Info`: `ipc.CommandInfo` (tps/real_tps/paused/sim_time/last_autosave).
- `simctl.go:59` — [WRITE] [WORLD] — `Reload`: `ipc.CommandReload` (game reloads most-recent save = the just-shipped drop file).

### control (`control/controller.go`)
- `controller.go:1` — [n/a] [—] — empty stub: `package control` only; no controller exists yet in this slice.

## Read paths

- **Live telemetry (no DB).** `node.info()`/`node.state()` → `NodeInfo`/`NodeState` (`node_control.go:41,111`) → `Node()` peek (drops w.mu) → `simctl.New(rt).Info` → `rt.Request` → `ipc.Session`. The round-trip holds NO workspace lock — this is what makes blocking waits cheap.
- **Blocking automation reads.** `node.wait(...)` (`automation.go:924`) loops `NodeInfo` against an AND-predicate (`buildWaitPredicate`), sleeping on a bare `select` over `ctx`/`deadline`/`poll_every`; holds no lock while parked. `workspace.poll(...)` (`automation.go:981`) is the general form, re-invoking a Starlark `do()` (which itself may read OR mutate). Cancel = hard error; timeout = graceful dict.
- **World/working-copy reads.** `world.query` (`automation.go:432`, read-only-gated, sees staged uncommitted rows via the open `LoadedSave`), `world.history_query`/`world.bibites…` (retained history), `workspace.query`/`workspace.bibites…` (all-world history). All go through DuckDB mirror partitions (impl in query/spanning slices).
- **Registry reads.** `workspace.nodes/node`, `Node`/`Nodes`, `activeNodeForWorldLocked`, `nodeRowByLogicalID`, `setNodeStatusByLogicalID`, `worldByName` all read `ListNodes`/`ListWorlds`/`GetWorld` from the SQLite registry. `nodeValue`'s `id/run_id/world/status` are a **frozen snapshot** of the row taken when the handle was created (`automation.go:631`).
- **Reload source read.** `headBlobBytes` (`node_reload.go:102`) reads the bound world's head revision + blob under w.mu (persisted head, never the staged working copy).
- **Log reads.** `NodeLogs` (`node_logs.go:131`) snapshots the per-node ring under `w.logMu`.

## Mutation paths

- **Process lifecycle.** `StartNode` (`node.go:64`): pre-check uniqueness → `noderuntime.Start` (lock dropped) → re-check both invariants → `CreateNode` (registry, status "running") → record runtime in `w.nodes`. Orphan-kills the process on any post-start failure. `StopNode`/`KillNode` (`node.go:232,265`): IPC/process teardown outside the lock, then `delete(w.nodes,…)`, `dropLogRing`, status → "stopped". `Workspace.Close` (`workspace.go:269`) best-effort kills all active runtimes (no status write).
- **Live sim control (IPC, no DB).** `node.stop` (pause), `node.resume(scale)` → `NodeStop`/`NodeResume` → `simctl` commands without w.mu. These mutate the *running game*, not the committed state.
- **Reload (workspace → game).** `ReloadNode` (`node_reload.go:45`): head blob → crash-safe drop file (`writeDropFileAtomic`) → `simctl.Reload`. Mutates the running game's loaded save from the committed head; commits nothing.
- **Autosave ingest (game → workspace).** `IngestAutosave`/`appendIngestedRevision` (`node_reload.go:197,298`): the only commit-grade path in this slice. Stabilize+parse+dedup outside the lock; under w.mu: blob Put → script-run record → re-read head as parent (TOCTOU) → `RecordRevisionAdvancingHead` → dual-key DuckDB import (working then derived history) → drop cached working copy.
- **World/transfer mutations.** `world.open().…set()/.delete()` then `save.commit()` (`CommitWorldLoaded`), `workspace.transfer` (`Transfer`), `world.evict_history` (`EvictWorldHistory`), `add_world`/`load`/`unload`. These advance heads / change the working set; their bodies are in the commit/transfer/eviction/working-set slices, but they ARE reachable from automation here.
- **Log capture.** `logBufferWriter.Write` (`node_logs.go:60`) continuously mutates the per-node ring as the process emits stdout/stderr.

**DuckDB-mirror / committed-state interaction.** Automation never touches DuckDB directly; it goes through workspace methods. The single per-workspace DuckDB writer is serialized by `w.mu`, held only inside `appendIngestedRevision` (and the out-of-slice commit/transfer/import paths). Reads (`world.query` etc.) hit mirror partitions through the shared handle. After an ingest, the cached `LoadedSave` for the world is dropped so a later `OpenWorld` lazy-reloads at the new head — automation code that ran a `wait` MUST re-open/re-read afterward (state can move under a long-parked run; see the concurrency note at `automation.go:884-915`).

## Missing seams

### Empty `control` package — no controller / supervisor exists
**What's missing.** `control/controller.go` is a bare `package control` stub (`controller.go:1`). There is no daemon-side controller, node supervisor, or process-crash watcher: nothing reconciles a node whose OS process died on its own. A crashed game leaves the persisted `nodes` row at status "running" and the runtime in `w.nodes` until someone explicitly Stops/Kills it; `activeNodeForWorldLocked` keeps the world "bound" to a dead runtime, blocking a fresh `StartNode` for that world.
**Consequence.** Crash recovery and restart are operator-manual; a crashed node can wedge its world. Likely overlaps with the **lifecycle** and **UI/daemon** slices — flag for de-dup.
**Where it lives.** `control/controller.go:1`; bind-conflict logic at `node.go:299-318`; teardown only via explicit `StopNode`/`KillNode` (`node.go:232,265`).

### `nodeValue` carries a frozen row snapshot; status field can lie after control ops
**What's missing.** `nodeValue` holds a `revisionstore.Node` captured at handle-creation (`automation.go:613`); `node.status` returns `v.node.Status` directly (`automation.go:639`). After the same script calls `node.stop()`/`node.kill()` (which write "stopped" via `setNodeStatusByLogicalID`) or after a concurrent run mutates the row, `node.status` still reports the stale snapshot. Only `node.info()`/`node.state()` re-hit the live runtime.
**Consequence.** A script that branches on `node.status` after acting on the node reads stale data; the documented "re-read after wait" discipline is needed but not enforced for the registry-snapshot fields. Minor; partly inherent to the snapshot model.
**Where it lives.** `automation.go:610-660` (snapshot + field accessors); contrast live `NodeInfo`/`NodeState` at `node_control.go:41,111`.

### No log binding in automation; logs are memory-only and dropped on stop
**What's missing.** The log ring (`node_logs.go`) is exposed via Go `NodeLogs` but has **no Starlark binding** on `nodeValue` (`AttrNames` at `automation.go:627` lists no `logs`/`tail`). The ring is in-memory, bounded to 1000 lines, never persisted, and `StopNode`/`KillNode` call `dropLogRing` — so logs vanish the moment a node stops and are unreadable from an automation script even while running.
**Consequence.** An automation run cannot inspect a node's output to make decisions, and post-mortem logs are gone after stop. Likely overlaps the **UI/daemon** slice (the UI logs drawer in commit U13) — flag for ownership.
**Where it lives.** `node_logs.go:117,131` (drop + Go reader); absence in `automation.go:627`.

### `node.wait` predicate cannot observe process death or disconnect
**What's missing.** `node.wait` (`automation.go:924`) polls `NodeInfo`, which fails hard if the node leaves the active set or loses its session. The predicate (`buildWaitPredicate`) only ANDs sim_time/paused/autosave conditions; there is no "process exited" / "disconnected" terminal condition. If the game dies mid-wait, the very next `NodeInfo` returns an error and the wait raises rather than reporting a clean terminal state.
**Consequence.** A long `wait` on a crashing node surfaces an opaque IPC error instead of an actionable "node died" signal; combined with the missing supervisor, the script cannot gracefully handle node loss.
**Where it lives.** `automation.go:957-961` (NodeInfo error → raise), `automation.go:1046-1079` (predicate set); related to the empty `control` package above.

### `workspace.gc()` is a deliberately-deferred name (G3), not bound
**What's missing.** `Attr` returns `(nil,nil)` for unbound names (`automation.go:107-110`); the file header (`automation.go:14-15`) notes `workspace.gc()` is intentionally NOT bound (G3). An automation script that references it gets a generic Starlark "has no .gc attribute" rather than a "deferred/not-yet-implemented" hint.
**Consequence.** Cosmetic — but blob/revision GC is unreachable from automation, so cleanup must run out-of-band. Documented-deferred, not a true gap.
**Where it lives.** `automation.go:14-15`, `automation.go:107-110`.
