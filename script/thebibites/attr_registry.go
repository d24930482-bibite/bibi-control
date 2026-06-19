package thebibites

import (
	"reflect"
	"sort"
	"sync"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

// attrCategory distinguishes how a friendly attribute resolves to a value. T4
// only ships flat scalar columns; richer surfaces (joined child collections such
// as brain nodes/synapses, stomach contents, children) get their own category
// here so the Attr dispatch in entity.go becomes a switch, not a rewrite.
type attrCategory int

const (
	// categoryScalar reads a single column off the entity's row (or a 1:1
	// sub-table row joined by entry_name).
	categoryScalar attrCategory = iota
	// categorySubCollection is a 1:many sub-table (brain synapses/nodes, stomach
	// contents) exposed as an iterable element collection (T11b). Dispatched in
	// entity.go to an ElementCollection rather than a scalar read.
	categorySubCollection
)

// attrSpec describes one friendly attribute of an entity kind. Everything here
// is derived from tb.NormalizedTables — there is no hand-maintained allowlist of
// readable fields. writable/sqlType are recorded now and consumed by T6.
//
// column vs sourceColumn: column is the FRIENDLY name — the registry map key and
// the b.<name> surface (== sourceColumn unless an entry in overrides renames it).
// sourceColumn is the generated DuckDB/sqlref column the value actually lives in;
// it is what every SQL query, mutator SQLValueRef, DuckDB mirror, and guard rule
// must key on. Keeping them distinct is what makes a writable alias (e.g.
// position_x -> transform_position_x) resolve correctly instead of querying a
// non-existent "position_x" column.
type attrSpec struct {
	category     attrCategory
	table        string // normalized table the value lives in
	column       string // friendly (snake_case) name == registry key (alias or generated)
	sourceColumn string // generated DuckDB/sqlref column == NormalizedFieldSpec.Column
	fieldIndex   []int  // reflect index path into the row struct
	writable     bool   // field has an sqlref path (mutable) — used by T6, not T4
	sqlType      string
	jsonKey      string // raw JSON key (NormalizedFieldSpec.SQLRefPath); used by T11b sub-collection append
}

// entityTables lists, per entity kind, the normalized tables whose columns are
// exposed as friendly attributes. The FIRST table is the identity table (one row
// per entity); the rest are 1:1 sub-tables joined by entry_name. Only 1:1 tables
// belong here — 1:many tables (brain nodes/synapses, stomach contents, children)
// are a future sub-collection category, deliberately excluded from T4.
//
// This short list is the one piece of domain knowledge in the registry; the
// columns themselves are still derived generically from tb.NormalizedTables, so
// new columns on any of these tables become readable with no change here.
var entityTables = map[string][]string{
	"bibite": {
		"bibites",
		"bibite_body",
		"bibite_mouth",
		"bibite_pheromone_emitters",
		"bibite_egg_layers",
		"bibite_control",
	},
	"egg": {
		"eggs",
	},
	// pellet is an ANALYTICS-ONLY kind (E3): it registers the pellets identity
	// table for the scoped aggregate push-down (world.pellets / workspace.pellets)
	// so spanning reads cover pellets too. It is deliberately NOT a mutation kind —
	// locatorSelect / EntryNames reject it, and the spanning scope is read-only, so
	// no set/delete/transfer/iteration path can reach it. The in-memory save.pellets
	// surface (pellets.go) is untouched; this is purely additive for the analytics
	// path. Its columns are derived from pelletRegistry() (generated metadata), not
	// a parallel list.
	"pellet": {
		"pellets",
	},
}

// spanningEntityTables lists, per SPANNING-ONLY analytics kind, the source tables
// whose genes/brain rows the cross-world aggregate push-down unions over (M1 §1).
// These kinds are read-only, aggregate-only spanning collections (world.genes /
// workspace.synapses etc.); they exist ONLY for the spanning push-down and are
// DELIBERATELY KEPT OUT of entityTables so the single-save working path
// (buildAccess in loadedsave.go iterates entityTables alone) stays byte-identical —
// ls.access never gains a gene/brain table, and the single-save attr-registry kind
// set (bibite/egg/pellet) is unchanged.
//
// Each kind unions its two carriers (bibites' + eggs') so a spanning `genes`/`nodes`/
// `synapses` collection spans genes/brain rows regardless of carrier (matches §1's
// single-`genes` target). The union members are 1:many sub-tables (entitySubCollections
// for nodes/synapses; the gene tables resolve through gene.go's geneTable). For the
// spanning path they ARE the identity (the union is the FROM), never scalar-joined, so
// the oneToManyTables() inflate-guard never trips — fromClause routes spanning kinds
// down spanningFromExpr BEFORE the 1:1 sub-table LEFT-JOIN loop.
var spanningEntityTables = map[string][]string{
	"gene":    {"bibite_genes", "egg_genes"},
	"node":    {"bibite_brain_nodes", "egg_brain_nodes"},
	"synapse": {"bibite_brain_synapses", "egg_brain_synapses"},
}

// tablesForKind resolves a kind's source tables, preferring the working-path
// entityTables (bibite/egg/pellet) and falling back to the spanning-only
// spanningEntityTables (gene/node/synapse). Routing identityTable / fromClause /
// EntityCollection.identityAccess through this is what makes the push-down generic
// over the new spanning kinds WITHOUT teaching buildAccess (the working path) about
// them — the isolation guarantee.
func tablesForKind(kind string) []string {
	if tables, ok := entityTables[kind]; ok {
		return tables
	}
	return spanningEntityTables[kind]
}

// isSpanningKind reports whether kind is a spanning-only analytics kind whose
// identity is a UNION over its source tables (so fromClause must emit the derived
// union identity, not a bare FROM <table> + sub-table joins).
func isSpanningKind(kind string) bool {
	_, ok := spanningEntityTables[kind]
	return ok
}

// spanningAlias is the synthesized identity-table alias a spanning kind's UNION-ALL
// FROM is exposed under. Every friendly column for the kind is qualified against
// this alias (in the registry) and the scope clause + catalog join key on
// "<alias>".save_id, so the union projection MUST carry save_id (spanningFromExpr).
// It is the plural of the kind ("gene" -> "genes"), matching the user-facing name.
func spanningAlias(kind string) string {
	return kind + "s"
}

// spanningProjection returns the columns the UNION-ALL identity projects for a
// spanning kind: save_id (load-bearing — the scope clause + catalog join bind to
// "<alias>".save_id, so a dropped save_id would silently SUM across ALL worlds) plus
// every distinct generated source column the kind's friendly surface resolves to
// (spec.sourceColumn for every registry attr). Derived from the registry + generated
// metadata, so no hand-listed column set: a friendly `value` (-> number_value)
// guarantees number_value is projected. Ordered deterministically by the first
// carrier table's generated field order so the UNION-ALL members align column-by-column.
func spanningProjection(kind string) []string {
	// The exact source columns the friendly surface needs (from the registry specs).
	needed := map[string]bool{"save_id": true}
	for _, spec := range attrRegistry()[kind] {
		needed[spec.sourceColumn] = true
	}
	cols := []string{"save_id"}
	emitted := map[string]bool{"save_id": true}
	// Deterministic order: the first carrier table's generated field order.
	if src := spanningEntityTables[kind]; len(src) > 0 {
		for _, spec := range tb.NormalizedTables {
			if spec.Table != src[0] {
				continue
			}
			for _, field := range spec.Fields {
				if needed[field.Column] && !emitted[field.Column] {
					emitted[field.Column] = true
					cols = append(cols, field.Column)
				}
			}
			break
		}
	}
	return cols
}

// geneOverrides aliases the gene tables' generated columns to the friendly surface
// the per-entity Gene handle exposes (gene.go:31-44): name -> gene_name, type ->
// value_type, value -> number_value. The §1 open question is resolved as "value
// defaults to number_value", so .mean()/.sum() over `value` are numeric-only by
// default (bool/string genes have NULL number_value and are silently ignored by the
// aggregate); a where("type == 'number'") is the explicit numeric guard. The raw
// typed *_value source columns are dropped from the direct friendly surface
// (geneHiddenColumns) so the gene surface is name/type/value, not the internal
// triple.
var geneOverrides = map[string]string{
	"name":  "gene_name",
	"type":  "value_type",
	"value": "number_value",
}

// geneHiddenColumns are gene source columns hidden from the friendly spanning
// surface: the raw typed value triple (folded into the single virtual `value` ->
// number_value alias) and the locator columns that carry no analytic signal.
var geneHiddenColumns = map[string]bool{
	"gene_name":    true, // re-exposed as the friendly `name`
	"value_type":   true, // re-exposed as the friendly `type`
	"number_value": true, // re-exposed as the friendly `value`
	"bool_value":   true,
	"string_value": true,
	"owner_kind":   true,
	"owner_id":     true,
	"entry_name":   true,
	"path":         true,
}

// brainHiddenColumns are node/synapse source columns hidden from the friendly
// spanning surface: the locators + the array-ordinal row index (analytic noise).
// The data columns (weight/enabled/node_in/node_out/innovation/node_index/
// node_desc/type_name/value/…) fall through to the friendly surface straight from
// generated metadata — no hand-listed allowlist.
var brainHiddenColumns = map[string]bool{
	"owner_kind":        true,
	"owner_id":          true,
	"entry_name":        true,
	"node_row_index":    true,
	"synapse_row_index": true,
}

// spanningHiddenColumns returns the source columns hidden from a spanning kind's
// friendly surface (locators / re-aliased typed columns), keyed by kind.
func spanningHiddenColumns(kind string) map[string]bool {
	switch kind {
	case "gene":
		return geneHiddenColumns
	default: // node, synapse
		return brainHiddenColumns
	}
}

// transformAliases is the shared friendly alias -> source column mapping for the
// generated transform_* position/rotation columns. It lives once here and is merged
// into every entity's overrides (and the pellet overrides, which adds transform_scale),
// so the bibite/egg/pellet short position surface stays defined in a single place.
var transformAliases = map[string]string{
	"position_x": "transform_position_x",
	"position_y": "transform_position_y",
	"rotation":   "transform_rotation",
}

// overrides renames/aliases generated column names to friendlier ones, keyed by
// entity kind then friendly alias -> source column. It is the only hand-edited
// surface in the registry and is intentionally tiny. The friendly alias becomes
// the registry key + b.<name> surface; spec.sourceColumn stays the generated
// column so SQL/mutator/mirror/guard keying is unaffected. (Gene-backed aliases
// like "diet" are not added here because genes resolve through gene(), not columns.)
// Each kind's map is seeded from the shared transformAliases so the position/rotation
// triple is not hand-listed per kind.
var overrides = map[string]map[string]string{
	"bibite": mergeAliases(transformAliases),
	"egg":    mergeAliases(transformAliases),
	// pellet is the E3 analytics-only kind; it shares the pellet position/scale
	// aliases so its friendly column surface (the registry attrRegistry["pellet"])
	// matches pelletRegistry() — same generated columns, same aliases, no parallel
	// list.
	"pellet": pelletOverrides,
}

// mergeAliases returns a fresh alias map containing every entry from the given
// alias maps (later maps win on key collision). Used to compose the shared
// transformAliases into per-kind / pellet override maps without mutating the
// shared map.
func mergeAliases(srcs ...map[string]string) map[string]string {
	out := make(map[string]string)
	for _, src := range srcs {
		for alias, source := range src {
			out[alias] = source
		}
	}
	return out
}

var (
	registryOnce sync.Once
	registry     map[string]map[string]attrSpec // kind -> friendly name -> spec
)

// attrRegistry returns the lazily built friendly-attribute registry. Built once
// per process from tb.NormalizedTables + the entity-table layout above.
func attrRegistry() map[string]map[string]attrSpec {
	registryOnce.Do(buildRegistry)
	return registry
}

func buildRegistry() {
	registry = make(map[string]map[string]attrSpec, len(entityTables)+len(spanningEntityTables))
	for kind, tables := range entityTables {
		attrs := make(map[string]attrSpec)
		for _, table := range tables {
			for col, spec := range tableScalarSpecs(table) {
				if _, exists := attrs[col]; exists {
					continue // first table wins (identity table precedence)
				}
				attrs[col] = spec
			}
		}
		applyOverrides(attrs, overrides[kind])
		registry[kind] = attrs
	}
	// Also register the spanning-only kinds' friendly columns (gene/node/synapse) so
	// resolveColumn / rewritePredicate resolve value/name/type (gene) and
	// weight/enabled/node_in/… (synapse/node) for the cross-world aggregate push-down.
	// This walks spanningEntityTables, NOT entityTables, so the working path's
	// buildAccess (which iterates entityTables alone) never sees these tables — the
	// byte-identity guarantee. The two carrier tables per kind are column-identical
	// (bibite_genes ≡ egg_genes etc.), so first-table precedence is well-defined.
	for kind, tables := range spanningEntityTables {
		hidden := spanningHiddenColumns(kind)
		alias := spanningAlias(kind)
		// raw is the full per-column spec set (all source columns, both carriers);
		// it backs the alias overrides whose source column is itself hidden from the
		// direct surface (e.g. gene `value` -> the hidden number_value).
		raw := make(map[string]attrSpec)
		attrs := make(map[string]attrSpec)
		for _, table := range tables {
			for col, spec := range tableScalarSpecs(table) {
				// Every spanning-kind column is qualified against the synthesized
				// UNION-ALL identity alias (spanningFromExpr), NOT the physical source
				// table, so resolveColumn/rewritePredicate emit "<alias>"."<col>" which
				// binds to the union projection.
				spec.table = alias
				if _, exists := raw[col]; exists {
					continue // first table wins (carrier-identity precedence)
				}
				raw[col] = spec
				if hidden[col] {
					continue // locator / re-aliased typed column — kept off the direct surface
				}
				attrs[col] = spec
			}
		}
		// Gene aliases (name/type/value) resolve against raw (their source columns
		// are hidden) so the alias spec keeps the real generated sourceColumn.
		if kind == "gene" {
			for friendly, source := range geneOverrides {
				if spec, ok := raw[source]; ok {
					spec.column = friendly
					attrs[friendly] = spec
				}
			}
		}
		registry[kind] = attrs
	}
}

// nonScalarColumns are projected columns excluded from the friendly scalar
// attribute surface. save_id is pure locator noise. raw_json is the whole
// serialized blob for the row (no SQLRefPath, so never writable) — exposing it as
// a readable attribute (e.g. save.zones[i].raw_json) would leak the entire payload
// as a friendly attr, so it is excluded like save_id. Shared across the entity,
// zone, and pellet registries that go through tableScalarSpecs.
var nonScalarColumns = map[string]bool{
	"save_id":  true,
	"raw_json": true,
}

// tableScalarSpecs derives the friendly scalar attribute specs for one normalized
// table straight from tb.NormalizedTables (no hand-maintained column allowlist):
// every projected column except the nonScalarColumns (save_id, raw_json) becomes a
// readable attrSpec, writable iff the field carries an sqlref path. Shared by the
// entity registry (buildRegistry) and the zone/pellet registries. The map key and
// friendly column default to the generated DuckDB column; callers apply any alias
// overrides afterward.
func tableScalarSpecs(table string) map[string]attrSpec {
	var spec tb.NormalizedTableSpec
	found := false
	for _, s := range tb.NormalizedTables {
		if s.Table == table {
			spec, found = s, true
			break
		}
	}
	if !found {
		return nil
	}
	// Resolve the row struct type by SaveField off a zero ExtractedSave so field
	// indices can be precomputed without an instance.
	rowType := rowTypeFor(reflect.TypeOf(tb.ExtractedSave{}), spec.SaveField)
	if rowType == nil {
		return nil
	}
	attrs := make(map[string]attrSpec, len(spec.Fields))
	for _, field := range spec.Fields {
		if nonScalarColumns[field.Column] {
			continue // pure locator noise (save_id) / whole-row blob (raw_json)
		}
		sf, ok := rowType.FieldByName(field.Field)
		if !ok {
			continue
		}
		idx := make([]int, len(sf.Index))
		copy(idx, sf.Index)
		attrs[field.Column] = attrSpec{
			category:     categoryScalar,
			table:        table,
			column:       field.Column,
			sourceColumn: field.Column,
			fieldIndex:   idx,
			writable:     field.SQLRefPath != "",
			sqlType:      field.SQLType,
			jsonKey:      field.SQLRefPath,
		}
	}
	return attrs
}

// applyOverrides registers friendly aliases (alias -> source column): the alias
// becomes an additional map key whose spec keeps sourceColumn, so all SQL/mutator/
// mirror/guard keying still targets the real generated column.
func applyOverrides(attrs map[string]attrSpec, aliases map[string]string) {
	for alias, source := range aliases {
		if spec, ok := attrs[source]; ok {
			spec.column = alias
			attrs[alias] = spec
		}
	}
}

// pelletOverrides aliases the generated transform_* pellet columns to the short
// position_x/position_y/rotation/scale surface used for bibites/eggs, keeping the
// pellet scalar surface consistent across entities. It reuses the shared
// transformAliases (position/rotation triple) and adds the pellet-only scale alias,
// so the triple is not re-listed; the columns themselves stay generated.
var pelletOverrides = mergeAliases(transformAliases, map[string]string{
	"scale": "transform_scale",
})

var (
	zoneRegOnce   sync.Once
	zoneRegMap    map[string]attrSpec
	pelletRegOnce sync.Once
	pelletRegMap  map[string]attrSpec
)

// zoneRegistry is the friendly scalar attribute registry for settings_zones
// (name/material/distribution writable; zone_index/zone_id readable), derived from
// generated metadata. Backs the save.zones surface (zones.go).
func zoneRegistry() map[string]attrSpec {
	zoneRegOnce.Do(func() { zoneRegMap = tableScalarSpecs("settings_zones") })
	return zoneRegMap
}

// pelletRegistry is the friendly scalar attribute registry for pellets, derived
// from generated metadata plus the short position aliases. Backs save.pellets
// (pellets.go).
func pelletRegistry() map[string]attrSpec {
	pelletRegOnce.Do(func() {
		m := tableScalarSpecs("pellets")
		applyOverrides(m, pelletOverrides)
		pelletRegMap = m
	})
	return pelletRegMap
}

// saveFieldByTable derives the normalized table -> ExtractedSave field name lookup
// straight from tb.NormalizedTables (spec.SaveField). It is the single source for
// "which ExtractedSave slice backs this table", shared by buildAccess (loadedsave.go)
// so that mapping is not re-derived inline. No hand-maintained list.
func saveFieldByTable() map[string]string {
	out := make(map[string]string, len(tb.NormalizedTables))
	for _, spec := range tb.NormalizedTables {
		out[spec.Table] = spec.SaveField
	}
	return out
}

var (
	normalizedTableNamesOnce sync.Once
	normalizedTableNamesList []string
)

// allNormalizedTableNames returns every normalized DuckDB table name (in
// tb.NormalizedTables order), derived once from the generated metadata — no
// hand-maintained list. Every normalized table carries a save_id column
// (normalize_metadata.go stamps {Field:"SaveID", Column:"save_id"} on each), so
// this is the exact set of base tables LoadedSave.Query must shadow with a
// per-world save_id filter to enforce working-copy scoping by construction. The
// returned slice is shared read-only — callers must not mutate it.
func allNormalizedTableNames() []string {
	normalizedTableNamesOnce.Do(func() {
		normalizedTableNamesList = make([]string, len(tb.NormalizedTables))
		for i, spec := range tb.NormalizedTables {
			normalizedTableNamesList[i] = spec.Table
		}
	})
	return normalizedTableNamesList
}

// rowTypeFor returns the element struct type of the ExtractedSave field named
// saveField (handling both []Row and *Row shapes).
func rowTypeFor(extractedType reflect.Type, saveField string) reflect.Type {
	sf, ok := extractedType.FieldByName(saveField)
	if !ok {
		return nil
	}
	t := sf.Type
	for t.Kind() == reflect.Slice || t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	return t
}

// subCollectionInfo names a 1:many sub-table exposed as an element collection and
// its synthesized array-ordinal column. indexColumn both orders elements for the
// read surface and selects which SQLValueRef index field a delete stamps (mapped
// in subElementIndexSetters, subcollection.go) — it is the array row index (no
// SQLRefPath), distinct from any in-payload "*_index" data column (e.g. a node's
// node_index, which carries SQLRefPath "Index").
type subCollectionInfo struct {
	table       string
	indexColumn string
}

// entitySubCollections lists, per entity kind, the 1:many sub-tables exposed as
// iterable element collections (T11b). Keyed by friendly attribute -> info. Like
// entityTables this is the one piece of domain knowledge; element columns are
// derived generically from tb.NormalizedTables. Eggs carry no stomach.
var entitySubCollections = map[string]map[string]subCollectionInfo{
	"bibite": {
		"synapses": {table: "bibite_brain_synapses", indexColumn: "synapse_row_index"},
		"nodes":    {table: "bibite_brain_nodes", indexColumn: "node_row_index"},
		"stomach":  {table: "bibite_stomach_contents", indexColumn: "content_index"},
	},
	"egg": {
		"synapses": {table: "egg_brain_synapses", indexColumn: "synapse_row_index"},
		"nodes":    {table: "egg_brain_nodes", indexColumn: "node_row_index"},
	},
}

// subLocatorColumns are the normalized projection columns that locate a row's
// parent or position rather than carry element data. They are excluded from the
// element read/append surface (the index column is exposed separately as `index`).
var subLocatorColumns = map[string]bool{
	"save_id": true, "entry_name": true,
	"owner_kind": true, "owner_id": true,
	"body_id": true, "has_body_id": true,
	"egg_id": true, "has_egg_id": true,
}

// subCollectionSpec is the built form of a sub-collection: the table + the
// ExtractedSave field holding its rows, the array-ordinal index column (field +
// name), the element's data attributes (derived from tb.NormalizedTables), and the
// deterministic stale-guard column used on element delete.
type subCollectionSpec struct {
	attr         string
	table        string
	saveField    string
	indexColumn  string
	indexField   []int
	elementAttrs map[string]attrSpec // friendly column -> spec (reads + append kwargs)
	writableCols []string            // writable element columns, sorted (append/guard)
	guardColumn  string              // chosen high-cardinality writable column, the delete stale guard
}

var (
	subRegOnce     sync.Once
	subRegistryMap map[string]map[string]*subCollectionSpec // kind -> attr -> spec
)

// subCollectionRegistry returns the lazily built sub-collection registry, derived
// from entitySubCollections + tb.NormalizedTables (no hand-maintained column list).
func subCollectionRegistry() map[string]map[string]*subCollectionSpec {
	subRegOnce.Do(buildSubRegistry)
	return subRegistryMap
}

func buildSubRegistry() {
	subRegistryMap = make(map[string]map[string]*subCollectionSpec, len(entitySubCollections))

	specByTable := make(map[string]tb.NormalizedTableSpec, len(tb.NormalizedTables))
	for _, spec := range tb.NormalizedTables {
		specByTable[spec.Table] = spec
	}
	extractedType := reflect.TypeOf(tb.ExtractedSave{})

	for kind, subs := range entitySubCollections {
		out := make(map[string]*subCollectionSpec, len(subs))
		for attr, info := range subs {
			tableSpec, ok := specByTable[info.table]
			if !ok {
				continue
			}
			rowType := rowTypeFor(extractedType, tableSpec.SaveField)
			if rowType == nil {
				continue
			}
			sc := &subCollectionSpec{
				attr:         attr,
				table:        info.table,
				saveField:    tableSpec.SaveField,
				indexColumn:  info.indexColumn,
				elementAttrs: make(map[string]attrSpec),
			}
			for _, field := range tableSpec.Fields {
				sf, found := rowType.FieldByName(field.Field)
				if !found {
					continue
				}
				idx := append([]int(nil), sf.Index...)
				if field.Column == info.indexColumn {
					sc.indexField = idx
					continue
				}
				if subLocatorColumns[field.Column] {
					continue
				}
				sc.elementAttrs[field.Column] = attrSpec{
					category:     categoryScalar,
					table:        info.table,
					column:       field.Column,
					sourceColumn: field.Column,
					fieldIndex:   idx,
					writable:     field.SQLRefPath != "",
					sqlType:      field.SQLType,
					jsonKey:      field.SQLRefPath,
				}
				if field.SQLRefPath != "" {
					sc.writableCols = append(sc.writableCols, field.Column)
				}
			}
			sort.Strings(sc.writableCols)
			sc.guardColumn = chooseGuardColumn(sc)
			out[attr] = sc
		}
		subRegistryMap[kind] = out
	}
}

// chooseGuardColumn picks the delete stale-guard column for a sub-collection. The
// guard's job is to fail loudly when a shifted/stale array index would delete the
// wrong element, so a boolean column is a poor guard: with only two values it
// rarely distinguishes a shifted element from its neighbor (synapses, sorted, lead
// with the bool `enabled`; nearly every synapse is enabled=true). Prefer the first
// writable column whose generated SQLType is NOT boolean — a higher-cardinality
// column (weight/innovation/node_in) catches a positional shift far more often.
// Fall back to writableCols[0] only if every writable column is boolean (so a guard
// still exists), and "" if there are no writable columns. Data-driven off the
// spec's sqlType — no hardcoded column names.
func chooseGuardColumn(sc *subCollectionSpec) string {
	if len(sc.writableCols) == 0 {
		return ""
	}
	for _, col := range sc.writableCols {
		if deriveType(sc.elementAttrs[col].sqlType) != kindBool {
			return col
		}
	}
	return sc.writableCols[0]
}
