# The Bibites SQL Mutation Bridge Handoff

## Purpose

This handoff covers the bridge from normalized SQL query results to staged save edits.

The goal is not to make SQL rows editable state. The parsed `Archive` remains the source of truth. SQL is used to select readable, normalized candidates; the bridge converts an allowlisted SQL cell reference into a guarded archive JSON path.

## Current Status

The SQL `SET` path is implemented end to end for allowlisted normalized cells:

```text
Parse save -> ExtractTables -> DuckDB import -> SQL query -> ScanSQLRefs
-> SQLValueRef.WithExpected(current_cell_value) -> Session.StageSQLSet(...)
-> Commit -> reparse -> assert normalized values changed
```

This is a guarded archive mutation path, not an editable-DuckDB path.

Implemented pieces:

- `savemutator/thebibites.SQLValueRef`
- `Session.StageSQLSet`
- `duckdb.ScanSQLRefs`
- settings value row mapping
- zone-scoped `settings_changer_targets.number_value` mapping
- quoted JSON path keys for `settingsBases["Zone(0).fertility"]`
- centralized writable-ref catalog in `savemutator/thebibites/sqlref_catalog.go`
- generated SQL-column to archive-path maps from `sqlref` tags in `normalize_types.go`
- exhaustive cheap resolver coverage for the allowlisted `table.column` pairs
- live-fixture commit/reparse coverage for observed writable refs
- fixture-backed targeted smoke test that mutates the large save and installs it into The Bibites `Savefiles` directory
- fixture-backed all-observed-field smoke test that writes named output zips and installs them into `Savefiles`

Important distinction: there are now two installed smoke paths. The targeted
smoke proves the direct refeed path for bibite color genes plus zone fertility
and zone-scoped fertility changer targets. The all-observed-field smoke mirrors
the live-fixture matrix, mutates representative rows for every observed
writable `table.column`, writes named zips, and installs those zips. Manual
game-load verification is still the remaining human check.

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
- `savemutator/thebibites/install.go`
- `savemutator/thebibites/install_test.go`
- `savemutator/thebibites/path.go`
- `savemutator/thebibites/session_test.go`
- `savemutator/thebibites/json.go`
- `saveparser/thebibites/archive.go`
- `saveparser/thebibites/parse_environment.go`
- `saveparser/thebibites/normalize.go`
- `saveparser/thebibites/normalize_types.go`
- `saveparser/thebibites/normalize_metadata.go`
- `duckdb/sqlref_scan.go`
- `duckdb/sqlref_scan_test.go`
- `duckdb/import.go`
- `duckdb/migrations/0001_extracted_save.sql`
- `cmd/gen_thebibites_schema/main.go`

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

    SettingName    string
    Scope          string
    TargetKey      string
    ValueType      string
    WrapperRawJSON string

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
    ChangerIndex          int
    HasChangerIndex       bool

    Expected              any
    HasExpected           bool
}
```

Use `Has*` booleans for every numeric locator because zero is a valid index or ID.

`WithExpected(value)` adds a stale-value guard on the resolved JSON path. Use it by default when staging edits from SQL query results.

Unsupported table/column pairs return an error. This is intentional; do not add fallback path guessing.

## Writable Ref Catalog

`savemutator/thebibites/sqlref.go` is the public bridge API: `SQLValueRef`,
`StageSQLSet`, `SQLSet`, and `ResolveSQLValueRef`.

`savemutator/thebibites/sqlref_catalog.go` owns the writable SQL-ref catalog:

```go
var writableSQLRefTables = []sqlRefTableSpec{...}
```

That catalog now drives both:

- `ResolveSQLValueRef` table dispatch
- `writableSQLRefKeys()` coverage enumeration used by the live fixture matrix

This replaced the previous test-local mirrored writable list. When adding or
removing a writable `table.column`, update the production catalog. For direct
normalized row fields, the SQL-column to archive-path maps are generated into:

```text
savemutator/thebibites/sqlref_generated.go
```

Those maps come from `sqlref` struct tags on writable fields in:

```text
saveparser/thebibites/normalize_types.go
```

Run this after changing those tags:

```sh
go generate ./saveparser/thebibites
```

The generator writes the parser metadata, DuckDB migration, and SQL-ref path
maps together. Locator columns such as `entry_name`, IDs, row indexes, and
guard-only fields should not get `sqlref` tags.

Resolver logic is split by domain:

- `sqlref_entities.go`: bibite, egg, brain, pellet, and pheromone refs
- `sqlref_settings.go`: settings value rows, zones, and changer targets

Dynamic EAV refs and special resolver cases should stay explicit in those
resolver files; do not make them path guesses.

`TestWritableSQLRefCatalogMatchesNormalizedSchema` checks that every catalog
`table.column` exists in generated normalized schema metadata. The coverage
tests should follow from the production catalog instead of maintaining their own
list of writable refs.

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

Observed live saves currently contain numeric and bool gene rows, but no string
gene rows. `bibite_genes.string_value` and `egg_genes.string_value` are
schema-capable resolver refs, not observed live-save refs.

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

Settings value rows:

- `settings_simulation_values.number_value`
- `settings_simulation_values.bool_value`
- `settings_simulation_values.string_value`
- `settings_independent_values.number_value`
- `settings_independent_values.bool_value`
- `settings_independent_values.string_value`
- `settings_material_values.number_value`
- `settings_material_values.bool_value`
- `settings_material_values.string_value`
- `settings_zone_values.number_value`
- `settings_zone_values.bool_value`
- `settings_zone_values.string_value`

Settings value refs require `entry_name`, `owner_kind`, `owner_id`, `setting_name`, `path`, `value_type`, and `wrapper_raw_json`.

Observed live saves currently contain:

- simulation, independent, and material settings as number/bool rows, but not string rows
- zone settings as number/string rows, but not bool rows

Therefore these refs are schema-capable but not observed in the active live save
set:

- `settings_simulation_values.string_value`
- `settings_independent_values.string_value`
- `settings_material_values.string_value`
- `settings_zone_values.bool_value`

Do not manufacture coverage for these by type-changing unrelated scalar rows.
That would only prove generic scalar preservation/editing, not that the bridge
is exercising a game-authored domain row.

The resolver verifies exact table/path/owner consistency and only appends `.Value` when `wrapper_raw_json` is a JSON object containing `Value`. This matters because zone settings may be represented either as wrapped values:

```text
zones[0].fertility.Value
```

or direct scalar values:

```text
zones[0].size
```

Settings changer targets:

- `settings_changer_targets.number_value`

Only zone-scoped numeric changer targets are writable right now. The supported target shape is:

```text
target_key = Zone(N).settingName
scope = zone
setting_name = settingName
zone_index = N
value_type = number
```

It resolves to:

```text
settingsChangers[changer_index].settingsBases["Zone(N).settingName"]
```

Simulation-wide changer targets such as `pelletEnergy` and `biomassDensity` are parsed into `settings_changer_targets`, but are not writable through `SQLValueRef` yet.

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

## Direct Refeed Adapter

`duckdb.ScanSQLRefs` converts DuckDB query rows into typed `SQLValueRef` values plus the current selected cell value.

This is the direct refeed path:

```text
DuckDB query row -> SQLValueRef.WithExpected(current_cell_value) -> Session.StageSQLSet(...)
```

The adapter should not make arbitrary SQL editable. It should only populate locator fields from selected column names and then let `ResolveSQLValueRef` remain the final allowlist.

Example shape:

```go
refs, err := duckdb.ScanSQLRefs(rows, duckdb.SQLRefScanSpec{
    Table:  "bibites",
    Column: "health",
})
if err != nil {
    return err
}

for _, row := range refs {
    if err := session.StageSQLSet(row.Ref.WithExpected(row.CurrentValue), 0); err != nil {
        return err
    }
}
```

Queries must select every locator column required by the target table/column pair. For example:

- `bibites.health` needs `entry_name`, `body_id`, and `has_body_id`.
- `bibite_stomach_contents.amount` needs `entry_name`, `body_id`, `has_body_id`, and `content_index`.
- `pellets.amount` needs `entry_name`, `group_index`, and `group_pellet_index`; `zone` is optional as an extra guard.
- `settings_zones.name` needs `entry_name` and `zone_index`; `zone_id` plus `has_zone_id` is optional as an extra guard.
- `settings_zone_values.number_value` needs `entry_name`, `owner_kind`, `owner_id`, `setting_name`, `path`, `value_type`, `wrapper_raw_json`, and optionally `zone_index`, `zone_id`, `has_zone_id`.
- `settings_changer_targets.number_value` for zone targets needs `entry_name`, `changer_index`, `target_key`, `scope`, `zone_index`, `setting_name`, `value_type`, and optionally `zone_id`, `has_zone_id`.

The scanner may infer `SQLValueRef` fields from returned column names, but it must not infer archive JSON paths. Path resolution stays in `savemutator/thebibites/sqlref.go`.

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
- Treat settings changer targets as separate from static settings values. The game can use `settingsChangers[*].settingsBases` to override or repopulate values such as World fertility.

## Settings Changer Caveat

The large fixture has this raw settings shape:

```text
settingsChangers[0] "season"
  settingsBases:
    Zone(0).fertility = 0.7827918
    pelletEnergy = 1000.0

settingsChangers[1] "Climate"
  settingsBases:
    biomassDensity = 0.01
```

Changing only `zones[0].fertility` is not enough for the World zone in the game. The season changer also carries `settingsBases["Zone(0).fertility"]`, and the game appears to use that value. The smoke test now updates both:

```text
zones[0].fertility = 30
settingsChangers[0].settingsBases["Zone(0).fertility"] = 30
```

The mutator JSON path parser supports quoted map keys for this case:

```text
settingsChangers[0].settingsBases["Zone(0).fertility"]
```

Do not replace this with dotted path syntax; `Zone(0).fertility` is a single JSON object key.

## Savefile Installer

`savemutator/thebibites/install.go` provides reusable helpers:

- `DefaultSavefilesDir()`
- `InstallSaveFile(srcPath, dstName)`
- `InstallSaveFileToDir(srcPath, dstDir, dstName)`

Default Linux path:

```text
~/.config/unity3d/The Bibites/The Bibites/Savefiles
```

Override with:

```bash
BIBITES_SAVEFILES_DIR=/path/to/Savefiles
```

The installer copies through a temporary file in the destination directory, then renames it into place. It rejects destination names with path separators and requires `.zip`.

## Numeric Guard Note

`jsonValuesEqual` now compares `float64` values by their shortest decimal representation before building a rational. This allows SQL-scanned floats such as `0.1` to match decoded `json.Number("0.1")` guards.

## Migration Caveat

The existing DuckDB migration uses `CREATE TABLE IF NOT EXISTS`. Adding `group_pellet_index` changes the desired schema for new databases, but it will not alter an already-created DuckDB file that already has a `pellets` table.

Before relying on pellet mutation refs against an existing DuckDB file, either rebuild the database or add a follow-up migration that performs an `ALTER TABLE pellets ADD COLUMN group_pellet_index INTEGER` guarded for existing schemas.

## Tests

Coverage lives mainly in:

- `savemutator/thebibites/sqlref_test.go`
- `savemutator/thebibites/session_test.go`
- `savemutator/thebibites/install_test.go`
- `duckdb/sqlref_scan_test.go`

Current test coverage verifies:

- Exhaustive cheap resolver tests for every allowlisted `table.column` pair:
  - resolves to the expected archive target and JSON path
  - builds a guarded `SQLSet`
  - validates operation shape
- Live-fixture path tests for observed writable refs:
  - stage every observed writable ref shape from copied live saves
  - commit to temp output
  - reparse the written zip from disk
  - assert normalized values changed at the selected rows
  - skip only schema-capable refs that are not observed in the live save set
- SQL refs commit and reparse for bibite energy.
- SQL refs commit and reparse for bibite genes.
- SQL refs commit and reparse for stomach content amount.
- SQL refs commit and reparse for pellet amount.
- SQL refs commit and reparse for settings zone name.
- SQL refs commit and reparse for settings value rows:
  - simulation values
  - independent values
  - material values
  - zone values
  - wrapped and unwrapped settings
  - number, bool, and string columns
- SQL refs commit and reparse for zone-scoped `settings_changer_targets.number_value`.
- DuckDB direct refeed for `bibites.health`.
- DuckDB direct refeed for a fixture settings simulation value.
- Large-fixture smoke test:
  - all bibites set to green via `ColorR=0`, `ColorG=1`, `ColorB=0`
  - all zone `fertility` values set to `30`
  - all zone-scoped changer-target `fertility` base values set to `30`
  - output written to `/tmp/bibicontrol-smoke/green-fertility.zip`
  - output installed to The Bibites `Savefiles/green-fertility.zip`
- All-observed-field installed smoke test:
  - starts with `autosave_20260301021357.zip`
  - selects an additional real fixture when needed for remaining observed refs, currently `dasdasd.zip` for pheromone rows
  - mutates every observed writable `table.column` through SQL refs
  - chooses a material setting row with `decay=true` for `settings_material_values.bool_value` so the bool mutation moves to `false`; flipping a non-decaying material to `true` can make the game expect missing decay fields during load
  - uses known game enum alternates for `settings_zones.distribution` and `settings_zone_values.string_value` when the selected setting is zone `movement`, avoiding parser-valid values like `CentricGradual_sql` or `None_sql` that Unity rejects during load
  - uses observed runtime matter material names for `pellets.material` and `bibite_stomach_contents.material`, avoiding settings material keys such as `ArmorSettings` that Unity cannot resolve while spawning pellets
  - commits and reparses each written zip from disk
  - asserts normalized values changed
  - writes `/tmp/bibicontrol-smoke/all-observed-sqlref-autosave_20260301021357.zip`
  - writes `/tmp/bibicontrol-smoke/all-observed-sqlref-dasdasd.zip`
  - installs those outputs into `Savefiles` with the same file names
- Installer copies to temp destination, honors `BIBITES_SAVEFILES_DIR`, and rejects unsafe destination names.
- Unsupported refs fail instead of guessing.
- Pellet refs require `group_pellet_index`.
- Settings refs reject missing wrapper metadata, wrong value type, owner/path mismatch, unsafe setting names, bad wrapper object shape, and zone index mismatch.
- JSON path parser supports quoted map keys such as `["Zone(0).fertility"]`.
- Expected-value guard mismatches do not change raw bytes.

Full verification command:

```bash
BIBITES_SAVEFILES_DIR=/tmp/bibicontrol-savefiles GOMODCACHE=/tmp/bibicontrol-go-mod GOCACHE=/tmp/bibicontrol-go-build go test ./...
```

This passed after the all-observed-field smoke was added.

The smoke tests intentionally write installable saves. Use
`BIBITES_SAVEFILES_DIR` to point installs at a writable or game-visible
`Savefiles` directory. When `BIBITES_SAVEFILES_DIR` is unset, the
all-observed-field smoke installs to `/tmp/bibicontrol-savefiles` so the
`savemutator/thebibites` package test remains sandbox-friendly.

## Auto-Generation Slice

`saveparser/thebibites/normalize_types.go` is now the source for normalized table order, table names, row field order, DuckDB import field specs, and generated DuckDB schema. `ExtractedSave` fields carry `dbtable` tags; row structs remain the source for column order and Go-to-SQL type inference.

Generation command:

```bash
GOMODCACHE=/tmp/bibicontrol-go-mod GOCACHE=/tmp/bibicontrol-go-build go generate ./saveparser/thebibites
```

Generated outputs:

- `saveparser/thebibites/normalize_metadata.go`
- `duckdb/migrations/0001_extracted_save.sql`

The generator lives at:

- `cmd/gen_thebibites_schema/main.go`

`duckdb/import.go` consumes `saveparser/thebibites.NormalizedTables` directly, so there is no separate generated DuckDB field-spec file.

The generated migration intentionally keeps the custom `bibite_mutation_refs` view trailer.

## Next Slices

1. Manually/game-verify that The Bibites loads the all-observed-field smoke
   saves.
2. Decide whether schema-capable but unobserved refs should remain writable:
   - `bibite_genes.string_value`
   - `egg_genes.string_value`
   - `settings_simulation_values.string_value`
   - `settings_independent_values.string_value`
   - `settings_material_values.string_value`
   - `settings_zone_values.bool_value`
3. Add SQL ref support for simulation-wide `settings_changer_targets` if game testing proves it is needed.
4. Add schema migration handling for already-existing DuckDB files.
5. Decide whether more settings changer target types should be writable, such as bool/string or non-zone targets.
6. Keep broader domain mutations separate: deletes, appends, count updates, corpse/pellet conversion, species/link consistency, and entry removal are not solved by this bridge.
