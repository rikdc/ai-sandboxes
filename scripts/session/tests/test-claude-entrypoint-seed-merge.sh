#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../../.."

fake_home=$(mktemp -d)
base_seed_dir=$(mktemp -d)
session_seed_dir=$(mktemp -d)
trap 'rm -rf "$fake_home" "$base_seed_dir" "$session_seed_dir"' EXIT

cat >"$base_seed_dir/settings.json" <<'JSON'
{"enabledPlugins":{"a@m1":true,"b@m1":true}}
JSON
cat >"$session_seed_dir/settings.json" <<'JSON'
{"enabledPlugins":{"c@m2":true}}
JSON

# Fresh home, both a base and a session seed: both must be enabled.
HOME="$fake_home" CLAUDE_CODE_PLUGIN_SEED_DIR="$base_seed_dir" \
  CLAUDE_CODE_SESSION_PLUGIN_SEED_DIR="$session_seed_dir" \
  images/claude/entrypoint.sh true

jq -e '.enabledPlugins["a@m1"] == true' "$fake_home/.claude/settings.json" >/dev/null
jq -e '.enabledPlugins["b@m1"] == true' "$fake_home/.claude/settings.json" >/dev/null
jq -e '.enabledPlugins["c@m2"] == true' "$fake_home/.claude/settings.json" >/dev/null

# Second launch: the user has since disabled a@m1 and set an unrelated key.
# Both must survive the merge; still-missing defaults must still be added.
jq -n '{enabledPlugins: {"a@m1": false}, theme: "dark"}' >"$fake_home/.claude/settings.json"

HOME="$fake_home" CLAUDE_CODE_PLUGIN_SEED_DIR="$base_seed_dir" \
  CLAUDE_CODE_SESSION_PLUGIN_SEED_DIR="$session_seed_dir" \
  images/claude/entrypoint.sh true

jq -e '.enabledPlugins["a@m1"] == false' "$fake_home/.claude/settings.json" >/dev/null
jq -e '.enabledPlugins["b@m1"] == true' "$fake_home/.claude/settings.json" >/dev/null
jq -e '.enabledPlugins["c@m2"] == true' "$fake_home/.claude/settings.json" >/dev/null
jq -e '.theme == "dark"' "$fake_home/.claude/settings.json" >/dev/null

# Base seed only (today's exact scenario: no session overlay at all).
fake_home2=$(mktemp -d)
trap 'rm -rf "$fake_home" "$base_seed_dir" "$session_seed_dir" "$fake_home2"' EXIT
HOME="$fake_home2" CLAUDE_CODE_PLUGIN_SEED_DIR="$base_seed_dir" images/claude/entrypoint.sh true
jq -e '.enabledPlugins["a@m1"] == true' "$fake_home2/.claude/settings.json" >/dev/null
jq -e '.enabledPlugins["c@m2"]? == null' "$fake_home2/.claude/settings.json" >/dev/null

# No base seed and no session seed at all: must no-op cleanly.
fake_home3=$(mktemp -d)
empty_seed_dir=$(mktemp -d)
trap 'rm -rf "$fake_home" "$base_seed_dir" "$session_seed_dir" "$fake_home2" "$fake_home3" "$empty_seed_dir"' EXIT
HOME="$fake_home3" CLAUDE_CODE_PLUGIN_SEED_DIR="$empty_seed_dir" images/claude/entrypoint.sh true
test ! -e "$fake_home3/.claude/settings.json"

echo ok
