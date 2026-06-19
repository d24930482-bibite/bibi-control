#!/usr/bin/env bash
# SubagentStop hook — the strict, in-session arm of the deterministic enforcer.
#
# Contract with the worker agents:
#   - on start, an agent writes "<id> <phase>" to "$WORKTREE/.current-ticket"
#   - on a SUCCESSFUL `ticket advance`/`reject`, the agent deletes that marker
#
# So when a subagent tries to stop:
#   - no marker in cwd            -> nothing to enforce here (allow)
#   - marker present, gate passes -> the step is demonstrably done (allow)
#   - marker present, gate fails  -> the agent is quitting with its step
#                                    incomplete -> BLOCK (exit 2) and tell it why
#
# This is the accelerator, not the guarantee: the hard guarantee is that
# `ticket advance` itself runs the gate and refuses to move a ticket forward
# unless it passes. The herder re-dispatches anything this hook can't see.
set -uo pipefail

input="$(cat)"

# Best-effort extraction of the stopping agent's cwd from the hook payload.
cwd="$(printf '%s' "$input" | sed -n 's/.*"cwd"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)"
[ -z "$cwd" ] && cwd="$PWD"

marker="$cwd/.current-ticket"
[ -f "$marker" ] || exit 0   # no claim staked in this dir; let the agent stop

read -r id phase _ < "$marker" || exit 0
[ -z "${id:-}" ] && exit 0

# This hook lives at <main-repo>/.claude/hooks/gate.sh; the CLI is a sibling.
HOOK_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TICKET="$HOOK_DIR/../bin/ticket"

if out="$("$TICKET" verify "$id" "$phase" 2>&1)"; then
  exit 0   # the phase gate passes — the step is complete
fi

# Gate failed and the agent is trying to stop. Block and feed back the reason.
{
  echo "BLOCKED: ticket $id has not completed its '$phase' step."
  echo "$out"
  echo "Complete the step (write the required artifact / get the suite green), then run"
  echo "  ticket advance $id        (or 'ticket reject $id -m ...' if you are reviewing)"
  echo "and remove $marker. You cannot stop until the gate passes."
} >&2
exit 2
