# Agent orchestration: plan → execute → review, herded + gated

A fleet of agents takes each **ticket** through **plan → execute → review**, with a
**herder** routing the work and a **deterministic enforcer** that guarantees no agent
skips a step. This doc is the operator's guide.

## Mental model

- **Tickets** are nodes in a DAG (`tickets/*.yml`). Edges are `deps`. A ticket is
  *ready* only when every dependency is `done` (topological order).
- Each ticket walks a sub-pipeline: `pending → planning → executing → reviewing → done`.
- Three **worker agents** own one phase each: `ticket-planner`, `ticket-executor`,
  `ticket-reviewer` (`.claude/agents/`).
- The **herder** (`/herd`) reads the ready-set, hands tickets to workers in per-ticket
  git worktrees, advances the pipeline as gates pass, and surfaces finished tickets for
  **your** merge approval. It never merges on its own.
- The **enforcer is not an agent** — it's the `ticket` CLI (pure Go, `.claude/orchestrator/`)
  plus a `SubagentStop` hook. Every "did the agent do the step?" is a file check or a
  process exit code, so it's deterministic and reproducible.

### What gates each transition

`ticket advance <id>` refuses to move a ticket forward unless the current phase's gate
passes:

| Leaving      | Gate (all must hold)                                                            |
|--------------|---------------------------------------------------------------------------------|
| `planning`   | `artifacts.plan` file exists and is non-empty                                   |
| `executing`  | non-empty `git diff` vs base **and** `go test ./...` exits 0 (run in worktree)  |
| `reviewing`  | `artifacts.review` has a `VERDICT: PASS` line **and** tests still green          |

`dod` checklists are advisory (agent-checked), never part of the gate. **The hard
guarantee is `advance` itself** — a worker literally cannot progress a skipped step. The
`SubagentStop` hook (`.claude/hooks/gate.sh`) is a strict accelerator: it blocks an agent
from *stopping* mid-step (when its `.current-ticket` marker is still present and the gate
fails), forcing it to finish in-session instead of bouncing back to the herder.

## The pieces

| Path | Role |
|------|------|
| `tickets/*.yml` + `tickets/README.md` | the DAG store (schema documented there) |
| `.claude/orchestrator/` | Go module: ticket model, ready-set, gates, Mermaid (`go test ./...` here for unit tests) |
| `.claude/bin/ticket` | stable CLI entrypoint (auto-builds; pins the store to this checkout) |
| `.claude/agents/ticket-{planner,executor,reviewer}.md` | the three workers |
| `.claude/skills/herd/SKILL.md` | the herder loop (`/herd`) |
| `.claude/hooks/gate.sh` + `.claude/settings.json` | the `SubagentStop` enforcer hook |
| `.claude/hooks/context-warn.sh` | `PostToolUse` hook: warns when herder context gets high (default 250k) |
| `docs/tickets/*.{plan,review}.md` | per-ticket plan + review artifacts |
| `docs/tickets.mmd` | generated Mermaid DAG — your live watch view |

## Daily use

```bash
T=.claude/bin/ticket

$T list                 # all tickets + phase
$T ready                # what's dispatchable right now
$T graph                # refresh docs/tickets.mmd (Mermaid)
$T graph -f dot         # also/instead emit docs/tickets.dot (Graphviz)
$T validate             # deps exist + acyclic
$T show C3              # one ticket
```

### Viewing the graph

`ticket graph` writes a text graph; pick whichever renderer you like:

- **Graphviz (local, no network)** — you have `dot` installed:
  ```bash
  $T graph -f dot && dot -Tsvg docs/tickets.dot -o docs/tickets.svg
  ```
  then open `docs/tickets.svg` (or `-Tpng` for an image). Nodes are colored by phase
  (grey=pending, blue=planning, yellow=executing, orange=reviewing, green=done, red=blocked).
- **Mermaid, zero install** — paste `docs/tickets.mmd` into <https://mermaid.live>.
- **Mermaid in your IDE** — install a Mermaid preview extension (e.g. VS Code
  "Markdown Preview Mermaid Support" or a `.mmd` previewer) and open `docs/tickets.mmd`.
- **Mermaid → image via npx** (first run pulls the package):
  ```bash
  npx -p @mermaid-js/mermaid-cli mmdc -i docs/tickets.mmd -o docs/tickets.svg
  ```

The herder regenerates the graph every loop, so re-render (or refresh your preview) to
watch phases change color as tickets move.

**Add a ticket** (edges make the DAG; order doesn't matter — `validate` catches typos):

```bash
$T new H1 --title "new thing" --deps C2,C4 --source docs/workspace_plan.md#h1
$T validate
```

**Run the fleet:** invoke `/herd` (optionally with a concurrency cap, e.g. `/herd 2`).
It announces the DAG, dispatches the ready roots, and walks each ticket through the
phases. When a ticket reaches `done` it pauses and shows you the diff + review for
**merge approval**.

**Recover a blocked ticket:** the herder marks a ticket `blocked` when a gate keeps
refusing. Inspect with `$T verify <id>` (prints exactly what's missing), then either
re-dispatch by setting it back (`$T claim <id> executing -w <worktree>`) or fix the
underlying issue and let the next `/herd` pick it up.

## One-time setup: permissions

The hook and CLI work out of the box. To cut down on permission prompts while the fleet
runs, add these to `.claude/settings.local.json` under `permissions.allow` (kept here so
*you* opt in — the orchestrator does not widen its own permissions):

```json
"Bash(go build *)",
"Bash(go test *)",
"Bash(.claude/bin/ticket *)",
"Bash(.claude/bin/ticket)",
"Bash(git diff *)",
"Bash(git -C *)",
"Bash(git status*)",
"Bash(git rev-list *)",
"Bash(git rev-parse *)",
"Bash(git log *)"
```

`git worktree *`, `git checkout *`, `git add *`, and `git commit -q *` are already
allowed.

## Context-high warning

A long herder run accumulates context fast (it monitors many agents). `context-warn.sh`
(a `PostToolUse` hook) reads the latest turn's token usage from the session transcript
and surfaces a non-blocking warning once context crosses a threshold, re-warning every
+25k after that:

> ⚠️ Herder context ~251k tokens (≥ 250k). Consider /compact, finishing the current wave…

- **Threshold:** default 250k; override with `BIBICTL_CONTEXT_WARN` (e.g.
  `export BIBICTL_CONTEXT_WARN=200000`).
- **Scope:** only fires while a `.herding` flag is present (the `/herd` skill sets it on
  start, clears it on stop). To warn in *every* session, delete the `.herding` guard at
  the top of `context-warn.sh`.

## Design notes (refinements over the original plan)

- **Manual, persistent worktrees** (not the Agent tool's auto-isolation): the herder runs
  `git worktree add ../bibicontrol-wt/<id> -b ticket/<id> <base>` so the *same* worktree
  is shared across a ticket's three phases (the reviewer must see the executor's code).
  Auto-isolation worktrees are ephemeral/per-agent and would discard the work.
- **One worktree per ticket; plan/review artifacts written to the main repo.** Code
  changes live in the worktree (keeps the execute gate's diff pure code); the YAML state
  files stay single-source in the main checkout. Workers always call the *main* repo's
  `.claude/bin/ticket` (absolute) so state never forks per worktree.
- **`go test ./...` is the whole-suite gate.** Some integration tests need
  `BIBITES_SAVEFILES_DIR` (and the shared `GOMODCACHE`/`GOCACHE`); the gate sets the
  caches, and the executor/reviewer agents run with the same env. If the suite needs the
  savefiles dir, export it before `/herd`.
- The orchestrator is a **nested Go module**, so `go test ./...` in the product repo does
  **not** see it (and vice-versa) — the gate measures only product code.

## Verification

```bash
# CLI unit tests (ready-set + every gate, with injected fakes):
cd .claude/orchestrator && GOMODCACHE=/tmp/bibicontrol-go-mod GOCACHE=/tmp/bibicontrol-go-build go test ./...

# DAG readiness honors deps (only dependency-free roots are ready):
.claude/bin/ticket ready          # -> A1, B1, B3

# The DAG is well-formed:
.claude/bin/ticket validate

# The enforcer hook blocks an incomplete stop (exit 2):
d=$(mktemp -d); echo "A1 planning" > "$d/.current-ticket"
printf '{"cwd":"%s"}' "$d" | .claude/hooks/gate.sh; echo "exit=$?"   # -> 2
rm -rf "$d"
```
