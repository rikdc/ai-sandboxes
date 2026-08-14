#!/usr/bin/env bash
set -o pipefail
cd "$(dirname "$0")/../../.." || exit 1

fake_home=$(mktemp -d) || exit 1
seed_dir=$(mktemp -d) || exit 1
trap 'rm -rf "$fake_home" "$seed_dir"' EXIT

# Real shape Claude writes on a successful `claude plugin marketplace add` +
# `claude plugin install`: marketplace registration lives under
# extraKnownMarketplaces (Claude resolves the marketplace's code relative to
# CLAUDE_CODE_PLUGIN_CACHE_DIR at runtime, not from a path recorded here),
# and enablement lives under enabledPlugins.
cat >"$seed_dir/settings.json" <<'JSON' || exit 1
{
  "extraKnownMarketplaces": {
    "ai-skills": {
      "source": { "source": "github", "repo": "rikdc/ai-skills" }
    }
  },
  "enabledPlugins": {
    "dev-skills@ai-skills": true
  }
}
JSON

# Fresh home: the whole seed becomes the initial settings.json.
HOME="$fake_home" CLAUDE_CODE_PLUGIN_SEED_DIR="$seed_dir" images/claude/entrypoint.sh true || exit 1

jq -e '.extraKnownMarketplaces["ai-skills"].source.repo == "rikdc/ai-skills"' "$fake_home/.claude/settings.json" >/dev/null || exit 1
jq -e '.enabledPlugins["dev-skills@ai-skills"] == true' "$fake_home/.claude/settings.json" >/dev/null || exit 1

# Second launch: the user has since disabled the plugin and set an unrelated
# key. Both must survive the merge; the marketplace registration (a key the
# user never touched) must still be present, and not just enabledPlugins —
# this is what the recursive merge covers that the original single-key
# merge (enabledPlugins only) would have dropped on this second launch.
jq -n '{enabledPlugins: {"dev-skills@ai-skills": false}, theme: "dark"}' >"$fake_home/.claude/settings.json" || exit 1

HOME="$fake_home" CLAUDE_CODE_PLUGIN_SEED_DIR="$seed_dir" images/claude/entrypoint.sh true || exit 1

jq -e '.extraKnownMarketplaces["ai-skills"].source.repo == "rikdc/ai-skills"' "$fake_home/.claude/settings.json" >/dev/null || exit 1
jq -e '.enabledPlugins["dev-skills@ai-skills"] == false' "$fake_home/.claude/settings.json" >/dev/null || exit 1
jq -e '.theme == "dark"' "$fake_home/.claude/settings.json" >/dev/null || exit 1

# No seed at all: must no-op cleanly.
fake_home2=$(mktemp -d) || exit 1
empty_seed_dir=$(mktemp -d) || exit 1
trap 'chmod -R u+w "$fake_cache_dir" 2>/dev/null; rm -rf -- "$fake_home" "$seed_dir" "$fake_home2" "$empty_seed_dir" "$fake_cache_dir" "$fake_home3" "$materialised_dir"' EXIT
HOME="$fake_home2" CLAUDE_CODE_PLUGIN_SEED_DIR="$empty_seed_dir" images/claude/entrypoint.sh true || exit 1
test ! -e "$fake_home2/.claude/settings.json" || exit 1

# A present, non-empty seed cache must be copied into a fresh per-session dir
# under /tmp and CLAUDE_CODE_PLUGIN_CACHE_DIR repointed at it, leaving the
# seed untouched. The seed cache mirrors the seeded /opt/claude-plugin-cache
# shape: a root-owned, non-writable tree the runtime user must never modify.
fake_cache_dir=$(mktemp -d) || exit 1
mkdir -p "$fake_cache_dir/marketplaces" "$fake_cache_dir/data"
printf '{}\n' >"$fake_cache_dir/known_marketplaces.json"
printf '{}\n' >"$fake_cache_dir/installed_plugins.json"
printf 'payload\n' >"$fake_cache_dir/marketplaces/sample.txt"
chmod -R a-w "$fake_cache_dir"
seed_checksum_before=$(find "$fake_cache_dir" -type f -print0 | sort -z | xargs -0 shasum -a 256 | shasum -a 256 | cut -d' ' -f1) || exit 1

fake_home3=$(mktemp -d) || exit 1
# shellcheck disable=SC2016 # $CLAUDE_CODE_PLUGIN_CACHE_DIR expands in the child sh the entrypoint execs, not here.
materialised_dir=$(HOME="$fake_home3" CLAUDE_CODE_PLUGIN_CACHE_DIR="$fake_cache_dir" CLAUDE_CODE_PLUGIN_SEED_DIR="$seed_dir" \
  images/claude/entrypoint.sh sh -c 'printf %s "$CLAUDE_CODE_PLUGIN_CACHE_DIR"') || exit 1
test -n "$materialised_dir" || exit 1
test "$materialised_dir" != "$fake_cache_dir" || exit 1
test -d "$materialised_dir" || exit 1
# The materialised copy is owned/writable by the runtime user and carries the
# seeded entries.
test -f "$materialised_dir/known_marketplaces.json" || exit 1
test -f "$materialised_dir/installed_plugins.json" || exit 1
test -f "$materialised_dir/marketplaces/sample.txt" || exit 1
test -w "$materialised_dir" || exit 1
test -w "$materialised_dir/marketplaces" || exit 1
test -w "$materialised_dir/data" || exit 1
test "${materialised_dir#/tmp/ai-sandboxes-plugin-cache.}" != "$materialised_dir" || exit 1
# The seed cache must be bit-for-bit unchanged after a launch.
seed_checksum_after=$(find "$fake_cache_dir" -type f -print0 | sort -z | xargs -0 shasum -a 256 | shasum -a 256 | cut -d' ' -f1) || exit 1
test "$seed_checksum_before" = "$seed_checksum_after" || exit 1

# When the seed cache is absent (no CLAUDE_CODE_PLUGIN_CACHE_DIR), the
# entrypoint must not materialise an extra per-session dir.
materialised_before=$(find /tmp -maxdepth 1 -type d -name 'ai-sandboxes-plugin-cache.*' 2>/dev/null | wc -l | tr -d ' ') || exit 1
HOME="$fake_home3" CLAUDE_CODE_PLUGIN_SEED_DIR="$seed_dir" images/claude/entrypoint.sh true || exit 1
materialised_after=$(find /tmp -maxdepth 1 -type d -name 'ai-sandboxes-plugin-cache.*' 2>/dev/null | wc -l | tr -d ' ') || exit 1
test "$materialised_before" = "$materialised_after" || exit 1

echo ok
