# The Bibites Save Parser

## Scope

The parser for The Bibites saves lives in `saveparser/thebibites`.

It is responsible for reading save archives into a lossless parsed model. It should stay independent from UI, process control, database persistence, scripting, and save mutation workflows.

## Public API

Current entry points:

```go
ParseFile(path string, options *Options) (*Archive, error)
ParseEntryBytes(name string, raw []byte) (*ParsedEntry, error)
ParseBytesAs(kind EntryKind, raw []byte) (*ParsedEntry, error)
ParseEntryBytesAs(name string, kind EntryKind, raw []byte) (*ParsedEntry, error)
```

Use `ParseFile` for full save archives. Use the single-entry APIs for tests, tools, and targeted payload inspection.

## Parsed Model

`Archive` preserves archive metadata, entry metadata, raw entry bytes, hashes, decoded JSON values, typed extracted state, scalar fallback rows, derived counts, and diagnostics.

Current typed coverage includes:

- scene and vars state.
- settings, materials, zones, spawners, and setting changers.
- species records and active species.
- bibites, eggs, genes, brain nodes, brain synapses, body child state, stomach contents, and children.
- pellet groups, pellets, pellet decay state, and pheromones.
- opaque `data.bin` and preview `img.png` as preserved entries.

## Safety Rules

Archive parsing validates paths, rejects duplicate entries, enforces configured size and count limits, and hashes the archive and each entry.

JSON payloads are decoded with UTF-8 BOM handling. Files are parsed independently so a malformed payload can produce diagnostics without discarding unrelated entries.

## Count Semantics

Scene-reported counts are validation metadata, not authoritative entity counts. Derived counts come from parsed archive entries and are stored separately from values such as `scene.nBibites`.

## Verification

Run parser and normalization fixture coverage with:

```bash
GOCACHE=/tmp/bibicontrol-go-build go test ./...
```

The fixture suite covers the save files in `testdata/saves/the-bibites`, including the large save where `scene.nBibites` differs from the number of parsed bibite files.

## Out Of Scope

The parser does not write SQL, decide analytics storage formats, mutate saves, or prove game reload compatibility. Future save writing should use the lossless `Archive` model rather than normalized analytics rows.
