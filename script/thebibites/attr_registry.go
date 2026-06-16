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
type attrSpec struct {
	category   attrCategory
	table      string // normalized table the value lives in
	column     string // friendly (snake_case) name, == NormalizedFieldSpec.Column
	fieldIndex []int  // reflect index path into the row struct
	writable   bool   // field has an sqlref path (mutable) — used by T6, not T4
	sqlType    string
	jsonKey    string // raw JSON key (NormalizedFieldSpec.SQLRefPath); used by T11b sub-collection append
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
}

// overrides renames/aliases generated column names to friendlier ones, keyed by
// entity kind then friendly alias -> source column. It is the only hand-edited
// surface in the registry and is intentionally tiny. (Gene-backed aliases like
// "diet" are not added here because genes resolve through gene(), not columns.)
var overrides = map[string]map[string]string{}

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
	registry = make(map[string]map[string]attrSpec, len(entityTables))

	specByTable := make(map[string]tb.NormalizedTableSpec, len(tb.NormalizedTables))
	for _, spec := range tb.NormalizedTables {
		specByTable[spec.Table] = spec
	}

	// Resolve row struct types by SaveField off a zero ExtractedSave so field
	// indices can be precomputed without an instance.
	extractedType := reflect.TypeOf(tb.ExtractedSave{})

	for kind, tables := range entityTables {
		attrs := make(map[string]attrSpec)
		for _, table := range tables {
			spec, ok := specByTable[table]
			if !ok {
				continue
			}
			rowType := rowTypeFor(extractedType, spec.SaveField)
			if rowType == nil {
				continue
			}
			for _, field := range spec.Fields {
				if field.Column == "save_id" {
					continue // pure locator noise
				}
				if _, exists := attrs[field.Column]; exists {
					continue // first table wins (identity table precedence)
				}
				sf, found := rowType.FieldByName(field.Field)
				if !found {
					continue
				}
				idx := make([]int, len(sf.Index))
				copy(idx, sf.Index)
				attrs[field.Column] = attrSpec{
					category:   categoryScalar,
					table:      table,
					column:     field.Column,
					fieldIndex: idx,
					writable:   field.SQLRefPath != "",
					sqlType:    field.SQLType,
					jsonKey:    field.SQLRefPath,
				}
			}
		}
		for alias, source := range overrides[kind] {
			if spec, ok := attrs[source]; ok {
				spec.column = alias
				attrs[alias] = spec
			}
		}
		registry[kind] = attrs
	}
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
	guardColumn  string              // first writable column, used as the delete stale guard
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
					category:   categoryScalar,
					table:      info.table,
					column:     field.Column,
					fieldIndex: idx,
					writable:   field.SQLRefPath != "",
					sqlType:    field.SQLType,
					jsonKey:    field.SQLRefPath,
				}
				if field.SQLRefPath != "" {
					sc.writableCols = append(sc.writableCols, field.Column)
				}
			}
			sort.Strings(sc.writableCols)
			if len(sc.writableCols) > 0 {
				sc.guardColumn = sc.writableCols[0]
			}
			out[attr] = sc
		}
		subRegistryMap[kind] = out
	}
}
