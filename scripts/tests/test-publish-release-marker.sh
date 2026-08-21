#!/usr/bin/env bash
set -o pipefail
cd "$(dirname "$0")/../.." || exit 1

# shellcheck disable=SC1091 # Resolved after cd to the repository root.
. ./versions.env

mockdir=$(mktemp -d) || exit 1
logfile="$mockdir/gh.log"
caught="$mockdir/caught.json"
existing_marker="$mockdir/existing.json"
cleanup() {
  rm -rf "$mockdir"
}
trap cleanup EXIT

commit='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
other_commit='bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'

mkdir -p "$mockdir/bin"
cat >"$mockdir/bin/gh" <<'SH'
#!/usr/bin/env bash
set -u
printf '%s\n' "gh $*" >>"$MOCK_GH_LOG"
cmd=$1
sub=$2
shift 2
case "$cmd:$sub" in
  release:view)
    payload="{\"targetCommitish\":\"$MOCK_VIEW_COMMIT\"}"
    filter=''
    while test "$#" -gt 0; do
      if test "$1" = --jq; then
        filter=$2
        shift 2
      else
        shift
      fi
    done
    if test -n "$filter"; then
      printf '%s\n' "$payload" | jq -r "$filter"
    else
      printf '%s\n' "$payload"
    fi
    exit "${MOCK_VIEW_STATUS:-0}"
    ;;
  release:download)
    dir=''
    while test "$#" -gt 0; do
      if test "$1" = --dir; then
        dir=$2
        shift 2
      else
        shift
      fi
    done
    test -n "$dir" || exit 2
    cp "${MOCK_MARKER_FILE:?}" "$dir/release.json"
    ;;
  api:*)
    exit "${MOCK_TAG_STATUS:-1}"
    ;;
  release:create)
    last=''
    for arg in "$@"; do last=$arg; done
    if test -n "$last" && test -n "${MOCK_CAUGHT:-}"; then
      cp "$last" "$MOCK_CAUGHT" 2>/dev/null || true
    fi
    exit 0
    ;;
  *) exit 1 ;;
esac
SH
chmod +x "$mockdir/bin/gh"

cat >"$mockdir/bin/date" <<'SH'
#!/usr/bin/env bash
printf '%s\n' '2026-08-09T20:00:00Z'
SH
chmod +x "$mockdir/bin/date"

# Marker an already-published release would have stored.
cat >"$existing_marker" <<JSON
{"schema_version":2,"upstream_commit":"$commit","base_version":"$BASE_VERSION","codex_version":"$CODEX_VERSION","claude_code_version":"$CLAUDE_CODE_VERSION","created_at":"2026-08-09T20:00:00Z"}
JSON

publish() {
  env "$@" GITHUB_REPOSITORY=testowner/testrepo \
    MOCK_GH_LOG="$logfile" MOCK_CAUGHT="$caught" \
    PATH="$mockdir/bin:$PATH" .github/workflows/publish-release-marker "$commit"
}

# 1. Fresh release: publishes and uploads an immutable, valid marker.
: >"$logfile"
rm -f "$caught"
publish MOCK_VIEW_STATUS=1 MOCK_TAG_STATUS=1 || {
  echo 'FAIL: fresh release should publish' >&2
  exit 1
}
grep -q 'gh release create ' "$logfile" || {
  echo 'FAIL: fresh release should call gh release create' >&2
  exit 1
}
grep -q 'gh release download' "$logfile" && {
  echo 'FAIL: fresh release should not download' >&2
  exit 1
}
test -f "$caught" || {
  echo 'FAIL: publish did not hand a marker file to gh release create' >&2
  exit 1
}
jq -e --arg c "$commit" --arg base "$BASE_VERSION" --arg cdx "$CODEX_VERSION" --arg cld "$CLAUDE_CODE_VERSION" \
  '.upstream_commit == $c and .base_version == $base and .codex_version == $cdx and .claude_code_version == $cld' \
  "$caught" >/dev/null || {
  echo 'FAIL: created marker does not match versions.env' >&2
  exit 1
}

# 2. Existing release on the same commit: idempotent no-op success.
: >"$logfile"
rm -f "$caught"
publish MOCK_VIEW_COMMIT="$commit" MOCK_MARKER_FILE="$existing_marker" || {
  echo 'FAIL: re-publishing the same commit should succeed' >&2
  exit 1
}
grep -q 'gh release download' "$logfile" || {
  echo 'FAIL: existing release should be revalidated via download' >&2
  exit 1
}
grep -q 'gh release create ' "$logfile" && {
  echo 'FAIL: existing release should not be re-created' >&2
  exit 1
}

# 3. Existing release pointing at a different commit: must refuse.
: >"$logfile"
if publish MOCK_VIEW_COMMIT="$other_commit" >/dev/null 2>&1; then
  echo 'FAIL: divergent existing release should fail' >&2
  exit 1
fi

# 4. Tag exists without a release: must refuse.
: >"$logfile"
if publish MOCK_VIEW_STATUS=1 MOCK_TAG_STATUS=0 >/dev/null 2>&1; then
  echo 'FAIL: dangling tag should fail' >&2
  exit 1
fi

# 5. GITHUB_REPOSITORY is required.
if env -u GITHUB_REPOSITORY PATH="$mockdir/bin:$PATH" \
  .github/workflows/publish-release-marker "$commit" >/dev/null 2>&1; then
  echo 'FAIL: missing GITHUB_REPOSITORY should fail' >&2
  exit 1
fi

echo ok