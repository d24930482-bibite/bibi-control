// Package thebibites binds a parsed Bibites save to a sandboxed Starlark
// scripting surface. T4 provides the read-only slice: load a save once and
// expose entity enumeration and friendly attribute/gene reads. Mutation (T6),
// DuckDB analytics (T5), settings (T7), and persistence (T8) attach to the seams
// left here (the lazy *sql.DB and the staged *mutator.Session) without
// restructuring this type.
package thebibites

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"sync"

	"github.com/asemones/bibicontrol/blobstore"
	"github.com/asemones/bibicontrol/duckdb"
	"github.com/asemones/bibicontrol/revisionstore"
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

	// mirror buffers scalar mutations not yet mirrored into DuckDB; mirrorDirty
	// marks it non-empty. T5 never sets these (no mutations); T6 fills the buffer
	// on every staged set and flushMirror drains it into the open DuckDB as one
	// batched UPDATE per (table, column). The query/aggregate entry points already
	// call flushMirror, so T6 only fills the buffer and the flush body.
	mirror      mirrorBuffer
	mirrorDirty bool

	// stagedOps counts mutations staged on the session this run (reported by
	// commit / the Result). dryRun stages but skips the write; set by the host
	// RunAndCommit from RunOptions.DryRun. willCommit is the script-declared commit
	// intent (default yes); the autocommit() builtin flips it to opt a pure-analysis
	// run out of producing a revision.
	stagedOps  int
	dryRun     bool
	willCommit bool

	// dbOpenCount / rowsMaterialized / flushStmtCount are test instrumentation:
	// the analytics path opens DuckDB at most once per run and never materializes
	// Entity values to aggregate; the mirror flushes N row-by-row sets as one
	// UPDATE per column, not N point-updates. writeArchiveCount / reparseCount let
	// the churn DoD assert a pure-mutation commit does exactly one WriteArchive and
	// zero reparses (one reparse only under verify). Tests assert these counters.
	dbOpenCount       int
	rowsMaterialized  int
	flushStmtCount    int
	writeArchiveCount int
	reparseCount      int
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
		path:       path,
		saveID:     saveID,
		archive:    archive,
		tables:     tb.ExtractTables(saveID, archive),
		session:    mutator.NewSession(archive),
		willCommit: true,
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
// It runs at the head of every query/aggregate (and once right after a lazy
// openDB) so a later read-after-write observes staged sets without a reparse or a
// re-import. It is a no-op when nothing is dirty. When the DB is not yet open the
// buffer is left intact — the lazy import picks up the write-throughs already
// applied to ls.tables, and openDB flushes (idempotently) once the handle exists.
// Each (table, column) drains as exactly one UPDATE (counted for tests).
func (ls *LoadedSave) flushMirror(ctx context.Context) error {
	if !ls.mirrorDirty || ls.mirror.empty() {
		ls.mirrorDirty = false
		return nil
	}
	if ls.db == nil {
		return nil
	}
	for key, col := range ls.mirror.cols {
		if err := ls.flushMirrorColumn(ctx, key, col); err != nil {
			return err
		}
		ls.flushStmtCount++
	}
	ls.mirror.reset()
	ls.mirrorDirty = false
	return nil
}

// entityLocatorRef builds the locator half of a SQLValueRef for one entity —
// entry_name plus the kind's id guard (body_id for bibites, egg_id for eggs) —
// read generically from the identity row through the same generated-metadata
// registry that powers reads. The caller fills Table/Column. No hand-maintained
// locator list: the id columns are ordinary (non-writable) registry specs.
func (ls *LoadedSave) entityLocatorRef(kind, entryName string) (mutator.SQLValueRef, error) {
	identity, err := identityTable(kind)
	if err != nil {
		return mutator.SQLValueRef{}, err
	}
	row, ok := ls.rowForEntry(identity, entryName)
	if !ok {
		return mutator.SQLValueRef{}, fmt.Errorf("no %s row for %q", identity, entryName)
	}
	reg := attrRegistry()[kind]
	ref := mutator.SQLValueRef{EntryName: entryName}
	switch kind {
	case "bibite":
		id, err := rowInt64Field(row, reg, "body_id")
		if err != nil {
			return mutator.SQLValueRef{}, err
		}
		has, err := rowBoolField(row, reg, "has_body_id")
		if err != nil {
			return mutator.SQLValueRef{}, err
		}
		ref.BodyID, ref.HasBodyID = id, has
	case "egg":
		id, err := rowInt64Field(row, reg, "egg_id")
		if err != nil {
			return mutator.SQLValueRef{}, err
		}
		has, err := rowBoolField(row, reg, "has_egg_id")
		if err != nil {
			return mutator.SQLValueRef{}, err
		}
		ref.EggID, ref.HasEggID = id, has
	default:
		return mutator.SQLValueRef{}, fmt.Errorf("unknown entity kind %q", kind)
	}
	return ref, nil
}

func rowInt64Field(row reflect.Value, reg map[string]attrSpec, column string) (int64, error) {
	spec, ok := reg[column]
	if !ok {
		return 0, fmt.Errorf("missing locator column %q", column)
	}
	return row.FieldByIndex(spec.fieldIndex).Int(), nil
}

func rowBoolField(row reflect.Value, reg map[string]attrSpec, column string) (bool, error) {
	spec, ok := reg[column]
	if !ok {
		return false, fmt.Errorf("missing locator column %q", column)
	}
	return row.FieldByIndex(spec.fieldIndex).Bool(), nil
}

// ensureApplied applies the staged mutations to the in-memory archive at most
// once. Session.Apply is itself idempotent (no-op once StateApplied or with no
// staged ops), so a script that calls save.commit(path) and is then committed by
// the host does not double-apply.
func (ls *LoadedSave) ensureApplied() error {
	if err := ls.session.Apply(); err != nil {
		return fmt.Errorf("apply staged mutations: %w", err)
	}
	return nil
}

// WriteSave applies the staged mutations to the in-memory archive and writes the
// resulting save zip — with NO reparse (the run is over; fresh projections would
// be unused). This is the T6 commit-to-file core (the save.commit(path) plain-file
// export); content-addressed persistence + provenance is layered by Commit.
func (ls *LoadedSave) WriteSave(path string) error {
	if err := ls.ensureApplied(); err != nil {
		return err
	}
	if err := tb.WriteArchive(path, ls.session.Archive()); err != nil {
		return fmt.Errorf("write save %q: %w", path, err)
	}
	return nil
}

// Commit serializes the applied save to content-addressed storage and records a
// provenance revision linked to scriptRunID. It is the T8 terminus on top of the
// T6 write core: apply (idempotent) → serialize to bytes via WriteArchiveTo (NO
// temp file, NO reparse) → blobs.Put → revs.RecordRevision. When verify is set the
// produced bytes are reparsed exactly once and their hash asserted against the
// blob ref (the only reparse this path ever performs). DuckDB is never touched.
func (ls *LoadedSave) Commit(ctx context.Context, blobs blobstore.Store, revs *revisionstore.Store, scriptRunID int64, verify bool) (revisionstore.Revision, error) {
	if blobs == nil {
		return revisionstore.Revision{}, fmt.Errorf("commit: blob store is nil")
	}
	if err := ls.ensureApplied(); err != nil {
		return revisionstore.Revision{}, err
	}

	var buf bytes.Buffer
	if err := tb.WriteArchiveTo(&buf, ls.session.Archive()); err != nil {
		return revisionstore.Revision{}, fmt.Errorf("serialize save: %w", err)
	}
	ls.writeArchiveCount++
	data := buf.Bytes()

	ref, err := blobs.Put(ctx, data)
	if err != nil {
		return revisionstore.Revision{}, fmt.Errorf("store save blob: %w", err)
	}

	if verify {
		if err := ls.verifyRoundTrip(data, ref); err != nil {
			return revisionstore.Revision{}, err
		}
	}

	rev, err := revs.RecordRevision(ctx, revisionstore.RevisionInput{
		SourcePath:  ls.path,
		BlobRef:     ref,
		ScriptRunID: scriptRunID,
		// ParentID nil in v1: the input save is not itself a recorded revision, so
		// there is no parent to link. Lineage chaining is a documented seam.
	})
	if err != nil {
		return revisionstore.Revision{}, fmt.Errorf("record revision: %w", err)
	}
	return rev, nil
}

// verifyRoundTrip reparses the produced save bytes once and asserts the parsed
// archive's whole-file SHA256 matches the blob ref. ParseFile is path-based (there
// is no in-memory archive parser), so verify writes a temp file; the common commit
// path stays temp-file-free.
func (ls *LoadedSave) verifyRoundTrip(data []byte, ref blobstore.Ref) error {
	tmp, err := os.CreateTemp("", "bibiscript-verify-*.zip")
	if err != nil {
		return fmt.Errorf("verify: temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("verify: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("verify: close temp: %w", err)
	}

	re, err := tb.ParseFile(tmpPath, nil)
	ls.reparseCount++
	if err != nil {
		return fmt.Errorf("verify: reparse produced save: %w", err)
	}
	if re.SHA256 != ref.SHA256 {
		return fmt.Errorf("verify: reparsed save sha256 %s does not match blob ref %s", re.SHA256, ref.SHA256)
	}
	return nil
}

// queryCtx is the context used for DuckDB calls from script builtins. v1 uses a
// background context — the Starlark step budget already bounds the run via
// Thread.Cancel, and analytics queries are short. Threading the run's context
// through here (so cancellation reaches in-flight SQL) is the documented seam.
func (ls *LoadedSave) queryCtx() context.Context { return context.Background() }
