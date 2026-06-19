# 01. Save parsing → normalized tables (read foundation)

**Scope (files scanned):**
`saveparser/thebibites/parse_archive.go`, `parse_entry.go`, `parse_payload.go`,
`parse_scene.go`, `parse_entities.go`, `parse_brain.go`, `parse_species.go`,
`parse_environment.go`, `parse_settings.go`, `parse_parallel.go`,
`entry_kind.go`, `json_helpers.go`, `archive.go`, `archive_counts.go`,
`normalize.go`, `normalize_types.go`, `normalize_metadata.go`,
`sqlref_populate.go`, `write_archive.go`. (Skipped `*_test.go`.)

## Location map

### Archive read / unzip (raw bytes → Entry structs)
- `parse_archive.go:13` — [READ][SAVE] `ParseFile(path, *Options)` — entry point: stat + size guard, hash whole file, open zip, per-entry guards, build `*Archive`, then `parseEntries()` + `recomputeCounts()`.
- `parse_archive.go:85` — [READ][SAVE] `readZipEntry` — copies one zip entry into an `Entry` (zip metadata + SHA256 + `Raw` bytes), with size/CRC verification; directory entries skipped.
- `parse_archive.go:134` — [READ][SAVE] `hashFile` — SHA256 of the whole archive file.
- `archive.go:31` `Options` / `archive.go:38` `DefaultOptions` — [READ][SAVE] zip-bomb limits (512MB archive, 10k entries, 128MB/entry, 1GB total uncompressed).
- `entry_kind.go:14` — [READ][SAVE] `ClassifyEntry(name)` — maps a zip entry name to an `EntryKind` by exact filename / regex (`bibites/bibite_N.bb8`, `eggs/egg_N.bb8`) / extension fallback.
- `entry_kind.go:48` `isJSONKind` / `entry_kind.go:57` `isJSONLikeName` — [READ][SAVE] which kinds/extensions are decoded as JSON (`.bb8/.bb8scene/.bb8settings/.json`).
- `entry_kind.go:66` `validateEntryName` — [READ][SAVE] zip-slip / traversal / absolute-path / NUL guards (also reused by the writer).

### JSON decode + scalar helpers
- `json_helpers.go:22` — [READ][SAVE] `decodeJSONWithBOM` — strips UTF-8 BOM, decodes to `any` with `UseNumber()`, **rejects trailing tokens** (strict EOF). Uses `goccy/go-json` aliased as `json`.
- `json_helpers.go:44`–`92` — [READ][SAVE] `asMap/mapAt/listAt/stringAt/boolAt/floatAt/intAt` — typed lookups into decoded `map[string]any`.
- `json_helpers.go:94` `toFloat` / `:114` `toInt` — [READ][SAVE] number coercion across `json.Number`/float/int kinds.
- `json_helpers.go:141` `scalarParts` — [READ][SAVE] turns any value into `(ScalarType, number, string, bool, rawJSON)` — the basis of every EAV-style value row (settings/genes/changer targets).
- `json_helpers.go:163` `rawJSON` — [READ][SAVE] canonical `json.Marshal` of a value (used to stash unparsed subtrees as text columns).
- `json_helpers.go:174` `ownerIDFromInt` — [READ][SAVE] renders a numeric owner id to a stable `owner_id` string, else falls back to entry name/index.

### Payload dispatch (Entry → typed parser)
- `parse_entry.go:9` `ParseEntryBytes` / `:13` `ParseBytesAs` / `:21` `ParseEntryBytesAs` — [READ][SAVE] parse a single in-memory payload (no zip); used for one-entry parsing (e.g. WORLD/transfer callers).
- `parse_entry.go:57` `defaultEntryName` — [READ][SAVE] canonical filename per kind.
- `parse_payload.go:30` `parseEntryPayload` — [READ][SAVE] **central switch**: decode JSON if JSON-kind, then dispatch by `EntryKind` to the typed parser; collects `Diagnostic`s in a per-call `parserContext` (pure per entry).
- `parse_payload.go:73` `applyParseResult` — [READ][SAVE] merges one entry's `parseResult` into the `Archive` (sets singletons; **appends** bibites/eggs/pheromones/diagnostics in entry order).
- `parse_payload.go:109` `parseGenericJSON` — [READ][SAVE] captures `vars` / unknown JSON sections; for `unknown_json` emits a **loud `unknown_json_section` Diagnostic** (drift signal) but stores no typed data.
- `parse_parallel.go:26` `parseEntries` — [READ][SAVE] runs `parseEntryPayload` per entry (sequential under threshold of 2, else GOMAXPROCS worker pool over a `results[]`), then applies **strictly in index order** for a deterministic Archive.
- `parse_parallel.go:94` `presizeFromResults` / `:123` `growCap` — [READ][SAVE] pre-sizes append-target slices to cut GC churn on big saves.

### Typed entity parsers (JSON → parser-internal structs)
- `parse_scene.go:3` `parseScene` — [READ][SAVE] `scene.bb8scene` → `SceneState` (version, nBibites, nPellets, simulatedTime; keeps `Raw`).
- `parse_entities.go:3` `parseBibite` — [READ][SAVE] `bibite_*.bb8` → `Bibite` (body.id, dead/dying, stomach, children, brain); warns on missing `body.id`.
- `parse_entities.go:43` `parseStomachContents` — [READ][SAVE] `body.stomach.content[]` → `[]StomachContent`.
- `parse_entities.go:79` `parseChildLinks` — [READ][SAVE] `body.eggLayer.children[]` → `[]ChildLink` (parent→child body ids).
- `parse_entities.go:103` `parseEgg` — [READ][SAVE] `egg_*.bb8` → `Egg` (egg.id + brain).
- `parse_entities.go:124` `parseEntityBrain` — [READ][SAVE] shared `brain.Nodes` / `brain.Synapses` extraction for bibites & eggs.
- `parse_brain.go:3` `parseBrainNodes` / `:52` `parseBrainSynapses` — [READ][SAVE] brain net nodes (Type/TypeName/Index/Inov/Desc/activation/value) and synapses (Inov/NodeIn/NodeOut/Weight/En); reused by species templates.
- `parse_species.go:5` `parseSpecies` — [READ][SAVE] `speciesData.json` → `SpeciesData` (active id list + records).
- `parse_species.go:20` `parseActiveSpeciesIDs` — [READ][SAVE] `activeSpeciesList[]`.
- `parse_species.go:34` `parseSpeciesRecords` — [READ][SAVE] `recordedSpecies[]` → `[]SpeciesRecord` (speciesID/parentID/names/template).
- `parse_species.go:82` `parseSpeciesTemplate` — [READ][SAVE] `template.nodes/synapses` → template brain nets (owner kind `species_template`).
- `parse_environment.go:3` `parsePellets` — [READ][SAVE] `pellets.bb8scene` → `PelletData` (groups + flattened pellets w/ global index).
- `parse_environment.go:28` `parsePelletGroup` / `:58` `parsePellet` — [READ][SAVE] per-zone pellet groups and individual pellets (flags `matterDecay`).
- `parse_environment.go:77` `parsePheromones` — [READ][SAVE] `pheromones.bb8scene` → `[]Pheromone` (keeps `phero.heading` as raw JSON).
- `parse_settings.go:33` `parseSettings` — [READ][SAVE] `settings.bb8settings` → `SettingsState` (sim values, independents, materials, zones, zone groups, bibite spawners, settings changers).
- `parse_settings.go:55` `parseSettingsValues` / `:79` `parseSettingValue` — [READ][SAVE] generic EAV flattening of a settings object (skips structural keys; unwraps `{Value:…}` wrappers; keeps `WrapperRawJSON`).
- `parse_settings.go:112` `parseSettingsMaterials` / `:138` `parseSettingsZones` / `:165` `parseSettingsZoneGeometry` / `:230` `parseSettingsZoneGroups` / `:253` `parseSettingsBibiteSpawners` / `:297` `parseSettingsChangers` (+ `parseSettingsChangerPoints`, `parseSettingsChangerTargets`) — [READ][SAVE] sub-table extraction; changer targets resolve `Zone(N).path` keys back to zone ids via `settingsChangerZoneTargetRE` (`parse_settings.go:10`).

### Counts & diagnostics
- `archive_counts.go:5` `recomputeCounts` — [READ][SAVE] derives `DerivedCounts` (file counts, parsed counts, scene-reported counts).
- `archive_counts.go:43` `countBibiteStates` — [READ][SAVE] alive/dead/dying + unique body-id counts (uses `Bibite.Dead/Dying`).
- `archive_counts.go:62` `addCountDiagnostics` — [READ][SAVE] **drift signal**: warns when `scene.nBibites/nPellets` disagree with parsed counts.

### Parser-internal struct definitions
- `archive.go:47` `Archive` — [READ][SAVE] the parsed save (entries + typed singletons/slices + counts + diagnostics).
- `archive.go:104` `Entry` — [READ][SAVE] one zip entry incl. `Raw []byte` and decoded `JSON any`.
- `archive.go:128` `Diagnostic` / `:135` `DerivedCounts` / `:160` `ScalarType` — [READ][SAVE] supporting types.
- `archive.go:169`–`433` — [READ][SAVE] parser-internal entity structs: `SceneState`, `SettingsState` (+ value/material/zone/geometry/group/spawner/changer/point/target), `SpeciesData`/`SpeciesRecord`, `Bibite`, `Egg`, `StomachContent`, `ChildLink`, `BrainNode`, `BrainSynapse`, `PelletData`/`PelletGroup`/`Pellet`, `Pheromone`. Note `archive.go:314` comment: flat bibite scalars are **no longer mirrored** here — normalized rows read them straight from `Raw` via sqlref metadata.
- `archive.go:85` `Archive.Entry(name)` / `:94` `EntriesByKind` — [READ][SAVE] lookups over parsed entries.

### Struct → table normalization (the row/table mapping)
- `normalize.go:8` `ExtractTables(saveID, *Archive)` — [READ][SAVE] **top of the struct→table mapping**: builds `ExtractedSave` (archive/entries/diagnostics rows) then calls the per-domain normalizers below.
- `normalize.go:52` `normalizeScene` — [READ][SAVE] → `scenes`, `vars`, `scene_color_selectors`, `scene_phero_towers`, `scene_rad_towers`.
- `normalize.go:115` `normalizeSettings` (+ `appendSettingValueRows` `:229`) — [READ][SAVE] → `settings_*` rows (simulation/independent/material/zone values, zones, geometry, groups, spawners, changers/points/targets). Zone scalars filled via `populateSQLRefFields(...,"settings_zones")`.
- `normalize.go:250` `normalizeSpecies` — [READ][SAVE] → `active_species`, `species`, `species_genes` (from `template.genes`), `species_brain_nodes/synapses`.
- `normalize.go:290` `normalizeBibites` — [READ][SAVE] → `bibites` (+ `populateSQLRefFields`), `bibite_genes`, body sub-tables, `bibite_stomach_contents`, `bibite_children`, `bibite_brain_nodes/synapses`.
- `normalize.go:334` `appendBibiteBodyRows` — [READ][SAVE] → `bibite_body`/`bibite_mouth`/`bibite_pheromone_emitters`/`bibite_egg_layers`/`bibite_control`, each populated by sqlref path against the bibite Raw.
- `normalize.go:361` `normalizeEggs` — [READ][SAVE] → `eggs`, `egg_genes`, `egg_brain_nodes/synapses`.
- `normalize.go:380` `normalizeEnvironment` — [READ][SAVE] → `pellet_groups`, `pellets` (+ sqlref), `pheromones` (+ sqlref).
- `normalize.go:417`–`499` `appendGeneRowsFromEntityGenes` / `appendGeneRowsFromMap` / `appendGeneRow` / `appendBrainNodeRows` / `appendBrainSynapseRows` / `sortedKeys` — [READ][SAVE] shared row builders for genes (EAV) and brain rows.
- `normalize_types.go:27` `ExtractedSave` — [READ][SAVE] the bag of `[]…Row` slices, each tagged `dbtable:"…"` and (for mutable tables) `sqlrefresolver:"…"`. **This struct enumerates every normalized table.**
- `normalize_types.go:79`–`490` — [READ][SAVE] all `…Row` structs. Mutable scalar columns carry `sqlref:"json.path"` tags (e.g. `BibiteRow.Health sqlref:"body.health"`, `transform.position[0]`); EAV value columns carry `sqlrefvalue:"number|string|bool"`.
- `normalize_types.go:9` `SQLRefResolverKind` constants — [READ][SAVE→WRITE seam] the small "resolver shape" vocabulary that ties a table back to a mutation target; **semantics owned by the mutator slice (02)**, not here.

### Generated metadata (single source of JSON↔column truth)
- `normalize_metadata.go:5` `NormalizedTableSpec` / `:14` `NormalizedFieldSpec` (+ `SQLRefPath`) — [READ][SAVE] generated schema descriptors.
- `normalize_metadata.go:24` `var NormalizedTables` — [READ][SAVE] **43 table specs** (DO NOT EDIT; generated by `//go:generate` → `cmd/gen_thebibites_schema`, `normalize_types.go:3`). Each field carries SQL column name, SQL type, and the entity-relative `SQLRefPath`.
- `sqlref_populate.go:18` `populateSQLRefFields(rowPtr, raw, table)` — [READ][SAVE] reflection-fills every sqlref-tagged row field from the entity Raw using `NormalizedTables` paths — the **one** place flat scalars are read (replaces hand-written extraction).
- `sqlref_populate.go:57` `normalizedTableByName` — [READ][SAVE] linear lookup of a table spec by name.
- `sqlref_populate.go:70` `lookupJSONPath` — [READ][SAVE] resolves `a.b.c[0]` dotted/indexed paths against decoded JSON; missing/mistyped → zero value.

### Save write-back (the only WRITE in this slice)
- `write_archive.go:23` `WriteArchive(path, *Archive)` — [WRITE][SAVE] atomic temp-file + rename; re-emits the zip from `Entry.Raw`, preserving order/names/method/comments/timestamps.
- `write_archive.go:57` `WriteArchiveTo` / `:85` `validateWritableEntry` / `:109` `writeArchiveEntry` / `:156` `encodeZipPayload` / `:184` `versionOrDefault` — [WRITE][SAVE] zip re-encode helpers (Store/Deflate only; uses `validateEntryName` again). **Writes raw bytes only — it never serializes normalized rows back to JSON.**

## Read paths
`ParseFile` (`parse_archive.go:13`) opens the save zip under bomb-limits, copies each entry's raw bytes + zip metadata into `Entry`s (`readZipEntry`), and classifies each by name (`ClassifyEntry`). `parseEntries` (`parse_parallel.go:26`) then runs `parseEntryPayload` per entry — possibly in parallel, but always applied to the `Archive` in deterministic index order — which decodes JSON (BOM-aware, strict-EOF, `UseNumber`) and dispatches by `EntryKind` to a typed parser that fills parser-internal structs while keeping each entity's decoded `Raw` map. `ExtractTables` (`normalize.go:8`) is the second stage: it walks those structs and emits the flat `…Row` slices of `ExtractedSave`, with flat scalar columns filled by reflection from each entity's `Raw` via the generated `SQLRefPath` metadata (`populateSQLRefFields` + `lookupJSONPath`). The result is ~43 normalized tables spanning entries/diagnostics, scene/vars, settings (values + zones/materials/changers), species + genes + brain nets, bibites/eggs (+ body sub-tables, genes, brains, stomach, children), and the environment (pellet groups/pellets, pheromones).

## Mutation paths
Almost entirely a read-only slice. The single write is `WriteArchive`/`WriteArchiveTo` (`write_archive.go:23`/`:57`), which serializes an `Archive` back to a `.zip` save **from each `Entry.Raw` byte slice** — it preserves entry order, names, compression method, comments and timestamps, and does **not** project normalized rows back to JSON. So any actual data mutation must happen upstream by editing `Entry.Raw` (or `Entry.JSON`); the normalized tables and the `sqlref`/`sqlrefresolver` tags here are only the *declarations* a separate mutation engine (slice 02) consumes — the resolver semantics live there, not in this slice.

## Missing seams

### Normalized-row edits never flow back to the save
**What's missing.** The read path is one-directional: `populateSQLRefFields` reads `Raw → row`, but there is no inverse in this slice that writes `row → Raw`. `WriteArchive` re-emits only `Entry.Raw` bytes (`write_archive.go:110`), so editing a normalized column (e.g. `bibites.health`) has zero effect on a written save unless something outside this slice mutates `Entry.Raw` first.
**Consequence.** A user manipulating tables via the DSL has no in-slice mechanism to persist; the sqlref tags advertise a write target (`normalize_types.go` `sqlref:`/`sqlrefresolver:`) that this slice cannot honor — write is forced into the mutation-engine escape hatch.
**Where it lives.** `sqlref_populate.go:18` (read-only fill), `write_archive.go:110` (raw-only emit), tag declarations at `normalize_types.go:57`–`76`. *Likely overlaps the mutation-engine slice (02).*

### Unknown / drifted JSON sections are dropped, not captured
**What's missing.** When a save grows a new or renamed top-level JSON section, `parseGenericJSON` emits only a warning Diagnostic and stores nothing typed (`parse_payload.go:109`); since H4 dropped the json_scalars EAV, there is no table that retains an unknown section's data. Likewise any field without a `sqlref` tag (or a path that drifted) is silently left at zero by `lookupJSONPath` returning `false` (`sqlref_populate.go:90`).
**Consequence.** Save-format churn degrades to invisible data loss for un-modeled sections — recoverable only by re-reading `Entry.Raw`/`Entry.JSON` as a raw escape hatch. The `scene_nbibites/npellets_mismatch` diagnostics (`archive_counts.go:62`) are the only structural drift alarms, and they cover just two counts.
**Where it lives.** `parse_payload.go:109`, `sqlref_populate.go:70`, `archive_counts.go:62`.

### No save-format version gate / per-version schema
**What's missing.** `SceneState.Version` and `SpeciesRecord.TemplateVersion` are parsed and stored (`parse_scene.go:13`, `parse_species.go:87`) but nothing in this slice branches on them — parsing is version-agnostic and best-effort (`floatAt`/`intAt` just return `false` on shape changes). There is no notion of "this save is format vX, use mapping vX".
**Consequence.** Cross-version drift is handled only by loud-but-localized diagnostics; there is no way to refuse, warn-by-version, or select an alternate field map when The Bibites changes its layout. Aligns with the documented save-format-churn strategy (harden, don't rewrite) but leaves version-aware behavior absent.
**Where it lives.** `parse_scene.go:13`, `parse_species.go:87` (version captured), with no consumer in `parse_payload.go` / `normalize.go`.

### Entity identity / cross-entity links are not foreign-keyed in this slice
**What's missing.** Owner ids are stringified (`ownerIDFromInt`, `json_helpers.go:174`) and `body.id` may be absent (warned at `parse_entities.go:21`); `bibite_children` stores raw parent/child body ids (`normalize.go:319`) and species rows store `parentID` with no enforced linkage. There is no remap/validation of ids across worlds here — that's left to transfer.
**Consequence.** Reads expose ids as plain columns; any cross-world identity work (the per-world speciesID remap noted in memory) must be done downstream. Within a save, an entity with a missing/duplicate body.id yields ambiguous joins with no in-slice guard beyond a warning.
**Where it lives.** `json_helpers.go:174`, `parse_entities.go:13`/`:21`, `normalize.go:262`/`:319`. *Likely overlaps the cross-save transfer slice (03).*
