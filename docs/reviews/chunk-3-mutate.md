# Code review — Chunk 3: scalar mutation + mirror + commit/provenance (T6 + T8)

| | |
|---|---|
| **Range** | `review/2-read` (`edadcb5`) → `review/3-mutate` (`15afac1`) |
| **Scope reviewed** | delta over chunk 2 — 1,426 lines (11 files; new: `mirror.go`, `run.go`) |
| **Excluded as noise** | `DSL_tickets_plan.md` |
| **Method** | high-effort, 3 subagents (T6 mutation/mirror correctness / T8 commit/provenance correctness / cleanup+altitude); the two structural findings re-anchored and verified against the code by hand |
| **Verdict** | **One real provenance-integrity bug (F1) worth fixing; one narrow read-after-write-lie (F2); the rest are edge cases and a clear reuse/altitude cleanup theme.** The core mutation mechanics — stale-guard ordering, flush batching, mirror type-homogeneity, `ensureApplied` idempotency — verified **correct**. |

The mutation design is sound and the agents confirmed the things most likely to be wrong
(guard ordering, double-apply, batched flush) are right. The substantive issues are in the
*lifecycle around* the mutation: provenance recorded before the commit it describes, and an
in-memory write that lands before the staging that authorizes it.

---

## Correctness

### F1 — Provenance is recorded "succeeded" *before* the commit, and never corrected if the commit fails · *severity: high*

**`script/thebibites/run.go:78-107`** (`runLoaded`)

`RecordScriptRun` (`:78`) persists `status="succeeded"`, `dry_run=recordedDryRun` (false on a
normal committing run), `staged_ops=N`. Only afterward does `if willWrite { ls.Commit(...) }`
(`:99`) run, and on any `Commit` error the function does `return res, err` (`:102`) with **no
compensating update** to the run row. There is no transaction spanning the run record and the
revision.

**Failure scenario:** a script stages a mutation and runs cleanly, but `Commit` fails —
`blobs.Put` errors (disk full / store down), the stale-value guard trips inside
`ensureApplied`→`Apply`, or `RecordRevision` errors. The `script_runs` row is now permanently
`status="succeeded", dry_run=0, staged_ops=N` with **no `save_revisions` row**. Any consumer
that reads "succeeded + non-dry + staged_ops>0" as "produced a revision" — which is precisely
the `willWrite` predicate at `:76` — sees a phantom commit. (Compounds with **F6**: the
`dry_run` flag can't disambiguate it either.)

**Fix:** make the recorded status reflect the actual outcome. Either (a) do the fallible work —
`WriteArchiveTo` + `blobs.Put` — *before* `RecordScriptRun`, then record the run with a status
that reflects whether the bytes were produced, then `RecordRevision`; or (b) add an
`UpdateScriptRun(status,…)` to `revisionstore` and set `"failed"`/`"commit_failed"` on the
`Commit` error path; or (c) wrap run + revision in one SQL transaction. (a) keeps the FK order
(run before revision) while making "succeeded" truthful, since an orphan content-addressed blob
is harmless.

---

### F2 — `SetField` writes the in-memory row through *before* staging, so a rejected stage leaves a phantom value · *severity: medium*

**`script/thebibites/entity.go:103` (write-through) precedes `:112` (`StageSQLSet`)**

Order in `SetField`: capture `old` (`:95`, a value copy — guard is correct) → `setRowField`
mutates `ls.tables` in memory (`:103`) → `StageSQLSet(ref.WithExpected(old), staged)` (`:112`).
If staging fails, the function returns at `:113` **without rolling back the `:103` write**, and
`stagedOps`/`recordMirror` (`:115-116`) are correctly skipped — so memory now holds a value no
staged op backs.

**Failure scenario:** a script does `save.commit(path)` (which applies the session via
`ensureApplied`), then assigns `b.energy = 9`. `setRowField` mutates the in-memory bibite;
`StageSQLSet` returns "cannot stage after apply"; the assignment surfaces an error, but a later
plain `b.energy` read returns **9** — a read-after-write that lies, backed by nothing in any
produced save. `bulkSet` has the same write-through-then-stage ordering per row (**F5**).

**Fix:** stage first, then write through on success; or capture-and-restore the field on a
stage error. Narrow today (requires post-apply mutation in one script), but it's a
silent-inconsistency footgun.

---

### F3 — Bulk `where().set()` skips the value-kind check on a zero-match predicate → inconsistent accept/reject · *severity: medium*

**`script/thebibites/sql.go` (`bulkSet`)**

Pre-query validation checks only `spec.writable` and that the value is *some* scalar
(`fromStarlark`). The actual **kind** check (float vs int vs bool vs string for the target
column) lives only in `setRowField`, which runs *per matched row*. So a type-wrong constant is
rejected when the predicate matches rows but **silently accepted (0 staged, no error)** when it
matches none.

**Failure scenario:** `save.bibites.where("species_id == 999999").set("energy", "oops")` (no
such species) returns 0 and "succeeds," masking a type error that the identical call over a
real species would raise. Validate the value against the column kind **once, before the query**
(the kind is known from `spec` without any row), matching how T10's guard layer later validates
bulk sets pre-query.

---

### F4 — `script.Run` has no panic recovery, so a panicking builtin skips `RecordScriptRun` entirely · *severity: medium-low*

**`script/engine.go` (no `recover`) → `script/thebibites/run.go:78`**

The "record the run on every exit" intent holds for script *errors* (returned as values) but
**not for a Go panic**: `starlark.ExecFileOptions` isn't wrapped in `defer/recover`, and
`runLoaded` records the run only *after* `script.Run` returns normally. A panic in any host
builtin unwinds past `:78`, so there is no provenance trace of the attempt at all — the opposite
of the intent. Low likelihood today (builtins reflect over generated value-structs), but the
invariant is structurally unguaranteed. **Fix:** a `defer`/`recover` in the engine that converts
a panic into a `RunError`, or a `defer` in `runLoaded` that records a `"panicked"` run.

---

### F5 — `bulkSet` partial failure leaves memory write-throughs and `stagedOps` half-applied · *severity: low*

**`script/thebibites/sql.go` (`bulkSet` loop)**

Per matched row the loop does `setRowField` (write-through) + `StageSQLSet` + `stagedOps++` +
`recordMirror`. A mid-loop stage error returns immediately, leaving rows `0..k-1`
written-through and staged, row `k` written-through but **not** staged, and `stagedOps`
counting only `0..k-1`. Nothing wrong *persists* (an erroring run never commits and `Apply` is
atomic), but the in-memory `LoadedSave` is internally inconsistent, so any continued in-run
reads after catching the error observe partial, un-staged values. Same write-through-before-stage
class as **F2**.

---

### F6 — `dry_run` records intent, not outcome → a failed staged run is `dry_run=0` with no revision · *severity: low (semantic)*

**`script/thebibites/run.go:75`** (`recordedDryRun := opts.DryRun || !ls.willCommit`)

The design says `script_runs.dry_run` means "this run produced no revision." But a run that
staged ops and then *failed* (`runErr != nil`) has `recordedDryRun == false` (default
`willCommit`) and yet produces no revision. So `dry_run` alone can't mean "no revision exists" —
a consumer must also check `status`. Pairs with **F1** to make the run row ambiguous. Also note
**`blobs == nil` is only validated inside `Commit`** (after the run is already recorded), not
up-front in `runLoaded` (which guards `revs == nil` only) — a nil blob store is a config error
that still records a phantom "succeeded" run before failing. Reconcile the flag's documented
meaning, and fail fast on `blobs == nil`.

### F7 — latent: `setRowField` integer width is unchecked · *severity: low (latent)*

`reflect.SetInt`/`SetUint` don't range-check against a sized field kind. Harmless **today** —
every writable entity column resolves to `int64`/`float64`/`bool`/`string` (no `int8/16/32` or
`uint*` writable attrs) — but if a future generated writable column is a sized/unsigned integer,
an out-of-range set wraps silently and stages a corrupted value instead of erroring. Worth a
range check now given the project's format-drift posture ([[save-format-churn-strategy]]).
Related: an id-less bibite (`has_body_id == false`, set by the parser with a diagnostic) is
queryable by `entry_name` but **cannot be mutated** — `entityLocatorRef` builds `HasBodyID=false`
and the mutator rejects it; a bulk `set` over a species containing one aborts the whole batch at
that row.

---

## Quality — one dominant theme + two nits

### Q1 — The scalar-set path hand-reimplements the locator + stale-guard the bulk path gets for free
`entity.go`/`loadedsave.go` build the `SQLValueRef` by reflecting `body_id`/`egg_id`/`has_*`
out of the in-memory row with a bespoke per-kind switch and new reflection helpers
(`entityLocatorRef`, `rowInt64Field`/`rowBoolField`), and manufacture `old` via `goScalar`. The
bulk path derives the **identical** ref + guard generically from query columns via the existing
`duckdb.ScanSQLRefs` (`sqlref_scan.go`), which already infers exactly these locator fields and
the current value. Cost: ~58 lines of net-new locator/guard plumbing that duplicates
`ScanSQLRefs` and must be kept in sync field-for-field as entity kinds/guards grow; the
`attrRegistry()` re-lookup + second row fetch per set (`entity.go:84-116`) and the `goScalar`
coercion table (`convert.go`) are facets of the same duplication. Consider routing the scalar
set through the same `ScanSQLRefs`-derived ref.

### Q2 — Two hardcoded `bibite`/`egg` id-column switches, against the "derive from metadata" rule
`sql.go` (`locatorSelect`) and `loadedsave.go` (`entityLocatorRef`) both `switch kind` to name
`body_id`/`egg_id`/`has_body_id`/`has_egg_id` — columns that are already ordinary
`attrRegistry`/`NormalizedTables` entries. This is the hand-maintained allowlist the project's
own memory ([[sqlref-generation-philosophy]]) warns against; every new entity kind means editing
two switches. Project the locator-column set from the registry instead.

### Q3 — small cleanups
- `mirrorDirty` (`loadedsave.go`) is redundant state fully derivable from `mirror.empty()`; the
  flag and the buffer can never disagree (a leftover T5 seam). `!ls.mirror.empty()` replaces it.
- **Cross-chunk note:** this chunk *deleted* the `copyQuoted` helper and inlined its body into
  both quote cases with a `goto nextToken` — a readability regression over chunk 2, and it's the
  exact function chunk 2's **F6** (unterminated-literal diagnostic) flagged. Fix both together:
  restore a small helper that also reports the unterminated-quote case.
- The mirror's `entry_name`-only key is a **deliberate T6 seam**, generalized to an N-ary
  locator key in chunk 5 (P1 1b) — not an oversight.

---

## Confirmed-clean (checked, no issue)
Stale-guard ordering (`old` captured before write-through); `set→read→set→query` guard chaining;
flush batching (N sets → one `UPDATE` per column; `flushStmtCount==1`); mirror type-homogeneity
+ last-write-wins by `entry_name`; `None` rejected before reaching the VALUES relation;
`ensureApplied` idempotency (no double-apply across `save.commit` + host `Commit`; `Apply`
atomic); verify path (exactly one reparse, temp file `defer`-cleaned, whole-file SHA vs blob
ref); churn counters (1 `WriteArchive`, 0 reparse, 0 DuckDB open on a pure-mutation commit);
`autocommit` default + single-threaded intent; the writable/unknown/non-scalar gate.

## Suggested action
Fix **F1** (the only data-integrity issue — phantom provenance) and decide on **F2/F5**
(stage-before-write-through, or restore on stage failure). **F3** is a quick pre-query kind check
that T10 will want anyway. **F4** is a cheap `defer`/`recover`. **F6/F7** are low. The **Q1/Q2**
reuse/altitude cleanup is worth doing while this code is fresh, since later chunks keep extending
both the scalar path and the per-kind switches.
