// Command ticket is the deterministic engine of the bibicontrol agent
// orchestrator. The herder uses `ready`/`graph`/`claim` to drive the DAG; the
// worker agents finish a phase by calling `advance`, which refuses to move a
// ticket forward unless that phase's gate passes; the SubagentStop hook calls
// `verify` to block an agent that tries to stop with its step incomplete.
//
//	ticket list                      list every ticket and its phase
//	ticket show <id>                 print one ticket
//	ticket ready                     ids dispatchable now (deps done, pending)
//	ticket graph [-o file]           emit the Mermaid DAG (default docs/tickets.mmd)
//	ticket validate                  check deps exist and the DAG is acyclic
//	ticket new <id> [flags]          scaffold a new ticket file
//	ticket claim <id> <phase> [-w p] move a ticket into a phase (herder)
//	ticket set <id> [flags]          set artifacts.plan/review, worktree, notes
//	ticket verify <id> [phase]       gate check only (no state change); exit!=0 on fail
//	ticket advance <id>              verify current phase, then move to the next
//	ticket reject <id> [-m notes]    send a reviewing ticket back to executing
//	ticket model <id> <phase>        print the per-phase model override (blank if none)
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/asemones/bibicontrol-orchestrator/internal/ticket"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	if err := run(cmd, args); err != nil {
		fmt.Fprintln(os.Stderr, "ticket: "+err.Error())
		os.Exit(1)
	}
}

func run(cmd string, args []string) error {
	switch cmd {
	case "list":
		return cmdList()
	case "show":
		return cmdShow(args)
	case "ready":
		return cmdReady()
	case "graph":
		return cmdGraph(args)
	case "validate":
		return cmdValidate()
	case "new":
		return cmdNew(args)
	case "claim":
		return cmdClaim(args)
	case "set":
		return cmdSet(args)
	case "verify":
		return cmdVerify(args)
	case "advance":
		return cmdAdvance(args)
	case "reject":
		return cmdReject(args)
	case "model":
		return cmdModel(args)
	case "-h", "--help", "help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", cmd)
	}
}

// --- environment / store helpers ---------------------------------------------

func ticketsDir() string {
	if d := os.Getenv("BIBICTL_TICKETS"); d != "" {
		return d
	}
	return "tickets"
}

func baseBranch() string {
	if b := os.Getenv("BIBICTL_BASE"); b != "" {
		return b
	}
	return "main"
}

// repoRoot is the parent of the (absolute) tickets dir; artifact paths resolve
// against it.
func repoRoot() (string, error) {
	abs, err := filepath.Abs(ticketsDir())
	if err != nil {
		return "", err
	}
	return filepath.Dir(abs), nil
}

func load() (*ticket.Store, error) {
	return ticket.Load(ticketsDir())
}

func gateEnv() (ticket.GateEnv, error) {
	root, err := repoRoot()
	if err != nil {
		return ticket.GateEnv{}, err
	}
	return ticket.DefaultGateEnv(root, baseBranch()), nil
}

// --- commands ----------------------------------------------------------------

func cmdList() error {
	s, err := load()
	if err != nil {
		return err
	}
	for _, t := range s.All() {
		deps := "-"
		if len(t.Deps) > 0 {
			deps = strings.Join(t.Deps, ",")
		}
		fmt.Printf("%-8s %-10s deps=%-14s %s\n", t.ID, t.Phase, deps, t.Title)
	}
	return nil
}

func cmdShow(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: ticket show <id>")
	}
	s, err := load()
	if err != nil {
		return err
	}
	t, err := s.Get(args[0])
	if err != nil {
		return err
	}
	fmt.Printf("id:        %s\n", t.ID)
	fmt.Printf("title:     %s\n", t.Title)
	fmt.Printf("phase:     %s\n", t.Phase)
	fmt.Printf("deps:      %s\n", strings.Join(t.Deps, ", "))
	fmt.Printf("worktree:  %s\n", t.Worktree)
	fmt.Printf("plan:      %s\n", t.Artifacts.Plan)
	fmt.Printf("review:    %s\n", t.Artifacts.Review)
	fmt.Printf("source:    %s\n", t.Source)
	if t.Notes != "" {
		fmt.Printf("notes:     %s\n", t.Notes)
	}
	return nil
}

func cmdReady() error {
	s, err := load()
	if err != nil {
		return err
	}
	for _, t := range s.Ready() {
		fmt.Println(t.ID)
	}
	return nil
}

func cmdGraph(args []string) error {
	fs := flag.NewFlagSet("graph", flag.ContinueOnError)
	format := fs.String("f", "mermaid", "output format: mermaid | dot")
	out := fs.String("o", "", "output file (default docs/tickets.{mmd,dot}; - for stdout)")
	if err := fs.Parse(reorder(args)); err != nil {
		return err
	}
	s, err := load()
	if err != nil {
		return err
	}
	var content, defOut string
	switch *format {
	case "mermaid":
		content, defOut = s.Mermaid(), "docs/tickets.mmd"
	case "dot":
		content, defOut = s.Dot(), "docs/tickets.dot"
	default:
		return fmt.Errorf("unknown format %q (want mermaid or dot)", *format)
	}
	target := *out
	if target == "" {
		target = defOut
	}
	if target == "-" {
		fmt.Print(content)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", target)
	return nil
}

func cmdValidate() error {
	s, err := load()
	if err != nil {
		return err
	}
	probs := s.Validate()
	if len(probs) == 0 {
		fmt.Println("ok: DAG is valid")
		return nil
	}
	for _, p := range probs {
		fmt.Fprintln(os.Stderr, "  - "+p.Error())
	}
	return fmt.Errorf("%d problem(s) found", len(probs))
}

func cmdNew(args []string) error {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	title := fs.String("title", "", "ticket title")
	deps := fs.String("deps", "", "comma-separated dependency ids")
	source := fs.String("source", "", "link back to the prose ticket")
	if err := fs.Parse(reorder(args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: ticket new <id> --title ... [--deps a,b] [--source ...]")
	}
	s, err := load()
	if err != nil {
		return err
	}
	t := &ticket.Ticket{
		ID:     fs.Arg(0),
		Title:  *title,
		Deps:   splitCSV(*deps),
		Phase:  ticket.Pending,
		Source: *source,
		DoD:    []string{},
	}
	if err := s.Add(t); err != nil {
		return err
	}
	if err := s.Save(t); err != nil {
		return err
	}
	fmt.Printf("created %s\n", t.ID)
	return nil
}

func cmdClaim(args []string) error {
	fs := flag.NewFlagSet("claim", flag.ContinueOnError)
	wt := fs.String("w", "", "worktree path to record")
	if err := fs.Parse(reorder(args)); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: ticket claim <id> <phase> [-w worktree]")
	}
	id, phase := fs.Arg(0), fs.Arg(1)
	if !ticket.ValidPhase(phase) {
		return fmt.Errorf("invalid phase %q", phase)
	}
	s, err := load()
	if err != nil {
		return err
	}
	t, err := s.Get(id)
	if err != nil {
		return err
	}
	t.Phase = ticket.Phase(phase)
	if *wt != "" {
		t.Worktree = *wt
	}
	if err := s.Save(t); err != nil {
		return err
	}
	fmt.Printf("%s -> %s\n", id, phase)
	return nil
}

func cmdSet(args []string) error {
	fs := flag.NewFlagSet("set", flag.ContinueOnError)
	plan := fs.String("plan", "", "artifacts.plan path")
	review := fs.String("review", "", "artifacts.review path")
	wt := fs.String("worktree", "", "worktree path")
	notes := fs.String("notes", "", "notes")
	mPlan := fs.String("model-plan", "", "model override for the planning phase (opus|sonnet|haiku|fable)")
	mExec := fs.String("model-exec", "", "model override for the executing phase (opus|sonnet|haiku|fable)")
	mReview := fs.String("model-review", "", "model override for the reviewing phase (opus|sonnet|haiku|fable)")
	if err := fs.Parse(reorder(args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: ticket set <id> [--plan p] [--review p] [--worktree p] [--notes n] [--model-plan m] [--model-exec m] [--model-review m]")
	}
	s, err := load()
	if err != nil {
		return err
	}
	t, err := s.Get(fs.Arg(0))
	if err != nil {
		return err
	}
	if *plan != "" {
		t.Artifacts.Plan = *plan
	}
	if *review != "" {
		t.Artifacts.Review = *review
	}
	if *wt != "" {
		t.Worktree = *wt
	}
	if *notes != "" {
		t.Notes = *notes
	}
	for _, m := range []struct{ key, val string }{
		{"plan", *mPlan}, {"exec", *mExec}, {"review", *mReview},
	} {
		if m.val == "" {
			continue
		}
		if !ticket.ValidModel(m.val) {
			return fmt.Errorf("invalid model %q for %s (want opus|sonnet|haiku|fable)", m.val, m.key)
		}
		if t.Models == nil {
			t.Models = map[string]string{}
		}
		t.Models[m.key] = m.val
	}
	if err := s.Save(t); err != nil {
		return err
	}
	fmt.Printf("updated %s\n", t.ID)
	return nil
}

// cmdModel prints the per-phase model override for a ticket (blank line if
// none), so the herder can pass it straight to the Agent tool's `model` arg.
func cmdModel(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: ticket model <id> <planning|executing|reviewing>")
	}
	if !ticket.ValidPhase(args[1]) {
		return fmt.Errorf("invalid phase %q", args[1])
	}
	s, err := load()
	if err != nil {
		return err
	}
	t, err := s.Get(args[0])
	if err != nil {
		return err
	}
	fmt.Println(t.ModelFor(ticket.Phase(args[1])))
	return nil
}

func cmdVerify(args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return fmt.Errorf("usage: ticket verify <id> [phase]")
	}
	s, err := load()
	if err != nil {
		return err
	}
	t, err := s.Get(args[0])
	if err != nil {
		return err
	}
	phase := t.Phase
	if len(args) == 2 {
		if !ticket.ValidPhase(args[1]) {
			return fmt.Errorf("invalid phase %q", args[1])
		}
		phase = ticket.Phase(args[1])
	}
	env, err := gateEnv()
	if err != nil {
		return err
	}
	if err := env.Verify(t, phase); err != nil {
		return err
	}
	fmt.Printf("ok: %s passes the %s gate\n", t.ID, phase)
	return nil
}

func cmdAdvance(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: ticket advance <id>")
	}
	s, err := load()
	if err != nil {
		return err
	}
	t, err := s.Get(args[0])
	if err != nil {
		return err
	}
	next, ok := ticket.NextPhase(t.Phase)
	if !ok {
		return fmt.Errorf("ticket %s is in phase %q, which cannot be advanced", t.ID, t.Phase)
	}
	env, err := gateEnv()
	if err != nil {
		return err
	}
	if err := env.Verify(t, t.Phase); err != nil {
		return err // the gate refused; ticket stays put
	}
	prev := t.Phase
	t.Phase = next
	if err := s.Save(t); err != nil {
		return err
	}
	fmt.Printf("%s: %s -> %s\n", t.ID, prev, next)
	return nil
}

func cmdReject(args []string) error {
	fs := flag.NewFlagSet("reject", flag.ContinueOnError)
	notes := fs.String("m", "", "review feedback for the executor")
	if err := fs.Parse(reorder(args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: ticket reject <id> [-m notes]")
	}
	s, err := load()
	if err != nil {
		return err
	}
	t, err := s.Get(fs.Arg(0))
	if err != nil {
		return err
	}
	if t.Phase != ticket.Reviewing {
		return fmt.Errorf("can only reject a ticket in reviewing; %s is %s", t.ID, t.Phase)
	}
	t.Phase = ticket.Executing
	if *notes != "" {
		t.Notes = *notes
	}
	if err := s.Save(t); err != nil {
		return err
	}
	fmt.Printf("%s: reviewing -> executing (sent back)\n", t.ID)
	return nil
}

// reorder moves flag tokens ahead of positional ones so commands accept flags
// in any position (e.g. `ticket new A1 --title x`). Every flag this CLI defines
// takes a value, so a flag token without `=` consumes the following token.
func reorder(args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		pos = append(pos, a)
	}
	return append(flags, pos...)
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return []string{}
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func usage() {
	fmt.Fprint(os.Stderr, `ticket — deterministic DAG/gate engine for the agent orchestrator

  ticket list                      list every ticket and its phase
  ticket show <id>                 print one ticket
  ticket ready                     ids dispatchable now (deps done, pending)
  ticket graph [-o file]           emit the Mermaid DAG (default docs/tickets.mmd)
  ticket validate                  check deps exist and the DAG is acyclic
  ticket new <id> [flags]          scaffold a new ticket file
  ticket claim <id> <phase> [-w p] move a ticket into a phase (herder)
  ticket set <id> [flags]          set artifacts.plan/review, worktree, notes,
                                   and per-phase model (--model-plan/-exec/-review)
  ticket verify <id> [phase]       gate check only; exit!=0 on failure
  ticket advance <id>              verify current phase, then move to the next
  ticket reject <id> [-m notes]    send a reviewing ticket back to executing
  ticket model <id> <phase>        print the per-phase model override (blank if none)

env: BIBICTL_TICKETS (tickets dir, default ./tickets), BIBICTL_BASE (base branch, default main)
`)
}
