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

func resolveBibiteStomachColumn(ref SQLValueRef) (Target, string, error) {
	field, err := sqlRefColumnValue(ref, bibiteStomachContentColumnFields)
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

func resolveGeneColumn(ref SQLValueRef, kind tb.EntryKind) (Target, string, error) {
	switch ref.Column {
	case "number_value", "bool_value", "string_value":
	default:
		return Target{}, "", unsupportedSQLValueRef(ref)
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

func resolveEntityBrainNodeColumn(ref SQLValueRef, kind tb.EntryKind) (Target, string, error) {
	return resolveEntityBrainIndexedColumn(ref, kind, brainNodeColumnKeys, ref.NodeRowIndex, ref.HasNodeRowIndex, "node_row_index", "brain.Nodes")
}

func resolveEntityBrainSynapseColumn(ref SQLValueRef, kind tb.EntryKind) (Target, string, error) {
	return resolveEntityBrainIndexedColumn(ref, kind, brainSynapseColumnKeys, ref.SynapseRowIndex, ref.HasSynapseRowIndex, "synapse_row_index", "brain.Synapses")
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

func resolvePelletColumn(ref SQLValueRef) (Target, string, error) {
	field, err := sqlRefColumnValue(ref, pelletColumnPaths)
	if err != nil {
		return Target{}, "", err
	}
	if err := requireSQLRefString(ref, ref.EntryName, "entry_name"); err != nil {
		return Target{}, "", err
	}
	if err := requireSQLRefFlag(ref, ref.HasGroupIndex, "group_index"); err != nil {
		return Target{}, "", err
	}
	if err := requireSQLRefFlag(ref, ref.HasGroupPelletIndex, "group_pellet_index"); err != nil {
		return Target{}, "", err
	}
	guards := make([]Guard, 0, 1)
	if ref.HasZone {
		guards = append(guards, Require(fmt.Sprintf("pellets[%d].zone", ref.GroupIndex), ref.Zone))
	}
	path := fmt.Sprintf("pellets[%d].pellets[%d].%s", ref.GroupIndex, ref.GroupPelletIndex, field)
	return EntryTarget(ref.EntryName, tb.EntryPellets, guards...), path, nil
}

func resolvePheromoneColumn(ref SQLValueRef) (Target, string, error) {
	field, err := sqlRefColumnValue(ref, pheromoneColumnPaths)
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
