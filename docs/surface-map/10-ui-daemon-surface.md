# 10. UI / notebook + daemon endpoints

**Scope (files scanned):** `cmd/bibid/main.go`, `api/daemon.go`, `api/handlers_workspaces.go`, `api/handlers_worlds.go`, `api/handlers_nodes.go`, `api/handlers_notebooks.go`, `api/handlers_upload.go`, `api/notebookstore.go`, `api/web/app.js`, `api/web/app.css`, `api/web/index.html`. (Cross-referenced for grounding only, NOT in slice: `api/web/api.js` — the fetch client app.js calls into; `workspace/*` — what each handler ultimately reads/mutates; `script/*` — the `Result` shape.)

## Location map

### `cmd/bibid/main.go` — the process / lifecycle
- `cmd/bibid/main.go:36` — [—] [WORKSPACE] — `api.New(root, owner)` + `d.Handler()`: constructs the Daemon (lazy per-id workspace cache) and mounts the route table. Binds loopback by default (`--addr 127.0.0.1:8080`, line 27); `--root` required (line 31); `--owner` defaults to the OS user (line 28, 71).
- `cmd/bibid/main.go:44` — [—] [WORKSPACE] — SIGINT/SIGTERM trap → `srv.Shutdown` + `d.Close()` (line 60-65). Close checkpoints every cached DuckDB so the WAL is truncated; skipping it leaves a dirty WAL the next open can OOM on.

### `api/daemon.go` — route table + shared infra
- `api/daemon.go:45` — [—] [WORKSPACE] — `d.ws(ctx, id)`: the "never open twice" gate. Whole check-open-store under one `d.mu`; every WORLD/WORKSPACE read endpoint funnels through here to get the single cached `*workspace.Workspace`.
- `api/daemon.go:80` — [—] [—] — `Handler()`: registers all 16 routes (lines 84-109), then mounts the embedded SPA (`//go:embed web`, line 20) at `/` (line 113). `/api/...` patterns are more specific so they win over the file server.
- `api/daemon.go:84` — [READ] [—] — `GET /api/health` → `handleHealth` (line 119): static `{"status":"ok"}`. Polled by the UI health indicator.
- `api/daemon.go:85` — [WRITE] [WORKSPACE] — `POST /api/workspaces/{id}/run` → `handleRun` (line 134): **the one script-execution seam.** Decodes `{program}`, opens the ws, calls `workspace.RunAutomation(...)`, returns the `script.Result` as JSON. Contract (lines 124-129): a program with diagnostics is HTTP 200 — only body-decode / ws-resolve failures are non-200 (404 via `revisionstore.IsNotFound`, else 500). Read-vs-write depends on the program; this single endpoint backs add_world, transfer, node start/stop/resume/reload/kill, AND every read query.

### `api/handlers_workspaces.go` — workspace collection CRUD
- `api/handlers_workspaces.go:23` — [READ] [WORKSPACE] — `GET /api/workspaces` → `handleListWorkspaces`: reads rows fresh from the registry (`workspace.ListWorkspaces`), not the in-memory cache (whose names go stale on rename, line 19-22). Returns `[]` not null.
- `api/handlers_workspaces.go:40` — [WRITE] [WORKSPACE] — `POST /api/workspaces` → `handleCreateWorkspace`: `workspace.Create(root, owner, name)`, caches the opened handle (line 60), 201. Empty name → 400 (line 48).
- `api/handlers_workspaces.go:69` — [WRITE] [WORKSPACE] — `PATCH /api/workspaces/{id}` → `handleRenameWorkspace`: renames the registry row only; cached handle's in-memory name left stale on purpose. 404 on not-found.
- `api/handlers_workspaces.go:103` — [WRITE] [WORKSPACE] — `DELETE /api/workspaces/{id}` → `handleDeleteWorkspace`: evicts + `Close()`s the cached handle (outside `d.mu`, line 113) BEFORE `os.RemoveAll`, so the DuckDB writer doesn't race the directory delete. Registry rows deleted before bytes. 204.

### `api/handlers_worlds.go` — worlds + revision history
- `api/handlers_worlds.go:34` — [READ] [WORKSPACE→WORLD] — `GET /api/workspaces/{id}/worlds` → `handleWorlds`: every world's id/name/head-revision/sim_time, plus a `live_node` indicator computed by intersecting `PersistedNodes` with the in-memory active set `ws.Nodes()` (never the persisted status column, line 51-71). Drives column-B world list + Start-Node/Transfer world `<select>`s.
- `api/handlers_worlds.go:93` — [READ] [WORLD] — `GET /api/workspaces/{id}/worlds/{wid}/history` → `handleWorldHistory`: ordered revision lineage for one world, marking `is_head`. 404 if `wid` absent (line 122). Drives the per-world history strip.

### `api/handlers_nodes.go` — live-world (sim process) control surface
- `api/handlers_nodes.go:55` — [READ] [WORLD] — `GET /api/workspaces/{id}/nodes/info` → `handleNodesInfo`: every persisted node row, each annotated with a **liveness verdict derived from the active set**, not `row.Status`: `detached` (no live handle, line 86), `crashed` (`ProcessExited`/`Failed` + exit code, line 90), or `alive` (line 94). When alive AND `rt.Connected()`, does a bounded 750ms `INFO` round-trip for telemetry (tps/real_tps/paused/sim_time/last_autosave, line 99-112); INFO failure keeps `alive` but omits telemetry (line 113-114). Drives column-B node cards + cadence poll.
- `api/handlers_nodes.go:130` — [READ] [WORLD] — `GET /api/workspaces/{id}/nodes/{nid}/logs` → `handleNodeLogs`: per-node ring-buffer snapshot (`ws.NodeLogs`). `?follow` is **accepted but a no-op** (line 143-144) — snapshot only, no streaming. 404 distinguishes "no buffer" from a node with zero lines (200 `{"lines":[]}`).
- `api/handlers_nodes.go:175` — [WRITE] [WORLD] — `DELETE /api/workspaces/{id}/nodes/{nid}` → `handleDeleteNode`: **row-only** registry deletion via `workspace.DeleteNode(root, nid)` — does NOT stop a live process (line 169-172), intended for detached/stale rows. 204; 404 on unknown nid. Note: takes `nid` straight to the registry; the `{id}` path segment is routing-only (line 187).

### `api/handlers_upload.go` — save-file upload (filesystem, no DuckDB)
- `api/handlers_upload.go:32` — [WRITE] [WORKSPACE] — `POST /api/workspaces/{id}/upload` → `handleUpload`: multipart `file` part streamed to `root/workspaces/{id}/uploads/{name}`, returns `{path}`. **Does NOT open the workspace** (line 29-31) — pure FS write, avoids a needless DuckDB writer. 256 MiB cap (line 15, 36). Traversal-hardened: parses the RAW Content-Disposition filename (line 50) before Go normalizes it, `sanitizeUploadName` rejects separators/NUL/`.`/`..` (line 127), plus a defense-in-depth dir-escape check (line 67). Layout string is reconstructed literally and will drift if `workspace.workspaceDir` changes (line 24-27).

### `api/handlers_notebooks.go` + `api/notebookstore.go` — notebook persistence (filesystem JSON)
- `api/handlers_notebooks.go:11` — [READ] [WORKSPACE] — `GET /api/workspaces/{id}/notebooks` → `handleListNotebooks` → `notebookList` (notebookstore.go:72): `{name,updated_at}` rows sorted by name from `root/workspaces/{id}/notebooks/*.json`; missing dir → `[]` (notebookstore.go:76).
- `api/handlers_notebooks.go:23` — [READ] [WORKSPACE] — `GET /api/workspaces/{id}/notebooks/{name}` → `handleGetNotebook` → `notebookGet` (notebookstore.go:105): one notebook `{name,cells,updated_at}`. 400 invalid name, 404 missing.
- `api/handlers_notebooks.go:41` — [WRITE] [WORKSPACE] — `PUT /api/workspaces/{id}/notebooks/{name}` → `handlePutNotebook` → `notebookPut` (notebookstore.go:130): atomic temp-file+rename upsert of `{cells:[...]}`. 400 on sanitize error (line 61), else 500. This is what autosave (app.js) writes.
- `api/handlers_notebooks.go:73` — [WRITE] [WORKSPACE] — `DELETE /api/workspaces/{id}/notebooks/{name}` → `handleDeleteNotebook` → `notebookDelete` (notebookstore.go:178): 204; 404 missing; 400 bad name.
- `api/notebookstore.go:46` — [—] [—] — `sanitizeNotebookName`: rejects empty / dotfile / separators / abs / `Base!=name` before any FS op. The notebook-name traversal guard.

### `api/web/index.html` — the three-column shell + modals
- `api/web/index.html:29` / `:44` / `:77` — [—] — column A (Workspaces), column B (Worlds + live nodes), column C (Notebook). Logs drawer slides over col C (line 133). `api.js` then `app.js` loaded last (line 297-298).
- `api/web/index.html:158` / `:174` / `:207` / `:237` — [—] — modals: New Workspace, Start Node, Add World, **Transfer Bibites/Eggs**.

### `api/web/app.js` — notebook UI, autocomplete graph, node/transfer controls
- `api/web/app.js:1444` — [WRITE] [—] — `runCell(n)`: reads the cell's textarea/`.code`, `POST`s `{program}` via `runProgram` (api.js → `/run`), then `renderResult`. Skips text cells; guards `selectedWsId`. The user's primary script-submit path.
- `api/web/app.js:1324` — [READ] [—] — `renderResult(cell, res)`: renders a `script.Result` (capitalized keys: `Output`, `Diagnostics[{Severity,Code,Message,Line,Column,...}]`, `StagedOps`, `RevisionRef`, `DryRun`). Diagnostics ≠ HTTP error — checks `res.Diagnostics` for the error chip (line 1321, 1333). `tryParseTable` (line 1303) auto-renders array-of-objects `Output` as a sortable table with client-side CSV export (line 1367); commit chip from structured `RevisionRef`+`StagedOps`, NOT regex-scraped (line 1429-1435).
- `api/web/app.js:1695` — [—] [—] — `AC_TYPE_MEMBERS`: the per-DSL-type member set (workspace/world/session/collection/node/settings) mirroring the Go bindings' `AttrNames`. The "after a dot" completion pool.
- `api/web/app.js:1703` — [—] [—] — `AC_RESULT`: maps each navigable member to its result type so chains (`workspace.world("x").open().bibites.` ) resolve step-by-step (`acTypeFromSteps`, line 1744). Unknown receiver → falls back to `AC_METHODS` union (line 1803) so completion degrades, never vanishes.
- `api/web/app.js:1671` / `:1679` — [—] [—] — `AC_METHODS` / `AC_TOP_LEVEL`: the union fallback pool + bare-identifier pool, built from the highlighter vocab (`SL_METHODS`/`SL_KEYWORDS`/`SL_BUILTINS`) plus hand-added workspace attrs and kwargs (`world=`,`path=`,`dst=`,`sql=`,`scale=`, line 1684).
- `api/web/app.js:1548-1565` — [—] [—] — `SL_KEYWORDS`/`SL_BUILTINS`/`SL_METHODS`: the highlighter vocab, a **second** hand-maintained mirror of the DSL surface independent of `AC_TYPE_MEMBERS`.
- `api/web/app.js:963` — [WRITE] [WORLD] — `submitStartNode()`: builds `workspace.start_node(world=,path=,connect=[,compat_addr=][,drop_path=])` Starlark from the modal and runs it via `/run`. Starts a live sim process.
- `api/web/app.js:998` — [WRITE] [WORLD] — `nodeAction(id, verb)`: builds `workspace.node("<id>").<verb>()` for stop/reload/kill, or `.resume(scale=1.0)` (line 1003), and runs via `/run`. Wired from the alive-node buttons (renderNodes, line 845-848).
- `api/web/app.js:1023` — [WRITE] [WORLD] — `dropNode(id)`: `confirm()` then `deleteNode` → `DELETE .../nodes/{nid}`. Wired ONLY from the detached branch (line 872) — row-only, never stops a process.
- `api/web/app.js:757` — [READ-render] [WORLD] — `renderNodes(rows)`: builds node cards by liveness; alive → stop/resume/reload/kill (line 845), crashed → "view logs" (line 860), detached → "reconnect" + "drop row" (line 867-872). Onclick set via `setAttribute` with single-quote-escaped ids (XSS/quote-safety note, line 836-840).
- `api/web/app.js:1068` — [WRITE] [WORLD→WORLD] — `transferToWorld()`: builds an **object-DSL** program (`s = workspace.world(src).open()` / `sel = s.<bibites|eggs>[.where(...)]` / `print(workspace.transfer(selector=sel, dst=...))`, line 1085-1091) — NEVER raw SQL. Parses `transferred` count from printed Output for the toast (line 1101), then refreshes dst worlds + history. No dedicated `/transfer` endpoint exists; transfer rides `/run`.
- `api/web/app.js:637` / `:661` — [READ] [WORLD] — `openLogs(nid)` / `_fetchLogs(nid)`: opens the logs drawer and polls `nodeLogs(wsId,nid,true)` (the no-op `?follow`) on a cadence floor of 2s (line 657). `renderLogs` (line 572) rebuilds the per-node filter.
- `api/web/app.js:916` — [READ] [WORLD] — `pollNodes()` + `tick()` (line 937): cadence-driven `nodesInfo` poll with single-in-flight + stale-workspace guards. "manual" cadence = no auto-poll (line 938, 948).
- `api/web/app.js:163` — [WRITE] [WORKSPACE] — `submitAddWorld`/`_runAddWorld` (line 199): on upload path `uploadSave` → `/upload` then `workspace.add_world(path=,name=)` via `/run` (line 200); on server-path path, `add_world` directly. The save→world ingest funnel.

## Read paths

**Script-driven reads (the real query surface).** Every read query a user writes — spanning collections, `.count()/.mean()/.group_by()`, `workspace.query(sql=...)`, `world.history_query(...)`, `node.info()/.state()` — is submitted through the **single** `POST /run` endpoint (daemon.go:85, app.js `runCell` 1444) and comes back inside `script.Result.Output`, rendered as a table or `<pre>` by `renderResult` (app.js:1324, table detection at 1303). There is no read-specific endpoint; the daemon does not know read from write — `RunAutomation` decides.

**Structural reads (UI chrome, fixed JSON shapes).** Workspaces list (handlers_workspaces.go:23), worlds + live-node map (handlers_worlds.go:34), world history (handlers_worlds.go:93), node info + telemetry (handlers_nodes.go:55), node logs snapshot (handlers_nodes.go:130), notebook list/get (handlers_notebooks.go:11/23), health (daemon.go:119). These are the columns/cards/drawers; all WORLD/WORKSPACE-scoped, all read-only.

**Liveness is always derived, never persisted.** Both `handleWorlds` (worlds.go:51-71) and `handleNodesInfo` (nodes.go:82-117) compute liveness from the in-memory active set + a live INFO probe, deliberately ignoring `row.Status`. The persisted status is surfaced only as an informational field. This is the one consistent read invariant across the node surface.

## Mutation paths

**The DSL funnel.** All data mutations (add_world, transfer, `collection.set/.delete`, `session.commit`, settings/zones edits) go through `POST /run` as object-DSL programs. The UI never hand-writes SQL: `transferToWorld` (app.js:1068) and `_runAddWorld` (app.js:199) assemble Starlark, not SQL — matching the "DSL not raw SQL" invariant. `staged_ops` / `RevisionRef` come back as structured `Result` fields and surface as the commit chip (renderResult, app.js:1429).

**Process-control mutations (live worlds).** Start (`submitStartNode`→`start_node`, app.js:963), stop/resume/reload/kill (`nodeAction`, app.js:998) also ride `/run` as `workspace.node(...).<verb>()` programs — they mutate process state, not save data. The ONLY non-`/run` mutation in the live-node surface is the **row-only** `DELETE .../nodes/{nid}` (nodes.go:175, app.js `dropNode` 1023), which never touches a running process.

**Resource-CRUD mutations (not data, not process).** Workspace create/rename/delete (handlers_workspaces.go), notebook put/delete (handlers_notebooks.go), and save upload (handlers_upload.go) are dedicated REST endpoints. Upload + notebooks are plain filesystem writes that bypass the DuckDB handle entirely (upload.go:29-31, notebookstore.go); workspace delete is the one path that must order handle-close before directory-removal (workspaces.go:96-118).

## Missing seams

### A. Logs `?follow` is a silent no-op — no live log streaming
**What's missing.** The logs endpoint advertises `?follow` and the client always passes it (`nodeLogs(wsId,nid,true)`, app.js:664), but the handler explicitly discards it and returns a single ring-buffer snapshot (`_ = r.URL.Query().Has("follow")`, handlers_nodes.go:143-144). "Streaming will be added when required" (line 128). The UI fakes liveness with a `setInterval` re-fetch floored at 2s (app.js:657).
**Consequence.** Each "follow" is a full snapshot re-render on a 2s+ poll, not a tail; lines between polls that age out of the bounded ring buffer are lost, and the drawer can drop output on a chatty node. The advertised contract (`?follow`) lies to any future client.
**Where it lives.** `api/handlers_nodes.go:143`; client at `api/web/app.js:657`, `:664`.

### B. Detached-node "reconnect" is a dead button
**What's missing.** A node whose process outlived the daemon (or vice-versa) renders as `detached` with a "reconnect" button whose only action is `toast('reconnect not yet implemented')` (app.js:868). There is no endpoint to re-attach a `compat_addr` session to a still-running sim; the only recovery offered is "drop row" (delete the registry row, app.js:872), which discards the node without stopping its process.
**Consequence.** A surviving sim process becomes unmanageable from the UI: you cannot resume INFO telemetry or issue stop/kill against it; the user must drop the row (orphaning the process) or kill the OS process by hand. Detached is a one-way street.
**Where it lives.** `api/web/app.js:868` (dead handler); no backing route in `api/daemon.go:97-100`.

### C. No `node.ingest_autosave` / `evict_history` / `world.load|unload` control in the UI
**What's missing.** `AC_TYPE_MEMBERS` and the highlighter advertise `ingest_autosave`, `evict_history`, `load`, `unload` as DSL members (app.js:1697, 1700, 1562), and node cards even surface `last_autosave` telemetry (nodes.go:111, renderNodes app.js:805-816), but there is no button or modal that calls any of them. A user who sees a fresh autosave on a live node has no one-click way to ingest it into history — they must hand-write the program in a cell.
**Consequence.** The most natural live-world workflow (snapshot a running sim's autosave into a committed revision) is discoverable in autocomplete but has no UI affordance; the live-node column is observe-only beyond stop/resume/reload/kill. Capability/affordance gap, not a correctness bug.
**Where it lives.** Members at `api/web/app.js:1697`, `:1700`; telemetry surfaced but unactioned at `api/web/app.js:805`; no wiring in `renderNodes` (`api/web/app.js:843-875`).

### D. Two hand-maintained DSL mirrors that can drift from each other and from the bindings
**What's missing.** The DSL member set is hand-typed in THREE places that must agree: `AC_TYPE_MEMBERS`/`AC_RESULT` (app.js:1695-1710), the highlighter vocab `SL_METHODS`/`SL_BUILTINS`/`SL_KEYWORDS` (app.js:1558-1565), and the kwarg/attr top-ups inside `AC_METHODS`/`AC_TOP_LEVEL` (app.js:1675, 1684). Nothing generates or cross-checks them against the Go bindings' `AttrNames()`. (missing.md §2 already proposes generating the *cheatlist* from `AC_TYPE_MEMBERS`; this is the upstream problem — `AC_TYPE_MEMBERS` itself is hand-maintained, and the highlighter is a *second* uncoordinated copy.)
**Consequence.** Adding/removing a binding member requires editing two unrelated JS tables plus the Go side; a miss means autocomplete offers a member the highlighter won't color (or vice-versa), and either can silently lag the actual bindings. This is the root that §2's generated cheatlist would inherit if not fixed first.
**Where it lives.** `api/web/app.js:1695` (`AC_TYPE_MEMBERS`/`AC_RESULT`), `api/web/app.js:1558` (`SL_METHODS` et al.), `api/web/app.js:1675`/`:1684` (manual top-ups). Overlaps missing.md §2.

### E. Transfer reports its result by regex-scraping printed Output
**What's missing.** `transfer` rides `/run` and has no structured return surfaced to the UI; `transferToWorld` recovers the transferred count by regex-matching the printed dict text (`res.Output.match(/transferred['":\s]+(\d+)/)`, app.js:1101), the very anti-pattern `renderResult` deliberately avoids for commit chips ("NOT by text-scraping", app.js:1322, 1430). On a format change to the printed repr the toast silently shows `transferred ?`.
**Consequence.** The transfer UI's success signal is brittle and decoupled from the structured `Result` fields (`StagedOps`/`RevisionRef`) that `/run` already returns; a printed-output format churn breaks the count display without any error. Overlaps the cross-save-transfer slice (03).
**Where it lives.** `api/web/app.js:1099-1103`. Overlaps slice 03 (cross-save transfer) and the automation/`/run` surface.
