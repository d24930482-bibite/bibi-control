# Code review — Chunk 5: settings R/W + gene writes + alias fix + mirror generalization (P1)

| | |
|---|---|
| **Range** | `review/4-structural` (`fdc5bf4`) → `review/5-p1` (`e0b15d4`) |
| **Scope reviewed** | delta — 1,491 lines (16 files; new: `settings_value.go`, `alias_test.go`, `gene_write_test.go`) |
| **Method** | high-effort, 3 subagents (alias+mirror infra / settings+gene correctness / cleanup), all scoped to the snapshot via the in-root worktree + `git show review/5-p1:…` |
| **Verdict** | **Clean, metadata-aware infra work. One medium correctness finding (gene mirror discriminator) + one low; one newly-introduced efficiency regression; the rest is quality.** The two infra refactors (alias split, mirror generalization) verified correct at the right depth. |

This chunk is the one that **closes the alias hazard** flagged dormant in chunks 2 (F4) and 3
(Q1): the `attrSpec.column → sourceColumn` split is applied consistently, and `b.position_x = v`
now stages/mirrors/persists against `transform_position_x` end-to-end. The mirror was
generalized from an `entry_name`-only key to a composite locator — a genuine generalization, not
a fork. The substantive issue is a discriminator choice in the *gene* mirror that the *settings*
mirror got right.

---

## Correctness

### F1 — Gene mirror is keyed by `gene_name`, but the unique key is `path`; a leaf-name collision across the two gene nesting levels corrupts the in-run mirror · *severity: medium*

**`script/thebibites/gene.go:161`** (`recordMirrorRow` discriminator)

Genes are flattened from **two** JSON levels into one `bibite_genes`/`egg_genes` table:
`appendGeneRowsFromEntityGenes` emits top-level `genes.<key>` and `appendGeneRowsFromMap` emits
nested `genes.genes.<key>`, **both into the same table sharing one `gene_name` namespace** (the
schema-fingerprint golden confirms the split is live: `genes.gen/isMutant/parent1/speciesID`
alongside `genes.genes.*`). The committed write targets one specific JSON `path`
(`SQLValueRef{Path: row.Path}` → resolved as `ref.Path`), but the mirror keys only on
`(entry_name, gene_name)`.

**Failure scenario:** a save where a top-level gene field and a nested gene share a leaf name
`X` (precisely the kind of per-version drift [[save-format-churn-strategy]] warns about).
`b.genes["X"] = v` stages a commit to the single path `byName["X"]` resolves to, but the in-run
mirror UPDATE `WHERE gene_name = 'X'` **rewrites both** the `genes.X` and `genes.genes.X` rows in
DuckDB. An in-run `save.sql` then shows both mutated (one of them wrong) while only one persists
on commit — in-run vs committed divergence. **Fix:** key the gene mirror on `path` (already on
`GeneRow`, a real column), exactly as the settings mirror does.

---

### F2 — Unknown `material("X")` surfaces as a `KeyError` on the setting key, masking the bad material name · *severity: low*

**`script/thebibites/settings_value.go`** (`materialBuiltin` → `SettingScope.Get`)

`material("X")` builds a `SettingScope{ownerID: name}` without validating that the material
exists; the miss only surfaces on the first subscript as a `KeyError` for the *setting* key.
`save.settings.material("Plnat")["energy"]` raises `KeyError: "energy"`, pointing the script
author at the wrong identifier. Clean (no panic), but a `material("X")` that names no material
should error on `X`.

---

## Quality

### Q1 — `GeneCollection.Len()` now allocates a full copy of every gene row just to count (newly introduced by this diff) · *efficiency*
**`gene.go:180` → `rows()` (`:72`)**. P1 changed `rows()` from returning `set.order` to
`make([]tb.GeneRow, …)` + a copy loop, and `Len()`/`Truth()` route through it. Starlark calls
`Truth`/`Len` opportunistically, so every `len(b.genes)`, truthiness test, or `for`-loop setup
now copies the whole gene slice per entity. `Len()` should return
`len(c.ls.genesFor(...).order)` (or 0) without materializing. Worth fixing — it's a regression.

### Q2 — `setGeneValue` and `setSettingValue` are near-identical write-through paths · *simplification*
**`gene.go:129-166`** and **`settings_value.go:174-210`** run the same 7-step shape
(`fromStarlark` → `validateValue(scalarTypeRule)` → `scalarValueColumn` → `applyScalarValue` →
build ref → `StageSQLSet(WithExpected(old))` → `recordMirrorRow`). This is the **same scalar-set
lifecycle duplication** chunk 6 flags across Zone/Pellet/Entity — a single
`stageScalarWrite(t, goVal, refTemplate, locators)` helper would absorb the invariant tail in
all of them. Any change to the stale-guard or mirror keying (e.g. **F1**) currently has to be
made in each copy.

### Q3 — two small hand-written tables that cut against "derive from metadata" · *altitude*
- `geneTable` (`gene.go:169`) hardcodes `bibite→bibite_genes` / `egg→egg_genes`, where
  `identityTable` derives the table from `entityTables[kind]`. A 2-entry loud-default map, but a
  third place that must learn any new gene-bearing kind.
- `scalarValueColumn` (`convert.go:208`) restates the `ScalarNumber→number_value(DOUBLE)` column
  set that generated metadata already owns (`sqlref_generated.go`, the `sqlrefvalue`-tagged
  specs). It keys on `ScalarType` while the generated maps key on column name, so it's not a
  drop-in reuse — but **no test bridges them** (unlike `TestSemanticRulesReferenceLiveColumns`),
  so a value-column rename/retype in the metadata silently desyncs the binding's copy. Worth a
  drift-guard test at minimum.

---

## Verified correct (no issue)
The `column → sourceColumn` migration is **complete** — every SQL/mirror/guard/`SQLValueRef`
site keys on `sourceColumn` (`resolveColumn`, `rewritePredicate`, `bulkSet`/`bulkSetQuery`,
`SetField` ref+mirror, `ruleFor` guard keying, sub-collection delete guard); the only write to
`spec.column` is the intended alias assignment. The alias regression is fixed end-to-end for
both bibite and egg (`transform_position_x/y`, `transform_rotation` writable on both). The mirror
composite key aligns discriminators positionally and by type across VALUES / alias / WHERE, stays
one UPDATE per `(table,column)`, and `recordMirror` collapses to an equivalent single-locator
wrapper (no behavior change for entity/bulk). Settings: `Path`/`WrapperRawJSON`/`ValueType`
passed verbatim (wrapper-vs-bare round-trips via the mutator's `settingValueUsesWrapper`); the
`(entry_name, path)` mirror key is genuinely unique (path embeds scope/material); typed-column
selection consistent. The `geneSet` backing-slice refactor reads back consistently across
`gene()`, `genes[]`, and iteration; stale guard captures `old` before write-through.

## Suggested action
Fix **F1** (key the gene mirror on `path`) — it's the one with an in-run/committed divergence,
and it's a one-field change. **Q1** is a cheap, worthwhile efficiency fix. **F2**, **Q2**, **Q3**
are polish; **Q2**'s shared helper is worth doing alongside chunk 6 since they share the duplicated
lifecycle.
