package thebibites

import (
	"reflect"
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
