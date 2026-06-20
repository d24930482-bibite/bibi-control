package thebibites

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	mutator "github.com/asemones/bibicontrol/savemutator/thebibites"
	"go.starlark.net/starlark"
)

// subcollection.go is the T11b read+structural-mutation surface for 1:many entity
// sub-tables (brain synapses/nodes, stomach contents), modeled on gene.go. An
// ElementCollection is a lazy, in-memory sequence over one entity's elements,
// exposed as b.synapses / b.nodes / b.stomach via the categorySubCollection
// registry. Append (friendly kwargs) and per-element delete are STRUCTURAL ops:
// staged on the session for the eventual commit but NOT mirrored into DuckDB
// (consistency contract, mirror.go) — an in-run read still sees the original set;
// the change appears only after commit.
//
// Brain-graph integrity is out of scope (v2): node/synapse append/delete are raw
// array edits with no cross-element validation (no synapse pruning on node delete,
// no node-index existence check on synapse append). The delete stale-value guard
// (a Require on one stable element field) is the one safety net, so a shifted index
// fails loudly rather than removing the wrong element.

// setRefArrayIndex stamps the array ordinal onto the SQLValueRef index field that
// matches the sub-collection's index column. It is the single mapping from a
// sub-collection to its mutator locator field.
func setRefArrayIndex(ref *mutator.SQLValueRef, indexColumn string, idx int64) error {
	switch indexColumn {
	case "synapse_row_index":
		ref.SynapseRowIndex, ref.HasSynapseRowIndex = int(idx), true
	case "node_row_index":
		ref.NodeRowIndex, ref.HasNodeRowIndex = int(idx), true
	case "content_index":
		ref.ContentIndex, ref.HasContentIndex = int(idx), true
	default:
		return fmt.Errorf("unknown sub-collection index column %q", indexColumn)
	}
	return nil
}

// ElementCollection is a lazy, in-memory sequence over one entity's elements of a
// 1:many sub-table. Exposed as b.synapses / b.nodes / b.stomach; iterate with
// `for s in b.synapses`. .append(**fields) stages a structural append.
type ElementCollection struct {
	ls        *LoadedSave
	kind      string
	entryName string
	spec      *subCollectionSpec
}

var (
	_ starlark.Value    = (*ElementCollection)(nil)
	_ starlark.Iterable = (*ElementCollection)(nil)
	_ starlark.Sequence = (*ElementCollection)(nil)
	_ starlark.HasAttrs = (*ElementCollection)(nil)
	_ starlark.Mapping  = (*ElementCollection)(nil)
)

func (c *ElementCollection) String() string       { return c.kind + "." + c.spec.attr }
func (c *ElementCollection) Type() string         { return "element_collection" }
func (c *ElementCollection) Freeze()              {}
func (c *ElementCollection) Truth() starlark.Bool { return starlark.Bool(c.Len() > 0) }
func (c *ElementCollection) Hash() (uint32, error) {
	return 0, fmt.Errorf("unhashable type: %s", c.Type())
}

func (c *ElementCollection) rows() []reflectValueAt {
	rows := c.ls.subRowsFor(c.spec.table, c.entryName)
	out := make([]reflectValueAt, len(rows))
	for i, r := range rows {
		out[i] = reflectValueAt{row: r, index: int64(i)}
	}
	return out
}

func (c *ElementCollection) Len() int {
	return len(c.ls.subRowsFor(c.spec.table, c.entryName))
}

func (c *ElementCollection) Iterate() starlark.Iterator {
	return &elementIterator{c: c, rows: c.rows()}
}

func (c *ElementCollection) Attr(name string) (starlark.Value, error) {
	switch name {
	case "append":
		return starlark.NewBuiltin("append", c.appendBuiltin), nil
	case "count":
		return starlark.NewBuiltin("count", c.countBuiltin), nil
	}
	return nil, nil
}

func (c *ElementCollection) AttrNames() []string { return []string{"append", "count"} }

// Get implements starlark.Mapping so b.nodes["name"] and b.nodes[i] route here.
// Starlark's getIndex checks Mapping before Indexable, so this single method
// handles both key types; Indexable is intentionally not implemented (would be
// dead, see plan §risks).
//
//   - starlark.String key: Desc fold lookup, nodes only.
//   - starlark.Int key: positional access (any sub-collection).
//   - Other types: loud error.
func (c *ElementCollection) Get(k starlark.Value) (starlark.Value, bool, error) {
	switch key := k.(type) {
	case starlark.String:
		if c.spec.attr != "nodes" {
			return nil, false, fmt.Errorf("%s has no name key (string subscript is only valid on nodes)", c.spec.attr)
		}
		name := string(key)
		ordinal, found, err := c.ls.nodeByDesc(c.spec.table, c.entryName, name)
		if err != nil {
			// Ambiguity — loud, never a silent pick.
			return nil, false, err
		}
		if !found {
			// Clean miss — Starlark raises KeyError for (nil, false, nil).
			return nil, false, nil
		}
		rows := c.rows()
		if ordinal < 0 || int(ordinal) >= len(rows) {
			return nil, false, fmt.Errorf("nodes[%q]: internal ordinal %d out of range (len %d)", name, ordinal, len(rows))
		}
		r := rows[ordinal]
		return &ArrayElement{ls: c.ls, kind: c.kind, entryName: c.entryName, spec: c.spec, row: r.row, index: r.index}, true, nil

	case starlark.Int:
		n, err2 := starlark.AsInt32(key)
		if err2 != nil {
			return nil, false, fmt.Errorf("%s index overflows int32: %w", c.spec.attr, err2)
		}
		length := c.Len()
		// Python-style negative indexing.
		idx := int(n)
		if idx < 0 {
			idx += length
		}
		if idx < 0 || idx >= length {
			return nil, false, fmt.Errorf("%s index %d out of range (len %d)", c.spec.attr, n, length)
		}
		rows := c.rows()
		r := rows[idx]
		return &ArrayElement{ls: c.ls, kind: c.kind, entryName: c.entryName, spec: c.spec, row: r.row, index: r.index}, true, nil

	default:
		return nil, false, fmt.Errorf("%s key must be a string (Desc) or int (position), got %s", c.spec.attr, k.Type())
	}
}

func (c *ElementCollection) countBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(b.Name(), args, kwargs); err != nil {
		return nil, err
	}
	return starlark.MakeInt(c.Len()), nil
}

// appendBuiltin implements b.<sub>.append(field=value, ...): stage a structural
// append of one element built from friendly kwargs. Every writable element column
// must be supplied (so the appended element is well-formed); unknown kwargs and
// out-of-domain values are rejected before staging. Not mirrored into DuckDB.
func (c *ElementCollection) appendBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if len(args) > 0 {
		return nil, fmt.Errorf("%s.append takes only keyword arguments", c.spec.attr)
	}
	provided := make(map[string]starlark.Value, len(kwargs))
	for _, kv := range kwargs {
		provided[string(kv[0].(starlark.String))] = kv[1]
	}

	element := make(map[string]any, len(c.spec.writableCols))
	for name, val := range provided {
		spec, ok := c.spec.elementAttrs[name]
		if !ok {
			return nil, fmt.Errorf("%s.append: unknown field %q", c.spec.attr, name)
		}
		if !spec.writable {
			return nil, fmt.Errorf("%s.append: field %q is read-only", c.spec.attr, name)
		}
		goVal, err := fromStarlark(val)
		if err != nil {
			return nil, fmt.Errorf("%s.append: field %q: %w", c.spec.attr, name, err)
		}
		if err := validateSet(spec, goVal); err != nil {
			return nil, fmt.Errorf("%s.append: field %q: %w", c.spec.attr, name, err)
		}
		element[spec.jsonKey] = goVal
	}
	for _, col := range c.spec.writableCols {
		if _, ok := provided[col]; !ok {
			return nil, fmt.Errorf("%s.append: missing required field %q (expected %s)", c.spec.attr, col, strings.Join(c.spec.writableCols, ", "))
		}
	}

	ref, err := c.ls.entityLocatorRef(c.kind, c.entryName)
	if err != nil {
		return nil, err
	}
	ref.Table = c.spec.table
	if err := c.ls.session.StageSQLAppend(ref, element); err != nil {
		return nil, fmt.Errorf("%s.append: %w", c.spec.attr, err)
	}
	c.ls.stagedOps++
	c.ls.markStructuralStaged()
	return starlark.None, nil
}

// reflectValueAt pairs an element row with its array index.
type reflectValueAt struct {
	row   reflect.Value
	index int64
}

type elementIterator struct {
	c    *ElementCollection
	rows []reflectValueAt
	pos  int
}

func (it *elementIterator) Next(p *starlark.Value) bool {
	if it.pos >= len(it.rows) {
		return false
	}
	r := it.rows[it.pos]
	*p = &ArrayElement{ls: it.c.ls, kind: it.c.kind, entryName: it.c.entryName, spec: it.c.spec, row: r.row, index: r.index}
	it.pos++
	return true
}

func (it *elementIterator) Done() {}

// ArrayElement is a read view of one sub-collection element plus a .delete()
// method. Element columns resolve through the sub-collection's elementAttrs
// (derived from generated metadata); `index` exposes the array ordinal.
type ArrayElement struct {
	ls        *LoadedSave
	kind      string
	entryName string
	spec      *subCollectionSpec
	row       reflect.Value
	index     int64
}

var (
	_ starlark.Value    = (*ArrayElement)(nil)
	_ starlark.HasAttrs = (*ArrayElement)(nil)
)

func (e *ArrayElement) String() string       { return fmt.Sprintf("%s[%d]", e.spec.attr, e.index) }
func (e *ArrayElement) Type() string         { return e.spec.attr + "_element" }
func (e *ArrayElement) Freeze()              {}
func (e *ArrayElement) Truth() starlark.Bool { return starlark.True }
func (e *ArrayElement) Hash() (uint32, error) {
	return starlark.String(fmt.Sprintf("%s\x00%s\x00%d", e.kind, e.entryName, e.index)).Hash()
}

func (e *ArrayElement) Attr(name string) (starlark.Value, error) {
	switch name {
	case "index":
		return starlark.MakeInt64(e.index), nil
	case "delete":
		return starlark.NewBuiltin("delete", e.deleteBuiltin), nil
	case "source":
		if e.spec.attr != "synapses" {
			return nil, nil
		}
		return e.navSynapseEndpoint("source", "node_in")
	case "target":
		if e.spec.attr != "synapses" {
			return nil, nil
		}
		return e.navSynapseEndpoint("target", "node_out")
	case "inputs":
		if e.spec.attr != "nodes" {
			return nil, nil
		}
		return e.navNodeSynapses("inputs", "node_out")
	case "outputs":
		if e.spec.attr != "nodes" {
			return nil, nil
		}
		return e.navNodeSynapses("outputs", "node_in")
	}
	spec, ok := e.spec.elementAttrs[name]
	if !ok {
		return nil, nil
	}
	return toStarlark(e.row.FieldByIndex(spec.fieldIndex))
}

func (e *ArrayElement) AttrNames() []string {
	names := make([]string, 0, len(e.spec.elementAttrs)+4)
	for name := range e.spec.elementAttrs {
		names = append(names, name)
	}
	names = append(names, "delete", "index")
	switch e.spec.attr {
	case "synapses":
		names = append(names, "source", "target")
	case "nodes":
		names = append(names, "inputs", "outputs")
	}
	sort.Strings(names)
	return names
}

// navSynapseEndpoint resolves syn.source or syn.target by reading the synapse's
// node_in or node_out field (the logical NodeIndex), then looking up the node whose
// node_index equals that value in the same entity's nodes sub-collection. Returns a
// fresh *ArrayElement for the nodes sub-collection with the correct array ordinal.
// A missing node_index is a loud error (§4/§5 rule — never silent None).
func (e *ArrayElement) navSynapseEndpoint(navName, endpointCol string) (starlark.Value, error) {
	// Read the logical NodeIndex from this synapse's node_in / node_out.
	epSpec, ok := e.spec.elementAttrs[endpointCol]
	if !ok {
		return nil, fmt.Errorf("synapse.%s: no column %q in synapse spec", navName, endpointCol)
	}
	nodeIdx := e.row.FieldByIndex(epSpec.fieldIndex).Int()

	// Resolve the nodes sub-collection spec for this entity kind.
	nodesSpec, ok := subCollectionRegistry()[e.kind]["nodes"]
	if !ok {
		return nil, fmt.Errorf("synapse.%s: no nodes sub-collection registered for kind %q", navName, e.kind)
	}

	// Lookup via the memoized nodeByIndex map (O(1) after first build).
	nodeRow, ordinal, found := e.ls.nodeByIndex(nodesSpec.table, e.entryName, nodeIdx)
	if !found {
		return nil, fmt.Errorf("synapse.%s: no node with node_index %d", navName, nodeIdx)
	}
	return &ArrayElement{
		ls:        e.ls,
		kind:      e.kind,
		entryName: e.entryName,
		spec:      nodesSpec,
		row:       nodeRow,
		index:     ordinal,
	}, nil
}

// navNodeSynapses implements n.inputs (edges where node_out == n.node_index) and
// n.outputs (edges where node_in == n.node_index). Scans the entity's synapses rows,
// collects matches, and returns a *starlark.List of *ArrayElement synapse handles.
// Each handle carries the synapse's correct array ordinal so .weight, .source,
// .delete() all work on the returned elements.
func (e *ArrayElement) navNodeSynapses(navName, matchCol string) (starlark.Value, error) {
	// Read this node's logical NodeIndex.
	niSpec, ok := e.spec.elementAttrs["node_index"]
	if !ok {
		return nil, fmt.Errorf("node.%s: no column node_index in node spec", navName)
	}
	myNodeIndex := e.row.FieldByIndex(niSpec.fieldIndex).Int()

	// Resolve the synapses sub-collection spec for this entity kind.
	synSpec, ok := subCollectionRegistry()[e.kind]["synapses"]
	if !ok {
		return nil, fmt.Errorf("node.%s: no synapses sub-collection registered for kind %q", navName, e.kind)
	}
	matchSpec, ok := synSpec.elementAttrs[matchCol]
	if !ok {
		return nil, fmt.Errorf("node.%s: no column %q in synapse spec", navName, matchCol)
	}

	// Scan all synapses for this entity and collect matching ones.
	synRows := e.ls.subRowsFor(synSpec.table, e.entryName)
	out := starlark.NewList(nil)
	for ordinal, row := range synRows {
		if row.FieldByIndex(matchSpec.fieldIndex).Int() == myNodeIndex {
			elem := &ArrayElement{
				ls:        e.ls,
				kind:      e.kind,
				entryName: e.entryName,
				spec:      synSpec,
				row:       row,
				index:     int64(ordinal),
			}
			if err := out.Append(elem); err != nil {
				return nil, fmt.Errorf("node.%s: %w", navName, err)
			}
		}
	}
	return out, nil
}

// deleteBuiltin implements element.delete(): stage a structural delete of this
// array element, located by its array index and guarded by the current value of a
// stable element field (so a shifted/stale index fails loudly at commit rather than
// removing a different element). Structural — not mirrored into DuckDB.
func (e *ArrayElement) deleteBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(b.Name(), args, kwargs); err != nil {
		return nil, err
	}
	ref, err := e.ls.subElementRef(e.kind, e.entryName, e.spec, e.index)
	if err != nil {
		return nil, err
	}
	if e.spec.guardColumn != "" {
		guardSpec := e.spec.elementAttrs[e.spec.guardColumn]
		cur, err := goScalar(e.row.FieldByIndex(guardSpec.fieldIndex))
		if err != nil {
			return nil, fmt.Errorf("%s.delete: %w", e.spec.attr, err)
		}
		ref.Column = guardSpec.sourceColumn
		ref = ref.WithExpected(cur)
	}
	if err := e.ls.session.StageSQLDelete(ref); err != nil {
		return nil, fmt.Errorf("%s.delete: %w", e.spec.attr, err)
	}
	e.ls.stagedOps++
	e.ls.markStructuralStaged()
	return starlark.None, nil
}
