#!/usr/bin/env bash
set -o pipefail
cd "$(dirname "$0")/../../.." || exit 1

if ! command -v fish >/dev/null 2>&1; then
  echo 'skip: fish not installed' >&2
  exit 0
fi

repo_root=$(pwd) || exit 1
launcher_file="$repo_root/shell/fish/claude.fish"
lib_file="$repo_root/shell/fish/lib/ai-sandbox.fish"

stub_dir=$(mktemp -d) || exit 1
capture_file=$(mktemp) || exit 1
# __ai_sandbox_run_claude refuses to run when its computed workspace
# (git rev-parse --show-toplevel, or pwd -P if that fails) overlaps the
# ai-sandboxes checkout that provides the launcher (see
# __ai_sandbox_refuse_workspace_overlap). Running fish from inside this repo
# would make workspace == launcher_root and trip that refusal before ever
# reaching `command msb run`. A scratch dir outside any git repo sidesteps
# this: `git rev-parse --show-toplevel` fails there, so host_workspace falls
# back to the scratch dir itself, which does not overlap the checkout.
scratch_dir=$(mktemp -d) || exit 1
trap 'rm -rf "$stub_dir" "$capture_file" "$scratch_dir"' EXIT

# A stub `msb` executable (not a fish function: __ai_sandbox_run_claude invokes
# `command msb run ...`, which resolves against PATH, not fish functions) that
# records its own argv and exits 0, standing in for the real msb binary so this
# test needs no Docker/msb/network -- only fish and the argv-slicing logic
# under test.
cat >"$stub_dir/msb" <<'EOF'
#!/bin/sh
printf '%s\n' "$@" > "$MSB_STUB_CAPTURE"
exit 0
EOF
chmod +x "$stub_dir/msb" || exit 1

run_probe() {
  : >"$capture_file"
  # Exporting PATH from bash before exec'ing fish is not reliable: fish's own
  # startup files (config.fish, universal variables like fish_user_paths) run
  # during initialization and can re-derive PATH, reordering entries such as
  # /opt/homebrew/bin ahead of an inherited prefix. Prepend the stub dir from
  # inside the fish script instead (after startup has run), and confirm `msb`
  # actually resolves to the stub before invoking the launcher, so the test
  # fails loudly instead of silently exercising the real msb binary.
  # These variables are consumed by the external Fish process below; its
  # single-quoted program deliberately owns its own variable expansion.
  # shellcheck disable=SC2034,SC2016
  MSB_STUB_CAPTURE="$capture_file" CLAUDE_MSB_PUBLIC_EGRESS=1 \
    AI_SANDBOX_TEST_LAUNCHER_FILE="$launcher_file" \
    AI_SANDBOX_TEST_LIB_FILE="$lib_file" \
    AI_SANDBOX_TEST_SCRATCH_DIR="$scratch_dir" \
    AI_SANDBOX_TEST_STUB_DIR="$stub_dir" \
    fish -c '
      cd "$AI_SANDBOX_TEST_SCRATCH_DIR"; or exit 1
      set -gx PATH "$AI_SANDBOX_TEST_STUB_DIR" $PATH
      if test (command -s msb) != "$AI_SANDBOX_TEST_STUB_DIR/msb"
          echo "FAIL: msb did not resolve to the stub" >&2
          exit 1
      end
      source "$AI_SANDBOX_TEST_LIB_FILE"
      __ai_sandbox_run_claude "$AI_SANDBOX_TEST_LAUNCHER_FILE" probe-image:local $argv
    ' -- "$@" >/dev/null
  # The assertions below only inspect the capture file, so a fish crash or a
  # failed msb invocation would otherwise surface as a misleading "wrong argv"
  # failure. Fail loudly on the probe's own exit status instead. stderr stays
  # visible so any FAIL message printed inside the fish script is not swallowed.
  local probe_status=$?
  if test "$probe_status" -ne 0; then
    echo "FAIL: fish probe exited with status $probe_status" >&2
    cat "$capture_file" >&2
    exit 1
  fi
}

# Case: shared_state_arg_count=0 -- no shared-state args, claude_argv is
# everything after the count. Note the base `msb run` invocation always
# emits its own `--mount-named` for the fixed home volume (independent of
# shared state), so the count=0 case is verified by asserting exactly ONE
# `--mount-named` line (the fixed home mount, no shared-state mount got
# spliced in), not by asserting zero.
run_probe 0 --version
grep -qFx -- '--version' "$capture_file" \
  || { echo 'FAIL: count=0 did not pass --version through to msb argv' >&2; cat "$capture_file" >&2; exit 1; }
mount_named_count=$(grep -cFx -- '--mount-named' "$capture_file")
test "$mount_named_count" -eq 1 \
  || { echo "FAIL: count=0 expected exactly 1 --mount-named (fixed home volume only), got $mount_named_count" >&2; cat "$capture_file" >&2; exit 1; }

# Case: shared_state_arg_count=2 -- the two shared-state tokens must reach
# msb's own argv as an additional --mount-named flag (on top of the fixed
# home volume's own --mount-named), and --version must still reach claude's
# argv (the very last line of the captured invocation), not get absorbed
# into the shared-state slice.
run_probe 2 --mount-named 'agent-state-probe-v1:/var/lib/agent-state:kind=dir,quota=1G' --version
mount_named_count=$(grep -cFx -- '--mount-named' "$capture_file")
test "$mount_named_count" -eq 2 \
  || { echo "FAIL: count=2 expected exactly 2 --mount-named (fixed home volume + shared state), got $mount_named_count" >&2; cat "$capture_file" >&2; exit 1; }
grep -qFx -- 'agent-state-probe-v1:/var/lib/agent-state:kind=dir,quota=1G' "$capture_file" \
  || { echo 'FAIL: count=2 mount-named value missing from msb argv' >&2; cat "$capture_file" >&2; exit 1; }
tail -1 "$capture_file" | grep -qFx -- '--version' \
  || { echo 'FAIL: count=2 did not preserve --version as the final claude argument' >&2; cat "$capture_file" >&2; exit 1; }

echo ok
