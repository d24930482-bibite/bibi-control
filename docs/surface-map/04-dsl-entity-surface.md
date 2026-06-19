# 04. DSL per-entity read/write surface

**Scope (files scanned):** `script/thebibites/entity.go`, `gene.go`, `subcollection.go`,
`collection.go`, `aggregates.go`, `save_value.go`, `convert.go`, `guards.go`,
`attr_registry.go`, `pellets.go`, `bindings.go`, `run.go`. (Helpers in `loadedsave.go` —
`rowForEntry`, `genesFor`, `subRowsFor`, `entityLocatorRef`, `subElementRef`,
`stageScalarSet`, `bulkSet`/`bulkSetExpr`/`bulkDelete`, `matchingEntryNames`, `scalarAgg`/
`groupedAgg` — and `sql.go`/`scope.go`/`mirror.go` are referenced as boundaries but owned by
other slices.)

All entries are scale **[SAVE]** (one loaded working copy) unless noted; the EntityCollection
also carries an optional spanning `scope` that flips it read-only for WORLD/WORKSPACE aggregate
reads (that scope plumbing is owned by other slices — here it appears only as a gate).

## Location map

### Top-level handle (save_value.go)
- `save_value.go:38` — [READ] [SAVE] — `Save.Attr`: dispatches `s.bibites`/`s.eggs` (EntityCollection), `s.pellets` (Pellets), `s.sql`/`s.settings`/`s.zones`/`s.commit` (other slices).
- `save_value.go:41` — [READ] [SAVE] — `s.bibites` -> `&EntityCollection{kind:"bibite"}` (no scope ⇒ working/mutable).
- `save_value.go:43` — [READ] [SAVE] — `s.eggs` -> `&EntityCollection{kind:"egg"}`.
- `save_value.go:51` — [READ/WRITE] [SAVE] — `s.pellets` -> `&Pellets{}`.
- `save_value.go:67` — [WRITE] [SAVE] — `commitBuiltin`: `save.commit(path)` writes the corrected zip (no reparse), returns staged-op count; dry-run stages but writes nothing.
- `save_value.go:28` — [READ] [SAVE] — `NewSaveValue(ls)`: constructor the workspace automation layer uses to wrap a working copy in the proven DSL surface.

### Entity handle: bibite / egg (entity.go)
- `entity.go:35` — [READ] [SAVE] — `Entity.Attr`: friendly scalar read via `attrRegistry()`, plus `gene`/`genes`/`delete`/sub-collections.
- `entity.go:52-58` — [READ] [SAVE] — `categoryScalar` read: `rowForEntry(spec.table, entryName)` then `toStarlark(row.FieldByIndex(spec.fieldIndex))`; missing 1:1 sub-table row ⇒ `None`.
- `entity.go:66` — [READ] [SAVE] — `AttrNames`: derived readable attrs + `gene`/`genes`/`delete` + sub-collection names, sorted.
- `entity.go:89` — [WRITE] [SAVE] — `Entity.SetField` (`b.energy = x`): scalar set. Resolves writability from `attrSpec.writable` (sqlref path), captures old value as stale guard, `validateSet`, `setRowField` write-through, `stageScalarSet` + DuckDB mirror; rolls back the in-memory write if staging fails.
- `entity.go:154` — [READ] [SAVE] — `geneBuiltin`: `b.gene("Name")` -> typed gene value, `None` if absent (point read).
- `entity.go:178` — [WRITE] [SAVE] — `deleteBuiltin`: `b.delete(prune=False)` stages whole-entity delete (structural, NOT mirrored).
- `entity.go:196` — [WRITE] [SAVE] — `stageEntityDelete`: shared by single + bulk delete; prune cascade (bibite only) via `StageDeleteBibiteWithOptions`, else generic `StageSQLDelete`.
- `entity.go:146` — [READ] [SAVE] — `SourceLoadedSave`: exposes `ls` to the transfer binding (cross-world slice consumer).
- `entity.go:151` — [READ] [SAVE] — `EntryName`: single-entity transfer selection accessor.

### Gene read/write (gene.go)
- `gene.go:32` — [READ] [SAVE] — `Gene.Attr`: `name`/`value`/`type` of one gene row.
- `gene.go:50` — [READ/WRITE] [SAVE] — `GeneCollection` (`b.genes`): Iterable/Sequence/Mapping/HasSetKey.
- `gene.go:72` — [READ] [SAVE] — `rows()`: materializes one entity's genes in save order via `genesFor`.
- `gene.go:87` — [READ] [SAVE] — `Get`: `b.genes["Name"]` -> typed value; missing ⇒ found=false (KeyError).
- `gene.go:112` — [WRITE] [SAVE] — `SetKey`: `b.genes["Name"] = v`; rejects unknown gene names (genes are not created here).
- `gene.go:132` — [WRITE] [SAVE] — `setGeneValue`: validate against scalar type, write-through GeneRow, `stageScalarSet` + DuckDB mirror keyed by (entry_name, path).
- `gene.go:174` — [WRITE] [SAVE] — `geneTable`: kind -> gene table (`bibite_genes`/`egg_genes`), localized loud-default map.
- `gene.go:188` — [READ] [SAVE] — `Len` (non-allocating gene count).
- `gene.go:196` — [READ] [SAVE] — `Iterate`/`geneIterator`: yields `*Gene` per row.

### Brain/stomach sub-collections (subcollection.go)
- `subcollection.go:48` — [READ/WRITE] [SAVE] — `ElementCollection` (`b.synapses`/`b.nodes`/`b.stomach`): Iterable/Sequence + `append`/`count`.
- `subcollection.go:70` — [READ] [SAVE] — `rows()`: `subRowsFor(spec.table, entryName)` paired with array index.
- `subcollection.go:79` — [READ] [SAVE] — `Len` (element count).
- `subcollection.go:87` — [READ] [SAVE] — `Attr`: `append` + `count` builtins.
- `subcollection.go:110` — [WRITE] [SAVE] — `appendBuiltin`: `b.<sub>.append(field=value,...)`; every writable col required, unknown/read-only/out-of-domain rejected; `StageSQLAppend` (structural, NOT mirrored).
- `subcollection.go:182` — [READ/WRITE] [SAVE] — `ArrayElement`: one element row; `index` + per-element `delete`.
- `subcollection.go:204` — [READ] [SAVE] — `ArrayElement.Attr`: `index`, `delete`, else element scalar via `spec.elementAttrs`.
- `subcollection.go:232` — [WRITE] [SAVE] — `ArrayElement.deleteBuiltin`: `element.delete()` located by array index, stale-guarded on `guardColumn` value; `StageSQLDelete` (structural, NOT mirrored).
- `subcollection.go:31` — [WRITE] [SAVE] — `setRefArrayIndex`: stamps array ordinal onto the matching `SQLValueRef` index field (synapse/node/content).

### Collection / .where / aggregates / bulk mutation (collection.go)
- `collection.go:74` — [READ/WRITE] [SAVE|WORLD|WORKSPACE] — `EntityCollection`: lazy query-backed sequence; `scope` (nil ⇒ working/mutable; non-nil ⇒ read-only spanning, aggregate-only).
- `collection.go:128` — [READ] — `Truth`: spanning ⇒ always truthy (never materialized); else `Len()>0`.
- `collection.go:141` — [READ] — `readOnly`: spanning scope ⇒ aggregate/where/group_by only.
- `collection.go:151` — [READ/WRITE] — `Attr`: aggregates + `where`/`group_by`; `set`/`set_expr`/`delete` hidden when read-only.
- `collection.go:181` — [READ] — `runAgg` -> `ls.scalarAgg` (one scalar, push-down, no row materialization).
- `collection.go:197` — [READ] — `EntryNames`: transfer-source selection (bibite/egg only; spanning rejected) — consumer is the transfer slice.
- `collection.go:235` — [WRITE] [SAVE] — `setBuiltin`: `where(...).set(column, value)` batched constant scalar set -> `ls.bulkSet`; returns rows staged.
- `collection.go:257` — [WRITE] [SAVE] — `setExprBuiltin`: `where(...).set_expr(column, expr)` per-row SQL-expr set -> `ls.bulkSetExpr`.
- `collection.go:278` — [WRITE] [SAVE] — `deleteBuiltin`: `where(...).delete(prune=False)` -> `ls.bulkDelete`; refuses unfiltered delete-all (must opt in with `.where("true")`).
- `collection.go:299` — [READ] [SAVE] — `whereBuiltin`: narrows collection, eagerly resolves predicate (one push-down query, memoized); spanning carries predicate forward without materializing.
- `collection.go:333` — [READ] — `groupByBuiltin` -> `GroupedCollection`.
- `collection.go:357` — [READ] [SAVE] — `entryNames`: unfiltered ⇒ identity-table in-memory order (no DuckDB); filtered ⇒ memoized push-down, re-resolved when `stagedOps` advanced.
- `collection.go:377` — [READ] — `resolveOrPanic`: Len/Iterate cannot error, so a bad predicate / spanning-iterate panics loudly (engine recovers into diagnostic).
- `collection.go:392/396` — [READ] [SAVE] — `Len`/`Iterate`/`entityIterator`: materialize `*Entity` per matching entry_name (`rowsMaterialized++`).
- `collection.go:425` — [READ] [SAVE|WORLD|WORKSPACE] — `GroupedCollection`: `group_by(col)` aggregate-only, returns dict keyed by group value via `ls.groupedAgg`.
- `collection.go:224` — [READ] — `rejectMutation`: belt-and-suspenders gate on mutation builtins under a read-only scope.
- `collection.go:342` — [READ] [SAVE] — `identityAccess`: `tableAccess` for the kind's identity table (first of `entityTables`).

### Host aggregate builtins (aggregates.go)
- `aggregates.go:21` — [READ] [SAVE] — `hostAggregates`: predeclared `sum`/`mean`/`median` over an already-materialized Starlark iterable (convenience for SMALL lists, NOT the push-down scale path).
- `aggregates.go:56` — [READ] — `aggMedian`: linear-interpolated median matching DuckDB's `median()`.
- `aggregates.go:76` — [READ] — `unpackFloats`: reads the single iterable arg into `[]float64`.

### Aggregate method plumbing (collection.go top)
- `collection.go:13` — [READ] — `aggRunner`: the one behavioral difference between EntityCollection (scalar) and GroupedCollection (dict).
- `collection.go:17/28/39` — [READ] — `aggMethod`/`countMethod`/`quantileMethod`: build the sum/mean/median/min/max, count, quantile builtins (quantile q∈[0,1] validated).
- `collection.go:56` — [READ] — `aggAttr`: shared aggregate-name dispatch for both collections.

### Value conversion (convert.go, save_value.go geneValueToStarlark)
- `convert.go:19` — [READ] — `toStarlark`: reflected Go scalar -> Starlark (named string/number kinds via underlying Kind).
- `convert.go:44` — [READ] — `fromSQLValue`: DuckDB driver scalar -> Starlark (NULL->None, HUGEINT *big.Int via `MakeBigInt`); delegates coercion to `duckdb.NormalizeSQLScanValue`.
- `convert.go:81` — [WRITE] — `fromStarlark`: Starlark scalar -> Go scalar to stage; rejects non-scalars (list/dict/None). Does NOT validate type-match (that's guards.go).
- `convert.go:104` — [WRITE] — `goScalar`: reflected Go scalar -> plain Go value, capturing the current value for the stale guard.
- `convert.go:128` — [WRITE] — `setRowField`: coerce goVal to the row field's kind + write through in memory; the only write-time typing (memory safety/JSON fidelity).
- `convert.go:174/185` — [WRITE] — `asFloat64`/`asInt64`: coercion helpers (asInt64 rejects out-of-int64-range floats).
- `convert.go:206` — [WRITE] — `scalarValueColumn`: ScalarType -> typed value column + SQL type (shared by gene + settings writes); NULL/unknown not settable.
- `convert.go:228` — [WRITE] — `applyScalarValue`: coerce + write-through gene/settings field pointers, return old + staged.
- `convert.go:254` — [READ] — `geneValueToStarlark`: typed gene cell -> Starlark by ScalarType (NULL->None).

### Write guards / validation (guards.go)
- `guards.go:68` — [WRITE] — `deriveType`: generated SQLType -> `valueKind` (the entire type policy; no per-column allowlist).
- `guards.go:100` — [WRITE] — `nonNegativeColumns`: hand-curated non-negative writable columns (the one place metadata can't express); `species_id` is shape-only (referential existence is a deliberate seam — see below).
- `guards.go:117` — [WRITE] — `semanticRules`: column -> Rule (today = the non-negative set).
- `guards.go:129` — [WRITE] — `ruleFor`: derived Type + semantic override, keyed by `sourceColumn` (alias-independent).
- `guards.go:140` — [WRITE] — `validateSet`: entity/sub/pellet scalar set guard before staging.
- `guards.go:149` — [WRITE] — `scalarTypeRule`: type-only Rule for a gene/settings value (no bounds).
- `guards.go:165` — [WRITE] — `validateValue`: pure type -> range -> enum check; rejects NaN/±Inf early (would else abort at commit-time json.Marshal).
- `guards.go:241` — [WRITE] — `coerceExprResult`: set_expr per-row SQL result -> stageable scalar (absorbs HUGEINT/uint64/NULL, column-named diagnostics).
- `guards.go:282/313` — [WRITE] — `exprToInt64`/`exprToFloat64`: narrow/widen a scanned expr scalar to the target column kind.
- `guards.go:330/337` — [WRITE] — `typeError`/`goScalarName`: friendly mismatch diagnostics.

### Attribute / member registration (attr_registry.go)
- `attr_registry.go:38` — [READ/WRITE] — `attrSpec`: one friendly attribute (`column` friendly vs `sourceColumn` generated; `writable` iff sqlref path; `jsonKey`=SQLRefPath for sub append).
- `attr_registry.go:58` — [READ] — `entityTables`: per-kind 1:1 tables (identity first); `pellet` is analytics-only (read-only spanning, no mutation).
- `attr_registry.go:87/101` — [READ] — `transformAliases`/`overrides`: friendly alias -> source column (position/rotation triple; pellet adds scale).
- `attr_registry.go:132/137` — [READ] — `attrRegistry`/`buildRegistry`: lazily built kind->name->spec from `tb.NormalizedTables` (identity table wins on collision).
- `attr_registry.go:160` — [READ] — `nonScalarColumns`: excludes `save_id` (locator noise) + `raw_json` (whole blob) from the friendly surface.
- `attr_registry.go:172` — [READ] — `tableScalarSpecs`: derive friendly scalar specs for one table from generated metadata (writable iff SQLRefPath != "").
- `attr_registry.go:218` — [READ] — `applyOverrides`: register aliases as extra keys keeping `sourceColumn`.
- `attr_registry.go:246/254` — [READ] — `zoneRegistry`/`pelletRegistry`: scalar registries for zones (other slice) / pellets.
- `attr_registry.go:287` — [READ] — `allNormalizedTableNames`: every normalized table (per-world save_id shadowing — other slice).
- `attr_registry.go:329` — [READ/WRITE] — `entitySubCollections`: per-kind 1:many sub-tables (synapses/nodes/stomach; eggs have no stomach) + array-ordinal index column.
- `attr_registry.go:344` — [READ] — `subLocatorColumns`: parent/position columns excluded from the element read/append surface.
- `attr_registry.go:355/373/378` — [READ/WRITE] — `subCollectionSpec`/`subCollectionRegistry`/`buildSubRegistry`: built sub-collection (elementAttrs, writableCols, guardColumn).
- `attr_registry.go:450` — [WRITE] — `chooseGuardColumn`: pick a high-cardinality (non-bool) writable column as the delete stale guard.

### Pellets (pellets.go)
- `pellets.go:42` — [READ/WRITE] [SAVE] — `Pellets` (`save.pellets`): flat Indexable/Sequence across all groups + `clone`/`count`.
- `pellets.go:59-61` — [READ] [SAVE] — `Len`/`Index`/`Iterate`: index into `ls.tables.Pellets`.
- `pellets.go:87` — [WRITE] [SAVE] — `cloneBuiltin`: `save.pellets.clone(i)` -> `PendingPellet`; deep-copies the archive's retained Raw JSON (PelletRow has no RawJSON).
- `pellets.go:130/152` — [READ] [SAVE] — `Pellet`/`Pellet.Attr`: scalar read via `pelletRegistry()`; `delete` builtin.
- `pellets.go:178` — [WRITE] [SAVE] — `pelletRef`: locator keyed by entry_name + group_index + group_pellet_index, zone as stale-group guard.
- `pellets.go:195` — [WRITE] [SAVE] — `Pellet.SetField`: scalar set; validate, write-through row, `stageScalarSet` + mirror keyed by (entry_name, group_index, group_pellet_index).
- `pellets.go:239` — [WRITE] [SAVE] — `Pellet.deleteBuiltin`: `p.delete()` structural delete, stale-guarded on material; mutator reconciles scene nPellets. NOT mirrored.
- `pellets.go:267` — [READ/WRITE] [SAVE] — `PendingPellet`: detached editable clone (creation surface); HasAttrs/HasSetField/`append`.
- `pellets.go:286/323` — [READ/WRITE] [SAVE] — `PendingPellet.Attr`/`SetField`: read/write into the pending nested JSON by sqlref path; coerced to field type so pending == committed on-disk JSON.
- `pellets.go:394` — [WRITE] [SAVE] — `appendBuiltin`: `pp.append(zone="X")` resolves zone -> group, `StageSQLAppend` of the whole element. Structural, NOT mirrored, single-shot (`appended`).
- `pellets.go:361` — [WRITE] [SAVE] — `coercePelletScalar`: match committed setRowField coercion (e.g. DOUBLE int->float).
- `pellets.go:428` — [READ] [SAVE] — `pelletGroupByZone`: zone name (ergonomic handle) -> group index; refuses missing/ambiguous.
- `pellets.go:470/500/541` — [READ/WRITE] [SAVE] — `parsePelletPath`/`setNestedPellet`/`getNestedPellet`: isolated nested-JSON path helpers (set-into-existing only; deliberately local).

### Run / bindings (bindings.go, run.go)
- `bindings.go:10` — [READ/WRITE] [SAVE] — `Globals(ls)`: predeclared globals — `open()`, `autocommit()`, host aggregates.
- `bindings.go:27` — [READ] [SAVE] — `openBuiltin`: `open(path=None)` returns the Save bound to the already-loaded `ls` (no re-Load); `path` accepted+ignored (forward compat).
- `bindings.go:42` — [WRITE] [SAVE] — `autocommitBuiltin`: `autocommit(enabled=True)` sets `ls.willCommit` intent only.
- `run.go:39` — [READ/WRITE] [SAVE] — `RunAndCommit`: load once, run program, record run, commit a content-addressed revision when run succeeds + not dry-run + autocommit on + `stagedOps>0`.
- `run.go:49` — [READ/WRITE] [SAVE] — `runLoaded`: core run loop; sets `ls.dryRun`, runs via `script.Run(Globals(ls))`, prepares commit BEFORE recording the run (truthful provenance), records revision.

## Read paths

**Point reads.** A loaded save is opened as a `Save` (`open()` -> `bindings.go:34`), whose
`Attr` (`save_value.go:38`) hands out entity collections, pellets, and (other-slice) settings/
zones/sql. A single entity comes from iterating a collection; reading a scalar off it
(`b.energy`) resolves through the generated `attrRegistry()` to a 1:1 row read
(`entity.go:52`, `convert.go:19`). Genes read three ways: point `b.gene("X")` -> None
(`entity.go:154`), mapping `b.genes["X"]` -> KeyError on miss (`gene.go:87`), or iteration over
`b.genes` (`gene.go:196`). Brain/stomach elements read by iterating `b.synapses`/`b.nodes`/
`b.stomach` and reading element scalars + `.index` (`subcollection.go:204`). Pellets read by
flat index `save.pellets[i].amount` (`pellets.go:152`).

**Scan / analytics reads (no row materialization).** `EntityCollection` aggregates
(`.count/.sum/.mean/.median/.min/.max/.quantile`) and `.group_by(...).<agg>` push down into
DuckDB (`collection.go:181/462` -> `ls.scalarAgg`/`groupedAgg`), never building `*Entity`
values. `.where(predicate)` (`collection.go:299`) takes a RAW SQL predicate string (the one
escape hatch baked into the otherwise-object surface) and AND-combines it; the predicate text
is friendly-column-rewritten at query-compile time (other slice). Iteration of a filtered
collection reuses the eagerly-resolved entry_name set, re-resolving when `stagedOps` advanced
(`collection.go:357`) so Len/Iterate stay consistent after an in-run mutation. Host
aggregates (`aggregates.go:21`) are a separate small-list convenience over an
already-materialized iterable — explicitly NOT the scale path.

**Spanning reads (WORLD/WORKSPACE) appear here only as a gate.** A non-nil read-only `scope`
makes the collection aggregate-only: `set`/`set_expr`/`delete` are hidden (`collection.go:160`)
and iteration/Len panic (`collection.go:377`). The scope itself is owned by other slices; this
slice enforces "mutation is per-save by construction."

## Mutation paths

**Scalar set (staged AND DuckDB-mirrored ⇒ visible to in-run reads/SQL).** Three entry points
share the validate -> write-through -> `stageScalarSet` + mirror shape:
- entity attribute `b.energy = x` (`entity.go:89`),
- gene value `b.genes["X"] = v` (`gene.go:112` -> `setGeneValue` `gene.go:132`),
- pellet scalar `save.pellets[i].amount = x` (`pellets.go:195`).
Each: `fromStarlark` (`convert.go:81`) -> `validateSet`/`validateValue` (`guards.go:140/165`)
-> `setRowField`/`applyScalarValue` write-through (`convert.go:128/228`) -> stage + mirror.
Writability is read straight from `attrSpec.writable` (the field's sqlref path) — no parallel
allowlist (the generation philosophy). Aliases (`position_x`) resolve to `sourceColumn` so
SQL/mutator/mirror/guard keying targets the real column (`attr_registry.go:38/218`).

**Bulk scalar set / set_expr (staged, mirrored, predicate-scoped).** `where(...).set(col, val)`
(`collection.go:235`) and `.set_expr(col, expr)` (`collection.go:257`) push down through
`ls.bulkSet`/`bulkSetExpr` (other slice) — `set` applies a constant, `set_expr` evaluates a SQL
expression per matched row (deliberately distinct methods, not string-vs-constant detection).
`coerceExprResult` (`guards.go:241`) turns the per-row SQL result into a validated stageable
scalar.

**Structural mutations (staged, NOT mirrored ⇒ invisible to in-run reads until commit).**
- whole-entity delete: `b.delete(prune=False)` (`entity.go:178`) and bulk
  `where(...).delete(prune=False)` (`collection.go:278`), both via `stageEntityDelete`
  (`entity.go:196`); unfiltered bulk delete is refused.
- sub-collection append: `b.synapses.append(...)` etc. (`subcollection.go:110`) — every
  writable column required.
- sub-element delete: `element.delete()` (`subcollection.go:232`) — array-index located,
  stale-guarded on a high-cardinality column.
- pellet delete: `save.pellets[i].delete()` (`pellets.go:239`).
- pellet creation by clone: `clone(i)` -> edit `PendingPellet` scalars -> `append(zone="X")`
  (`pellets.go:87/323/394`).

**Commit.** `save.commit(path)` (`save_value.go:67`) writes the corrected zip; the host path
(`run.go`) commits a content-addressed revision when run-ok + not dry-run + `autocommit` on +
`stagedOps>0`. `autocommit(False)` (`bindings.go:42`) opts a pure-analysis run out.

## Missing seams

### Sub-collection element scalars are read-only (no `element.field = v`)
**What's missing.** `ArrayElement` (synapse/node/stomach element) implements `HasAttrs` for
reads but NOT `HasSetField` (`subcollection.go:182-216` — no `SetField`). The sub-collection
write surface is whole-element `append` + whole-element `delete` only. Editing one field of an
existing synapse/node/stomach element (e.g. bump one synapse `weight`, retarget one `node_in`)
has no DSL path: a user must delete the element and re-append it (recomputing every required
field), which also changes its array index. `elementAttrs` already records `writable`/`sqlType`/
`sourceColumn` per element column (`attr_registry.go:418-427`) and `guardColumn` exists, so the
metadata for an in-place guarded set is present and unused.
**Consequence.** The most common brain-tuning edit (adjust an existing synapse weight) is not
expressible; the only workaround is destructive delete+append, which loses positional identity
and forces the user to restate the whole element. This is adjacent to but distinct from
missing.md §6 (node↔synapse join) and §5 (unify surfaces) — those are about reading/joining the
graph; this is the absence of an element-scalar *write*.
**Where it lives.** `subcollection.go:182` (ArrayElement, no SetField) /
`attr_registry.go:418` (writable element specs already derived).

### Entity / element scalar set cannot write NULL (clear an optional value)
**What's missing.** `fromStarlark` (`convert.go:81`) rejects `None` ("cannot set attribute to
NoneType"), and `setRowField` (`convert.go:128`) has no nil branch. Every scalar set must be a
concrete int/float/bool/string. There is no way through the DSL to set a column back to NULL /
clear an optional sub-table value (and `entity.go:52-56` shows reads can legitimately return
`None` for an absent 1:1 row, so the read/write surfaces are asymmetric).
**Consequence.** A field that the format treats as nullable/optional can be read as None but
never written to None — the only escape is raw `save.sql` (other slice). A round-trip
`x = b.field; ...; b.field = x` breaks when `x` is None.
**Where it lives.** `convert.go:81` (fromStarlark rejects None) / `convert.go:128`
(setRowField has no nil case).

### `b.delete()` is silently a no-op when nothing matched (no count, no surfacing)
**What's missing.** Single `Entity.delete` (`entity.go:178`) returns `None` and just increments
`stagedOps`; it cannot report whether the entity was actually present/deletable (the referential
guard for orphaning a parent link fires far later, inside `Session.Apply` at commit —
`entity.go:170-177` comment). Bulk `where(...).delete` returns a count (`collection.go:296`),
so the single and bulk delete surfaces disagree on feedback, and a single delete that ultimately
fails its guard only surfaces at commit, far from the call site.
**Consequence.** A script deleting one entity gets no in-run signal of success/failure;
guard rejection is deferred to commit, making the diagnostic non-local — inconsistent with the
"fail loudly, localized" stance applied to scalar/element guards.
**Where it lives.** `entity.go:178` (single delete returns None) vs `collection.go:296` (bulk
returns count); guard deferral noted at `entity.go:170-177`.

### Pellet creation cannot place into a zone with duplicate group names, and `append` has no in-place pellet edit
**What's missing.** `PendingPellet.append(zone=...)` resolves the target group strictly by zone
name and refuses when a zone name maps to >1 pellet group (`pellets.go:441-448` — "ambiguous").
There is no alternate handle (e.g. explicit group index) to disambiguate, so for a save whose
format does not guarantee zone uniqueness across groups, those groups are simply unreachable for
clone-append. Separately, committed pellets have no batch/`where`-style mutation: `save.pellets`
is index-only (`pellets.go:60`), unlike entities' `where(...).set`.
**Consequence.** Pellet creation is blocked outright for ambiguous-zone saves (no fallback
selector), and bulk pellet edits require a manual `for`-loop over the flat index (or raw SQL),
with no predicate push-down.
**Where it lives.** `pellets.go:441` (ambiguous-zone refusal, no index fallback) /
`pellets.go:60` (Index-only, no where/set on Pellets).

### `species_id` write has no referential check (shape-only guard)
**What's missing.** `species_id` is in `nonNegativeColumns` (`guards.go:106`) so a set is only
checked for non-negativity; the comment explicitly notes "full referential existence (must match
a species row) is a deliberate seam, not implemented here" (`guards.go:94-95`). Setting
`b.species_id = <nonexistent>` validates and stages cleanly.
**Consequence.** A scalar set can point a bibite/egg at a species id that has no
`activeSpeciesList` row, producing a structurally valid but semantically dangling entity that no
guard catches in this slice (cross-world transfer remaps species ids — memory note
`bibites-id-spaces` — but a plain same-save scalar set does not). Flag for the mutation-engine /
transfer slices to confirm whether commit-time validation covers it.
**Where it lives.** `guards.go:106` (species_id as shape-only) / `guards.go:94` (seam comment).
