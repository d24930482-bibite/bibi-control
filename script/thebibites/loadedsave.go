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

	// settingsIdx indexes the per-scope settings value tables by (owner_id,
	// setting_name) into their backing slices, so a named settings read/write is
	// O(1) and writes through to ls.tables. owner_id is the scope discriminator:
	// "settings" for simulation, "independents" for independent, the material name
	// for material. Built lazily, modeled on the gene index.
	settingsOnce sync.Once
	settingsIdx  map[string]map[string]map[string]int // table -> owner_id -> setting_name -> backing index

	// subRowIdx indexes 1:many sub-collection tables (brain synapses/nodes,
	// stomach contents) by entry_name, holding each entity's element rows in array
	// order (T11b). Built lazily; reads served in-memory like genes.
	subRowOnce sync.Once
	subRowIdx  map[string]map[string][]reflect.Value // table -> entry_name -> rows

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

	// zoneIDNext is the next synthetic zone id handed out to save.zones.clone(...)
	// appends, lazily seeded from max(existing zone id)+1 so cloned zones do not
	// collide on id with the template or each other within a run. Zone-group
	// membership and other id references are not reconciled (see zones.go).
	// zoneIDNextReady records that the one-time seeding scan of SettingsZones ran;
	// zoneIDNone records that the scan found no id-bearing zone, so allocZoneID
	// reports (0,false) on every later call WITHOUT re-scanning.
	zoneIDNext      int64
	zoneIDNextReady bool
	zoneIDNone      bool

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

// geneSet indexes one entity's genes into the kind's underlying gene slice
// (backing): order holds the slice indices in save order (for iteration), byName
// maps gene name -> slice index (for gene() lookup). Both point into backing so a
// gene write (setGeneValue) through backing[idx] is observed by every read path
// without a second copy.
type geneSet struct {
	backing []tb.GeneRow   // ls.tables.BibiteGenes or EggGenes
	order   []int          // indices into backing for this entity, in save order
	byName  map[string]int // gene name -> index into backing
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

	saveFields := saveFieldByTable()

	seen := make(map[string]bool)
	for _, tables := range entityTables {
		for _, table := range tables {
			if seen[table] {
				continue
			}
			seen[table] = true
			saveField, ok := saveFields[table]
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

// subRowsFor returns one entity's element rows for a 1:many sub-collection table,
// in array order, building the per-table index lazily. Returns nil when the entity
// has no elements in that table.
func (ls *LoadedSave) subRowsFor(table, entryName string) []reflect.Value {
	ls.subRowOnce.Do(ls.buildSubRowIndex)
	return ls.subRowIdx[table][entryName]
}

// buildSubRowIndex groups every sub-collection table's rows by entry_name,
// preserving slice order (which the parser emits in array order, so it equals the
// element index order). One pass per distinct table across all entity kinds.
func (ls *LoadedSave) buildSubRowIndex() {
	ls.subRowIdx = make(map[string]map[string][]reflect.Value)
	extracted := reflect.ValueOf(ls.tables)
	extractedType := extracted.Type()

	seen := make(map[string]bool)
	for _, subs := range subCollectionRegistry() {
		for _, sc := range subs {
			if seen[sc.table] {
				continue
			}
			seen[sc.table] = true
			fld, ok := extractedType.FieldByName(sc.saveField)
			if !ok {
				continue
			}
			slice := extracted.FieldByIndex(fld.Index)
			if slice.Kind() != reflect.Slice {
				continue
			}
			elem := slice.Type().Elem()
			sf, ok := elem.FieldByName("EntryName")
			if !ok {
				continue
			}
			byEntry := make(map[string][]reflect.Value)
			for i := 0; i < slice.Len(); i++ {
				row := slice.Index(i)
				name := row.FieldByIndex(sf.Index).String()
				byEntry[name] = append(byEntry[name], row)
			}
			ls.subRowIdx[sc.table] = byEntry
		}
	}
}

func indexGenes(rows []tb.GeneRow) map[string]*geneSet {
	out := make(map[string]*geneSet)
	for i := range rows {
		g := rows[i]
		set := out[g.EntryName]
		if set == nil {
			set = &geneSet{backing: rows, byName: make(map[string]int)}
			out[g.EntryName] = set
		}
		set.order = append(set.order, i)
		set.byName[g.GeneName] = i
	}
	return out
}

// settingsBacking returns the live row slice backing one settings-value table, so
// settingRow can hand out a pointer that writes through to ls.tables.
func (ls *LoadedSave) settingsBacking(table string) []tb.SettingValueRow {
	switch table {
	case "settings_simulation_values":
		return ls.tables.SettingsSimulationValues
	case "settings_independent_values":
		return ls.tables.SettingsIndependentValues
	case "settings_material_values":
		return ls.tables.SettingsMaterialValues
	case "settings_zone_values":
		return ls.tables.SettingsZoneValues
	default:
		return nil
	}
}

// settingRow returns a pointer to the SettingValueRow for one (table, owner_id,
// setting_name), building the per-table index lazily. The pointer is into the
// backing slice, so a caller's write-through is observed by later reads and the
// DuckDB import. Returns nil when the setting is absent.
func (ls *LoadedSave) settingRow(table, ownerID, name string) (*tb.SettingValueRow, bool) {
	ls.settingsOnce.Do(ls.buildSettingsIndex)
	idx, ok := ls.settingsIdx[table][ownerID][name]
	if !ok {
		return nil, false
	}
	backing := ls.settingsBacking(table)
	if idx < 0 || idx >= len(backing) {
		return nil, false
	}
	return &backing[idx], true
}

func (ls *LoadedSave) buildSettingsIndex() {
	ls.settingsIdx = map[string]map[string]map[string]int{
		"settings_simulation_values":  indexSettings(ls.tables.SettingsSimulationValues),
		"settings_independent_values": indexSettings(ls.tables.SettingsIndependentValues),
		"settings_material_values":    indexSettings(ls.tables.SettingsMaterialValues),
		"settings_zone_values":        indexSettings(ls.tables.SettingsZoneValues),
	}
}

// indexSettings groups a settings-value slice by (owner_id, setting_name) into
// backing indices. owner_id is constant within the flat scopes (simulation,
// independent) and the material name for material values, so this one shape serves
// every scope.
func indexSettings(rows []tb.SettingValueRow) map[string]map[string]int {
	out := make(map[string]map[string]int)
	for i := range rows {
		r := rows[i]
		byName := out[r.OwnerID]
		if byName == nil {
			byName = make(map[string]int)
			out[r.OwnerID] = byName
		}
		byName[r.SettingName] = i
	}
	return out
}

// allocZoneID returns a fresh, unused zone id for a cloned-zone append, lazily
// seeding from max(existing zone id)+1 and incrementing per call. Reports
// (0,false) when zones in this save carry no ids (nothing to keep unique).
func (ls *LoadedSave) allocZoneID() (int64, bool) {
	if !ls.zoneIDNextReady {
		max := int64(0)
		any := false
		for i := range ls.tables.SettingsZones {
			if z := &ls.tables.SettingsZones[i]; z.HasZoneID {
				any = true
				if z.ZoneID > max {
					max = z.ZoneID
				}
			}
		}
		ls.zoneIDNext = max + 1
		ls.zoneIDNone = !any
		ls.zoneIDNextReady = true // mark scanned BEFORE the id-less early return so it runs once
	}
	if ls.zoneIDNone {
		return 0, false
	}
	id := ls.zoneIDNext
	ls.zoneIDNext++
	return id, true
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

// subElementRef builds the locator for one array element: the parent entity
// locator (entry_name + body_id/egg_id) plus the table and the array-ordinal index
// stamped onto the matching SQLValueRef index field. The caller adds Column +
// WithExpected for a delete stale guard. Append uses only the parent half (Table +
// entity locator), since the appended element has no index yet.
func (ls *LoadedSave) subElementRef(kind, entryName string, sc *subCollectionSpec, index int64) (mutator.SQLValueRef, error) {
	ref, err := ls.entityLocatorRef(kind, entryName)
	if err != nil {
		return mutator.SQLValueRef{}, err
	}
	ref.Table = sc.table
	if err := setRefArrayIndex(&ref, sc.indexColumn, index); err != nil {
		return mutator.SQLValueRef{}, err
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
	ref, err := ls.prepareCommit(ctx, blobs, verify)
	if err != nil {
		return revisionstore.Revision{}, err
	}
	return ls.recordRevision(ctx, revs, ref, scriptRunID)
}

// prepareCommit performs the fallible, side-effecting part of a commit that does
// NOT need a script-run id: apply (idempotent) → serialize to bytes via
// WriteArchiveTo → blobs.Put (+ optional verify). It is split out of Commit so
// the host can produce the content-addressed blob BEFORE recording the script run,
// letting the recorded run status reflect the actual commit outcome (provenance is
// never recorded "succeeded" for a commit that failed). A content-addressed blob
// written without a following revision is a harmless orphan.
func (ls *LoadedSave) prepareCommit(ctx context.Context, blobs blobstore.Store, verify bool) (blobstore.Ref, error) {
	if blobs == nil {
		return blobstore.Ref{}, fmt.Errorf("commit: blob store is nil")
	}
	if err := ls.ensureApplied(); err != nil {
		return blobstore.Ref{}, err
	}

	var buf bytes.Buffer
	if err := tb.WriteArchiveTo(&buf, ls.session.Archive()); err != nil {
		return blobstore.Ref{}, fmt.Errorf("serialize save: %w", err)
	}
	ls.writeArchiveCount++
	data := buf.Bytes()

	ref, err := blobs.Put(ctx, data)
	if err != nil {
		return blobstore.Ref{}, fmt.Errorf("store save blob: %w", err)
	}

	if verify {
		if err := ls.verifyRoundTrip(data, ref); err != nil {
			return blobstore.Ref{}, err
		}
	}
	return ref, nil
}

// recordRevision links an already-produced blob to scriptRunID as a provenance
// revision. Split from prepareCommit so it runs strictly AFTER the script run is
// recorded (the FK save_revisions.script_run_id -> script_runs.id requires the run
// row first).
func (ls *LoadedSave) recordRevision(ctx context.Context, revs *revisionstore.Store, ref blobstore.Ref, scriptRunID int64) (revisionstore.Revision, error) {
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
