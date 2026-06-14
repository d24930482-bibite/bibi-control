# The Bibites DuckDB Analytics Handoff

## Package

The DuckDB analytics importer lives in:

`duckdb`

It consumes `saveparser/thebibites.ExtractedSave` and writes typed analytical tables to DuckDB. It does not parse save archives directly and does not mutate save archives.

## Dependency

The project uses the official DuckDB Go driver:

```go
github.com/duckdb/duckdb-go/v2
```

The driver is used through `database/sql` with a blank import in `duckdb/import.go`.

## Core Flow

Expected workflow:

```go
archive, err := thebibites.ParseFile(path, nil)
tables := thebibites.ExtractTables(saveID, archive)

db, err := duckdb.OpenAndImport(ctx, "analytics.duckdb", tables)
```

Equivalent explicit flow:

```go
db, err := duckdb.Open("analytics.duckdb")
err = duckdb.ApplyMigrations(ctx, db)
err = duckdb.ReplaceExtractedSave(ctx, db, tables)
```

Pass an empty path to `duckdb.Open("")` for an in-memory DuckDB database.

## Source Of Truth

DuckDB is a query and candidate-selection engine only.

The parsed archive remains the editable source of truth. Mutation code should use DuckDB query results only as locators, then stage guarded archive edits through `savemutator/thebibites`.

Do this:

```text
Parse archive
ExtractTables
Import ExtractedSave into DuckDB
SQL selects entry_name/body_id candidates
Mutator stages guarded archive edits
Commit writes and reparses archive
```

Do not mutate DuckDB rows and treat that as save state.

## Schema

Schema migration:

`duckdb/migrations/0001_extracted_save.sql`

The migration creates one table for every row family in `ExtractedSave`, plus a helper view:

```sql
bibite_mutation_refs
```

The view exposes:

```text
save_id
entry_name
body_id
health
energy
dead
dying
has_body_id
```

for rows with `has_body_id`.

## Imported Table Families

Archive and diagnostics:

- `save_archives`
- `save_entries`
- `diagnostics`

Scene and vars:

- `scenes`
- `vars`
- `scene_color_selectors`
- `scene_phero_towers`
- `scene_rad_towers`

Settings:

- `settings_simulation_values`
- `settings_independent_values`
- `settings_materials`
- `settings_material_values`
- `settings_zones`
- `settings_zone_geometry`
- `settings_zone_values`
- `settings_zone_groups`
- `settings_bibite_spawners`
- `settings_changers`
- `settings_changer_points`
- `settings_changer_targets`

Species:

- `active_species`
- `species`
- `species_genes`
- `species_brain_nodes`
- `species_brain_synapses`

Bibites:

- `bibites`
- `bibite_genes`
- `bibite_body`
- `bibite_mouth`
- `bibite_pheromone_emitters`
- `bibite_egg_layers`
- `bibite_control`
- `bibite_stomach_contents`
- `bibite_children`
- `bibite_brain_nodes`
- `bibite_brain_synapses`

Eggs:

- `eggs`
- `egg_genes`
- `egg_brain_nodes`
- `egg_brain_synapses`

Environment and fallback:

- `pellet_groups`
- `pellets`
- `pheromones`
- `json_scalars`

## Column Shape

Tables use snake_case column names.

Most fields map directly from `normalize_types.go`. A few names avoid SQL ambiguity or improve readability:

- `Type` fields import as `value_type`, except brain node type imports as `node_type`.
- `SettingsChangerRow.Repeat` imports as `repeat_enabled`.
- `SettingsChangerRow.Start` imports as `start_time`.
- `BrainNodeRow.Desc` imports as `node_desc`.

Raw JSON already exposed by normalization is preserved as `TEXT` columns such as `raw_json`, `wrapper_raw_json`, and `heading_raw_json`.

The importer does not currently store full archive entry raw JSON for every entry. Add that only if JSON spelunking in DuckDB needs complete per-entry documents.

## Import Semantics

Entry points:

- `Open(path string) (*sql.DB, error)`
- `OpenAndImport(ctx, path, save) (*sql.DB, error)`
- `ApplyMigrations(ctx, db) error`
- `ImportExtractedSave(ctx, db, save) error`
- `ReplaceExtractedSave(ctx, db, save) error`

`ImportExtractedSave` applies migrations and then calls `ReplaceExtractedSave`.

`ReplaceExtractedSave` runs in one transaction. It deletes existing rows for `save.Archive.SaveID` from all known tables, then inserts the current `ExtractedSave`.

This makes repeated imports of the same save ID idempotent, but it also means `save_id` must be chosen deliberately if multiple snapshots should coexist.

## Candidate Query Example

Basic culling candidate query:

```sql
SELECT entry_name, body_id
FROM bibite_mutation_refs
WHERE has_body_id
  AND NOT dead
  AND NOT dying
  AND health > 0
ORDER BY energy ASC
LIMIT 100;
```

Gene-aware query:

```sql
SELECT b.entry_name, b.body_id
FROM bibites b
JOIN bibite_genes g
  ON g.save_id = b.save_id
 AND g.entry_name = b.entry_name
 AND g.owner_id = CAST(b.body_id AS VARCHAR)
WHERE b.has_body_id
  AND NOT b.dead
  AND g.gene_name = 'Diet'
  AND g.number_value < 0.25;
```

Mutation code should convert query rows into guarded mutator refs:

```go
ref := mutator.BibiteRef{
	EntryName: entryName,
	BodyID:    bodyID,
}
err := session.StageSet(mutator.BibiteTarget(ref), "body.health", 0)
```

## Tests

Current coverage:

- `duckdb/import_test.go` builds a synthetic `ExtractedSave`.
- It inserts at least one row into every imported table.
- It verifies `bibite_mutation_refs`.
- It verifies repeated import replaces rows for the same `save_id`.

Run:

```bash
GOMODCACHE=/tmp/bibicontrol-go-mod GOCACHE=/tmp/bibicontrol-go-build go test ./...
```

## DuckDB Vs SQLite Shape

This layer is intentionally DuckDB-shaped:

- typed wide tables for scan-heavy analytics.
- simple append/replace loading by `save_id`.
- no transactional mutation/audit domain model.
- no normalized archive blob store.
- no relational constraints or migrations for long-lived OLTP state yet.

SQLite is still a better fit later for durable control-plane state: runs, mutation plans, user decisions, save lineage, audit logs, and small transactional bookkeeping.

## Next Steps

Good next slices:

1. Add a CLI or control-plane function that parses a save, extracts tables, imports them into a DuckDB file, and runs a candidate SQL query.
2. Add fixture-backed DuckDB import coverage using the largest save, not just the synthetic `ExtractedSave`.
3. Add a typed candidate result helper that scans `entry_name` and `body_id` into `savemutator/thebibites.BibiteRef`.
4. Add curated views/macros for common selections: alive bibites, low-health bibites, gene-filtered bibites, zone-bounded bibites.
5. Decide whether full entry JSON should be imported as a separate `entry_json` table for ad hoc exploration.

Avoid adding delete/entry-removal mutation workflows until the save-game side effects are better understood.
