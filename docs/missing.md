# Missing

Capabilities the object DSL / notebook **should** have but doesn't yet. Three kinds:

- **Escape-hatch leaks** — places forcing the user into `workspace.query(sql=...)` (raw
  SQL with internal joins like `mirror_saves` and internal columns like `number_value`,
  `save_id`, `owner_id`). That hatch is a workaround: the user surface is the object DSL
  (collections / `.where` / aggregates), never raw SQL or internal joins. Each leak
  violates that rule.
- **Ergonomics gaps** — discoverability/silent-failure papercuts (`gene` vs `genes`,
  case-sensitive lookups returning `None` instead of erroring, no in-app reference).
  Don't break the rule but make the surface hostile.
- **Engine / robustness gaps** — incompleteness *below* the DSL (transfer copy-only,
  structural edits invisible in-run, no live-node supervisor, unbounded in-memory working
  set). Not all at the user surface, but they shape what it can promise.

§1–§5 are the DSL-surface entries. §6+ are a full read/mutation scan across **saves /
worlds / workspaces**; the per-file location index (every read/write tagged by scale)
lives in [`surface-map/`](surface-map/), one file per slice.

---

## 1. Spanning surface: genes/brains missing, no time axis, unwrapped raw hatch

Genes and brain structure are reachable only *per entity* — `b.genes`, `b.gene("X")`,
`b.genes["X"]`, `b.nodes`, `b.synapses` on a single materialized bibite/egg. No world-
or workspace-level collection exists. `workspace.bibites` / `world.bibites` span only
**bibite columns**; genes/brains live in separate 1:many tables (`bibite_genes`,
`bibite_brain_nodes`, `bibite_brain_synapses`) the DSL never exposes above the entity.

**Consequence.** Any cross-world (or whole-world) gene/brain question forces SQL, e.g.
`SELECT m.world_id, g.gene_name, avg(g.number_value) FROM bibite_genes g JOIN
mirror_saves m ON m.save_id = g.save_id GROUP BY 1,2` — leaking the partition model
(`save_id`), catalog (`mirror_saves`), and typed value columns
(`number_value`/`bool_value`/`string_value`) the DSL should hide. The only DSL-native
alternative is a manual loop over `workspace.worlds()` opening each world — verbose, no
aggregation.

**Two more leaks ride the same spanning hatch** (the §1 escape-hatch rule, generalized to
the cross-world read path):
- **No time-series axis.** `mirror_saves.sim_time` is LEFT-JOINed by the spanning scope,
  but terminals are scalar/grouped only — `group_by("sim_time")` buckets by exact
  equality, not a trajectory; "population over time" forces raw `Query`/`HistoryQuery`
  (`workspace/spanning.go:6-8`, `workspace/query.go:43-47`, `script/thebibites/scope.go:33-36`).
- **No `world_id` wrapper on the all-worlds hatch.** `HistoryQuery` prepends a
  `world_saves` CTE; all-worlds `Query` prepends nothing → every cross-world raw query
  hand-writes `JOIN mirror_saves m ON m.save_id = <tbl>.save_id`, hard-coding catalog +
  partition key (the deepest leak, same shape as the gene SQL above)
  (`workspace/query.go:232-250` vs `:290-295`).
- **Minor (same slice):** `ensureReadOnly` blocklists bare keywords (not parse) →
  false-positives on legit read-only constructs containing a listed token; errs safe
  (`workspace/query.go:316-324`).

**Target.** Genes/brains become additional "kinds" in the existing spanning machinery, so
the catalog join + save-partition scoping happen invisibly — same shape as
`workspace.bibites`; and the spanning terminals grow a time axis + automatic `world_id`
attribution so the raw hatch is never needed for a cross-world read:

```python
workspace.genes.where("name == 'SizeRatio'").mean()   # one number, all worlds
workspace.synapses.where("enabled").count()            # brain size, workspace-wide
workspace.nodes.group_by("world")
# single-world: world.genes / s.genes / s.synapses / s.nodes
```

**Where it lives.**
- Spanning machinery: `workspace/spanning.go`, `script/thebibites/collection.go`
  (`EntityCollection`, `readOnly()` aggregate-only path, `AttrNames`).
- Per-entity genes/brains (today's only surface): `script/thebibites/gene.go`,
  `subcollection.go`, `entity.go`.
- Tables + columns: `saveparser/thebibites/normalize_metadata.go` (`bibite_genes`,
  `bibite_brain_nodes`, `bibite_brain_synapses`).
- Catalog / scoping / read hatch: `workspace/query.go` (`mirror_saves`, all-worlds `Query`
  vs `HistoryQuery`), `script/thebibites/scope.go`, `script/thebibites/sql.go`.

**Open questions.**
- *Aggregate value column.* Genes are typed (number/bool/string). Default
  `.mean()`/`.sum()` to `number_value`, require `.where("type == 'number'")`, or expose a
  single virtual `value` column?
- *`group_by("world")` / `group_by("sim_time")`.* Treat `world` (and the time axis) as
  virtual catalog dimensions (via `mirror_saves`) or require explicit columns? A trajectory
  needs bucketing, not exact-equality grouping.
- *Mutation.* Read-only spanning is the default; workspace-wide gene `set` is out of scope
  (mutation is per-save). Per-entity gene writes already exist. Brain wiring ("which node
  feeds which") needs a node↔synapse join the flat aggregate doesn't model — out of scope
  for v1 (this is §5).

---

## 2. No cheatlist / quick-reference page

Nothing shows the DSL surface at a glance: the type graph (`workspace → world → session →
collection / node / settings`), each type's members, the handful of non-guessable
recipes. The surface is discovered by trial and error — the exact churn of recent
questions: `.open` vs `.open()`, `gene` (parens) vs `genes` (brackets) vs invalid
`genes(...)`, `workspace.bibites` being aggregate-only, opening a world before iterating.

**Target.** An always-available cheatlist page/panel (and/or generated markdown):
- the type → members graph, one line per type;
- the read/write/iterate distinctions — two *intentional* surfaces, not duplicates:
  `gene("X")` parens → point read, value or **`None`** on miss (silent) · `genes["X"]`
  brackets → mapping read, value or **`KeyError`** on miss (loud) · `genes["X"] = v` →
  write · `for g in genes` → iterate · `len(genes)` → count · `genes(...)` → error;
- spanning vs single-world (`workspace.bibites` aggregate-only; open a world to iterate);
- ~6 copy-paste recipes (open+iterate, `.where().set()`+`commit()`, `group_by`, query
  table, start node, transfer).

**Source of truth.** Autocomplete already encodes the type graph in `api/web/app.js`
(`AC_TYPE_MEMBERS`, `AC_RESULT`), and bindings expose it via each value's `AttrNames()`.
The cheatlist must be **generated**, not hand-maintained — a second hand copy drifts like
an allowlist (see SQL-ref generation philosophy: derive, don't duplicate).

**Caveat — the root is deeper.** `AC_TYPE_MEMBERS`/`AC_RESULT` is *itself* hand-typed,
not cross-checked against the Go bindings' `AttrNames()`, and the syntax highlighter keeps
a **second** independent copy of the DSL vocab (`SL_METHODS`/`SL_BUILTINS`/`SL_KEYWORDS`)
plus manual kwarg top-ups in `AC_METHODS` (`api/web/app.js:1695,1558,1675`). Generating
from `AC_TYPE_MEMBERS` inherits a stale source unless `AC_TYPE_MEMBERS` is itself
generated from `AttrNames()` and the highlighter vocab folds into the same source.

---

## 3. Name lookups are case-sensitive and fail silently

Every string-name lookup matches exact case, several missing *silently*:
- `b.gene("diet")` → `None` (not an error) when the gene is `Diet`
  (`script/thebibites/gene.go`, `genes.byName[name]` exact match);
- `workspace.world("M")` → not-found when the world is `m`
  (`workspace/automation.go` `worldByName`);
- likely the same for any future species/zone/column name input.

Silent-`None` is worst: a script "runs" and prints `None` per row, looking like empty
data rather than a typo.

**Target.** Normalize **all name input to lowercase** at the lookup boundary (lowercase
both the user's arg and the candidate key before comparing) — case-insensitive across
genes, world resolution, and any later name-keyed lookup; the user shouldn't have to know
the canonical casing. This section owns case-insensitive name lookups; §1/§4 name-miss
behavior defers here.

**Design notes.**
- *Lookup-side only.* Lowercase for comparison; never mutate stored canonical case
  (display/serialization must round-trip the original).
- *Collisions.* Names differing only by case (worlds `m`/`M`, case-folding genes) resolve
  deterministically — ambiguous lookup errors loudly (mirroring existing ambiguous-world-
  name handling), never silently picks one.
- *Miss behavior (the `gene` vs `genes` asymmetry).* The two gene surfaces already
  disagree: `b.genes["X"]` raises `KeyError` (loud, mapping — `gene.go:84-86`) while
  `b.gene("X")` returns `None` (silent point read — `entity.go:153-168`). The loud
  behavior already exists; the papercut is the inconsistency. Either align `gene("X")` to
  error/warn on a miss, or document the split as deliberate. Independent of case-folding,
  same root. **Note:** §4 removes `gene("X")` entirely, resolving this by deletion
  (tolerant read becomes `genes.get("X")`).
- *Scope.* Cover the full name-input set (gene, world, species, zone, column) uniformly —
  partial rollout reintroduces the surprise.

---

## 4. One unified child-collection surface (genes / settings / brains / stomach)

The DSL has *five* surfaces that are conceptually the same — "the child rows of an entity
or scope" — each teaching a different idiom. Learning one doesn't transfer to the next,
and §2's cheatlist must document five mental models instead of one.

| surface | shape | read selection | scalar read | scalar write | structural write | miss | mirrored? |
|---|---|---|---|---|---|---|---|
| **genes** (`gene.go`) | name-keyed mapping | `["X"]` (+ legacy `gene("X")`) | value **directly** | `["X"] = v` | — | `None` vs `KeyError` split | yes |
| **settings** (`settings_value.go`) | scope → name-keyed mapping | `scope["X"]` → handle | `.value` | `.set(v)` | — | `KeyError` (loud) | yes |
| **brains** (`subcollection.go`) | positional sequence | iterate only (no key) | `s.col` attr | **none** | `.append()` / `el.delete()` | n/a | **no** |
| **stomach** (`subcollection.go`) | positional sequence | iterate only | `e.col` attr | **none** | `.append()` / `el.delete()` | n/a | **no** |
| **zones** (`zones.go`) | positional sequence | `[i]` / iterate | `z.col` attr | **`z.col = v`** (attr assign) | `clone(i)`→edit→`append()` / `z.delete()` | n/a | scalar yes / structural no |

Five idioms for one concept: value-direct vs handle vs attr; `[]=` vs `.set()` vs `col=v`
vs no-write; silent-`None` vs loud vs no-miss; mirrored vs not. This is the root the
recent questions kept circling — §3 (case-folding) is a *local* patch on one cell, and
removing `gene("X")` (below) is a second.

**Zones prove the target idiom — and contradict it.** `zones.go` does both halves at once:
- *Implements §4's proposed write idiom today*: `z.name = "Plains"` via `HasSetField`
  (`zones.go:175-219`) — so "one write idiom = attribute assignment" is a working surface
  brains/settings should match.
- *Yet internally inconsistent*: the zone writes via `z.col = v` but its *sub-values* via
  `z.values["k"].set(v)`; and that sub-value surface splits committed vs pending —
  committed `z.values` is a `SettingScope` (`["k"].set(v)`) while `clone()`'s
  `PendingZone.values` is `["k"] = v` (`zones.go:438-499`). Same op, two spellings by
  whether the zone exists yet. Structural create is a third pattern
  (`clone(i)`→edit→`append()`, vs brains' `collection.append(**fields)`).
Treat zones as the reference for scalar writes *and* a surface that needs reconciling.

**Target.** One element-collection model — the abstraction §1 needs for spanning and the
SQL-ref/DSL philosophy mandates (collections + `.where` + aggregates, never bespoke per-
surface shapes). Every child surface (per entity *and* spanning) is the same `Collection`
of element handles:

```python
# selection — uniform everywhere
b.genes.where("name == 'SizeRatio'")     # filter
b.genes["SizeRatio"]                      # keyed lookup — loud, KeyError on miss
b.genes.get("SizeRatio")                  # keyed lookup — tolerant, None on miss
for g in b.genes: ...                      # iterate
len(b.genes)                               # count
# element — a handle everywhere, columns read as attributes
g.value; g.name; g.type                    # gene handle
s.value; s.name; s.scope                   # setting handle (same shape)
syn.weight; syn.index                      # synapse handle
# write — one idiom: attribute assignment on the handle
g.value = 1.4                               # gene scalar write
s.value = 200                               # setting write (was .set(v))
syn.weight = 0.7                            # synapse write (NEW — brains can't today)
# structural — one idiom where the shape allows
b.synapses.append(weight=…, …); syn.delete()
```

Convergence rules:
- **Selection** is always `.where(expr)` + iterate; **keyed lookup** (`["X"]`/`.get`) only
  where the element has a name key (genes, settings — not the positional brain/stomach
  arrays).
- **Every element is a handle** with column attribute reads — drops genes' value-directly
  read (`g.value`, not the bare value), so gene and setting are the same handle shape
  (named typed scalar).
- **One write idiom: attribute assignment** (`el.col = v`, via `HasSetField`), replacing
  settings' `.set(v)` and adding scalar writes to (read-only) brain elements. Structural
  `.append()`/`delete()` stay where they are.
- **Miss is always loud** (`["X"]` → `KeyError`) with `.get()` for tolerant reads;
  `gene("X")` disappears as a side effect (case-miss: §3).

**Remove `gene("X")` (the special case that proves the rule).** Today a materialized
bibite/egg has *two* top-level gene accessors, one dead weight. `b.genes` is a full
`GeneCollection` (`gene.go:47-54`) implementing Starlark's `Mapping`/`HasSetKey`/`Iterable`
— already point read, write, iterate, `len()`. The separate `b.gene("X")`
(`entity.go:153-168`) does *only* a point read; its sole distinguishing behavior is
returning `None` on a miss instead of `KeyError` — a hand-rolled `.get()` masquerading as
its own surface. Delete it in favor of the dict idiom: `b.genes["X"]` (loud),
`b.genes.get("X")` (tolerant), `b.genes["X"] = v`. Kills the `None`-vs-`KeyError`
asymmetry (§3) and removes a surface; pre-v1, no compatibility reason to keep it.
- Remove: `script/thebibites/entity.go` (`gene` builtin registration `:37-38`, impl
  `geneBuiltin` `:153-168`, `AttrNames()` entry `:72`).
- Add a `get` method to the otherwise method-less `GeneCollection` (`gene.go`).

The concrete *write* gaps that fall out of this unify are §7 (bulk/predicate writes plus
element/NULL/structural writes).

**Where it lives.**
- Shapes to converge: `script/thebibites/gene.go` (`GeneCollection`), `settings_value.go`
  (`SettingScope`/`Setting`), `subcollection.go` (`ElementCollection`/`ArrayElement`).
- Target abstraction: `script/thebibites/collection.go` (`EntityCollection`,
  `readOnly()`, `AttrNames`) + `workspace/spanning.go` — the same machinery §1 lifts to
  spanning, so per-entity and spanning share one model.
- Regenerate from it: autocomplete `AC_TYPE_MEMBERS` in `api/web/app.js` and the §2
  cheatlist.

**Open questions.**
- *Handle vs value for single scalars.* Forcing `g.value` is more uniform but more verbose
  and a breaking change. Acceptable pre-v1, or keep value-direct for the two scalar
  surfaces and accept brains (multi-column) can't match?
- *Mirror consistency.* genes/settings scalar writes are mirrored into DuckDB (in-run reads
  see them); brain structural ops are not (mirror.go contract). Unify this rule or document
  the split loudly — a user shouldn't have to know which writes an in-run `.where` sees
  (the concrete read-after-write bug is §7).
- *Scopes.* Settings has a scope layer (`simulation`/`independent`/`material(name)`/zone).
  Is each scope just another named collection, or is "scope" first-class (cf. §1's
  `group_by("world")`)?
- *Structural create idiom.* Two patterns today — brains/stomach build-then-append
  (`collection.append(weight=…)`) and zones clone-then-append (`z = collection.clone(i);
  z.col = v; z.append()`). Clone-from-template fits deep nested elements (zones carry
  sub-values); kwargs-build fits flat rows. Pick one default, say when the other applies.
  Also decide what `append`/`delete` mean per surface (gene set already adds-or-updates;
  settings are fixed-schema).
- *`.get` signature.* Match Starlark dict `get(key, default=None)` exactly. *Egg parity:*
  confirm `gene("X")` has no callers beyond bibite/egg needing the same. *Case-folding:*
  `.get`/`[]` adopt whatever §3 lands on, applied uniformly.
- *Brain-graph integrity.* Already deferred to v2 in `subcollection.go` (no synapse pruning
  on node delete); a unified write surface raises the stakes — flag it, don't silently
  inherit. The graph-aware surface is §5.

---

## 5. Brains: expose the node↔synapse join, nothing more

Brains are already the §4 element-collection like everything else (`b.nodes`,
`b.synapses` — handles, `.where`, iterate). The *one* brain-specific gap: you can't follow
an edge — a synapse stores `NodeIn`/`NodeOut` as bare ids, so "what feeds this node"
forces a manual join in user script (§1 flagged this and punted).

**Grounding (so the join is correct).** A brain is a directed graph of neurons wired by
synapses (`saveparser/thebibites/normalize_types.go:296-325`): a node's `NodeIndex` is
the id synapses reference (≠ `NodeRowIndex`, the array slot); a synapse is
`NodeIn → NodeOut` with `Weight`/`Enabled`. `TypeName` is the activation function
(TanH/Sigmoid/…), not the input/hidden/output role. The graph may be **recurrent** (nodes
keep last-tick state), so nothing may assume it's acyclic.

**Target.** Add the join as navigation on the existing handles — then every wiring
question falls out of §4's `.where`/iterate, no bespoke graph API:

```python
syn.source / syn.target     # synapse -> its NodeIn / NodeOut node handle
n.inputs / n.outputs        # node -> its incoming / outgoing synapses

# "what feeds the Accelerate output", plain DSL:
for out in b.nodes.where("desc == 'Accelerate'"):
    for s in out.inputs.where("enabled"):
        print(s.source.desc, s.weight)
```

That's the whole feature. Anything heavier (topological order, reachability, cycle
detection) is a user-space walk over those two primitives, or out of scope for v1 — not
new surface.

**Open questions.**
- *Keyed lookup by `NodeIndex`.* `syn.source`/`target` resolve an endpoint by logical
  `NodeIndex`, so the node collection needs a `NodeIndex`-keyed lookup (loud miss, the
  §4 rule) — distinct from today's positional `NodeRowIndex`.
- *Enabled filtering is just `.where("enabled")`* — no separate "effective graph" mode.
- *Recurrence.* v1 ships only navigation (no `topo()`), so cycles are a non-issue;
  revisit only if ordered traversal is added.

**Where it lives.** `script/thebibites/subcollection.go` (`ElementCollection`/
`ArrayElement` handles to cross-link), `saveparser/thebibites/normalize_types.go`
(`NodeIndex` / `NodeIn` / `NodeOut`).

---

# Surface-scan seams (§6+)

From the cross-save/world/workspace read+mutation scan; full location index in
[`surface-map/`](surface-map/). Each bullet cites `file:line`.

---

## 6. Transfer is copy-only, partial, and surface-incomplete

`workspace.transfer(selector, dst)` (only cross-save mutation) is a copy/graft, not a move:
- **Copy-only.** Appends on `dst`, never deletes source → entity in both worlds (biomass double-counted);
  "evacuate" needs a manual source delete (`savemutator/thebibites/transfer.go:138,204`; none in `transfer_bridge.go:66-77`).
- **`body.id` collision fatal + batch-aborting.** No remap allocator (unlike `speciesID`, which *is* remapped);
  first failing graft aborts the batch (`transfer_identity.go:48-52`, `transfer_bridge.go:71-73`).
- **Only whole-entity grafts wired.** Settings-copy (`CollectSettingsValue`/`SetFromCollected`) and array-element
  feed (`CollectArrayElement`/`AppendArray`: synapses/stomach/pellets/zones) are engine-complete but surface-dead
  (`savemutator/thebibites/transfer.go:71-189` vs `transfer_bridge.go:66-77`).
- **Species ancestry dropped, not remapped** → cross-world phylogeny truncated at the graft
  (`savemutator/thebibites/transfer.go:282-301`); cf. §8.
- **No structured UI result:** count regex-scraped from printed `Output`, so a format change shows `transferred ?`
  (`api/web/app.js:1099-1103`).
- **No cross-save atomicity for a future move** (dst commit not coupled to a source delete) (`workspace/transfer.go:79`); cf. §10.

---

## 7. Bulk / predicate / element / NULL / structural write gaps

(concretizes §4 — the write half of the unify.)

**Bulk / predicate mutation is entity-only (settings, zones, pellets).** Entity collections get push-down bulk
writes (`where(...).set` / `.set_expr` / `.delete()`, `script/thebibites/sql.go:535,630,974`); other mutable
surfaces have none:
- **Settings / zones:** only per-handle scalar writes (`setting.set(v)`, `Zone.SetField`) + per-zone `delete()` — N
  zones = N point-sets, no batched UPDATE, no predicate (`script/thebibites/settings_value.go:162`, `zones.go:175-242`).
- **Pellets:** index-only `save.pellets[i]`, no `.where(...).set` (`script/thebibites/pellets.go:60`).
- So "scale all zone fertilities" etc. force O(N) loops or raw `save.sql`; `bulkSet`/`bulkSetExpr` can't target
  `settings_*` / `settings_zones`.

**Element / NULL / structural writes are asymmetric and sometimes silent.**
- **Can't write NULL.** `fromStarlark` rejects `None`, `setRowField` has no nil branch, yet absent 1:1 rows read as
  `None` → round-trip `b.field = b.field` breaks and clearing an optional needs raw `save.sql`
  (`script/thebibites/convert.go:81,128`).
- **Structural edits invisible in-run** (mirror is scalar-only): `zone.clone(i).append()` / `delete()` / `bulkDelete`
  staged but never mirrored, so a same-run `.sql`/iterate sees the pre-edit set (`script/thebibites/mirror.go:66,133`,
  `zones.go:224-357`, `sql.go:974`).
- **`b.delete()` silent no-op, no count** (returns `None`, defers its referential guard to commit) while bulk
  `where(...).delete()` returns a count (`script/thebibites/entity.go:178` vs `collection.go:296`).
- **`species_id` set unchecked** (non-negativity only) → can point an entity at a nonexistent species id
  (`script/thebibites/guards.go:94,106`). Write-guard gap; cf. §8 gene-SET.

---

## 8. Save-format drift & write-path hardening (parser + mutator)

"Loud but localized" on drift (harden, don't rewrite), with unguarded edges:
- **Unknown / drifted JSON sections dropped, not captured** (no table since H4); un-tagged/drifted paths silently
  zeroed → invisible data loss, recoverable only via `Entry.Raw`/`Entry.JSON`
  (`saveparser/thebibites/parse_payload.go:109`, `sqlref_populate.go:70`).
- **No version gate:** `SceneState.Version` parsed but nothing branches on it (`saveparser/thebibites/parse_scene.go:13`).
- **Species / scene cascades no-op on missing paths** → `nBibites`/`activeSpeciesList` desync silently
  (`savemutator/thebibites/session.go:466,583`); cf. §6.
- **Gene SET trusts caller's path verbatim:** `resolveGeneColumn` checks only non-empty, never the value against the
  gene's `value_type` (unlike the guarded settings resolver) (`savemutator/thebibites/sqlref_entities.go:47`).
  Write-guard gap; cf. §7 `species_id`.
- **Append/insert can't create missing intermediate structure**, and **same-batch cascade reads use on-disk JSON, not
  pending edits** (species reassignment + delete in one Apply can mis-maintain `activeSpeciesList`) — documented
  correctness assumptions (`savemutator/thebibites/path.go:151,279`; `session.go:491,556`).

---

## 9. Live-world supervision & observability

Nodes can be started/driven, but nothing watches or fully exposes them:
- **No supervisor:** `control/controller.go` is an empty stub — a dead process leaves the row "running" and
  `activeNodeForWorldLocked` keeps the world bound to a dead runtime, wedging a fresh `StartNode`
  (`control/controller.go:1`, `workspace/node.go:299-318`).
- **`node.wait` can't see death** (predicate ANDs only sim_time/paused/autosave) → a mid-wait death surfaces as an
  opaque IPC error, not "node exited" (`workspace/automation.go:957-961,1046-1079`).
- **Logs memory-only, unbound from automation, dropped on stop:** 1000-line ring has no Starlark binding, isn't
  persisted, `dropLogRing` fires on stop/kill; UI `?follow` is a no-op faked by a 2s poll (`workspace/node_logs.go:117,131`;
  `workspace/automation.go:627`; `api/handlers_nodes.go:143`; `api/web/app.js:657`).
- **Detached node = dead end:** "reconnect" button only toasts "not yet implemented" — no re-attach endpoint; only
  recovery is dropping the row (orphaning the process) (`api/web/app.js:868`).
- **Stale snapshot:** `node.status` returns a row captured at handle creation → lies after a same-run `stop()`/`kill()`
  (`workspace/automation.go:610-660`).
- **No UI affordance for `ingest_autosave` / `evict_history` / `load` / `unload`** though autocomplete advertises them
  and `last_autosave` is shown (`api/web/app.js:1697,1700,805`).
- **IPC events lossy:** unsolicited game events drop when the 128-buffered channel is full (`ipc/session.go:181`).

---

## 10. Workspace memory & storage lifecycle

Persisted reclamation is solid; in-memory and cross-store edges have gaps:
- **Working set unbounded in memory:** `w.worlds` grows on `Load`/`OpenWorld`, shrinks only on explicit
  `Unload`/commit-consumption — no cap/LRU/sweeper (`eviction.go`/`gc.go` touch persisted blobs+catalog, not the map)
  (`workspace/workspace.go:48`, `working_set.go:120-129`).
- **`DeleteWorkspace` can't evict the live handle it warns about:** own-handle free function `RemoveAll`s the dir of an
  open DuckDB file, with only a prose contract to evict+Close first (`workspace/registry_ops.go:79-104`).
- **`ReconcileBlobs` not auto-wired into `Open`** → post-crash catalog↔blobstore drift repair depends on the host
  calling it (`workspace/eviction.go:271-283`, `workspace.go:199`).
- **GC / eviction / reconcile method-only on a live `*Workspace`** (no own-handle admin variant) → maintenance forces
  a full `Open`, serializes on `w.mu` (`workspace/eviction.go:101,204,283`, `gc.go:47,124`).
- **Working-partition drift not self-healing:** a failed `ReplaceExtractedSave` on commit leaves the working partition
  lagging head with no on-demand reproject (`workspace/commit.go:215-220`, `working_set.go:97-106`); cf. §6.
- **Cross-store linkage unenforced:** DuckDB `save_id` partitions and revisionstore `sha256` rows have no joint
  invariant; blob byte deletion decoupled from refcount bookkeeping (crash-safe by ordering, correctness rides on a GC
  pass running) (`duckdb/import.go:272` vs `revisionstore/store.go:1136`; `revisionstore/store.go:1372` + `blobstore/fsstore.go:190`).

---

## 11. Brain nodes: lookup by normalized name (Desc), not just id

Brain nodes are addressable today only by *id*: positionally (`b.nodes[0]`, the array
slot) and — since §5 — by logical `NodeIndex` for edge navigation
(`script/thebibites/subcollection.go:256-316`). To find a node by *what it is* you must
write an exact-case predicate, `b.nodes.where("desc == 'Accelerate'")`, which is brittle
(`'accelerate'` silently matches nothing). Every node already names itself: the save's
per-node `Desc` field is parsed and projected as `BrainNodeRow.Desc`
(`saveparser/thebibites/normalize_types.go:307`, populated `saveparser/thebibites/parse_brain.go:29`).

**Grounding (verified against `testdata/saves/the-bibites/autosave_20260301021357.zip`).**
Across all 40 bibites — including evolved brains (48–49 nodes, hidden types 2/7/8/9) —
**every node carries a non-empty `Desc`** and **no two nodes in a brain share a `Desc`**.
Standard nodes are `EnergyRatio … Want2Attack`; evolved hidden nodes are `Hidden0`,
`Hidden1`, …. `Desc` always matches the live save, so it — not a side table — is the
source of truth for names.

**Non-goal / trap.** A handwritten `NodeIndex → name` side table is *not* the mechanism.
Such tables drift from the real save — an early hand-sketch of these mappings already had
the output block off by one (`32: Accelerate` where the true `NodeIndex` is `33`) and the
`Phero*` inputs mis-ordered, which is exactly the failure mode name lookup exists to kill.
Resolution MUST key off the node's own live `Desc`, never a fixed index table. This is a
*desc override of how nodes are addressed*, not an override of ids.

**Target.** Normalized-name lookup on the existing node collection, reusing §3's fold
machinery — same characters, any casing, resolved lowercased:

```python
b.nodes["accelerate"]      # -> the node whose Desc == "Accelerate" (NodeIndex 33)
b.nodes["EnergyRatio"]     # same node regardless of caps
b.nodes["hidden0"]         # evolved hidden node, by name
```

It returns the ordinary node handle, so it inherits the existing read/iterate/write
surface (§4/§5) for free — this ticket adds an *addressing* path, not a new node API.

**Reuse, don't reinvent.** §3 already ships `foldLookup`
(`script/thebibites/loadedsave.go:411-440`): exact match wins, then a single case-fold
scan, and a name that folds to ≥2 distinct canonical entries is a **loud error**, never a
silent default. A normalized-name miss is a loud miss (the §4 rule), consistent with
`gene("name")` (`script/thebibites/gene.go:102,162`). Name resolution is per-brain (each
`b.nodes` is one brain's nodes), so the fold index is built over that brain's `Desc` set.

**Open questions (planner).**
- *Surface form.* `b.nodes["name"]` subscript vs. an explicit `b.node("name")` accessor —
  and how either coexists with the existing positional/`NodeIndex` integer access on the
  same collection (string key → Desc, int key → position/NodeIndex?). Pick the form most
  consistent with the §4 collection idiom; flag any overload ambiguity.
- *Cross-brain collections.* If a node collection ever spans brains (§1 all-X surfaces),
  `Desc` is no longer unique; define the behavior (scope-to-brain only, or loud).
- *Hidden-node names.* `Hidden0/1/…` are unique within a brain but not globally meaningful;
  confirm they resolve like any other `Desc` with no special-casing.

---

## 12. Brain wiring: multi-hop path / reachability is user-space only

You can name a node (§11) and follow one edge (§5: `s.source`/`s.target`,
`n.inputs`/`n.outputs`), but you cannot ask a *path* question — "is there a string of
connections from `MeatAngle` to `Rotate`, and is it net-positive?" — as first-class
surface. §5 deliberately left topo-order / reachability / cycle-detection to a user-space
walk over its two primitives. `.where` can't express it either: `.where` is a per-element
attribute predicate (one node, one synapse), while reachability is a whole-graph property,
so `bibites.where("meatangle ~> rotate > 0")` has nowhere to compile to.

**Today's answer** is an in-script DFS/BFS, e.g.:

```python
def positive_path(start, goal_idx):
    seen, stack = set(), [(start, 1)]            # brain is recurrent → guard visited
    while stack:
        n, sign = stack.pop()
        if n.node_index in seen: continue
        seen.add(n.node_index)
        for s in n.outputs.where("enabled and weight != 0"):
            nsign = sign * (1 if s.weight > 0 else -1)
            if s.target.node_index == goal_idx:
                if nsign > 0: return True
            else:
                stack.append((s.target, nsign))
    return False

hits = [b for b in world.bibites
        if positive_path(b.nodes["meatangle"], b.nodes["rotate"].node_index)]
```

**Gotchas this surfaces** (the reasons it's not trivially first-class):
- *Names are exact-character.* The output node's `Desc` is **`Rotate`**, not `Rotation`;
  §11 normalization is lowercase+strip only (no stemming/fuzzy), so `"rotation"` is a loud
  miss. `"meatangle"` is correct (node 16 `MeatAngle`).
- *"Positive" is ambiguous:* net sign = product of edge weights along the path (shown
  above) vs. every edge `weight > 0`. Neither models activation-function monotonicity.
- *Cost:* O(bibites × brain edges) evaluated in-script — no `.where` pushdown.

**Target (if promoted to a ticket).** A first-class brain path predicate, e.g.
`b.reaches("meatangle", "rotate", sign="+")` and/or a `bibites.where(...)`-pushable form,
with explicit policy for: enabled-only edges, sign semantics, max depth / cycle handling,
and whether it stays an in-process walk or pushes to SQL. Until then this is user-space.

---

## 13. Mutations don't bound-check values

DSL writes (attribute assignment / `.set`, §7) persist whatever value is given — an
out-of-range value (e.g. a negative count, an activation outside a field's valid domain)
is written and only surfaces later, if at all. **Target:** intercept out-of-bounds values
at mutation time and return a loud error instead of writing (consistent with §3/§4's
loud-miss/loud-ambiguity stance). Open: where bounds come from (per-field metadata, ideally
generated from the schema rather than a hand-maintained list — cf. the SQL-ref generation
philosophy), which fields actually carry ranges, and hard-fail vs. clamp. Lives on the
write path (`script/thebibites` set surface; cf. §7 write gaps, §8 write-path hardening).
