/* ============================================================================
   bibicontrol — API fetch layer  (api.js)
   ----------------------------------------------------------------------------
   Shared fetch helpers for the SPA. Convention:
     - One async function per backend endpoint (named after the route action).
     - All requests go through the private `req(method, path, body)` helper.
     - req() throws an Error on non-2xx responses; the error message is taken
       from the JSON `{error: "..."}` shape that all handlers return via
       writeError() in api/daemon.go.
     - req() returns parsed JSON for responses with a body, or undefined for
       HTTP 204 No Content.

   Downstream tickets (U11 worlds/history, U12 notebook, U13 nodes/logs,
   U14+) extend this file by adding new async functions following the same
   pattern. Do NOT add column-A-only assumptions to req().

   Backend shapes:
     workspaceJSON  { id: string, name: string, owner: string }
     nodeInfoResponse { id, world_id, run_id, liveness, status,
                        tps?, real_tps?, paused?, sim_time?, last_autosave?,
                        exit_code? }  (api/handlers_nodes.go)
     logsResponse   { lines: [{time, level, text}] }  (handlers_nodes.go)
     script.Result  { Output, Diagnostics, StagedOps, RevisionRef, DryRun }
                    (capitalized — Go default JSON, no json tags)
     error body     { error: string }
   ============================================================================ */

/**
 * Private helper: perform a fetch against `path` with the given method and
 * optional body. Sets Content-Type: application/json for bodies. Throws an
 * Error (message from the {error} JSON field) on non-2xx. Returns undefined
 * on 204, otherwise returns the parsed JSON body.
 *
 * @param {string} method  HTTP method (GET, POST, PATCH, DELETE, …)
 * @param {string} path    Absolute path, e.g. "/api/workspaces"
 * @param {*}      [body]  Optional request body; will be JSON-encoded.
 * @returns {Promise<*>}
 */
async function req(method, path, body) {
  const opts = { method };
  if (body !== undefined) {
    opts.headers = { 'Content-Type': 'application/json' };
    opts.body = JSON.stringify(body);
  }
  const res = await fetch(path, opts);
  if (res.status === 204) return undefined;
  const data = await res.json();
  if (!res.ok) {
    const err = new Error((data && data.error) ? data.error : res.statusText);
    err.status = res.status;
    throw err;
  }
  return data;
}

/* ---------- health ---------- */

/**
 * GET /api/health
 * Resolves to { status: "ok" } on success; throws on network error or non-2xx.
 * @returns {Promise<{status: string}>}
 */
async function apiHealth() {
  return req('GET', '/api/health');
}

/* ---------- workspaces ---------- */

/**
 * GET /api/workspaces
 * Returns the list of workspaces visible to the current owner.
 * @returns {Promise<Array<{id: string, name: string, owner: string}>>}
 */
async function listWorkspaces() {
  return req('GET', '/api/workspaces');
}

/**
 * POST /api/workspaces
 * Creates a new workspace with the given name.
 * @param {string} name
 * @returns {Promise<{id: string, name: string, owner: string}>}
 */
async function createWorkspace(name) {
  return req('POST', '/api/workspaces', { name });
}

/**
 * PATCH /api/workspaces/{id}
 * Renames the workspace identified by id.
 * @param {string} id
 * @param {string} name  New name.
 * @returns {Promise<{id: string, name: string, owner: string}>}
 */
async function renameWorkspace(id, name) {
  return req('PATCH', '/api/workspaces/' + id, { name });
}

/**
 * DELETE /api/workspaces/{id}
 * Deletes the workspace identified by id.
 * @param {string} id
 * @returns {Promise<undefined>}  Resolves on 204.
 */
async function deleteWorkspace(id) {
  return req('DELETE', '/api/workspaces/' + id);
}

/* ---------- worlds ---------- */

/**
 * GET /api/workspaces/{id}/worlds
 * Returns all worlds in the workspace with head-revision, sim_time, and live-node indicator.
 * @param {string} wsId  Workspace id.
 * @returns {Promise<Array<{id: string, name: string, head_revision: number|null, sim_time: number|null, live_node: string|null}>>}
 */
async function listWorlds(wsId) {
  return req('GET', '/api/workspaces/' + wsId + '/worlds');
}

/**
 * GET /api/workspaces/{id}/worlds/{wid}/history
 * Returns the revision lineage for one world, ordered oldest→newest.
 * @param {string} wsId  Workspace id.
 * @param {string} wid   World id.
 * @returns {Promise<Array<{id: number, parent_id: number|null, created_at: string, source_path: string, is_head: boolean}>>}
 */
async function worldHistory(wsId, wid) {
  return req('GET', '/api/workspaces/' + wsId + '/worlds/' + wid + '/history');
}

/**
 * POST /api/workspaces/{id}/run
 * Runs a Starlark program against the workspace. HTTP 200 even on program failure;
 * check Diagnostics for errors.
 * @param {string} wsId     Workspace id.
 * @param {string} program  Starlark source.
 * @returns {Promise<{Output: string, Diagnostics: Array<{Severity: string, Code: string, Message: string, Detail: string, Filename: string, Line: number, Column: number}>, StagedOps: number, RevisionRef: string, DryRun: boolean}>}
 */
async function runProgram(wsId, program) {
  return req('POST', '/api/workspaces/' + wsId + '/run', { program });
}

/* ---------- nodes ---------- */

/**
 * GET /api/workspaces/{id}/nodes/info
 * Returns an array of node info objects. Telemetry fields (tps, real_tps,
 * paused, sim_time, last_autosave) are present only when liveness=="alive" and
 * the INFO round-trip succeeded. exit_code present only when liveness=="crashed".
 * Backend shape (handlers_nodes.go: nodeInfoResponse):
 *   { id: string, world_id: string, run_id: string,
 *     liveness: "alive"|"crashed"|"detached", status: string,
 *     tps?: number, real_tps?: number, paused?: boolean, sim_time?: number,
 *     last_autosave?: object, exit_code?: number }
 * @param {string} wsId  Workspace id.
 * @returns {Promise<Array<object>>}
 */
async function nodesInfo(wsId) {
  return req('GET', '/api/workspaces/' + wsId + '/nodes/info');
}

/**
 * GET /api/workspaces/{id}/nodes/{nid}/logs[?follow=1]
 * Returns a snapshot of the node's captured output ring buffer.
 * 404 means no buffer exists for that node id (never started / pruned).
 * 200 with lines:[] means the node started but produced no output yet.
 * Backend shape (handlers_nodes.go: logsResponse / logLineJSON):
 *   { lines: Array<{ time: string, level: "info"|"error", text: string }> }
 * @param {string}  wsId    Workspace id.
 * @param {string}  nid     Node id.
 * @param {boolean} follow  Append ?follow=1 (accepted by backend; currently no-op snapshot).
 * @returns {Promise<{lines: Array<{time: string, level: string, text: string}>}>}
 */
async function nodeLogs(wsId, nid, follow) {
  return req('GET', '/api/workspaces/' + wsId + '/nodes/' + nid + '/logs' + (follow ? '?follow=1' : ''));
}

/**
 * DELETE /api/workspaces/{id}/nodes/{nid}
 * Removes the persisted node row (row-only; does NOT stop a live process).
 * Intended for detached / stale rows. Returns 204 on success; 404 if nid is
 * unknown.
 * @param {string} wsId  Workspace id.
 * @param {string} nid   Node id.
 * @returns {Promise<undefined>}  Resolves on 204.
 */
async function deleteNode(wsId, nid) {
  return req('DELETE', '/api/workspaces/' + wsId + '/nodes/' + encodeURIComponent(nid));
}

/* ---------- notebooks ---------- */

/**
 * GET /api/workspaces/{id}/notebooks
 * Returns all notebooks for the workspace, sorted by name. Empty array when none.
 * Backend shape: notebookMeta {name: string, updated_at: string} (notebookstore.go:27).
 * @param {string} wsId  Workspace id.
 * @returns {Promise<Array<{name: string, updated_at: string}>>}
 */
async function listNotebooks(wsId) {
  return req('GET', '/api/workspaces/' + wsId + '/notebooks');
}

/**
 * GET /api/workspaces/{id}/notebooks/{name}
 * Returns a single notebook by name.
 * Backend shape: notebookDoc {name: string, cells: [{type: string, source: string}], updated_at: string}
 * (notebookstore.go:19).
 * @param {string} wsId  Workspace id.
 * @param {string} name  Notebook name (will be encodeURIComponent-encoded in URL).
 * @returns {Promise<{name: string, cells: Array<{type: string, source: string}>, updated_at: string}>}
 */
async function getNotebook(wsId, name) {
  return req('GET', '/api/workspaces/' + wsId + '/notebooks/' + encodeURIComponent(name));
}

/**
 * PUT /api/workspaces/{id}/notebooks/{name}
 * Creates or updates a notebook. Body is {cells} only (handlers_notebooks.go:45).
 * Backend shape for response: notebookDoc {name, cells, updated_at}.
 * @param {string} wsId   Workspace id.
 * @param {string} name   Notebook name (will be encodeURIComponent-encoded in URL).
 * @param {Array<{type: string, source: string}>} cells  Cell array to persist.
 * @returns {Promise<{name: string, cells: Array<{type: string, source: string}>, updated_at: string}>}
 */
async function putNotebook(wsId, name, cells) {
  return req('PUT', '/api/workspaces/' + wsId + '/notebooks/' + encodeURIComponent(name), { cells });
}

/**
 * POST /api/workspaces/{id}/upload
 * Uploads a save file (.zip) via multipart/form-data. Uses FormData/fetch directly
 * (bypassing req()) so the browser sets the correct multipart boundary.
 * @param {string} wsId  Workspace id.
 * @param {File}   file  The file object to upload (the "file" form part).
 * @returns {Promise<{path: string}>}  Absolute server path of the uploaded file.
 */
async function uploadSave(wsId, file) {
  const fd = new FormData();
  fd.append('file', file);
  const res = await fetch('/api/workspaces/' + wsId + '/upload', {
    method: 'POST',
    body: fd
    // Do NOT set Content-Type: the browser must set it with the multipart boundary.
  });
  const data = await res.json();
  if (!res.ok) {
    throw new Error((data && data.error) ? data.error : res.statusText);
  }
  return data;
}
