# Code review — Chunk 4: validation guards + structural delete/array (T10 + T11a + T11b)

| | |
|---|---|
| **Range** | `review/3-mutate` (`15afac1`) → `review/4-structural` (`fdc5bf4`) |
| **Scope reviewed** | delta — 2,035 lines (15 files; new: `guards.go`, `subcollection.go`, `delete_test.go`; the **only** chunk touching the tested `savemutator`) |
| **Method** | high-effort, 3 subagents (T10 guards / T11 structural binding / `savemutator` change). The `savemutator` review is snapshot-exact; the `script/thebibites` reviews ran against the merged HEAD tree (same code line — a few line refs point at the final-state file, but the flagged logic originates in this chunk) |
| **Verdict** | **One real structural-safety bug (F1) worth fixing; two medium guard/consistency gaps; the high-risk `savemutator` species cascade verified correct.** |

The headline reassurance: the change everyone worried about — the `activeSpeciesList`
reconciliation bolted onto the tested delete cascade — is **correct** under hand-tracing of
every edge (last member, batch delete, egg-as-last-member, species-less archive), and the
`synapseArrayResolver`→`entityBrainArrayResolver` generalization preserves prior synapse
behavior exactly. The bugs are in the binding layer's structural-delete safety and the new
guard's value domain.

---

## Correctness

### F1 — Synapse element-delete stale guard is a boolean (`enabled`), too weak to catch a shifted index — contradicts the stated safety contract · *severity: high*

**`script/thebibites/subcollection.go:240-247` + `attr_registry.go:354-357`**

`guardColumn = sc.writableCols[0]` is chosen **alphabetically**. For synapses the sorted
writable set is `[enabled, innovation, node_in, node_out, weight]`, so the stale guard is
`enabled` — a bool, and nearly every synapse is `enabled=true`. The `subcollection.go:22-26`
header promises "a shifted index fails loudly rather than removing the wrong element," but a
two-valued guard can't distinguish a shifted element from its neighbor.

**Failure scenario:** a script deletes two elements of the same array in one run —
`b.synapses[2].delete(); b.synapses[3].delete()`. Both stage against the `subRowIdx` built
once at load (never updated after a non-mirrored structural stage). At commit the mutator
applies them in stage order with **no index re-base** (positional `brain.Synapses[i]`): the
first delete shrinks the array, so the second's path now addresses what was originally index 4.
The `enabled` guard catches this only if original-index-4's `enabled` differs from the captured
value — usually it doesn't, so the **wrong synapse is silently deleted**.

**Fix:** pick a high-cardinality guard column (`weight` or `innovation`) rather than the
alphabetical first; or re-base indices when staging multiple deletes against the same array (or
forbid it). Precondition is multi-delete-by-index on one array — plausible in real brain edits.

---

### F2 — Prune-bibite delete drops the `has_body_id` guard the non-prune path enforces · *severity: medium*

**`script/thebibites/entity.go` (the prune branch of the entity-delete builtin)**

The non-prune path stages `StageSQLDelete(ref)` → `bibiteTargetFromSQLRef`, which calls
`requireSQLRefFlag(ref, ref.HasBodyID, "body_id")`. The prune path builds
`mutator.BibiteRef{EntryName, BodyID: ref.BodyID}` and **never consults `ref.HasBodyID`**, even
though `entityLocatorRef` populated it.

**Failure scenario:** a bibite whose identity row has `has_body_id=false` (so `BodyID` defaults
to 0). `b.delete(prune=True)` stages a delete keyed on `body.id == 0`, while the same entity
with `prune=False` is rejected up front for a missing body id. Inconsistent guarding for a
malformed/edge entity — the prune path proceeds with a zero-value guard instead of failing
loudly. **Fix:** check `ref.HasBodyID` on the prune path too.

---

### F3 — NaN/Inf floats bypass every numeric guard and fail opaquely at commit · *severity: medium*

**`script/thebibites/guards.go` (`validateValue` Min/Max block)**

For a `Min:0` column (`energy`, `health`, …) the range check is `f < *r.Min`. With `f = NaN`,
both `NaN < 0` and `NaN > Max` are `false`, so NaN passes; `+Inf` likewise (these columns have
no `Max`). `asFloat64(NaN/Inf)` returns ok, so the type check passes too, and `setRowField`
happily `SetFloat`s it.

**Failure scenario:** `b.energy = float("nan")` (or `float("inf")`, `1e308*10`) passes the
`energy >= 0` guard and stages; the commit path (`json.Marshal`) then aborts the whole
`save.commit()` with `json: unsupported value: NaN`, surfaced far from the offending assignment
— exactly the localization the guard exists to provide. A finite negative is caught; a
non-finite one isn't. **Fix:** reject non-finite floats in `validateValue` (`math.IsNaN/IsInf`).

---

### F4 — latent: `asInt64` accepts out-of-range integral floats for BIGINT columns · *severity: low*

**`script/thebibites/convert.go` (`asInt64`, shared by guard and writer)**

`validateValue`'s `kindInt` path accepts any float with `Trunc(x)==x`, including `1e19`
(> MaxInt64); `int64(1e19)` is an implementation-defined out-of-range conversion. Guard and
writer use the same `asInt64`, so they *agree* on the garbage — not a guard/writer mismatch, but
the guard doesn't reject a clearly out-of-range integer. `b.generation = 1e19` stages a
silently-truncated value. Pre-existing in `asInt64`; T10 inherits it. Add a range check.

---

### F5 — latent: positional `.index` desyncs from JSON position if the parser skipped a malformed array element · *severity: low (defensive)*

**`script/thebibites/subcollection.go:70-77` + `loadedsave.go:260-265`** (parser:
`parse_brain.go`)

The binding stamps the *slice* position as the array ordinal the mutator uses for
`brain.Synapses[i]`. The parser builds the slice in array order but `continue`s past any element
that fails `asMap`, so slice position no longer equals JSON array position. A save with a
non-object entry at `Synapses[1]` makes `b.synapses[1].delete()` target the wrong JSON element.
Edge/defensive — normal saves have all-object arrays.

---

### F6 — `removeActiveSpecies` hard-fails instead of degrading on an undecodable `speciesData.json` · *severity: low*

**`savemutator/thebibites/session.go:583-590`**

The helper's doc promises a quiet degrade when the save "has no species entry, no
activeSpeciesList, or the id is already absent." The entry-absent case is handled, but if
`speciesData.json` is *present with `JSON == nil`* (parser kept it after a `json_decode_failed`
diagnostic), `entryUpdate` returns "entry does not have decoded JSON" and **aborts the entire
delete Apply**. Deleting the last member of any species on such a save fails the whole batch.
Consistent with the pre-existing `adjustSceneCount` hard-fail (a known pattern, not a
regression), so low — but a one-line `entry.JSON == nil → return nil` guard would honor the
"degrade quietly" contract.

---

## Verified correct (the high-risk surface)

The `savemutator` change — the reason this chunk was flagged highest-risk — checks out:

- **Species "last living member"**: `speciesHasOtherMembers` excludes the current entry
  (`skipName`) and prior deletes (the accumulating `removed` set), and the species check runs
  *before* appending to `removed`. Hand-traced batch delete of both members (both orders) →
  species dropped exactly once, order-independent. Two-member species, one delete → correctly
  not dropped. No index skew in `removeActiveSpecies` (`activeSpeciesIndexOf` re-reads the live
  shortened slice each call).
- **Egg as last member**: both `BibiteRow`/`EggRow` carry `genes.speciesID`; `entitySpeciesID`
  resolves it for eggs; test confirms.
- **Species-less / empty-list**: clean no-op, no nil-deref.
- **Resolver generalization**: `entityBrainArrayResolver(kind, resolve)` == old
  `synapseArrayResolver` behavior; synapse table/index field unchanged; no dangling
  `synapseArrayResolver` references.
- **Array targets**: nodes → `brain.Nodes`/`node_row_index`; stomach → `body.stomach.content`/
  `content_index` (bibite-only); `SceneCount` set only for pellets — nodes/synapses/stomach
  carry none, and pellets' `nPellets` path is untouched. 3 new switch cases route correctly, no
  fallthrough.

And in the binding layer: structural ops bump `stagedOps` but **never** `recordMirror` (only
scalar `SetField` mirrors — the in-run invisibility invariant holds); `identityTable` mapping;
egg vs bibite locator fields; prune is a correct no-op for eggs; append requires the complete
writable set and rejects unknown/read-only/wrong-type kwargs pre-stage; `SetField` rejects
sub-collection names and `delete`; `AttrNames` lists them; unknown sub-attr → clean miss. T10:
type derivation matches `setRowField` for every writable column; all 24 `semanticRules` keys are
live writable columns; `bulkSet` validates once per column pre-query (rejects on zero matches);
enum mechanism correct.

## Suggested action
Fix **F1** (silent wrong-element delete — the one with data-loss potential) and **F2** (prune
guard parity); both are small. **F3** (reject non-finite floats) is a one-liner that meaningfully
improves diagnostics. **F4–F6** are low/latent. The species cascade needs no changes.
