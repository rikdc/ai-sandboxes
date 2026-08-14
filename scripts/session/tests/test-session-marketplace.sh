#!/usr/bin/env bash
set -o pipefail
cd "$(dirname "$0")/../../.." || exit 1

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

session_tag=$(CLAUDE_MSB_BUILD_EGRESS=1 scripts/session/resolve-image.sh scripts/session/fixtures/valid/claude-marketplaces.json | jq -er '.image') || exit 1

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
marketplace_output=$(docker run --rm --user node -e HOME=/tmp/claude-session-marketplace-test "$session_tag" claude plugin marketplace list 2>&1) || exit 1
if ! printf '%s\n' "$marketplace_output" | grep -Fq 'Source: GitHub (rikdc/ai-skills)'; then
  echo 'Claude did not register the session marketplace' >&2
  printf '%s\n' "$marketplace_output" >&2
  exit 1
fi
if printf '%s\n' "$marketplace_output" | grep -Fq 'Failed to load marketplace'; then
  echo 'Claude could not load the session marketplace' >&2
  exit 1
fi

plugin_output=$(docker run --rm --user node -e HOME=/tmp/claude-session-marketplace-test "$session_tag" claude plugin list 2>&1) || exit 1
if ! printf '%s\n' "$plugin_output" | awk '
  /dev-skills@ai-skills/ { found = 1; next }
  found && /Status: ✔ enabled/ { enabled = 1; exit }
  END { exit !(found && enabled) }
'; then
  echo 'Session plugin dev-skills@ai-skills is not enabled' >&2
  printf '%s\n' "$plugin_output" >&2
  exit 1
fi

# The seeded cache under /opt/claude-plugin-cache is immutable: the entrypoint
# (inherited from the base image) materialises a fresh, fully writable
# per-session copy under /tmp and repoints CLAUDE_CODE_PLUGIN_CACHE_DIR at it
# before exec'ing claude, so none of the seed's paths may ever be writable by
# node. The old carve-out assertions (data/, marketplaces/, and the root each
# writable) are gone — this test instead asserts the seed can't be modified and
# that claude still lists the seeded marketplaces/plugins via the materialised
# copy. Without claude's actual runtime writes landing somewhere writable, the
# startup would hit `EACCES: permission denied, open
# '/opt/claude-plugin-cache/known_marketplaces.json.tmp.XXXXXX'`; the
# marketplace/plugin checks above cover that end-to-end.
if docker run --rm --user node "$session_tag" sh -c 'touch /opt/claude-plugin-cache/data/.runtime-write-test 2>/dev/null'; then
  echo 'FAIL: session image seeded plugin cache data/ is writable by node' >&2
  exit 1
fi
if docker run --rm --user node "$session_tag" sh -c 'touch /opt/claude-plugin-cache/marketplaces/.runtime-write-test 2>/dev/null'; then
  echo 'FAIL: session image seeded plugin cache marketplaces/ is writable by node' >&2
  exit 1
fi
if docker run --rm --user node "$session_tag" sh -c 'touch /opt/claude-plugin-cache/.runtime-write-test 2>/dev/null'; then
  echo 'FAIL: session image seeded plugin cache root is writable by node' >&2
  exit 1
fi
# Top-level seeded entries must not be replaceable by node.
if docker run --rm --user node "$session_tag" sh -c 'rm -f /opt/claude-plugin-cache/known_marketplaces.json 2>/dev/null'; then
  echo 'FAIL: session image seeded known_marketplaces.json is replaceable by node' >&2
  exit 1
fi
if docker run --rm --user node "$session_tag" sh -c 'mv /opt/claude-plugin-cache/installed_plugins.json /tmp/session-renamed.json 2>/dev/null'; then
  echo 'FAIL: session image seeded installed_plugins.json is replaceable by node' >&2
  exit 1
fi
# The entrypoint must actually have materialised a writable per-session cache
# under /tmp for the claude invocations above.
if ! docker run --rm --user node "$session_tag" sh -c 'test -n "$(find /tmp -maxdepth 1 -type d -name "ai-sandboxes-plugin-cache.*" 2>/dev/null | head -n 1)"'; then
  echo 'FAIL: session image entrypoint did not materialise a per-session plugin cache' >&2
  exit 1
fi

echo ok
