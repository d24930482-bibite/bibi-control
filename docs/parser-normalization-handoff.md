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

Cardinality rule:

- Treat fixture counts as observations, not schema constraints.
- A save can have zero or more zones, zone groups, bibite spawners, setting changers, pellet groups, pheromones, towers, color selectors, species, eggs, and bibites.
- Normalized tables should use child rows for collections and should not rely on fixed positions such as "exactly two zones" or "exactly three pellet groups".
- Fixture tests may assert known counts for specific fixture files, but implementation code must not hard-code those counts.

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
    Vars *VarsRow
    SceneColorSelectors []SceneColorSelectorRow
    ScenePheroTowers []SceneTowerRow
    SceneRadTowers []SceneTowerRow

    SettingsSimulationValues []SettingValueRow
    SettingsIndependentValues []SettingValueRow
    SettingsMaterials []SettingsMaterialRow
    SettingsMaterialValues []SettingValueRow
    SettingsZones []SettingsZoneRow
    SettingsZoneGeometry []SettingsZoneGeometryRow
    SettingsZoneValues []SettingValueRow
    SettingsZoneGroups []SettingsZoneGroupRow
    SettingsBibiteSpawners []SettingsBibiteSpawnerRow
    SettingsChangers []SettingsChangerRow
    SettingsChangerPoints []SettingsChangerPointRow
    SettingsChangerTargets []SettingsChangerTargetRow

    ActiveSpecies []ActiveSpeciesRow
    Species []SpeciesRow
    SpeciesGenes []GeneRow
    SpeciesBrainNodes []BrainNodeRow
    SpeciesBrainSynapses []BrainSynapseRow

    Bibites []BibiteRow
    BibiteGenes []GeneRow
    BibiteBody []BibiteBodyRow
    BibiteMouth []BibiteMouthRow
    BibitePheromoneEmitters []BibitePheromoneEmitterRow
    BibiteEggLayers []BibiteEggLayerRow
    BibiteControl []BibiteControlRow
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

`vars_state`:

```text
save_id
entry_name
tower_max_id
```

Scene child collections should be zero-to-many rows even though the current fixture arrays are empty:

```text
scene_color_selectors
scene_phero_towers
scene_rad_towers
```

Shared `setting_value` row shape for simulation, independent, material, and zone-scoped settings:

```text
save_id
entry_name
scope
owner_kind
owner_id
setting_name
path
type
number_value
string_value
bool_value
raw_json
```

Settings rows:

```text
settings_materials
settings_material_values
settings_zones
settings_zone_geometry
settings_zone_values
settings_zone_groups
settings_bibite_spawners
settings_changers
settings_changer_points
settings_changer_targets
```

All settings collection rows are zero-to-many. Do not infer fixed counts from the current fixtures.

`settings_zones`:

```text
save_id
entry_name
zone_index
zone_id
has_zone_id
name
material
distribution
```

`settings_zone_geometry`:

```text
save_id
entry_name
zone_index
zone_id
geometry_index
geometry_kind
position_x
position_y
radius
radius_is_relative
raw_json
```

`settings_bibite_spawners`:

```text
save_id
entry_name
spawner_index
path
spawn_priority
minimum
randomize_genes
growth_at_spawn
tagging
spawn_type
total_spawned
raw_json
```

`settings_changers`:

```text
save_id
entry_name
changer_index
name
repeat
start
raw_json
```

`settings_changer_points`:

```text
save_id
entry_name
changer_index
point_index
t
y
d
f
```

`settings_changer_targets`:

```text
save_id
entry_name
changer_index
target_key
scope
zone_index
zone_id
setting_name
type
number_value
string_value
bool_value
raw_json
```

`active_species`:

```text
save_id
entry_name
active_species_index
species_id
```

`species_records`:

```text
save_id
entry_name
species_index
species_id
has_species_id
parent_id
has_parent_id
generation_of_first_specimen
time_creation
favorite
generic_name
specific_name
description
template_version
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

`bibite_mouth`:

```text
save_id
entry_name
body_id
attacked_last_frame
bibites_bitten
bite_progress
murdered_area
total_damage_dealt
total_murders
```

`bibite_pheromone_emitters`:

```text
save_id
entry_name
body_id
progress
```

`bibite_egg_layers`:

```text
save_id
entry_name
body_id
egg_progress
n_eggs_laid
```

`bibite_control`:

```text
save_id
entry_name
body_id
total_travel
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
matter_decay_time_alive
matter_decay_rot_amount
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

`pheromones`:

```text
save_id
entry_name
pheromone_index
transform_position_x
transform_position_y
transform_rotation
transform_scale
heading_raw_json
r_strength
g_strength
b_strength
n_r
n_g
n_b
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

## Settings Scope Correction

The initial proposed settings rows are too flat.

`settings.bb8settings` contains simulation-wide settings plus scoped collections such as `zones`, `zoneGroups`, `materials`, `bibites`, and `settingsChangers`.

Model settings as scoped rows:

- Simulation-wide scalar settings should exclude structural collections.
- Zones need their own rows.
- Zone shape/coordinates should be modeled separately from zone scalar settings.
- Current inspected fixtures encode zone geometry with `posX`, `posY`, `radius`, and `radiusIsRelative`, but the table model should not assume one coordinate forever.
- Zone-specific scalar settings such as `fertility`, `biomassDensity`, `pelletSize`, `movement`, and `speed` should be queryable as zone-scoped settings.
- Setting changers need definition rows plus child rows for curve points and target/base settings.
- `settingsChangers[*].settingsBases` can target simulation-wide settings such as `pelletEnergy` and zone-scoped settings such as `Zone(0).fertility`.

Suggested settings rows:

- `settings_sim_values`
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

`settings_changer_targets` should parse target strings into:

```text
save_id
changer_index
target_key
scope
zone_index
zone_id
setting_name
type
number_value
bool_value
string_value
raw_json
```

Use `scope = simulation` for plain keys and `scope = zone` for keys matching `Zone(<index>).<setting_name>`.

## Second Fixture Pass Findings

A second pass over all eight save fixtures found no JSON parse failures.

Archive-level facts:

- All current fixtures contain the expected core entries: `settings.bb8settings`, `speciesData.json`, `scene.bb8scene`, `vars.bb8scene`, `pellets.bb8scene`, `pheromones.bb8scene`, `data.bin`, and `img.png`.
- Total parsed entity fixture coverage is `3051` bibite files and `39` egg files.
- The current fixture corpus has three pellet groups per save, but the schema must support zero or more pellet groups.
- Pheromones are present only in `dasdasd.zip` and `dddd.zip`, with `4` total pheromone rows across the fixture set.

Settings findings:

- Most top-level simulation settings are wrapper objects shaped like:

```json
{
  "interactableChanged": {
    "m_PersistentCalls": {
      "m_Calls": []
    }
  },
  "Value": 123
}
```

- The normalizer should treat `.Value` as the setting value and preserve wrapper raw JSON separately.
- `corpsesEnabled` appears only in `dasdasd.zip` and `dddd.zip`; do not assume every save has every setting key.
- `independents` is a nested map of simulation settings with the same wrapper shape. Observed independent setting keys are:
  - `SimulationSize`
  - `biomassDensity`
  - `pelletGrowth`
  - `limitBibiteBirth`
  - `bibiteLimit`
  - `virginSpawnRate`
  - `speciesGeneticSpan`
  - `speciesSpanScalingFactor`
  - `speciesPrunePointLimit`
  - `simTPS`
  - `brainTPS`
  - `decayTPS`
  - `visionLookupUpdateFactor`
  - `visionSenseUpdateFactor`
- The current fixture corpus has two zones per save, but the schema must support zero or more zones. Observed zone keys:
  - `name`
  - `id`
  - `material`
  - `distribution`
  - `fertility`
  - `biomassDensity`
  - `pelletSize`
  - `movement`
  - `speed`
  - `posX`
  - `posY`
  - `radius`
  - `radiusIsRelative`
- `zoneGroups` is present in every current fixture and is empty in every current fixture, but the schema must support non-empty zone groups.
- Observed material names are `ArmorSettings`, `FatSettings`, `MeatSettings`, and `PlantSettings`.
- Observed material keys are `cohesiveness`, `decay`, `decayRate`, `energyDensity`, `freshTime`, `massDensity`, `maxEfficiency`, `minEfficiency`, and `reactivity`.
- The current fixture corpus has one bibite spawner row per save, but the schema must support zero or more bibite spawners. Observed spawner keys:
  - `path`
  - `spawnPriority`
  - `minimum`
  - `randomizeGenes`
  - `growthAtSpawn`
  - `tagging`
  - `spawnType`
  - `totalSpawned`
- Six current fixtures have two setting changers; `dasdasd.zip` and `dddd.zip` have none. The schema must support zero or more setting changers.
- Changer curve arrays `t`, `y`, `d`, and `f` are aligned in the observed fixtures. Observed curve lengths are all `2` or all `3`.
- Observed changer targets are:
  - `Zone(0).fertility`
  - `pelletEnergy`
  - `biomassDensity`

Scene and vars findings:

- `scene.bb8scene` contains `colorSelectors`, `pheroTowers`, and `radTowers` arrays in every fixture, but all are empty in current fixtures.
- `vars.bb8scene` contains `towerMaxID`; keep it queryable as a vars scalar and consider a small typed vars row.

Species findings:

- `speciesData.json` contains `activeSpeciesList`, `recordedSpecies`, `usedSpanText`, and scalar counters such as `nextSpeciesID`, `nodeMaxInnovation`, and `connectionMaxInnovation`.
- Species records include `parentID` and `timeCreation`; these should be added to typed species rows.
- Species templates use direct `template.genes`, `template.nodes`, and `template.synapses`.
- Add a normalized active species list row.

Entity findings:

- Bibite and egg genes store actual gene values under `genes.genes`.
- Gene row extraction should unwrap `genes.genes` for bibites and eggs while still preserving wrapper scalars such as `speciesID`, `gen`, `isReady`, `isMutant`, and optional `parent1`.
- Bibite body contains nested `mouth`, `phero`, `eggLayer`, `control`, and `stomach`.
- Current parser extracts stomach contents and child links, but typed rows are still needed for:
  - mouth stats.
  - pheromone emitter progress.
  - egg layer scalar state such as `eggProgress` and `nEggsLaid`.
  - control/travel stats such as `totalTravel`.

Environment findings:

- Pellet groups have `zone` plus child `pellets`.
- Observed pellet group zones are `free pellets`, `World`, `Zone 1`, and `Zone 2`.
- Pellet rows include `transform`, `rb2d`, `pellet`, and sometimes `matterDecay`.
- Current parser extracts transform, rigid-body state, material, and amount, but typed rows still need `matterDecay` fields if present.
- Pheromone rows include `transform` and `phero`.
- Observed pheromone state keys are `heading`, `Rstrength`, `Gstrength`, `Bstrength`, `nR`, `nG`, and `nB`.

## Next Implementation Steps

1. Update the normalized row shape from the second-pass findings before writing `ExtractTables`.
2. Extend parser settings types before normalization:
   - simulation setting values.
   - independent setting values.
   - zone geometry and zone scalar settings.
   - typed bibite spawner fields.
   - setting changer points and parsed targets.
3. Extend typed parser coverage for species `parentID`/`timeCreation`, active species rows, bibite body child states, pellet `matterDecay`, and pheromone state.
4. Add `normalize.go` with `ExtractTables(saveID string, archive *Archive) ExtractedSave`.
5. Add `normalized_types.go` with the row structs.
6. Start with archive, entries, diagnostics, scene, vars, settings scope rows, species rows, bibites, eggs, pellets, pheromones, brain rows, and scalar rows.
7. Add fixture tests that parse `autosave_20260301021357.zip`, normalize it, and assert row counts:
   - `Entries == 1043`
   - `Bibites == 1027`
   - `Eggs == 8`
   - `Pellets == 22902`
   - `Scene.ReportedNBibites == 812`
   - `Scene.ParsedBibites == 1027`
8. Add a second test for `dddd.zip` or `dasdasd.zip` to cover non-empty pheromones and absent setting changers.
9. Only after normalized rows are stable, add a SQLite writer package.

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
