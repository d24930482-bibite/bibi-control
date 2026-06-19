# Surface map — read & mutation across saves / worlds / workspaces

A location index of *how data is read and mutated* in bibicontrol, at three scales:

- **SAVE** — one loaded save (per-entity / per-save DSL, parser, mutator).
- **WORLD** — one world (a save plus its live running node / lifecycle).
- **WORKSPACE** — across many saves/worlds (spanning reads, catalog, transfer).

Each file below is produced by one scan agent over a fixed, non-overlapping slice of
the codebase. Every entry is tagged `[READ]`/`[WRITE]` and `[SAVE]`/`[WORLD]`/`[WORKSPACE]`
with a `file:line` pointer. Each file ends with a **Missing seams** section — gaps where
a read/mutation across one of these scales is absent or forced into an escape hatch,
written as drop-in candidates for `../missing.md`.

## Files

- `01-save-parse-normalize.md` — raw save → normalized tables (read foundation)
- `02-save-mutation-engine.md` — sqlref resolvers, staging, install/commit (per-save write)
- `03-cross-save-transfer.md` — moving entities between saves/worlds; id/species remap
- `04-dsl-entity-surface.md` — object DSL for one save's entities/genes/brains/pellets
- `05-dsl-settings-zones-mirror.md` — settings/zones write + DuckDB mirror + commit_world
- `06-workspace-spanning-query.md` — spanning aggregate reads, mirror_saves catalog, SQL hatch
- `07-workspace-lifecycle.md` — registry, working-set, eviction, GC, commit
- `08-workspace-automation-nodes.md` — automation loops + live node control (running worlds)
- `09-duckdb-ipc-storage.md` — DuckDB import/query, IPC, blob/revision stores
- `10-ui-daemon-surface.md` — notebook UI, autocomplete type graph, daemon endpoints
