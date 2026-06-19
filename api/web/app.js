/* ============================================================================
   bibicontrol — app interactions  (app.js)
   ----------------------------------------------------------------------------
   Depends on api.js (loaded first). Column A (Workspaces) is wired to the
   live backend via the helpers in api.js. Columns B and C retain the original
   mockup interactions (mock/static data) as scaffolding for U11/U12/U13.

   Global functions called from inline HTML attributes:
     selectWs, openWsCtx, ctxRename, ctxDuplicate, ctxDelete,
     focusWorld, openModal, closeModal, backdropClose, toggleAddSrc,
     openLogs, closeLogs, filterLogs, changeCadence,
     runCell, editTextCell, renderTextCell, addCell, insertAtDivider,
     toast
   ============================================================================ */

/* ---------- column A: workspace selection ---------- */

// The real workspace id of the currently selected workspace.
// U11/U12/U13 read this to scope their fetch calls.
let selectedWsId = null;

// The name of the currently loaded notebook (null if none loaded yet).
let currentNotebook = null;

function selectWs(el) {
  document.querySelectorAll('.ws-item').forEach(n => n.classList.remove('active'));
  el.classList.add('active');
  document.getElementById('wbName').textContent = el.dataset.ws;
  selectedWsId = el.dataset.id || null;
  if (selectedWsId) {
    loadWorlds(selectedWsId);
    loadNotebookList(selectedWsId);
    pollNodes(); // U13: immediate node refresh on workspace switch
  }
}

/* ---------- column A: render workspace list ----------
   Builds the #wsList children from an array of {id, name, owner} rows.
   data-ws = name (for banner text / existing selection code)
   data-id = real backend id (for rename/delete/U11 fetch scope) */
function renderWorkspaces(rows) {
  const list = document.getElementById('wsList');
  list.innerHTML = '';
  rows.forEach(function(ws) {
    const div = document.createElement('div');
    div.className = 'ws-item';
    div.dataset.ws = ws.name;
    div.dataset.id = ws.id;
    div.setAttribute('onclick', 'selectWs(this)');
    div.setAttribute('oncontextmenu', 'openWsCtx(event, this)');
    div.innerHTML = '<span class="ws-caret">&#9656;</span><span class="ws-name">' +
      escapeHtml(ws.name) + '</span>';
    list.appendChild(div);
  });
}

/* ---------- column B: worlds render + load ---------- */

/**
 * renderWorlds(rows) — rebuild the .worlds container from the API array.
 * Mirrors renderWorkspaces; world rows use id="world-<id>" and
 * onclick="focusWorld('<id>')" so the cross-highlight stays intact.
 * @param {Array<{id: string, name: string, head_revision: number|null, live_node: string|null}>} rows
 */
function renderWorlds(rows) {
  const container = document.querySelector('.worlds');
  container.innerHTML = '';
  (rows || []).forEach(function(w) {
    const div = document.createElement('div');
    div.className = 'world';
    div.id = 'world-' + w.id;
    div.setAttribute('onclick', 'focusWorld(' + JSON.stringify(w.id) + ')');

    const nodeCls = w.live_node ? 'w-node live' : 'w-node';
    const nodeText = w.live_node ? ('&#9679; ' + escapeHtml(w.live_node)) : '&#9675; &#8212;';
    const revText  = w.head_revision != null ? 'head rev' + w.head_revision : '&#8212;';

    div.innerHTML =
      '<span class="w-bullet">&#8226;</span>' +
      '<span class="w-name">' + escapeHtml(w.name) + '</span>' +
      '<span class="' + nodeCls + '" title="' + (w.live_node ? 'live node bound' : 'no live node') + '">' + nodeText + '</span>' +
      '<span class="w-rev">' + revText + '</span>';

    container.appendChild(div);
  });
}

/**
 * loadWorlds(wsId) — fetch worlds for the workspace and render col B.
 * On success, auto-focuses the first world and loads its history.
 * @param {string} wsId
 */
function loadWorlds(wsId) {
  listWorlds(wsId).then(function(rows) {
    rows = rows || [];
    renderWorlds(rows);
    if (rows.length > 0) {
      focusWorld(rows[0].id);
      loadHistory(wsId, rows[0].id, rows[0].name);
    }
  }).catch(function(err) { toast(err.message); });
}

/* ---------- column B: history render + load ---------- */

/**
 * renderHistory(wid, name, revs) — rebuild the .history block from the API array.
 * API order is oldest->newest; render newest->oldest (head on the left).
 * @param {string} wid
 * @param {string} name  World name for the .h-title.
 * @param {Array<{id: number, parent_id: number|null, is_head: boolean}>} revs
 */
function renderHistory(wid, name, revs) {
  const history = document.querySelector('.history');
  if (!history) return;

  const titleEl = history.querySelector('.h-title');
  if (titleEl) titleEl.innerHTML = 'History &#183; <b>' + escapeHtml(name) + '</b>';

  const wrap = history.querySelector('.rev-line-wrap');
  if (!wrap) return;

  // Rebuild the rev-line, preserving .h-actions
  const existingActions = wrap.querySelector('.h-actions');
  const line = document.createElement('div');
  line.className = 'rev-line';

  // newest->oldest
  const ordered = (revs || []).slice().reverse();
  ordered.forEach(function(rev, i) {
    const span = document.createElement('span');
    span.className = rev.is_head ? 'rev head' : 'rev';

    const dotHtml = '<span class="rev-dot">' + (rev.is_head ? '&#9679;' : '&#9675;') + '</span>';
    const tagHtml = !rev.parent_id ? ' <span class="rev-tag">(import)</span>' : '';
    span.innerHTML = dotHtml + 'rev' + rev.id + tagHtml;

    line.appendChild(span);

    if (i < ordered.length - 1) {
      const link = document.createElement('span');
      link.className = 'rev-link';
      link.textContent = '──';
      line.appendChild(link);
    }
  });

  wrap.innerHTML = '';
  wrap.appendChild(line);
  if (existingActions) wrap.appendChild(existingActions);
}

/**
 * loadHistory(wsId, wid, name) — fetch revision history for one world and render it.
 * @param {string} wsId
 * @param {string} wid
 * @param {string} name
 */
function loadHistory(wsId, wid, name) {
  worldHistory(wsId, wid).then(function(revs) {
    renderHistory(wid, name, revs);
  }).catch(function(err) { toast(err.message); });
}

/* ---------- column B: add world ---------- */

/**
 * submitAddWorld() — real handler for the Add World modal's Import button.
 * Reads radio state, uploads (or uses server path), calls workspace.add_world via /run,
 * and refreshes col B on success.
 */
function submitAddWorld() {
  if (!selectedWsId) { toast('select a workspace first'); return; }

  var name = (document.getElementById('aw-name').value || '').trim();
  if (!name) { toast('name is required'); return; }

  var useUpload = document.getElementById('srcUpload').checked;

  if (useUpload) {
    var fileInput = document.getElementById('aw-file');
    var file = fileInput && fileInput.files && fileInput.files[0];
    if (!file) { toast('choose a file to upload'); return; }

    uploadSave(selectedWsId, file).then(function(result) {
      return _runAddWorld(result.path, name);
    }).catch(function(err) { toast(err.message); });
  } else {
    var serverPath = (document.getElementById('aw-server-path').value || '').trim();
    if (!serverPath) { toast('server path is required'); return; }
    _runAddWorld(serverPath, name).catch(function(err) { toast(err.message); });
  }
}

/**
 * _runAddWorld(path, name) — internal: call workspace.add_world via /run, handle result.
 * Returns a promise so the caller can catch network errors.
 */
function _runAddWorld(path, name) {
  var program = 'workspace.add_world(path=' + JSON.stringify(path) + ', name=' + JSON.stringify(name) + ')';
  return runProgram(selectedWsId, program).then(function(res) {
    if (res.Diagnostics && res.Diagnostics.length) {
      toast(res.Diagnostics[0].Message);
      return; // leave modal open
    }
    closeModal('m-add-world');
    // clear inputs for next open
    document.getElementById('aw-name').value = '';
    document.getElementById('aw-server-path').value = '';
    var fi = document.getElementById('aw-file');
    if (fi) fi.value = '';
    // reset to upload radio
    document.getElementById('srcUpload').checked = true;
    toggleAddSrc();
    toast('added "' + name + '"');
    loadWorlds(selectedWsId);
  });
}

/* ---------- column A: load workspaces on boot ---------- */
document.addEventListener('DOMContentLoaded', function() {
  listWorkspaces().then(function(rows) {
    renderWorkspaces(rows || []);
    // select the first workspace by default if any exist
    const first = document.querySelector('.ws-item');
    if (first) selectWs(first);
    // pollNodes() is called inside selectWs above; if no workspace exists, poll anyway
    // to ensure #nodesList is initialized (renders empty).
    else pollNodes();
  }).catch(function(err) {
    toast('failed to load workspaces: ' + err.message);
  });

  // start daemon health poll
  pollHealth();
  setInterval(pollHealth, 10000);
});

/* ---------- node <-> world association ----------
   Each node card carries data-world (its persisted world_id binding); the
   worlds list shows a live/stale node indicator. Clicking either side
   cross-highlights the world row and every node bound to it, then clears. */
let focusTimer = null;
function focusWorld(world) {
  clearTimeout(focusTimer);
  document.querySelectorAll('.world.highlight, .node.bound-flash')
    .forEach(function(el) { el.classList.remove('highlight', 'bound-flash'); });

  const row = document.getElementById('world-' + world);
  if (row) {
    row.classList.add('highlight');
    row.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
    // Refresh history strip when a real workspace is active (U11).
    // loadHistory is defined above; selectedWsId is the col-A seam variable.
    if (selectedWsId) {
      var nameEl = row.querySelector('.w-name');
      var worldName = nameEl ? nameEl.textContent : String(world);
      loadHistory(selectedWsId, world, worldName);
    }
  }
  document.querySelectorAll('.node[data-world="' + world + '"]')
    .forEach(function(n) { n.classList.add('bound-flash'); });

  focusTimer = setTimeout(function() {
    document.querySelectorAll('.world.highlight, .node.bound-flash')
      .forEach(function(el) { el.classList.remove('highlight', 'bound-flash'); });
  }, 1600);
}

/* ---------- workspace right-click menu (rename / duplicate / delete) ---------- */
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

  const commit = function() {
    const next = input.value.trim() || old;
    const span = document.createElement('span');
    span.className = 'ws-name';
    span.textContent = next;
    input.replaceWith(span);
    if (next === old) return;

    // optimistic DOM update; revert on backend failure
    item.dataset.ws = next;
    if (item.classList.contains('active'))
      document.getElementById('wbName').textContent = next;

    renameWorkspace(item.dataset.id, next).then(function() {
      toast('renamed to "' + next + '"');
    }).catch(function(err) {
      // revert the name
      item.dataset.ws = old;
      item.querySelector('.ws-name').textContent = old;
      if (item.classList.contains('active'))
        document.getElementById('wbName').textContent = old;
      toast(err.message);
    });
  };
  input.addEventListener('blur', commit, { once: true });
  input.addEventListener('keydown', function(e) {
    if (e.key === 'Enter') input.blur();
    else if (e.key === 'Escape') { input.value = old; input.blur(); }
  });
}

function ctxDuplicate() {
  closeWsCtx();
  // No duplicate-workspace backend endpoint exists.
  toast('duplicate not supported yet');
}

function ctxDelete() {
  const item = ctxTarget;
  closeWsCtx();
  if (!item) return;
  const name = item.dataset.ws;
  const id = item.dataset.id;
  if (!confirm('Delete workspace "' + name + '"?')) return;

  // optimistic removal; revert on backend failure
  const wasActive = item.classList.contains('active');
  item.remove();
  if (wasActive) {
    const first = document.querySelector('.ws-item');
    if (first) selectWs(first);
    else document.getElementById('wbName').textContent = '—';
  }

  deleteWorkspace(id).then(function() {
    toast('deleted "' + name + '"');
  }).catch(function(err) {
    // revert: re-fetch the list and re-render
    toast(err.message);
    listWorkspaces().then(function(rows) { renderWorkspaces(rows || []); })
      .catch(function() {});
  });
}

// dismiss any open popup on an outside click or scroll
document.addEventListener('click', function(e) {
  if (!e.target.closest('#wsCtx')) closeWsCtx();
  if (!e.target.closest('#cellMenu')) closeCellMenu();
});
window.addEventListener('blur', function() { closeWsCtx(); closeCellMenu(); });
document.querySelector('.col-a').addEventListener('scroll', closeWsCtx, true);

/* ---------- column A: new workspace modal ---------- */
function submitNewWorkspace() {
  const input = document.getElementById('m-new-ws-name');
  const name = input.value.trim();
  if (!name) { toast('name is required'); return; }

  createWorkspace(name).then(function(ws) {
    closeModal('m-new-ws');
    input.value = '';
    // re-render full list then select the new workspace
    listWorkspaces().then(function(rows) {
      renderWorkspaces(rows || []);
      const newItem = document.querySelector('.ws-item[data-id="' + ws.id + '"]');
      if (newItem) selectWs(newItem);
    }).catch(function() {});
    toast('created "' + name + '"');
  }).catch(function(err) {
    toast(err.message);
    // leave the modal open so the user can correct the name
  });
}

/* ---------- daemon-up health indicator ---------- */
function pollHealth() {
  const indicator = document.querySelector('.daemon-up');
  const dot = document.getElementById('daemonDot');
  const text = document.getElementById('daemonText');
  apiHealth().then(function() {
    if (indicator) indicator.classList.remove('down');
    if (dot) dot.textContent = '●';
    if (text) text.textContent = 'daemon up';
  }).catch(function() {
    if (indicator) indicator.classList.add('down');
    if (dot) dot.textContent = '●';
    if (text) text.textContent = 'daemon down';
  });
}

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
  document.querySelectorAll('.cell.menu-open').forEach(function(c) { c.classList.remove('menu-open'); });
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
  else toast('text cell — nothing to run');
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

  divider.addEventListener('mousedown', function(e) {
    dragging = true;
    divider.classList.add('dragging');
    document.body.classList.add('resizing');
    e.preventDefault();
  });
  window.addEventListener('mousemove', function(e) {
    if (!dragging) return;
    // col B starts right after col A; its width = pointerX - col A width
    setColB(e.clientX - FIXED_A);
  });
  window.addEventListener('mouseup', function() {
    if (!dragging) return;
    dragging = false;
    divider.classList.remove('dragging');
    document.body.classList.remove('resizing');
  });
  // double-click resets to the default width
  divider.addEventListener('dblclick', function() { app.style.setProperty('--colb', '290px'); });
})();

/* ---------- modals ---------- */
function openModal(id)  { document.getElementById(id).classList.add('show'); }
function closeModal(id) { document.getElementById(id).classList.remove('show'); }
function backdropClose(ev, id) { if (ev.target.id === id) closeModal(id); }
document.addEventListener('keydown', function(e) {
  if (e.key === 'Escape') {
    document.querySelectorAll('.backdrop.show').forEach(function(m) { m.classList.remove('show'); });
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

/* ---------- logs drawer (live per-node polling) ----------
   openLogs(nid) opens the drawer and starts polling nodeLogs for that node.
   closeLogs() closes it and stops the poll interval.
   renderLogs(lines, nid) builds #logBody rows and rebuilds #logFilter options.
   filterLogs(node) shows/hides lines by data-node. */

var _logsInterval = null;
var _logsNid = null;       // currently open node id

function renderLogs(lines, nid) {
  var body = document.getElementById('logBody');
  body.innerHTML = '';

  if (!lines || !lines.length) {
    var empty = document.createElement('div');
    empty.className = 'log-line info';
    empty.dataset.node = nid || '';
    empty.textContent = '(no log lines captured yet)';
    body.appendChild(empty);
  } else {
    lines.forEach(function(ln) {
      var div = document.createElement('div');
      // map level to css: "error" -> "fatal", "info" -> "info"
      var cls = ln.level === 'error' ? 'fatal' : 'info';
      div.className = 'log-line ' + cls;
      div.dataset.node = nid || '';
      // format: timestamp  node  LEVEL  text
      var ts = '';
      if (ln.time) {
        try {
          var d = new Date(ln.time);
          ts = d.toLocaleTimeString('en-US', { hour12: false }) + ' ';
        } catch (e) { ts = ln.time + ' '; }
      }
      div.innerHTML =
        '<span class="ts">' + escapeHtml(ts.trim()) + '</span> ' +
        '<span class="node">' + escapeHtml(nid || '') + '</span> ' +
        '<span class="lvl">' + escapeHtml((ln.level || '').toUpperCase()) + '</span> ' +
        escapeHtml(ln.text || '');
      body.appendChild(div);
    });
  }

  // apply current filter
  filterLogs(document.getElementById('logFilter').value);

  // rebuild logFilter options from currently rendered node ids
  var sel = document.getElementById('logFilter');
  var current = sel.value;
  sel.innerHTML = '<option value="">all nodes</option>';
  // collect unique node ids from rendered nodes list + this nid
  var seen = {};
  document.querySelectorAll('#nodesList .node').forEach(function(n) {
    var nidVal = n.dataset.id;
    if (nidVal && !seen[nidVal]) {
      seen[nidVal] = true;
      var opt = document.createElement('option');
      opt.value = nidVal;
      opt.textContent = nidVal;
      sel.appendChild(opt);
    }
  });
  if (nid && !seen[nid]) {
    var opt = document.createElement('option');
    opt.value = nid;
    opt.textContent = nid;
    sel.appendChild(opt);
  }
  sel.value = current || nid || '';

  // scroll to tail
  body.scrollTop = body.scrollHeight;
}

function openLogs(nid) {
  _logsNid = nid || null;
  var sel = document.getElementById('logFilter');
  if (nid) sel.value = nid;
  document.getElementById('logsDrawer').classList.add('show');
  document.querySelector('.col-c').classList.add('logs-open');

  // stop any existing poll
  if (_logsInterval) { clearInterval(_logsInterval); _logsInterval = null; }

  if (!nid || !selectedWsId) {
    // no node selected or no workspace: render empty
    renderLogs([], nid);
    return;
  }

  // immediate fetch
  _fetchLogs(nid);

  // poll while drawer is open; floor to 2s, respect cadence
  var interval = Math.max(2, cadence || 10) * 1000;
  _logsInterval = setInterval(function() { _fetchLogs(_logsNid); }, interval);
}

function _fetchLogs(nid) {
  if (!nid || !selectedWsId) return;
  var wsId = selectedWsId;
  nodeLogs(wsId, nid, true).then(function(data) {
    if (wsId !== selectedWsId) return;   // stale
    renderLogs(data.lines || [], nid);
  }).catch(function(err) {
    if (err && err.status === 404) {
      // no buffer for this node — show placeholder without toast storm
      renderLogs([], nid);
    }
    // other errors: silently ignore (poll will retry)
  });
}

function closeLogs() {
  document.getElementById('logsDrawer').classList.remove('show');
  document.querySelector('.col-c').classList.remove('logs-open');
  if (_logsInterval) { clearInterval(_logsInterval); _logsInterval = null; }
  _logsNid = null;
}

/* node filter: empty string = show all; otherwise hide non-matching lines */
function filterLogs(node) {
  document.querySelectorAll('#logBody .log-line').forEach(function(line) {
    var match = !node || line.dataset.node === node;
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
  grip.addEventListener('mousedown', function(e) {
    dragging = true;
    grip.classList.add('dragging');
    document.body.classList.add('resizing-v');
    e.preventDefault();
  });
  window.addEventListener('mousemove', function(e) {
    if (!dragging) return;
    // drawer is anchored to the right edge: width = distance from pointer to right edge
    const right = colC.getBoundingClientRect().right;
    setWidth(right - e.clientX);
  });
  window.addEventListener('mouseup', function() {
    if (!dragging) return;
    dragging = false;
    grip.classList.remove('dragging');
    document.body.classList.remove('resizing-v');
  });
  grip.addEventListener('dblclick', function() { colC.style.setProperty('--drawer-w', '340px'); });
})();

/* ---------- toast ---------- */
let toastTimer = null;
function toast(msg) {
  const t = document.getElementById('toast');
  t.textContent = msg;
  t.classList.add('show');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(function() { t.classList.remove('show'); }, 1900);
}

/* ---------- telemetry countdown + node poll ----------
   Default cadence 10s. On reaching 0 we poll nodesInfo and rerender.
   Manual mode: no automatic countdown; poll on demand only. */
let cadence = 10;        // seconds; or 0 for manual
let remaining = 7;       // start mid-countdown like the wireframe (↻ 0:07)

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

/* ---------- column B: node render ----------
   Builds #nodesList children from the nodesInfo array. Mirrors renderWorkspaces.
   data-world = row.world_id (for focusWorld cross-highlight at app.js:focusWorld).
   data-id = row.id (for action dispatch). */
function renderNodes(rows) {
  var list = document.getElementById('nodesList');
  list.innerHTML = '';
  (rows || []).forEach(function(row) {
    var div = document.createElement('div');
    div.className = 'node ' + row.liveness;
    div.dataset.world = row.world_id;
    div.dataset.id = row.id;

    // state dot by liveness
    var dot = row.liveness === 'alive' ? '&#9679;' :
              row.liveness === 'crashed' ? '&#9675;' : '&#10687;';
    var stateLabel = row.liveness.toUpperCase();

    var topHtml =
      '<div class="node-top">' +
        '<span class="node-dot">' + dot + '</span>' +
        '<span class="node-name">' + escapeHtml(row.id) + '</span>' +
        '<span class="node-state">' + stateLabel + '</span>' +
      '</div>';

    var worldHtml =
      '<div class="node-world" onclick="focusWorld(' + JSON.stringify(row.world_id) + ')" title="bound world">' +
        '<span class="nw-ico">&#11041;</span> world ' +
        '<span class="nw-name">' + escapeHtml(row.world_id) + '</span>' +
      '</div>';

    // telemetry block (alive only, feature-detect each field)
    var metricsHtml = '';
    if (row.liveness === 'alive') {
      var parts = [];
      if (row.tps != null) {
        var tpsStr = escapeHtml(String(row.tps != null ? row.tps.toFixed(1) : ''));
        var realStr = row.real_tps != null ? escapeHtml(row.real_tps.toFixed(1)) : '—';
        parts.push('<span class="m-val">' + tpsStr + '</span> TPS · real <span class="m-val">' + realStr + '</span>');
      }
      if (row.sim_time != null) {
        parts.push('sim t = <span class="m-val">' + escapeHtml(row.sim_time.toLocaleString('en-US')) + '</span>');
      }
      if (row.paused != null) {
        var pausedStr = row.paused ? 'yes' : 'no';
        var autosaveStr = '';
        if (row.last_autosave) {
          var as = row.last_autosave;
          if (as.modified_unix) {
            // compute age from unix epoch seconds
            var ageSecs = Math.round(Date.now() / 1000 - as.modified_unix);
            autosaveStr = ' · last autosave <span class="m-val">' + fmtClock(Math.max(0, ageSecs)) + '</span> ago';
          } else if (as.time) {
            autosaveStr = ' · last autosave <span class="m-val">' + escapeHtml(as.time) + '</span>';
          } else if (as.name) {
            autosaveStr = ' · last autosave <span class="m-val">' + escapeHtml(as.name) + '</span>';
          }
        }
        parts.push('paused: ' + pausedStr + autosaveStr);
      }
      if (parts.length) {
        metricsHtml = '<div class="node-metrics">' + parts.join('<br>') + '</div>';
      }
    } else if (row.liveness === 'crashed') {
      var exitStr = row.exit_code != null ? ' exit code <span class="m-val">' + escapeHtml(String(row.exit_code)) + '</span>' : '';
      metricsHtml = '<div class="node-metrics dim">crashed' + exitStr + '</div>';
    } else {
      // detached
      metricsHtml = '<div class="node-metrics dim">persisted row, no live handle</div>';
    }

    // action buttons by liveness
    var id = row.id;
    var actionsHtml = '<div class="node-actions">';
    if (row.liveness === 'alive') {
      actionsHtml +=
        '<button class="btn btn-sm" onclick="nodeAction(' + JSON.stringify(id) + ',\'stop\')">&#9208; stop</button>' +
        '<button class="btn btn-sm" onclick="nodeAction(' + JSON.stringify(id) + ',\'resume\')">&#9654; resume</button>' +
        '<button class="btn btn-sm" onclick="nodeAction(' + JSON.stringify(id) + ',\'reload\')">&#10231; reload</button>' +
        '<button class="btn btn-sm" onclick="nodeAction(' + JSON.stringify(id) + ',\'kill\')">&#10005; kill</button>';
    } else if (row.liveness === 'crashed') {
      actionsHtml +=
        '<button class="btn btn-sm" onclick="openLogs(' + JSON.stringify(id) + ')">view logs</button>';
    } else {
      // detached
      actionsHtml +=
        '<button class="btn btn-sm" onclick="toast(\'reconnect not yet implemented\')">reconnect</button>' +
        '<button class="btn btn-sm" onclick="toast(\'drop row (U14)\')">drop row</button>';
    }
    actionsHtml += '</div>';

    div.innerHTML = topHtml + worldHtml + metricsHtml + actionsHtml;
    list.appendChild(div);
  });

  // Rebuild #sn-world options from the current world ids visible in worlds list,
  // so the Start Node modal world selector stays current.
  _rebuildSnWorldOptions();
}

/* Rebuild the Start Node modal's world <select> from the current .world rows. */
function _rebuildSnWorldOptions() {
  var sel = document.getElementById('sn-world');
  if (!sel) return;
  var current = sel.value;
  sel.innerHTML = '';
  document.querySelectorAll('.world').forEach(function(el) {
    // world elements use id="world-<uuid>" — extract the id from there
    var worldId = el.id.replace('world-', '');
    var nameEl = el.querySelector('.w-name');
    var name = nameEl ? nameEl.textContent : worldId;
    var opt = document.createElement('option');
    opt.value = worldId;   // must be the UUID, not the name
    opt.textContent = name;
    sel.appendChild(opt);
  });
  // restore selection if still present
  if (current) sel.value = current;
}

/* ---------- column B: node poll ----------
   Single in-flight guard + stale-workspace guard (capture wsId at call time).
   Called on boot (DOMContentLoaded), on workspace switch (selectWs), and on
   each cadence tick boundary. In manual mode, only called explicitly. */
var _nodesPollInFlight = false;

function pollNodes() {
  if (!selectedWsId) return;
  if (_nodesPollInFlight) return;
  var wsId = selectedWsId;   // capture for stale-check
  _nodesPollInFlight = true;
  nodesInfo(wsId).then(function(rows) {
    _nodesPollInFlight = false;
    // discard stale response if the workspace changed mid-flight
    if (wsId !== selectedWsId) return;
    renderNodes(rows || []);
    // flash the countdown chip to signal a poll happened
    var cd = document.getElementById('countdown');
    cd.classList.add('flash');
    setTimeout(function() { cd.classList.remove('flash'); }, 350);
  }).catch(function(err) {
    _nodesPollInFlight = false;
    // don't toast on every poll failure — only log to console
    console.warn('pollNodes:', err.message);
  });
}

function tick() {
  if (cadence === 0) return;          // manual: no countdown
  remaining -= 1;
  if (remaining <= 0) {
    pollNodes();
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

/* ---------- column B: Start Node modal ----------
   submitStartNode() reads modal fields, builds the Starlark program, runs it
   via /run, and refreshes on success. */
function submitStartNode() {
  if (!selectedWsId) { toast('select a workspace first'); return; }

  var worldSel = document.getElementById('sn-world');
  var world = worldSel ? (worldSel.value || '').trim() : '';
  if (!world) { toast('select a world'); return; }

  var path = (document.getElementById('sn-path').value || '').trim();
  if (!path) { toast('sim bin path is required'); return; }

  var drop = (document.getElementById('sn-drop').value || '').trim();
  var compat = (document.getElementById('sn-compat').value || '').trim();
  var connect = document.getElementById('connectChk').checked;

  // build Starlark: mandatory kwargs first, then optional ones
  var prog = 'workspace.start_node(world=' + JSON.stringify(world) +
             ', path=' + JSON.stringify(path) +
             ', connect=' + (connect ? 'True' : 'False');
  if (compat) prog += ', compat_addr=' + JSON.stringify(compat);
  if (drop && drop !== 'auto') prog += ', drop_path=' + JSON.stringify(drop);
  prog += ')';

  runProgram(selectedWsId, prog).then(function(res) {
    if (res.Diagnostics && res.Diagnostics.length) {
      toast(res.Diagnostics[0].Message || 'start_node failed');
      return;   // leave modal open
    }
    closeModal('m-start-node');
    toast('node started');
    pollNodes();
  }).catch(function(err) { toast(err.message); });
}

/* ---------- column B: node actions ----------
   nodeAction(id, verb) builds workspace.node("<id>").<verb>(...) and runs it. */
function nodeAction(id, verb) {
  if (!selectedWsId) { toast('select a workspace first'); return; }

  var call;
  if (verb === 'resume') {
    call = 'workspace.node(' + JSON.stringify(id) + ').resume(scale=1.0)';
  } else {
    call = 'workspace.node(' + JSON.stringify(id) + ').' + verb + '()';
  }

  runProgram(selectedWsId, call).then(function(res) {
    if (res.Diagnostics && res.Diagnostics.length) {
      toast(res.Diagnostics[0].Message || verb + ' failed');
      return;
    }
    toast(verb + ' ok');
    pollNodes();
  }).catch(function(err) { toast(err.message); });
}

/* ---------- notebook col C: notebook selector ----------
   The #flow <select> is populated from listNotebooks().
   Selecting a notebook loads it via loadNotebook(name).
   loadNotebookList() is called from selectWs() whenever a workspace is chosen. */

/**
 * loadNotebookList(wsId) — fetch the notebook list and rebuild the #flow <select>.
 * If there are notebooks, auto-loads the first one.
 * @param {string} wsId
 */
function loadNotebookList(wsId) {
  listNotebooks(wsId).then(function(rows) {
    rows = rows || [];
    var flow = document.getElementById('flow');
    flow.innerHTML = '';
    if (rows.length === 0) {
      var opt = document.createElement('option');
      opt.value = '';
      opt.textContent = '(no notebooks)';
      flow.appendChild(opt);
      currentNotebook = null;
      return;
    }
    rows.forEach(function(nb) {
      var opt = document.createElement('option');
      opt.value = nb.name;
      opt.textContent = nb.name;
      flow.appendChild(opt);
    });
    // auto-load the first notebook
    loadNotebook(rows[0].name);
  }).catch(function(err) { toast('notebooks: ' + err.message); });
}

/**
 * loadNotebook(name) — fetch and render a notebook by name.
 * Guards on selectedWsId being set.
 * @param {string} name
 */
function loadNotebook(name) {
  if (!selectedWsId) { toast('select a workspace first'); return; }
  getNotebook(selectedWsId, name).then(function(doc) {
    renderNotebook(doc);
    currentNotebook = name;
    // ensure the selector reflects the loaded notebook
    var flow = document.getElementById('flow');
    flow.value = name;
  }).catch(function(err) { toast('load notebook: ' + err.message); });
}

/**
 * saveNotebook() — serialize the current cells and PUT them to the backend.
 * Prompts for a name if no notebook is currently loaded.
 */
function saveNotebook() {
  if (!selectedWsId) { toast('select a workspace first'); return; }
  var name = currentNotebook;
  if (!name) {
    name = (prompt('Notebook name:') || '').trim();
    if (!name) return;
  }
  var cells = notebookCells();
  putNotebook(selectedWsId, name, cells).then(function() {
    currentNotebook = name;
    toast('notebook saved');
    // rebuild the dropdown without auto-loading (to preserve current cells)
    listNotebooks(selectedWsId).then(function(rows) {
      rows = rows || [];
      var flow = document.getElementById('flow');
      flow.innerHTML = '';
      rows.forEach(function(nb) {
        var opt = document.createElement('option');
        opt.value = nb.name;
        opt.textContent = nb.name;
        flow.appendChild(opt);
      });
      flow.value = name;
    }).catch(function() {});
  }).catch(function(err) { toast(err.message); });
}

/**
 * notebookCells() — serialize the current #notebook cells to [{type, source}].
 * source for code: .code-edit textarea value (fallback .code textContent).
 * source for text: .md-edit textarea value.
 * @returns {Array<{type: string, source: string}>}
 */
function notebookCells() {
  var cells = [];
  document.querySelectorAll('#notebook > .cell').forEach(function(cell) {
    var type = cell.classList.contains('text') ? 'text' : 'code';
    var source;
    if (type === 'text') {
      var ta = cell.querySelector('.md-edit');
      source = ta ? ta.value : '';
    } else {
      var codeEdit = cell.querySelector('.code-edit');
      if (codeEdit) {
        source = codeEdit.value;
      } else {
        var code = cell.querySelector('.code');
        source = code ? code.textContent : '';
      }
    }
    cells.push({ type: type, source: source });
  });
  return cells;
}

/**
 * renderNotebook(doc) — clear #notebook and rebuild cells from doc.cells.
 * Reuses buildCell(type) for each cell, sets source, then calls refreshInserters().
 * @param {{name: string, cells: Array<{type: string, source: string}>, updated_at: string}} doc
 */
function renderNotebook(doc) {
  var nb = document.getElementById('notebook');
  nb.innerHTML = '';
  // Reset cellCount so ids are predictable from the new set of cells.
  cellCount = 0;
  (doc.cells || []).forEach(function(c) {
    var type = c.type === 'text' ? 'text' : 'code';
    var cell = buildCell(type);
    if (type === 'text') {
      var ta = cell.querySelector('.md-edit');
      if (ta) ta.value = c.source || '';
      // render the markdown immediately (not just on blur)
      renderTextCell(cell.id);
      cell.classList.remove('editing');
    } else {
      // Set the highlighted .code textContent before setupCodeEditor runs so
      // the textarea is seeded correctly (setupCodeEditor seeds ta from code.textContent).
      var code = cell.querySelector('.code');
      if (code) code.textContent = c.source || '';
      // setupCodeEditor was already called inside buildCell, but it seeds from
      // the textContent at build time. Re-seed the textarea explicitly.
      var codeEditTa = cell.querySelector('.code-edit');
      if (codeEditTa) codeEditTa.value = c.source || '';
      // reset to idle state (no stale mock status)
      var status = cell.querySelector('.cell-status');
      if (status) { status.className = 'cell-status idle'; status.textContent = 'idle'; }
      var meta = cell.querySelector('.cell-meta');
      if (meta) meta.textContent = '';
      // clear any prior output
      cell.querySelectorAll('.cell-out, .result-wrap').forEach(function(el) { el.remove(); });
    }
    nb.appendChild(cell);
  });
  refreshInserters();
}

/* ---------- notebook col C: tryParseTable ----------
   Returns a non-empty array-of-plain-objects iff str is valid JSON of that
   shape; null otherwise. Used by renderResult to decide table vs text.
   Strict check: must be a non-empty array whose FIRST element is a plain object
   (not null, not an Array). Arrays of scalars, plain strings, empty arrays, and
   parse errors all return null. */
function tryParseTable(str) {
  if (!str || !str.trim()) return null;
  try {
    var parsed = JSON.parse(str.trim());
    if (!Array.isArray(parsed) || parsed.length === 0) return null;
    var first = parsed[0];
    if (typeof first !== 'object' || first === null || Array.isArray(first)) return null;
    return parsed;
  } catch (e) {
    return null;
  }
}

/* ---------- notebook col C: renderResult ----------
   Renders a script.Result (capitalized keys) into a cell's output area.
   res = { Output: string, Diagnostics: [{Severity, Code, Message, Detail,
           Filename, Line, Column}], StagedOps: number, RevisionRef: string,
           DryRun: boolean }
   NOTE: /run returns HTTP 200 even on program failure — check Diagnostics.
   Commit chip comes from res.RevisionRef + res.StagedOps (structured fields),
   NOT by text-scraping the printed s.commit() output. */
function renderResult(cell, res) {
  var status = cell.querySelector('.cell-status');
  var meta = cell.querySelector('.cell-meta');

  // clear previous output blocks
  cell.querySelectorAll('.cell-out, .result-wrap').forEach(function(el) { el.remove(); });
  if (meta) meta.textContent = '';

  // --- Diagnostics (errors / warnings) ---
  if (res.Diagnostics && res.Diagnostics.length) {
    status.className = 'cell-status error';
    status.innerHTML = '&#10005; error';
    var diagBlock = document.createElement('div');
    diagBlock.className = 'cell-out';
    res.Diagnostics.forEach(function(d) {
      var line = document.createElement('div');
      var loc = '';
      if (d.Line) loc += ' line ' + d.Line;
      if (d.Column) loc += ':' + d.Column;
      line.textContent = (d.Severity ? d.Severity + ': ' : '') + (d.Message || '') + loc;
      diagBlock.appendChild(line);
    });
    cell.appendChild(diagBlock);
  } else {
    status.className = 'cell-status ok';
    status.innerHTML = '&#10003; ok';
  }

  // --- Output: table or preformatted text ---
  var rows = tryParseTable(res.Output);
  if (rows) {
    var keys = Object.keys(rows[0]);
    var wrap = document.createElement('div');
    wrap.className = 'result-wrap';

    var cap = document.createElement('div');
    cap.className = 'result-cap';
    cap.appendChild(document.createTextNode('result'));

    var csvBtn = document.createElement('button');
    csvBtn.className = 'btn btn-sm';
    csvBtn.style.marginLeft = 'auto';
    // client-side CSV export over the rendered rows
    csvBtn.onclick = (function(capturedRows, capturedKeys) {
      return function() {
        var lines = [capturedKeys.join(',')];
        capturedRows.forEach(function(r) {
          lines.push(capturedKeys.map(function(k) {
            var v = r[k] == null ? '' : String(r[k]);
            // quote fields that contain comma, double-quote, or newline
            if (v.indexOf(',') >= 0 || v.indexOf('"') >= 0 || v.indexOf('\n') >= 0) {
              v = '"' + v.replace(/"/g, '""') + '"';
            }
            return v;
          }).join(','));
        });
        var blob = new Blob([lines.join('\n')], { type: 'text/csv' });
        var a = document.createElement('a');
        a.href = URL.createObjectURL(blob);
        a.download = (currentNotebook || 'result') + '.csv';
        a.click();
      };
    })(rows, keys);
    csvBtn.textContent = 'export csv';
    cap.appendChild(csvBtn);
    wrap.appendChild(cap);

    var table = document.createElement('table');
    table.className = 'result';
    var thead = document.createElement('thead');
    var headerRow = document.createElement('tr');
    keys.forEach(function(k) {
      var th = document.createElement('th');
      th.textContent = k;
      headerRow.appendChild(th);
    });
    thead.appendChild(headerRow);
    table.appendChild(thead);

    var tbody = document.createElement('tbody');
    rows.forEach(function(r) {
      var tr = document.createElement('tr');
      keys.forEach(function(k) {
        var td = document.createElement('td');
        var val = r[k];
        td.textContent = val == null ? '' : String(val);
        if (typeof val === 'number') td.className = 'num';
        tr.appendChild(td);
      });
      tbody.appendChild(tr);
    });
    table.appendChild(tbody);
    wrap.appendChild(table);
    cell.appendChild(wrap);

    if (meta) meta.textContent = '· ' + rows.length + ' rows';
  } else if (res.Output && res.Output.length) {
    var out = document.createElement('div');
    out.className = 'cell-out';
    var pre = document.createElement('pre');
    pre.textContent = res.Output;
    out.appendChild(pre);
    cell.appendChild(out);
  }

  // --- Commit chip: read from structured RevisionRef + StagedOps fields ---
  // NOT from regex-scraping the printed s.commit() dict text.
  if (res.RevisionRef && res.RevisionRef.length) {
    var existing = (meta && meta.textContent) ? meta.textContent : '';
    var chip = (existing ? existing + ' ' : '') + '· ' + res.RevisionRef + ' · staged_ops: ' + res.StagedOps;
    if (meta) meta.textContent = chip;
  }
}

/* ---------- notebook col C: runCell (real) ----------
   Accepts either the numeric cell-N id suffix or a cell element.
   Skips text cells. Posts {program} to /run, renders the script.Result.
   Guards selectedWsId. */
const SPINNER = '<span class="spinner">&#10047;</span>';

function runCell(n) {
  // resolve to the cell element (accept numeric id or element)
  var cell;
  if (typeof n === 'object' && n && n.classList) {
    cell = n;
  } else {
    cell = document.getElementById('cell-' + n);
  }
  if (!cell) return;
  // skip text cells — they have no Starlark program to run
  if (cell.classList.contains('text')) { toast('text cell — nothing to run'); return; }

  if (!selectedWsId) { toast('select a workspace first'); return; }

  // read the source: live textarea value, fallback to .code textContent
  var codeEdit = cell.querySelector('.code-edit');
  var source;
  if (codeEdit) {
    source = codeEdit.value;
  } else {
    var code = cell.querySelector('.code');
    source = code ? code.textContent : '';
  }

  // set running state
  var status = cell.querySelector('.cell-status');
  var meta = cell.querySelector('.cell-meta');
  status.className = 'cell-status running';
  status.innerHTML = '&#9654; running… ' + SPINNER;
  if (meta) meta.textContent = '';

  runProgram(selectedWsId, source).then(function(res) {
    renderResult(cell, res);
  }).catch(function(err) {
    // network / 4xx / 5xx (req() throws with an {error} message)
    var errStatus = cell.querySelector('.cell-status');
    var errMeta = cell.querySelector('.cell-meta');
    errStatus.className = 'cell-status error';
    errStatus.innerHTML = '&#10005; error';
    if (errMeta) errMeta.textContent = '';
    cell.querySelectorAll('.cell-out, .result-wrap').forEach(function(el) { el.remove(); });
    var out = document.createElement('div');
    out.className = 'cell-out';
    out.textContent = err.message;
    cell.appendChild(out);
  });
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
  const flushPara = function() { if (para.length) { html += '<p>' + mdInline(para.join(' ')) + '</p>'; para = []; } };
  const flushList = function() { if (listOpen) { html += '</ul>'; listOpen = false; } };
  for (let i = 0; i < lines.length; i++) {
    const raw = lines[i];
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
document.querySelectorAll('.cell.text').forEach(function(c) { renderTextCell(c.id); });

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
      '<button class="btn btn-sm" onclick="runCell(' + n + ')">&#9654; run</button>' +
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
  nb.querySelectorAll('.cell-insert').forEach(function(el) { el.remove(); });
  const cells = [...nb.querySelectorAll(':scope > .cell')];
  nb.prepend(makeInserter(''));                     // leading inserter (top)
  cells.forEach(function(cell) {
    if (!cell.id) cell.id = 'cell-x' + (++cellCount);  // ensure an anchor id
    cell.after(makeInserter(cell.id));              // inserter below each cell
  });
}

// build the initial set of between-cell inserters for the cells shipped in HTML
refreshInserters();
