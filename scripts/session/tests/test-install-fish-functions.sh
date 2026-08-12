#!/usr/bin/env bash
set -o pipefail
cd "$(dirname "$0")/../../.." || exit 1

if ! command -v fish >/dev/null 2>&1; then
  echo 'skip: fish not installed' >&2
  exit 0
fi

repo_root=$(pwd) || exit 1
fake_home=$(mktemp -d) || exit 1
fake_home_symlinked=$(mktemp -d) || exit 1
real_config_target=$(mktemp -d) || exit 1
fake_home_apostrophe=$(mktemp -d) || exit 1
apostrophe_checkout="$fake_home/o'brien-checkout"
trap 'rm -rf "$fake_home" "$fake_home_symlinked" "$real_config_target" "$fake_home_apostrophe"' EXIT

env HOME="$fake_home" ./scripts/install-fish-functions >/dev/null || exit 1

fish_functions_dir="$fake_home/.config/fish/functions"
trusted_dir="$fake_home/.config/ai-sandboxes/trusted"

test -f "$fish_functions_dir/claude.fish" || exit 1
test -f "$fish_functions_dir/codex.fish" || exit 1
test -f "$fish_functions_dir/claude-session.fish" || exit 1
test -f "$trusted_dir/guard.fish" || exit 1

assert_refuses() {
  local label=$1 wrapper=$2 workspace=$3
  mkdir -p "$workspace" || return 1
  local output status
  output=$(cd "$workspace" && fish -c "source '$wrapper'; claude" 2>&1) && status=0 || status=$?
  test "$status" -ne 0 || { echo "FAIL ($label): expected refusal for workspace $workspace" >&2; exit 1; }
  printf '%s\n' "$output" | grep -q 'refusing to run' \
    || { echo "FAIL ($label): missing refusal message for workspace $workspace" >&2; exit 1; }
}

assert_allows() {
  local label=$1 wrapper=$2 workspace=$3
  mkdir -p "$workspace" || return 1
  local output
  output=$(cd "$workspace" && fish -c "source '$wrapper'; claude" 2>&1) || true
  printf '%s\n' "$output" | grep -q 'refusing to run' \
    && { echo "FAIL ($label): unexpected refusal for workspace $workspace" >&2; exit 1; }
  return 0
}

# The installed wrapper must refuse for the checkout itself and for its own
# installed directories (the wrapper and guard's own writable install roots),
# not only the checkout: a guest mounted at any of these could otherwise
# tamper with the trust boundary itself.
assert_refuses checkout "$fish_functions_dir/claude.fish" "$repo_root" || exit 1
assert_refuses fish-functions-dir "$fish_functions_dir/claude.fish" "$fish_functions_dir" || exit 1
assert_refuses fish-config-dir "$fish_functions_dir/claude.fish" "$fake_home/.config/fish" || exit 1
assert_refuses trusted-dir "$fish_functions_dir/claude.fish" "$trusted_dir" || exit 1
assert_allows unrelated "$fish_functions_dir/claude.fish" "$fake_home/unrelated-project" || exit 1

# Symlinked dotfiles layout: ~/.config itself is a symlink to elsewhere (GNU
# stow, chezmoi, and similar tools all do this). The protected root baked
# into the wrapper is the symlink-path spelling; a workspace reached through
# the resolved target, not the symlink, must still be refused.
ln -s "$real_config_target" "$fake_home_symlinked/.config" || exit 1
env HOME="$fake_home_symlinked" ./scripts/install-fish-functions >/dev/null || exit 1
symlinked_fish_functions_dir="$fake_home_symlinked/.config/fish/functions"
test -f "$symlinked_fish_functions_dir/claude.fish" || exit 1
assert_refuses symlinked-config-real-target "$symlinked_fish_functions_dir/claude.fish" "$real_config_target/fish/functions" || exit 1

# A path containing an apostrophe (a real possibility in a $HOME directory
# name) must not break the generated wrapper's fish syntax.
mkdir -p "$apostrophe_checkout" || exit 1
cp -R scripts shell "$apostrophe_checkout/" || exit 1
env HOME="$fake_home_apostrophe" "$apostrophe_checkout/scripts/install-fish-functions" >/dev/null || exit 1
apostrophe_fish_functions_dir="$fake_home_apostrophe/.config/fish/functions"
fish --no-execute "$apostrophe_fish_functions_dir/claude.fish" || exit 1
assert_allows apostrophe-checkout-unrelated "$apostrophe_fish_functions_dir/claude.fish" "$fake_home_apostrophe/unrelated-project" || exit 1

echo ok
