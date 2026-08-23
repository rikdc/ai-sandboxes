#!/usr/bin/env bash
# Release-marker self-check exercised by scripts/verify. Kept in its own
# script so scripts/tests/test-release-marker-check.sh can prove that a
# failing marker command fails verification instead of being skipped over.
# Every invocation below is fatal on purpose: the marker tool signals failure
# through its exit code (0 changed/valid, 1 unchanged/invalid, 2 error,
# 64 usage), and verify must not wander past a failed check.
set -o pipefail
cd "$(dirname "$0")/../.." || exit 1

release_marker_test=$(mktemp) || exit 1
release_marker_previous=$(mktemp) || exit 1
release_marker_next=$(mktemp) || exit 1
cleanup() {
  rm -f "$release_marker_test" "$release_marker_previous" "$release_marker_next"
}
trap cleanup EXIT

# shellcheck disable=SC1091 # Resolved from the repository root above.
. ./versions.env || exit 1

./.github/workflows/release-marker create \
  "$release_marker_test" \
  aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
  "$BASE_VERSION" 0.145.0 2.1.224 1.18.21 \
  2026-08-09T20:00:00Z || exit 1
./.github/workflows/release-marker validate \
  "$release_marker_test" \
  aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
  "$BASE_VERSION" 0.145.0 2.1.224 1.18.21 || exit 1
printf '%s\n' 'BASE_VERSION=0.1.0-alpha' 'CODEX_VERSION=0.144.0' 'CLAUDE_CODE_VERSION=2.1.224' 'OPENCODE_VERSION=1.18.20' >"$release_marker_previous"
# An agent-version bump is a release...
printf '%s\n' 'BASE_VERSION=0.1.0-alpha' 'CODEX_VERSION=0.145.0' 'CLAUDE_CODE_VERSION=2.1.224' 'OPENCODE_VERSION=1.18.20' >"$release_marker_next"
./.github/workflows/release-marker changed \
  "$release_marker_previous" "$release_marker_next" || exit 1
# ...and so is a control-plane base bump on its own: BASE_VERSION must
# participate in `changed` or a project release would never publish.
printf '%s\n' 'BASE_VERSION=0.1.0-beta' 'CODEX_VERSION=0.144.0' 'CLAUDE_CODE_VERSION=2.1.224' 'OPENCODE_VERSION=1.18.20' >"$release_marker_next"
./.github/workflows/release-marker changed \
  "$release_marker_previous" "$release_marker_next" || exit 1
# ...and an OpenCode bump on its own must count too.
printf '%s\n' 'BASE_VERSION=0.1.0-alpha' 'CODEX_VERSION=0.144.0' 'CLAUDE_CODE_VERSION=2.1.224' 'OPENCODE_VERSION=1.18.21' >"$release_marker_next"
./.github/workflows/release-marker changed \
  "$release_marker_previous" "$release_marker_next" || exit 1
if ./.github/workflows/release-marker changed versions.env versions.env; then
  echo 'release marker reported an unchanged versions.env as changed' >&2
  exit 1
fi
