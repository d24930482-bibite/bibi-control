package thebibites

import "fmt"

func parseBrainNodes(entryName, ownerKind, ownerID string, values []any) []BrainNode {
	nodes := make([]BrainNode, 0, len(values))
	for i, value := range values {
		raw, ok := asMap(value)
		if !ok {
			continue
		}
		node := BrainNode{
			Index:     i,
			EntryName: entryName,
			OwnerKind: ownerKind,
			OwnerID:   ownerID,
			Raw:       raw,
			Scalars:   collectScalars(entryName, ownerKind+"_brain_node", ownerID, fmt.Sprintf("brain.nodes[%d]", i), raw),
		}
		if v, ok := intAt(raw, "Type"); ok {
			node.Type = v
		}
		if v, ok := stringAt(raw, "TypeName"); ok {
			node.TypeName = v
		}
		if v, ok := intAt(raw, "Index"); ok {
			node.NodeIndex = v
		}
		if v, ok := intAt(raw, "Inov"); ok {
			node.Innovation = v
		}
		if v, ok := stringAt(raw, "Desc"); ok {
			node.Desc = v
		}
		if v, ok := intAt(raw, "archetype"); ok {
			node.Archetype = v
		}
		if v, ok := floatAt(raw, "baseActivation"); ok {
			node.BaseActivation = v
		}
		if v, ok := floatAt(raw, "Value"); ok {
			node.Value = v
		}
		if v, ok := floatAt(raw, "LastInput"); ok {
			node.LastInput = v
		}
		if v, ok := floatAt(raw, "LastOutput"); ok {
			node.LastOutput = v
		}
		nodes = append(nodes, node)
	}
	return nodes
}

func parseBrainSynapses(entryName, ownerKind, ownerID string, values []any) []BrainSynapse {
	synapses := make([]BrainSynapse, 0, len(values))
	for i, value := range values {
		raw, ok := asMap(value)
		if !ok {
			continue
		}
		synapse := BrainSynapse{
			Index:     i,
			EntryName: entryName,
			OwnerKind: ownerKind,
			OwnerID:   ownerID,
			Raw:       raw,
			Scalars:   collectScalars(entryName, ownerKind+"_brain_synapse", ownerID, fmt.Sprintf("brain.synapses[%d]", i), raw),
		}
		if v, ok := intAt(raw, "Inov"); ok {
			synapse.Innovation = v
		}
		if v, ok := intAt(raw, "NodeIn"); ok {
			synapse.NodeIn = v
		}
		if v, ok := intAt(raw, "NodeOut"); ok {
			synapse.NodeOut = v
		}
		if v, ok := floatAt(raw, "Weight"); ok {
			synapse.Weight = v
		}
		if v, ok := boolAt(raw, "En"); ok {
			synapse.Enabled = v
		}
		synapses = append(synapses, synapse)
	}
	return synapses
}
