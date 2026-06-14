# Sim Game Control Plane Architecture Notes

## Product Shape

We are shipping a cross-platform control plane for a simulation game.

There are two major sides:

1. A client-side injection layer inside the Unity game.
   - This layer will eventually handle direct game controls, memory/runtime access, reload hooks, and other in-game integration.
   - This is explicitly out of scope for the control-plane implementation for now.
   - The injection layer will be handled separately later.
2. A local control-plane application/agent.
   - This is responsible for process orchestration, save parsing, save mutation, scripting, tracking state, and eventually coordinating with the game-side injection layer.

## Required Flow

1. Control game processes.
   - Start a game process.
   - Stop a game process.
   - Run health checks.
   - Work cross-platform.

2. Load and manage multiple game processes on a single machine.
   - The first target is multiple local processes on one machine.
   - Future work can extend this to remote machines or distributed workers, but that should not be designed into the first version too aggressively.

3. Parse autosaves and combine them with live simulation data.
   - Most control-plane operations will work from autosave data.
   - Some data will come directly from runtime memory through the game-side integration layer.
   - The control plane should treat this runtime data as an overlay on top of parsed save data.

4. Manipulate autosaves through an embedded scripting language.
   - Users or automation code should be able to run scripts against a structured save/simulation model.
   - On execution, the control plane writes a new save.
   - The new save is shipped back to the game process.
   - The game then auto-reloads that save.
   - The internal mechanics of auto-reload on the Unity/game side are out of scope for now.

5. Track saves and simulation entities in a local database.
   - Track save files, revisions, hashes, provenance, and script runs.
   - Track simulation entities extracted from saves and runtime overlays.
   - Preserve enough history for future analytics and debugging.

## Recommended Stack

### Control-Plane Core

For a small hobby project that may become distributed later, the best default is **Go** unless there is a specific reason to prefer Rust.

Use **Go** if the priority is fast iteration, straightforward process management, simple cross-platform builds, and lower implementation friction.

Use **Rust** if the priority is maximum correctness around binary save parsing/mutation, tighter control over memory/layout, or eventually shipping a more hardened product.

Recommended default for this project right now: **Go for the control-plane agent**.

Reasons Go is a good fit:

- Easy to build and maintain for a small project.
- Produces simple cross-platform binaries for Windows, macOS, and Linux.
- Strong standard library for processes, files, HTTP, JSON, and concurrency.
- Easier than Rust for quick iteration while the save format and control model are still changing.
- Good future path to distributed control through HTTP, WebSockets, gRPC, NATS, or a job/worker model.

Reasons Rust may still be worth choosing:

- Produces small, cross-platform binaries for Windows, macOS, and Linux.
- Good fit for process management, filesystem work, IPC, parsers, and long-running local agents.
- Strong safety and data modeling are useful when mutating save files.
- Has good embedded database support.
- Has solid options for embedded scripting.

Suggested Go packages:

- Standard `net/http` for the local API.
- `gorilla/websocket` or `nhooyr.io/websocket` for event streams.
- `database/sql` with `modernc.org/sqlite` or `mattn/go-sqlite3` for SQLite.
- `cobra` for CLI commands if needed.
- `fsnotify` for watching autosave directories.
- `zap` or `zerolog` for structured logs.

Suggested Rust crates if Rust is chosen:

- `tokio` for async runtime.
- `serde` and `serde_json` for structured models and API payloads.
- `sqlx` or `rusqlite` for SQLite.
- `sysinfo` for process discovery and health metadata.
- Platform-specific process control where needed:
  - Windows Job Objects.
  - Unix process groups/signals.
- `tracing` for structured logs.

### API Surface

Use a local **HTTP + WebSocket API** first.

Reasons:

- Easy to call from a UI, CLI, tests, and future tools.
- WebSockets are useful for process logs, health events, save reload events, and script progress.
- Simpler to debug than gRPC in early development.

Suggested Go options:

- Standard `net/http` first.
- Add `chi` if routing grows.
- Add OpenAPI later if external clients become important.

Suggested Rust options if Rust is chosen:

- `axum` for HTTP/WebSocket server.
- `tower` middleware.
- `utoipa` or OpenAPI generation later if needed.

### UI

Use a **local-first web UI served by the agent** as the default experience.

This means the control plane runs locally, but users interact with it through a browser at a local address such as `http://localhost:<port>`.

If a packaged desktop UI is needed later, wrap the same local web UI with **Tauri + TypeScript**.

Reasons:

- Browser UI is easier to build, iterate on, and share.
- Local agent has the filesystem and process access needed to manage the game.
- Users do not need to trust a cloud service with local saves or process control.
- The same UI can later talk to a remote coordinator if the system becomes distributed.
- Tauri can provide cross-platform desktop packaging if users want an app-shaped experience.
- Pairs well with either a Rust or Go backend.
- Keeps the UI as a normal web frontend while preserving native app distribution.

If no desktop UI is needed at first, start with a CLI plus local HTTP API.

Avoid a purely hosted web app as the first version. A hosted app cannot directly manage local game processes, inspect local save files, or coordinate reload flows without installing a local agent anyway.

### Embedded Scripting

For a Go control-plane agent, there are several viable scripting paths. The right choice depends on whether scripts are user-facing automation, safe rule expressions, internal developer extensions, or untrusted plugins.

Target users are technical hobbyists who should be able to generate and adapt scripts with LLM help, but they are not expected to safely manage a full embedded Go interpreter or professional plugin environment.

Recommended default for this project: **JavaScript via `goja` or Lua via `gopher-lua`**, with a constrained domain API.

Slight preference: **JavaScript via `goja`** if "vibe coding" and LLM-generated scripts are important. JavaScript has the strongest broad familiarity and the best chance that users can ask an LLM for a script, inspect it, and adapt it.

Use **Lua via `gopher-lua`** if the project wants a smaller, more game-tooling-native scripting language and the expected users are comfortable with Lua-like examples.

Reasons Lua is still a strong option:

- Widely understood as an embedded scripting language.
- Good fit for game tooling and simulation manipulation.
- Small runtime.
- Easier for users to learn than a custom DSL.
- `gopher-lua` is pure Go, which keeps cross-platform builds simple.

Main Go options:

1. **Lua with `gopher-lua`**
   - Best for user-facing save/sim manipulation scripts.
   - Pure Go, no cgo dependency.
   - Familiar to game/modding communities.
   - Good fit for scripts like "find entities, mutate fields, write a new save revision."
   - Must expose a constrained API and avoid opening unsafe standard libraries by default.

2. **JavaScript with `goja`**
   - Best if users are expected to generate scripts with LLMs and make small edits themselves.
   - Pure Go ECMAScript implementation.
   - Broad language familiarity and many examples for users to pattern-match against.
   - More surface area than Lua, so the host API must be intentionally small.
   - Recommended if the scripting UX matters more than the smallest possible embedded language.

3. **Starlark with `starlark-go`**
   - Best for deterministic, Python-like configuration and automation.
   - Strong choice if scripts should be more predictable and hermetic than Lua.
   - Less familiar to general game/modding users than Lua.
   - Good fit for controlled automation, config transforms, and repeatable scripted operations.

4. **Tengo**
   - Best for a small embedded language designed specifically for Go.
   - Simple, fast enough, native Go implementation.
   - Less widely known than Lua, Python-like languages, or JavaScript.
   - Good fit if we want a small purpose-built scripting language and do not care about ecosystem familiarity.

5. **Expr**
   - Best for safe expressions, filters, computed fields, and simple rules.
   - Not a full scripting language.
   - Good fit for user-defined filters like `entity.type == "vehicle" && entity.age > duration("30m")`.
   - Useful alongside Lua or Starlark rather than as the only scripting layer.

6. **CEL with `cel-go`**
   - Best for portable, safe, non-Turing-complete policy/rule expressions.
   - Strong fit for validation, filtering, predicates, and guardrails.
   - Less ergonomic for full save mutation workflows.
   - Useful if we need rules that must always terminate and be easy to reason about.

7. **Go scripts with Yaegi**
   - Best for internal developer extensions written in Go-like code.
   - Powerful, but too much power for normal user scripting.
   - Larger trust and API-control concerns.
   - Not recommended for this project's user-facing scripting layer.

8. **WebAssembly with `wazero`**
   - Best for future untrusted or semi-trusted plugin execution.
   - Stronger isolation model than ordinary embedded scripts.
   - Lets multiple source languages target the plugin interface.
   - More complex authoring and host API design.
   - Good future plugin path, not the first save-scripting layer.

Recommended scripting stack:

- First scripting layer: JavaScript with `goja` if LLM-assisted scripting is central; otherwise Lua with `gopher-lua`.
- Add Expr or CEL later for simple filters/rules if the UI needs user-authored predicates.
- Add DuckDB SQL for analytics queries, but keep it separate from save mutation scripting.
- Add WASM later only if third-party plugins need stronger isolation.

The script API should expose domain operations rather than raw database access:

- query entities by type, ID, save revision, process run, and time.
- iterate through cursors for large result sets.
- stage mutations in memory.
- validate staged changes.
- write a new save revision.
- record script provenance.
- never mutate a tracked save in place.

For untrusted third-party scripts, prefer **WebAssembly** later. Lua, Starlark, Tengo, Expr, and CEL can be constrained, but WASM gives a clearer plugin isolation story.

### Persistence

Use **SQLite as the operational source of truth**.

SQLite should store:

- Game process records.
- Process runs.
- Health-check events.
- Save file records.
- Save revisions.
- File hashes.
- Script executions.
- Entity snapshots extracted from saves.
- Runtime overlay snapshots from the injection layer.

Use **DuckDB + Parquet for ad hoc analytics over all historical data**.

DuckDB should be part of the planned architecture from the start if we expect ad hoc queries across all accumulated simulation data.

Why SQLite first:

- Better fit for operational state and app metadata.
- Simple cross-platform deployment.
- Excellent local durability.
- Easy migrations.
- Good enough for entity tracking, process state, and save history.

Why not DuckDB as the primary database:

- DuckDB is excellent for analytics, columnar scans, and large ad-hoc queries.
- It is less ideal as the main mutable application database for operational state.
- The control plane needs transactional metadata and frequent small reads/writes more than columnar analytics at first.

Recommended database approach:

- Primary DB: SQLite.
- Analytics format: Parquet files partitioned by run, entity type, and time window.
- Analytics engine: DuckDB querying Parquet directly.
- Optional DuckDB database file for saved views, derived tables, and repeated analytical workflows.
- SQLite keeps the current operational truth; DuckDB answers ad hoc historical questions.
- Known save fields should be parsed into typed analytical columns/tables, not only stored as raw JSON blobs.
- Raw JSON should still be preserved for lossless round-tripping and fields not yet modeled.

### Expected Scale

The expected working set may range from tens of thousands to millions of simulation entities, records, or events over time.

Current expected live scale:

- Around 1,000 live simulation entities at a time.
- Simulations may run continuously for days.
- Individual entities typically live for 30 to 60 minutes.

Future expected live scale:

- Around 10,000 to 100,000 live simulation entities at a time.

The important distinction is live working set versus historical volume. The live set is currently modest, while the accumulated history can grow large because simulations run for long periods and entities churn over time.

This does not automatically require a distributed database. It does require care in how data is stored and queried.

Recommended approach:

- Keep SQLite for operational state and indexed entity lookups.
- Keep the current live entity view compact and easy to query.
- Store historical entity lifecycle data as append-friendly events or snapshots.
- Record entity birth, updates, and death/despawn times explicitly.
- Partition or group historical data by run, save revision, and time window.
- Use batch inserts inside transactions for save ingestion.
- Use stable entity IDs and save revision IDs on every extracted row.
- Index the common lookup paths:
  - entity by save revision.
  - live entity by process/run.
  - entity by type.
  - entity by external/game ID.
  - entity lifecycle by run and time.
  - latest revision by game process.
  - script runs by save revision.
- Avoid loading every entity into memory for normal UI/API requests.
- Expose paginated and filtered query APIs.
- For scripts, expose query/cursor helpers instead of requiring scripts to materialize the full simulation unless explicitly requested.
- Store large raw snapshots and historical analytics exports in Parquet when analytical queries become important.
- Use DuckDB for large scans, aggregations, comparisons across revisions, and ad-hoc analytics.
- Make historical exports a normal pipeline, not a one-off migration.
- Extract all known nested save data into queryable analytical structures, including entity brains, genes, body state, environment state, settings, and species records.

SQLite is still appropriate at this scale for local operational data if queries are indexed and writes are batched. DuckDB becomes useful when the question is analytical, such as comparing millions of entities across many save revisions.

If the system becomes genuinely multi-machine or multi-user, add a coordinator service with PostgreSQL before trying to make SQLite itself distributed.

For 1,000 live entities, the agent can comfortably maintain an in-memory live view if useful. For 10,000 to 100,000 live entities, the design should still be viable, but the API and scripting layer must avoid full-table scans on every tick and should prefer filtered queries, cursors, and incremental updates.

### Analytics Storage Pattern

For ad hoc queries over all data, do not rely only on SQLite tables.

Recommended pattern:

- SQLite stores operational metadata and recent indexed state.
- Append historical entity observations/events to durable local files.
- Periodically compact/export history into Parquet.
- Partition Parquet by:
  - simulation run.
  - date or time window.
  - entity type.
  - event or snapshot kind.
- Query Parquet with DuckDB directly.
- Keep a small DuckDB catalog/database for saved views, macros, and derived analytical tables if useful.

Example analytical questions DuckDB should handle:

- entity counts over time.
- average lifespan by entity type.
- churn rate per hour.
- comparisons between save revisions.
- script impact before and after a mutation.
- anomalies across multi-day simulation runs.

The control plane should expose this as an analytics/query mode separate from operational control APIs. Operational APIs should stay fast and indexed; analytical queries can scan large historical partitions and may take longer.

## Data Model Direction

Core concepts:

- `GameInstance`: a configured game installation or launch profile.
- `GameProcess`: one running process.
- `ProcessRun`: a historical run of a game process.
- `HealthCheck`: observed process and integration-layer health.
- `SaveFile`: an observed save on disk.
- `SaveRevision`: immutable version of a save after parsing or mutation.
- `ScriptRun`: execution record for a save-manipulation script.
- `SimEntity`: normalized entity extracted from a save.
- `RuntimeOverlay`: data reported by the injection layer from live memory/runtime state.

Important rule:

Never mutate an existing save in place. Always create a new save revision, hash it, track its parent revision, and record the script or operation that produced it.

## Local First, Distributed Later

The first version should be local-first:

- One control-plane agent runs on one machine.
- The agent owns local process management.
- SQLite is local to that agent.
- Each game process has an isolated workspace.

Do not build a distributed system in v1, but keep the boundaries compatible with one:

- Put all process control behind an agent API instead of calling process code directly from the UI.
- Give every process, save revision, entity snapshot, and script run a stable ID.
- Keep process state and desired operations explicit.
- Make script execution jobs recordable and replayable.
- Design events as append-only facts where practical.
- Avoid assuming the UI and game process are on the same machine forever.

Future distributed shape:

- A central coordinator stores global state and schedules work.
- One lightweight agent runs per machine.
- Agents manage local game processes and local save workspaces.
- Agents report health, process events, save revisions, and script results back to the coordinator.
- SQLite can remain per-agent, while the coordinator can use PostgreSQL if multi-user or multi-machine coordination becomes important.

If multi-machine control must ship, use the same model earlier:

- **Agent**
  - Runs on every machine that hosts game processes.
  - Starts/stops local processes.
  - Watches local save folders.
  - Runs local health checks.
  - Executes local save import/export work.
  - Maintains a local SQLite operational cache.
  - Writes local Parquet analytics partitions when appropriate.

- **Coordinator**
  - Runs as a central service.
  - Owns fleet-wide desired state.
  - Knows which agents exist and what they are running.
  - Schedules process launches, script jobs, and save operations.
  - Stores global metadata in PostgreSQL if more than one machine or user needs coordination.
  - Provides the main web UI/API.

- **Analytics**
  - Agents can upload or sync Parquet partitions to shared storage.
  - DuckDB can query local partitions, shared partitions, or both.
  - The coordinator should track partition manifests so analytical queries know which runs/time windows exist.

Connection direction:

- Prefer agents dialing out to the coordinator.
- This avoids requiring inbound firewall/NAT access to every game machine.
- Use WebSockets, long polling, or a lightweight message bus for command/event flow.
- Keep direct coordinator-to-agent calls optional for trusted LAN deployments.

Do not make game machines share one SQLite database over a network filesystem. SQLite should remain local to one process/machine. Use PostgreSQL or another coordinator-owned database for global multi-machine state.

Deployment modes:

1. **Single-machine mode**
   - Agent, UI, SQLite, DuckDB, and game processes all run on one machine.

2. **LAN coordinator mode**
   - One coordinator runs on a trusted machine.
   - Agents run on each game machine.
   - Browser UI talks to the coordinator.

3. **Hosted coordinator mode**
   - Hosted service provides login, orchestration UI, and global state.
   - Local agents still perform all process/file/game control.
   - Useful if users need remote access or machines spread across networks.

The architecture should support all three modes without changing the scripting or save pipeline model.

## Save And Runtime Data Strategy

The control plane should parse autosaves into a stable internal model. Runtime memory data should arrive from the game-side injection layer through an explicit protocol, not through direct memory scraping in the control plane.

### Unity Save Discovery

Unity save locations vary by game, platform, engine setup, and whether the developer used `Application.persistentDataPath`, a custom save directory, Steam/Epic cloud paths, or game-specific profile folders.

Do not hard-code one save path. Build save discovery as a first-class subsystem.

Recommended discovery order:

1. Explicit user-configured save path.
   - Best and most reliable.
   - Let users select the autosave directory in the UI or config.

2. Game profile configuration.
   - Store known paths per game install/profile.
   - Allow multiple profiles/processes to use isolated save directories if the game supports it.

3. Platform default probes.
   - Windows common locations:
     - `%USERPROFILE%/AppData/LocalLow/<CompanyName>/<ProductName>`
     - `%USERPROFILE%/AppData/Local/<GameName>`
     - `%USERPROFILE%/Documents/My Games/<GameName>`
   - macOS common locations:
     - `~/Library/Application Support/<CompanyName>/<ProductName>`
     - `~/Library/Preferences/<CompanyName>/<ProductName>`
   - Linux common locations:
     - `~/.config/unity3d/<CompanyName>/<ProductName>`
     - `~/.local/share/<GameName>`
     - Steam Proton prefixes when running Windows builds under Proton.

4. Heuristic scanning.
   - Search likely folders for files matching known save extensions, naming patterns, recent modification times, and autosave cadence.
   - Never scan an entire drive by default.
   - Ask for confirmation before adopting a discovered path.

5. Injection-layer report.
   - Later, the game-side integration can report the active save path, profile ID, autosave cadence, and reload target path directly.
   - This should become the most reliable source once available.

The control plane should track save roots separately from managed save workspaces.

- **Save root**: where the game writes or expects autosaves.
- **Managed workspace**: where the control plane copies, hashes, parses, mutates, and archives saves.
- **Reload/drop path**: where the control plane places the newly generated save for the game to reload.

Important rule:

Never parse or mutate directly from the live autosave file while the game may still be writing it. Copy the candidate save into the managed workspace first, verify it is stable, hash it, and only then parse it.

### Zipped Save Handling

Saves are expected to be zipped or zip-like archives.

The ingestion pipeline should treat the zip archive as the immutable save artifact and parse extracted contents in a controlled workspace.

Recommended zip flow:

1. Detect candidate autosave archive.
2. Wait for the file to become stable:
   - size unchanged across checks.
   - modification time no longer moving.
   - archive can be opened successfully.
3. Copy the archive into the managed workspace.
4. Hash the copied archive.
5. Validate the archive:
   - reject absolute paths.
   - reject `..` path traversal.
   - reject entries that would extract outside the workspace.
   - enforce maximum uncompressed size.
   - enforce maximum file count.
6. Extract into a per-save temporary directory.
7. Identify manifest, metadata, serialized Unity data, JSON/XML/binary payloads, and other parse targets.
8. Parse extracted contents into the internal save/entity model.
9. Store the original zip hash and extracted file hashes.
10. For mutations, write modified contents into a new archive rather than changing the original.
11. Hash the new archive, record parent revision and script provenance, then place it at the reload/drop path.

Never trust zip entry paths. Treat zip extraction as untrusted input even when the save was produced locally.

Recommended flow:

1. Detect or receive autosave path.
2. Copy autosave into a managed workspace.
3. Hash the copied save.
4. Parse into structured entities.
5. Store save metadata and extracted entity snapshots in SQLite.
6. Receive runtime overlay data from the injection layer.
7. Merge save entities and runtime overlays into a queryable simulation view.
8. Run scripts against that view.
9. Write a new save revision.
10. Ship the new save to the game process.
11. Ask or allow the game-side integration to reload it.

## Process Management Notes

Process management must be designed for cross-platform differences.

Required capabilities:

- Start process with configured executable, working directory, args, and environment.
- Stop gracefully where possible.
- Force-kill after timeout.
- Track process ID and child process group where possible.
- Capture stdout/stderr if available.
- Detect crashed/exited processes.
- Run health checks.
- Associate a running process with a save workspace and control-plane state.

Health should have layers:

1. OS process is alive.
2. Expected files or save directories are accessible.
3. Injection layer heartbeat is available.
4. Game simulation is responsive enough to accept reload/control commands.

## Initial Milestones

1. Local agent skeleton.
   - Go binary.
   - Config file.
   - SQLite migrations.
   - Logging.

2. Process orchestration.
   - Start/stop one game process.
   - Health check process status.
   - Track process runs in SQLite.

3. Multiple local processes.
   - Manage several configured game processes.
   - Isolate workspace/save folders per process.

4. Save ingestion.
   - Watch or import autosaves.
   - Copy, hash, and register save revisions.
   - Stub parser until the save format is known.

5. Entity model.
   - Parse known save structures into `SimEntity` records.
   - Store snapshots in SQLite.

6. Scripting.
   - Embed Lua.
   - Expose a constrained API over the simulation model.
   - Support dry-run and execute modes.

7. Save mutation pipeline.
   - Script produces a new save revision.
   - Control plane records provenance.
   - New save is placed where the game-side reload integration expects it.

8. Injection-layer integration.
   - Add heartbeat.
   - Add runtime overlay data.
   - Add reload command or reload notification protocol.

## Key Recommendations

- Build the first version as a local-first control-plane agent, not a distributed system.
- Use SQLite as the source of truth.
- Add DuckDB later for analytics workloads instead of replacing SQLite.
- Treat parsed saves as immutable revisions.
- Treat runtime memory data as an overlay delivered by the injection layer.
- Keep the Unity injection layer protocol explicit and narrow.
- Use Lua for the first embedded scripting language unless there is a strong reason to keep scripts internal-only.
- Design for cross-platform process management from day one, but hide platform specifics behind a small process-control abstraction.

## Open Questions

- What is the target game save format?
- Are autosaves plain files, compressed archives, binary blobs, JSON/XML, or custom serialized Unity data?
- Do multiple game processes need isolated game install directories, isolated save directories, or both?
- Does the game support launch arguments for save directories or profiles?
- Should scripts be trusted local automation, user-provided mods, or potentially untrusted third-party code?
- Is the first UI a desktop app, a web UI, or a CLI?
- What data from RAM is essential and unavailable from saves?
- How frequently should runtime overlay data be sampled?
