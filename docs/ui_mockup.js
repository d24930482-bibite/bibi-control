/* ============================================================================
   bibicontrol — UI MOCKUP interactions (loaded by ui_mockup.html)
   ----------------------------------------------------------------------------
   All interactions here are UI-state only against HARDCODED MOCK DATA.
   Nothing talks to a backend. No network calls. See docs/ui_prototype_plan.md.

   Live (clickable): workspace select + right-click menu (rename/duplicate/
   delete) · resizable column divider · telemetry cadence + countdown + fake
   poll · node↔world cross-highlight · notebook flow/save/+cell · per-cell
   fake-run · text/markdown cells · cell menu (delete) · modals · logs drawer
   (slide-up bottom panel, resizable).
   ========================================================================== */

/* ---------- workspace selection ---------- */
function selectWs(el) {
  document.querySelectorAll('.ws-item').forEach(n => n.classList.remove('active'));
  el.classList.add('active');
  document.getElementById('wbName').textContent = el.dataset.ws;
}

/* ---------- node <-> world association ----------
   Each node card carries data-world (its persisted world_id binding); the
   worlds list shows a live/stale node indicator. Clicking either side
   cross-highlights the world row and every node bound to it, then clears. */
let focusTimer = null;
function focusWorld(world) {
  clearTimeout(focusTimer);
  document.querySelectorAll('.world.highlight, .node.bound-flash')
    .forEach(el => el.classList.remove('highlight', 'bound-flash'));

  const row = document.getElementById('world-' + world);
  if (row) {
    row.classList.add('highlight');
    row.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
  }
  document.querySelectorAll('.node[data-world="' + world + '"]')
    .forEach(n => n.classList.add('bound-flash'));

  focusTimer = setTimeout(() => {
    document.querySelectorAll('.world.highlight, .node.bound-flash')
      .forEach(el => el.classList.remove('highlight', 'bound-flash'));
  }, 1600);
}

/* ---------- workspace right-click menu (rename / duplicate / delete) ----------
   All mock-only: rename edits the label in place, delete removes the row. */
let ctxTarget = null;   // the .ws-item the menu was opened on

function openWsCtx(ev, item) {
  ev.preventDefault();
  ctxTarget = item;
  const menu = document.getElementById('wsCtx');
  menu.classList.add('show');
  // clamp to viewport so it never spills off-screen
  const mw = menu.offsetWidth, mh = menu.offsetHeight;
  const x = Math.min(ev.clientX, window.innerWidth - mw - 8);
  const y = Math.min(ev.clientY, window.innerHeight - mh - 8);
  menu.style.left = x + 'px';
  menu.style.top = y + 'px';
}
function closeWsCtx() {
  document.getElementById('wsCtx').classList.remove('show');
  ctxTarget = null;
}

function ctxRename() {
  const item = ctxTarget;
  closeWsCtx();
  if (!item) return;
  const nameEl = item.querySelector('.ws-name');
  const old = nameEl.textContent;
  const input = document.createElement('input');
  input.className = 'ws-name-edit';
  input.value = old;
  nameEl.replaceWith(input);
  input.focus();
  input.select();

  const commit = () => {
    const next = input.value.trim() || old;
    const span = document.createElement('span');
    span.className = 'ws-name';
    span.textContent = next;
    input.replaceWith(span);
    if (next !== old) {
      item.dataset.ws = next;
      if (item.classList.contains('active'))
        document.getElementById('wbName').textContent = next;
      toast('renamed to "' + next + '" (mock)');
    }
  };
  input.addEventListener('blur', commit, { once: true });
  input.addEventListener('keydown', e => {
    if (e.key === 'Enter') input.blur();
    else if (e.key === 'Escape') { input.value = old; input.blur(); }
  });
}

function ctxDuplicate() {
  const item = ctxTarget;
  closeWsCtx();
  if (!item) return;
  const name = item.dataset.ws + '-copy';
  const clone = document.createElement('div');
  clone.className = 'ws-item';
  clone.dataset.ws = name;
  clone.setAttribute('onclick', 'selectWs(this)');
  clone.setAttribute('oncontextmenu', 'openWsCtx(event, this)');
  clone.innerHTML = '<span class="ws-caret">▸</span><span class="ws-name">' + name + '</span>';
  item.after(clone);
  toast('duplicated → "' + name + '" (mock)');
}

function ctxDelete() {
  const item = ctxTarget;
  closeWsCtx();
  if (!item) return;
  const name = item.dataset.ws;
  if (!confirm('Delete workspace "' + name + '"? This is mock-only.')) return;
  const wasActive = item.classList.contains('active');
  item.remove();
  if (wasActive) {
    const first = document.querySelector('.ws-item');
    if (first) selectWs(first);
    else document.getElementById('wbName').textContent = '—';
  }
  toast('deleted "' + name + '" (mock)');
}

// dismiss any open popup on an outside click or scroll
document.addEventListener('click', e => {
  if (!e.target.closest('#wsCtx')) closeWsCtx();
  if (!e.target.closest('#cellMenu')) closeCellMenu();
});
window.addEventListener('blur', () => { closeWsCtx(); closeCellMenu(); });
document.querySelector('.col-a').addEventListener('scroll', closeWsCtx, true);

/* ---------- notebook cell context menu (right-click) ----------
   All mock-only. Right-clicking anywhere on a cell opens the menu at the
   cursor; cellMenuTarget is the .cell it was opened on. */
let cellMenuTarget = null;

function openCellMenu(ev) {
  const cell = ev.target.closest('.cell');
  if (!cell) return;                 // not on a cell — let the native menu show
  // text-cell markdown editor keeps its native menu (paste-heavy); the code
  // overlay does NOT, so right-click works consistently across a code cell.
  if (ev.target.closest('.md-edit')) return;
  ev.preventDefault();
  ev.stopPropagation();
  closeCellMenu();
  cellMenuTarget = cell;
  cell.classList.add('menu-open');
  const menu = document.getElementById('cellMenu');
  menu.classList.add('show');
  // anchor at the cursor, clamped to the viewport
  const mw = menu.offsetWidth, mh = menu.offsetHeight;
  let x = Math.min(ev.clientX, window.innerWidth - mw - 8);
  let y = ev.clientY;
  if (y + mh > window.innerHeight - 8) y = window.innerHeight - mh - 8;
  menu.style.left = x + 'px';
  menu.style.top = y + 'px';
}
function closeCellMenu() {
  document.getElementById('cellMenu').classList.remove('show');
  document.querySelectorAll('.cell.menu-open').forEach(c => c.classList.remove('menu-open'));
  cellMenuTarget = null;
}
// open the cell menu on right-click anywhere in the notebook
document.getElementById('notebook').addEventListener('contextmenu', openCellMenu);

function cellMenuRun() {
  const cell = cellMenuTarget;
  closeCellMenu();
  if (!cell) return;
  const runBtn = cell.querySelector('.cell-head .btn:not(.btn-ghost)');
  // text cells have no run; code cells carry runCell(n) in the run button
  if (runBtn && /runCell/.test(runBtn.getAttribute('onclick') || '')) runBtn.click();
  else toast('text cell — nothing to run (mock)');
}

function cellMenuInsert(position, type) {
  const cell = cellMenuTarget;
  closeCellMenu();
  if (!cell) return;
  insertCell(cell, position, type);
}

function cellMenuMove(dir) {
  const cell = cellMenuTarget;
  closeCellMenu();
  if (!cell) return;
  // find the adjacent .cell (inserters may sit between cells)
  const cells = [...document.querySelectorAll('#notebook > .cell')];
  const i = cells.indexOf(cell);
  if (dir < 0 && i > 0) cells[i - 1].before(cell);
  else if (dir > 0 && i < cells.length - 1) cells[i + 1].after(cell);
  refreshInserters();
  cell.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
}

function cellMenuDuplicate() {
  const cell = cellMenuTarget;
  closeCellMenu();
  if (!cell) return;
  const clone = cell.cloneNode(true);
  clone.removeAttribute('id');   // refreshInserters assigns a fresh anchor id
  clone.classList.remove('menu-open');
  // unwrap the code editor so setupCodeEditor can rebuild it cleanly
  // (cloneNode copies the DOM but not the textarea's live value/listeners)
  const wrap = clone.querySelector('.code-wrap');
  if (wrap) {
    const code = wrap.querySelector('.code');
    wrap.replaceWith(code);
    clone.removeAttribute('data-editable');
  }
  cell.after(clone);
  setupCodeEditor(clone);
  refreshInserters();
  toast('cell duplicated (mock)');
}

function cellMenuDelete() {
  const cell = cellMenuTarget;
  closeCellMenu();
  if (!cell) return;
  cell.remove();
  refreshInserters();
  toast('cell deleted (mock)');
}

/* ---------- resizable divider (live-state col B vs notebook col C) ----------
   Dragging sets --colb (column B width); col C is 1fr so it absorbs the rest.
   Width is clamped and the notebook stays at least ~420px wide. */
(function () {
  const app = document.querySelector('.app');
  const divider = document.getElementById('divider');
  const MIN_B = 200, MIN_C = 420, FIXED_A = 230, HANDLE = 6;
  let dragging = false;

  function clamp(px) {
    const maxB = window.innerWidth - FIXED_A - HANDLE - MIN_C;
    return Math.max(MIN_B, Math.min(px, maxB));
  }
  function setColB(px) { app.style.setProperty('--colb', clamp(px) + 'px'); }

  divider.addEventListener('mousedown', e => {
    dragging = true;
    divider.classList.add('dragging');
    document.body.classList.add('resizing');
    e.preventDefault();
  });
  window.addEventListener('mousemove', e => {
    if (!dragging) return;
    // col B starts right after col A; its width = pointerX - col A width
    setColB(e.clientX - FIXED_A);
  });
  window.addEventListener('mouseup', () => {
    if (!dragging) return;
    dragging = false;
    divider.classList.remove('dragging');
    document.body.classList.remove('resizing');
  });
  // double-click resets to the default width
  divider.addEventListener('dblclick', () => app.style.setProperty('--colb', '290px'));
})();

/* ---------- modals ---------- */
function openModal(id)  { document.getElementById(id).classList.add('show'); }
function closeModal(id) { document.getElementById(id).classList.remove('show'); }
function backdropClose(ev, id) { if (ev.target.id === id) closeModal(id); }
document.addEventListener('keydown', e => {
  if (e.key === 'Escape') {
    document.querySelectorAll('.backdrop.show').forEach(m => m.classList.remove('show'));
    closeLogs();
    closeWsCtx();
    closeCellMenu();
  }
});

/* Add World: toggle Upload vs Server-path inputs */
function toggleAddSrc() {
  const upload = document.getElementById('srcUpload').checked;
  document.getElementById('uploadPick').style.display = upload ? 'flex' : 'none';
  document.getElementById('serverPick').style.display = upload ? 'none' : 'flex';
}

/* ---------- logs drawer (one unified, node-prefixed stream) ----------
   Every node's lines live in one stream prefixed by node id. openLogs(node)
   opens the drawer; if a node is passed, it pre-filters the stream to it. */
function openLogs(node) {
  const sel = document.getElementById('logFilter');
  sel.value = node || '';
  filterLogs(sel.value);
  document.getElementById('logsDrawer').classList.add('show');
  document.querySelector('.col-c').classList.add('logs-open');
  // keep the tail in view
  const body = document.getElementById('logBody');
  body.scrollTop = body.scrollHeight;
}
function closeLogs() {
  document.getElementById('logsDrawer').classList.remove('show');
  document.querySelector('.col-c').classList.remove('logs-open');
}
/* node filter: empty string = show all; otherwise hide non-matching lines */
function filterLogs(node) {
  document.querySelectorAll('#logBody .log-line').forEach(line => {
    const match = !node || line.dataset.node === node;
    line.classList.toggle('filtered', !match);
  });
}

/* drag the drawer's left grip to resize its width (mirrors the column divider).
   --drawer-w lives on .col-c so both the drawer and the content push share it. */
(function () {
  const colC = document.querySelector('.col-c');
  const grip = document.getElementById('drawerGrip');
  const MIN_W = 240, MARGIN = 360;  // keep a sensible minimum notebook width
  let dragging = false;

  function setWidth(px) {
    const maxW = Math.max(MIN_W, colC.clientWidth - MARGIN);
    const w = Math.max(MIN_W, Math.min(px, maxW));
    colC.style.setProperty('--drawer-w', w + 'px');
  }
  grip.addEventListener('mousedown', e => {
    dragging = true;
    grip.classList.add('dragging');
    document.body.classList.add('resizing-v');
    e.preventDefault();
  });
  window.addEventListener('mousemove', e => {
    if (!dragging) return;
    // drawer is anchored to the right edge: width = distance from pointer to right edge
    const right = colC.getBoundingClientRect().right;
    setWidth(right - e.clientX);
  });
  window.addEventListener('mouseup', () => {
    if (!dragging) return;
    dragging = false;
    grip.classList.remove('dragging');
    document.body.classList.remove('resizing-v');
  });
  grip.addEventListener('dblclick', () => colC.style.setProperty('--drawer-w', '340px'));
})();

/* ---------- toast ---------- */
let toastTimer = null;
function toast(msg) {
  const t = document.getElementById('toast');
  t.textContent = msg;
  t.classList.add('show');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => t.classList.remove('show'), 1900);
}

/* ---------- telemetry countdown + fake poll ----------
   Default cadence 10s. On reaching 0 we "poll": bump the ALIVE node's
   TPS / real / sim_time / autosave to new mock values, then reset. */
let cadence = 10;        // seconds; or 0 for manual
let remaining = 7;       // start mid-countdown like the wireframe (↻ 0:07)
let simTime = 1240512;
let autosaveSecs = 42;

function fmtClock(s) {
  const m = Math.floor(s / 60);
  const ss = String(s % 60).padStart(2, '0');
  return m + ':' + ss;
}

function renderCountdown() {
  const cd = document.getElementById('countdown');
  if (cadence === 0) { cd.textContent = '↻ manual'; return; }
  cd.textContent = '↻ ' + fmtClock(remaining);
}

function fakePoll() {
  // new mock telemetry values for the ALIVE node
  const tps = 54 + Math.floor(Math.random() * 9);          // 54..62
  const real = (tps - 0.1 - Math.random() * 1.2).toFixed(1);
  simTime += tps * cadence * 1000;                          // advance sim time
  autosaveSecs = (autosaveSecs + cadence) % 600;

  document.getElementById('n1-tps').textContent  = tps;
  document.getElementById('n1-real').textContent = real;
  document.getElementById('n1-simt').textContent = simTime.toLocaleString('en-US');
  document.getElementById('n1-autosave').textContent = fmtClock(autosaveSecs);

  // brief flash on the countdown chip to signal a poll happened
  const cd = document.getElementById('countdown');
  cd.classList.add('flash');
  setTimeout(() => cd.classList.remove('flash'), 350);
}

function tick() {
  if (cadence === 0) return;          // manual: no countdown
  remaining -= 1;
  if (remaining <= 0) {
    fakePoll();
    remaining = cadence;
  }
  renderCountdown();
}

function changeCadence(val) {
  if (val === 'manual') {
    cadence = 0;
  } else {
    cadence = parseInt(val, 10);
    remaining = cadence;
  }
  renderCountdown();
}

setInterval(tick, 1000);
renderCountdown();

/* ---------- fake cell run ----------
   ▶ run -> show "running… ⠿" briefly -> flip to "✓ <time>".
   For query cell 2 the table/output is revealed on completion. */
const SPINNER = '<span class="spinner">⠿</span>';

function runCell(n) {
  const cell = document.getElementById('cell-' + n);
  const status = cell.querySelector('.cell-status');
  const meta = cell.querySelector('.cell-meta');

  // set running state
  status.className = 'cell-status running';
  status.innerHTML = '▶ running… ' + SPINNER;
  if (meta) meta.textContent = '';

  const dur = (0.1 + Math.random() * 0.6).toFixed(1);
  setTimeout(() => {
    status.className = 'cell-status ok';
    status.innerHTML = '✓ ' + dur + 's';

    // reveal per-cell outputs
    if (n === 2) {
      const out = document.getElementById('out-2');
      out.classList.remove('hidden');
      if (meta) meta.textContent = '· 7 rows';
    }
    if (n === 3) {
      document.getElementById('res-3').classList.remove('hidden');
      if (meta) meta.textContent = '· 7 rows';
    }
    if (n === 1 && meta) meta.textContent = '· rev1';
    // mutation cells: restore their commit chip (rev + staged_ops)
    if (n === 4 && meta) meta.textContent = '· rev2 · staged_ops: 1';
    if (n === 5 && meta) meta.textContent = '· rev3 · staged_ops: 18';
    if (n === 6 && meta) meta.textContent = '· rev4 · staged_ops: 3';
    // node-control cell: restore its node chip (process control, no rev)
    if (n === 7 && meta) meta.textContent = '· node-1';
  }, 850);
}

/* ---------- text / markdown cells ----------
   Two views per text cell: a <textarea> (raw) and a .md-render (rendered).
   Clicking the rendered prose (or "edit") switches to raw; blur re-renders.
   mdToHtml is a deliberately tiny, mock-only markdown subset. */
function escapeHtml(s) {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}
function mdInline(s) {
  // order matters: escape first, then apply inline spans
  s = escapeHtml(s);
  s = s.replace(/`([^`]+)`/g, '<code>$1</code>');
  s = s.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
  s = s.replace(/\*([^*]+)\*/g, '<em>$1</em>');
  s = s.replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a href="$2" onclick="return false">$1</a>');
  return s;
}
function mdToHtml(src) {
  const lines = src.replace(/\r/g, '').split('\n');
  let html = '', listOpen = false, para = [];
  const flushPara = () => { if (para.length) { html += '<p>' + mdInline(para.join(' ')) + '</p>'; para = []; } };
  const flushList = () => { if (listOpen) { html += '</ul>'; listOpen = false; } };
  for (const raw of lines) {
    const line = raw.trimEnd();
    const h = line.match(/^(#{1,3})\s+(.*)$/);
    const li = line.match(/^[-*]\s+(.*)$/);
    if (h) { flushPara(); flushList(); html += '<h' + h[1].length + '>' + mdInline(h[2]) + '</h' + h[1].length + '>'; }
    else if (li) { flushPara(); if (!listOpen) { html += '<ul>'; listOpen = true; } html += '<li>' + mdInline(li[1]) + '</li>'; }
    else if (line === '') { flushPara(); flushList(); }
    else { para.push(line); }
  }
  flushPara(); flushList();
  return html || '<span class="md-empty">Empty text cell — click to edit</span>';
}
function renderTextCell(id) {
  const cell = document.getElementById(id);
  const ta = cell.querySelector('.md-edit');
  cell.querySelector('.md-render').innerHTML = mdToHtml(ta.value);
  cell.classList.remove('editing');
}
function editTextCell(id) {
  const cell = document.getElementById(id);
  cell.classList.add('editing');
  const ta = cell.querySelector('.md-edit');
  ta.focus();
  ta.setSelectionRange(ta.value.length, ta.value.length);
}
// render any text cells present on load
document.querySelectorAll('.cell.text').forEach(c => renderTextCell(c.id));

/* ---------- always-editable code cells ----------
   A code cell is editable by default but still LOOKS like the highlighted view:
   we wrap the highlighted <pre class="code"> and overlay a transparent textarea
   on top. The colored highlight shows through; the caret/selection are the
   textarea's. Mock only — typed text isn't re-highlighted. */
function setupCodeEditor(cell) {
  if (cell.classList.contains('text')) return;          // text cells edit differently
  const code = cell.querySelector('.code');
  if (!code || cell.dataset.editable) return;           // need a code block; once only
  cell.dataset.editable = '1';

  // wrap the highlighted block
  const wrap = document.createElement('div');
  wrap.className = 'code-wrap';
  code.replaceWith(wrap);
  wrap.appendChild(code);

  // transparent textarea overlaid on top, seeded from the highlighted text
  const ta = document.createElement('textarea');
  ta.className = 'code-edit';
  ta.spellcheck = false;
  ta.value = code.textContent;
  wrap.appendChild(ta);
}
// make every code cell shipped in the HTML editable
document.querySelectorAll('#notebook > .cell').forEach(setupCodeEditor);

/* ---------- cells: build / append / insert (Jupyter-style) ----------
   Starts at 7 because the notebook ships with static cells [1]..[7]. New cells
   can be appended at the end OR inserted before/after any existing cell. */
let cellCount = 7;

// buildCell(type) -> a fresh idle cell element (not yet attached).
function buildCell(type) {
  const cell = document.createElement('div');
  cellCount += 1;

  if (type === 'text') {
    const id = 'cell-md' + cellCount;
    cell.className = 'cell text editing';
    cell.id = id;
    cell.innerHTML =
      '<div class="cell-head">' +
        '<span class="cell-idx">md</span>' +
        '<span class="cell-kind">text</span>' +
        '<span class="spacer"></span>' +
        '<button class="btn btn-sm" onclick="editTextCell(\'' + id + '\')">edit</button>' +
      '</div>' +
      '<textarea class="md-edit" spellcheck="false" placeholder="# Heading\n\nWrite **markdown** here…" ' +
        'onblur="renderTextCell(\'' + id + '\')"></textarea>' +
      '<div class="md-render" onclick="editTextCell(\'' + id + '\')"></div>';
    return cell;
  }

  // default: code cell
  const n = cellCount;
  cell.className = 'cell';
  cell.id = 'cell-' + n;
  cell.innerHTML =
    '<div class="cell-head">' +
      '<span class="cell-idx">[' + n + ']</span>' +
      '<span class="cell-status idle">idle</span>' +
      '<span class="cell-meta"></span>' +
      '<span class="spacer"></span>' +
      '<button class="btn btn-sm" onclick="runCell(' + n + ')">▶ run</button>' +
    '</div>' +
    '<pre class="code"># scratch cell — type Starlark here\n' +
    'print(workspace.query(sql="SELECT count(*) FROM bibites"))</pre>';
  setupCodeEditor(cell);        // overlay the editable textarea on the code view
  return cell;
}

// reveal a freshly placed cell: scroll to it + focus its editable area.
function focusNewCell(cell, type) {
  cell.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
  const ta = cell.querySelector(type === 'text' ? '.md-edit' : '.code-edit');
  if (ta) ta.focus();
}

// append at the end of the notebook (top-toolbar + Code / + Text).
function addCell(type) {
  const cell = buildCell(type);
  document.getElementById('notebook').appendChild(cell);
  refreshInserters();
  focusNewCell(cell, type);
}

// insert relative to an anchor cell: position is "before" or "after".
function insertCell(anchor, position, type) {
  if (!anchor) { addCell(type); return; }
  const cell = buildCell(type);
  if (position === 'before') anchor.before(cell);
  else anchor.after(cell);
  refreshInserters();
  focusNewCell(cell, type);
}

// insert relative to a between-cells "+" divider (data-after = id of cell above).
function insertAtDivider(divider, type) {
  const afterId = divider.dataset.after;
  if (afterId) insertCell(document.getElementById(afterId), 'after', type);
  else {  // the leading divider: prepend to the top of the notebook
    const cell = buildCell(type);
    document.getElementById('notebook').prepend(cell);
    focusNewCell(cell, type);
    refreshInserters();
  }
}

/* ---------- Jupyter-style between-cell inserters ----------
   Rebuilds the hover "+ Code / + Text" affordances: one before the first cell
   and one after every cell. data-after carries the id of the cell above ("" =
   the leading inserter), so insertAtDivider knows where to splice. */
function makeInserter(afterId) {
  const ins = document.createElement('div');
  ins.className = 'cell-insert';
  ins.dataset.after = afterId || '';
  ins.innerHTML =
    '<span class="ins-pill">' +
      '<span class="ins-btn" onclick="insertAtDivider(this.closest(\'.cell-insert\'),\'code\')">+ Code</span>' +
      '<span class="ins-btn" onclick="insertAtDivider(this.closest(\'.cell-insert\'),\'text\')">+ Text</span>' +
    '</span>';
  return ins;
}
function refreshInserters() {
  const nb = document.getElementById('notebook');
  nb.querySelectorAll('.cell-insert').forEach(el => el.remove());
  const cells = [...nb.querySelectorAll(':scope > .cell')];
  nb.prepend(makeInserter(''));                     // leading inserter (top)
  cells.forEach(cell => {
    if (!cell.id) cell.id = 'cell-x' + (++cellCount);  // ensure an anchor id
    cell.after(makeInserter(cell.id));              // inserter below each cell
  });
}

// build the initial set of between-cell inserters for the cells shipped in HTML
refreshInserters();
