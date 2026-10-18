#!/usr/bin/env bash
# Exercises scripts/build-release end to end: it actually cross-compiles the
# control plane (go is a hard prerequisite), so each scenario below points
# OUTPUT_DIR at a fresh temp directory rather than mocking go.
set -o pipefail
cd "$(dirname "$0")/../.." || exit 1

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

dist=$(mktemp -d) || exit 1
cleanup() {
  rm -rf "$dist"
}
trap cleanup EXIT

run_build() {
  ./scripts/build-release "$1" "$2" >"$dist/out.log" 2>"$dist/err.log"
  rc=$?
  return "$rc"
}

# 1. Fresh build: tarball and checksum exist, the extracted binary runs on
#    this host's toolchain output expectations, and it reports the stamped
#    revision.
run_build "$dist" deadbeefdeadbeefdeadbeefdeadbeefdeadbeef \
  || fail "fresh build failed (rc=$rc): $(cat "$dist/err.log")"
test -f "$dist/ai-sandbox-darwin-arm64.tar.gz" || fail 'tarball missing'
test -f "$dist/ai-sandbox-darwin-arm64.tar.gz.sha256" || fail 'checksum file missing'
grep -qF 'ai-sandbox-darwin-arm64.tar.gz' "$dist/ai-sandbox-darwin-arm64.tar.gz.sha256" \
  || fail 'checksum file does not name the tarball'

extract="$dist/extract"
mkdir -p "$extract"
tar -xzf "$dist/ai-sandbox-darwin-arm64.tar.gz" -C "$extract" || fail 'could not extract tarball'
test -x "$extract/ai-sandbox" || fail 'tarball does not contain an executable ai-sandbox'

# The artifact targets darwin/arm64; it can only be executed on that host.
# Everywhere else (e.g. the ubuntu-latest release runner) packaging plus a
# successful native build is the strongest check available, and the complete
# smoke test stays a manual step on the supported machine.
if [ "$(uname -s)" = Darwin ] && [ "$(uname -m)" = arm64 ]; then
  version=$("$extract/ai-sandbox" version 2>/dev/null) || version=''
  case "$version" in
    'ai-sandbox '*) ;;
    *) fail "binary reported unexpected version: $version" ;;
  esac
  case "$version" in
    *'deadbeef'*) ;;
    *) fail "binary does not carry the stamped revision: $version" ;;
  esac
  # Both stamped values must be asserted exactly: the artifact reports the
  # release it is attached to (BASE_VERSION from versions.env), not a stale
  # hardcode, alongside the revision.
  # shellcheck disable=SC1091 # Resolved from the repository root above.
  . ./versions.env || fail 'could not read versions.env'
  test -n "$BASE_VERSION" || fail 'versions.env defines no BASE_VERSION'
  test "$version" = "ai-sandbox $BASE_VERSION (revision deadbeefdeadbeefdeadbeefdeadbeefdeadbeef)" \
    || fail "binary does not report the stamped release and revision: $version"
fi

# 2. Wrong argument count is a usage error.
if ./scripts/build-release "$dist" >/dev/null 2>&1; then
  fail 'missing REVISION should be rejected'
fi

# 3. An empty revision is rejected rather than stamped silently.
if ./scripts/build-release "$dist" '' >/dev/null 2>&1; then
  fail 'empty REVISION should be rejected'
fi
