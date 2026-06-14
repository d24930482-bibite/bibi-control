# The Bibites Save Parser Considerations

## Fixture Source

Parser fixtures are stored in:

`testdata/saves/the-bibites`

They were copied from:

`/home/asemones/.config/unity3d/The Bibites/The Bibites/Savefiles`

## Observed Save Shape

The Bibites saves are zip archives.

Common top-level archive entries:

- `settings.bb8settings`
- `speciesData.json`
- `scene.bb8scene`
- `vars.bb8scene`
- `pellets.bb8scene`
- `pheromones.bb8scene`
- `data.bin`
- `img.png`
- `bibites/bibite_*.bb8`
- `eggs/egg_*.bb8` when eggs exist

Most `.bb8`, `.bb8scene`, `.bb8settings`, and `.json` files are JSON text with a UTF-8 BOM. They are usually written as one long line with no pretty formatting.

`img.png` is a 400x400 preview image in the inspected fixtures.

`data.bin` is binary and should be treated as opaque until its purpose is identified. One small fixture was detected by `file` as zlib-like, but direct zlib decompression failed. Do not assume it is safely ignorable until reload tests prove that.

## Fixture Counts

| Save | Bibite files | Egg files | Total files |
| --- | ---: | ---: | ---: |
| `dasdasd.zip` | 27 | 0 | 35 |
| `dddd.zip` | 27 | 0 | 35 |
| `autosave_20260228004041.zip` | 46 | 1 | 55 |
| `autosave_20260228005042.zip` | 219 | 3 | 230 |
| `autosave_20260228010042.zip` | 467 | 7 | 482 |
| `s.zip` | 585 | 11 | 604 |
| `d.zip` | 653 | 9 | 670 |
| `autosave_20260301021357.zip` | 1027 | 8 | 1043 |

In the large inspected fixture, `scene.bb8scene` reports `nBibites: 812`, while the archive contains 1027 `bibites/*.bb8` files. A BOM-tolerant JSON pass found:

- 1027 bibite files.
- 1027 unique `body.id` values.
- 804 alive bibites.
- 223 dead bibites.

The count in `scene.bb8scene` should not be treated as the authoritative number of entity files. The parser should derive counts from parsed files and store reported counts separately for validation.

## Entity JSON Shape

Observed bibite top-level keys:

- `transform`
- `rb2d`
- `genes`
- `body`
- `clock`
- `brain`

Important nested fields:

- `body.id`
- `body.dead`
- `body.dying`
- `body.health`
- `body.energy`
- `clock.timeAlive`
- `genes.speciesID`
- `genes.gen`
- `transform.position`
- `rb2d` position/velocity/rotation state
- `brain.Nodes`
- `brain.Synapses`

Egg files use a similar structure but contain `egg` state instead of `body`/`clock` state.

## Parser Requirements

The parser should:

- Open the zip safely and reject path traversal.
- Preserve archive-level metadata and hash.
- Parse each text payload with UTF-8 BOM handling.
- Avoid assuming pretty-printed JSON or line-oriented records.
- Stream or process entries incrementally rather than extracting everything into memory.
- Parse per-entity files independently so one malformed entity can be reported precisely.
- Store parser diagnostics per entry.
- Store scene-reported counts separately from derived counts.
- Treat `data.bin` as required opaque payload for save round-tripping until proven otherwise.
- Preserve unknown fields when mutating saves.

## Mutation Requirements

Save mutation should be conservative.

Rules:

- Do not rewrite fields unrelated to the mutation.
- Preserve unknown fields and original object structure where possible.
- Preserve UTF-8 BOM behavior if the game expects it.
- Preserve archive entry names.
- Preserve required binary/image payloads.
- Write a new archive instead of mutating the original.
- Validate the new archive and record its hash before reload.

Because the JSON files are emitted as compact one-line objects, any rewrite may produce large diffs. For parser tests, semantic equality is more important than byte-for-byte equality. For game reload compatibility, we need round-trip tests against the actual game.

## Storage Modeling Notes

Recommended normalized entities:

- `SaveArchive`
- `SaveEntry`
- `SceneState`
- `Bibite`
- `Egg`
- `Pellet`
- `Pheromone`
- `SpeciesRecord`
- `BrainNode`
- `BrainSynapse`

Analytics will need all known save data parsed and queryable. The parser should therefore have two outputs:

1. **Lossless raw representation** for round-trip save mutation.
2. **Normalized analytical representation** for querying every known field.

For v1, keep both:

- raw JSON per important entry for lossless re-emission.
- extracted columns/tables for all known fields, including nested brain, genes, body, pellet, pheromone, settings, species, and scene data.

Good first indexed fields:

- save revision ID.
- entity type.
- `body.id` for bibites.
- `egg.id` for eggs.
- species ID.
- alive/dead/dying status.
- position.
- time alive.
- generation.

Brain data can be large and nested. Because analytics will need all data queryable, normalize nodes and synapses into side tables instead of putting every field into the main entity row.

Recommended analytical table families:

- save/archive tables:
  - save archive metadata.
  - archive entries.
  - parser diagnostics.
  - source file hashes.
- scene tables:
  - scene summary.
  - simulation time.
  - reported counts.
  - zone/tower/color selector data when present.
- settings tables:
  - scalar settings.
  - materials.
  - zones.
  - zone groups.
  - setting changers.
- species tables:
  - species records.
  - species template genes.
  - species template brain nodes.
  - species template synapses.
  - active species list.
- bibite tables:
  - bibite identity and status.
  - transform.
  - rigid body state.
  - genes.
  - body scalar state.
  - mouth state.
  - stomach contents.
  - egg layer state.
  - children IDs.
  - pheromone emitter state.
  - control/travel stats.
  - clock state.
  - brain nodes.
  - brain synapses.
- egg tables:
  - egg identity/state.
  - transform.
  - rigid body state.
  - genes.
  - brain nodes.
  - brain synapses.
- environment tables:
  - pellets.
  - pellet zones.
  - pellet transform/physics.
  - pellet material and amount.
  - pellet decay state.
  - pheromones.
  - pheromone transform.
  - pheromone color strengths and headings.

SQLite can store the operational/current extracted data, but full analytical extraction should also be written to Parquet so DuckDB can query all fields across many saves and runs.

Column naming should be stable and explicit. Prefer flattened names like `transform_position_x`, `rb2d_velocity_y`, and `body_energy` in wide analytical tables where that improves query ergonomics. Use child tables for repeated structures like stomach contents, children IDs, brain nodes, and synapses.

Do not rely only on JSON columns for analytics. JSON columns are acceptable as a lossless fallback, but known fields should be extracted into typed columns or child tables.

## Open Questions

- What is `data.bin` used for?
- Does the game require the same archive entry ordering?
- Does the game require UTF-8 BOMs on rewritten JSON files?
- Does the game tolerate pretty-printed JSON, or must output remain compact?
- Are dead bibites intentionally preserved as entity files?
- Is `scene.nBibites` a live count, total count, or stale/approximate count?
- Which fields are safe to mutate without breaking reload?
- Can the game reload a save if `img.png` is stale?
