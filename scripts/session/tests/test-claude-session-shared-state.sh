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

# A developer could legitimately name a real profile's shared_state.id
# "session-tools-verify" (the fixture's literal id). Removing a volume by
# that fixed name on cleanup would delete their real persistent data. Instead,
# derive a per-run profile in a temp file with a unique shared_state.id, and
# only ever remove the volume this run actually created.
profile_tmp=$(mktemp) || exit 1
volumes_list_tmp=$(mktemp) || exit 1
unique_state_id="session-tools-verify-$$-$RANDOM"
state_volume="agent-state-$unique_state_id-v1"
jq --arg id "$unique_state_id" '.shared_state.id = $id' scripts/session/fixtures/valid/icm-with-shared-state.json >"$profile_tmp" || exit 1

# The unique id is not a guarantee: a stale volume from a crashed earlier run,
# or a recycled $$/$RANDOM collision, could leave a volume under this name.
# Mounting it would reuse that volume and cleanup would then delete it, so
# refuse to run unless the volume is verifiably absent first -- and only ever
# remove it in cleanup when this run established that absence.
session_tag=''
state_volume_absent=0
cleanup() {
  if test -n "$session_tag"; then
    docker image rm -f "$session_tag" >/dev/null 2>&1 || true
    msb image remove "$session_tag" >/dev/null 2>&1 || true
  fi
  if test "$state_volume_absent" -eq 1; then
    msb volume remove "$state_volume" >/dev/null 2>&1 || true
  fi
  rm -f "$profile_tmp" "$volumes_list_tmp"
}
trap cleanup EXIT

if ! msb volume list --quiet >"$volumes_list_tmp" 2>/dev/null; then
  echo "refusing to run: could not list msb volumes to verify $state_volume is absent" >&2
  exit 1
fi
if grep -qFx -- "$state_volume" "$volumes_list_tmp"; then
  echo "refusing to run: shared-state volume $state_volume already exists (cleanup would remove it)" >&2
  exit 1
fi
state_volume_absent=1

descriptor=$(CLAUDE_MSB_BUILD_EGRESS=1 scripts/session/resolve-image.sh "$profile_tmp") || exit 1
session_tag=$(jq -er '.image' <<<"$descriptor") || exit 1
state_id=$(jq -er '.shared_state.id' <<<"$descriptor") || exit 1
state_quota=$(jq -er '.shared_state.quota' <<<"$descriptor") || exit 1

scripts/session/load-image.sh "$session_tag" || exit 1

fish_status=0
# The Fish program intentionally owns its variable expansion.
# shellcheck disable=SC2016
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
