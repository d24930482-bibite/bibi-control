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

   Backend shapes (from api/handlers_workspaces.go):
     workspaceJSON  { id: string, name: string, owner: string }
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
    throw new Error((data && data.error) ? data.error : res.statusText);
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
