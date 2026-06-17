# DSL_dev — high-effort branch review

Scope: `git diff @{upstream}...HEAD` (~40 commits, ~3.4k insertions). Reviewed for
recall across 8 finder angles (3 correctness, reuse/simplification, efficiency,
altitude, conventions), deduped, and verified against source. No CLAUDE.md files
govern the changed code, so the conventions angle returned nothing.

Findings ranked most-severe first. Correctness/behavior outrank cleanup.

## Correctness / behavior

### 1. `script/thebibites/zones.go:488` — wrapped zone-value write misparses dotted/bracketed keys
`pendingZoneValues.SetKey` writes the bare branch as `data[name] = coerced` *precisely
because* a zone-value key can contain `.`/`[`, but the wrapped branch calls
`setNestedPellet(pv.pz.data, name+".Value", coerced)`. `setNestedPellet` → `parsePelletPath`
splits on `.` and expands a trailing `[n]`. A wrapped value under key `"foo.bar"` is
navigated as `data["foo"]["bar"]["Value"]` → "key missing" error (rejecting a legitimate
edit), or a wrong-location write if a colliding nested object exists. The dotted-key risk
the bare branch guards against is reintroduced in the wrapped branch.

### 2. `script/thebibites/attr_registry.go:412` — synapse multi-delete can still delete the wrong element
`chooseGuardColumn` only excludes *boolean* guard columns; it does not re-base positional
indices on multi-delete. `b.synapses[2].delete(); b.synapses[3].delete()` stages both against
load-time indices; the first delete shifts the array so the second addresses the original
index 4. If index-4's guard value (weight/innovation — duplicates recur in evolved brains)
equals the captured value, the wrong synapse is silently deleted. The guard makes the
collision *less likely*, not impossible — the "shifted index fails loudly" contract is still
violated. The deep fix re-bases indices (or rejects multi-delete against one array).

### 3. `script/thebibites/collection.go:177` — unfiltered `delete()`/`set()` affects the whole population
`delete`/`set`/`set_expr` are exposed directly on `EntityCollection`, and `entryNames()`
treats `where == ""` as "the whole identity table." `save.bibites.delete()` with no `.where()`
(a dropped or typo'd filter) stages a whole-population delete with no error or confirmation.
There is no minimum-predicate guard on the destructive structural ops.

### 4. `script/thebibites/collection.go:259` — filtered `Iterate()`/`Len()` swallow predicate errors
Starlark's `Iterator`/`Len` cannot return errors, so `entryNames()` maps a push-down
resolution failure (`resolveErr`) to the empty set. `for e in save.bibites.where("enrgy > 100")`
(typo'd column) silently iterates nothing and `len(...)` returns 0 with no diagnostic — a
maintenance/cull script reports success having processed nothing. (Trade-off: empty-on-error
is safer than enumerating the whole population, but it masks malformed predicates.)

### 5. `script/engine.go:50` — panic recovery drops `result.Output`
The panic-recovery defer is registered *before* `output` is declared (line 69), so it
structurally cannot set `result.Output`; it populates only `Diagnostics`/`err`. A script that
prints and then triggers a host-builtin panic never returns from `ExecFileOptions`, so
`result.Output = output.String()` (line 96) is skipped and `RecordScriptRun` records the
failed run with empty output — losing the diagnostics an operator needs to debug the panic.

### 6. `script/thebibites/collection.go:231` — memoized `entryNames` goes stale vs fresh `count()`
`entryNames()` memoizes the resolved set on the collection (`resolvedSet`), but
`count()`/aggregates re-query fresh via `scalarAgg`. After an in-run *mirrored* mutation
changes a predicate-relevant column, the same collection object's `len(c)`/`for b in c` use the
stale memo while `c.count()` reflects the change — the Len/count consistency the F2 fix
established breaks once a mutation touches the predicate column.

### 7. `script/thebibites/sql.go:600` — `set_expr` expression gate is too permissive
`validateExprSafety` blocks only `;` and `( SELECT`, so `set_expr` accepts shapes the constant
`set()` path never could. Aggregates/window functions (`sum(energy)`) surface as a confusing
"unknown column in expression" binder error; a trailing `--` comments out the rest of the
single-line generated SELECT (including the `save_id` placeholder), yielding a low-level parse
error instead of a clean diagnostic. The gate advertises catching subqueries but is incomplete.

## Cleanup / altitude / efficiency

### 8. `script/thebibites/sql.go:495` — duplicated write-through/rollback block
`bulkSetExpr`'s write-through/rollback/stage block is a near-verbatim copy of `bulkSet`
(sql.go:393–419): same `rowForEntry` → `goScalar` → `setRowField` → rollback-closure →
`stageScalarSet`. The phantom-value rollback invariant now lives in two copies; a fix to one
(rollback ordering, guard capture) silently misses the other. Extract a shared
`writeThroughAndStage` helper.

### 9. `script/thebibites/sql.go:627` — `wrapExpr` couples to DuckDB's internal error wording
`wrapExpr` classifies unknown-column errors by substring-matching DuckDB's binder wording
(`"Referenced column"`, `"Binder Error"`, `"does not exist"`, `"not found in FROM clause"`).
A DuckDB version that rephrases those errors silently drops the clean diagnostic with no test
failure. The registry already enumerates valid columns — validate referenced identifiers
structurally instead of string-sniffing the driver error.

### 10. `script/thebibites/sql.go:193` — `oneToManyTables()` rebuilt on every `fromClause()`
`oneToManyTables()` reallocates and repopulates a map from the static `entitySubCollections`
on every `fromClause()` call (every aggregate, grouped aggregate, bulk set/set_expr/delete).
Build it once via `sync.Once` like the other registry singletons (`zoneRegOnce`, `subRegOnce`)
rather than per push-down query.

## Checked and dropped (intentional / harmless)
- `entity.go:198` `prune=True` body_id guard — intentional parity with the non-prune path
  (which already rejects a missing body_id), not a regression.
- `raw_json` exclusion from the readable attribute surface — the deliberate fix in `0310bfc`.
- `run.go` `prepareCommit` before `RecordScriptRun` — produces only a harmless orphan blob in
  the content-addressed store; documented and intended.
- `coerceExprResult` `*big.Int`/`uint64` passthrough for a `kindUnknown` column — latent only;
  no `kindUnknown` writable column exists today.
