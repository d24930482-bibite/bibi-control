# The Bibites — headless mode & IPC control

This documents the runtime control channel between the Go control plane and a
running Bibites simulation, plus the headless launch path. The game-side half is
a mod compiled into `BibitesAssembly` (see `dll/`); the control-plane half is the
`ipc` transport and the `simctl` typed client.

## Roles

- The **control plane dials in**: it is the TCP client (`ipc.Dial`). The
  **game-side DLL is the server** (`TcpListener`). This matches the launch flow —
  the control plane starts the process, then connects to the port it listens on.
- The session is symmetric (`ipc.Session`): either side can send requests,
  responses, and events. Today the control plane sends requests and the DLL
  answers them.

## Wire format

Newline-delimited JSON. Each message is one compact JSON `Envelope` terminated by
`\n` (Go's `json.Encoder`/`Decoder`; the DLL reads/writes lines).

```jsonc
{
  "id":       "…",        // request id (sender-assigned)
  "reply_to": "…",        // set on responses: the request's id
  "kind":     "request",  // request | response | event | error
  "command":  "STOP",     // on requests
  "payload":  { … },      // typed body (see below); omitted when empty
  "error":    "…",        // set on failures; non-empty => request failed
  "time":     "RFC3339"
}
```

Routing: a response **must** set `reply_to` to the request's `id`. A non-empty
`error` makes the client return `ipc.ErrRequestFailed`.

## Commands

All four are request/response. `payload` field names are the contract; keep them
identical in `ipc/commands.go` and `dll/BibiControl`.

### STOP — pause

- Request payload: none.
- Effect: forces the engine time scale to 0 (simulation freezes; `Update`-based
  control keeps working).
- Response: `{ "previous_time_scale": <float> }` — the configured speed before
  pausing, so a later RESUME can restore it.

### RESUME — run at a speed

- Request payload: `{ "time_scale": <float> }` (must be > 0).
- Effect: sets both target and engine time scale to `time_scale` so the speed
  takes effect immediately.
- Response: `{ "time_scale": <float> }`.

### INFO — telemetry

- Request payload: none.
- Response (fields may grow; treat all as optional on the client):
  ```jsonc
  {
    "tps":      60,        // configured target sim ticks/sec
    "real_tps": 58.25,     // measured sim ticks/sec
    "paused":   false,
    "sim_time": 1234.5,    // total simulated seconds
    "last_autosave": {     // newest autosave, or omitted if none
      "path": "…/Autosaves/autosave_20260615.zip",
      "name": "autosave_20260615.zip",
      "modified_unix": 1700000000,
      "time": "2026-06-15T12:00:00.0000000Z"
    }
  }
  ```

### RELOAD — reload most recent save

- Request payload: none.
- Effect: reloads the newest save (manual saves + autosaves combined) via
  `GameManager.StartGame`. The IPC server survives the scene reload
  (`DontDestroyOnLoad`).
- Response: `{ "save": "<path>", "ok": true }`. Errors if no save exists.

## Threading model (DLL side)

Network I/O runs on background threads (accept loop + one read loop per
connection). Unity state may only be touched on the main thread, so each command
is marshalled onto the main thread via a queue drained in `Update()`. The network
thread blocks on the result and writes the response. `Update()` runs every frame
regardless of `Time.timeScale`, so commands keep working while paused.

## Headless launch

`-bibiteHeadless` (plus `-bibiteSave`, `-bibiteIpcPort`, `-bibiteIpcHost`) boots
into the simulation with a save and starts the server. See `dll/README.md` for
flags, build, and run instructions. The control plane launches the process with
these args via `noderuntime`/`ipc.ProcessSpec` and connects to the chosen port.

Two launch gotchas learned on a real install (Steam, Unity 6.0):

- **Steam strips args on a direct `.exe` launch** (it relaunches the game through
  Steam). Add a `steam_appid.txt` containing the app id (`2736860`) next to the
  exe, or pass the args as Steam launch options, so the flags actually reach the
  game.
- **Quote save paths with spaces.** The Bibites' save folder lives under
  `AppData/LocalLow/The Bibites/The Bibites/...`, so an unquoted `-bibiteSave`
  path splits into several args.

## Verification status

- Go transport + `simctl` client: covered by `simctl/simctl_test.go` (the test's
  fake server is the canonical contract for the DLL).
- DLL: compiles against the real game assemblies; `-batchmode -nographics`
  headless launch and arg delivery confirmed against a live install.
- **Not yet confirmed end-to-end:** the mod executing in-game. On Unity 6 a
  standalone DLL listed in `ScriptingAssemblies.json` is not loaded/scanned, so
  the mod must be compiled into (or called from) `BibitesAssembly`. See the
  "Building" and "Status" sections of `dll/README.md`.

## Extending

Add a command in four places that must agree:

1. `ipc/commands.go` — constant + payload/result types.
2. `simctl/simctl.go` — a method wrapping `Request`.
3. `dll/BibiControl/SimCommands.cs` — a handler + `server.Register(...)`.
4. `simctl/simctl_test.go` — extend the fake server to cover it.

Future commands under consideration: `LAUNCH`, `LOAD`.
