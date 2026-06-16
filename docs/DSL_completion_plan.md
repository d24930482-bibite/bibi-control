# Plan: Completing the Bibites DSL (settings, genes, zones, pellets, aliases)

> **Status:** P1 resolved 2026-06-16; P2/P3 not yet implemented.
> **Follows:** `docs/DSL_tickets_plan.md` (v1 language core). This plan closes the gaps
> between that v1 and the intended full feature set.

## Context

The Bibites DSL (a Starlark binding layer over a parsed save, in `script/thebibites/`)
currently ships a solid core: load-once-per-run `LoadedSave` (no god object), entity
enumeration, scalar bibite writes (`b.energy = x`), brain/stomach append+delete,
bulk `where().set()`, DuckDB push-down analytics, and a data-driven friendly-attribute
registry. But measured against the intended full feature set, five capabilities are
missing **at the DSL surface only** — and that is the key finding of this review:

> **The mutator layer (`savemutator/thebibites`) already fully supports settings writes,
> zone set/append/delete, pellet set/append/delete, and gene-value writes** via generated
> SQL-ref resolvers (`sqlref_catalog.go`, `sqlref_settings.go`, `sqlref_entities.go`).
> The generated metadata (`saveparser/thebibites/normalize_types.go`) already carries every
> locator field these need. What is absent is the Starlark *binding* surface (`save.settings`,
> `save.zones`, `save.pellets`, gene writes) and one alias-mechanism fix.

This means the work is overwhelmingly low-risk binding code that mirrors existing patterns
(`gene.go`, `subcollection.go`, `entity.go`), **not** new parser/mutator/codegen work. This
contradicts the `docs/DSL_tickets_plan.md` framing, which defers zones/pellets to "v2"; that
deferral reflected scope-cutting for the original PR, not a technical barrier.

### Status vs. the 8 intended features
| # | Feature | Mutator | DSL surface | Action |
|---|---------|---------|-------------|--------|
| 1 | Mutate settings (aliased) | ✅ `settings_value` resolver | ✅ `settings_value.go` (P1) | **Done (P1)** |
| 2 | Load save as variable, no god object | ✅ | ✅ `LoadedSave` | none |
| 3 | Mutate zones + append/delete | ✅ `settings_zone_path_map` | ❌ | **Build (P2)** |
| 4 | Mutate bibites: scalar/pos/health/energy | ✅ | ✅ | none |
| 4 | …brain synapses/nodes, stomach | ✅ | ✅ `subcollection.go` | none |
| 4 | …gene **values** | ✅ `bibite/egg_gene_value` | ✅ `gene.go` R/W (P1) | **Done (P1)** |
| 5 | Query settings (get) | ✅ in-memory rows + DuckDB | ✅ `settings_value.go` (P1) | **Done (P1)** |
| 5 | Query bibites + push-down | ✅ | ✅ `sql.go`/`collection.go` | none |
| 6 | Mutate + delete pellets | ✅ `pellet_path_map` | ❌ | **Build (P2)** |
| 7 | Aliases for all of the above | ✅ registry | ✅ fixed (`sourceColumn`, P1) | **Done (P1)** |
| 8 | Cross-save query→insert | seam `workspace.go` | ❌ | **Documented stub (P3)** |

## Decisions
- **Scope:** close all gaps, phased — P1 settings R/W + gene writes + alias fix; P2 zones + pellets; P3 cross-save stub.
- **Create-new (append):** zones via **clone** (deep-nested); pellets via **explicit fields** (flat, like the existing stomach/synapse append).
- **In-run consistency:** **mirror everything** — settings/gene/zone/pellet scalar writes must be visible to in-run `save.sql()` (extend the mirror buffer to name-keyed sub-rows), matching today's entity-scalar behavior.

---

## Review: code-quality gaps & risks (address as noted)

1. **Writable/queryable aliases are broken** *(fix in P1).* `buildRegistry` (`attr_registry.go:128-133`) rewrites `spec.column` to the alias, but `spec.column` is also used as the real DuckDB/mutator column in `entity.go:126` (`ref.Column`), `recordMirror` (`entity.go:131`), `bulkSet` (`sql.go:381`), and `resolveColumn` (`sql.go:132`). An alias on a *writable* field (e.g. `position_x → transform_position_x`) would set `ref.Column = "position_x"`, which `sqlRefColumnValue` then fails to resolve. Dormant only because `overrides` is empty; the authors flagged it in a NOTE at `sql.go:125`.
   **Fix:** split `attrSpec` into a friendly name (registry key + display) and a `sourceColumn` (the generated DuckDB/sqlref column). Use `sourceColumn` for all SQL/mutator/mirror/guard keying; use the friendly name only as the map key and `b.<name>` surface.

2. **Mirror buffer is single-keyed** *(extend in P1).* `mirrorBuffer`/`flushMirrorColumn` (`mirror.go`) key pending writes by `entry_name` only and the flush UPDATE joins on `entry_name`. Genes (`entry_name`+`gene_name`), settings values (`entry_name`+`setting_name` within scope/owner), zones (`zone_index`), and pellets (`group_index`+`group_pellet_index`) need N-ary discriminators. Required by the "mirror everything" decision.

3. **`append` requires the complete writable column set** *(document; shapes P2).* `subcollection.go:137-141` rejects any append missing a writable field. Pellets inherit this (~16 fields incl. position/rb2d) → verbose. Zones can't use it at all (deeply nested) → motivates the clone path.

4. **Structural ops are not visible mid-run** *(shapes P2 clone API).* Append/delete are staged but never mirrored (`subcollection.go:18-20`); a just-appended zone/pellet has no in-memory row or index until commit. So a clone-then-mutate flow cannot address the new element by index mid-run — the clone surface must collect edits into the *append payload* before staging, not mutate a committed row.

5. **Two commit paths** *(document).* `save.commit(path)` (explicit export, `save_value.go:50`) and host `Commit`/`RunAndCommit` (content-addressed) both call the idempotent `ensureApplied`; no double-apply, but a script calling `save.commit()` under a committing host writes twice (file + blob). Intended, but document.

6. **Brain-graph integrity is unenforced** *(restate as risk).* `subcollection.go:22-26`: node/synapse append/delete are raw array edits — no node-index validation on synapse append, no synapse pruning on node delete. A script can produce a structurally invalid brain. Known v2 limitation; surface it in user-facing docs for "mutate brain".

7. **`settings_changers` / `settings_changer_target` resolvers exist but are unexposed.** Beyond the 8 requested features; note as a ready future surface, do not build now.

8. **Pellet append needs a target group + zone guard** *(P2).* `pelletAppendTarget` requires `GroupIndex` and (optionally) a `Zone` guard; `pelletDeleteTarget` adds `GroupPelletIndex`. The flat `save.pellets.append(group=…, …)` must carry the group; document the group requirement.

---

## P1 — Settings R/W, gene-value writes, alias fix (mutator-ready, low risk)

> **Status: Resolved 2026-06-16.** Binding-layer only — **no `savemutator`/`saveparser`/
> `duckdb`/generator changes** (the generated `settings_value` and `bibite/egg_gene_value`
> resolvers already carried everything). New file `script/thebibites/settings_value.go`;
> edits to `attr_registry.go`, `entity.go`, `sql.go`, `guards.go`, `subcollection.go`,
> `mirror.go`, `gene.go`, `convert.go`, `loadedsave.go`, `save_value.go`. Tests:
> `settings_value_test.go`, `gene_write_test.go`, `alias_test.go` (+ `guards_test.go` update).
> Whole suite green: `go test ./...`.
>
> **Resolution by sub-ticket:**
> - **1a (alias fix):** split `attrSpec` into `column` (friendly registry key + `b.<name>`)
>   and `sourceColumn` (generated DuckDB/sqlref column). All SQL/mutator/mirror/guard
>   keying now uses `sourceColumn` (`entity.go`, `sql.go` ×5 incl. `rewritePredicate`,
>   `guards.go`, `subcollection.go` delete guard). `overrides` populated with
>   `position_x/position_y/rotation → transform_*` for bibite+egg; the `sql.go:125` NOTE
>   is removed. `TestSemanticRulesReferenceLiveColumns` now collects `sourceColumn` (rules
>   key on the real quantity, alias-independent). Regression proof: `TestAliasWritablePositionX`.
> - **1b (mirror):** `mirror.go` generalized to an ordered discriminator list
>   (`mirrorLocator`) keyed by a composite locator; `flushMirrorColumn` emits the
>   discriminators into the `VALUES` relation and `AND`s them in the `WHERE`, still one
>   batched `UPDATE` per `(table,column)`. Added `recordMirrorRow`; `recordMirror` kept as
>   the `entry_name`-only wrapper (entity/bulk paths unchanged).
> - **1c (settings):** `save.settings.{simulation,independent,material("X")}["name"]`
>   returns a `Setting` handle (`.value/.name/.type/.scope` read, `.set(v)` write). Reads
>   served in-memory from a lazy per-table `(owner_id, setting_name)` index modeled on the
>   gene index, pointing into `ls.tables` (write-through). `.set` builds the `settings_value`
>   `SQLValueRef` passing `Path`/`WrapperRawJSON`/`ValueType` **verbatim** from the row, so
>   wrapper-vs-bare and zone-index guards stay the mutator's concern; mirror keyed by
>   `(entry_name, path)` (path is globally unique per settings entry).
> - **1d (gene writes):** `geneSet` refactored to backing-slice + indices (single source of
>   truth for read-back). `GeneCollection` is now a `Mapping` (`b.genes["x"]` read) +
>   `HasSetKey` (`b.genes["x"] = v` write); `b.gene("name")` point-read unchanged. Ref built
>   straight from the `GeneRow` locator; mirror keyed by `(entry_name, gene_name)`.
>
> **Deviations from the stub:**
> 1. **Zone-scoped settings values are deferred to P2.** P1 ships `simulation`/`independent`/
>    `material`; zone values share the same `SettingValueRow`/`settings_value` path and arrive
>    with the `save.zones` surface (the stub already marked `zones[i].values` as "shared w/ P2").
> 2. **Gene/settings refs are built directly from the row's own `OwnerKind`/`OwnerID`/`Path`**,
>    not via `entityLocatorRef` — the mutator gene resolver re-derives the body/egg target from
>    `owner_id` (it ignores `BodyID`), so a separate entity-locator lookup would be dead code.
> 3. **`Setting` exposes the value via `.value`** (not bare subscript) so the same handle can
>    also carry `.set()`; `scope["k"].set(v)` matches the stub's write shape.
> 4. Shared `scalarValueColumn`/`applyScalarValue` (`convert.go`) + `scalarTypeRule`
>    (`guards.go`) back both gene and settings writes (number/bool/string columns selected by
>    `ScalarType`); they carry no semantic bounds (a gene/setting may be negative).

### 1a. Alias-mechanism fix (prerequisite, see risk #1)
> **Status: Resolved 2026-06-16** — see the P1 Resolution block above.
- `script/thebibites/attr_registry.go`: add `sourceColumn string` to `attrSpec`; in `buildRegistry`, the override path sets the map key + `column` (friendly) but preserves `sourceColumn` = generated column. Sub-collection registry (`buildSubRegistry`) gets the same split.
- Update consumers to use `sourceColumn` where the real column is required: `entity.go:126,131` (`ref.Column`, `recordMirror`), `sql.go:132` (`resolveColumn`), `sql.go:381,416` (`bulkSet`/`bulkSetQuery`), guard keying in `guards.go`.
- Populate `overrides` with the friendly names the spec calls for (avoid `b.transform_position_x`): e.g. `bibite`/`egg`: `position_x → transform_position_x`, `position_y → transform_position_y`, `rotation → transform_rotation`. Keep the map tiny; everything else stays generated.
- Remove the now-resolved NOTE at `sql.go:125`.

### 1b. Mirror generalization (see risk #2)
> **Status: Resolved 2026-06-16** — see the P1 Resolution block above.
- `script/thebibites/mirror.go`: generalize `mirrorColumn` to carry an ordered list of discriminator columns and key rows by a composite locator (the discriminator values) instead of a bare `entry_name`. `flushMirrorColumn` emits those columns into the `VALUES` relation and `AND`s them in the `WHERE` (keep the single batched UPDATE per `(table,column)`). Add a `recordMirrorRow(table, column, sqlType, locators []mirrorLocator, value)` variant; keep `recordMirror` as the `entry_name`-only convenience wrapper so existing entity/bulk paths are unchanged.

### 1c. Settings surface — `save.settings.*` (read + write)
> **Status: Resolved 2026-06-16** — simulation/independent/material shipped; zone-scoped
> values deferred to P2 (`save.zones`). See the P1 Resolution block above.
- New file `script/thebibites/settings_value.go`. Add `case "settings"` to `Save.Attr`/`AttrNames` (`save_value.go:27,42`).
- Shape (follows `docs/DSL_tickets_plan.md` T7):
  ```python
  save.settings.simulation["maxBibiteCount"]          # read
  save.settings.simulation["maxBibiteCount"].set(200) # write
  save.settings.independent["..."].set(v)
  save.settings.material("Wood")["friction"].set(v)
  save.settings.zones[i].values["fertility"].set(v)   # zone-scoped values (shared w/ P2)
  ```
- Reads served in-memory from `tables.SettingsSimulationValues`/`IndependentValues`/`MaterialValues`/`ZoneValues` (`[]SettingValueRow`). Build a lazy name index per scope, modeled exactly on `buildGeneIndex`/`genesFor` (`loadedsave.go:193-203`).
- A `Setting` handle keeps the full locator from its `SettingValueRow` (`EntryName`, `Scope`, `OwnerKind`, `OwnerID`, `SettingName`, `Path`, `Type`, `WrapperRawJSON`). `.set(v)`:
  1. pick the typed value column from `row.Type` (`number_value`/`string_value`/`bool_value` — the `sqlrefvalue`-tagged fields), validate via `validateSet` (guards keyed by scope/setting_name per `guards.go:14`),
  2. build `mutator.SQLValueRef` populating Table + the chosen value Column + the locator fields above (+ `ZoneIndex/HasZoneIndex` for zone scope), `StageSQLSet(ref.WithExpected(old), v)`,
  3. write through to the in-memory `SettingValueRow` and `recordMirrorRow` keyed by `(entry_name, setting_name[, scope/owner])` (1b).
- The mutator already validates wrapper-vs-bare (`settingValueUsesWrapper`) and zone-index guards (`sqlref_settings.go`) — pass `WrapperRawJSON`/`Path` verbatim from the parsed row; do not reconstruct paths.

### 1d. Gene-value writes
> **Status: Resolved 2026-06-16** — see the P1 Resolution block above.
- Extend `script/thebibites/gene.go`: make `GeneCollection` an indexable mapping — `b.genes["Speed"]` (read) and `b.genes["Speed"] = v` (write, via `starlark.HasSetKey`). Keep `b.gene("name")` as the scalar point-read (unchanged, so existing scripts/tests don't break).
- Write path mirrors 1c: from the `GeneRow` (`EntryName`, `OwnerKind/OwnerID`, `Path`, `Type` + typed value column), build a `SQLValueRef` for the `bibite_genes`/`egg_genes` table (`resolveGeneColumn` needs `ref.Path` + entity target), `StageSQLSet`, write-through to the in-memory `GeneRow`, `recordMirrorRow` keyed by `(entry_name, gene_name)`. Reuse `entityLocatorRef` for the body/egg id locator.

---

## P2 — Zones and pellets (structural)

### 2a. Zones — `save.zones`
- New file `script/thebibites/zones.go`; add `case "zones"` to `Save.Attr`.
- Read: `save.zones` iterates `tables.SettingsZones` (`[]SettingsZoneRow`); a `Zone` exposes `name`/`material`/`distribution` (writable scalar columns → `resolveSettingsZoneColumn`) and `.values["key"]` (zone-scoped `SettingValueRow`s, shared with 1c). Build a lazy by-`zone_index` index.
- Scalar set (`z.name = "..."`): mirror the `entity.SetField` pattern — `SQLValueRef{Table:"settings_zones", Column:<source>, EntryName, ZoneIndex/HasZoneIndex, ZoneID/HasZoneID}` (zone-id stale guard via `zoneIDGuards`), `StageSQLSet`, write-through, `recordMirrorRow` keyed by `zone_index`.
- **Append by clone** (decision): `new = save.zones.clone(i)` returns a detached **PendingZone** holding a deep copy of `SettingsZoneRow.RawJSON` (parsed to `map[string]any`); `new.name = "..."`/`new.values["k"] = v` mutate that in-memory copy. A single `StageSQLAppend(ref{Table:"settings_zones", EntryName}, payload)` (→ `settingsZoneAppendTarget`, "zones" container) is staged when the pending zone is finalized (explicit `.append()` or end-of-run). This sidesteps risk #4 (no mid-run index needed). Document: a cloned zone is not visible to in-run reads/SQL until commit.
- Delete: `save.zones[i].delete()` → `SQLValueRef` with `ZoneIndex` + `ZoneID` guard → `StageSQLDelete` (`settingsZoneDeleteTarget`). Structural; not mirrored.

### 2b. Pellets — `save.pellets`
- New file `script/thebibites/pellets.go`; add `case "pellets"` to `Save.Attr`.
- Read: `save.pellets` iterates `tables.Pellets` (`[]PelletRow`, flat across groups); each `Pellet` exposes scalar columns (`material`, `amount`, `position_x/y`, `rotation`, rb2d, decay) + `group`/`group_pellet_index` locators.
- Scalar set: `p.amount = 5.0` → `SQLValueRef{Table:"pellets", Column:<source>, EntryName, GroupIndex/HasGroupIndex, GroupPelletIndex/HasGroupPelletIndex, Zone/HasZone}` → `resolvePelletColumn`; write-through + `recordMirrorRow` keyed by `(group_index, group_pellet_index)`.
- **Append by fields** (decision): `save.pellets.append(group=0, material="meat", amount=5.0, position_x=…, …)` — reuse the `ElementCollection.appendBuiltin` contract (`subcollection.go:110`): all writable fields required, validated, built into the element map, `StageSQLAppend` (→ `pelletAppendTarget`, needs `GroupIndex`, optional `Zone` guard). Sets `op.SceneCount = sceneCountPellets` automatically via the resolver. Document the `group=` requirement (risk #8).
- Delete: `save.pellets[i].delete()` → `GroupIndex`+`GroupPelletIndex` → `StageSQLDelete` (`pelletDeleteTarget`, scene-count reconciled). Structural.

---

## P3 — Cross-save query→insert: documented stub
- Add a clearly-erroring seam on `save` (e.g. `save.insert_from(...)` / a `workspace`/`open()` binding) that returns a clean Starlark error: `"cross-save transfer is not implemented (v2); see savemutator/thebibites/workspace.go"`. Add a one-paragraph note to `docs/DSL_tickets_plan.md` pointing at `workspace.go` as the entry point and naming **settings** as the first canonical cross-save copy target. No mutator work.

---

## Reuse map (do not reinvent)
- Locators: `entityLocatorRef`, `subElementRef`, `rowForEntry` (`loadedsave.go`).
- Lazy per-entity indexes: copy `buildGeneIndex`/`genesFor` and `buildSubRowIndex`/`subRowsFor` (`loadedsave.go:193-250`) for settings/zone/pellet indexes.
- Conversions/validation/write-through: `fromStarlark`/`toStarlark`/`goScalar` (`convert.go`), `validateSet` (`guards.go`), `setRowField`, `recordMirror[Row]` (`mirror.go`).
- Staging: `session.StageSQLSet/StageSQLAppend/StageSQLDelete` + `SQLValueRef.WithExpected` (`sqlref.go`).
- Registry: extend `attrSpec`/`overrides`/`buildRegistry` (`attr_registry.go`) — keep single-source-of-truth derivation from `tb.NormalizedTables`; do **not** add hand-written allowlists (per the sqlref generation philosophy).

## Verification
- **Unit tests** alongside new files, modeled on `mutation_test.go`, `delete_test.go`, `subcollection_test.go`, `analytics_test.go`:
  - Settings: `simulation["k"].set(v)` round-trips through reparse; wrapper-vs-bare both work; wrong-type rejected by guard; zone-scoped value hits the right index.
  - Genes: `b.genes["x"] = v` stages + persists; read-back via `b.gene("x")` and via SQL both reflect it (mirror-everything).
  - Zones: clone→edit→commit produces a new zone; `z.name=` set persists; `[i].delete()` removes the right zone (id-guard catches a shifted index).
  - Pellets: field append persists with reconciled `SceneCount`; `[i].delete()` reconciles count; scalar set round-trips.
  - **Alias fix:** `b.position_x = v` (aliased, writable) stages, mirrors, and persists — the regression that proves risk #1 is fixed.
  - **Mirror-everything:** `set → save.sql()` in one script observes the new value for each surface (settings/gene/zone-value/pellet).
  - **Churn DoD:** assert `writeArchiveCount ≤ 1`, `dbOpenCount ≤ 1`, `reparseCount == 0` (no verify) on a mixed mutation run.
- **Whole-suite:** `go test ./script/... ./savemutator/... ./saveparser/...`. No `go generate` expected (reusing existing resolvers/metadata); if any `sqlref`/`dbtable` tag is touched, regenerate and re-run the drift tests (`TestSQLRefTagsMatchParsedValues`, schema fingerprint).
- **End-to-end:** a script exercising one mutation per surface → `save.commit(tmp)` → reparse → assert values (extend `commit_test.go`).

## Out of scope
Cross-save transfer implementation (#8 stays a stub); brain-graph integrity enforcement; `settings_changers` surface; bulk `where().append()/delete()`; multi-save workspaces; relaxing the all-fields append contract.
