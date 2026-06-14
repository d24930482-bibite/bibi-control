# The Bibites SQL Mutation Bridge Handoff

## Purpose

This handoff covers the bridge from normalized SQL query results to staged save edits.

The goal is not to make SQL rows editable state. The parsed `Archive` remains the source of truth. SQL is used to select readable, normalized candidates; the bridge converts an allowlisted SQL cell reference into a guarded archive JSON path.

## Implemented Slice

New mutator API:

```go
err := session.StageSQLSet(mutator.SQLValueRef{
    Table:     "bibites",
    Column:    "health",
    EntryName: entryName,
    BodyID:    bodyID,
    HasBodyID: true,
}.WithExpected(currentHealth), 0)
```

Relevant files:

- `savemutator/thebibites/sqlref.go`
- `savemutator/thebibites/sqlref_test.go`
- `savemutator/thebibites/json.go`
- `saveparser/thebibites/archive.go`
- `saveparser/thebibites/parse_environment.go`
- `saveparser/thebibites/normalize.go`
- `saveparser/thebibites/normalize_types.go`
- `duckdb/tables.go`
- `duckdb/migrations/0001_extracted_save.sql`

## Core Contract

`SQLValueRef` identifies one normalized SQL cell:

```go
type SQLValueRef struct {
    Table  string
    Column string

    EntryName string

    BodyID    int64
    HasBodyID bool

    EggID    int64
    HasEggID bool

    OwnerKind string
    OwnerID   string
    Path      string

    ContentIndex          int
    HasContentIndex       bool
    GroupIndex            int
    HasGroupIndex         bool
    GroupPelletIndex      int
    HasGroupPelletIndex   bool
    Zone                  string
    HasZone               bool
    PheromoneIndex        int
    HasPheromoneIndex     bool
    NodeRowIndex          int
    HasNodeRowIndex       bool
    SynapseRowIndex       int
    HasSynapseRowIndex    bool
    ZoneIndex             int
    HasZoneIndex          bool
    ZoneID                int64
    HasZoneID             bool
}
```

Use `Has*` booleans for every numeric locator because zero is a valid index or ID.

`WithExpected(value)` adds a stale-value guard on the resolved JSON path. Use it by default when staging edits from SQL query results.

Unsupported table/column pairs return an error. This is intentional; do not add fallback path guessing.

## Supported Refs

Direct bibite fields:

- `bibites.species_id`
- `bibites.generation`
- `bibites.dead`
- `bibites.dying`
- `bibites.health`
- `bibites.energy`
- `bibites.time_alive`
- `bibites.transform_position_x`
- `bibites.transform_position_y`
- `bibites.transform_rotation`
- `bibites.transform_scale`
- `bibites.rb2d_px`
- `bibites.rb2d_py`
- `bibites.rb2d_vx`
- `bibites.rb2d_vy`
- `bibites.rb2d_r`

Bibite component tables:

- `bibite_body.*` modeled scalar columns
- `bibite_mouth.*` modeled scalar columns
- `bibite_pheromone_emitters.progress`
- `bibite_egg_layers.egg_progress`
- `bibite_egg_layers.n_eggs_laid`
- `bibite_control.total_travel`
- `bibite_stomach_contents.material`
- `bibite_stomach_contents.amount`
- `bibite_stomach_contents.average_chunk_amount`

Genes:

- `bibite_genes.number_value`
- `bibite_genes.bool_value`
- `bibite_genes.string_value`
- `egg_genes.number_value`
- `egg_genes.bool_value`
- `egg_genes.string_value`

Gene refs require `entry_name`, `owner_kind`, `owner_id`, and `path`.

Brain rows:

- `bibite_brain_nodes.*` modeled scalar columns
- `bibite_brain_synapses.*` modeled scalar columns
- `egg_brain_nodes.*` modeled scalar columns
- `egg_brain_synapses.*` modeled scalar columns

Egg fields:

- `eggs.species_id`
- `eggs.generation`
- `eggs.hatch_progress`
- `eggs.energy`
- `eggs.transform_position_x`
- `eggs.transform_position_y`
- `eggs.transform_rotation`
- `eggs.transform_scale`
- `eggs.rb2d_px`
- `eggs.rb2d_py`
- `eggs.rb2d_vx`
- `eggs.rb2d_vy`
- `eggs.rb2d_r`

Environment:

- `pellets.material`
- `pellets.amount`
- `pellets.matter_decay_time_alive`
- `pellets.matter_decay_rot_amount`
- `pellets.transform_position_x`
- `pellets.transform_position_y`
- `pellets.transform_rotation`
- `pellets.transform_scale`
- `pellets.rb2d_px`
- `pellets.rb2d_py`
- `pellets.rb2d_vx`
- `pellets.rb2d_vy`
- `pellets.rb2d_r`
- `pheromones.transform_position_x`
- `pheromones.transform_position_y`
- `pheromones.transform_rotation`
- `pheromones.transform_scale`
- `pheromones.r_strength`
- `pheromones.g_strength`
- `pheromones.b_strength`
- `pheromones.nr`
- `pheromones.ng`
- `pheromones.nb`

Settings zones:

- `settings_zones.name`
- `settings_zones.material`
- `settings_zones.distribution`

## Pellet Locator Change

`PelletRow` now includes `GroupPelletIndex`, imported as `group_pellet_index`.

Reason: `pellet_index` is flattened across all groups and is good for analytics, but save mutation needs nested paths:

```text
pellets[group_index].pellets[group_pellet_index].pellet.amount
```

Any SQL query intended to mutate `pellets` rows must select:

```sql
entry_name,
group_index,
group_pellet_index
```

Optionally select `zone` and pass it as a guard.

## Example Query

```sql
SELECT entry_name,
       body_id,
       health
FROM bibites
WHERE save_id = ?
  AND has_body_id
  AND NOT dead
  AND health > 0
ORDER BY energy ASC
LIMIT 100;
```

Then stage:

```go
err := session.StageSQLSet(mutator.SQLValueRef{
    Table:     "bibites",
    Column:    "health",
    EntryName: entryName,
    BodyID:    bodyID,
    HasBodyID: true,
}.WithExpected(health), 0)
```

## Safety Rules

- Do not mutate `ExtractedSave` or DuckDB rows as save state.
- Stage mutations against `Archive` only.
- Prefer `WithExpected` for every SQL-selected edit.
- Keep the resolver allowlisted.
- Do not infer JSON paths from column names at runtime.
- Do not add delete, append, or entry removal through this bridge yet.
- After `Apply`, parser projections on the in-memory archive are invalid until `Commit` reparses the written archive.

## Numeric Guard Note

`jsonValuesEqual` now compares `float64` values by their shortest decimal representation before building a rational. This allows SQL-scanned floats such as `0.1` to match decoded `json.Number("0.1")` guards.

## Migration Caveat

The existing DuckDB migration uses `CREATE TABLE IF NOT EXISTS`. Adding `group_pellet_index` changes the desired schema for new databases, but it will not alter an already-created DuckDB file that already has a `pellets` table.

Before relying on pellet mutation refs against an existing DuckDB file, either rebuild the database or add a follow-up migration that performs an `ALTER TABLE pellets ADD COLUMN group_pellet_index INTEGER` guarded for existing schemas.

## Tests

Coverage lives in `savemutator/thebibites/sqlref_test.go`.

Current test coverage verifies:

- SQL refs commit and reparse for bibite energy.
- SQL refs commit and reparse for bibite genes.
- SQL refs commit and reparse for stomach content amount.
- SQL refs commit and reparse for pellet amount.
- SQL refs commit and reparse for settings zone name.
- Unsupported refs fail instead of guessing.
- Pellet refs require `group_pellet_index`.
- Expected-value guard mismatches do not change raw bytes.

Full verification command:

```bash
GOMODCACHE=/tmp/bibicontrol-go-mod GOCACHE=/tmp/bibicontrol-go-build go test ./...
```

This passed after the bridge slice was added.

## Next Slices

1. Add `go generate` for row metadata, field specs, and DuckDB schema so `normalize_types.go`, `duckdb/tables.go`, and SQL migrations stop drifting.
2. Add a typed DuckDB scanner helper that converts selected rows into `SQLValueRef` structs.
3. Add fixture-backed end-to-end tests: parse largest fixture, import to DuckDB, query candidates, stage SQL refs, commit, reparse, and assert normalized values changed.
4. Decide whether settings value rows should be writable through `SQLValueRef`; wrapper `.Value` handling needs explicit mapping.
5. Add schema migration handling for already-existing DuckDB files.
6. Keep broader domain mutations separate: deletes, appends, count updates, corpse/pellet conversion, species/link consistency, and entry removal are not solved by this bridge.
