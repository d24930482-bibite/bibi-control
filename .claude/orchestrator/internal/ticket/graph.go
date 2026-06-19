package ticket

import (
	"fmt"
	"sort"
	"strings"
)

// Ready returns the topological ready-set: tickets that are Pending and whose
// every dependency is Done. These are the only tickets the herder may dispatch.
func (s *Store) Ready() []*Ticket {
	var out []*Ticket
	for _, t := range s.All() {
		if t.Phase != Pending {
			continue
		}
		if s.depsDone(t) {
			out = append(out, t)
		}
	}
	return out
}

// depsDone reports whether all of t's dependencies exist and are Done.
func (s *Store) depsDone(t *Ticket) bool {
	for _, d := range t.Deps {
		dep, ok := s.tickets[d]
		if !ok || dep.Phase != Done {
			return false
		}
	}
	return true
}

// Validate checks referential integrity of the DAG: every dep must exist and
// the dependency graph must be acyclic. Returns all problems found.
func (s *Store) Validate() []error {
	var probs []error
	for _, t := range s.All() {
		for _, d := range t.Deps {
			if _, ok := s.tickets[d]; !ok {
				probs = append(probs, fmt.Errorf("%s depends on unknown ticket %q", t.ID, d))
			}
		}
	}
	if cyc := s.findCycle(); cyc != nil {
		probs = append(probs, fmt.Errorf("dependency cycle: %s", strings.Join(cyc, " -> ")))
	}
	return probs
}

// findCycle returns one cycle (as a path of ids) if the DAG has one, else nil.
func (s *Store) findCycle() []string {
	const (
		white = 0 // unvisited
		gray  = 1 // on the current DFS stack
		black = 2 // fully explored
	)
	color := map[string]int{}
	var stack []string
	var dfs func(id string) []string
	dfs = func(id string) []string {
		color[id] = gray
		stack = append(stack, id)
		t := s.tickets[id]
		if t != nil {
			for _, d := range t.Deps {
				if _, ok := s.tickets[d]; !ok {
					continue // missing dep reported separately by Validate
				}
				switch color[d] {
				case white:
					if c := dfs(d); c != nil {
						return c
					}
				case gray:
					// found a back-edge: slice the stack from d onward.
					for i, n := range stack {
						if n == d {
							return append(append([]string{}, stack[i:]...), d)
						}
					}
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[id] = black
		return nil
	}
	for _, t := range s.All() {
		if color[t.ID] == white {
			if c := dfs(t.ID); c != nil {
				return c
			}
		}
	}
	return nil
}

// Mermaid renders the DAG as a Mermaid flowchart, nodes colored by phase.
func (s *Store) Mermaid() string {
	var b strings.Builder
	b.WriteString("flowchart TD\n")

	// Stable node declarations.
	for _, t := range s.All() {
		label := fmt.Sprintf("%s: %s", t.ID, t.Title)
		fmt.Fprintf(&b, "    %s[%q]:::%s\n", nodeID(t.ID), label, t.Phase)
	}
	// Edges: dep --> ticket (dependency points at the work it unblocks).
	for _, t := range s.All() {
		deps := append([]string{}, t.Deps...)
		sort.Strings(deps)
		for _, d := range deps {
			fmt.Fprintf(&b, "    %s --> %s\n", nodeID(d), nodeID(t.ID))
		}
	}
	// Phase color classes.
	b.WriteString("\n")
	b.WriteString("    classDef pending fill:#eee,stroke:#999;\n")
	b.WriteString("    classDef planning fill:#cfe8ff,stroke:#1a73e8;\n")
	b.WriteString("    classDef executing fill:#fff3c4,stroke:#e8a91a;\n")
	b.WriteString("    classDef reviewing fill:#ffd8c4,stroke:#e8601a;\n")
	b.WriteString("    classDef done fill:#c4ffd0,stroke:#1ea84a;\n")
	b.WriteString("    classDef blocked fill:#ffc4c4,stroke:#d11;\n")
	return b.String()
}

// nodeID sanitizes a ticket id into a Mermaid-safe node identifier.
func nodeID(id string) string {
	return strings.NewReplacer("-", "_", ".", "_", " ", "_").Replace(id)
}

// phaseFill is the Graphviz node fill color per phase; it mirrors the Mermaid
// classDef palette above so both views read the same.
func phaseFill(p Phase) string {
	switch p {
	case Planning:
		return "#cfe8ff"
	case Executing:
		return "#fff3c4"
	case Reviewing:
		return "#ffd8c4"
	case Done:
		return "#c4ffd0"
	case Blocked:
		return "#ffc4c4"
	default: // pending
		return "#eeeeee"
	}
}

// Dot renders the DAG as Graphviz dot, nodes colored by phase. Render with e.g.
// `dot -Tsvg docs/tickets.dot -o docs/tickets.svg` (Graphviz is a local, no-network tool).
func (s *Store) Dot() string {
	var b strings.Builder
	b.WriteString("digraph tickets {\n")
	b.WriteString("  rankdir=TB;\n")
	b.WriteString("  node [shape=box, style=\"filled,rounded\", fontname=\"sans-serif\"];\n")
	for _, t := range s.All() {
		fmt.Fprintf(&b, "  %q [label=%q, fillcolor=%q];\n", t.ID, t.ID+": "+t.Title, phaseFill(t.Phase))
	}
	for _, t := range s.All() {
		deps := append([]string{}, t.Deps...)
		sort.Strings(deps)
		for _, d := range deps {
			fmt.Fprintf(&b, "  %q -> %q;\n", d, t.ID)
		}
	}
	b.WriteString("}\n")
	return b.String()
}
