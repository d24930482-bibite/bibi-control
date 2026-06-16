// Package thebibites binds a parsed Bibites save to a sandboxed Starlark
// scripting surface. T4 provides the read-only slice: load a save once and
// expose entity enumeration and friendly attribute/gene reads. Mutation (T6),
// DuckDB analytics (T5), settings (T7), and persistence (T8) attach to the seams
// left here (the lazy *sql.DB and the staged *mutator.Session) without
// restructuring this type.
package thebibites

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"sync"

	"github.com/asemones/bibicontrol/duckdb"
	mutator "github.com/asemones/bibicontrol/savemutator/thebibites"
	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

// tableAccess holds a normalized table's row slice plus an entry_name -> row
// index for O(1) attribute lookups by entity identity.
type tableAccess struct {
	slice   reflect.Value  // []RowStruct
	byEntry map[string]int // entry_name -> slice index
	order   []string       // entry names in slice order (for stable enumeration)
}

// LoadedSave is one parsed save prepared for scripting. It is parsed and
// extracted exactly once; reads are served from the in-memory ExtractedSave.
type LoadedSave struct {
	path    string
	saveID  string
	archive *tb.Archive
	tables  tb.ExtractedSave

	// db is opened lazily on the first analytical query (T5); it stays nil for
	// pure read/enumerate runs. session stages mutations for the eventual single
	// write (T6); never applied during a read.
	db      *sql.DB
	session *mutator.Session

	access map[string]*tableAccess // table name -> rows + entry_name index

	geneOnce sync.Once
	geneIdx  map[string]map[string]*geneSet // kind -> entry_name -> genes

	// mirrorDirty marks pending scalar mutations that DuckDB has not yet observed.
	// T5 never sets it (no mutations); T6 flips it on every staged set and clears
	// it in flushMirror. It exists now so the query/aggregate entry points already
	// call flushMirror and T6 only has to fill the buffer.
	mirrorDirty bool

	// dbOpenCount / rowsMaterialized are test instrumentation: the analytics path
	// must open DuckDB at most once per run and must not materialize Entity values
	// to aggregate. Tests assert these counters.
	dbOpenCount      int
	rowsMaterialized int
}

// geneSet holds one entity's genes both in save order (for iteration) and by
// name (for gene() lookup).
type geneSet struct {
	order  []tb.GeneRow
	byName map[string]tb.GeneRow
}

// Load parses and normalizes a save file once and prepares it for scripting. It
// does not open DuckDB.
func Load(path string) (*LoadedSave, error) {
	archive, err := tb.ParseFile(path, nil)
	if err != nil {
		return nil, fmt.Errorf("load save %q: %w", path, err)
	}
	saveID := archive.SHA256
	if saveID == "" {
		saveID = archive.FileName
	}
	ls := &LoadedSave{
		path:    path,
		saveID:  saveID,
		archive: archive,
		tables:  tb.ExtractTables(saveID, archive),
		session: mutator.NewSession(archive),
	}
	ls.buildAccess()
	return ls, nil
}

// buildAccess indexes every table referenced by entityTables by entry_name so
// attribute reads (including 1:1 sub-tables) are O(1).
func (ls *LoadedSave) buildAccess() {
	ls.access = make(map[string]*tableAccess)
	extracted := reflect.ValueOf(ls.tables)
	extractedType := extracted.Type()

	saveFieldByTable := make(map[string]string)
	for _, spec := range tb.NormalizedTables {
		saveFieldByTable[spec.Table] = spec.SaveField
	}

	seen := make(map[string]bool)
	for _, tables := range entityTables {
		for _, table := range tables {
			if seen[table] {
				continue
			}
			seen[table] = true
			saveField, ok := saveFieldByTable[table]
			if !ok {
				continue
			}
			fld, ok := extractedType.FieldByName(saveField)
			if !ok {
				continue
			}
			slice := extracted.FieldByIndex(fld.Index)
			if slice.Kind() != reflect.Slice {
				continue
			}
			ls.access[table] = indexByEntryName(slice)
		}
	}
}

// indexByEntryName builds an entry_name -> index map over a row slice. Rows
// without an EntryName field (or duplicates) are skipped/last-wins respectively.
func indexByEntryName(slice reflect.Value) *tableAccess {
	ta := &tableAccess{slice: slice, byEntry: make(map[string]int, slice.Len())}
	if slice.Len() == 0 {
		return ta
	}
	elem := slice.Type().Elem()
	sf, ok := elem.FieldByName("EntryName")
	if !ok {
		return ta
	}
	ta.order = make([]string, slice.Len())
	for i := 0; i < slice.Len(); i++ {
		name := slice.Index(i).FieldByIndex(sf.Index).String()
		ta.byEntry[name] = i
		ta.order[i] = name
	}
	return ta
}

// readAttr resolves a scalar attribute for the entity identified by entryName.
// Returns (nil, true) when the column maps to a 1:1 sub-table that has no row for
// this entity, so the caller can surface None.
func (ls *LoadedSave) rowForEntry(table, entryName string) (reflect.Value, bool) {
	ta := ls.access[table]
	if ta == nil {
		return reflect.Value{}, false
	}
	idx, ok := ta.byEntry[entryName]
	if !ok {
		return reflect.Value{}, false
	}
	return ta.slice.Index(idx), true
}

// genesFor returns an entity's gene set (in save order + by name), building the
// per-kind gene index lazily on first use. Returns nil when the entity has no
// genes.
func (ls *LoadedSave) genesFor(kind, entryName string) *geneSet {
	ls.geneOnce.Do(ls.buildGeneIndex)
	return ls.geneIdx[kind][entryName]
}

func (ls *LoadedSave) buildGeneIndex() {
	ls.geneIdx = map[string]map[string]*geneSet{
		"bibite": indexGenes(ls.tables.BibiteGenes),
		"egg":    indexGenes(ls.tables.EggGenes),
	}
}

func indexGenes(rows []tb.GeneRow) map[string]*geneSet {
	out := make(map[string]*geneSet)
	for _, g := range rows {
		set := out[g.EntryName]
		if set == nil {
			set = &geneSet{byName: make(map[string]tb.GeneRow)}
			out[g.EntryName] = set
		}
		set.order = append(set.order, g)
		set.byName[g.GeneName] = g
	}
	return out
}

// openDB lazily opens DuckDB and imports this save's normalized rows on the first
// analytical query, then reuses the handle. DuckDB therefore opens at most once
// per run. The import is the in-memory ExtractedSave already parsed at Load time —
// no reparse. A run is single-threaded (one Starlark thread), so no locking.
func (ls *LoadedSave) openDB(ctx context.Context) (*sql.DB, error) {
	if ls.db != nil {
		return ls.db, nil
	}
	db, err := duckdb.OpenAndImport(ctx, "", ls.tables)
	if err != nil {
		return nil, fmt.Errorf("open analytics db: %w", err)
	}
	ls.db = db
	ls.dbOpenCount++
	// Per the consistency contract, a freshly opened DB flushes any pending mirror
	// buffer once so mutation/open ordering is irrelevant. No-op in T5.
	if err := ls.flushMirror(ctx); err != nil {
		return nil, err
	}
	return ls.db, nil
}

// flushMirror makes DuckDB observe scalar mutations staged since the last flush.
// In T5 there are no mutations, so it is a no-op whenever nothing is dirty; T6
// fills the deferred buffer and replaces this body with the batched UPDATE flush.
// It is the call site already wired into every query/aggregate entry point.
func (ls *LoadedSave) flushMirror(ctx context.Context) error {
	if !ls.mirrorDirty {
		return nil
	}
	// T6 flushes the buffer here as one UPDATE ... FROM (VALUES …) per column.
	ls.mirrorDirty = false
	return nil
}

// queryCtx is the context used for DuckDB calls from script builtins. v1 uses a
// background context — the Starlark step budget already bounds the run via
// Thread.Cancel, and analytics queries are short. Threading the run's context
// through here (so cancellation reaches in-flight SQL) is the documented seam.
func (ls *LoadedSave) queryCtx() context.Context { return context.Background() }
