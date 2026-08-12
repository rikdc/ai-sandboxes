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
trap 'rm -rf "$fake_home" "$seed_dir" "$fake_home2" "$empty_seed_dir"' EXIT
HOME="$fake_home2" CLAUDE_CODE_PLUGIN_SEED_DIR="$empty_seed_dir" images/claude/entrypoint.sh true || exit 1
test ! -e "$fake_home2/.claude/settings.json" || exit 1

echo ok
