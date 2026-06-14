# Parser Normalization Handoff

## Current State

The Bibites save parser lives in:

`saveparser/thebibites`

The parser is currently isolated from UI, DB, process control, scripting, and save mutation logic.

Implemented public APIs:

- `ParseFile(path string, options *Options) (*Archive, error)`
- `ParseEntryBytes(name string, raw []byte) (*ParsedEntry, error)`
- `ParseBytesAs(kind EntryKind, raw []byte) (*ParsedEntry, error)`
- `ParseEntryBytesAs(name string, kind EntryKind, raw []byte) (*ParsedEntry, error)`

Current parser capabilities:

- Reads full The Bibites save zip archives.
- Safely validates zip entry names.
- Rejects duplicate entries and configured size/count limit violations.
- Preserves raw bytes for every entry.
- Preserves opaque `data.bin`.
- Preserves preview `img.png`.
- Computes SHA-256 for archive and entries.
- Classifies known entries.
- Decodes UTF-8 BOM JSON payloads.
- Keeps decoded JSON values on entries.
- Extracts typed/queryable structures for scene, settings, species, bibites, eggs, pellets, pheromones, brain nodes/synapses, stomach contents, children, transforms, rigid-body state, and scalar fallback values.
- Emits diagnostics for malformed payloads while continuing when safe.
- Derives counts separately from scene-reported counts.

The parser was refactored into focused files:

- `parser.go`: archive reading and top-level parse flow.
- `entry_parser.go`: internal parser context, entry dispatch, parse result aggregation.
- `single.go`: single payload parsing APIs.
- `scene.go`: scene state extraction.
- `settings.go`: settings extraction.
- `species.go`: species extraction.
- `entities.go`: bibite/egg extraction.
- `environment.go`: pellet/pheromone extraction.
- `brain.go`: brain node/synapse extraction.
- `components.go`: transform and rigid body helpers.
- `scalars.go`: generic scalar extraction.
- `json.go`: BOM-aware JSON decoding and JSON helpers.
- `counts.go`: derived counts and validation diagnostics.

Fixture tests pass:

```bash
GOCACHE=/tmp/bibicontrol-go-build go test ./...
```

Last known result:

```text
ok github.com/asemones/bibicontrol/saveparser/thebibites 7.303s
```

The fixture suite validates all saves in:

`testdata/saves/the-bibites`

Important validated large-save facts:

- `autosave_20260301021357.zip` has `1043` entries.
- It has `1027` bibite files.
- It has `8` egg files.
- Parsed bibites have `1027` unique `body.id` values.
- `804` parsed bibites are alive.
- `223` parsed bibites are dead.
- `scene.bb8scene` reports `nBibites=812`.
- The scene count is validation metadata, not authoritative entity count.

## Normalization Goal

The next layer should normalize `Archive` into an in-memory relational/table-shaped model.

Do not make the parser write SQL directly.

Preferred flow:

```go
archive, err := thebibites.ParseFile(path, nil)
tables := thebibites.ExtractTables(saveID, archive)
err = sqlitewriter.InsertExtractedSave(ctx, db, tables)
```

Reasoning:

- Parsing, normalization, and SQL persistence stay separate.
- The same normalized rows can later feed SQLite, DuckDB, Parquet, CSV, or tests.
- The lossless archive model remains available for future save writing/mutation.
- DB schema changes do not force parser rewrites.

## Proposed API Stub

Add a new normalizer API under the same parser package first:

```go
func ExtractTables(saveID string, archive *Archive) ExtractedSave
```

Initial type shape:

```go
type ExtractedSave struct {
    Archive SaveArchiveRow
    Entries []SaveEntryRow
    Diagnostics []DiagnosticRow
    Scene *SceneRow

    SettingsScalars []ScalarRow
    SettingsMaterials []SettingsMaterialRow
    SettingsZones []SettingsZoneRow
    SettingsBibiteSpawners []SettingsBibiteSpawnerRow
    SettingsChangers []SettingsChangerRow

    Species []SpeciesRow
    SpeciesGenes []GeneRow
    SpeciesBrainNodes []BrainNodeRow
    SpeciesBrainSynapses []BrainSynapseRow

    Bibites []BibiteRow
    BibiteGenes []GeneRow
    BibiteBody []BibiteBodyRow
    BibiteStomachContents []StomachContentRow
    BibiteChildren []BibiteChildRow
    BibiteBrainNodes []BrainNodeRow
    BibiteBrainSynapses []BrainSynapseRow

    Eggs []EggRow
    EggGenes []GeneRow
    EggBrainNodes []BrainNodeRow
    EggBrainSynapses []BrainSynapseRow

    PelletGroups []PelletGroupRow
    Pellets []PelletRow
    Pheromones []PheromoneRow

    JSONScalars []ScalarRow
}
```

## Proposed Core Rows

`save_archives`:

```text
save_id
source_path
file_name
size_bytes
sha256
```

`save_entries`:

```text
save_id
entry_index
entry_name
kind
sha256
compressed_size
uncompressed_size
has_utf8_bom
```

`parser_diagnostics`:

```text
save_id
entry_name
severity
code
message
```

`scene_state`:

```text
save_id
version
simulated_time
reported_n_bibites
reported_n_pellets
parsed_bibites
parsed_eggs
alive_bibites
dead_bibites
dying_bibites
parsed_pellets
```

`bibites`:

```text
save_id
entry_name
body_id
species_id
generation
dead
dying
health
energy
time_alive
transform_position_x
transform_position_y
transform_rotation
transform_scale
rb2d_px
rb2d_py
rb2d_vx
rb2d_vy
rb2d_r
```

`bibite_body`:

```text
save_id
entry_name
body_id
d2_size
fat_reserves_amount
attacked_dmg
times_attacked
total_damage_suffered
brain_ticks_count
vision_lookup_count
vision_sensing_count
corpse_energy_offset
```

`bibite_stomach_contents`:

```text
save_id
entry_name
body_id
content_index
material
amount
average_chunk_amount
```

`bibite_children`:

```text
save_id
entry_name
parent_body_id
child_index
child_body_id
```

Shared `*_genes` row:

```text
save_id
owner_kind
owner_id
entry_name
gene_name
number_value
bool_value
string_value
raw_json
```

Shared `*_brain_nodes` row:

```text
save_id
owner_kind
owner_id
entry_name
node_row_index
node_index
innovation
type
type_name
desc
archetype
base_activation
value
last_input
last_output
```

Shared `*_brain_synapses` row:

```text
save_id
owner_kind
owner_id
entry_name
synapse_row_index
innovation
node_in
node_out
weight
enabled
```

`eggs`:

```text
save_id
entry_name
egg_id
species_id
generation
hatch_progress
energy
transform_position_x
transform_position_y
transform_rotation
transform_scale
rb2d_px
rb2d_py
rb2d_vx
rb2d_vy
rb2d_r
```

`pellet_groups`:

```text
save_id
group_index
zone
pellet_count
```

`pellets`:

```text
save_id
pellet_index
group_index
zone
material
amount
transform_position_x
transform_position_y
transform_rotation
transform_scale
rb2d_px
rb2d_py
rb2d_vx
rb2d_vy
rb2d_r
```

Generic scalar fallback:

```text
save_id
entry_name
owner_kind
owner_id
path
type
number_value
string_value
bool_value
raw_json
```

The scalar fallback should include all known parsed scalar values for now. It keeps unknown/new fields queryable while typed rows mature.

## Setting Units Note

Current `settings.bb8settings` fixtures do not include explicit unit metadata.

There are no observed keys such as:

- `unit`
- `displayUnit`
- `suffix`

Do not hard-code setting units as parser facts yet.

If units are added, model them as a curated dictionary later:

```text
setting_name
unit_hint
unit_source
notes
```

`unit_source` should distinguish `save_file`, `inferred`, and `manual_dictionary`.

## Next Implementation Steps

1. Add `normalize.go` with `ExtractTables(saveID string, archive *Archive) ExtractedSave`.
2. Add `normalized_types.go` with the row structs.
3. Start with archive, entries, diagnostics, scene, bibites, eggs, pellets, brain rows, and scalar rows.
4. Add fixture tests that parse `autosave_20260301021357.zip`, normalize it, and assert row counts:
   - `Entries == 1043`
   - `Bibites == 1027`
   - `Eggs == 8`
   - `Pellets == 22902`
   - `Scene.ReportedNBibites == 812`
   - `Scene.ParsedBibites == 1027`
5. Add a second test for a small fixture to catch edge cases without making every test heavy.
6. Only after normalized rows are stable, add a SQLite writer package.

## Important Design Constraint

Keep these layers separate:

```text
Zip/raw archive model
        ↓
Parsed typed model
        ↓
Normalized table rows
        ↓
SQLite / DuckDB / Parquet
```

The writer/mutation path should use the lossless archive model, not the normalized analytics rows.

