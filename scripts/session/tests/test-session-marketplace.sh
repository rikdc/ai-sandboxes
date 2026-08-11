#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../../.."

if ! command -v msb >/dev/null 2>&1; then
  echo 'skip: msb not installed' >&2
  exit 0
fi

if ! docker image inspect ai-sandboxes-claude:local >/dev/null 2>&1; then
  echo 'skip: ai-sandboxes-claude:local not built (run ./scripts/build)' >&2
  exit 0
fi

session_tag=''
cleanup() {
  if test -n "$session_tag"; then
    docker image rm -f "$session_tag" >/dev/null 2>&1 || true
    msb image remove "$session_tag" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

session_tag=$(CLAUDE_MSB_BUILD_EGRESS=1 scripts/session/resolve-image.sh scripts/session/fixtures/valid/claude-marketplaces.json)
scripts/session/load-image.sh "$session_tag"

plugin_output=$(msb run --pull never --no-tty --user node --security restricted -e HOME=/tmp/claude-session-marketplace-test "$session_tag" -- claude plugin list 2>&1)
printf '%s\n' "$plugin_output" | awk '
  /dev-skills@ai-skills/ { found = 1; next }
  found && /Status: ✔ enabled/ { enabled = 1; exit }
  END { exit !(found && enabled) }
'

echo ok
