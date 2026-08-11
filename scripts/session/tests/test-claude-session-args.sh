#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../../.."

if ! command -v fish >/dev/null 2>&1; then
  echo 'skip: fish not installed' >&2
  exit 0
fi

output=$(fish -c 'source shell/fish/claude-session.fish; __ai_sandbox_impl_claude_session' 2>&1) && status=0 || status=$?
test "$status" -eq 2
printf '%s\n' "$output" | grep -q -- '--profile'

output=$(fish -c 'source shell/fish/claude-session.fish; __ai_sandbox_impl_claude_session --profile /no/such/profile.json' 2>&1) && status=0 || status=$?
test "$status" -eq 1
printf '%s\n' "$output" | grep -q 'profile not found'

echo ok
