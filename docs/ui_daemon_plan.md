# UI Daemon — Ticketed Implementation Plan (make the mock UI real)

> **Status:** ticketed 2026-06-18. Source for orchestrator tickets `U1…U14`.
> **Design refs:** `docs/ui_prototype_plan.md` (wireframes/flow), `docs/ui_mockup_HANDOFF.md`
> (the static mockup this makes real), `docs/ui_mockup.{html,css,js}` (the visual spec to port).

## Context

`docs/ui_mockup.{html,css,js}` is a complete, interactive **static** mockup of the bibicontrol
web UI — a 3-column SPA (workspaces · live state · notebook) with **hardcoded mock data** and no
backend. This plan makes every panel real: an HTTP daemon over the existing Go library plus a
data-driven port of the mockup, demoing the end-to-end spine:

```
add world (import) → query → mutate (object DSL) → commit (head++) → history
  → start node → telemetry/logs → control (stop/resume/reload) → transfer
```

**The hard part already exists.** `RunAutomation` + the entire Starlark binding surface *is* the
API; node control/telemetry, revisions, blobs, and the registry are production-ready. The real
work is the **serving layer + a data-driven frontend + three small new pieces** (notebook
persistence, upload, log capture) + a **stub sim**.

### Locked decisions
1. **Local single-user.** The daemon binds `127.0.0.1`, no auth, one trusted owner of the
   workspaces root. Served/multi-user (auth + sandboxed path/exec bindings + per-user roots) is a
   deliberately deferred fork (`ui_prototype_plan.md §10`).
2. **Data-driven vanilla JS frontend**, served via `go:embed`. No build step, no npm, no CDN —
   preserves the single-binary/offline property that was the mockup's whole point. The mockup is
   the visual spec; port it in place, replacing hardcoded DOM with render-from-data + a fetch layer.
3. **Standalone stub-sim binary** so the nodes panel is fully live in a demo without the real game.

### Reuse map (do NOT rebuild — verified against the code)
- **Universal RPC:** `RunAutomation(ctx, ws *Workspace, program []byte, opts script.Options)
  (script.Result, error)` — `workspace/automation.go:39`. `AutomationGlobals` builds the
  `workspace` root object.
- **Result shape:** `script.Result{Output string, Diagnostics []Diagnostic, StagedOps int,
  RevisionRef string, DryRun bool}` — `script/result.go:4`;
  `Diagnostic{Severity, Code, Message, Detail, Filename, Line, Column}` — `script/result.go:20`.
  `script.Options{Filename, ThreadName, MaxExecutionSteps, Load, StagedOps, RevisionRef, DryRun}`
  — `script/engine.go:17`.
- **Bindings (all writes/control go through `/run`):** `workspace.{worlds,world,add_world,nodes,
  node,start_node,query,transfer,poll}`, `world.{open,query,history_query,head,sim_time,load,
  unload}`, `s.{bibites,eggs,pellets,settings,zones}` + `.where().set()/.set_expr()/.delete()` +
  `s.commit()` (→ `{committed,revision_id,sha256}`), `node.{info,state,stop,resume,reload,
  ingest_autosave,kill,wait}` (`node.info()` → `{tps,real_tps,paused,sim_time,last_autosave}`).
  All in `workspace/automation.go`.
- **Lifecycle:** `workspace.Create(ctx, root, owner, name)` (`workspace.go:138`),
  `Open(ctx, root, id)` (`:194`), `Close()` (`:264`). One **non-reentrant** `sync.Mutex` per
  workspace serializes mutating ops — never nest it.
- **Registry (`revisionstore.Store`) — listers already present:** `ListWorkspaces` (store.go:819),
  `CreateWorkspace` (782), `GetWorkspace` (805), `ListWorlds` (893), `GetWorld` (878),
  `SetWorldHead` (927), `RevisionsForWorld` (463), `RevisionsForWorkspace` (506), `ListNodes`
  (1005), `GetNode` (990), `CreateNode` (963), `BindNode` (1038), `SetNodeStatus` (1064).
  **Absent (must add): `RenameWorkspace`, `DeleteWorkspace`, `DeleteNode`.**
- **Node/IPC/telemetry:** `workspace.StartNode` (`node.go:63`, takes `StartNodeSpec{…, Process
  ipc.ProcessSpec}`), `NodeInfo`/`NodeState`/`NodeStop`/`NodeResume` (`node_control.go`),
  `ReloadNode`/`IngestAutosave` (`node_reload.go`); `simctl` STOP/RESUME/INFO/RELOAD; `noderuntime`
  Start/Connect/Request/Stop; IPC envelopes in `ipc/`. Liveness = active set (`w.nodes[id]`) +
  `Runtime.Connected()` + INFO; stale persisted "running" row not in `w.nodes` = CRASHED/DETACHED.
- **Log writers wired to the bottom:** `ipc.ProcessSpec.Stdout/Stderr` (`ipc/process.go:31-39`,
  default `io.Discard`) → `noderuntime.Spec.Process` → `workspace.StartNodeSpec.Process`.
- **Fake-sim templates:** `simctl/simctl_test.go:27 newFakeSim` (envelope contract over a
  `Requester`), `workspace/node_control_test.go newFakeNode` (in-process `net.Pipe` runtime).
- **Fixture:** `testdata/saves/the-bibites/dasdasd.zip` (the file the mockup literally names).
- **Go 1.24:** `http.ServeMux` supports `GET /a/{id}/b` method+path patterns → **no router dep**.

## Architecture

```
browser (vanilla SPA, go:embed) ──HTTP/JSON──▶ api.Daemon (127.0.0.1)
  · GET lists for col A/B/history              · root dir owns N workspaces
  · POST …/run per cell                        · open map[id]*workspace.Workspace (lazy Open, CACHED)
  · polls GET …/nodes/info on cadence          · mutating /run serialized by each ws.mu (already)
                                               · holds live node IPC sessions + per-node log buffers
```

The daemon **caches** open workspaces — one `*workspace.Workspace` per id, lazily `Open`ed on first
use and reused (each `Open` allocates its own DuckDB/registry/blob handles, so never open an id
twice). A request handler resolves the workspace via the cache, then calls `RunAutomation` or a
registry lister. The browser drives the telemetry cadence (no server poll loop in the prototype);
logs `?follow` is simple polling.

**Query → table convention.** `script.Result.Output` is captured `print()` text. Query cells call
`print(workspace.query(sql=...))`; the frontend renders a **table** when `Output` parses as a JSON
array-of-objects, else preformatted text. (`workspace.query` returns `list[dict]`; its `print`
form is JSON-able.) Mutation cells show the rev/staged-ops chip parsed from the printed
`s.commit()` dict. A structured `Result.Data` field is a noted product upgrade — **out of scope**.

### HTTP surface (all under `/api`; U2 registers every route, others 501 until their ticket)
| Method | Path | Backed by |
|---|---|---|
| GET | `/api/health` | daemon liveness |
| GET | `/api/workspaces` | `ListWorkspaces` |
| POST | `/api/workspaces` | `workspace.Create` |
| PATCH | `/api/workspaces/{id}` | `RenameWorkspace` (U1) |
| DELETE | `/api/workspaces/{id}` | `DeleteWorkspace` (U1) + close handle + rm dir |
| POST | `/api/workspaces/{id}/run` | `RunAutomation` → `script.Result` |
| GET | `/api/workspaces/{id}/worlds` | `ListWorlds` + head rev + live-node indicator |
| GET | `/api/workspaces/{id}/worlds/{wid}/history` | `RevisionsForWorld` |
| GET | `/api/workspaces/{id}/nodes/info` | `ListNodes` + liveness + batched telemetry |
| GET | `/api/workspaces/{id}/nodes/{nid}/logs` | per-node ring buffer (U7) |
| POST | `/api/workspaces/{id}/upload` | multipart → server path for `add_world` |
| GET/PUT/DELETE | `/api/workspaces/{id}/notebooks[/{name}]` | notebook store (U6) |

### Conventions for all tickets
- **No new go.mod deps.** stdlib `net/http` only (Go 1.24 routing); JSON via the existing
  `goccy/go-json` if a package already imports it, else `encoding/json`.
- **Tests fast:** API handlers via `net/http/httptest`; workspace/node tests reuse the tiny fixture
  and `newFakeNode`. The executor gate runs `go test ./...` — keep new tests cheap.
- **Errors → JSON:** handlers return `{ "error": "..." }` with a sensible status; `/run` returns
  `script.Result` JSON even when the program has diagnostics (HTTP 200; diagnostics in the body).
- **Frontend offline rule:** no `https?://`, no CDN, no `@import`, no icon fonts in `api/web/`.

---

## Tickets

<a name="U1"></a>
### U1 — Registry mutators (rename/delete workspace, delete node)
**Deps:** none. **Files:** `revisionstore/store.go`, `revisionstore/store_test.go`.

Add three methods mirroring the existing `Create*/Set*` patterns and transaction style in the file:
- `RenameWorkspace(ctx, id, name string) error` — UPDATE `workspaces.name`; not-found → the store's
  standard not-found error (`IsNotFound`).
- `DeleteWorkspace(ctx, id string) error` — DELETE the workspace row. Respect existing FK
  constraints (`worlds`/`nodes` reference `workspaces` `ON DELETE RESTRICT`): document and enforce
  that callers delete/migrate dependent rows first, OR delete dependents in a transaction here —
  pick the approach consistent with the schema and note it. (The daemon's DELETE handler in U3 also
  closes the cached handle and removes the workspace dir.)
- `DeleteNode(ctx, id string) error` — DELETE the `nodes` row (for "drop row" on a DETACHED node).

**DoD:** new unit tests cover create→rename→get, create→delete→not-found, and node create→delete;
`go test ./revisionstore/...` green.

<a name="U2"></a>
### U2 — Daemon skeleton + `/run` + `/health` + embed shell
**Deps:** none. **Files:** `api/daemon.go`, `api/daemon_test.go`, `api/web/` (placeholder
`index.html`), `cmd/bibid/main.go`.

The foundation every endpoint ticket builds on.
- `api/daemon.go`: `type Daemon struct { root string; owner string; mu sync.Mutex; open
  map[string]*workspace.Workspace }`. `New(root, owner) *Daemon`. A `ws(ctx, id)
  (*workspace.Workspace, error)` helper that returns the cached handle or `workspace.Open`s and
  caches it (guarded by `mu`; never open twice). `Close()` closes all cached workspaces.
- `Handler() http.Handler`: an `http.ServeMux` registering **all** routes from the table above.
  Implement `GET /api/health` (→ `{"status":"ok"}`) and `POST /api/workspaces/{id}/run` (decode
  `{program string}`, resolve workspace, `RunAutomation(ctx, ws, []byte(program), script.Options{
  Filename:"notebook"})`, encode `script.Result` as JSON). **Every other API route is registered
  now but returns `501 Not Implemented`** (a shared `notImplemented` handler) so later tickets only
  add their own `handlers_*.go` body + swap one registration line.
- Serve the embedded SPA: `//go:embed web` + `http.FileServerFS` at `/` (API under `/api`).
- `cmd/bibid/main.go`: flags `--root` (required), `--addr` (default `127.0.0.1:8080`), `--owner`
  (default current user); build the daemon, `http.ListenAndServe`. Binds loopback only.

**DoD:** `httptest` tests for `/api/health` and `/api/workspaces/{id}/run` (create a temp-root
workspace in the test, run `print("hi")`, assert `Output` contains `hi`); `go build ./cmd/bibid`;
`go test ./api/...` green.

<a name="U3"></a>
### U3 — Workspace endpoints
**Deps:** U2, U1. **Files:** `api/handlers_workspaces.go`, test; wire registrations in `daemon.go`.

- `GET /api/workspaces` → `[{id,name,owner}]` from `registry.ListWorkspaces` (open the registry via
  a daemon helper rooted at `daemon.root`, or a lightweight `workspace`-package list helper).
- `POST /api/workspaces` `{name}` → `workspace.Create(ctx, root, owner, name)`; return `{id,name,
  owner}`; cache the handle.
- `PATCH /api/workspaces/{id}` `{name}` → `RenameWorkspace`.
- `DELETE /api/workspaces/{id}` → close+evict the cached handle, `DeleteWorkspace`, remove the
  workspace dir under root.

**DoD:** `httptest` round-trip create→list→rename→delete; `go test ./api/...` green.

<a name="U4"></a>
### U4 — Worlds + history endpoints
**Deps:** U2. **Files:** `api/handlers_worlds.go`, test; registrations.

- `GET /api/workspaces/{id}/worlds` → `[{id,name,head_revision,sim_time,live_node}]` from
  `registry.ListWorlds` (+ head rev id). `live_node` = id of the bound node currently in the
  workspace active set (or null) — reuse the liveness logic (active set membership), do not trust
  the DB status row.
- `GET /api/workspaces/{id}/worlds/{wid}/history` → ordered lineage from `RevisionsForWorld`
  (`[{id,parent_id,created_at,source_path,is_head}]`), oldest→newest, marking the head.

**DoD:** `httptest` test seeding a world via `AddWorld` on a temp root, asserting worlds list +
history lineage shape; `go test ./api/...` green.

<a name="U5"></a>
### U5 — Upload handler
**Deps:** U2. **Files:** `api/handlers_upload.go`, test; registration.

`POST /api/workspaces/{id}/upload` (multipart `file`) → stream to
`root/workspaces/{id}/uploads/{sanitized-filename}`, return `{path}` (an absolute server path the
client then passes to `workspace.add_world(path=…)` via `/run`). Reject path traversal in the
filename; cap size sensibly.

**DoD:** `httptest` multipart POST writes the file and returns its path; traversal attempt rejected;
`go test ./api/...` green.

<a name="U6"></a>
### U6 — Notebook store + endpoints
**Deps:** U2. **Files:** `api/notebookstore.go`, `api/handlers_notebooks.go`, tests; registrations.

Filesystem notebook store under `root/workspaces/{id}/notebooks/{name}.json`. A notebook =
`{name string, cells []Cell, updated_at string}` with `Cell{Type "code"|"text", Source string}` —
**no outputs persisted** (a saved notebook is a saved script; re-run to regenerate).
- `GET /api/workspaces/{id}/notebooks` → `[{name,updated_at}]`.
- `GET /api/workspaces/{id}/notebooks/{name}` → the notebook.
- `PUT /api/workspaces/{id}/notebooks/{name}` `{cells}` → upsert (set `updated_at`).
- `DELETE /api/workspaces/{id}/notebooks/{name}`.
Sanitize `name` (no traversal/separators).

**DoD:** store unit tests (put/get/list/delete round-trip) + one `httptest` endpoint test;
`go test ./api/...` green.

<a name="U7"></a>
### U7 — Node log capture
**Deps:** none. **Files:** `workspace/node.go` and/or `workspace/node_control.go` (+ a small
`workspace/node_logs.go`), test.

Make node stdout/stderr captured for **every** node (including binding-started ones, which build
the `ProcessSpec` internally), exposed for the logs endpoint:
- Add a per-node bounded ring buffer (`maxLines`, line-oriented, timestamped, level-tagged best
  effort) owned by the `Workspace`, keyed by node id.
- Inside `StartNode`, if `spec.Process.Stdout/Stderr` are nil, set them to the ring-buffer writer
  (tee if already set). Drop the buffer when the node row is removed.
- `Workspace.NodeLogs(nodeID string) ([]LogLine, error)` returning buffered lines; `LogLine{Time,
  Level, Text}`.

**DoD:** a test using `newFakeNode`/a process that writes to stdout asserts `NodeLogs` returns the
lines; `go test ./workspace/...` green.

<a name="U8"></a>
### U8 — Node info + logs endpoints
**Deps:** U2, U7. **Files:** `api/handlers_nodes.go`, test; registrations.

- `GET /api/workspaces/{id}/nodes/info` → batched `[{id,world_id,run_id,liveness,status,tps,
  real_tps,paused,sim_time,last_autosave,exit_code?}]` where `liveness ∈ {alive,crashed,detached}`
  derived from active-set membership + `Connected()` + a non-blocking INFO (alive), `Wait()`/exit
  seen (crashed), persisted row with no handle (detached). Telemetry only for alive nodes.
- `GET /api/workspaces/{id}/nodes/{nid}/logs` (`?follow` optional; prototype = return current
  buffer) → `{lines:[{time,level,text}]}` from `Workspace.NodeLogs`.

**DoD:** `httptest` test using a `newFakeNode`-backed workspace asserting an alive node reports
telemetry and logs; `go test ./api/...` green.

<a name="U9"></a>
### U9 — Stub sim binary
**Deps:** none. **Files:** `cmd/stubsim/main.go`, `cmd/stubsim/main_test.go`.

A standalone TCP server speaking the IPC envelope contract (generalize `newFakeSim` from `net.Pipe`
to a `net.Listener`): handle INFO (synthetic, advancing `tps`/`sim_time`, toggled `paused` by
STOP/RESUME), STOP, RESUME, RELOAD; periodically write plausible log lines to **stdout** (so U7
captures them when launched as a node). Flags: `--addr` (default `127.0.0.1:0` printing the chosen
port, or a fixed port), maybe `--tps`. Must be launchable by `workspace.start_node(path=…)` —
i.e., it binds the compat address the runtime connects to (follow how `noderuntime`/`livetest`
expect the address to be discovered; print/accept it accordingly).

**DoD:** a test starts the binary (or its server func) and exercises INFO/STOP/RESUME/RELOAD via
`simctl`; `go build ./cmd/stubsim`; `go test ./cmd/stubsim/...` green.

<a name="U10"></a>
### U10 — Frontend foundation + col A (workspaces)
**Deps:** U2, U3. **Files:** copy `docs/ui_mockup.{html,css,js}` → `api/web/{index.html,app.css,
app.js}`; add `api/web/api.js`.

Establish the served frontend and make column A real:
- `api.js`: a thin fetch layer (one function per endpoint; JSON in/out; surfaces `{error}`).
- Replace hardcoded col-A workspace items with **render-from-data** off `GET /api/workspaces`;
  selection drives the col-B banner; New modal → `POST /api/workspaces`; right-click rename →
  `PATCH`; delete → `DELETE` (re-select another). Daemon-up indicator polls `GET /api/health`.
- Keep all existing generic interactions (context menus, resizable divider, modals). Leave col B/C
  on mock data for now (later tickets), but ensure the page loads served from the daemon.
- Preserve the offline rule (no external refs).

**DoD:** with a running daemon, col A lists real workspaces and create/rename/delete work end to
end (manual/`/run` skill check noted in the ticket plan). No automated browser test required, but
keep `node --check api/web/*.js` clean and the no-external-refs grep empty. `go test ./...` green
(no Go behavior changed).

<a name="U11"></a>
### U11 — Frontend col B worlds + history + add-world
**Deps:** U10, U4, U5. **Files:** `api/web/app.js` (+ css as needed). *(api/web is a
serialization point — coordinate with U12/U13.)*

- Render the worlds list from `GET …/worlds` (name, head rev, live/stale node indicator).
- Render the history strip from `GET …/worlds/{wid}/history` for the focused world.
- Add World modal: upload branch → `POST …/upload` then `POST …/run` with
  `workspace.add_world(path=<server path>, name=<name>)`; server-path branch → `add_world` directly.
  Refresh worlds/history on success.

**DoD:** add-world via upload imports `dasdasd.zip` and the new world + its rev1 history appear;
offline grep empty; `go test ./...` green.

<a name="U12"></a>
### U12 — Frontend col C notebook (cells, run, results, save/load)
**Deps:** U10, U6. **Files:** `api/web/app.js` (+ css). *(serialization point)*

- Render cells from a notebook (`GET …/notebooks/{name}`); flow dropdown = notebook selector
  (`GET …/notebooks`); Save → `PUT`. Keep the existing cell UX (insert/move/duplicate/delete,
  editable code/markdown).
- `runCell` → `POST …/run` with the cell's source → render `Output`/`Diagnostics`; when `Output`
  parses as a JSON array-of-objects, render the **results table** (+ keep export-csv affordance);
  mutation cells show the rev/staged-ops chip parsed from the `s.commit()` dict.

**DoD:** against an imported world, a `print(workspace.query(...))` cell renders a table and a
`.where().set()`+`commit()` cell reports the new revision; Save/load round-trips a notebook;
offline grep empty; `go test ./...` green.

<a name="U13"></a>
### U13 — Frontend col B live nodes + logs + control
**Deps:** U10, U8, U9. **Files:** `api/web/app.js` (+ css). *(serialization point)*

- Render node cards from `GET …/nodes/info` with the three liveness states + telemetry; the
  cadence dropdown drives real polling (replace `fakePoll`).
- Start Node modal → `POST …/run` with `workspace.start_node(world=…, path=<stubsim or real>,
  connect=True)`; node actions (stop/reload/kill) → `/run` `workspace.node(id).stop()/reload()/
  kill()`. Refresh on success.
- Logs drawer streams from `GET …/nodes/{nid}/logs` (poll while open / `follow`); keep the unified
  node-prefixed view + filter.

**DoD:** with `cmd/stubsim` as the sim path, Start Node yields an ALIVE card with ticking
telemetry; stop/reload work; kill → CRASHED; logs drawer shows stub output; offline grep empty;
`go test ./...` green.

<a name="U14"></a>
### U14 — Transfer + polish + end-to-end
**Deps:** U11, U12, U13. **Files:** `api/web/app.js`, `revisionstore`/`api` as needed for drop-row,
`docs/ui_daemon_plan.md` (E2E notes).

- Wire transfer via `/run` (`workspace.transfer(selector=…, dst=…)`) from a notebook flow; refresh
  affected worlds/history.
- Node↔world cross-highlight driven by real `world_id` data; CSV export from the rendered table;
  DETACHED "drop row" → `DELETE` node (U1 `DeleteNode`, via a small endpoint or `/run`).
- Final end-to-end pass per the verification section; capture any gaps as follow-ups.

**DoD:** the full spine demoable end to end on the data half against `dasdasd.zip` and the live half
against `cmd/stubsim`; offline grep empty; `go test ./...` green.

---

## End-to-end verification (after the DAG drains)
- `go build ./...`; targeted `go test ./api/... ./workspace/... ./revisionstore/... ./cmd/...`.
- `go run ./cmd/bibid --root /tmp/bibiroot` → open `http://127.0.0.1:8080`.
- **Data half:** New Workspace → Add World (upload `testdata/saves/the-bibites/dasdasd.zip`) → run
  `print(workspace.query(sql="SELECT species_id,count(*) n FROM bibites GROUP BY 1 ORDER BY n DESC"))`
  → table → run a `.where().set()`+`s.commit()` cell → history advances rev1→rev2.
- **Live half:** `go build -o /tmp/stubsim ./cmd/stubsim`; Start Node (sim path `/tmp/stubsim`) →
  ● ALIVE with ticking telemetry; stop/reload work; logs drawer shows output; kill → CRASHED.
- **Invariants:** `grep -rnE 'https?://|cdn|@import' api/web/*` empty; `go.mod` gains no
  HTTP/router/websocket deps.
