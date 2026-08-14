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

# The wrapper invokes ai-sandbox by absolute path (a wrapper that instead
# relied on PATH lookup would let a workspace-mounted stub in ~/.local/bin
# hijack the next launch). Install a stub at the location the wrapper bakes
# in so assert_allows can prove the wrapper actually reached the binary
# rather than silently failing because it was missing.
ai_sandbox_stub_dir="$fake_home/.local/libexec/ai-sandboxes"
mkdir -p "$ai_sandbox_stub_dir" || exit 1
ai_sandbox_stub_log="$fake_home/ai-sandbox-stub.log"
cat >"$ai_sandbox_stub_dir/ai-sandbox" <<STUB
#!/usr/bin/env bash
printf 'invoked: %s\n' "\$*" >>"$ai_sandbox_stub_log"
exit 0
STUB
chmod +x "$ai_sandbox_stub_dir/ai-sandbox" || exit 1

env HOME="$fake_home" AI_SANDBOX_INSTALL_DIR="$ai_sandbox_stub_dir" \
  ./scripts/install-fish-functions >/dev/null || exit 1

# The wrapper must bake the absolute install path, not rely on PATH lookup.
grep -Fq "$ai_sandbox_stub_dir/ai-sandbox" "$fake_home/.config/fish/functions/claude.fish" \
  || { echo 'FAIL: claude wrapper does not bake absolute ai-sandbox path' >&2; exit 1; }

# claude-session is a claude invocation driven by the profile flag: ai-sandbox
# must receive `run claude --profile ...` intact, so no `--` may be inserted
# between `run claude` and the user's arguments (it would swallow --profile).
grep -Eq 'run claude[[:space:]]' "$fake_home/.config/fish/functions/claude-session.fish" \
  || { echo 'FAIL: claude-session wrapper missing run-claude passthrough' >&2; exit 1; }
if grep -Eq 'run claude[[:space:]]+--' "$fake_home/.config/fish/functions/claude-session.fish"; then
  echo 'FAIL: claude-session wrapper must not insert -- (it would swallow --profile)' >&2
  exit 1
fi

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
  : >"$ai_sandbox_stub_log"
  local output
  output=$(cd "$workspace" && fish -c "source '$wrapper'; claude a-forwarded-arg" 2>&1) || true
  printf '%s\n' "$output" | grep -q 'refusing to run' \
    && { echo "FAIL ($label): unexpected refusal for workspace $workspace" >&2; exit 1; }
  # Prove the wrapper actually reached ai-sandbox with the expected argv,
  # rather than passing simply because it errored out before the refusal check.
  grep -Fq 'invoked: run claude -- a-forwarded-arg' "$ai_sandbox_stub_log" \
    || { echo "FAIL ($label): ai-sandbox stub was not invoked as expected for $workspace" >&2;
         echo "stub log:" >&2; cat "$ai_sandbox_stub_log" >&2; exit 1; }
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

# The claude-session wrapper must forward --profile and the claude arguments to
# ai-sandbox unchanged (no `--` separator, so the profile flag reaches the
# control plane rather than being handed to claude).
: >"$ai_sandbox_stub_log"
output=$(cd "$fake_home/unrelated-project" && fish -c "source '$fish_functions_dir/claude-session.fish'; claude-session --profile work -p hi" 2>&1) || true
printf '%s\n' "$output" | grep -q 'refusing to run' \
  && { echo 'FAIL: claude-session unexpectedly refused for unrelated workspace' >&2; exit 1; }
grep -Fq 'invoked: run claude --profile work -p hi' "$ai_sandbox_stub_log" \
  || { echo 'FAIL: claude-session did not pass --profile through to ai-sandbox' >&2;
       echo 'stub log:' >&2; cat "$ai_sandbox_stub_log" >&2; exit 1; }

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
# The apostrophe run uses its own home, so the wrapper bakes a different
# install path; put a stub there too and repoint the log so assert_allows can
# verify that wrapper as well.
apostrophe_stub_dir="$fake_home_apostrophe/.local/libexec/ai-sandboxes"
mkdir -p "$apostrophe_stub_dir" || exit 1
ai_sandbox_stub_log="$fake_home_apostrophe/ai-sandbox-stub.log"
cat >"$apostrophe_stub_dir/ai-sandbox" <<STUB
#!/usr/bin/env bash
printf 'invoked: %s\n' "\$*" >>"$ai_sandbox_stub_log"
exit 0
STUB
chmod +x "$apostrophe_stub_dir/ai-sandbox" || exit 1
env HOME="$fake_home_apostrophe" "$apostrophe_checkout/scripts/install-fish-functions" >/dev/null || exit 1
apostrophe_fish_functions_dir="$fake_home_apostrophe/.config/fish/functions"
fish --no-execute "$apostrophe_fish_functions_dir/claude.fish" || exit 1
assert_allows apostrophe-checkout-unrelated "$apostrophe_fish_functions_dir/claude.fish" "$fake_home_apostrophe/unrelated-project" || exit 1

echo ok
