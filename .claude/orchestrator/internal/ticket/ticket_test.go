package ticket

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// storeFrom builds an in-memory store from literal tickets for tests.
func storeFrom(ts ...*Ticket) *Store {
	s := &Store{Dir: "", tickets: map[string]*Ticket{}}
	for _, t := range ts {
		s.tickets[t.ID] = t
	}
	return s
}

func ids(ts []*Ticket) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.ID
	}
	return out
}

func TestReady(t *testing.T) {
	s := storeFrom(
		&Ticket{ID: "A", Phase: Done},
		&Ticket{ID: "B", Phase: Pending, Deps: []string{"A"}},       // ready: A done
		&Ticket{ID: "C", Phase: Pending, Deps: []string{"B"}},       // not ready: B pending
		&Ticket{ID: "D", Phase: Pending},                            // ready: no deps
		&Ticket{ID: "E", Phase: Executing},                          // not ready: not pending
		&Ticket{ID: "F", Phase: Pending, Deps: []string{"A", "D"}}, // not ready: D not done
	)
	got := ids(s.Ready())
	want := []string{"B", "D"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Ready() = %v, want %v", got, want)
	}
}

func TestValidateMissingDep(t *testing.T) {
	s := storeFrom(&Ticket{ID: "A", Phase: Pending, Deps: []string{"ghost"}})
	probs := s.Validate()
	if len(probs) != 1 || !strings.Contains(probs[0].Error(), "unknown ticket") {
		t.Fatalf("expected one missing-dep problem, got %v", probs)
	}
}

func TestValidateCycle(t *testing.T) {
	s := storeFrom(
		&Ticket{ID: "A", Deps: []string{"B"}},
		&Ticket{ID: "B", Deps: []string{"C"}},
		&Ticket{ID: "C", Deps: []string{"A"}},
	)
	probs := s.Validate()
	foundCycle := false
	for _, p := range probs {
		if strings.Contains(p.Error(), "cycle") {
			foundCycle = true
		}
	}
	if !foundCycle {
		t.Fatalf("expected a cycle to be reported, got %v", probs)
	}
}

func TestValidateAcyclic(t *testing.T) {
	s := storeFrom(
		&Ticket{ID: "A"},
		&Ticket{ID: "B", Deps: []string{"A"}},
		&Ticket{ID: "C", Deps: []string{"A", "B"}},
	)
	if probs := s.Validate(); len(probs) != 0 {
		t.Fatalf("expected valid DAG, got %v", probs)
	}
}

func TestNextPhase(t *testing.T) {
	cases := []struct {
		in   Phase
		want Phase
		ok   bool
	}{
		{Planning, Executing, true},
		{Executing, Reviewing, true},
		{Reviewing, Done, true},
		{Pending, "", false},
		{Done, "", false},
	}
	for _, c := range cases {
		got, ok := NextPhase(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("NextPhase(%s) = (%s,%v), want (%s,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

// testEnv returns a GateEnv with controllable fakes and a temp repo root.
func testEnv(t *testing.T, testsPass bool, changed bool) GateEnv {
	t.Helper()
	root := t.TempDir()
	return GateEnv{
		RepoRoot: root,
		Base:     "main",
		RunTests: func(string) error {
			if testsPass {
				return nil
			}
			return &gateErr{"red"}
		},
		HasChanges: func(string, string) (bool, error) { return changed, nil },
		ReadFile:   os.ReadFile,
	}
}

type gateErr struct{ s string }

func (e *gateErr) Error() string { return e.s }

// writeArtifact creates a non-empty file under the env's repo root and returns
// its repo-relative path.
func writeArtifact(t *testing.T, env GateEnv, rel, content string) string {
	t.Helper()
	abs := filepath.Join(env.RepoRoot, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return rel
}

func TestPlanGate(t *testing.T) {
	env := testEnv(t, true, true)

	// Missing artifact -> fail.
	tk := &Ticket{ID: "T1", Phase: Planning}
	if err := env.Verify(tk, Planning); err == nil {
		t.Fatal("plan gate should fail with no artifact")
	}

	// Present non-empty artifact -> pass.
	tk.Artifacts.Plan = writeArtifact(t, env, "docs/tickets/T1.plan.md", "# plan\nstuff\n")
	if err := env.Verify(tk, Planning); err != nil {
		t.Fatalf("plan gate should pass: %v", err)
	}

	// Empty artifact -> fail.
	tk.Artifacts.Plan = writeArtifact(t, env, "docs/tickets/T1.empty.md", "")
	if err := env.Verify(tk, Planning); err == nil {
		t.Fatal("plan gate should fail on empty artifact")
	}
}

func TestExecuteGate(t *testing.T) {
	// No worktree recorded -> fail.
	env := testEnv(t, true, true)
	if err := env.Verify(&Ticket{ID: "T2", Phase: Executing}, Executing); err == nil {
		t.Fatal("execute gate should fail without a worktree")
	}

	tk := &Ticket{ID: "T2", Phase: Executing, Worktree: "/tmp/wt"}

	// No changes -> fail.
	env = testEnv(t, true, false)
	if err := env.Verify(tk, Executing); err == nil {
		t.Fatal("execute gate should fail with no changes")
	}

	// Changes but red tests -> fail.
	env = testEnv(t, false, true)
	if err := env.Verify(tk, Executing); err == nil {
		t.Fatal("execute gate should fail with red tests")
	}

	// Changes and green tests -> pass.
	env = testEnv(t, true, true)
	if err := env.Verify(tk, Executing); err != nil {
		t.Fatalf("execute gate should pass: %v", err)
	}
}

func TestReviewGate(t *testing.T) {
	env := testEnv(t, true, true)
	tk := &Ticket{ID: "T3", Phase: Reviewing, Worktree: "/tmp/wt"}

	// No review artifact -> fail.
	if err := env.Verify(tk, Reviewing); err == nil {
		t.Fatal("review gate should fail with no review file")
	}

	// Review file without PASS verdict -> fail.
	tk.Artifacts.Review = writeArtifact(t, env, "docs/tickets/T3.review.md", "looks fine\nVERDICT: FAIL\n")
	if err := env.Verify(tk, Reviewing); err == nil {
		t.Fatal("review gate should fail without VERDICT: PASS")
	}

	// PASS verdict + green tests -> pass.
	tk.Artifacts.Review = writeArtifact(t, env, "docs/tickets/T3.pass.md", "great work\nVERDICT: PASS\n")
	if err := env.Verify(tk, Reviewing); err != nil {
		t.Fatalf("review gate should pass: %v", err)
	}

	// The review gate does NOT run the suite: the reviewer changes no code, so
	// the worktree is byte-identical to what the execute gate already proved
	// green; re-running it would be duplication, and the post-merge guard is the
	// integration net. So a red RunTests must NOT affect the review gate.
	env.RunTests = func(string) error { return &gateErr{"red"} }
	if err := env.Verify(tk, Reviewing); err != nil {
		t.Fatalf("review gate must not depend on tests; should still pass with PASS verdict: %v", err)
	}
}

func TestMermaid(t *testing.T) {
	s := storeFrom(
		&Ticket{ID: "T1", Title: "first", Phase: Done},
		&Ticket{ID: "T2", Title: "second", Phase: Executing, Deps: []string{"T1"}},
	)
	out := s.Mermaid()
	for _, want := range []string{"flowchart TD", "T1 --> T2", ":::done", ":::executing", "classDef executing"} {
		if !strings.Contains(out, want) {
			t.Errorf("Mermaid output missing %q\n%s", want, out)
		}
	}
}

// TestLoadSaveRoundTrip exercises the YAML store against a temp directory.
func TestLoadSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	orig := &Ticket{ID: "T9", Title: "round trip", Deps: []string{"T1"}, Phase: Pending, DoD: []string{"a", "b"}}
	if err := s.Add(orig); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(orig); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reloaded.Get("T9")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "round trip" || len(got.Deps) != 1 || got.Deps[0] != "T1" || len(got.DoD) != 2 {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

func TestModelFor(t *testing.T) {
	tk := &Ticket{ID: "T9", Models: map[string]string{"review": "sonnet"}}
	if got := tk.ModelFor(Reviewing); got != "sonnet" {
		t.Errorf("ModelFor(Reviewing) = %q, want sonnet", got)
	}
	if got := tk.ModelFor(Planning); got != "" {
		t.Errorf("ModelFor(Planning) = %q, want empty (frontmatter default)", got)
	}
	// A ticket with no Models map must not panic and yields no override.
	if got := (&Ticket{ID: "T0"}).ModelFor(Executing); got != "" {
		t.Errorf("ModelFor on nil Models = %q, want empty", got)
	}
}

func TestModelKeyForPhase(t *testing.T) {
	cases := map[Phase]string{
		Planning: "plan", Executing: "exec", Reviewing: "review",
		Pending: "", Done: "", Blocked: "",
	}
	for p, want := range cases {
		if got := ModelKeyForPhase(p); got != want {
			t.Errorf("ModelKeyForPhase(%s) = %q, want %q", p, got, want)
		}
	}
}

func TestValidModel(t *testing.T) {
	for _, ok := range []string{"opus", "sonnet", "haiku", "fable"} {
		if !ValidModel(ok) {
			t.Errorf("ValidModel(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "gpt-4", "opus-4.8", "Sonnet"} {
		if ValidModel(bad) {
			t.Errorf("ValidModel(%q) = true, want false", bad)
		}
	}
}

// TestModelsRoundTrip pins that a per-phase model survives a YAML save/reload and
// that a ticket without overrides serializes no `models:` key (no churn).
func TestModelsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	withModel := &Ticket{ID: "M1", Phase: Pending, Models: map[string]string{"review": "sonnet"}}
	plain := &Ticket{ID: "M2", Phase: Pending}
	for _, tk := range []*Ticket{withModel, plain} {
		if err := s.Add(tk); err != nil {
			t.Fatal(err)
		}
		if err := s.Save(tk); err != nil {
			t.Fatal(err)
		}
	}
	reloaded, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reloaded.Get("M1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ModelFor(Reviewing) != "sonnet" {
		t.Fatalf("M1 review override lost on round trip: %+v", got.Models)
	}
	// The plain ticket must not have gained a `models:` key on disk.
	raw, err := os.ReadFile(filepath.Join(dir, "M2.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "models:") {
		t.Fatalf("plain ticket churned a models: key:\n%s", raw)
	}
}
