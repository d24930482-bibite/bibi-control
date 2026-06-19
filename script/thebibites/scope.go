package thebibites

import (
	"database/sql"
	"fmt"
)

// scope.go is the E3 save-partition scoping seam. Every analytics push-down
// builder restricts its reads to a set of save_ids; today that set is exactly one
// working partition (save_id = ls.saveID). SaveScope abstracts "which save_ids are
// in scope" so the SAME builder serves both the single-save working path and the
// E3 spanning paths (one world's whole retained history; the whole workspace)
// WITHOUT forking the query builder.
//
// Scoping is injected BY CONSTRUCTION: the scope supplies the WHERE fragment and
// the optional catalog JOIN; the script (or any caller) never writes a save_id
// filter or a mirror_saves JOIN. This is the E2-leak prevention invariant — a
// spanning read can only ever see the worlds its scope's subquery names.

// catalogTable is the workspace catalog table that maps a history save_id to its
// world_id / sim_time (workspace/query.go mirrorCatalogDDL). The spanning scopes
// filter against it (save_id IN (SELECT save_id FROM mirror_saves ...)) and LEFT
// JOIN it so world_id / sim_time resolve as friendly columns. It carries only
// sha256 history keys, never the world-id working key, so a spanning read sees
// committed history — never another world's uncommitted working partition.
const catalogTable = "mirror_saves"

// catalogColumns are the friendly columns a spanning scope contributes, sourced
// from the catalog DDL (workspace/query.go:48-55), NOT a hand-kept entity
// allowlist. world_id / sim_time map to mirror_saves.<col>; entity columns keep
// deriving from attrRegistry(). Keeping this to exactly the catalog's own columns
// satisfies the generated-metadata-fidelity rule (sqlref-generation-philosophy).
var catalogColumns = map[string]string{
	"world_id": "world_id",
	"sim_time": "sim_time",
}

// SaveScope supplies the save-partition filter the analytics push-down injects so
// a collection's reads stay scoped BY CONSTRUCTION (the caller never writes
// save_id / JOIN). It also declares writability (spanning scopes are read-only)
// and any extra friendly columns the scope contributes (world_id / sim_time from
// mirror_saves).
type SaveScope interface {
	// scopeClause returns the SQL fragment AND-ed into the WHERE that restricts
	// every push-down query to this scope's save_id set, plus its bound args.
	// identity is the identity table the clause qualifies its save_id against.
	scopeClause(identity string) (clause string, args []any)
	// writable reports whether mutation is permitted (only the single-save working
	// scope is writable). Spanning scopes return false.
	writable() bool
	// catalogJoin returns an optional extra FROM/JOIN fragment that brings
	// mirror_saves into scope so world_id/sim_time resolve as columns. "" for the
	// single-save scope (no catalog needed). For spanning scopes it LEFT JOINs
	// mirror_saves ON <identity>.save_id = mirror_saves.save_id.
	catalogJoin(identity string) string
	// catalogCols returns the friendly catalog columns this scope exposes (so
	// rewritePredicate / resolveColumn can resolve world_id / sim_time). nil for
	// the single-save scope (no catalog columns).
	catalogCols() map[string]string
}

// workingScope is the default single-working-partition scope. Its clause is
// byte-identical to the pre-E3 hardcoded "<identity>.save_id = ?" / [saveID], so
// every existing single-save test is behavior-preserving. It is the only writable
// scope and contributes no catalog columns/JOIN.
type workingScope struct {
	saveID string
}

func (s workingScope) scopeClause(identity string) (string, []any) {
	return quoteIdent(identity) + ".save_id = ?", []any{s.saveID}
}

func (s workingScope) writable() bool                     { return true }
func (s workingScope) catalogJoin(identity string) string { return "" }
func (s workingScope) catalogCols() map[string]string     { return nil }

// spanningScope restricts reads to every history partition the catalog names,
// optionally filtered to one world. It is read-only and LEFT JOINs the catalog so
// world_id / sim_time resolve as friendly columns. The save_id set is an
// IN-subquery against mirror_saves, so the scope can only ever read worlds the
// catalog lists (history, not uncommitted working partitions) — the E2-leak guard
// by construction.
type spanningScope struct {
	// worldID, when non-empty, narrows the catalog subquery to one world's
	// history; empty means the whole workspace (all worlds).
	worldID string
}

func (s spanningScope) scopeClause(identity string) (string, []any) {
	sub := "SELECT save_id FROM " + quoteIdent(catalogTable)
	var args []any
	if s.worldID != "" {
		sub += " WHERE world_id = ?"
		args = []any{s.worldID}
	}
	return quoteIdent(identity) + ".save_id IN (" + sub + ")", args
}

func (s spanningScope) writable() bool { return false }

func (s spanningScope) catalogJoin(identity string) string {
	return " LEFT JOIN " + quoteIdent(catalogTable) + " ON " +
		quoteIdent(identity) + ".save_id = " + quoteIdent(catalogTable) + ".save_id"
}

func (s spanningScope) catalogCols() map[string]string { return catalogColumns }

// NewWorldHistoryScope returns a read-only spanning scope over one world's whole
// retained history (every revision's history partition). worldID must be the
// world's stable id (the catalog's world_id). The workspace constructs this for
// world.bibites / world.eggs / world.pellets.
func NewWorldHistoryScope(worldID string) SaveScope {
	return spanningScope{worldID: worldID}
}

// NewWorkspaceScope returns a read-only spanning scope over every world's history
// in the workspace. The workspace constructs this for workspace.bibites /
// workspace.eggs / workspace.pellets.
func NewWorkspaceScope() SaveScope {
	return spanningScope{}
}

// scopeFor returns the effective scope for a collection: the explicit scope when
// set, else the default working scope built from ls.saveID. Centralizing the
// nil->working fallback keeps every builder call site terse and makes the
// single-save path byte-identical (workingScope's clause == the old hardcoded
// clause).
func scopeFor(ls *LoadedSave, scope SaveScope) SaveScope {
	if scope != nil {
		return scope
	}
	return workingScope{saveID: ls.saveID}
}

// SpanningReader is a save-less analytics reader bound to a shared workspace
// DuckDB handle and a spanning SaveScope. It produces aggregate-only
// EntityCollections (bibites/eggs/pellets) that push down through the SAME builder
// the single-save path uses, scoped BY CONSTRUCTION via the spanning scope. It
// parses NO save: a spanning read needs only the catalog + the history partitions
// already in DuckDB, so it carries a *LoadedSave with just db+scope and no parsed
// archive (the read path never dereferences the archive — see flushMirror/openDB).
type SpanningReader struct {
	ls    *LoadedSave
	scope SaveScope
}

// NewSpanningReader builds a SpanningReader over the shared workspace handle db
// under the given spanning scope. db must be the workspace's DuckDB handle (the
// one holding mirror_saves + every world's history partitions); scope must be a
// read-only spanning scope (NewWorldHistoryScope / NewWorkspaceScope). The
// returned reader's collections never mutate and never materialize Entity rows.
func NewSpanningReader(db *sql.DB, scope SaveScope) (*SpanningReader, error) {
	if db == nil {
		return nil, fmt.Errorf("spanning reader: db must not be nil")
	}
	if scope == nil {
		return nil, fmt.Errorf("spanning reader: scope must not be nil")
	}
	if scope.writable() {
		return nil, fmt.Errorf("spanning reader: scope must be read-only")
	}
	// A spanning ls carries only the shared db + an empty session: it never
	// parses/mutates a save, so archive is nil and saveID is unused (the spanning
	// scope supplies the save_id filter). ls.query needs only db + flushMirror,
	// and flushMirror is a no-op while mirrorDirty stays false (it never goes
	// dirty without a staged set, which a read-only collection cannot do).
	return &SpanningReader{
		ls:    &LoadedSave{db: db, willCommit: false},
		scope: scope,
	}, nil
}

// Bibites returns the spanning, aggregate-only bibite collection.
func (r *SpanningReader) Bibites() *EntityCollection {
	return &EntityCollection{ls: r.ls, kind: "bibite", scope: r.scope}
}

// Eggs returns the spanning, aggregate-only egg collection.
func (r *SpanningReader) Eggs() *EntityCollection {
	return &EntityCollection{ls: r.ls, kind: "egg", scope: r.scope}
}

// Pellets returns the spanning, aggregate-only pellet collection (the
// analytics-only "pellet" kind — distinct from save.pellets' in-memory surface).
func (r *SpanningReader) Pellets() *EntityCollection {
	return &EntityCollection{ls: r.ls, kind: "pellet", scope: r.scope}
}

// Genes returns the spanning, aggregate-only gene collection (M1): genes of BOTH
// bibites and eggs, unioned. Read-only and aggregate-only like the other spanning
// kinds — value/name/type are friendly columns; value defaults to number_value.
func (r *SpanningReader) Genes() *EntityCollection {
	return &EntityCollection{ls: r.ls, kind: "gene", scope: r.scope}
}

// Nodes returns the spanning, aggregate-only brain-node collection (M1): nodes of
// BOTH bibites and eggs, unioned (the FLAT node aggregate — node_in/node_out are bare
// ids; graph navigation is deferred to M5).
func (r *SpanningReader) Nodes() *EntityCollection {
	return &EntityCollection{ls: r.ls, kind: "node", scope: r.scope}
}

// Synapses returns the spanning, aggregate-only brain-synapse collection (M1):
// synapses of BOTH bibites and eggs, unioned (FLAT — weight/enabled/node_in/node_out;
// no source/target graph join).
func (r *SpanningReader) Synapses() *EntityCollection {
	return &EntityCollection{ls: r.ls, kind: "synapse", scope: r.scope}
}

// Collection returns the spanning collection for one of "bibites"/"eggs"/"pellets"/
// "genes"/"nodes"/"synapses", or an error for any other name. It is the single
// dispatch the workspace binding uses so the accessors stay in one place.
func (r *SpanningReader) Collection(name string) (*EntityCollection, error) {
	switch name {
	case "bibites":
		return r.Bibites(), nil
	case "eggs":
		return r.Eggs(), nil
	case "pellets":
		return r.Pellets(), nil
	case "genes":
		return r.Genes(), nil
	case "nodes":
		return r.Nodes(), nil
	case "synapses":
		return r.Synapses(), nil
	default:
		return nil, fmt.Errorf("spanning reader: unknown collection %q", name)
	}
}
