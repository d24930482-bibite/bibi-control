# UI Mockup — Handoff

**Status:** static, interactive UI mockup complete. NOT wired to any backend. No Go code touched.
**Authoritative design:** `docs/ui_prototype_plan.md` (the mockup renders its §4–§6 wireframes).

---

## 1. What this is

A clean, modern, ChatGPT/Claude-style mockup of the bibicontrol web UI: a 3-column SPA
(workspaces sidebar · live state · notebook). It is a **visual + interaction** mockup with
**hardcoded mock data** — every node/world/cell/log value is fake. Nothing talks to a daemon.

### Hard constraints (keep these — they were the point)
- **Opens by double-clicking `docs/ui_mockup.html`.** No build step, no npm, no server.
- **Fully offline. No external/CDN deps, no network calls, no icon fonts.** Vanilla JS only.
  Unicode glyphs (●○⦿⟳⏸✕↻⠿) stand in for icons.
- Verify with: `grep -rnE 'https?://|cdn|@import|fonts\.' docs/ui_mockup.*` → must be empty.

### Files (was one file; split into three on request)
```
docs/ui_mockup.html   ~514 lines   structure only; <link> css + <script src> js, no inline blocks
docs/ui_mockup.css    ~575 lines   all styles (dark theme, one accent color, CSS vars)
docs/ui_mockup.js     ~643 lines   all interactions; ~41 top-level functions
```
> Trade-off of the split: the three files must stay together in the same folder (relative
> paths). Still fully offline.

### Sanity checks before/after edits
```
node --check docs/ui_mockup.js                                  # JS parses
grep -rnE 'https?://|cdn|@import|fonts\.' docs/ui_mockup.*       # no external refs
awk '{o+=gsub(/{/,"{"); c+=gsub(/}/,"}")} END{print o, c}' docs/ui_mockup.css   # braces balance
```

---

## 2. Layout (maps to ui_prototype_plan §6.1)

```
┌── col A ───┬── col B ──────────┬─║─ col C ──────────────────────────┐
│ WORKSPACES │ live state        │ ║  notebook + results + history     │
│ chat-list  │  NODES (3 states) │ ║  top bar (telemetry/daemon)       │
│ [+ New]    │  WORLDS           │ ║  toolbar (flow/Save/+Code/+Text)   │
│ ...        │                   │ ║  cells (+ between-cell inserters)  │
│ settings   │                   │ ║  HISTORY strip                     │
└────────────┴───────────────────┴─║──────────────────────────────────┘
        col A fixed 230px    ║ = draggable divider (resizes B vs C)
```
- **Col A:** 4 mock workspaces (predator-study selected), `[+ New]`, settings at bottom.
- **Col B:** NODES list (3 telemetry states), `[+ start node]`, WORLDS list, `[+ add/upload]`.
- **Col C:** telemetry/daemon top bar, notebook toolbar, cells, history strip; logs drawer
  docks bottom-right.

---

## 3. Implemented features (and where they live in ui_mockup.js)

### Workspaces (col A)
- **Select** workspace → highlights, updates col-B banner. `selectWs`
- **Right-click a workspace** → context menu: Rename (inline edit), Duplicate, Delete (confirm,
  re-selects another). `openWsCtx` / `ctxRename` / `ctxDuplicate` / `ctxDelete`

### Nodes (col B) — §6.2 "alive honesty"
- Three states with distinct styling: **● ALIVE** (green), **○ CRASHED** (red), **⦿ DETACHED**
  (grey), each with the per-state metrics + action buttons from the wireframe.
- **Node ↔ world binding** rendered both ways (this mirrors the real `world_id` FK; see §5):
  each node card has a "→ world X" chip; each world row shows a live/stale node indicator
  (`● node-1` live vs `○ node-2` stale). Clicking either cross-highlights. `focusWorld`
- **Telemetry countdown + fake poll:** `↻ 0:07` ticks down; at 0 it bumps the ALIVE node's
  TPS/real/sim_time/autosave to new mock values and resets. Cadence dropdown 5/10/30s/manual.
  `tick` / `fakePoll` / `changeCadence`
- Node action buttons (stop/reload/kill/restart/reconnect/drop) and worlds are toast-only.

### Notebook (col C) — §5/§6.3
- Ships **8 cells**: a markdown cell + cells `[1]..[7]` demonstrating the end-to-end spine:
  1. `[md]` text/markdown cell (edit/render)
  2. `[1]` `add_world` (OK, shows world output)
  3. `[2]` query, **starts RUNNING** (spinner)
  4. `[3]` query with **results TABLE** + `export csv`
  5. `[4]` mutation via **object DSL** `.where().set()` (commits rev2)
  6. `[5]` mutation via **plain Starlark** row loop (commits rev3)
  7. `[6]` **settings** mutation `save.settings.{simulation,independent,material}` (rev4)
  8. `[7]` **node-control DSL** `start_node` / `n.info()/resume/reload` (no commit, `· node-1`)
- **Code cells are editable by default but look like the highlighted view.** A transparent
  textarea is overlaid on the syntax-highlighted `<pre>`; you click straight in and type.
  No view/edit toggle. `setupCodeEditor` (CSS `.code-wrap` / `.code-edit`).
  *Mock caveat:* typed text is NOT re-highlighted live (the highlight reflects seeded
  content). This is exactly the structure where CodeMirror/Prism would drop in for the real product.
- **Markdown text cells:** click rendered prose (or `edit`) to edit raw; blur re-renders.
  Tiny built-in markdown subset (headings, lists, bold/italic/code/links). `mdToHtml` /
  `renderTextCell` / `editTextCell`
- **Run a cell** (`▶ run`): fake transition running… ⠿ → ✓ <time>; reveals query table /
  outputs; restores per-cell commit chips. `runCell` (per-cell behavior is `if (n === …)` cases)
- **Jupyter-style cell insertion:** hover the gap between/around cells → `+ Code / + Text`
  pill inserts there; also "Insert above/below" in the cell menu. `refreshInserters` /
  `makeInserter` / `insertCell` / `insertAtDivider`. New code cells open focused for editing.
- **Cell menu is RIGHT-CLICK** (no `⋯` button): Run, Insert above/below, Move up/down,
  Duplicate, Delete. `openCellMenu` (contextmenu handler on `#notebook`) / `cellMenu*`
- Top toolbar `+ Code` / `+ Text` append at the end. `addCell`
- Sticky top bars: the telemetry bar and the Save/+Code/+Text toolbar stay pinned while cells
  scroll (CSS `position: sticky` on `.c-topbar` / `.nb-toolbar`).

### History strip — §6.1
- `rev3 ●── rev2 ○── rev1 ○(import)` with `[diff][open]` and a caption (toast-only).

### Modals — §6.4
- New Workspace, Start Node (connect-on-start checkbox), Add World (Upload vs Server-path
  radios toggle inputs). Open from buttons; close on Cancel / backdrop / Esc.
  `openModal` / `closeModal` / `backdropClose` / `toggleAddSrc`

### Logs drawer — §6.5
- **One unified, node-prefixed log stream** (consolidated from per-node views to reduce
  friction). Each line is prefixed + color-coded by node; a header dropdown filters by node;
  the crashed node's `[view logs]` opens the stream pre-filtered. Plus a global `≣ logs`
  button in the NODES header. Includes the FATAL/exit-139 line + follow checkbox + download.
- **Docks bottom-right, fixed to the viewport** (so it never scrolls off-screen), logs
  anchored to the bottom, height hugs content (no scrollbar unless it overflows). Narrow,
  width-resizable via a left-edge grip; pushes notebook content left so cells don't overlap.
  `openLogs` / `closeLogs` / `filterLogs`

### Resizable divider
- Drag the bar between col B and col C to resize; double-click resets. `--colb` CSS var.

---

## 4. What's live (clickable) vs visual-only

**Live UI-state interactions:** workspace select + right-click menu; telemetry cadence +
countdown + fake poll; node↔world cross-highlight; resizable column divider; notebook flow
dropdown / Save (toast) / +Code / +Text; per-cell run transition; cell right-click menu
(run/insert/move/duplicate/delete); between-cell inserters; editable code + markdown cells;
modals open/close; logs drawer open/close/filter/resize.

**Visual-only / toast stubs:** all node action buttons; worlds list; history diff/open;
export csv; download logs; settings; modal form inputs don't persist. Cell run is fake
(timeout, not a real `/run`). No data is real.

---

## 5. Useful repo context (for whoever wires this to a backend)

The mockup's cell examples and the node↔world rendering were made faithful to the actual
codebase, not invented:

- **`/run` is the universal RPC; the Starlark automation layer IS the API.** Every op is a
  binding returning `script.Result {Output, Diagnostics, StagedOps, RevisionRef, DryRun}`
  (JSON-serializable). The notebook = saved script (ui_prototype_plan §5, §7).
- **Node↔world binding lives on the node as a `world_id` FK** (`revisionstore` `nodes.world_id`;
  set by `workspace/node.go` `StartNode`→`CreateNode`). Invariant: **at most one *LIVE* node
  per world**, enforced against the in-memory active set (`activeNodeForWorldLocked`), NOT the
  DB `status`. Liveness reads the live handle: ALIVE = in active set; CRASHED = `Wait()` saw
  exit; DETACHED = persisted row, no handle. The UI's live/stale world indicators encode this.
- **Node DSL** (verified in `workspace/automation.go` + `workspace/automation_test.go`):
  `workspace.node(id)` / `start_node(...)` → `n.{info,state,stop,resume,reload,ingest_autosave,
  kill}`; `info()` returns `{tps, real_tps, paused, sim_time, last_autosave}`.
- **Settings DSL** (`script/thebibites/settings_value.go`): `s.settings.simulation["k"].set(v)`,
  `.independent[...]`, `.material("Name")[...]`.
- **Mutation surface:** object DSL `s.bibites.where(...).set(...)` for bulk; plain Starlark
  loops for custom per-row logic; `s.commit()` advances head. `workspace.query(sql=...)` is the
  read escape-hatch (returns list-of-dicts) — never raw SQL for writes.

---

## 6. Open design discussions (NOT built — captured for direction)

These came up while building; recorded so the thread isn't lost. None are implemented.

- **"Restart node when X" / conditional node control.** Conclusion: don't build a `when/do`
  rule DSL. Generalize to **scheduled Starlark** — one new `every("10s", program=...)`
  primitive; each tick is one `/run`; the existing telemetry poll is the clock. Engineering
  budget goes to lifecycle (overlap-skip, cooldown/backoff, alive-vs-crashed-vs-detached
  action choice, failure handling), not a language. Auto-ingest (§10) is the first concrete
  schedule; generalize from it. Surface schedules as first-class objects (a SCHEDULES panel
  in col B), not buried in cells.
- **Graphs (flagged as crucial).** Treat a query result as chartable by default (Table/Chart
  toggle + column auto-map) with a savable `chart.*` Starlark binding that emits a **spec**
  (Vega-Lite-style JSON) into `script.Result`; render in the browser. **Telemetry time series
  is the killer chart** and is its own small subsystem (TS store + streaming + a renderer like
  uPlot), distinct from ad-hoc query charts. For the *mockup* a dependency-free inline-SVG
  renderer is correct (no-deps rule); for the *product*, vendor a chart lib — don't hand-roll.
- **Starlark vs full Python.** Keep Starlark. Its sandbox-by-construction + frozen-value model
  + JSON-serializable results are load-bearing (34 Go files depend on it). Full CPython =
  arbitrary code execution + GIL + packaging pain; you'd rebuild the sandbox you already get
  free. "Want pandas/charts" is satisfied by DuckDB (already embedded) + browser charts. A
  sandboxed out-of-process Python *task* is at most a last-resort escape hatch, never the cell
  language.
- **Cloud / served.** This sharpens the ui_prototype_plan §10 fork. Split into a stateless
  control/data plane (autoscaled API; registry→managed Postgres; blobs→S3/GCS; per-workspace
  mutex→distributed lock) and a stateful **node plane** (each sim = a container on K8s/ECS with
  a node-agent owning local IPC; daemon addresses agents over the network; telemetry pushed to
  a TS store; autosave→ingest via object-store events). Long `/run`s + watchdogs → a durable
  job/workflow engine (e.g. Temporal), not in-process loops. Per-tenant isolation + auth become
  mandatory (the binding-level path/exec jail + blessed sim image). The middle ground —
  "cloud but still one in-process daemon holding child processes + a mutex" — is the trap.

---

## 7. Suggested next steps (mockup-side, if continuing)

1. **Charts**, since flagged crucial: Table/Chart toggle on cell `[3]` (inline-SVG bar chart of
   the species/n mock rows) + a live TPS sparkline on the ALIVE node card fed by the existing
   `fakePoll` tick. Dependency-free; add a comment noting the product would use Vega-Lite/uPlot.
2. **SCHEDULES panel** in col B (name · cadence · last-fire · status, with pause/edit/run-now)
   to make the scheduled-Starlark idea visible.
3. If the product gets a real frontend, this file set is the visual spec to port — keep the
   spec-based chart contract and the right-click/editable-cell interactions.

---

## 8. How to view

Double-click `docs/ui_mockup.html`, or `file://…/docs/ui_mockup.html` in any browser, or
`xdg-open docs/ui_mockup.html`. No server needed. Things to try: select a workspace; watch the
`↻` countdown poll; click into a code cell and type; right-click a cell; hover between cells for
the insert pill; click a node's world chip; open `≣ logs`; open the modals.
