#!/usr/bin/env bash
set -o pipefail
cd "$(dirname "$0")/../../.." || exit 1

if ! docker image inspect ai-sandboxes-claude:local >/dev/null 2>&1; then
  echo 'skip: ai-sandboxes-claude:local not built (run ./scripts/build)' >&2
  exit 0
fi

stderr_file=$(mktemp) || exit 1
cleanup() {
  rm -f "$stderr_file"
  if test -n "${tag_empty:-}"; then
    docker image rm -f "$tag_empty" >/dev/null 2>&1 || true
  fi
  if test -n "${tag_icm:-}"; then
    docker image rm -f "$tag_icm" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

if scripts/session/resolve-image.sh scripts/session/fixtures/valid/empty.json >/dev/null 2>"$stderr_file"; then
  echo 'FAIL: cache miss should require CLAUDE_MSB_BUILD_EGRESS=1' >&2
  exit 1
fi
grep -q CLAUDE_MSB_BUILD_EGRESS "$stderr_file" || exit 1

descriptor_empty=$(CLAUDE_MSB_BUILD_EGRESS=1 scripts/session/resolve-image.sh scripts/session/fixtures/valid/empty.json) || exit 1
tag_empty=$(jq -er '.image' <<<"$descriptor_empty") || exit 1
docker image inspect "$tag_empty" >/dev/null || exit 1
jq -e '.shared_state == null' <<<"$descriptor_empty" >/dev/null \
  || { echo 'FAIL: empty profile should have a null shared_state in its descriptor' >&2; exit 1; }

descriptor_empty_again=$(scripts/session/resolve-image.sh scripts/session/fixtures/valid/empty.json) || exit 1
test "$descriptor_empty" = "$descriptor_empty_again" || exit 1

descriptor_icm=$(CLAUDE_MSB_BUILD_EGRESS=1 scripts/session/resolve-image.sh scripts/session/fixtures/valid/icm-with-shared-state.json) || exit 1
tag_icm=$(jq -er '.image' <<<"$descriptor_icm") || exit 1
jq -e '.shared_state.id == "personal" and .shared_state.quota == "2G"' <<<"$descriptor_icm" >/dev/null \
  || { echo 'FAIL: icm-with-shared-state descriptor did not carry the requested shared_state' >&2; exit 1; }

echo ok
