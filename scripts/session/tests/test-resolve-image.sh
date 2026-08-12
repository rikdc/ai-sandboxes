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
}
trap cleanup EXIT

if scripts/session/resolve-image.sh scripts/session/fixtures/valid/empty.json >/dev/null 2>"$stderr_file"; then
  echo 'FAIL: cache miss should require CLAUDE_MSB_BUILD_EGRESS=1' >&2
  exit 1
fi
grep -q CLAUDE_MSB_BUILD_EGRESS "$stderr_file" || exit 1

tag_empty=$(CLAUDE_MSB_BUILD_EGRESS=1 scripts/session/resolve-image.sh scripts/session/fixtures/valid/empty.json) || exit 1
docker image inspect "$tag_empty" >/dev/null || exit 1

tag_empty_again=$(scripts/session/resolve-image.sh scripts/session/fixtures/valid/empty.json) || exit 1
test "$tag_empty" = "$tag_empty_again" || exit 1

echo ok
