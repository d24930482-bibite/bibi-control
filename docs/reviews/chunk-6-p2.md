# Code review — Chunk 6: zones + pellets + bulk delete (P2 / P2B / P2C)

| | |
|---|---|
| **Range** | `review/5-p1` (`e0b15d4`) → `review/6-p2` / `DSL_dev` HEAD (`554f8bf`) |
| **Scope reviewed** | delta — 1,962 lines (12 files; new: `zones.go`, `pellets.go`) |
| **Method** | high-effort, 3 subagents (zones correctness / pellets+bulk-delete correctness / cleanup), scoped to the snapshot via the in-root worktree |
| **Verdict** | **No high-severity bug. One medium correctness finding (pending-pellet coercion); several low/by-design items on the zone structural surface; the dominant quality issue is the scalar-set lifecycle now copy-pasted 4×.** The load-bearing safety mechanisms — the zone-id stale guard, clone deep-copy isolation, `allocZoneID` uniqueness, predicate-scoped bulk delete — all verified correct. |

This is the last and broadest binding chunk. Reassuringly, the things most likely to corrupt a
save — a clone aliasing the live archive, a stale index deleting the wrong zone, bulk delete
silently deleting everything — are all handled. The findings are a type-fidelity gap on the
pending-pellet path and a cluster of low/by-design edges on the zone structural surface.

---

## Correctness

### F1 — Pending-pellet scalar writes skip type coercion, staging JSON ints into DOUBLE fields · *severity: medium*

**`script/thebibites/pellets.go:343`** (`PendingPellet.SetField`)

`PendingPellet.SetField` writes the raw `goVal` from `fromStarlark` straight into the cloned JSON
via `setNestedPellet(pp.data, spec.jsonKey, goVal)` with **no coercion to the field's type**.
Every pellet scalar (`amount`, `transform.*`, `matterDecay.*`, `rb2d.*`) is a `DOUBLE`, and
`validateValue`'s `kindNumber` accepts a Starlark int. The *committed* path
(`Pellet.SetField → setRowField`) coerces the same int to `float64` before staging.

**Failure scenario:** `p = save.pellets.clone(0); p.amount = 5; p.append(zone="World")` stages a
pellet whose JSON serializes `amount` as integer `5`, whereas `save.pellets[i].amount = 5`
stages `5.0` — the two pellet paths diverge in on-disk JSON type fidelity for any integral
value. Mitigating: this matches the pre-existing sub-collection append (`subcollection.go` also
writes raw `goVal`), so it's a package-wide pattern, not a pellet regression, and JSON number
parsers are usually tolerant. **Fix:** coerce through the field type (or share the committed
path's `setRowField`), keeping the two pellet paths identical — see **Q1** (the pending and
committed write paths should converge anyway). This is the same shape as chunk 6's quality
theme and chunk 5's `Pending` vs committed converter divergence.

---

### F2 — `raw_json` is exposed as a readable zone attribute · *severity: low*

**`script/thebibites/attr_registry.go` (`zoneRegistry` via `tableScalarSpecs("settings_zones")`)**

`tableScalarSpecs` excludes only `save_id`, so the `raw_json` column (no `SQLRefPath`, not
writable) becomes a readable attribute: `save.zones[i].raw_json` returns the entire serialized
zone blob as a string. Harmless (read-only) but an unintended surface that widens the contract
and can expose format-version internals. Exclude `raw_json` explicitly like `save_id`. (Worth
checking the pellet/entity registries for the same leak.)

---

### F3 — Zone-scoped `.values` writes carry no zone-id stale guard · *severity: low (narrow)*

**`script/thebibites/settings_value.go:190-203`** (ref omits `HasZoneID`)

`setSettingValue` builds the `SQLValueRef` without `ZoneID/HasZoneID`, so a zone *value* write is
protected only by the old-value `Expected` guard, not the zone-id guard the zone *scalar* path
uses. The absolute `Path` is parse-fixed so it's safe in practice, but under a staged structural
zone delete that shifts the array, `save.zones[0].delete()` then
`save.zones[2].values["fertility"].set(...)` could resolve against the shifted index and write
the wrong zone's value *if* the old value happens to match at the shifted position. Narrow;
consider carrying the zone-id guard on value writes for parity with scalar writes.

### F4 — Structural delete + a later set/delete in the same run fails at commit, not at script time · *severity: low (by-design UX)*

Structural deletes aren't mirrored in-run (the documented consistency contract), so
`save.zones[0].delete(); save.zones[2].name = "x"` looks fine mid-run but at Apply the delete
shifts indices and the set's zone-id guard fires loudly ("is missing" / id mismatch). This is
**correct** — no silent corruption, the guard catches every shift variant traced — but the error
surfaces at commit and doesn't obviously point back at the earlier delete. Same pattern applies
to pellets. Worth a doc note, or detecting a staged delete-then-index-set in the same run.

### F5 — minor: `PendingPellet` reads of locator columns return stale template metadata; failed append double-allocates a zone id · *severity: low / cosmetic*

- `PendingPellet.Attr` reads locator columns (`zone`, `group_index`) off the template row
  (`pp.src`), so `p.zone` reports the template's zone, not the `append(zone=…)` target (read-only
  confusion, no corruption). (`pellets.go:299-305`)
- `allocZoneID` mutates `pz.data["id"]` before `StageSQLAppend`; a staging error leaves
  `appended=false`, and a retried `append()` allocates a *second* id. Ids stay unique — just a
  skipped value in the sequence. (`zones.go:340-348`)

---

## Quality

### Q1 — The scalar-set lifecycle is now copy-pasted 4× (and the pending-set prologue twice) · *simplification (cross-chunk)*
`Zone.SetField`, `Pellet.SetField`, `Entity.SetField`, and `bulkSet` all replicate the same
8-step sequence (`goScalar(old)` → `fromStarlark` → `validateSet` → `setRowField` →
`StageSQLSet(WithExpected)` → `stagedOps++` → `recordMirrorRow`), varying only in the locator.
Separately, `PendingZone.SetField` and `PendingPellet.SetField` are line-for-line identical
except the final write (flat key vs nested path). This is the **same duplication chunk 5 Q2
flags** for `setGeneValue`/`setSettingValue` — by HEAD there are ~6 copies of one lifecycle. A
`LoadedSave.stageScalarSet(spec, rv, goVal, refTemplate, locators)` helper plus a pending-set
helper taking a write closure would absorb both. This is the highest-value cleanup in the
chunk because it also makes one-line fixes like **F1** and chunk-5 **F1** land in a single place.

### Q2 — small altitude/efficiency items
- The `transform_*` alias triple (`position_x/y`, `rotation`) is hand-listed in **three** places:
  `overrides["bibite"]`, `overrides["egg"]`, and `pelletOverrides` (`attr_registry.go`). One
  shared `transformAliases` map merged into each would make it one place (the columns are still
  generated; only the short alias is hand-written — same "list it once" spirit as chunk 5 Q3).
- `allocZoneID` on an **id-less** save returns early without marking its ready flag, so it
  re-scans `SettingsZones` on every `append()` instead of once. One-line fix.
- `Pending*` reads use `fromSQLValue` while committed handles use `toStarlark` — correct today
  (different value sources) but a latent read-back skew if the converters drift; a shared
  any→Starlark normalization removes the risk (ties into **F1**).
- The isolated nested-path setter (`parsePelletPath`/`setNestedPellet`) is **correctly** kept
  local (it mirrors the mutator's private path logic and nothing else needs it) — no change; only
  `getNestedPellet`/`setNestedPellet` could share their step-walk core.

---

## Verified correct (the load-bearing surface)
- **Bulk delete is predicate-scoped**: `matchingEntryNames` runs the same rewrite/from/where
  builders as `bulkSet` and returns only matches; all `entityTables` sub-tables are strictly 1:1
  and `rewritePredicate` only resolves 1:1-table columns, so the LEFT JOINs can't duplicate
  `entry_name`s — each match staged exactly once, order-independent. (Empty predicate → all,
  i.e. unfiltered `.delete()`.) `stageEntityDelete` is identical for `Entity.delete()` and the
  bulk path.
- **Clone deep-copy isolation** (both zone and pellet): a fresh `json.Unmarshal` of the
  template's retained `RawJSON`/`Raw`, fully detached; edits touch only the clone; the mutator's
  `appendJSONArray`/`normalizeJSONValue` deep-copies at apply, so the live archive is never
  aliased. Unset fields correctly inherit the template.
- **`allocZoneID`** is collision-free including two clones in one run (lazy max+1 seed + monotonic
  counter); the zone-id stale guard pins expected id to index and fires loudly on any
  delete-induced shift (verified across set/delete variants).
- **`pelletGroupByZone`**: unknown zone → loud error listing options; ambiguous zone → error, not
  silent first-match; returns the group index the mutator's `pelletAppendTarget` indexes; zone
  doubles as the stale guard. Pellet `(group_index, group_pellet_index)` locators are unique;
  delete carries a `material` stale guard and routes `SceneCount = nPellets`.
- **Structural-not-mirrored** holds for pellet append/delete and bulk delete (only scalar sets
  mirror).

## Suggested action
Fix **F1** (coerce pending-pellet writes) — ideally by doing **Q1** (converge the pending and
committed scalar-set paths), which also closes chunk-5 F1's blast radius. **F2** is a one-line
registry exclusion. **F3/F4/F5** are low/by-design — worth a doc note on the in-run-delete-then-edit
ordering. The zone/pellet/bulk-delete safety mechanisms need no changes.
