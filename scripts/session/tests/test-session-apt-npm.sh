#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../../.." || exit 1

if ! docker image inspect ai-sandboxes-claude:local >/dev/null 2>&1; then
  echo 'skip: ai-sandboxes-claude:local not built (run ./scripts/build)' >&2
  exit 0
fi

session_tag=''
cleanup() {
  if test -n "$session_tag"; then
    docker image rm -f "$session_tag" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

session_tag=$(CLAUDE_MSB_BUILD_EGRESS=1 scripts/session/resolve-image.sh scripts/session/fixtures/valid/apt-npm-packages.json)

docker run --rm --user node "$session_tag" tree --version >/dev/null \
  || { echo 'apt-installed tree binary does not run' >&2; exit 1; }

recorded_version=$(docker run --rm --user node "$session_tag" sh -c "jq -r '.packages.apt[] | select(.name==\"tree\") | .version' /opt/session-profile/resolved.json")
actual_version=$(docker run --rm --user root "$session_tag" dpkg-query -W -f='${Version}' tree)
test -n "$recorded_version" \
  || { echo 'resolved.json has no recorded apt version for tree' >&2; exit 1; }
test "$recorded_version" = "$actual_version" \
  || { echo "resolved.json apt version ($recorded_version) does not match dpkg ($actual_version)" >&2; exit 1; }

cowsay_output=$(docker run --rm --user node "$session_tag" cowsay moo 2>&1) \
  || { echo 'npm-installed cowsay binary does not run via PATH' >&2; printf '%s\n' "$cowsay_output" >&2; exit 1; }
printf '%s\n' "$cowsay_output" | grep -q moo \
  || { echo 'cowsay output missing expected text' >&2; exit 1; }

docker run --rm --user node "$session_tag" sh -c '! touch /opt/claude-session/npm/.write-test 2>/dev/null' \
  || { echo 'npm prefix is writable by node' >&2; exit 1; }
docker run --rm --user node "$session_tag" sh -c '! touch /usr/bin/.write-test 2>/dev/null' \
  || { echo 'apt system path is writable by node' >&2; exit 1; }

echo ok
