#!/usr/bin/env bash
set -o pipefail
cd "$(dirname "$0")/../../.." || exit 1

if ! command -v msb >/dev/null 2>&1; then
  echo 'skip: msb not installed' >&2
  exit 0
fi
if ! command -v fish >/dev/null 2>&1; then
  echo 'skip: fish not installed' >&2
  exit 0
fi
if ! docker image inspect ai-sandboxes-claude:local >/dev/null 2>&1; then
  echo 'skip: ai-sandboxes-claude:local not built (run ./scripts/build)' >&2
  exit 0
fi

session_tag=''
cleanup() {
  if test -n "$session_tag"; then
    docker image rm -f "$session_tag" >/dev/null 2>&1 || true
    msb image remove "$session_tag" >/dev/null 2>&1 || true
  fi
  msb volume remove "agent-state-${state_id:-}-v1" >/dev/null 2>&1 || true
}
trap cleanup EXIT

descriptor=$(CLAUDE_MSB_BUILD_EGRESS=1 scripts/session/resolve-image.sh scripts/session/fixtures/valid/icm-with-shared-state.json) || exit 1
session_tag=$(jq -er '.image' <<<"$descriptor") || exit 1
state_id=$(jq -er '.shared_state.id' <<<"$descriptor") || exit 1
state_quota=$(jq -er '.shared_state.quota' <<<"$descriptor") || exit 1

scripts/session/load-image.sh "$session_tag" || exit 1

fish_status=0
fish_output=$(fish -c '
  source shell/fish/lib/ai-sandbox.fish
  set -l tag $argv[1]
  set -l state_id $argv[2]
  set -l state_quota $argv[3]

  set -l label_mount_args (__ai_sandbox_shared_state_mount_args probe "$tag"); or exit 1
  if test (count $label_mount_args) -ne 0
      echo "FAIL: label-based lookup unexpectedly found shared-state metadata on a session image" >&2
      exit 1
  end

  set -l descriptor_mount_args (__ai_sandbox_shared_state_request_args probe "$state_id" "$state_quota"); or exit 1
  if test (count $descriptor_mount_args) -ne 2
      echo "FAIL: descriptor-based mount args were not produced" >&2
      exit 1
  end
  __ai_sandbox_initialize_shared_state probe "$tag" $descriptor_mount_args; or exit 1
  echo ok
' -- "$session_tag" "$state_id" "$state_quota") || fish_status=$?
printf '%s\n' "$fish_output" >&2
test "$fish_status" -eq 0 || exit 1
printf '%s\n' "$fish_output" | grep -qx ok || exit 1

# __ai_sandbox_initialize_shared_state created /var/lib/agent-state (0700,
# node-owned) on the named volume; confirm it is actually mounted and usable
# with the same mount args claude-session.fish would compute, and that icm's
# wrapper (scripts/tools/install-selected.sh) creates its own subdirectory
# there on first real use -- deliberately using no arguments (not
# `--version`, which the wrapper bypasses) so this exercises the wrapper's
# state-directory creation, not icm's actual CLI, which is out of scope here.
msb run --pull never --no-tty --user node --security restricted \
  --mount-named "agent-state-$state_id-v1:/var/lib/agent-state:kind=dir,quota=$state_quota" \
  "$session_tag" -- icm >/dev/null 2>&1
msb run --pull never --no-tty --user node --security restricted \
  --mount-named "agent-state-$state_id-v1:/var/lib/agent-state:kind=dir,quota=$state_quota" \
  "$session_tag" -- test -d /var/lib/agent-state/icm \
  || { echo 'icm did not create its state directory under the descriptor-driven mount' >&2; exit 1; }

echo ok
