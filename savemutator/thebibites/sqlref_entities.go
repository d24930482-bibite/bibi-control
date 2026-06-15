package thebibites

import (
	"fmt"
	"strconv"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

type sqlRefTargetResolver func(SQLValueRef) (Target, error)

func pathMapResolver(paths map[string]string, targetResolver sqlRefTargetResolver) sqlRefResolver {
	return func(ref SQLValueRef) (Target, string, error) {
		path, err := sqlRefColumnValue(ref, paths)
		if err != nil {
			return Target{}, "", err
		}
		target, err := targetResolver(ref)
		if err != nil {
			return Target{}, "", err
		}
		return target, path, nil
	}
}

func bibiteStomachColumnResolver(columns map[string]string) sqlRefResolver {
	return func(ref SQLValueRef) (Target, string, error) {
		return resolveBibiteStomachColumn(ref, columns)
	}
}

func resolveBibiteStomachColumn(ref SQLValueRef, columns map[string]string) (Target, string, error) {
	field, err := sqlRefColumnValue(ref, columns)
	if err != nil {
		return Target{}, "", err
	}
	if err := requireSQLRefFlag(ref, ref.HasContentIndex, "content_index"); err != nil {
		return Target{}, "", err
	}
	target, err := bibiteTargetFromSQLRef(ref)
	if err != nil {
		return Target{}, "", err
	}
	return target, fmt.Sprintf("body.stomach.content[%d].%s", ref.ContentIndex, field), nil
}

func resolveGeneColumn(ref SQLValueRef, kind tb.EntryKind, columns map[string]string) (Target, string, error) {
	if _, err := sqlRefColumnValue(ref, columns); err != nil {
		return Target{}, "", err
	}
	if err := requireSQLRefString(ref, ref.Path, "path"); err != nil {
		return Target{}, "", err
	}

	switch kind {
	case tb.EntryBibite:
		target, err := bibiteTargetFromGeneRef(ref)
		if err != nil {
			return Target{}, "", err
		}
		return target, ref.Path, nil
	case tb.EntryEgg:
		target, err := eggTargetFromGeneRef(ref)
		if err != nil {
			return Target{}, "", err
		}
		return target, ref.Path, nil
	default:
		return Target{}, "", unsupportedSQLValueRef(ref)
	}
}

func brainNodeColumnResolver(kind tb.EntryKind, columns map[string]string) sqlRefResolver {
	return func(ref SQLValueRef) (Target, string, error) {
		return resolveEntityBrainIndexedColumn(ref, kind, columns, ref.NodeRowIndex, ref.HasNodeRowIndex, "node_row_index", "brain.Nodes")
	}
}

func brainSynapseColumnResolver(kind tb.EntryKind, columns map[string]string) sqlRefResolver {
	return func(ref SQLValueRef) (Target, string, error) {
		key, err := sqlRefColumnValue(ref, columns)
		if err != nil {
			return Target{}, "", err
		}
		target, element, err := entitySynapseDeleteTarget(ref, kind)
		if err != nil {
			return Target{}, "", err
		}
		return target, element + "." + key, nil
	}
}

// entitySynapseAppendTarget resolves the brain.Synapses array container for an
// append; entitySynapseDeleteTarget extends it with the row index for an element
// delete/set. SET, DELETE, and APPEND all share this locator core.
func entitySynapseAppendTarget(ref SQLValueRef, kind tb.EntryKind) (Target, string, error) {
	target, err := entityTargetFromSQLRef(ref, kind)
	if err != nil {
		return Target{}, "", err
	}
	return target, "brain.Synapses", nil
}

func entitySynapseDeleteTarget(ref SQLValueRef, kind tb.EntryKind) (Target, string, error) {
	if err := requireSQLRefFlag(ref, ref.HasSynapseRowIndex, "synapse_row_index"); err != nil {
		return Target{}, "", err
	}
	target, container, err := entitySynapseAppendTarget(ref, kind)
	if err != nil {
		return Target{}, "", err
	}
	return target, fmt.Sprintf("%s[%d]", container, ref.SynapseRowIndex), nil
}

func resolveEntityBrainIndexedColumn(ref SQLValueRef, kind tb.EntryKind, columns map[string]string, index int, hasIndex bool, indexField, pathPrefix string) (Target, string, error) {
	key, err := sqlRefColumnValue(ref, columns)
	if err != nil {
		return Target{}, "", err
	}
	if err := requireSQLRefFlag(ref, hasIndex, indexField); err != nil {
		return Target{}, "", err
	}
	target, err := entityTargetFromSQLRef(ref, kind)
	if err != nil {
		return Target{}, "", err
	}
	return target, fmt.Sprintf("%s[%d].%s", pathPrefix, index, key), nil
}

func pelletColumnResolver(columns map[string]string) sqlRefResolver {
	return func(ref SQLValueRef) (Target, string, error) {
		return resolvePelletColumn(ref, columns)
	}
}

func resolvePelletColumn(ref SQLValueRef, columns map[string]string) (Target, string, error) {
	field, err := sqlRefColumnValue(ref, columns)
	if err != nil {
		return Target{}, "", err
	}
	target, element, err := pelletDeleteTarget(ref)
	if err != nil {
		return Target{}, "", err
	}
	return target, element + "." + field, nil
}

// pelletAppendTarget resolves the pellets[g].pellets array container for an
// append; pelletDeleteTarget extends it with the in-group index for an element
// delete/set. SET, DELETE, and APPEND all share this locator/guard core.
func pelletAppendTarget(ref SQLValueRef) (Target, string, error) {
	if err := requireSQLRefString(ref, ref.EntryName, "entry_name"); err != nil {
		return Target{}, "", err
	}
	if err := requireSQLRefFlag(ref, ref.HasGroupIndex, "group_index"); err != nil {
		return Target{}, "", err
	}
	guards := make([]Guard, 0, 1)
	if ref.HasZone {
		guards = append(guards, Require(fmt.Sprintf("pellets[%d].zone", ref.GroupIndex), ref.Zone))
	}
	return EntryTarget(ref.EntryName, tb.EntryPellets, guards...), fmt.Sprintf("pellets[%d].pellets", ref.GroupIndex), nil
}

func pelletDeleteTarget(ref SQLValueRef) (Target, string, error) {
	target, container, err := pelletAppendTarget(ref)
	if err != nil {
		return Target{}, "", err
	}
	if err := requireSQLRefFlag(ref, ref.HasGroupPelletIndex, "group_pellet_index"); err != nil {
		return Target{}, "", err
	}
	return target, fmt.Sprintf("%s[%d]", container, ref.GroupPelletIndex), nil
}

func pheromoneColumnResolver(columns map[string]string) sqlRefResolver {
	return func(ref SQLValueRef) (Target, string, error) {
		return resolvePheromoneColumn(ref, columns)
	}
}

func resolvePheromoneColumn(ref SQLValueRef, columns map[string]string) (Target, string, error) {
	field, err := sqlRefColumnValue(ref, columns)
	if err != nil {
		return Target{}, "", err
	}
	if err := requireSQLRefString(ref, ref.EntryName, "entry_name"); err != nil {
		return Target{}, "", err
	}
	if err := requireSQLRefFlag(ref, ref.HasPheromoneIndex, "pheromone_index"); err != nil {
		return Target{}, "", err
	}
	return EntryTarget(ref.EntryName, tb.EntryPheromones), fmt.Sprintf("pheromones[%d].%s", ref.PheromoneIndex, field), nil
}

func entityTargetFromSQLRef(ref SQLValueRef, kind tb.EntryKind) (Target, error) {
	switch kind {
	case tb.EntryBibite:
		return bibiteTargetFromSQLRef(ref)
	case tb.EntryEgg:
		return eggTargetFromSQLRef(ref)
	default:
		return Target{}, unsupportedSQLValueRef(ref)
	}
}

func bibiteTargetFromSQLRef(ref SQLValueRef) (Target, error) {
	if err := requireSQLRefString(ref, ref.EntryName, "entry_name"); err != nil {
		return Target{}, err
	}
	if err := requireSQLRefFlag(ref, ref.HasBodyID, "body_id"); err != nil {
		return Target{}, err
	}
	return BibiteTarget(BibiteRef{EntryName: ref.EntryName, BodyID: ref.BodyID}), nil
}

func eggTargetFromSQLRef(ref SQLValueRef) (Target, error) {
	if err := requireSQLRefString(ref, ref.EntryName, "entry_name"); err != nil {
		return Target{}, err
	}
	if err := requireSQLRefFlag(ref, ref.HasEggID, "egg_id"); err != nil {
		return Target{}, err
	}
	return EntryTarget(ref.EntryName, tb.EntryEgg, Require("egg.id", ref.EggID)), nil
}

func bibiteTargetFromGeneRef(ref SQLValueRef) (Target, error) {
	id, err := ownerIDAsInt(ref)
	if err != nil {
		return Target{}, err
	}
	if ref.OwnerKind != "" && ref.OwnerKind != "bibite" {
		return Target{}, fmt.Errorf("%s.%s owner_kind = %q, want bibite", ref.Table, ref.Column, ref.OwnerKind)
	}
	ref.BodyID = id
	ref.HasBodyID = true
	return bibiteTargetFromSQLRef(ref)
}

func eggTargetFromGeneRef(ref SQLValueRef) (Target, error) {
	id, err := ownerIDAsInt(ref)
	if err != nil {
		return Target{}, err
	}
	if ref.OwnerKind != "" && ref.OwnerKind != "egg" {
		return Target{}, fmt.Errorf("%s.%s owner_kind = %q, want egg", ref.Table, ref.Column, ref.OwnerKind)
	}
	ref.EggID = id
	ref.HasEggID = true
	return eggTargetFromSQLRef(ref)
}

func ownerIDAsInt(ref SQLValueRef) (int64, error) {
	if err := requireSQLRefString(ref, ref.OwnerID, "owner_id"); err != nil {
		return 0, err
	}
	id, err := strconv.ParseInt(ref.OwnerID, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s.%s owner_id %q is not an integer: %w", ref.Table, ref.Column, ref.OwnerID, err)
	}
	return id, nil
}
