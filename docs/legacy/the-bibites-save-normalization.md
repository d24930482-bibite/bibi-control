# The Bibites Save Normalization

## Scope

Normalization converts a parsed `Archive` into in-memory table-shaped rows for analytics and persistence.

It lives in `saveparser/thebibites` but remains separate from archive parsing and SQL writing.

## API

Current entry point:

```go
func ExtractTables(saveID string, archive *Archive) ExtractedSave
```

Expected flow:

```go
archive, err := thebibites.ParseFile(path, nil)
tables := thebibites.ExtractTables(saveID, archive)
err = sqlitewriter.InsertExtractedSave(ctx, db, tables)
```

The SQL writer is intentionally outside the parser package.

## Row Families

`ExtractedSave` currently groups rows for:

- archive, entries, and parser diagnostics.
- scene, vars, scene color selectors, and scene towers.
- settings values, materials, zones, zone geometry, zone groups, spawners, changers, changer points, and changer targets.
- active species, species records, species genes, and species template brain rows.
- bibites, genes, body state, mouth state, pheromone emitters, egg layers, control state, stomach contents, children, and brain rows.
- eggs, egg genes, and egg brain rows.
- pellet groups, pellets, and pheromones.
- generic JSON scalar fallback rows.

## Modeling Rules

Collections are zero-to-many. Do not hard-code fixture counts such as two zones, three pellet groups, one spawner, or two setting changers.

Rows should carry enough identity to join back to the save and source entry. Use `save_id`, `entry_name`, entity IDs when available, and stable row indexes for repeated child data.

Settings values preserve both the unwrapped scalar value and wrapper JSON when the save uses wrapper objects with `Value`.

Generic scalar fallback rows are part of the model. They keep new or not-yet-typed fields queryable while typed row coverage matures.

## Persistence Boundary

SQLite, DuckDB, Parquet, and CSV writers should consume `ExtractedSave`. They should not call parser internals or make archive parsing decisions.

Save mutation should not use normalized rows as the source of truth. Mutation should operate on the lossless parsed archive model, validate the result, and write a new archive.

## Verification

Current fixture tests assert large-save row counts and coverage for non-empty pheromones with absent setting changers:

- `TestExtractTablesLargestFixture`
- `TestExtractTablesPheromonesWithoutSettingChangers`

Run them with:

```bash
GOCACHE=/tmp/bibicontrol-go-build go test ./...
```
