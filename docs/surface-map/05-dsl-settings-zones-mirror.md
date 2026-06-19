# 05. DSL settings/zones + DuckDB mirror + commit

**Scope (files scanned):** `script/thebibites/settings_value.go`, `script/thebibites/zones.go`, `script/thebibites/scope.go`, `script/thebibites/sql.go`, `script/thebibites/loadedsave.go`, `script/thebibites/mirror.go`, `script/thebibites/commit_world.go`. (`*_test.go` skipped.)

## Location map

### Settings read/write surface (`settings_value.go`) — [SAVE]
- `settings_value.go:49` — [READ] [SAVE] — `Settings.Attr`: `save.settings.{simulation,independent,material(name)}` selects a `SettingScope` (table + owner_id discriminator).
- `settings_value.go:68` — [READ] [SAVE] — `materialBuiltin`: `save.settings.material("Name")` -> `SettingScope{table:"settings_material_values", ownerID:name}`.
- `settings_value.go:102` — [READ] [SAVE] — `SettingScope.Get`: `scope["name"]` resolves a `*SettingValueRow` via `ls.settingRow` -> a `Setting` handle (missing => KeyError).
- `settings_value.go:136` — [READ] [SAVE] — `Setting.Attr`: `.value`/`.name`/`.type`/`.scope` read the in-memory row; `.set` returns the write builtin.
- `settings_value.go:162` — [WRITE] [SAVE] — `Setting.setBuiltin`: `.set(v)` -> `ls.setSettingValue`.
- `settings_value.go:174` — [WRITE] [SAVE] — `setSettingValue`: validate against scalar type, write-through the in-memory row, stage via `stageScalarSet` mirrored on `(entry_name, path)`. MIRRORED.
- `settings_value.go:212` — [READ] [SAVE] — `settingValueToStarlark`: typed cell -> Starlark by `ScalarType` (null -> None).

### Zones read/write surface (`zones.go`) — [SAVE]
- `zones.go:63` — [READ] [SAVE] — `Zones.Len`/`Index`/`Iterate`: `save.zones` indexable/iterable over `ls.tables.SettingsZones`.
- `zones.go:79` — [READ] [SAVE] — `Zones.countBuiltin`: `save.zones.count()`.
- `zones.go:88` — [WRITE-prep] [SAVE] — `Zones.cloneBuiltin`: `save.zones.clone(i)` deep-copies template `RawJSON` into a detached `PendingZone` (not staged yet).
- `zones.go:145` — [READ] [SAVE] — `Zone.Attr`: `.values` (a `SettingScope` over `settings_zone_values`), `.delete`, plus scalar columns via `zoneRegistry()`.
- `zones.go:175` — [WRITE] [SAVE] — `Zone.SetField`: `z.name/material/distribution = v` — validate, write-through row, `stageScalarSet` mirrored on `(entry_name, zone_index)`, id-guarded by `zone_id`. MIRRORED.
- `zones.go:224` — [WRITE/STRUCTURAL] [SAVE] — `Zone.deleteBuiltin`: `z.delete()` stages `StageSQLDelete` located by `zone_index`, guarded by `zone_id`. NOT mirrored.
- `zones.go:247` — [READ] [SAVE] — `zoneValuesOwnerID`: reproduces the parser's owner-id rule (`zone_id` or `zone_index`) so `z.values` resolves through the shared settings index.
- `zones.go:279` — [READ] [SAVE] — `PendingZone.Attr`: reads edited values out of the cloned JSON map, falling back to the template row.
- `zones.go:315` — [WRITE/in-memory] [SAVE] — `PendingZone.SetField`: edits a writable scalar in the clone's JSON; nothing staged until append.
- `zones.go:340` — [WRITE/STRUCTURAL] [SAVE] — `PendingZone.appendBuiltin`: `z.append()` allocs a fresh zone id (`allocZoneID`), stages `StageSQLAppend` of the whole object. NOT mirrored.
- `zones.go:421` — [READ] [SAVE] — `pendingZoneValues.Get`: reads an inherited zone-scoped value out of the clone (unwraps "Value").
- `zones.go:442` — [WRITE/in-memory] [SAVE] — `pendingZoneValues.SetKey`: `z.values["k"]=v` edits an inherited zone value in the clone (inherited-key-only, no type change, wrapper-shape preserved). NOT staged/mirrored until append.

### Per-save query scoping (`scope.go`, `sql.go`) — mostly [READ] [SAVE], spanning [WORKSPACE]
- `scope.go:43` — [READ] [SAVE/WORKSPACE] — `SaveScope` interface: `scopeClause`/`writable`/`catalogJoin`/`catalogCols` — injects the save-partition filter BY CONSTRUCTION.
- `scope.go:66` — [READ/WRITE] [SAVE] — `workingScope`: the single writable scope; clause `"<identity>.save_id = ?"`/[saveID].
- `scope.go:84` — [READ] [WORKSPACE] — `spanningScope`: read-only `save_id IN (SELECT save_id FROM mirror_saves [WHERE world_id=?])` (out of this slice's primary scope; owned by 06).
- `scope.go:129` — [READ] [SAVE] — `scopeFor`: nil -> default `workingScope{ls.saveID}` (keeps single-save path byte-identical).
- `scope.go:153` — [READ] [WORKSPACE] — `NewSpanningReader`/`Bibites/Eggs/Pellets`: save-less spanning reader over the shared handle (06's surface; noted for the `LoadedSave{db,willCommit:false}` shortcut).
- `sql.go:46` — [READ] [SAVE] — `LoadedSave.query`: opens DB lazily, `flushMirror` at head, runs SQL. The read-after-write seam.
- `sql.go:92` — [READ] [SAVE] — `LoadedSave.Query`: `world.query()` executor; wraps the user SQL with `scopedQuery`, returns `[]map[string]any`.
- `sql.go:108` — [READ] [SAVE] — `scopedQuery`: prepends one shadowing CTE per normalized table filtered to `save_id = ls.saveID` (the save_id self-scoping rule — see below).
- `sql.go:167` — [READ] [SAVE] — `Save.sqlBuiltin`: `save.sql(query)` raw escape hatch — NO scoping CTE, NO friendly-name resolution; runs `s.ls.query(query)` verbatim.
- `sql.go:356` — [READ] [SAVE/WORKSPACE] — `whereClause`: `WHERE <scope clause> [AND (predicate)]`; partition filter from the active scope.
- `sql.go:373` — [READ] [SAVE] — `LoadedSave.workingScope`: single-partition scope used by all mutation/match builders (mutation can't reach history/all-worlds).
- `sql.go:394`,`460` — [READ] [SAVE/WORKSPACE] — `scalarAgg`/`groupedAgg`: push-down aggregates under `scopeFor`.
- `sql.go:535` — [WRITE] [SAVE] — `bulkSet`: push-down match -> per-row `writeThroughAndStage` (mirrored), batched. Uses `workingScope`.
- `sql.go:585` — [WRITE] [SAVE] — `writeThroughAndStage`: capture prior, write-through in-memory, `stageScalarSet` keyed on `entry_name` with rollback. MIRRORED.
- `sql.go:630` — [WRITE] [SAVE] — `bulkSetExpr`: per-row computed value; coerce + re-validate POST-compute, then `writeThroughAndStage`. MIRRORED.
- `sql.go:974` — [WRITE/STRUCTURAL] [SAVE] — `bulkDelete`: stages an entity delete per matching `entry_name` (reuses `stageEntityDelete`). NOT mirrored.
- `sql.go:992` — [READ] [SAVE] — `matchingEntryNames`: push-down SELECT of matched `entry_name`s (used by `bulkDelete`).
- `sql.go:1067` — [READ] [SAVE/WORKSPACE] — `rewritePredicate`: literal-aware identifier tokenizer that qualifies friendly columns; optional catalog-column rewrite for spanning.

### In-memory state + index lookups (`loadedsave.go`) — [SAVE], commit [WORLD]
- `loadedsave.go:35` — [STATE] [SAVE] — `LoadedSave`: path/saveID/archive/`tables` (ExtractedSave), lazy `db`, staged `session`, indices, `mirror`/`mirrorDirty`, `stagedOps`/`dryRun`/`willCommit`, zone-id allocator, test counters.
- `loadedsave.go:123` — [READ] [SAVE] — `Load`: parse once; saveID = archive SHA256 (fallback FileName); DuckDB not opened.
- `loadedsave.go:146` — [READ] [WORLD] — `LoadInto`: parse under an explicit worldID + shared caller-owned `*sql.DB` (the workspace working-partition seam; openDB short-circuits, never imports).
- `loadedsave.go:183` — [READ] [SAVE] — `buildAccess`: entry_name -> row index per entityTables table (`tableAccess`).
- `loadedsave.go:238` — [READ] [SAVE] — `rowForEntry`: O(1) `(table, entry_name)` -> in-memory row.
- `loadedsave.go:329` — [READ] [SAVE] — `settingsBacking`: live row slice per settings table (so `settingRow` returns a write-through pointer).
- `loadedsave.go:348` — [READ] [SAVE] — `settingRow`: `(table, owner_id, setting_name)` -> `*SettingValueRow` via the lazy `settingsIdx`.
- `loadedsave.go:361`,`374` — [READ] [SAVE] — `buildSettingsIndex`/`indexSettings`: `table -> owner_id -> setting_name -> backing index`.
- `loadedsave.go:391` — [WRITE-prep] [SAVE] — `allocZoneID`: lazy `max(zone_id)+1` allocator for clone-append; `(0,false)` if no id-bearing zone.
- `loadedsave.go:426` — [READ] [SAVE] — `openDB`: lazy in-memory `OpenAndImport` OR short-circuit to injected handle (no import, no `dbOpenCount`); flushes mirror once after a fresh open.
- `loadedsave.go:451` — [WRITE/MIRROR] [SAVE] — `flushMirror`: drains the buffer into the open DuckDB as one UPDATE per `(table,column)`; no-op if clean or DB not yet open.
- `loadedsave.go:475` — [WRITE-prep] [SAVE] — `entityLocatorRef`: builds entry_name + id-guard locator from registry metadata.
- `loadedsave.go:546` — [WRITE/COMMIT] [SAVE] — `ensureApplied`: idempotent `session.Apply` to the in-memory archive.
- `loadedsave.go:561` — [WRITE/COMMIT] [SAVE] — `WriteSave`: apply + `tb.WriteArchive(path)` (no reparse) — `save.commit(path)` plain-file export.
- `loadedsave.go:577` — [WRITE/COMMIT] [SAVE] — `Commit`: apply -> serialize -> `blobs.Put` -> `RecordRevision` (parent-less standalone path).
- `loadedsave.go:592` — [WRITE/COMMIT] [SAVE] — `prepareCommit`: apply -> `WriteArchiveTo(buf)` (one WriteArchive, no temp file) -> `blobs.Put` (+ optional verify).
- `loadedsave.go:642` — [WRITE/COMMIT] [SAVE] — `verifyRoundTrip`: the ONLY reparse on the commit path (temp file, sha assert).

### Mirror core (`mirror.go`) — [WRITE] [SAVE]
- `mirror.go:29-61` — [STATE] [SAVE] — `mirrorLocator`/`mirrorKey`/`mirrorRow`/`mirrorColumn`/`mirrorBuffer`: pending scalar sets grouped by `(table,column)`, keyed by composite discriminator values.
- `mirror.go:66` — [WRITE] [SAVE] — `mirrorBuffer.record`: buffer one set, last-write-wins per composite locator key.
- `mirror.go:98` — [WRITE] [SAVE] — `recordMirrorRow`: buffer + set `mirrorDirty`.
- `mirror.go:116` — [WRITE] [SAVE] — `stageScalarSet`: the shared scalar-set tail — `StageSQLSet(ref.WithExpected(old), staged)`, then on success `stagedOps++` + `recordMirrorRow(staged)`; runs `rollback` on stage failure.
- `mirror.go:133` — [WRITE/MIRROR] [SAVE] — `flushMirrorColumn`: one set-based `UPDATE … FROM (VALUES …)` AND-joined on every discriminator + `save_id = ls.saveID`, value CAST to the column type.

### Per-save / per-world commit (`commit_world.go`) — [WRITE/COMMIT] [WORLD]
- `commit_world.go:34` — [STATE] [WORLD] — `WorldCommit`: `Committed`, `Revision`, `Applied` (post-Apply archive with STALE projections), `SaveID == worldID`.
- `commit_world.go:63` — [WRITE/COMMIT] [WORLD] — `RunAndCommitWorld`: run program -> `commitLoadedWorld` with real provenance.
- `commit_world.go:129` — [WRITE/COMMIT] [WORLD] — `CommitLoadedWorld`: object-based (no re-run) commit of already-staged mutations; synthetic provenance.
- `commit_world.go:184` — [WRITE/COMMIT] [WORLD] — `commitLoadedWorld`: the ONE commit body — `willWrite` gate, `prepareCommit` (one WriteArchive) BEFORE `RecordScriptRun`, then `RecordRevisionAdvancingHead` (parent threaded, head + sim_time advanced atomically, blob self-ref — no separate IncBlobRef).

## Read paths

**Settings.** `save.settings.<scope>["name"].value` — `Settings.Attr` (settings_value.go:49) picks a `SettingScope` (table + owner_id); `SettingScope.Get` (settings_value.go:102) resolves through `ls.settingRow` (loadedsave.go:348) against the lazy `(owner_id, setting_name)` index; `Setting.Attr` (settings_value.go:136) reads the in-memory `*SettingValueRow` and `settingValueToStarlark` (settings_value.go:212) types the cell. Material scope is `save.settings.material("Name")` (owner_id = material name); zone-scoped values reuse the SAME `SettingScope` keyed on `settings_zone_values` via `zoneValuesOwnerID` (zones.go:247). All settings reads are in-memory; no DuckDB.

**Zones.** `save.zones` enumerates `ls.tables.SettingsZones` (zones.go:63); a `Zone` reads scalar columns via reflection through `zoneRegistry()` (zones.go:145) and `.values` reuses the settings `SettingScope`. A `PendingZone` reads out of its cloned JSON map (zones.go:279/421), falling back to the template row.

**SQL / analytics.** Two surfaces over the SHARED workspace DuckDB handle:
- `save.sql(query)` (sql.go:167) is the raw, UNSCOPED hatch — it runs the query verbatim; isolation is the caller's responsibility.
- `world.query(query)` (`LoadedSave.Query`, sql.go:92) and every collection aggregate (`scalarAgg`/`groupedAgg`, sql.go:394/460) are scoped BY CONSTRUCTION.

`LoadedSave.query` (sql.go:46) is the common spine: open DB lazily, `flushMirror` at the head (so a staged-but-uncommitted set is visible to a read-after-write), then run.

## Mutation paths

**MIRRORED scalar writes (visible to in-run reads after the next `flushMirror`):**
- `setting.set(v)` (settings_value.go:174) — mirrored on `(entry_name, path)`.
- `zone.name/material/distribution = v` (zones.go:175) — mirrored on `(entry_name, zone_index)`, id-guarded by `zone_id`.
- `bulkSet` / `bulkSetExpr` (sql.go:535/630) via `writeThroughAndStage` (sql.go:585) — mirrored on `entry_name`.

Every scalar write funnels through `stageScalarSet` (mirror.go:116): `StageSQLSet(ref.WithExpected(old))` on the session, then on success `stagedOps++` and `recordMirrorRow(staged)`. The mirror always records the staged (coerced) value keyed by `sourceColumn`, so an in-run SQL read addresses the real generated column and the deferred UPDATE never diverges from the guarded value.

**STRUCTURAL writes (staged but NOT mirrored — visible only after commit):**
- `zone.delete()` (zones.go:224) — `StageSQLDelete`, zone_index located, zone_id guarded.
- `zone.clone(i)…append()` (zones.go:340) — `StageSQLAppend` of the whole cloned object with a fresh zone id; pending edits (`PendingZone.SetField` zones.go:315, `pendingZoneValues.SetKey` zones.go:442) only mutate the detached clone until append.
- `bulkDelete` (sql.go:974) — per-match `stageEntityDelete`.

**Consistency contract (mirror.go header + loadedsave.go:451):** a scalar set is staged on the session for the single end-of-run write AND recorded in the mirror buffer; `flushMirror` at the head of every query drains the buffer as one `UPDATE … FROM (VALUES …)` per `(table,column)`, joined on the discriminators AND `save_id = ls.saveID`. When the DB is not yet open the buffer is left intact (the lazy import picks up the in-memory write-throughs, and `openDB` flushes once idempotently after opening). Structural ops bypass the mirror entirely, so an in-run read/query still sees the pre-structural state.

## Per-save scoping (the save_id self-scoping rule)

The DuckDB handle is shared across worlds; every normalized base table holds EVERY world's working/history partitions keyed by `save_id`. Isolation is enforced two different ways depending on the surface:

1. **Raw `world.query()`** wraps the user SQL with `scopedQuery` (sql.go:108): one shadowing CTE per normalized table, each `SELECT * … WHERE save_id = '<ls.saveID>'`. DuckDB binds a CTE name ahead of the catalog table, so a bare `FROM bibites` resolves to the per-world CTE — the caller never writes a save_id filter. `ls.saveID` is single-quote-escaped for defense in depth.
2. **Collection aggregates / bulk set / match** inject the filter through `SaveScope.scopeClause` in `whereClause` (sql.go:356). Mutation/match builders always use `LoadedSave.workingScope` (sql.go:373) — `"<identity>.save_id = ?"`/[saveID] — because mutation can never reach history/all-worlds. Spanning (read-only, `mirror_saves` subquery) is a separate scope owned by slice 06.
3. **The mirror UPDATE** itself appends `… WHERE <table>.save_id = ls.saveID` (mirror.go:171), so a flush only ever touches this save's working partition.

`save.sql()` is the deliberate exception: no CTE, no scope — it sees all save_ids and is the documented escape hatch.

## Missing seams

### Material/zone-scope name lookups silently miss (reference §3)
**What's missing.** `save.settings.material("Name")` (settings_value.go:73) and the zone owner-id (zones.go:247) feed `ls.settingRow` -> the `settingsIdx` keyed on the exact `owner_id` / `setting_name` strings; a non-existent material name or a mis-cased setting name yields `found=false` (KeyError) with no enumeration of valid names. This is the same case-sensitive-silent-miss class missing.md §3 already files for entity/gene name lookups; flagged here only to note the SETTINGS/ZONES surface shares it (`materialBuiltin` does not validate the material exists, and there is no `save.settings.materials()`/scope-listing read).
**Consequence.** A typo'd material/setting name reads as "absent" rather than "unknown name", and there is no read path to discover the valid keys. **Where it lives.** settings_value.go:68-73, settings_value.go:102-111, loadedsave.go:348-359.

### Settings/zone scalar writes have no bulk/predicate form (overlaps entity-DSL slice 04 and §5)
**What's missing.** Entities get `collection.where(...).set/set_expr/delete` push-down (sql.go:535/630/974), but settings and zones expose only per-handle scalar writes (`setting.set`, `zones.go` `Zone.SetField`) and per-zone `delete()`. There is no `save.zones.where(material=="Plant").set(...)` or bulk settings set; to touch N zones a script loops in Starlark, staging N point-sets (each its own mirror row), with no single batched UPDATE and no SQL-side predicate. The unify-surfaces work (§5) covers child-collection ergonomics broadly; this is specifically the *write/bulk* asymmetry between the entity collections and the settings/zones surfaces.
**Consequence.** Bulk settings/zone edits are O(N) Starlark loops with no push-down; the analytics builder (`bulkSet`/`bulkSetExpr`) is entity-only and cannot target the settings_* / settings_zones tables. **Where it lives.** sql.go:535-678 (entity-only bulk), settings_value.go:162, zones.go:175-242.

### Zone clone-append does not reconcile zone-group membership or id references (known v2 limitation)
**What's missing.** `PendingZone.append` (zones.go:340) assigns a fresh zone id via `allocZoneID` (loadedsave.go:391) to avoid colliding with the template, but the header (zones.go:40-42) and the struct doc (loadedsave.go:86-88) state zone-group membership and other id references are NOT reconciled — the new zone is appended structurally and is invisible to in-run reads/queries (not mirrored) until commit, with no surface to add it to a zone group or fix back-references. This is analogous to the brain-graph-integrity gap but for zones; it is not the same as §1 (genes/brains in the spanning surface) or §6 (node↔synapse join).
**Consequence.** A cloned-appended zone can land orphaned from any zone group; a script cannot fix the references through the DSL and cannot observe the appended zone until after commit (no read-after-append within a run). **Where it lives.** zones.go:337-357, loadedsave.go:388-413.

### Structural ops have no in-run read-back (mirror is scalar-only) — overlaps DuckDB slice 09
**What's missing.** The mirror buffers/flushes only scalar sets (mirror.go:66/133); `StageSQLDelete`/`StageSQLAppend` (zones.go:237/351, `bulkDelete` sql.go:974) are staged but never mirrored, so `save.zones.clone(0).append()` then `save.sql("SELECT count(*) FROM settings_zones …")` still returns the pre-append count — and `save.zones` (which reads `ls.tables.SettingsZones`, zones.go:63) likewise never grows in-run because the append lives only on the session, not in `ls.tables`. There is no documented seam to mirror a structural op (it would require an in-run DuckDB INSERT/DELETE + an in-memory `ls.tables` mutation, neither of which exists). This is the consistency-contract boundary; flagged because it is a genuine read-after-write gap for the structural surface, distinct from the scalar mirror that slice 09 owns.
**Consequence.** A script that appends/deletes and then queries/iterates observes stale structure within the same run; mid-run logic branching on a freshly-appended zone or a post-delete count is impossible without committing first. **Where it lives.** mirror.go:11-25 (contract), zones.go:224-242/340-357, sql.go:966-986.
