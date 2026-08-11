#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../../.."

context_dir=$(mktemp -d)
marketplace_context_dir=$(mktemp -d)
trap 'rm -rf "$context_dir" "$marketplace_context_dir"' EXIT

if scripts/session/render-dockerfile.sh "$context_dir" 'ai-sandboxes-claude-session-base:deadbeef' '{"schema_version":1}' 2>/dev/null; then
  echo 'FAIL: should refuse a context dir with no resolved.json' >&2
  exit 1
fi

echo '{"ok":true}' >"$context_dir/resolved.json"
scripts/session/render-dockerfile.sh "$context_dir" 'ai-sandboxes-claude-session-base:deadbeef' '{"schema_version":1}'

test -f "$context_dir/Dockerfile"
grep -qFx 'FROM ai-sandboxes-claude-session-base:deadbeef' "$context_dir/Dockerfile"
grep -qFx 'USER node' "$context_dir/Dockerfile"
test "$(find "$context_dir" -maxdepth 1 -type f | wc -l)" -eq 2

echo '{"ok":true}' >"$marketplace_context_dir/resolved.json"
profile_with_marketplace='{"schema_version":1,"claude_marketplaces":[{"url":"https://github.com/rikdc/ai-skills.git","ref":"d66daa0504ff859a9d51c86b9175eb9fe768cd25","path":".","plugins":["dev-skills"]}]}'
scripts/session/render-dockerfile.sh "$marketplace_context_dir" 'ai-sandboxes-claude-session-base:deadbeef' "$profile_with_marketplace"

test -f "$marketplace_context_dir/Dockerfile"
test -f "$marketplace_context_dir/session-marketplaces.json"
test -f "$marketplace_context_dir/install-claude-marketplaces.sh"
test -f "$marketplace_context_dir/merge-plugin-seed.sh"
grep -qFx 'FROM ai-sandboxes-claude-session-base:deadbeef AS build' "$marketplace_context_dir/Dockerfile"
# Installs additively into the SAME paths the running container reads, not a
# separate/unreferenced root: Claude resolves a marketplace's code relative
# to CLAUDE_CODE_PLUGIN_CACHE_DIR at runtime (confirmed empirically), so a
# session-only cache root nothing points to at runtime would register but
# never load.
grep -qF 'CLAUDE_CODE_PLUGIN_CACHE_DIR=/opt/claude-plugin-cache' "$marketplace_context_dir/Dockerfile"
grep -qF 'CLAUDE_CODE_PLUGIN_SEED_DIR=/opt/claude-plugin-seed' "$marketplace_context_dir/Dockerfile"
grep -qF 'merge-session-plugin-seed.sh /opt/claude-session-build-home/.claude/settings.json /opt/claude-plugin-seed/settings.json' "$marketplace_context_dir/Dockerfile"
# The base image deliberately keeps /opt/claude-plugin-cache/data
# node-owned/writable (for Claude's runtime plugin state) after locking
# everything else read-only. The build stage's own relock sweeps that
# subdirectory back to read-only since it already exists there, copied in
# from the base image this stage starts FROM — the final stage must recreate
# it after the COPY, or a marketplace-derived session image ships with
# Claude's runtime-data directory read-only.
grep -qFx 'RUN install -d -o node -g node -m 0755 /opt/claude-plugin-cache/data' "$marketplace_context_dir/Dockerfile"
grep -qFx 'USER node' "$marketplace_context_dir/Dockerfile"
test "$(find "$marketplace_context_dir" -maxdepth 1 -type f | wc -l)" -eq 5
jq -e '.claude | length == 1' "$marketplace_context_dir/session-marketplaces.json" >/dev/null
jq -e '.claude[0].url == "https://github.com/rikdc/ai-skills.git"' "$marketplace_context_dir/session-marketplaces.json" >/dev/null
jq -e '.codex == []' "$marketplace_context_dir/session-marketplaces.json" >/dev/null
diff -q "$marketplace_context_dir/install-claude-marketplaces.sh" scripts/marketplaces/install-claude.sh
diff -q "$marketplace_context_dir/merge-plugin-seed.sh" scripts/session/merge-plugin-seed.sh

echo ok
