# The Bibites SQL Ref Maintainability Handoff

## Purpose

This handoff captures the current SQL mutation bridge maintainability work.

The long-term goal is to make save-format drift mostly parser-owned:

```text
saveparser/thebibites changes -> go generate -> SQL schema + SQL-ref metadata update
```

That goal is now mostly achieved for catalog/allowlist data. The parser owns a
small stable mutation-addressing interface:

- table-level `sqlrefresolver:"..."` tags on `ExtractedSave`
- field-level `sqlref:"archive.path"` tags for path-map fields
- field-level `sqlrefvalue:"number|string|bool"` tags for scalar value columns
- exported `SQLRefResolverKind` constants

`go generate` turns those tags into SQL-ref path maps, scalar value maps, and
the writable table catalog. Mutation semantics are still explicit in
`savemutator/thebibites` because locator, target, guard, and dynamic path rules
are not safe to infer from SQL schema alone.

## Current Layout

Important files:

- `savemutator/thebibites/sqlref.go`
  - public API only:
    - `SQLValueRef`
    - `SQLValueRef.WithExpected`
    - `Session.StageSQLSet`
    - `SQLSet`
    - `ResolveSQLValueRef`
- `savemutator/thebibites/sqlref_catalog.go`
  - maps parser-owned `SQLRefResolverKind` values to explicit resolver
    functions
  - `writableSQLRefTables = generatedWritableSQLRefTables`
  - catalog enumeration helpers for coverage tests
- `savemutator/thebibites/sqlref_entities.go`
  - bibite, egg, brain, pellet, pheromone resolver semantics
- `savemutator/thebibites/sqlref_settings.go`
  - settings values, settings zones, settings changer target semantics
- `savemutator/thebibites/sqlref_require.go`
  - small shared validation helpers
- `savemutator/thebibites/sqlref_generated.go`
  - generated SQL-column to archive-path maps
  - generated scalar value column maps
  - generated writable table catalog
  - do not edit manually
- `saveparser/thebibites/normalize_types.go`
  - normalized row structs
  - `SQLRefResolverKind` parser interface
  - writable path fields carry `sqlref:"archive.path"` tags
  - writable scalar value fields carry `sqlrefvalue:"number|string|bool"`
    tags
  - writable table rows carry `sqlrefresolver:"..."` tags
- `cmd/gen_thebibites_schema/main.go`
  - generator for parser metadata, DuckDB migration, SQL-ref path/value maps,
    and generated writable catalog entries
- `duckdb/sqlref_scan.go`
  - converts query result locator columns into `SQLValueRef`

The main background handoff remains:

- `docs/the-bibites-sql-mutation-bridge-handoff.md`

## What Is Generated

`go generate ./saveparser/thebibites` writes:

- `saveparser/thebibites/normalize_metadata.go`
- `duckdb/migrations/0001_extracted_save.sql`
- `savemutator/thebibites/sqlref_generated.go`

The SQL-ref generated file currently contains direct path maps such as:

- `bibiteColumnPaths`
- `bibiteBodyColumnPaths`
- `eggColumnPaths`
- `brainNodeColumnKeys`
- `pelletColumnPaths`
- `settingsZoneColumnPaths`

Those maps come from `sqlref` struct tags in `normalize_types.go`.

It also contains scalar value maps such as:

- `geneValueColumnTypes`
- `settingsValueColumnTypes`
- `settingsChangerTargetColumns`

Those maps come from `sqlrefvalue` struct tags in `normalize_types.go`.

Finally, it contains:

- `generatedWritableSQLRefTables`

That slice comes from table-level `sqlrefresolver` tags on `ExtractedSave`.

Important rule: locator columns should not get `sqlref` tags. Examples:

- `entry_name`
- `body_id`, `egg_id`
- `has_body_id`, `has_egg_id`
- row indexes such as `node_row_index`, `group_index`, `zone_index`
- guard-only fields such as `zone_id`

Scalar value columns should use `sqlrefvalue`, not `sqlref`. Examples:

- `number_value`
- `string_value`
- `bool_value`

## Current Boundary

Generated data owns stable parser-facing metadata:

```text
SQL column -> archive JSON path suffix/key
SQL scalar column -> scalar value type
normalized table -> resolver kind
writable table.column allowlist
```

Manual resolver code owns semantics:

```text
which locator is required
which target entry to mutate
which guard to attach
how dynamic path rows are validated
how each resolver kind maps to a target/path resolver
```

Do not replace this with runtime path guessing. The bridge is intentionally an
allowlisted archive mutation path, not an editable SQL model.

## Save Format Change Impact

If the save format changes but existing normalized tables and resolver semantics
still apply, changes should usually stay in:

- `saveparser/thebibites`
- `sqlref` tags in `saveparser/thebibites/normalize_types.go`
- `sqlrefvalue` tags for scalar value rows
- `sqlrefresolver` tags for writable table rows

Then run:

```sh
GOMODCACHE=/tmp/bibicontrol-go-mod GOCACHE=/tmp/bibicontrol-go-build \
  go generate ./saveparser/thebibites
```

Other packages still need edits when semantics change:

- New writable table/column:
  - add parser extraction/normalized row fields
  - add `sqlref` or `sqlrefvalue` tags for writable columns
  - add a table-level `sqlrefresolver` tag if the table is writable
  - run generator
  - no manual catalog entry should be needed if the resolver kind already
    exists
- New resolver shape:
  - add a `SQLRefResolverKind` constant in `normalize_types.go`
  - add the generator validation entry in `cmd/gen_thebibites_schema/main.go`
  - add a switch case in `sqlref_catalog.go`
  - add explicit resolver semantics in `sqlref_entities.go` or
    `sqlref_settings.go`
- New locator shape:
  - update `SQLValueRef`
  - update `duckdb/sqlref_scan.go`
  - update resolver logic
- New dynamic/settings behavior:
  - update `sqlref_settings.go`
- Entity nesting or identity changes:
  - update `sqlref_entities.go`

## Recent Refactor State

The bridge was split from one large `sqlref.go` into focused files. The API file
is now small; the catalog and resolver logic are separate.

DRY work already done:

- direct path-map table entries use `pathMapSQLRefTable`
- direct path map resolution uses `pathMapResolver`
- all writable table catalog entries are generated from parser-owned
  `sqlrefresolver` tags
- scalar value allowlist maps are generated from parser-owned `sqlrefvalue`
  tags
- `sqlref_catalog.go` only maps resolver kinds to explicit resolver functions
- repeated required-field errors use helpers in `sqlref_require.go`
- zone-id guard construction uses `zoneIDGuards`
- brain node/synapse indexed resolution shares `resolveEntityBrainIndexedColumn`
- top-level SQL-ref tests use `stageSQLRefSet`
- repeated bibite body-ID table test setup is table-driven

## Tests/Checks Recently Used

Focused resolver checks:

```sh
GOMODCACHE=/tmp/bibicontrol-go-mod GOCACHE=/tmp/bibicontrol-go-build \
  go test ./savemutator/thebibites \
  -run 'TestResolveSQLValueRefsAllowlist|TestWritableSQLRefCatalogMatchesNormalizedSchema|TestSQLSetRejectsUnsafeSettingsValueRefs|TestStageSQLSetStagesResolvesAndCommits|TestStageSQLSetUpdatesSettingsValueRows'
```

Mechanical check:

```sh
git diff --check
```

Additional checks used after the latest generation pass:

```sh
GOMODCACHE=/tmp/bibicontrol-go-mod GOCACHE=/tmp/bibicontrol-go-build \
  go test ./savemutator/thebibites

GOMODCACHE=/tmp/bibicontrol-go-mod GOCACHE=/tmp/bibicontrol-go-build \
  go test ./duckdb \
  -run 'TestImportExtractedSaveCoversEveryTable|TestLargestFixtureQueryRefsExistInArchiveState|TestDumpJSONScalarPaths|TestScanSQLRefs'

GOMODCACHE=/tmp/bibicontrol-go-mod GOCACHE=/tmp/bibicontrol-go-build \
  go test ./saveparser/thebibites
```

`go test ./duckdb` without `-run` currently reaches
`TestSmokeFixtureSetsBibitesGreenAndZoneFertility`, which installs a smoke save
under the live Unity Savefiles directory. In the managed sandbox that failed
with a read-only filesystem error for:

```text
/home/asemones/.config/unity3d/The Bibites/The Bibites/Savefiles
```

## Next Good Step

The writable catalog itself is now generated. The next useful step is generated
locator metadata, not generated dynamic path semantics.

Possible shape:

```go
type SQLRefLocatorSpec struct {
    Table string
    RequiredColumns []string
    OptionalGuardColumns []string
}
```

That metadata could be emitted from resolver kinds and used by tests or query
helpers to tell users which locator columns they must select with a writable
SQL cell. Keep it descriptive; do not make it construct archive paths.

Another useful step is a generator-side test or generated assertion that every
`SQLRefResolverKind` constant is accepted by both:

- `cmd/gen_thebibites_schema/main.go` validation
- `savemutator/thebibites/sqlref_catalog.go` resolver-kind switch

## Caution

Do not generate special/dynamic resolver semantics until there is a clear,
parser-owned declarative model for locators and guards. The current explicit
resolver functions are safer than a generic guesser.

Semantics that should remain explicit for now:

- genes (`owner_kind`, `owner_id`, `path`)
- brain nodes/synapses (`owner_kind`, row indexes, body/egg target)
- stomach content (`content_index`)
- pellets (`group_index`, `group_pellet_index`, optional zone guard)
- pheromones (`pheromone_index`)
- settings value rows (`owner_kind`, `owner_id`, wrapped/unwrapped values)
- settings changer targets (`Zone(N).setting` map keys)
