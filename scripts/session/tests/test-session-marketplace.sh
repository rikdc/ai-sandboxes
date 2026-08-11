#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../../.."

if ! docker image inspect ai-sandboxes-claude:local >/dev/null 2>&1; then
  echo 'skip: ai-sandboxes-claude:local not built (run ./scripts/build)' >&2
  exit 0
fi

session_tag=''
cleanup() {
  if test -n "$session_tag"; then
    docker image rm -f "$session_tag" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

session_tag=$(CLAUDE_MSB_BUILD_EGRESS=1 scripts/session/resolve-image.sh scripts/session/fixtures/valid/claude-marketplaces.json)

# Marketplace registration and code reachability are two different failures:
# a marketplace can show as enabled in `claude plugin list` (settings.json
# merge worked) while `claude plugin marketplace list` shows nothing at all
# (the marketplace's code isn't reachable from CLAUDE_CODE_PLUGIN_CACHE_DIR
# at runtime). Confirmed empirically against an earlier, broken version of
# this mechanism, which checking only `claude plugin list` did not catch:
# settings.json had a correct extraKnownMarketplaces entry and the
# marketplace's code was fully present on disk, yet `claude plugin
# marketplace list` reported "No marketplaces configured" because it was
# cloned into a cache directory nothing pointed at runtime. Check both, and
# run via plain `docker run` (not msb) — resolve-image.sh only needs Docker,
# and CI's runner does not have msb installed, so gating this behind msb
# would mean the test that catches exactly this class of regression never
# runs automatically.
marketplace_output=$(docker run --rm --user node -e HOME=/tmp/claude-session-marketplace-test "$session_tag" claude plugin marketplace list 2>&1)
printf '%s\n' "$marketplace_output" | grep -Fq 'Source: GitHub (rikdc/ai-skills)'
if printf '%s\n' "$marketplace_output" | grep -Fq 'Failed to load marketplace'; then
  echo 'Claude could not load the session marketplace' >&2
  exit 1
fi

plugin_output=$(docker run --rm --user node -e HOME=/tmp/claude-session-marketplace-test "$session_tag" claude plugin list 2>&1)
printf '%s\n' "$plugin_output" | awk '
  /dev-skills@ai-skills/ { found = 1; next }
  found && /Status: ✔ enabled/ { enabled = 1; exit }
  END { exit !(found && enabled) }
'

echo ok
