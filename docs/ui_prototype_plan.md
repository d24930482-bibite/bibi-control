# UI Prototype — End-to-End Flow First

**Status:** design + wireframes (HTML mockup next; no production code yet)
**Priority #1:** demonstrate the *end-to-end flow*, not breadth or polish.
**Issues:** advances #11 (UI plan/design) and #16 (Starlark editor: UI); consumes #15
(process-control bindings, already implemented).

---

## 1. Backend reality (what we build on)

- **No daemon / no HTTP today.** `api/api.go` and `control/controller.go` are empty
  placeholders. Only binaries are `cmd/gen_thebibites_schema` and `cmd/livetest`.
- The system is an **in-process Go library**: a `*workspace.Workspace`
  (`Create(ctx, root, owner, name)` / `Open(ctx, root, id)`, guarded by a `sync.Mutex`)
  owning the registry (SQLite), the shared DuckDB query handle, the blob store, and live
  node handles (real child sim processes over IPC).
- **The Starlark automation layer already *is* the API.** Every operation is a binding,
  run via `RunAutomation(ctx, ws, program, opts) → script.Result {Output, Diagnostics,
  StagedOps, RevisionRef, DryRun}` (trivially JSON-serializable).

**Verified gaps the UI forces us to add:**
1. **No workspace enumeration** — only `Create`/`Open` by id. The sidebar needs the daemon
   to own a *workspaces root* + its own index (list/create/open).
2. **Liveness lives on the runtime handle** — `Runtime.State()` + `Wait()` give true
   process state; the DB `status` field is separate and goes stale. "Alive" must read the
   live handle, never the row.
3. **No script persistence** — programs are just `[]byte`. "Save a script/notebook" is
   entirely new backend (per-workspace store).

---

## 2. Two script contexts (the UI exposes both)

| Context | Globals | Run via | Purpose |
|---|---|---|---|
| **Workspace automation** | `workspace.*` | `RunAutomation` | orchestration: worlds, nodes, query, transfer |
| **Per-world mutation** | `s = world.open()` → `s.bibites…`, `s.commit()` | inside automation / `CommitWorld` | edit one world's entities + commit a revision |

Both reachable from one program, so a single `/run` endpoint covers everything:

```python
w = workspace.add_world(path="uploads/dasdasd.zip", name="alpha")
s = w.open()
s.bibites.where(s.bibites.energy < 10).set(energy=50)   # object DSL (never raw SQL for writes)
rev = s.commit()                                         # advancing-head revision
print(workspace.query(sql="SELECT count(*) FROM bibites"))   # read escape-hatch
n = workspace.start_node(world=w.id, path="<sim>", connect=True)
print(n.info()); n.stop(); n.reload()                    # live process control (needs a sim binary)
workspace.transfer(selector=w.bibites.where(...), dst=other.id)
```

---

## 3. End-to-end spine (the flow to demo)

```
add world (import) → open + inspect (query/collections) → mutate (object DSL)
  → commit revision (advances head) → view history → start node → observe (TPS/simtime)
  → control (stop/resume/reload) → ingest autosave (append + advance head) → transfer
```

The **data half** (import→query→mutate→commit→history→transfer) runs **headless against
fixtures** and is demoable immediately. The **live-node half** needs the real Bibites
binary or a stub (§7).

---

## 4. Architecture — multi-workspace daemon

A long-lived daemon (fill in `api/`) owning a **workspaces root** and a map of open
workspaces (not a singleton — the sidebar lists many, like chat threads):

```
┌──────────────┐   HTTP/JSON     ┌─────────────────────────────────────┐
│  Web UI      │ ──────────────▶ │  api daemon                         │
│ (3-col SPA)  │ ◀────────────── │  manages N workspaces under a root  │
│              │  poll /info 10s │  POST /run    (Starlark RPC)        │──▶ RunAutomation
└──────────────┘                 │  GET  /workspaces /worlds /nodes…   │──▶ structured reads
                                  └──────────┬──────────────────────────┘
                  per workspace: registry(SQLite) · DuckDB · blobs · live node IPC sessions
```

- Daemon maintains a small index of workspaces (dirs under the root + names/owners).
- Mutating runs **serialized through the target workspace's lock**; reads can be
  concurrent later. Telemetry polled **only for the open workspace's nodes**, batched.
- The daemon is the sole holder of live node IPC sessions → all control + telemetry route
  through it.

---

## 5. Notebook over terminal (execution model)

The persistent state here is the **workspace** (worlds, nodes, DuckDB), *not* Starlark
variables. So: **notebook primary** — each cell is one `/run` against the shared
workspace; a saved notebook *is* the saved script. A REPL/terminal would need a persistent
Starlark session (variable carryover), fighting Starlark's frozen-value model for little
gain. Provide one always-open "scratch" cell for a console feel.

**Baked-in caveat:** Starlark *locals* don't carry across cells — keep state in the
workspace (`workspace.world("x")` re-fetched per cell). Session-scoped variables are a
later upgrade if wanted.

---

## 6. Wireframes

### 6.1 Main workspace screen (3 columns: workspaces · live state · do-work)

```
┌────────────┬──────────────────────┬───────────────────────────────────────────────┐
│ WORKSPACES │  predator-study       │   ⚙  telemetry: every 10s ▾      ● daemon up   │
│            │  owner: aaron         ├───────────────────────────────────────────────┤
│ [+ New]    │──────────────────────│  NOTEBOOK   [▾ flow: "seed+run"] [Save] [+ Cell]│
│            │  NODES         ↻ 0:07 │ ┌───────────────────────────────────────────┐ │
│  alpha     │ ───────────────────── │ │ [1] ✓ 0.4s                          [▶][⋯]│ │
│ ▸beta   ◄  │  ● node-1   ALIVE     │ │  w = workspace.add_world(                  │ │
│  gamma     │    58 TPS  t=1.24M    │ │      path="uploads/dasdasd.zip",          │ │
│  scratch   │    [⏸ stop][⟳ reload] │ │      name="alpha")                        │ │
│            │ ───────────────────── │ │  → world alpha  (head rev1)               │ │
│            │  ○ node-2   CRASHED   │ ├───────────────────────────────────────────┤ │
│            │    exit 139  [logs]   │ │ [2] ▶ running…  ⠿                    [■][⋯]│ │
│            │    [↻ restart]        │ │  workspace.query(sql="""                  │ │
│            │ ───────────────────── │ │    SELECT species_id, count(*) n          │ │
│            │  ⦿ node-3   DETACHED  │ │    FROM bibites GROUP BY 1 ORDER BY n DESC""")│
│            │    (no live handle)   │ ├───────────────────────────────────────────┤ │
│            │    [reconnect][drop]  │ │ [3] ✓ 0.1s  · 7 rows                 [▶][⋯]│ │
│            │ ───────────────────── │ │  ┌── result ─────────────────────────────┐│ │
│            │  [+ start node]       │ │  │ species_id │  n  │ avg_energy          ││ │
│            │                       │ │  │     12     │ 410 │ 88.3                ││ │
│            │  WORLDS               │ │  │      7     │ 191 │ 61.0   [export csv] ││ │
│            │ ───────────────────── │ │  └───────────────────────────────────────┘│ │
│            │  • alpha   head rev3  │ └───────────────────────────────────────────┘ │
│            │  • beta    head rev1  │  HISTORY · alpha                               │
│            │  [+ add / upload]     │  rev3 ●──rev2 ○──rev1 ○(import)   [diff][open] │
│ ───────────│                       │  “set energy” “transfer in”  by you · 2m ago   │
│ ⓘ settings │                       │                                                │
└────────────┴──────────────────────┴───────────────────────────────────────────────┘
   col A: workspaces       col B: live state           col C: notebook + results + history
   GET /workspaces         GET /nodes/info (batch,     POST /run per cell
   POST /workspaces        poll 10s) · GET /worlds      GET /worlds/{id}/history
```

### 6.2 Node telemetry states (alive honesty)

```
 ● node-1  ALIVE              ○ node-2  CRASHED            ⦿ node-3  DETACHED
   58 TPS   real 57.8           last 0 TPS                   persisted row, but the
   sim t = 1,240,512            exit code 139                daemon holds NO live handle
   paused: no                   uptime 14m → died            (restarted daemon / orphan)
   last autosave 0:42 ago       [view logs] [↻ restart]      [reconnect] [drop row]
   [⏸ stop] [⟳ reload] [✕ kill]
```
Green = live handle + responsive (`Runtime.State()` + one batched `INFO`); Red = `Wait()`
saw exit; Grey = row with no handle (post-restart). `↻ 0:07` = countdown to next poll;
cadence control sets 5s/10s/30s/manual.

### 6.3 Notebook cell lifecycle

```
 idle ────────────────┐   running ───────────┐   ok ───────────────┐   error ──────────────┐
 [ ] code…       [▶][⋯]│   [▶ running… ⠿] [■]│   ✓ 0.4s · rev a1b2 │   ✗ 1 diagnostic      │
                       │   (cancellable)      │   staged_ops: 3     │   line 2: unknown attr│
                       │                      │   → result/▼ table  │   'bibties' [fix][▶]  │
```
Maps to `script.Result`; a committing cell shows the `rev` chip linking into History.

### 6.4 Modals

```
  NEW WORKSPACE                 START NODE                       ADD WORLD
 ┌───────────────────┐  ┌──────────────────────────┐  ┌────────────────────────────┐
 │ Name [__________] │  │ World    [ alpha      ▾] │  │ ○ Upload save (.zip)         │
 │ (owner = you)     │  │ Sim bin  [/opt/bibites ] │  │   [ choose file… ] dasdasd.zip│
 │                   │  │ Drop path[ auto         ]│  │ ○ Server path [____________] │
 │   [Cancel][Create]│  │ ☑ connect on start       │  │ Name [______]                │
 └───────────────────┘  │   [Cancel][Start ▶]      │  │   [Cancel][Import]           │
                        └──────────────────────────┘  └────────────────────────────┘
   POST /workspaces        POST /run: start_node(...)    upload → daemon fs,
                                                          then POST /run: add_world
```

### 6.5 Node logs drawer (slides over col C)

```
 ┌─ node-2 · logs ──────────────────────────────────────── [⤓ download][✕] ┐
 │ 12:03:01  INFO  sim started run-7 world=alpha tps=60                       │
 │ 12:16:44  WARN  autosave written autosave_1216.zip                         │
 │ 12:17:02  FATAL segfault in physics step  (exit 139)                       │
 │ ▸ follow ☑                                                                 │
 └────────────────────────────────────────────────────────────────────────────┘
```

---

## 7. API surface (prototype)

| Method | Path | Body / params | Returns |
|---|---|---|---|
| `GET`  | `/workspaces` | — | `[{id, name, owner}]` |
| `POST` | `/workspaces` | `{name}` | created workspace |
| `POST` | `/run` | `{workspace_id, program}` | `script.Result` JSON |
| `GET`  | `/workspaces/{id}/worlds` | — | `[{id, name, head_revision}]` |
| `GET`  | `/workspaces/{id}/nodes/info` | — | batched `[{id, status, alive, tps, real_tps, sim_time, paused, last_autosave}]` |
| `GET`  | `/workspaces/{id}/worlds/{wid}/history` | — | revisions (lineage) |
| `GET`  | `/nodes/{id}/logs` | `?follow` | log lines |
| `POST` | `/upload` | multipart | server path for `add_world` |
| `GET`  | `/scripts` / `POST` `/scripts` | per-workspace notebooks | save/load/list |

`/run` is the universal write/control RPC; structured GETs exist only so the UI can render
lists + poll telemetry without scripting. Promote hot ops to endpoints later, never before.

---

## 8. Build vs stub

| Piece | Prototype approach |
|---|---|
| Data flow (import/query/mutate/commit/history/transfer) | **Real**, headless, against `testdata` fixtures (tiny `dasdasd.zip`) |
| Live node (`start_node`/`info`/`reload`) | Real Bibites binary **or** a stub speaking IPC `INFO`/`RELOAD` (test `reloadFakeNode`/`fakeSimCtl` are templates) |
| Auth | none for local prototype (see §10 fork) |
| Persistence | workspace root dir persists across restarts via `Open` |

---

## 9. Build order (each slice independently demoable)

1. **`/run` daemon + static page** — paste a script, see `Result`. Proves the RPC.
2. **`GET /workspaces` + sidebar + New** — multi-workspace switching.
3. **Worlds list + upload + add-world** — the import loop; save reaches the daemon fs.
4. **Notebook: cells, save/load, results table, history strip** — the data-half E2E.
5. **Stub sim + node list + batched `/info` 10s poll + stop/resume/reload + logs** — live half.
6. **Transfer** — cross-world graft, closing the full spine.

---

## 10. Open decisions / gaps

- **FORK — single-user-local vs multi-user-served.** A chat-style workspace sidebar implies
  per-user. `/run` executes arbitrary Starlark that reads files + execs binaries
  (`add_world(path=)`, `start_node(path=)`). Local single-user → fine. Served → needs auth
  + sandboxed path/exec bindings + per-user roots. **Decide before building beyond slice 1.**
- **Autosave→ingest loop:** auto-ingest on a cadence vs manual button (the live-data
  heartbeat — surfaced as a per-node "auto-ingest ☑" + "last autosave" line).
- **Async run lifecycle:** runs are non-blocking with per-cell state + captured
  output/diagnostics + cancellation (imports/transfers take seconds; `start_node` spawns).
- **Liveness + reconnection:** post-restart, persisted node rows have no live handle → show
  DETACHED, offer reconnect/restart; crash via `Wait()`.
- **Telemetry:** one batched `/info`, poll only the open workspace, only when tab visible,
  cadence default 10s (configurable).
- **Script/notebook persistence model:** per-workspace store; is a notebook == a saved
  script; naming, versioning.

---

## 11. Issue mapping

- **#11 (UI plan/design):** this document.
- **#16 (Starlark editor: UI):** the notebook in §5/§6 is the editor; `/run` is its backend.
- **#15 (process-control bindings):** done; the node panel consumes
  `node.{info,state,stop,resume,reload,ingest_autosave,kill}`.
