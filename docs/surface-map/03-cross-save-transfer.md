# 03. Cross-save / cross-world transfer

**Scope (files scanned):**
- `savemutator/thebibites/transfer.go`
- `savemutator/thebibites/transfer_identity.go`
- `savemutator/thebibites/workspace.go`
- `script/thebibites/transfer_bridge.go`
- `workspace/transfer.go`

This slice is **[WRITE]** at **[WORKSPACE]** scale: every mutation spans two saves
(a source archive read + a destination session staged). The transfer engine itself
stages only — it never commits. The head-advancing commit is delegated to the
unchanged `CommitWorldLoaded` path (out of slice; see seams).

## End-to-end shape (one sentence per layer)

1. DSL automation builtin `workspace.transfer(srcCollection, dstWorld)` → calls
   `Workspace.Transfer` (`workspace/automation.go:324`, upstream of this slice).
2. `Workspace.Transfer` (`workspace/transfer.go:36`) resolves the dst working copy,
   delegates the graft to the bridge, then commits via `CommitWorldLoaded`.
3. `thebibites.TransferEntries` (`script/thebibites/transfer_bridge.go:41`) loops the
   selected entry names through the mutator engine, staging onto the dst session.
4. `mutator.transfer` (`savemutator/thebibites/transfer.go`) collects a source element
   and stages it onto the dst Session, doing identity reconcile + species remap.
5. `mutator.Workspace` interface (`savemutator/thebibites/workspace.go:31`) is the
   mockable seam the bridge depends on; `*transfer` is the concrete implementer.

## Location map

### `workspace/transfer.go` — WORKSPACE-level orchestration (graft + commit)
- `workspace/transfer.go:36` — [WRITE] [WORKSPACE] — `Workspace.Transfer(ctx, srcLS, entryNames, dstWorldID)`: the Go method backing the `workspace.transfer(...)` automation builtin; grafts source entries into a dst world and commits a new dst revision.
- `workspace/transfer.go:52` — [WRITE] [WORKSPACE] — empty-selection guard: returns the zero `Revision` (committed=false), does NOT advance the head.
- `workspace/transfer.go:59` — [READ] [WORKSPACE] — `OpenWorld(ctx, dstWorldID)` resolves the dst working copy BEFORE taking any further lock (OpenWorld takes its own non-reentrant `w.mu`; Transfer must not nest it).
- `workspace/transfer.go:66` — [WRITE] [WORKSPACE] — self-transfer guard: rejects `dstLS == srcLS` (would double-stage / conflate src+dst archives).
- `workspace/transfer.go:72` — [WRITE] [WORKSPACE] — delegates staging to `thebibites.TransferEntries`; lock-free in-memory mutation of the dst session.
- `workspace/transfer.go:79` — [WRITE] [WORKSPACE] — commits via `CommitWorldLoaded` (the ONLY DuckDB/SQLite writer here; owns `w.mu` for its whole body; re-resolves the SAME cached dst handle).

### `script/thebibites/transfer_bridge.go` — package boundary (LoadedSave ↔ mutator)
- `script/thebibites/transfer_bridge.go:41` — [WRITE] [WORKSPACE] — `TransferEntries(srcLS, dstLS, entryNames)`: thin exported seam wiring the mutator engine over two `*LoadedSave`. Lives in package `thebibites` to read the unexported `ls.session`; keeps `mutator.Session` from leaking outside the package.
- `script/thebibites/transfer_bridge.go:42-50` — [READ] [WORKSPACE] — nil guards + self-transfer guard (`srcLS == dstLS`).
- `script/thebibites/transfer_bridge.go:54,57` — [READ] [WORKSPACE] — state guards: refuses a source/dest session in `StateApplied` (a consumed/committed handle would graft stale bytes).
- `script/thebibites/transfer_bridge.go:61` — [READ] [WORKSPACE] — `mutator.NewTransfer(srcLS.session, dstLS.session)` builds the coordinator over both sessions.
- `script/thebibites/transfer_bridge.go:66-77` — [WRITE] [WORKSPACE] — per-entry loop: `CollectEntry(name)` (read src) → `AppendEntry(el)` (stage dst); bumps `dstLS.stagedOps++` per graft so the commit's `willWrite` gate (`ls.stagedOps > 0`) fires.

### `savemutator/thebibites/workspace.go` — the cross-save SEAM (interface + payload)
- `savemutator/thebibites/workspace.go:19` — [READ] — `CollectedElement{SourcePath, Table, JSON}`: one value pulled from a source save, held between collect and append.
- `savemutator/thebibites/workspace.go:31` — [WRITE] [WORKSPACE] — `Workspace` interface: the mockable cross-save append seam — `Destination()`, `AppendArray`, `AppendEntry`. (Note: settings-copy `SetFromCollected` is a SET, not an append, so it is NOT on the interface — concrete-only.)

### `savemutator/thebibites/transfer.go` — pure-archive coordinator (collect + stage)
- `savemutator/thebibites/transfer.go:35` — [WRITE] [WORKSPACE] — `transfer{src, dst *Session}`: coordinator over a source + destination Session; stages onto dst, never commits.
- `savemutator/thebibites/transfer.go:41` — compile-time assert `*transfer` satisfies `Workspace`.
- `savemutator/thebibites/transfer.go:45` — [READ] [WORKSPACE] — `NewTransfer(src, dst)`: validates both sessions non-nil and both wrap a decoded archive.
- `savemutator/thebibites/transfer.go:62,65` — [READ] [WORKSPACE] — `Source()` / `Destination()` accessors.
- `savemutator/thebibites/transfer.go:71` — [READ] [SAVE→src] — `CollectSettingsValue(ref)`: resolves a scalar ref against the SOURCE archive (`ResolveSQLValueRef`) and reads the JSON value at the path. Settings-copy collect.
- `savemutator/thebibites/transfer.go:104` — [READ] [SAVE→src] — `CollectArrayElement(ref)`: resolves the SOURCE array-element ref via `SQLDelete` (whole-element path, e.g. `brain.Synapses[3]`) and reads the element JSON. Feeds `AppendArray`.
- `savemutator/thebibites/transfer.go:138` — [READ] [SAVE→src] — `CollectEntry(entryName)`: looks up a whole bibite/egg entry in the SOURCE archive, deep-copies its JSON, classifies via `tb.ClassifyEntry` → table `bibites`/`eggs`. Feeds `AppendEntry`.
- `savemutator/thebibites/transfer.go:167` — [WRITE] [SAVE→dst] — `SetFromCollected(dstRef, element)`: stages a scalar SET (`dst.StageSQLSet`) — the settings-copy path; table-match guarded. NOT on the `Workspace` interface.
- `savemutator/thebibites/transfer.go:181` — [WRITE] [SAVE→dst] — `AppendArray(dst, element)`: stages an array-element append via `dst.StageSQLAppend` (reuses append resolvers + SceneCount reconciliation, e.g. pellets); table-match guarded.
- `savemutator/thebibites/transfer.go:204` — [WRITE] [WORKSPACE] — `AppendEntry(element)`: the whole-entity graft. Deep-clones, reconciles non-species identity FIRST, REMAPS speciesID, allocates a fresh entry name, then `dst.StageAppendBibite`. All fail-able checks run BEFORE any stage (0-staged-ops-on-rejection invariant).
- `savemutator/thebibites/transfer.go:232` — [WRITE] [WORKSPACE] — species-remap branch: `entitySpeciesID(cloned)` → `freshDstSpeciesID()` → `sourceSpeciesRecord(...)` → rewrite `genes.speciesID` → `stageSpeciesImport(...)`.
- `savemutator/thebibites/transfer.go:255` — [WRITE] [SAVE→dst] — `nextEntryName(...)`: allocates a non-colliding entry name, unioning live dst entries with names already STAGED this session (`dstStagedAppendNames`) so a multi-graft loop never reuses `bibite_<n>`.
- `savemutator/thebibites/transfer.go:268` — [READ] [SAVE→dst] — `dstStagedAppendNames(kind)`: scans `dst.StagedOperations()` for already-staged `OperationAppendEntry` names of this kind.
- `savemutator/thebibites/transfer.go:291` — [WRITE] [SAVE→dst] — `stageSpeciesImport(record, freshID)`: stamps record `speciesID=freshID`, resets `parentID=freshID` (lineage root, only if present), stages append onto `recordedSpecies` + `activeSpeciesList`, bumps `nextSpeciesID=freshID+1` IF the field exists (no CreateMissing).
- `savemutator/thebibites/transfer.go:318` — [READ] — `getJSONPathPresent`: presence probe ignoring read errors.

### `savemutator/thebibites/transfer_identity.go` — identity guards + species allocator
- `savemutator/thebibites/transfer_identity.go:46` — [READ] [WORKSPACE] — `reconcileGraftIdentity(kind, value)`: pure CHECK (no staging), called before any stage. Enforces body.id-collision + dangling-child guards.
- `savemutator/thebibites/transfer_identity.go:48` — [READ] [WORKSPACE] — body.id collision guard: `dstBibiteWithBodyID(id)` → rejects loudly (F3 does NOT remap entity ids).
- `savemutator/thebibites/transfer_identity.go:53` — [READ] [WORKSPACE] — dangling child-ref guard: `danglingChildRefs(value)` → rejects loudly (F3 does NOT strip/remap children).
- `savemutator/thebibites/transfer_identity.go:74` — [READ] [WORKSPACE] — `freshDstSpeciesID()`: the load-bearing conflation guard. Fails loudly if dst has no decoded `speciesData.json`; otherwise `fresh = max(dstSpeciesIDUsage()+1, nextSpeciesID)`.
- `savemutator/thebibites/transfer_identity.go:96` — [READ] [WORKSPACE] — `dstSpeciesIDUsage()`: single traversal of max species id across `activeSpeciesList`, `recordedSpecies[*].speciesID`, every live dst bibite/egg `genes.speciesID`, AND ids already staged-for-import this session (so each loop graft gets a DISTINCT id).
- `savemutator/thebibites/transfer_identity.go:155` — [READ] [SAVE→src] — `sourceSpeciesRecord(srcArchive, sid)`: deep-copies the source `recordedSpecies` record for `sid`; missing record → caller fails loudly.
- `savemutator/thebibites/transfer_identity.go:178` — [READ] — `jsonInt64Path` helper.
- `savemutator/thebibites/transfer_identity.go:188` — [READ] — `jsonInt64Array` helper.
- `savemutator/thebibites/transfer_identity.go:209` — [READ] [SAVE→dst] — `dstBibiteWithBodyID(id)`: scans dst bibite entries for a colliding body.id.
- `savemutator/thebibites/transfer_identity.go:227` — [READ] [SAVE→dst] — `danglingChildRefs(value)`: reports `body.eggLayer.children` ids absent from dst.
- `savemutator/thebibites/transfer_identity.go:252` — [READ] [SAVE→dst] — `dstBibiteBodyIDs()`: set of dst bibite body.ids.
- `savemutator/thebibites/transfer_identity.go:274` — [WRITE-name-alloc] [SAVE→dst] — `nextEntryName(dst, kind, reserved...)`: `max(numeric index over dst.Entries ∪ reserved) + 1`, format matches the parser's bibite/egg entry regex.
- `savemutator/thebibites/transfer_identity.go:306` — [READ] — `entryIndexToken` helper.

## Read paths

The transfer engine is **pure-archive on the read side** — it reads parsed archive
JSON directly and **never runs a query, opens a DB, or touches a revision store**
(`savemutator/thebibites/transfer.go:7-10`, `workspace.go:11-14`).

- **Source collect** (all read the SOURCE `*tb.Archive` JSON):
  - Settings scalar: `CollectSettingsValue` → `ResolveSQLValueRef` + `getJSONPath` (`transfer.go:71`).
  - Array element: `CollectArrayElement` → `SQLDelete` (whole-element resolver) + `getJSONPath` (`transfer.go:104`).
  - Whole entity: `CollectEntry` → `Archive().Entry(name)` + `ClassifyEntry` + `cloneJSON` (`transfer.go:138`).
  - Species record: `sourceSpeciesRecord` → src `speciesData.json#recordedSpecies` deep copy (`transfer_identity.go:155`).
- **Destination probes** (read the DST `*tb.Archive` JSON + DST staged ops, for collision/allocation):
  - body.id collision: `dstBibiteWithBodyID` / `dstBibiteBodyIDs` (`transfer_identity.go:209,252`).
  - species id usage: `dstSpeciesIDUsage` over active list + records + live entities + staged imports (`transfer_identity.go:96`).
  - entry-name allocation: `nextEntryName` over dst entries + staged names (`transfer_identity.go:274`).
- **Selection is the object DSL, never raw SQL.** `entryNames` arriving at
  `Workspace.Transfer`/`TransferEntries` are entry_names a where-collection already
  resolved, scoped to the source world by construction
  (`workspace/transfer.go:18-21`). The user writes no SQL/JOINs; transfer reuses the
  collection abstraction upstream (consistent with the "DSL not raw SQL" /
  "collection abstraction for spanning" project invariants).

## Mutation paths

All destination mutation is **STAGED onto the dst `Session`** and rides
`Apply()`'s all-or-nothing atomicity; **nothing is committed inside the engine**
(`transfer.go:200-203`). The single commit is delegated to `CommitWorldLoaded`.

- **Settings copy (scalar SET):** `SetFromCollected` → `dst.StageSQLSet`
  (`transfer.go:167`). Not on the `Workspace` interface (it's a set, not an append).
- **Array feed (APPEND):** `AppendArray` → `dst.StageSQLAppend` — reuses append
  resolvers + SceneCount reconciliation (`transfer.go:181`).
- **Whole-entity graft (APPEND ENTRY)** — the F1/F3 path (`transfer.go:204`):
  1. Deep-clone collected JSON (`transfer.go:220`) so a later source mutation can't
     alias the staged payload.
  2. `reconcileGraftIdentity` — body.id collision + dangling children → **fail
     loud** (no remap of entity ids / child refs; `transfer_identity.go:46`).
  3. **speciesID remap** (the F1→F3 change; covers bibites AND eggs via
     `genes.speciesID`): allocate a `freshDstSpeciesID` that beats every id in use,
     import the SOURCE species record under that fresh id (`stageSpeciesImport`),
     rewrite the grafted entity's `genes.speciesID` (`transfer.go:232-247`). A
     species-less entity grafts cleanly with no remap.
  4. `nextEntryName` allocates a name that collides with neither live nor
     staged-this-session entries (`transfer.go:255`).
  5. `dst.StageAppendBibite` stages the whole entry (`transfer.go:261`).
- **Source side is NOT mutated.** Collect reads + deep-copies; the source archive is
  never written. Transfer is a COPY/GRAFT, not a move — the source entity is left in
  place (no delete-from-source observed in this slice).
- **Commit / head advance:** `TransferEntries` bumps `dstLS.stagedOps++` per graft
  (`transfer_bridge.go:75`) so the dst commit's `willWrite` gate fires; the actual
  WriteArchive + advancing-head revision + DuckDB mirror re-seed happens in
  `CommitWorldLoaded` (`workspace/transfer.go:79`, out of slice).

### Identity-collision handling (the F1/F2/F3 work)

- **body.id** (random int32): **NOT remapped** — a collision with any dst bibite is
  rejected loudly, naming the offending id (`transfer_identity.go:48-52`).
- **speciesID** (per-world LINEAR id space): **REMAPPED**. The engine never reuses
  the source id and never adopts the dst's coincidental same-id species. It allocates
  `fresh = max(activeSpeciesList, recordedSpecies[*].speciesID, every live entity's
  genes.speciesID, nextSpeciesID) + 1` (using `max(...)` not `nextSpeciesID` alone
  defends against a stale counter), imports the source RECORD under `fresh`, and
  rewrites `genes.speciesID` on the graft (`transfer_identity.go:60-90`,
  `transfer.go:232-247`). Imported `parentID` is reset to self (lineage root) because
  it points into the source id space with no dst counterpart
  (`transfer.go:282-301`).
- **Multi-graft loop conflation guard:** within one session, staged-but-not-applied
  species imports are folded into `dstSpeciesIDUsage` (`transfer_identity.go:138-145`)
  and staged entry names into `nextEntryName` (`transfer.go:268-276`) so each graft
  in a loop gets a DISTINCT fresh species id and entry name.

### DSL surface vs plumbing

- **DSL-facing:** the `workspace.transfer(srcCollection, dstWorld)` automation
  builtin (`workspace/automation.go:324`, upstream) → `Workspace.Transfer`
  (`workspace/transfer.go:36`). Selection is a resolved where-collection; users write
  no SQL.
- **`transfer_bridge.go`** is the package-boundary adapter (`*LoadedSave` ↔
  unexported `mutator.Session`), not a DSL surface itself.
- **Plumbing:** the `mutator.Workspace` interface + `*transfer` coordinator
  (`workspace.go`, `transfer.go`, `transfer_identity.go`) are the mockable engine; the
  collect/append methods map 1:1 onto DSL targets (settings copy / array feed / whole
  graft) per the method-to-DSL map at `transfer.go:12-24`.

## Missing seams

### Transfer is copy-only; no source-side delete (no true "move")
**What's missing.** This slice grafts a COPY onto the destination
(`CollectEntry` deep-copies + `StageAppendBibite`) and never stages a delete on the
source session. There is no "move" primitive that removes the entity from the source
after a successful dst commit.
**Consequence.** After `workspace.transfer(...)`, the entity exists in BOTH worlds.
A user expecting a "move" gets a duplicate (same body.id in two worlds — fine since
body.id is per-world, but the population/biomass is double-counted across the
workspace). Any "evacuate this world" workflow must manually delete on the source.
**Where it lives.** `savemutator/thebibites/transfer.go:138` (CollectEntry, copy
semantics), `transfer.go:204` (AppendEntry stages only on dst), no delete in
`script/thebibites/transfer_bridge.go:66-77`.

### Cross-save atomicity is per-side, not two-phase
**What's missing.** The graft stages on dst and commits only dst
(`workspace/transfer.go:72,79`). The source is never committed, but there is no
transactional coupling between "dst commit succeeded" and any source-side action. If
a future move adds a source delete, nothing here coordinates the two commits.
**Consequence.** Today: benign (copy-only). But the seam name `TransferEntries`
implies move-like semantics it does not provide; a later source-delete bolt-on would
have no two-phase / rollback story — a dst commit + source-delete-commit pair could
half-apply across worlds.
**Where it lives.** `workspace/transfer.go:79` (single dst commit), no source commit
anywhere in slice. **Overlaps the workspace-lifecycle slice** (commit ordering /
`CommitWorldLoaded` / revision-store atomicity are owned there).

### Species ancestry chain is dropped, not remapped
**What's missing.** `stageSpeciesImport` resets the imported record's `parentID` to
self and explicitly does NOT carry the source `parentID` or remap an ancestry chain
(`transfer.go:282-301`). Only the single grafted species record is imported.
**Consequence.** The imported species loses its phylogenetic lineage in the dst
world (it becomes a lineage root). For users analyzing speciation/ancestry across a
transferred population, the tree is silently truncated at the graft boundary. This is
a deliberate scope cut, not a bug, but it is an unsealed seam for any future
"transfer with lineage" feature.
**Where it lives.** `savemutator/thebibites/transfer.go:282-301` (parentID reset),
`transfer_identity.go:16-24` (scope note: cross-ref reconciliation beyond
identity/species is out of scope).

### Settings-copy / array-feed collect paths have no DSL-facing transfer binding in this slice
**What's missing.** `CollectSettingsValue`/`SetFromCollected` (settings copy) and
`CollectArrayElement`/`AppendArray` (synapse/brain-node/stomach/pellet/zone feed)
exist on `*transfer` (`transfer.go:71,104,167,181`), but the only bridge/workspace
wiring present (`TransferEntries` → `Transfer`) drives `CollectEntry`/`AppendEntry`
(whole entity) ONLY. No bridge function exercises the scalar/array-element transfer.
**Consequence.** The engine supports cross-save settings copy and array-element feed,
but there is no end-to-end DSL path to reach them from `workspace.transfer(...)` as
wired here — only whole-bibite/egg grafts are reachable. The scalar/array capability
is engine-complete but surface-dead until a bridge binds it.
**Where it lives.** Engine: `savemutator/thebibites/transfer.go:71-189`. Bridge gap:
`script/thebibites/transfer_bridge.go:66-77` only loops `CollectEntry`/`AppendEntry`.
**Overlaps the mutation-engine slice** (StageSQLSet/StageSQLAppend resolvers and the
SQL-ref surface that back these collect methods live there).

### body.id collision is fatal, not remapped — a populated dst can hard-block transfer
**What's missing.** body.id is a random int32 with negligible collision odds, but on
the rare collision the WHOLE graft is rejected loudly with no remap option
(`transfer_identity.go:48-52`). Unlike speciesID, there is no allocator to mint a
fresh non-colliding body.id.
**Consequence.** A transfer into a densely populated dst world can fail on a 1-in-2^32
collision with no recovery path other than retrying / editing the source. For a
batch/automation transfer of many entities, one unlucky id aborts the entire
batch (`TransferEntries` returns on first graft error, `transfer_bridge.go:71-73`).
**Where it lives.** `savemutator/thebibites/transfer_identity.go:48-52` (fatal
guard), `script/thebibites/transfer_bridge.go:71-73` (first-error abort of the batch).
