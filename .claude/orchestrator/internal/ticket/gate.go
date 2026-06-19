package ticket

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// verdictPass matches the line a reviewer must write to pass the review gate.
var verdictPass = regexp.MustCompile(`(?m)^VERDICT:\s*PASS\b`)

// GateEnv holds the (injectable) machinery the gates use to reach the outside
// world. The CLI wires the real implementations; tests inject fakes so gate
// logic is unit-testable without shelling out to git or `go test`.
type GateEnv struct {
	RepoRoot   string                                  // main repo root; resolves relative artifact paths
	Base       string                                  // base branch the executor diffs against
	RunTests   func(dir string) error                  // nil => suite green
	HasChanges func(worktree, base string) (bool, error) // true => non-empty diff vs base
	ReadFile   func(path string) ([]byte, error)
}

// DefaultGateEnv returns a GateEnv that runs the real `go test ./...` and git.
func DefaultGateEnv(repoRoot, base string) GateEnv {
	return GateEnv{
		RepoRoot:   repoRoot,
		Base:       base,
		RunTests:   runGoTest,
		HasChanges: gitHasChanges,
		ReadFile:   os.ReadFile,
	}
}

// NextPhase returns the phase a ticket moves to after passing the gate for its
// current phase. ok is false when the current phase is not advanceable.
func NextPhase(p Phase) (next Phase, ok bool) {
	switch p {
	case Planning:
		return Executing, true
	case Executing:
		return Reviewing, true
	case Reviewing:
		return Done, true
	}
	return "", false
}

// Verify runs the deterministic gate for *leaving* the given phase. It returns
// nil when every required step for that phase is demonstrably complete, or an
// error naming exactly what is missing. This is the whole enforcement contract.
func (env GateEnv) Verify(t *Ticket, phase Phase) error {
	switch phase {
	case Planning:
		return env.requireArtifact("plan", t.Artifacts.Plan)

	case Executing:
		if t.Worktree == "" {
			return fmt.Errorf("execute gate: ticket %s has no worktree recorded", t.ID)
		}
		changed, err := env.HasChanges(t.Worktree, env.Base)
		if err != nil {
			return fmt.Errorf("execute gate: checking diff: %w", err)
		}
		if !changed {
			return fmt.Errorf("execute gate: no code changes in %s vs %s", t.Worktree, env.Base)
		}
		if err := env.RunTests(t.Worktree); err != nil {
			return fmt.Errorf("execute gate: tests not green: %w", err)
		}
		return nil

	case Reviewing:
		if err := env.requireArtifact("review", t.Artifacts.Review); err != nil {
			return err
		}
		raw, err := env.ReadFile(env.resolve(t.Artifacts.Review))
		if err != nil {
			return fmt.Errorf("review gate: reading review file: %w", err)
		}
		if !verdictPass.Match(raw) {
			return fmt.Errorf("review gate: %s has no `VERDICT: PASS` line", t.Artifacts.Review)
		}
		// No full-suite run here on purpose. The reviewer changes no code, so the
		// worktree is byte-identical to what the EXECUTE gate already proved green
		// (gate.go: Executing -> RunTests). Re-running `go test ./...` would be
		// pure duplication of that run seconds later. The reviewer's own value is
		// the targeted checks (-race, perf, mutation, diff review), and the
		// herder's post-merge guard re-runs the whole suite on the merged main as
		// the final integration net. So the review gate enforces only artifact +
		// PASS verdict.
		return nil
	}
	return fmt.Errorf("phase %q has no gate", phase)
}

// requireArtifact fails unless the named artifact path is set and points at a
// non-empty file.
func (env GateEnv) requireArtifact(kind, rel string) error {
	if rel == "" {
		return fmt.Errorf("%s gate: artifacts.%s is not set", kind, kind)
	}
	info, err := os.Stat(env.resolve(rel))
	if err != nil {
		return fmt.Errorf("%s gate: artifact %s missing: %w", kind, rel, err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("%s gate: artifact %s is empty", kind, rel)
	}
	return nil
}

// resolve turns a repo-relative artifact path into an absolute one.
func (env GateEnv) resolve(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(env.RepoRoot, p)
}

// runGoTest runs the project's whole-suite test gate in dir, inheriting the
// caller's environment but defaulting the shared build/mod caches the repo uses.
// Passes -timeout=20m: the workspace integration package runs ~400-630s, right
// at Go's default 600s per-package timeout, so the default spuriously fails the
// gate. This is the single authoritative full-suite run per execute gate.
func runGoTest(dir string) error {
	cmd := exec.Command("go", "test", "-timeout=20m", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GOMODCACHE="+envOr("GOMODCACHE", "/tmp/bibicontrol-go-mod"),
		"GOCACHE="+envOr("GOCACHE", "/tmp/bibicontrol-go-build"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v\n%s", err, tail(out, 40))
	}
	return nil
}

// gitHasChanges reports whether worktree differs from base, counting both
// uncommitted edits and commits ahead of base.
func gitHasChanges(worktree, base string) (bool, error) {
	// Uncommitted changes?
	out, err := exec.Command("git", "-C", worktree, "status", "--porcelain").Output()
	if err != nil {
		return false, fmt.Errorf("git status: %w", err)
	}
	if len(out) > 0 {
		return true, nil
	}
	// Commits ahead of base?
	out, err = exec.Command("git", "-C", worktree, "rev-list", "--count", base+"..HEAD").Output()
	if err != nil {
		// base may not exist as a ref in this worktree; treat as "can't prove changes".
		return false, fmt.Errorf("git rev-list %s..HEAD: %w", base, err)
	}
	return len(out) > 0 && string(out[0]) != "0", nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// tail returns the last n lines of b, to keep failure output readable.
func tail(b []byte, n int) string {
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
