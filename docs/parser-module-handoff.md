# Parser Module Handoff

## Goal

Build an isolated parser module for The Bibites save files.

The parser must:

- Read zipped The Bibites save archives.
- Safely inspect and extract archive entries.
- Parse all known JSON-like save files.
- Preserve raw payloads for lossless save round-tripping.
- Extract all known fields into typed/queryable structures for analytics.
- Support future mutation workflows that write a new save archive.

## Fixture Location

Parser fixtures are already copied into the repo:

`testdata/saves/the-bibites`

Files:

- `dasdasd.zip`
- `dddd.zip`
- `autosave_20260228004041.zip`
- `autosave_20260228005042.zip`
- `autosave_20260228010042.zip`
- `s.zip`
- `d.zip`
- `autosave_20260301021357.zip`

Source path on this machine:

`/home/asemones/.config/unity3d/The Bibites/The Bibites/Savefiles`

Autosaves source path:

`/home/asemones/.config/unity3d/The Bibites/The Bibites/Savefiles/Autosaves`

## Observed Archive Shape

The save files are zip archives.

Common entries:

- `settings.bb8settings`
- `speciesData.json`
- `scene.bb8scene`
- `vars.bb8scene`
- `pellets.bb8scene`
- `pheromones.bb8scene`
- `data.bin`
- `img.png`
- `bibites/bibite_*.bb8`
- `eggs/egg_*.bb8`

`eggs/` is present only when eggs exist.

## File Formats

Most `.bb8`, `.bb8scene`, `.bb8settings`, and `.json` files are JSON text with a UTF-8 BOM.

Important parser detail:

- Use BOM-aware decoding.
- Do not assume line-delimited JSON.
- Do not assume pretty JSON.
- Most payloads are one compact line.

`img.png` is a preview image.

`data.bin` is binary and currently opaque. It must be preserved during round-trip save writes. Do not drop it.

## Observed Counts

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

In `autosave_20260301021357.zip`:

- Archive contains 1027 `bibites/*.bb8` files.
- `scene.bb8scene` reports `nBibites: 812`.
- BOM-tolerant parse found 1027 unique `body.id` values.
- 804 bibites have `body.dead == false` and `body.dying == false`.
- 223 bibites have `body.dead == true`.

Conclusion:

Do not treat `scene.nBibites` as the authoritative file/entity count. Store reported counts and derived counts separately.

## Observed Bibite Shape

Top-level bibite keys:

- `transform`
- `rb2d`
- `genes`
- `body`
- `clock`
- `brain`

Important fields:

- `body.id`
- `body.dead`
- `body.dying`
- `body.health`
- `body.energy`
- `clock.timeAlive`
- `genes.speciesID`
- `genes.gen`
- `transform.position`
- `transform.rotation`
- `transform.scale`
- `rb2d.px`
- `rb2d.py`
- `rb2d.vx`
- `rb2d.vy`
- `rb2d.r`
- `brain.Nodes`
- `brain.Synapses`

Nested body data observed:

- `mouth`
- `stomach.content`
- `phero`
- `eggLayer`
- `control`
- scalar state such as health, energy, size, damage, death flags, travel stats.

Egg files are similar but use an `egg` object instead of bibite `body`/`clock` state.

## Analytical Requirement

Analytics must be able to query all known save data.

Do not rely only on JSON blobs.

The parser should produce:

1. Raw representation:
   - archive metadata.
   - raw bytes/hash per entry.
   - raw JSON for round-trip mutation and unknown fields.

2. Typed analytical representation:
   - flattened scalar columns.
   - child records for repeated/nested structures.
   - stable IDs linking every row to save revision, archive entry, entity ID, and run.

Expected analytical outputs:

- `save_archives`
- `save_entries`
- `scene_state`
- `settings_scalars`
- `settings_materials`
- `settings_zones`
- `species_records`
- `species_template_genes`
- `species_template_brain_nodes`
- `species_template_brain_synapses`
- `bibites`
- `bibite_body`
- `bibite_genes`
- `bibite_stomach_contents`
- `bibite_children`
- `bibite_brain_nodes`
- `bibite_brain_synapses`
- `eggs`
- `egg_genes`
- `egg_brain_nodes`
- `egg_brain_synapses`
- `pellets`
- `pheromones`
- parser diagnostics

SQLite can hold operational/current extracted state. DuckDB + Parquet should be used for whole-history ad hoc analytics.

## Mutation Requirement

The parser module should not mutate originals.

Mutation flow should be:

1. Load archive.
2. Parse and preserve raw entries.
3. Stage changes through a domain API.
4. Validate consistency.
5. Write a new archive.
6. Hash new archive.
7. Record provenance.

Examples of future mutations:

- Select bibites where conditions are true.
- Change settings according to a script.
- Delete half of selected bibites and transform them into meat pellets.

This means parser/writer must understand:

- removing `bibites/bibite_*.bb8` entries.
- adding/updating `pellets.bb8scene`.
- updating reported scene counts if needed.
- preserving `data.bin`, `img.png`, unknown fields, and unknown entries.

## Parser Safety Requirements

Zip handling:

- Reject absolute paths.
- Reject `..` path traversal.
- Reject paths that would extract outside a workspace.
- Enforce max archive size.
- Enforce max uncompressed size.
- Enforce max file count.
- Validate archive integrity before parsing.

Parsing:

- Parse files independently.
- Emit diagnostics per entry.
- Keep parsing other entries when safe.
- Preserve raw payload for entries that fail typed parsing.

## Recommended First Implementation Steps

1. Create parser package/module with no dependency on process control, UI, scripting, or DB.
2. Implement archive reader:
   - list entries.
   - validate paths.
   - hash archive and entries.
   - classify entries by type.
3. Implement BOM-aware JSON loader.
4. Parse:
   - `scene.bb8scene`
   - `vars.bb8scene`
   - `speciesData.json`
   - `settings.bb8settings`
   - `bibites/*.bb8`
   - `eggs/*.bb8`
   - `pellets.bb8scene`
   - `pheromones.bb8scene`
5. Produce an in-memory parsed model.
6. Produce diagnostics and derived counts.
7. Add fixture tests:
   - every fixture opens.
   - every expected entry parses or is preserved with diagnostics.
   - derived counts match known fixture counts.
   - large fixture identifies 1027 bibite files and 804 alive bibites.
8. Add exporter interface:
   - SQLite writer later.
   - Parquet writer later.
   - JSON debug dump for early development.

## Open Questions

- What is `data.bin`?
- Does the game require archive entry order?
- Does the game require UTF-8 BOMs on rewritten JSON?
- Does the game tolerate pretty-printed JSON?
- Does the game require `img.png` to match current state?
- How should `scene.nBibites` be updated during mutation?
- Are dead bibite files intentionally kept for corpse/meat/history state?
- Which fields are safe to mutate without breaking reload?

