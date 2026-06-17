# Code review — Chunk 2: host engine + read bindings + analytics (T3 + T4 + T5)

| | |
|---|---|
| **Range** | `review/1-infra` (`b2ec4db`) → `review/2-read` (`edadcb5`) |
| **Scope reviewed** | `script/` (host engine) + `script/thebibites/` read & analytics layer — 2,492 lines (15 files, all new) |
| **Excluded as noise** | `go.mod`/`go.sum`, `DSL_tickets_plan.md` |
| **Method** | high-effort, fanned to 3 subagents (engine+bindings correctness / analytics+registry correctness / cleanup+altitude); two headline findings verified against the code by hand |
| **Verdict** | **Two real correctness bugs to fix before the analytics/`where` surface is trusted (F1, F2).** The rest is latent or quality. The engine, conversions, gene index, lazy-DuckDB, and the metadata-derived registry are otherwise solid. |

This is the foundation chunk — the registry, locator, and push-down machinery every later
chunk reuses — so the findings here have leverage. The design genuinely honors the project's
"derive from generated metadata, never hand-write allowlists" principle. Two bugs and a 1:1
join assumption are the substantive issues; several reuse/duplication items are worth folding
in because they directly touch the buggy code.

---

## Correctness

### F1 — `sum()` over an integer column always errors (every-time failure) · *severity: high*

**`script/thebibites/sql.go:144-145`** (`aggExpr`) + **`convert.go:74`** (`fromSQLValue` default)

`aggExpr` emits a bare `sum(<col>)` with no cast. DuckDB returns **HUGEINT** (128-bit) for a
`sum` over any integer column, and the `github.com/duckdb/duckdb-go/v2` driver scans HUGEINT
into **`*big.Int`**. `fromSQLValue` has cases for every fixed-width int/uint/float but **no
`*big.Int` case**, so it hits `default` and returns `unsupported SQL value type *big.Int` —
the whole aggregate fails.

**Failure scenario:** `save.bibites.sum("species_id")` (also `sum("generation")`,
`sum("body_id")` — all BIGINT) errors every time. `mean`/`median`/`min`/`max`/`count` dodge it
(DOUBLE / native width / BIGINT), and `aggregates.go`'s host-side `sum` over a materialized
Starlark list is a different path — so there is **no push-down-`sum`-over-integer test**, and
the suite stays green over a real, deterministic break. DECIMAL columns would hit the same gap
via `min`/`max`/`sum`, but none exist in the current metadata, so HUGEINT-from-`sum` is the
live path.

**Fix:** either cast in the builder (`CAST(sum(<col>) AS DOUBLE)` / `... AS BIGINT` where it
fits) or — cleaner and more general — add a `*big.Int` (and `Decimal`) case to `fromSQLValue`
that yields a Starlark `Int`/`Float`. See **Q1**: the analytics converter duplicates the
duckdb driver's coercion table, which is exactly why this gap exists in isolation.

---

### F2 — A filtered `EntityCollection` ignores its `where` on iteration, `len()`, and truth-test · *severity: high (silent wrong result)*

**`script/thebibites/collection.go:114-129` (`Len`/`Iterate`), `:31` (`Truth`)**

`whereBuiltin` (`:64`) returns a new collection carrying the predicate, and the type doc
(`:9-19`) says `.where` "narrows the collection." But `Len()`, `Iterate()`, and `Truth()` all
resolve through `identityAccess().order` — the **full** identity table — and never consult
`c.where`. Only `scalarAgg`/`groupedAgg` (the `.count/.mean/...` paths) receive the predicate.

**Failure scenario:** `for b in save.bibites.where("energy > 100"): ...` iterates the **entire**
bibite population; `len(save.bibites.where("..."))` and `if save.bibites.where("..."):` report
the full count/truth. Worse, `.count()` and `len()` on the *same* filtered collection
**disagree** (count honors the filter, len doesn't) — a confusing, silent correctness trap.
Untested: every `Len`/`Iterate` test uses an unfiltered collection.

This is a **known** behavior — `DSL_completion_plan.md` (P2C, chunk 6) states "EntityCollection
iteration ignores the `where` predicate ... walks the identity table's full order" and adds bulk
`where().delete()` to route around it — **but the iteration/`len` footgun itself was never
fixed.** A user reaching for the obvious `for b in coll.where(...)` still gets the whole set.

**Fix:** make `Iterate`/`Len`/`Truth` honor the predicate by resolving matching `entry_name`s
through the same push-down builder. Chunk 6 already adds `matchingEntryNames` in `sql.go` for
bulk delete — wire `Iterate`/`Len` through it so the filtered collection is consistent end to
end. (If a filtered iteration must stay eager-only for a reason, at minimum it should error
rather than silently return everything.)

---

### F3 — Aggregate JOINs assume 1:1 sub-tables with no enforcement; a 1:many sub-table silently inflates results · *severity: medium (latent, format-drift-triggered)*

**`script/thebibites/sql.go:166-187`** (`fromClause`)

The builder `LEFT JOIN`s each referenced sub-table on `(save_id, entry_name)`, relying on a
*comment*-only assumption (`:163-165`) that every `entityTables[kind][1:]` table is exactly 1:1
with identity. If any listed sub-table ever holds >1 row per entity, the join multiplies
identity rows and every `count`/`sum`/`median` over that query is wrong — with no error.

**Failure scenario:** a save-format revision makes, e.g., `bibite_pheromone_emitters` or
`bibite_egg_layers` (`attr_registry.go:47-48`) emit two rows for one bibite; a predicate or
aggregate that pulls in that table now counts/medians duplicated rows. The project's own memory
([[save-format-churn-strategy]]) flags exactly this kind of per-version drift, and the codebase's
stated preference is to fail **loud and localized** rather than silently. Today it's silent.

**Fix:** assert 1:1 cardinality when building the join (or derive 1:1-ness from metadata and
refuse to scalar-join a 1:many table), so a future format change trips a clear error instead of
skewing analytics.

---

### F4 — Override aliasing overwrites `spec.column` with the friendly alias → aliased column emitted as the SQL column name · *severity: low (dormant; fixed in chunk 5)*

**`script/thebibites/attr_registry.go:121-124`**

The override loop sets `spec.column = alias` and stores the spec under `attrs[alias]`,
**discarding the real DuckDB column**. Since `resolveColumn` and `rewritePredicate` build SQL
as `quoteIdent(spec.table) + "." + quoteIdent(spec.column)`, an aliased attribute would emit
`"table"."alias"` — a non-existent column. `resolveColumn` (`sql.go:122-124`) even comments the
hazard. Dormant only because `overrides` is empty today.

**Note:** this is precisely **risk #1** in `DSL_completion_plan.md` P1, fixed in **chunk 5** by
splitting `attrSpec` into a friendly `column` and a `sourceColumn`. Flagging here for
completeness of the chunk-2 record; no action needed if reviewing chunk 5 separately.

---

### F5 — Duplicate `entry_name`s make enumeration count and attribute reads disagree · *severity: low (latent)*

**`script/thebibites/loadedsave.go:128-145`** (`indexByEntryName`)

`tableAccess.order` retains every row (length N) while `byEntry` is last-wins on collision.
Iteration yields N distinct `Entity` values, but colliding ones resolve every scalar attribute
through `byEntry` to the **last** duplicate's row, and `len()` over-counts vs distinct
identities. `EntryName` is normally the unique archive entry filename, so this is unlikely to
fire — latent inconsistency worth a guard or a dedupe-on-load assertion.

---

### F6 — Unterminated string/identifier literal in a `.where()` predicate is swallowed, degrading the diagnostic · *severity: low*

**`script/thebibites/sql.go:415-431`** (`copyQuoted`)

On an unterminated quote, `copyQuoted` returns `len(s)`, so the tokenizer consumes the
remainder as an opaque literal and emits a malformed clause. `save.bibites.where("name = 'foo")`
reaches DuckDB as a broken `WHERE`, producing a low-level parser error instead of a clean
"unterminated string literal in predicate." Not a wrong-result or injection bug (DuckDB still
rejects it) — a diagnostic-quality edge. *(The reviewer specifically hunted the
`rewritePredicate` tokenizer for misqualification/double-qualification/injection across string
literals, `''` escapes, quoted idents, function calls, dotted refs, and keyword/column
collisions, and found none — identifiers are only ever replaced by hard-quoted
`table.column` pairs from generated metadata, and `save_id` is always parameterized. This is the
one rough edge.)*

---

### F7 — `MaxExecutionSteps == 0` silently disables the step budget · *severity: low (informational / footgun)*

**`script/engine.go:61-67`**

When `opts.MaxExecutionSteps` is 0 (the zero value), neither `SetMaxExecutionSteps` nor
`OnMaxSteps` is installed, so there is **no** step limit — only context cancellation can stop a
runaway script. `Run(ctx, program, globals, Options{})` with a non-cancelable `ctx` can hang.
This is the documented "0 = unlimited" convention, but `Options` makes the budget look opt-out
rather than required; consider a sane default or a doc-comment warning at the call site, since
hermetic step-bounding is a stated reason for choosing Starlark.

---

## Quality (reuse / simplification / altitude)

These don't change behavior, but **Q1 is entangled with F1** and worth doing together.

### Q1 — DuckDB SQL/coercion primitives are duplicated across `script/thebibites` and `duckdb`
- `quoteIdent` (`sql.go:21-23`) is a character-for-character copy of `duckdb.quoteIdent`.
- `fromSQLValue` (`convert.go:40-77`) re-implements `duckdb.normalizeSQLScanValue`'s driver-scalar
  coercion table — and **its copy is incomplete (missing `*big.Int`), which is the root of F1.**
- The metadata→reflect-index plumbing (`attr_registry.go:105-118`, `loadedsave.go:113-121`)
  re-derives what `duckdb.buildFieldIndices` already does from the same metadata — now written
  three times, and it's the trickiest reflection in the change.

**Cost:** SQL-safety + driver-coercion logic lives in two packages that both emit DuckDB SQL and
can drift silently (tests are green on today's driver). Export and reuse from `duckdb`; let
`fromSQLValue` delegate the scalar coercion and then do a small Starlark switch — which also
fixes F1 centrally.

### Q2 — minor simplification / dead scaffolding
- Redundant `flushMirror`: `query()` calls it at `sql.go:37-45` but `openDB` already flushes
  (`loadedsave.go:207`) — a confusing double-call of an ordering primitive (cheap today; a
  maintenance trap once T6 fills the buffer).
- The quantile `q ∈ [0,1]` check and the `{sum,mean,median,min,max}` method set are copy-pasted
  between `EntityCollection` (`collection.go:48,91`) and `GroupedCollection` (`:180,213`); a
  shared helper parameterized by the runner removes the drift risk.
- `attrCategory` is a single-variant enum with an unreachable `default` arm in `entity.go`
  (`attr_registry.go:14-20`) — intentional seam for later sub-collection categories; acceptable,
  noted for the record.
- `saveFieldByTable` (`loadedsave.go:97`) is derivable from the registry's `specByTable`
  (`spec.SaveField`); one of the two table indexes is unnecessary.

---

## Confirmed-clean (checked, no issue)
Lazy DuckDB open is idempotent and reparse-free (`dbOpenCount++` once); `flushMirror` is a
correct no-op when not dirty; aggregate paths never reach the `rowsMaterialized` iterator;
`fromSQLValue` NULL→None and `group_by` NULL-key→None shaping are correct; every `*sql.Rows` is
`Close`d and `Err()`-checked; `toStarlark`/gene index/`EvalError` innermost-frame backtrace all
check out; the `rewritePredicate` tokenizer has no injection or misqualification path (F6 aside).

## Suggested action
Fix **F1** and **F2** before the analytics/`where` surface is relied on — both are silent,
deterministic, and untested. Do **Q1** alongside F1 (same code, removes the root cause). **F3**
deserves a cheap loud-failure guard given the project's format-drift posture. **F4** is already
handled in chunk 5; **F5–F7** are fine to defer.
