#!/usr/bin/env bash
set -o pipefail
cd "$(dirname "$0")/../../.." || exit 1

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

build_dir=$(mktemp -d) || exit 1
printf 'FROM scratch\nCOPY resolved.json /resolved.json\n' >"$build_dir/Dockerfile" || exit 1
echo '{}' >"$build_dir/resolved.json" || exit 1
docker buildx build --load --tag "$test_tag" "$build_dir" >/dev/null || exit 1
rm -rf "$build_dir" || exit 1

claude_present_before=false
msb image list --quiet | grep -Fxq ai-sandboxes-claude:local && claude_present_before=true

scripts/session/load-image.sh "$test_tag" || exit 1
msb image list --quiet | grep -Fxq "$test_tag" || exit 1

# msb currently drops OCI labels while loading, so session launchers compare
# the OCI config digest retained by msb with Docker's image ID. Keep that
# contract tested: a same-tag image in msb must be detectable if it is not the
# exact Docker image the resolver built.
docker_config_digest=$(docker image inspect --format '{{.Id}}' "$test_tag") || exit 1
msb_config_digest=$(msb image inspect --format json "$test_tag" | jq -er '.config.digest') || exit 1
test "$msb_config_digest" = "$docker_config_digest" || {
  echo 'msb image config digest does not match the Docker image ID' >&2
  exit 1
}

# Second call must skip (already present) rather than remove-and-reload.
scripts/session/load-image.sh "$test_tag" || exit 1
msb image list --quiet | grep -Fxq "$test_tag" || exit 1

claude_present_after=false
msb image list --quiet | grep -Fxq ai-sandboxes-claude:local && claude_present_after=true
test "$claude_present_before" = "$claude_present_after" || exit 1

echo ok
