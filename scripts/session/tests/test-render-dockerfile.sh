#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../../.."

context_dir=$(mktemp -d)
trap 'rm -rf "$context_dir"' EXIT

if scripts/session/render-dockerfile.sh "$context_dir" 'ai-sandboxes-claude-session-base:deadbeef' 2>/dev/null; then
  echo 'FAIL: should refuse a context dir with no resolved.json' >&2
  exit 1
fi

echo '{"ok":true}' >"$context_dir/resolved.json"
scripts/session/render-dockerfile.sh "$context_dir" 'ai-sandboxes-claude-session-base:deadbeef'

test -f "$context_dir/Dockerfile"
grep -qFx 'FROM ai-sandboxes-claude-session-base:deadbeef' "$context_dir/Dockerfile"
grep -qFx 'USER node' "$context_dir/Dockerfile"
test "$(find "$context_dir" -maxdepth 1 -type f | wc -l)" -eq 2

echo ok
