#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../../.."

if ! command -v msb >/dev/null 2>&1; then
  echo 'skip: msb not installed' >&2
  exit 0
fi

test_tag="ai-sandboxes-session-loader-test:local"
cleanup() {
  docker image rm -f "$test_tag" >/dev/null 2>&1 || true
  msb image remove "$test_tag" >/dev/null 2>&1 || true
}
trap cleanup EXIT

build_dir=$(mktemp -d)
printf 'FROM scratch\nCOPY resolved.json /resolved.json\n' >"$build_dir/Dockerfile"
echo '{}' >"$build_dir/resolved.json"
docker build --tag "$test_tag" "$build_dir" >/dev/null
rm -rf "$build_dir"

claude_present_before=false
msb image list --quiet | grep -Fxq ai-sandboxes-claude:local && claude_present_before=true

scripts/session/load-image.sh "$test_tag"
msb image list --quiet | grep -Fxq "$test_tag"

# Second call must skip (already present) rather than remove-and-reload.
scripts/session/load-image.sh "$test_tag"
msb image list --quiet | grep -Fxq "$test_tag"

claude_present_after=false
msb image list --quiet | grep -Fxq ai-sandboxes-claude:local && claude_present_after=true
test "$claude_present_before" = "$claude_present_after"

echo ok
