# The Bibites Save Mutator Handoff

## Package

The save mutator lives in:

`savemutator/thebibites`

Keep it separate from `saveparser/thebibites`. The parser owns archive parsing, writing, and read/query projections. The mutator owns staged read-modify-write operations against parsed archive entries.

## Core Flow

Use a parsed `*saveparser/thebibites.Archive` as input:

```go
archive, err := thebibites.ParseFile(path, nil)
session := mutator.NewSession(archive)

err = session.StageSet(mutator.BibiteTarget(mutator.BibiteRef{
	EntryName: "bibites/bibite_0.bb8",
	BodyID:    42,
}), "body.health", 0)

fresh, err := session.Commit(outPath)
tables := thebibites.ExtractTables("mutated", fresh)
```

Lifecycle:

1. Query parsed/normalized state elsewhere.
2. Pass entry names, IDs, indexes, and paths into the mutator.
3. Stage generic operations.
4. Apply updates to in-memory entry JSON/raw bytes.
5. Commit writes the save and reparses it.

After `Apply`, entry bytes are authoritative but parser projections on the in-memory archive are invalid. Only the archive returned by `Commit` should be queried.

## Implemented Surface

Generic set:

```go
session.StageSet(target, "some.path[0].field", value)
session.StageSetWithOptions(target, path, value, mutator.SetOptions{CreateMissing: true})
```

Targets:

- `EntryTarget(entryName, kind, guards...)`
- `BibiteTarget(BibiteRef{EntryName, BodyID})`
- `SettingsTarget(guards...)`
- `PelletsTarget(guards...)`
- `SceneTarget(guards...)`
- `VarsTarget(guards...)`
- `SpeciesTarget(guards...)`
- `PheromonesTarget(guards...)`

Guards:

```go
mutator.Require("body.id", int64(42))
mutator.Require("zones[0].id", int64(7))
```

Apply is atomic: if one staged operation fails, archive raw bytes are not changed.

## Proven Paths

Current tests prove generic set through commit, reparse, and `ExtractTables` for:

- Settings: `pelletEnergy.Value`, `independents.worldSize.Value`
- Zones: `zones[0].name`, `zones[0].fertility.Value`
- Zone geometry/state: `zones[0].posX`, `zones[0].posY`, `zones[0].radius`, `zones[0].size`
- Pellets: `pellets[0].pellets[0].pellet.amount`, `pellets[0].pellets[0].transform.position[0]`
- Bibite genes: `genes.genes.Diet`
- Bibite location: `transform.position[0]`, `transform.position[1]`, `rb2d.px`, `rb2d.py`
- Bibite health/energy fields are independently settable, but do not use energy as a kill/delete signal.

## Culling Guidance

Do not delete `bibites/bibite_*.bb8` entries yet. Fixture saves contain dead bibite files, so removing entries is a domain mutation with unknown side effects.

For a first cull/kill experiment, stage only:

```text
body.health = 0
```

Do not set `body.energy` for culling. Energy is a separate metabolic/nutritional value and may affect corpse value or other game behavior. Do not set `body.dead` until known dead entries have been inspected and matched.

## Not Implemented Yet

- Append operations.
- Delete operations.
- Entry add/remove.
- Domain wrappers beyond `StageBibiteEnergy`.
- Automatic count/link/species/corpse/pellet consistency updates.

Append is probably the next safe generic operation. Generic key deletion is lower priority because missing expected schema keys may break game reloads.

## Verification

Run:

```bash
GOCACHE=/tmp/bibicontrol-go-build go test ./...
```
