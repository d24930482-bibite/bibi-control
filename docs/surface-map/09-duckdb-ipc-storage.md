# 09. DuckDB import/query + IPC + blob/revision storage

**Scope (files scanned):** `duckdb/import.go`, `duckdb/sqlref_scan.go`, `ipc/codec.go`, `ipc/commands.go`, `ipc/errors.go`, `ipc/id.go`, `ipc/opaque.go`, `ipc/process.go`, `ipc/session.go`, `ipc/types.go`, `blobstore/blobstore.go`, `blobstore/fsstore.go`, `revisionstore/store.go` (+ supporting non-Go: `duckdb/migrations/0001_extracted_save.sql`, `duckdb/migrations/0002_drop_json_scalars.sql`, `revisionstore/schema.sql`).

## Where the three data kinds physically live
- **Save bytes** (the raw `.bibites` archive): never in DuckDB. They live in the **blobstore** (`blobstore/fsstore.go` — fileblob bucket on disk under `root/objects/ab/cd/<sha256>`, or inline in the `Ref` for blobs < threshold). The revision row also carries the bytes inline in SQLite when small (`inline_blob BLOB`). Confirmed by `duckdb/import.go:144` ("Archive entry bytes remain outside DuckDB").
- **Normalized tables** (the parsed save projection): in **DuckDB**, partitioned by `save_id` (a content sha256), one row-family per `tb.NormalizedTables` spec (`duckdb/import.go:299`, migration `duckdb/migrations/0001_extracted_save.sql`). `save_id` is the sha-keyed partition (one shared handle keyed by save_id — see MEMORY).
- **Revisions / lineage / registry** (workspaces, worlds, nodes, save_revisions, script_runs): in **SQLite** via `revisionstore` (`revisionstore/schema.sql`). `metadata.sqlite` is shared across workspaces (`revisionstore/store.go:498-505`).

## Location map
### DuckDB import (normalized tables)
- `duckdb/import.go:41` — [READ] [n/a] — `Open` builds a DuckDB DSN with a temp_directory spill (and env-driven memory_limit/threads) and pings.
- `duckdb/import.go:93` — [WRITE] [SAVE] — `OpenAndImport`: open + migrate + import one ExtractedSave.
- `duckdb/import.go:106` — [WRITE] [WORKSPACE] — `ApplyMigrations` runs embedded `migrations/*.sql` in name order (creates the shared schema).
- `duckdb/import.go:137` — [WRITE] [SAVE] — `ImportExtractedSave`: migrate then ReplaceExtractedSave.
- `duckdb/import.go:146` — [WRITE] [SAVE] — `ReplaceExtractedSave`: one conn, BEGIN/deferred-ROLLBACK/COMMIT; `deleteSaveRows(save_id)` then `insertExtractedSave`.
- `duckdb/import.go:196` — [WRITE] [SAVE] — `CopySavePartition(src,dst)`: DB-side clone of every normalized table for a save_id, rewriting only `save_id` via `INSERT … SELECT`. Used to materialize a new save partition from an existing one (transfer/derive).
- `duckdb/import.go:250` — [READ] — `copySaveSelectList` derives the per-table column list from generated `NormalizedFieldSpec` (never a hand-kept allowlist; matches SQL-ref generation philosophy / MEMORY).
- `duckdb/import.go:272` — [WRITE] [SAVE] — `deleteSaveRows` DELETEs `WHERE save_id = ?` across every table in `allTables`.
- `duckdb/import.go:287` — [READ] — `allTables`/`normalizedTableNames`: table list derived from `tb.NormalizedTables`.
- `duckdb/import.go:297` — [WRITE] [SAVE] — `insertExtractedSave` iterates `tb.NormalizedTables`, reflecting each ExtractedSave field.
- `duckdb/import.go:307` — [WRITE] [SAVE] — `insertExtractedTable`: resolves the save struct field (`SaveField`), handles nil-pointer/empty-slice/optional.
- `duckdb/import.go:345` — [WRITE] [SAVE] — `appendRows`: uses the duckdb-go **Appender** (`NewAppenderWithColumns`) with precomputed column extractors; the bulk-insert hot path.
- `duckdb/import.go:429` — [WRITE] — `rowValuesInto` fills the driver.Value buffer per row via precomputed extractors.
- `duckdb/import.go:473` — [READ] — `buildColumnExtractors`: resolves struct field index paths + typed cell extractors once per table (H5 perf win).
- `duckdb/import.go:519` — [READ] — `cellValue`/`fieldValue`: per-cell Go→driver.Value coercion (uint64>int64 overflow errors; the reference impl extractors must match).
- `duckdb/import.go:557` — [READ] — `QuoteIdent`: SQL identifier quoting, exported and reused by the analytics query builder.

### sqlref scan (query rows → mutation refs)
- `duckdb/sqlref_scan.go:18` — [READ] — `SQLRefScanSpec` describes the target cell (Table/Column/ValueColumn/Op).
- `duckdb/sqlref_scan.go:46` — [READ] [SAVE/WORLD] — `ScanSQLRefs`: turns DuckDB query rows into `mutator.SQLValueRef` + current-value guard. Infers locator fields from column names; JSON-path resolution stays in `savemutator/thebibites`.
- `duckdb/sqlref_scan.go:131` — [READ] [SAVE/WORLD] — `ScanSQLRefsWithNewValue`: ScanSQLRefs + per-row computed replacement (`__new_val`, the DSL `set_expr` path); op is always SET.
- `duckdb/sqlref_scan.go:220` — [READ] — `sqlRefFromRow`: maps a fixed vocabulary of result columns (entry_name, body_id/has_body_id, egg_id, owner_kind/owner_id, path, setting_name, scope, target_key, value_type, wrapper_raw_json, content/group/pheromone/node/synapse/zone/changer indices, zone_id) onto the ref. **READ-only**: this slice produces refs; it never writes the save. The actual mutation lives in `savemutator/thebibites` (mutation-engine slice).
- `duckdb/sqlref_scan.go:456` — [READ] — `NormalizeSQLScanValue`: canonical Go scalar coercion shared with the analytics converter.

### IPC (process boundary + wire protocol)
- `ipc/types.go:49` — [READ/WRITE] — `Envelope`: the wire message (ID, ReplyTo, Kind, Command, Payload json.RawMessage, Error, Time). The protocol unit.
- `ipc/types.go:40` — `MessageKind` constants: request / response / event / error.
- `ipc/codec.go:5` — `Codec` interface; `JSONCodec` (default) — newline-delimited JSON over the conn.
- `ipc/session.go:29` — [READ/WRITE] — `Dial`/`NewSession`: opens a **TCP** session over `net.Conn`, spawns a `readLoop`. This is the cross-process boundary (Go control plane <-> game-side C# DLL).
- `ipc/session.go:64` — [WRITE→READ] — `Request`: send a KindRequest envelope (random ID), park a pending channel, await the reply matched by `ReplyTo`; decodes typed result.
- `ipc/session.go:56` — [WRITE] — `Notify`: fire-and-forget KindEvent.
- `ipc/session.go:102` — [WRITE] — `Send`: `json.Encoder.Encode` under writeMu; refuses after Close.
- `ipc/session.go:154` — [READ] — `readLoop`: decode envelopes; route by `ReplyTo` to pending requests, else push to a 128-buffered `events` channel (drops events if nobody consumes).
- `ipc/commands.go:11` — the sim-control command vocabulary: `STOP`/`RESUME`/`INFO`/`RELOAD` with typed payload/result structs. Wire contract shared with `dll/BibiControl` and `simctl.Client`.
- `ipc/process.go:22` — [WRITE] [n/a] — `StartProcess`: spawns the game OS process (`exec.CommandContext`), tracks state (starting/running/exited/failed), `go p.wait()`.
- `ipc/process.go:101` — [WRITE] — `Process.Kill`: forcible OS kill (graceful shutdown is the game-owned compat endpoint's job).
- `ipc/opaque.go:8` — `OpaqueNode`: a deliberately policy-free pairing of a `Process` and/or a `Session`; higher layers assign meaning. Note: this `OpaqueNode` is the transport handle, NOT the `revisionstore.Node` registry row.
- `ipc/id.go:8` — `newID`: 12 random hex bytes for envelope correlation.
- `ipc/errors.go:5` — `ErrClosed`/`ErrNoProcess`/`ErrRequestFailed`.

### blobstore (save bytes, content-addressed)
- `blobstore/blobstore.go:20` — `Store` interface: `Put`/`Get`/`Has`/`Delete` (Delete idempotent — the only byte-destructive primitive).
- `blobstore/blobstore.go:35` — `Ref{SHA256, Size, Inline}`: content reference; small blobs carry bytes inline, large ones live in the bucket.
- `blobstore/blobstore.go:48` — [READ] — `Ref.Validate`: verifies inline bytes against digest+size.
- `blobstore/fsstore.go:41` — [WRITE] [SAVE] — `NewFSStore`: filesystem-backed (gocloud `fileblob`) content-addressed store rooted at `root`.
- `blobstore/fsstore.go:98` — [WRITE] [SAVE] — `Put`: digest the blob; inline if `< inlineThreshold` else dedup-check (`storedObjectMatches`) and `WriteAll` to `objects/ab/cd/<sha256>`.
- `blobstore/fsstore.go:128` — [READ] [SAVE] — `Get`: read object, re-verify size+digest (or return inline bytes).
- `blobstore/fsstore.go:158` — [READ] [SAVE] — `Has`: stat object (NotFound→false); inline always present.
- `blobstore/fsstore.go:190` — [WRITE] [SAVE] — `Delete`: idempotent byte delete (NotFound→nil); the crash-safe eviction sequence relies on this (catalog flip committed first).
- `blobstore/fsstore.go:214` — `objectKey`: sharded path layout `objects/<2>/<2>/<sha256>`.

### revisionstore (lineage + registry, SQLite)
- `revisionstore/store.go:153` — [WRITE] [WORKSPACE] — `Open`: SQLite at path (FK pragma per-conn, MaxOpenConns=1), applies `schema.sql`. This DB (`metadata.sqlite`) is shared across workspaces.
- `revisionstore/store.go:201` — [WRITE] [WORKSPACE] — `RecordScriptRun`: persists a script execution (sha, status, output, staged_ops, dry_run).
- `revisionstore/store.go:250` — [WRITE] [WORLD] — `RecordRevision`: inserts an immutable save revision (sha256, size, parent_id lineage, world_id, blob_ref JSON, inline_blob, script_run_id); seeds+increments per-hash refcount in-tx.
- `revisionstore/store.go:285` — [WRITE] [WORLD] — `RecordRevisionAdvancingHead`: insert revision + set world head (head_revision_id + sim_time) atomically so a head never points at a missing revision.
- `revisionstore/store.go:337` — [WRITE] [WORLD] — `insertRevisionTx`: the actual revision INSERT + per-hash `incBlobRefTx` (shared dedup refcount).
- `revisionstore/store.go:405` — [READ] [WORLD] — `RevisionByID`.
- `revisionstore/store.go:423` — [READ] [SAVE] — `RevisionsBySHA256`: all revisions of one content hash (dedup view).
- `revisionstore/store.go:463` — [READ] [WORLD] — `RevisionsForWorld`: lineage/history order for a world.
- `revisionstore/store.go:506` — [READ] [WORKSPACE] — `RevisionsForWorkspace`: JOIN worlds; batches the W+1 per-world loop for the analytics mirror rebuild; the workspace_id scope is load-bearing (shared DB).
- `revisionstore/store.go:577` — [READ] [WORKSPACE] — `CatalogFingerprint`: one aggregate (Count/MaxID/StateSum) used to skip analytics-mirror rebuilds.
- `revisionstore/store.go:609/655/696` — [READ] — `MirrorOnlyRevisions`/`OrphanedBlobs`/`FullRevisions`: reconcile + GC candidate scans.
- `revisionstore/store.go:734` — [READ] [WORLD] — `IsRevisionHead`.
- `revisionstore/store.go:757` — [WRITE] [WORLD] — `MarkMirrorOnly`: unconditional reconcile-repair demote (non-head full-but-missing).
- `revisionstore/store.go:783` — [WRITE] [WORKSPACE] — `CreateWorkspace` (UUID id).
- `revisionstore/store.go:853` — [WRITE] [WORLD] — `CreateWorld` (head/sim_time NULL until first revision).
- `revisionstore/store.go:928` — [WRITE] [WORLD] — `SetWorldHead`: standalone head advance.
- `revisionstore/store.go:964/1039/1065` — [WRITE] [WORKSPACE] — `CreateNode`/`BindNode`/`SetNodeStatus`: the compat-runner registry rows (node ↔ world binding, status). `nodes.world_id` is `ON DELETE SET NULL`.
- `revisionstore/store.go:1094` — [WRITE] [WORKSPACE] — `RenameWorkspace` (0 rows → sql.ErrNoRows).
- `revisionstore/store.go:1136` — [WRITE] [WORKSPACE] — `DeleteWorkspace`: atomic cascade (NULL heads → drain revisions leaves-first → nodes → worlds → workspace). Leaves script_runs and blobstore bytes untouched.
- `revisionstore/store.go:1242/1270` — [WRITE] [WORKSPACE] — `DeleteNode`/`DeleteNodeByNodeID`.
- `revisionstore/store.go:1297/1339` — [WRITE] [SAVE] — `IncBlobRef`/`DecBlobRef`: per-content-hash dedup refcount levers (floored at 0).
- `revisionstore/store.go:1372` — [WRITE] [WORLD] — `EvictRevisionBlob`: demote full→mirror_only (refuses heads / still-referenced); never deletes bytes or the row.
- `revisionstore/store.go:1425` — [READ] [WORKSPACE] — `UnreferencedBlobs`: refcount=0 GC candidate digests.
- `revisionstore/store.go:1461` — [WRITE] [WORLD] — `PromoteRevision`: mirror_only→full when bytes reappear (idempotent).
- `revisionstore/store.go:1629/1643` — [READ] — `encodeBlobRef`/`decodeBlobRef`: only sha256+size are persisted as JSON; the inline bytes ride the `inline_blob` BLOB column. Decode re-validates sha/size against the row.

## Read paths
- **Analytics / DSL query → mutation refs.** A DuckDB SELECT over the normalized `save_id`-partitioned tables yields rows; `ScanSQLRefs` / `ScanSQLRefsWithNewValue` (`duckdb/sqlref_scan.go:46,131`) convert them into `mutator.SQLValueRef` + current-value guard (+ computed new value). This slice produces refs and reads cells; it does NOT apply the mutation (that crosses into the mutation-engine slice, `savemutator/thebibites`).
- **Save bytes read-back.** `FSStore.Get`/`Has` (`blobstore/fsstore.go:128,158`) with digest+size verification; inline refs short-circuit.
- **Lineage / registry reads.** `RevisionByID`, `RevisionsForWorld`, `RevisionsForWorkspace`, `RevisionsBySHA256`, `ListWorkspaces/Worlds/Nodes`, `CatalogFingerprint`, and the reconcile/GC scans (`revisionstore/store.go`).
- **IPC reads.** `Session.Request` reply decode + `Session.Events()` channel (`ipc/session.go:64,154`); `INFO` command (`ipc/commands.go`).

## Mutation paths
- **DuckDB normalized tables.** Replace-by-save_id (`ReplaceExtractedSave` = `deleteSaveRows` + Appender bulk insert) and DB-side clone (`CopySavePartition`). Granularity is whole-save-partition; there is no per-row UPDATE/DELETE in this slice.
- **Blob bytes.** `FSStore.Put` (content-addressed, dedup) and `FSStore.Delete` (idempotent; the only byte-destructive op).
- **Revision/registry.** `RecordRevision[AdvancingHead]`, `SetWorldHead`, head/tier/refcount transitions (`MarkMirrorOnly`, `EvictRevisionBlob`, `PromoteRevision`, `Inc/DecBlobRef`), and the lifecycle cascade `DeleteWorkspace`/`DeleteNode*`. All multi-step writes use a tx with deferred Rollback.
- **IPC writes.** `Session.Send`/`Request`/`Notify` envelopes; `Process.Start`/`Kill` for the game process; `STOP`/`RESUME`/`RELOAD` commands.

## Missing seams

### Normalized-table mutations are coarse replace-only (no per-row DuckDB write here)
**What's missing.** The DuckDB layer in this slice can only delete-and-reinsert a whole save partition (`ReplaceExtractedSave`, `duckdb/import.go:146`) or clone one (`CopySavePartition`, `:196`). `sqlref_scan.go` is purely READ — it produces `SQLValueRef`s but never writes a cell. There is no UPDATE/DELETE-row path in this slice; the analytics tables are a derived projection that is wholesale re-materialized.
**Consequence.** Mutations are applied to the **save bytes** (savemutator) and the DuckDB projection is rebuilt, not edited in place. Re-import after every mutation is the implicit cost (ties to the "test suite is import-bound" MEMORY note).
**Where it lives.** `duckdb/import.go:146`, `duckdb/import.go:196`, `duckdb/sqlref_scan.go:46`. **Overlaps mutation-engine slice** (the write side of these refs lives in `savemutator/thebibites.ResolveSQLValueRef`) and the **spanning slice** (cross-world reshape reuses the collection/`CopySavePartition` abstraction — see F2/transfer in MEMORY).

### `save_id` ↔ `save_revisions.sha256` linkage is not enforced across stores
**What's missing.** DuckDB partitions by `save_id` (a sha256 supplied to `ExtractTables`, `saveparser/thebibites/normalize.go:8`) and revisionstore keys revisions by `sha256` (`revisionstore/schema.sql:42`), but nothing in this slice ties a DuckDB partition's existence to a revision row or vice-versa — they are two independent stores keyed by the same hash. `deleteSaveRows` and `DeleteWorkspace` operate on their own store with no cross-store transaction.
**Consequence.** A DuckDB partition can outlive its revision (or be missing when a revision exists). Consistency is whatever the orchestrating layer (workspace/lifecycle slice) enforces; this slice provides no joint invariant.
**Where it lives.** `duckdb/import.go:272` (`deleteSaveRows`) vs `revisionstore/store.go:1136` (`DeleteWorkspace`). **Overlaps lifecycle slice** (who deletes the matching DuckDB partition when a workspace/revision is deleted is not visible here — `DeleteWorkspace` explicitly leaves "blobstore bytes the eviction layer's job" and says nothing about DuckDB).

### Blob byte deletion is decoupled from refcount/eviction bookkeeping
**What's missing.** `EvictRevisionBlob` (`revisionstore/store.go:1372`) only flips tier/blob_present and never deletes bytes; `UnreferencedBlobs`/`OrphanedBlobs` only *report* candidates; `FSStore.Delete` is the only byte-destructive op but lives in a different package. There is no single transaction spanning "flip catalog row" + "delete blob byte" — by design (the comments call out a crash-safe ordering: catalog flip committed first, then idempotent byte delete, re-runnable).
**Consequence.** A crash between the catalog flip and the byte delete leaves orphan bytes on disk until a later GC pass (G3) re-derives the candidate list and re-deletes. Correctness depends on `FSStore.Delete` idempotency and the GC pass actually running — neither is owned in this slice.
**Where it lives.** `revisionstore/store.go:1372,1422,1455` + `blobstore/fsstore.go:190`. **Overlaps lifecycle/GC slice** (G2/G3 eviction + GC orchestration is not in these files).

### Event-channel backpressure is lossy (unsolicited IPC events dropped)
**What's missing.** `Session.readLoop` pushes non-reply envelopes onto a 128-buffered `events` channel and **drops** them when full (`ipc/session.go:181-185`); `Send` likewise has no per-request timeout beyond the caller's ctx, and a slow/full pending channel reply is dropped in `readLoop` (`:173-176`).
**Consequence.** Game-pushed events (e.g. autosave-written notifications) can be silently lost if the consumer stalls. Requests are safe (correlated + parked), but the event stream is best-effort.
**Where it lives.** `ipc/session.go:181`. No direct overlap with the storage slices; relevant to the IPC/automation consumer (automation-loop slice, which polls workspace state).
