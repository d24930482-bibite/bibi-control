#!/usr/bin/env bash
# PostToolUse hook — warns when the herder session's context grows large.
#
# Context size isn't handed to hooks directly, but the session transcript records
# each turn's token usage. Current context ~= input_tokens + cache_creation +
# cache_read of the latest assistant turn. When that crosses the threshold we
# surface a non-blocking warning (and re-warn every +25k after that).
#
# Scoped to herder runs: the /herd skill drops a `.herding` flag in the project
# dir on start and removes it on exit, so this only fires while you're herding.
# To make it global, delete the `.herding` guard below.
set -uo pipefail

PROJ="${CLAUDE_PROJECT_DIR:-$PWD}"
[ -f "$PROJ/.herding" ] || exit 0        # only warn during a herder run

input="$(cat)"
field() { printf '%s' "$input" | sed -n "s/.*\"$1\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p" | head -1; }
transcript="$(field transcript_path)"
sid="$(field session_id)"
[ -f "$transcript" ] || exit 0

# Latest usage object from the tail of the transcript.
line="$(tail -n 800 "$transcript" 2>/dev/null | grep -o '"usage":{[^}]*}' | tail -1)"
[ -z "$line" ] && exit 0
num() { printf '%s' "$line" | grep -oP "\"$1\":\\s*\\K[0-9]+" | head -1; }
total=$(( $(num input_tokens || echo 0) + $(num cache_creation_input_tokens || echo 0) + $(num cache_read_input_tokens || echo 0) ))

threshold="${BIBICTL_CONTEXT_WARN:-250000}"
step=25000
[ "$total" -ge "$threshold" ] || exit 0

# Throttle: warn once per 25k bucket, per session.
state="/tmp/bibicontrol-ctxwarn-${sid:-default}"
last="$(cat "$state" 2>/dev/null || echo 0)"
bucket=$(( total / step ))
[ "$bucket" -gt "$last" ] || exit 0
echo "$bucket" > "$state"

printf '{"systemMessage":"⚠️ Herder context ~%dk tokens (≥ %dk). Consider /compact, finishing the current wave before dispatching more, or handing off."}\n' \
  "$(( total / 1000 ))" "$(( threshold / 1000 ))"
exit 0
