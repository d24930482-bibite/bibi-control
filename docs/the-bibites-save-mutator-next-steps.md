# The Bibites Save Mutator Next Steps

## Current State

The parser and save writer live in `saveparser/thebibites`.

`ParseFile` reads a save archive into a lossless `Archive`. `WriteArchive` writes an `Archive` back to a ZIP save. Fixture round-trip tests live in `saveparser/thebibites/tests`, and a rewritten large save has loaded successfully in the game.

## Source Of Truth

Use `Archive` as the editable source of truth.

Do not mutate `ExtractedSave` or normalized row DTOs as save state. Normalized rows are read/query projections. They may provide locators such as `entry_name`, entity IDs, indexes, and JSON paths, but the mutator must patch archive entries.

## Recommended Shape

Start in the existing `saveparser/thebibites` package:

```go
type Session struct {
	archive *Archive
	dirty   map[string]struct{}
}

func NewSession(archive *Archive) *Session
func (s *Session) Archive() *Archive
func (s *Session) DirtyEntries() []string
func (s *Session) Write(path string) error
```

Keep this package-local until the API stabilizes. Avoid a subpackage until there is real pressure to split exports.

## First Mutations

Implement generic JSON patching first:

```go
func (s *Session) SetJSONValue(entryName, path string, value any) error
```

Then add one domain wrapper:

```go
type BibiteRef struct {
	EntryName string
	BodyID    int64
}

func (s *Session) SetBibiteEnergy(ref BibiteRef, energy float64) error
```

Domain wrappers should resolve locators, validate expected entity identity, patch the underlying decoded/raw JSON, mark the entry dirty, and refresh that entry's raw bytes.

## Serialization Rule

Untouched entries must continue to pass through byte-for-byte via `Entry.Raw`.

Dirty JSON entries may be reserialized. Preserve UTF-8 BOM when the original entry had one. After a mutation write, reparse the output and re-run normalization.

## Test Plan

For each mutation:

1. Parse a fixture.
2. Apply mutation through `Session`.
3. Write with `WriteArchive`.
4. Reparse the written save.
5. Assert the changed field moved.
6. Assert unrelated entries keep the same SHA-256.
7. Assert `ExtractTables` sees the new value.

Keep one manual game smoke test for the first mutated save before broadening the API.
