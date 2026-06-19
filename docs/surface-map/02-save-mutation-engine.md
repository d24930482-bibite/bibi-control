# 02. Save mutation engine (sqlref + session staging + install)

**Scope (files scanned):** `savemutator/thebibites/sqlref.go`, `sqlref_generated.go`, `sqlref_catalog.go`, `sqlref_entities.go`, `sqlref_settings.go`, `sqlref_require.go`, `session.go`, `operation.go`, `path.go`, `install.go`, `json.go`, `doc.go` (all `*_test.go` and transfer*.go / workspace.go excluded).

## Location map

### Package contract / overview
- `savemutator/thebibites/doc.go:1` — [WRITE] [SAVE] — package doc: the four-step read-modify-write flow (query elsewhere → stage with locators+guards → Apply to entry JSON/raw → Commit reparses); states projections are invalid after Apply until Commit.

### SQLValueRef model + resolve entrypoint (sqlref.go)
- `savemutator/thebibites/sqlref.go:10` — [WRITE] [SAVE] — `SQLValueRef` struct: identifies one normalized SQL cell (Table/Column + entity locators: EntryName, BodyID/EggID, OwnerKind/OwnerID/Path, Setting/Scope/TargetKey/ValueType/WrapperRawJSON, many indexed `Has*` flags, ZoneID, Expected stale-value guard).
- `savemutator/thebibites/sqlref.go:58` — [WRITE] [SAVE] — `WithExpected` attaches a stale-value guard value to the ref.
- `savemutator/thebibites/sqlref.go:65` — [WRITE] [SAVE] — `Session.StageSQLSet` resolves a ref into a guarded Set op and stages it.
- `savemutator/thebibites/sqlref.go:74` — [WRITE] [SAVE] — `SQLSet` resolves ref→(Target,path), appends a `Require(path, Expected)` guard if `HasExpected`, returns a Set op.
- `savemutator/thebibites/sqlref.go:87` — [WRITE] [SAVE] — `ResolveSQLValueRef` validates Table/Column non-empty, looks up the writable table spec (else `unsupportedSQLValueRef`), delegates to `spec.resolve`.
- `savemutator/thebibites/sqlref.go:103` — [WRITE] [SAVE] — `SQLRefOp` enum (`set`/`delete`/`append`) naming the mutation a ref is resolved for.
- `savemutator/thebibites/sqlref.go:112` — [WRITE] [SAVE] — `StageSQLDelete` / `StageSQLAppend` stage resolved delete/append ops.
- `savemutator/thebibites/sqlref.go:133` — [WRITE] [SAVE] — `SQLDelete`: array-element delete (`spec.deleteArray`, with optional Require guard on the element field + `SceneCount`) OR whole-entry delete (`spec.entry`→`DeleteEntry`); refuses entry append.
- `savemutator/thebibites/sqlref.go:165` — [WRITE] [SAVE] — `SQLAppend`: array append via `spec.appendArray` (carries `SceneCount`); entry-level append explicitly rejected as "requires a cross-save workspace".
- `savemutator/thebibites/sqlref.go:187` — [WRITE] [SAVE] — `ValidateSQLRefForOp` dry-runs resolution per op kind without staging (fail-fast for query scanners).
- `savemutator/thebibites/sqlref.go:203` — [WRITE] [SAVE] — `mutableSQLRefTable` / `unsupportedSQLRefOp` helpers.

### Generated catalog (mirrors parser metadata)
- `savemutator/thebibites/sqlref_generated.go:1` — [WRITE] [SAVE] — `// Code generated … DO NOT EDIT` — generated from `go generate ./saveparser/thebibites`.
- `savemutator/thebibites/sqlref_generated.go:7` — [WRITE] [SAVE] — per-table column→archive-JSON-path maps (`bibiteColumnPaths`, `eggColumnPaths`, `pelletColumnPaths`, `pheromoneColumnPaths`, `settingsZoneColumnPaths`, etc.) and column→key/field/type maps (`brainNodeColumnKeys`, `brainSynapseColumnKeys`, `bibiteStomachContentColumnFields`, `geneValueColumnTypes`, `settingsValueColumnTypes`, `settingsChangerTargetColumns`).
- `savemutator/thebibites/sqlref_generated.go:154` — [WRITE] [SAVE] — `generatedWritableSQLRefTables`: the full writable-table allowlist, each wiring a table name + column map + `tb.SQLRefResolverKind` (kinds defined parser-side in `saveparser/thebibites/normalize_types.go:11` and stamped in `normalize_metadata.go`).

### Resolver dispatch / table specs (sqlref_catalog.go)
- `savemutator/thebibites/sqlref_catalog.go:10` — [WRITE] [SAVE] — `sqlRefResolver` / `sqlRefArrayResolver` / `sqlRefEntryResolver` function types (scalar set, array container/element, whole-entry).
- `savemutator/thebibites/sqlref_catalog.go:20` — [WRITE] [SAVE] — `sqlRefTableSpec`: table + columns + `resolve` (set) + optional `appendArray`/`deleteArray`/`entry` + `sceneCount`. Array/entry capability is **derived from the resolver kind, not a separate allowlist**.
- `savemutator/thebibites/sqlref_catalog.go:38` — [WRITE] [SAVE] — `generatedSQLRefTable`: builds the base spec then attaches array/entry resolvers per kind (brain node/synapse, stomach, pellet[+`sceneCountPellets`], settings zone, bibite/egg entry-delete).
- `savemutator/thebibites/sqlref_catalog.go:79` — [WRITE] [SAVE] — `baseGeneratedSQLRefTable`: maps each resolver kind to its scalar SET resolver; `panic` on an unknown kind (generation contract).
- `savemutator/thebibites/sqlref_catalog.go:124` — [WRITE] [SAVE] — `writableSQLRefTable` (table lookup), `writableSQLRefKeys`/`sortedSQLRefColumns` (enumerate writable `table.column`), `unsupportedSQLValueRef` error.

### Entity resolvers — bibite/egg/pellet/pheromone/brain/stomach (sqlref_entities.go)
- `savemutator/thebibites/sqlref_entities.go:12` — [WRITE] [SAVE] — `pathMapResolver`: scalar SET for simple column→path tables; column→path lookup + entity target.
- `savemutator/thebibites/sqlref_entities.go:32` — [WRITE] [SAVE] — `resolveBibiteStomachColumn`: requires `content_index`, builds `body.stomach.content[i].field`.
- `savemutator/thebibites/sqlref_entities.go:47` — [WRITE] [SAVE] — `resolveGeneColumn`: gene SET uses caller-supplied `ref.Path` verbatim (validated only as non-empty) against a bibite/egg target resolved from `owner_id`.
- `savemutator/thebibites/sqlref_entities.go:96` — [WRITE] [SAVE] — `entitySynapseAppendTarget`/`entitySynapseDeleteTarget` (and node pair at :119) — shared `brain.Synapses` / `brain.Nodes` locator core; SET/DELETE/APPEND all route through it; brain-graph-integrity caveat noted as living in the binding layer, not here.
- `savemutator/thebibites/sqlref_entities.go:142` — [WRITE] [SAVE] — `bibiteStomachAppendTarget`/`bibiteStomachDeleteTarget`: `body.stomach.content` container + indexed element.
- `savemutator/thebibites/sqlref_entities.go:182` — [WRITE] [SAVE] — `resolvePelletColumn` / `pelletAppendTarget` / `pelletDeleteTarget`: `pellets[g].pellets[i].field`; requires `entry_name`+`group_index`(+`group_pellet_index` for delete); optional `pellets[g].zone` guard from `ref.Zone`.
- `savemutator/thebibites/sqlref_entities.go:228` — [WRITE] [SAVE] — `resolvePheromoneColumn`: `pheromones[i].field` on the pheromones scene entry.
- `savemutator/thebibites/sqlref_entities.go:242` — [WRITE] [SAVE] — `entityTargetFromSQLRef` / `bibiteTargetFromSQLRef` / `eggTargetFromSQLRef`: build guarded entry targets (`body.id` guard for bibite, `egg.id` guard for egg).
- `savemutator/thebibites/sqlref_entities.go:273` — [WRITE] [SAVE] — `bibiteTargetFromGeneRef`/`eggTargetFromGeneRef`/`ownerIDAsInt`: parse `owner_id` string→int, verify `owner_kind`, then reuse the entity target builders.

### Settings & zone resolvers (sqlref_settings.go)
- `savemutator/thebibites/sqlref_settings.go:18` — [WRITE] [SAVE] — `resolveSettingsValueColumn`: heavily-guarded scalar SET for settings values; cross-checks `value_type` vs column type, `wrapper_raw_json` to decide `.Value` suffix, and reconciles `zone_index` against the path.
- `savemutator/thebibites/sqlref_settings.go:61` — [WRITE] [SAVE] — `settingsValueArchivePath`: per-table archive path builder for `settings_simulation_values`, `settings_independent_values`, `settings_material_values` (owner_id segment), `settings_zone_values` (→`zones[i].name`).
- `savemutator/thebibites/sqlref_settings.go:109` — [WRITE] [SAVE] — `settingsScopedValueArchivePath`: validates `owner_kind`/`owner_id`/`path` against the expected SQL path, returns the archive path.
- `savemutator/thebibites/sqlref_settings.go:129` — [WRITE] [SAVE] — `resolveSettingsChangerTargetColumn`: builds `settingsChangers[c].settingsBases["Zone(z).name"]` with a zone-id guard; validates `scope==zone` and `target_key` shape.
- `savemutator/thebibites/sqlref_settings.go:165` — [WRITE] [SAVE] — `settingValueUsesWrapper`: unmarshals `wrapper_raw_json` to detect a `{"Value": …}` wrapper vs a bare scalar.
- `savemutator/thebibites/sqlref_settings.go:179` — [WRITE] [SAVE] — `settingsZoneValuePathIndex` / `safeSettingsPathSegment`: parse `settings.zones[N].field` and reject segments containing `.[]`.
- `savemutator/thebibites/sqlref_settings.go:211` — [WRITE] [SAVE] — `resolveSettingsZoneColumn` / `settingsZoneAppendTarget` / `settingsZoneDeleteTarget`: zone-row SET (`zones[z].field`), append to `zones` (no zone guard since the row is new), indexed delete with zone-id guard.

### Guard / requirement helpers (sqlref_require.go)
- `savemutator/thebibites/sqlref_require.go:5` — [WRITE] [SAVE] — `requireSQLRefString`/`requireSQLRefFlag`/`requireSQLRefEqual`/`requireSQLRefValueType`: ref-field presence/equality validators with `table.column requires X` errors.
- `savemutator/thebibites/sqlref_require.go:33` — [WRITE] [SAVE] — `sqlRefColumnValue`: maps `ref.Column`→generated path/key/field/type; unknown column ⇒ "not writable".
- `savemutator/thebibites/sqlref_require.go:41` — [WRITE] [SAVE] — `zoneIDGuards`: emits a `zones[z].id == ZoneID` guard when `HasZoneID` (stale-zone protection).

### Session lifecycle / staging / Apply / Commit (session.go)
- `savemutator/thebibites/session.go:13` — [WRITE] [SAVE] — `State` enum: `clean`/`staged`/`applied`.
- `savemutator/thebibites/session.go:27` — [WRITE] [SAVE] — `Session` struct (archive + `ops` + `dirty` set + state) and `NewSession`.
- `savemutator/thebibites/session.go:45` — [READ] [SAVE] — `Archive`/`State`/`ProjectionsValid` (projections invalid only when `StateApplied`)/`StagedOperations`/`DirtyEntries` accessors.
- `savemutator/thebibites/session.go:91` — [WRITE] [SAVE] — `Stage`: validates op shape, rejects staging after Apply, deep-clones the op, sets `StateStaged`.
- `savemutator/thebibites/session.go:111` — [WRITE] [SAVE] — `StageSet`/`StageSetWithOptions`/`StageBibiteEnergy` convenience stagers.
- `savemutator/thebibites/session.go:128` — [WRITE] [SAVE] — `Apply`: atomic. Reorders element deletes, applies each op into a per-entry `entryUpdate` working copy, then re-serializes changed entries (recomputing JSON/Raw/SHA256/CRC32/sizes) and applies entry list add/remove; sets `StateApplied`. Any op error aborts before bytes change.
- `savemutator/thebibites/session.go:182` — [WRITE] [SAVE] — `Commit`/`CommitWithOptions`: Apply → `tb.WriteArchive` → `tb.ParseFile` reparse → swap in the fresh archive and reset to `StateClean`.
- `savemutator/thebibites/session.go:218` — [WRITE] [SAVE] — `applyOperation`: validates target guards, dispatches Set/Append/Delete; Append/Delete trigger `adjustSceneCount` when `SceneCount` set.
- `savemutator/thebibites/session.go:251` — [WRITE] [SAVE] — `entryUpdate`: fetch-or-create a cloned working copy of an entry's JSON; checks entry exists, kind matches, JSON decoded.
- `savemutator/thebibites/session.go:275` — [WRITE] [SAVE] — `validateTargetGuards`: reads each guard path and compares via `jsonValuesEqual`; missing/mismatch aborts (the stale-value guard enforcement point).
- `savemutator/thebibites/session.go:291` — [WRITE] [SAVE] — `StageAppend`/`StageDelete`/`StageAppendPellet`/`StageDeletePellet` (pellets reconcile `nPellets`).
- `savemutator/thebibites/session.go:320` — [WRITE] [SAVE] — `StageDeleteBibite`(+WithOptions)/`StageAppendBibite`: whole-entry stagers.
- `savemutator/thebibites/session.go:343` — [WRITE] [SAVE] — `applyEntryOperation`→`applyDeleteEntry`/`applyAppendEntry`: entry-level structural mutations.
- `savemutator/thebibites/session.go:354` — [WRITE] [SAVE] — `applyDeleteEntry`: guard-check, refuse/prune parent-child links, decrement `nBibites`, drop species id from `activeSpeciesList` if last member, queue removal.
- `savemutator/thebibites/session.go:405` — [WRITE] [SAVE] — `applyAppendEntry`: reject duplicate name, verify `ClassifyEntry` matches kind, encode new entry bytes/hashes, bump `nBibites`.
- `savemutator/thebibites/session.go:443` — [WRITE] [SAVE] — `applyEntryListChanges`: rebuild `archive.Entries` minus removed plus added; mark dirty.
- `savemutator/thebibites/session.go:466` — [WRITE] [SAVE] — `adjustSceneCount`: clamp-at-zero integer delta to a scene field (`scene.bb8scene`); missing field is a silent no-op.
- `savemutator/thebibites/session.go:489` — [READ] [SAVE] — `entriesReferencingChild`/`pruneChildRef`/`childIndexOf`/`bibiteBodyID`: parent-link cascade helpers reading `body.eggLayer.children`.
- `savemutator/thebibites/session.go:542` — [READ] [SAVE] — `entitySpeciesID`/`speciesHasOtherMembers`/`removeActiveSpecies`/`activeSpeciesIndexOf`: species-cascade helpers; species reconciliation degrades quietly (no parser validation) on missing paths.
- `savemutator/thebibites/session.go:622` — [WRITE] [SAVE] — `cloneOperation`: deep-copies guards + payload JSON so the session owns staged state.
- `savemutator/thebibites/session.go:645` — [WRITE] [SAVE] — `orderElementDeletes`: slot-preserving reorder so same-array element deletes apply in descending index order (commit-correctness for sibling deletes); `deleteArrayKey` (:696) extracts parent-array + trailing index.

### Operation model (operation.go)
- `savemutator/thebibites/operation.go:9` — [WRITE] [SAVE] — `OperationKind` enum: `set`, `append`, `delete` (array element), `delete_entry`, `append_entry`.
- `savemutator/thebibites/operation.go:29` — [WRITE] [SAVE] — well-known archive entry name constants (`settings.bb8settings`, `speciesData.json`, `scene.bb8scene`, `vars.bb8scene`, `pellets.bb8scene`, `pheromones.bb8scene`).
- `savemutator/thebibites/operation.go:40` — [WRITE] [SAVE] — `Target`(EntryName/Kind/Guards), `Guard`(Path/Equal), `Operation`(Kind/Target/Path/Value/SetOptions/DeleteOptions/EntryPayload/SceneCount), `SetOptions.CreateMissing`, `DeleteOptions.PruneParentLinks`, `EntryPayload`, `BibiteRef`.
- `savemutator/thebibites/operation.go:99` — [WRITE] [SAVE] — `Require` + target constructors (`EntryTarget`, `SettingsTarget`, `SpeciesTarget`, `SceneTarget`, `VarsTarget`, `PelletsTarget`, `PheromonesTarget`, `BibiteTarget`).
- `savemutator/thebibites/operation.go:147` — [WRITE] [SAVE] — `Set`/`SetWithOptions`/`Append`/`Delete`/`DeleteEntry`(+WithOptions)/`AppendEntry` op constructors.
- `savemutator/thebibites/operation.go:206` — [WRITE] [SAVE] — `validateOperationShape`: per-kind validation (guard-path parseable; set/append/delete need entry+path; delete path must end in index; delete_entry/append_entry require bibite/egg kind and (append) JSON).

### JSON path engine (path.go)
- `savemutator/thebibites/path.go:16` — [READ] [SAVE] — `parsePath`/`parseQuotedKey`: dotted+bracket path parser supporting `a.b[0]` and quoted keys `["Zone(0).x"]`; rejects empty/trailing-dot/bad-index.
- `savemutator/thebibites/path.go:118` — [READ] [SAVE] — `getJSONPath`: read a value at a path (used by guards and cascade lookups); out-of-range/missing key returns `(nil,false,nil)`.
- `savemutator/thebibites/path.go:151` — [WRITE] [SAVE] — `setJSONPath`: in-place scalar set; final missing key allowed only with `CreateMissing`; intermediate creation deliberately unimplemented.
- `savemutator/thebibites/path.go:199` — [WRITE] [SAVE] — `containerRef`/`resolveContainer`: locate a parent slot so a grown/shrunk slice can be written back (slices reallocate).
- `savemutator/thebibites/path.go:279` — [WRITE] [SAVE] — `appendJSONArray`/`deleteJSONArrayElement`: array append/element-delete with parent write-back; `renderPath` (:335) reconstructs path strings for errors.

### JSON encode / clone / numeric guard equality (json.go)
- `savemutator/thebibites/json.go:12` — [WRITE] [SAVE] — `encodeJSON`: marshal entry JSON, re-prepend UTF-8 BOM when the source entry had one (`HasUTF8BOM`).
- `savemutator/thebibites/json.go:26` — [WRITE] [SAVE] — `cloneJSON`: deep-copy maps/slices for the working entry copy.
- `savemutator/thebibites/json.go:45` — [WRITE] [SAVE] — `normalizeJSONValue`: coerce typed Go slices (`[]string`/`[]int`/…) to `[]any` so set/append values match parser-decoded shapes.
- `savemutator/thebibites/json.go:94` — [READ] [SAVE] — `jsonNumberToInt64`: int coercion for id/count comparisons.
- `savemutator/thebibites/json.go:115` — [READ] [SAVE] — `jsonValuesEqual`/`numberRat`/`normalizeComparableScalar`: rational-exact numeric guard comparison (`json.Number` vs Go numerics) — the heart of stale-value guard correctness across numeric representations.

### Local install (install.go)
- `savemutator/thebibites/install.go:21` — [WRITE] [SAVE] — `DefaultSavefilesDir`: OS-specific Bibites Savefiles path, overridable via `BIBITES_SAVEFILES_DIR`.
- `savemutator/thebibites/install.go:50` — [WRITE] [SAVE] — `InstallSaveFile`/`InstallSaveFileToDir`: atomic copy of a committed save into the game's Savefiles dir (temp-file + rename); `validateSaveFileName` (:123) enforces `.zip`, no separators.

## Read paths

Within this slice "reading" is only what mutation needs: `getJSONPath`/`parsePath` (`path.go:118`) walk the decoded entry JSON to read guard values and to power the delete cascades (`childIndexOf`, `activeSpeciesIndexOf`, `bibiteBodyID`, `entitySpeciesID` in `session.go`), and `jsonValuesEqual` (`json.go:115`) compares them exactly. The actual query/projection surface that produces a `SQLValueRef` lives in the parser/DuckDB slices, not here; this package consumes already-queried locators. `Session` accessors (`session.go:45`) expose state for callers, and crucially `ProjectionsValid` warns that after `Apply` the parser projections on the in-memory archive are stale until `Commit` reparses.

## Mutation paths

Two staging surfaces converge on one `Operation` model. The high-level path takes a `SQLValueRef` (one normalized cell), looks the table up in the generated allowlist (`sqlref_generated.go:154`), and runs the kind-specific resolver (`sqlref_catalog.go`, `sqlref_entities.go`, `sqlref_settings.go`) to produce a `Target` + JSON path; `SQLSet`/`SQLDelete`/`SQLAppend` (`sqlref.go`) wrap that in a Set/Delete/Append op, optionally attaching a `Require` stale-value guard from `WithExpected`. The low-level path uses `Operation` constructors (`operation.go`) directly. `Session.Stage` (`session.go:91`) validates op shape and clones it; `Apply` (`session.go:128`) reorders sibling element deletes, runs every op against per-entry cloned working copies while enforcing target guards, then re-serializes changed entries (JSON/Raw/SHA256/CRC32/sizes, BOM-aware) and add/removes entry-list rows — atomic, all-or-nothing. Mutation **kinds**: scalar `set` and array `append`/`delete` are *mirrored* edits into existing JSON; `delete_entry`/`append_entry` are *structural* (add/remove whole bibite/egg entries) and carry side-effect cascades: scene-count reconciliation (`nBibites`/`nPellets`), parent-child link pruning, and `activeSpeciesList` upkeep. `Commit` (`session.go:182`) writes the archive and reparses to restore valid projections.

## Missing seams

### Gene SET trusts caller-supplied path verbatim
**What's missing.** `resolveGeneColumn` (`sqlref_entities.go:47`) validates only that `ref.Column` is a known type and `ref.Path` is non-empty; it then writes to `ref.Path` directly, with no `safeSettingsPathSegment`-style sanitization (unlike settings values). The numeric/bool/string `value_type` column is looked up but never checked against the value actually being set.
**Consequence.** A caller (or a malformed query row) can steer a gene SET to any JSON path within the bibite/egg entry, and can write a string into a `number_value` cell. The guard model assumes upstream produced a faithful `Path`; nothing here defends it.
**Where it lives.** `savemutator/thebibites/sqlref_entities.go:47`.

### Append/insert cannot create missing intermediate structure
**What's missing.** `setJSONPath` refuses to create intermediate objects (`path.go:188`, comment "Intermediate creation is intentionally not implemented yet"), and `appendJSONArray`/`deleteJSONArrayElement` error if the container array is absent (`path.go:289`, `:322`). There is no "ensure array exists then append" path.
**Consequence.** Appending the first pellet to a group that has no `pellets` array, or setting a field on a partially-populated entity, fails rather than materializing the container — pushing the burden onto callers/templates to pre-shape JSON.
**Where it lives.** `savemutator/thebibites/path.go:151`, `:279`.

### Brain edits have no graph-integrity validation
**What's missing.** Node/synapse append and delete (`sqlref_entities.go:96`–`136`) only locate `brain.Nodes[i]` / `brain.Synapses[i]`. Deleting a node does not check or fix synapses referencing it by `NodeIn`/`NodeOut`, and appending a synapse does not validate that its endpoints exist. The code comment explicitly defers the caveat "to the binding layer, not here."
**Consequence.** A staged brain edit can leave dangling synapse endpoints or orphaned nodes; unlike bibite-entry delete (which prunes child links and species), structural brain edits have no cascade. The "binding layer" that is supposed to guard this is outside this slice.
**Where it lives.** `savemutator/thebibites/sqlref_entities.go:119` (and synapse pair at `:96`).

### Species / scene cascades degrade silently on missing paths
**What's missing.** `removeActiveSpecies` (`session.go:583`) and `adjustSceneCount` (`session.go:466`) are explicit no-ops when the species entry, `activeSpeciesList`, or the scene count field is absent — these reconciliations are *not* parser-validated.
**Consequence.** If the save's species/scene shape drifts (a real risk per the save-format-churn memo), counts like `nBibites`/`nPellets` and the active-species list can silently desync from the actual entry set after a delete/append, with no error surfaced. This overlaps the **parser** slice (entry decode/`ClassifyEntry`) and the **settings/zones** slice (`scene.bb8scene` schema).
**Where it lives.** `savemutator/thebibites/session.go:466`, `:583`.

### Same-batch cascade reads use on-disk JSON, not pending edits
**What's missing.** `entriesReferencingChild` and `speciesHasOtherMembers` (`session.go:491`, `:556`) read `entry.JSON` directly (the comment flags that "a same-batch genes.speciesID reassignment is not considered"), while delete edits accumulate in the separate `updates` working-copy map.
**Consequence.** A single Apply that both reassigns a species id and deletes the last old-species member can wrongly keep/drop an `activeSpeciesList` id, because the membership scan never sees the staged reassignment. Cascade correctness assumes deletes and field reassignments are not mixed in one batch.
**Where it lives.** `savemutator/thebibites/session.go:491`, `:556`.

### Entry-level append requires the cross-save workspace (out of this slice)
**What's missing.** `SQLAppend` for an entry-backed table errors with "requires a cross-save workspace and is not supported in a single-save session" (`sqlref.go:179`). `StageAppendBibite` exists (`session.go:332`) but needs a fully-formed `EntryPayload` (name + classified kind + JSON) that this slice never constructs — id remapping and payload sourcing live in transfer.go / workspace.go.
**Consequence.** Adding a whole new bibite/egg is only reachable through the **transfer/workspace** slices; the SAVE-scale ref API deliberately can't synthesize one. Overlaps the transfer slice (id-space remap per the bibites-id-spaces memo) and workspace slice.
**Where it lives.** `savemutator/thebibites/sqlref.go:165`, `savemutator/thebibites/session.go:331`.
