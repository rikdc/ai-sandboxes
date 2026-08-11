#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../../.."

if ! command -v fish >/dev/null 2>&1; then
  echo 'skip: fish not installed' >&2
  exit 0
fi

repo_root=$(pwd)
fake_home=$(mktemp -d)
trap 'rm -rf "$fake_home"' EXIT

env HOME="$fake_home" ./scripts/install-fish-functions >/dev/null

fish_functions_dir="$fake_home/.config/fish/functions"
trusted_dir="$fake_home/.config/ai-sandboxes/trusted"

test -f "$fish_functions_dir/claude.fish"
test -f "$fish_functions_dir/codex.fish"
test -f "$fish_functions_dir/claude-session.fish"
test -f "$trusted_dir/guard.fish"

assert_refuses() {
  local label=$1 workspace=$2
  mkdir -p "$workspace"
  local output status
  output=$(cd "$workspace" && fish -c "source '$fish_functions_dir/claude.fish'; claude" 2>&1) && status=0 || status=$?
  test "$status" -ne 0 || { echo "FAIL ($label): expected refusal for workspace $workspace" >&2; exit 1; }
  printf '%s\n' "$output" | grep -q 'refusing to run' \
    || { echo "FAIL ($label): missing refusal message for workspace $workspace" >&2; exit 1; }
}

# The installed wrapper must refuse for the checkout itself and for its own
# installed directories (the wrapper and guard's own writable install roots),
# not only the checkout: a guest mounted at any of these could otherwise
# tamper with the trust boundary itself.
assert_refuses checkout "$repo_root"
assert_refuses fish-functions-dir "$fish_functions_dir"
assert_refuses fish-config-dir "$fake_home/.config/fish"
assert_refuses trusted-dir "$trusted_dir"

unrelated_workspace="$fake_home/unrelated-project"
mkdir -p "$unrelated_workspace"
output=$(cd "$unrelated_workspace" && fish -c "source '$fish_functions_dir/claude.fish'; claude" 2>&1) || true
printf '%s\n' "$output" | grep -q 'refusing to run' \
  && { echo 'FAIL: unexpected refusal for an unrelated workspace' >&2; exit 1; }

echo ok
