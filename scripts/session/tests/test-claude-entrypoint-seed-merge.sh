#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../../.."

fake_home=$(mktemp -d)
seed_dir=$(mktemp -d)
trap 'rm -rf "$fake_home" "$seed_dir"' EXIT

# Real shape Claude writes on a successful `claude plugin marketplace add` +
# `claude plugin install`: marketplace registration lives under
# extraKnownMarketplaces (Claude resolves the marketplace's code relative to
# CLAUDE_CODE_PLUGIN_CACHE_DIR at runtime, not from a path recorded here),
# and enablement lives under enabledPlugins.
cat >"$seed_dir/settings.json" <<'JSON'
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
HOME="$fake_home" CLAUDE_CODE_PLUGIN_SEED_DIR="$seed_dir" images/claude/entrypoint.sh true

jq -e '.extraKnownMarketplaces["ai-skills"].source.repo == "rikdc/ai-skills"' "$fake_home/.claude/settings.json" >/dev/null
jq -e '.enabledPlugins["dev-skills@ai-skills"] == true' "$fake_home/.claude/settings.json" >/dev/null

# Second launch: the user has since disabled the plugin and set an unrelated
# key. Both must survive the merge; the marketplace registration (a key the
# user never touched) must still be present, and not just enabledPlugins —
# this is what the recursive merge covers that the original single-key
# merge (enabledPlugins only) would have dropped on this second launch.
jq -n '{enabledPlugins: {"dev-skills@ai-skills": false}, theme: "dark"}' >"$fake_home/.claude/settings.json"

HOME="$fake_home" CLAUDE_CODE_PLUGIN_SEED_DIR="$seed_dir" images/claude/entrypoint.sh true

jq -e '.extraKnownMarketplaces["ai-skills"].source.repo == "rikdc/ai-skills"' "$fake_home/.claude/settings.json" >/dev/null
jq -e '.enabledPlugins["dev-skills@ai-skills"] == false' "$fake_home/.claude/settings.json" >/dev/null
jq -e '.theme == "dark"' "$fake_home/.claude/settings.json" >/dev/null

# No seed at all: must no-op cleanly.
fake_home2=$(mktemp -d)
empty_seed_dir=$(mktemp -d)
trap 'rm -rf "$fake_home" "$seed_dir" "$fake_home2" "$empty_seed_dir"' EXIT
HOME="$fake_home2" CLAUDE_CODE_PLUGIN_SEED_DIR="$empty_seed_dir" images/claude/entrypoint.sh true
test ! -e "$fake_home2/.claude/settings.json"

echo ok
