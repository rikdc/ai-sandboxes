#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../../.."

if ! docker image inspect ai-sandboxes-claude:local >/dev/null 2>&1; then
  echo 'skip: ai-sandboxes-claude:local not built (run ./scripts/build)' >&2
  exit 0
fi

cleanup() {
  test -n "${tag_empty:-}" && docker image rm -f "$tag_empty" >/dev/null 2>&1 || true
}
trap cleanup EXIT

if scripts/session/resolve-image.sh scripts/session/fixtures/valid/full.json >/dev/null 2>/tmp/resolve-image-stderr.$$; then
  echo 'FAIL: cache miss should require CLAUDE_MSB_BUILD_EGRESS=1' >&2
  exit 1
fi
grep -q CLAUDE_MSB_BUILD_EGRESS /tmp/resolve-image-stderr.$$
rm -f /tmp/resolve-image-stderr.$$

tag_empty=$(CLAUDE_MSB_BUILD_EGRESS=1 scripts/session/resolve-image.sh scripts/session/fixtures/valid/empty.json)
docker image inspect "$tag_empty" >/dev/null

tag_empty_again=$(scripts/session/resolve-image.sh scripts/session/fixtures/valid/empty.json)
test "$tag_empty" = "$tag_empty_again"

echo ok
