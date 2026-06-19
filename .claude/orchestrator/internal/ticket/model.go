// Package ticket is the deterministic engine behind the bibicontrol agent
// orchestrator: the ticket DAG store, the topological ready-set, the Mermaid
// renderer, and the phase gates that the planner/executor/reviewer agents must
// pass before a ticket can advance.
//
// Nothing in this package calls an LLM. Every "did the agent do the step?"
// question reduces to a file check or a process exit code, which is what makes
// the enforcement deterministic and reproducible.
package ticket

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Phase is a ticket's position in the per-ticket plan->execute->review sub-DAG.
type Phase string

const (
	Pending   Phase = "pending"   // not yet picked up by the herder
	Planning  Phase = "planning"  // a planner agent owns it
	Executing Phase = "executing" // an executor agent owns it
	Reviewing Phase = "reviewing" // a reviewer agent owns it
	Done      Phase = "done"      // merged / complete
	Blocked   Phase = "blocked"   // manual hold
)

// ValidPhase reports whether s names a known phase.
func ValidPhase(s string) bool {
	switch Phase(s) {
	case Pending, Planning, Executing, Reviewing, Done, Blocked:
		return true
	}
	return false
}

// Artifacts records the repo-relative paths the gates check for.
type Artifacts struct {
	Plan   string `yaml:"plan"`
	Review string `yaml:"review"`
}

// Ticket is one node of the DAG, persisted as one YAML file in the tickets dir.
type Ticket struct {
	ID        string    `yaml:"id"`
	Title     string    `yaml:"title"`
	Deps      []string  `yaml:"deps"`     // edges: this ticket waits on these ids
	Phase     Phase     `yaml:"phase"`    // current phase
	Worktree  string    `yaml:"worktree"` // abs path the executor works in
	Artifacts Artifacts `yaml:"artifacts"`
	Source    string    `yaml:"source"` // link back to the prose ticket
	DoD       []string  `yaml:"dod"`    // advisory checklist, NOT a gate
	Notes     string    `yaml:"notes,omitempty"`

	// Models optionally overrides the LLM model the herder dispatches for each
	// phase's worker agent. Keys are the short phase forms "plan"/"exec"/"review";
	// values are model names (see ValidModel). An absent or empty entry means the
	// agent definition's frontmatter default applies. Omitted entirely when nil so
	// existing ticket files don't churn.
	Models map[string]string `yaml:"models,omitempty"`

	path string `yaml:"-"` // file this ticket was loaded from; not serialized
}

// ModelKeyForPhase maps a worker phase to its key in Ticket.Models, or "" for
// phases that have no dispatched worker (pending/done/blocked).
func ModelKeyForPhase(p Phase) string {
	switch p {
	case Planning:
		return "plan"
	case Executing:
		return "exec"
	case Reviewing:
		return "review"
	}
	return ""
}

// ModelFor returns the per-phase model override for p, or "" when none is set
// (in which case the herder omits the Agent `model` arg and the agent's
// frontmatter default is used).
func (t *Ticket) ModelFor(p Phase) string {
	return t.Models[ModelKeyForPhase(p)]
}

// ValidModel reports whether s names a model the Agent dispatch accepts as a
// per-phase override.
func ValidModel(s string) bool {
	switch s {
	case "opus", "sonnet", "haiku", "fable":
		return true
	}
	return false
}

// Store is an in-memory view of a directory of ticket files.
type Store struct {
	Dir     string
	tickets map[string]*Ticket
}

// Load reads every *.yml / *.yaml file in dir into a Store.
func Load(dir string) (*Store, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read tickets dir %s: %w", dir, err)
	}
	s := &Store{Dir: dir, tickets: map[string]*Ticket{}}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml") {
			continue
		}
		p := filepath.Join(dir, name)
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		var t Ticket
		if err := yaml.Unmarshal(raw, &t); err != nil {
			return nil, fmt.Errorf("parse %s: %w", p, err)
		}
		if t.ID == "" {
			return nil, fmt.Errorf("%s: ticket has no id", p)
		}
		if _, dup := s.tickets[t.ID]; dup {
			return nil, fmt.Errorf("duplicate ticket id %q (%s)", t.ID, p)
		}
		t.path = p
		s.tickets[t.ID] = &t
	}
	return s, nil
}

// Get returns the ticket with the given id, or an error if absent.
func (s *Store) Get(id string) (*Ticket, error) {
	t, ok := s.tickets[id]
	if !ok {
		return nil, fmt.Errorf("no such ticket: %s", id)
	}
	return t, nil
}

// All returns every ticket sorted by id for stable output.
func (s *Store) All() []*Ticket {
	out := make([]*Ticket, 0, len(s.tickets))
	for _, t := range s.tickets {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Save writes a ticket back to its file (or a new file under Dir for new ones).
func (s *Store) Save(t *Ticket) error {
	if t.path == "" {
		t.path = filepath.Join(s.Dir, t.ID+".yml")
	}
	raw, err := yaml.Marshal(t)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", t.ID, err)
	}
	if err := os.WriteFile(t.path, raw, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", t.path, err)
	}
	s.tickets[t.ID] = t
	return nil
}

// Add registers a brand-new ticket in the store (without writing it yet).
func (s *Store) Add(t *Ticket) error {
	if _, dup := s.tickets[t.ID]; dup {
		return fmt.Errorf("ticket %s already exists", t.ID)
	}
	s.tickets[t.ID] = t
	return nil
}
