#!/usr/bin/env bash
# Exercises scripts/build without Docker: a stubbed docker records the bake
# invocation, and the scenarios assert that a failed bake propagates its exit
# code instead of falling through to the (passing) post-build digest checks —
# the regression that once let install/update verify stale images and record
# them as current.
set -o pipefail
cd "$(dirname "$0")/../.." || exit 1

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

sandbox=$(mktemp -d) || exit 1
cleanup() {
  rm -rf "$sandbox"
}
trap cleanup EXIT

repo_root=$(pwd)
state_dir="$sandbox/state"
export XDG_STATE_HOME="$state_dir"
export AI_SANDBOX_CONFIG_DIR="$sandbox/userconfig"

# Minimal checkout copy: the build script itself, the resolver library, the
# tools validators as recording stubs, and the files it reads at rest.
bindir="$sandbox/bin"
mkdir -p "$bindir" "$sandbox/scripts/lib" "$sandbox/scripts/tools" || exit 1
cp "$repo_root/scripts/build" "$sandbox/scripts/build" || fail 'could not copy scripts/build'
cp "$repo_root/scripts/lib/config-dir.sh" "$sandbox/scripts/lib/config-dir.sh" \
  || fail 'could not copy scripts/lib/config-dir.sh'
printf 'CODEX_VERSION=0.0.0\nCLAUDE_CODE_VERSION=0.0.0\n' >"$sandbox/versions.env" || exit 1
mkdir -p "$sandbox/config" || exit 1
for f in tool-catalog.json marketplaces.json tools.json runtime.json; do
  printf '{}\n' >"$sandbox/config/$f" || exit 1
done

cat >"$sandbox/scripts/tools/validate-selection.sh" <<'EOF' || exit 1
#!/usr/bin/env bash
exit 0
EOF
cat >"$sandbox/scripts/tools/runtime-values.sh" <<'EOF' || exit 1
#!/usr/bin/env bash
exit 0
EOF

cat >"$bindir/docker" <<'EOF' || exit 1
#!/usr/bin/env bash
if test "${1:-}" = buildx && test "${2:-}" = bake; then
  printf 'bake\n' >>"$BAKE_JOURNAL"
  exit "${BAKE_STATUS:-0}"
fi
exit 0
EOF
chmod 0755 "$bindir/docker" "$sandbox/scripts/build" \
  "$sandbox/scripts/tools/validate-selection.sh" "$sandbox/scripts/tools/runtime-values.sh"

run_build() {
  : >"$sandbox/bake-journal"
  BAKE_JOURNAL="$sandbox/bake-journal" \
    PATH="$bindir:/usr/bin:/bin" \
    bash "$sandbox/scripts/build" >"$sandbox/out.log" 2>"$sandbox/err.log"
  rc=$?
}

expect_status() {
  local want=$1 title=$2
  if test "$rc" != "$want"; then
    {
      printf '%s\n' '--- stdout ---'
      cat "$sandbox/out.log"
      printf '%s\n' '--- stderr ---'
      cat "$sandbox/err.log"
    } >&2
    fail "$title (want exit $want, got $rc)"
  fi
}

# 1. A successful bake exits zero and runs exactly once.
run_build
expect_status 0 'successful build'
test "$(cat "$sandbox/bake-journal")" = 'bake' || fail 'build must invoke bake exactly once'

# 2. Regression: a failed bake must abort the script. Without the explicit
#    `|| exit 1`, the script fell through to the post-build digest checks —
#    which succeed on unchanged configuration — and returned zero, letting
#    install/update load and record stale images.
BAKE_STATUS=42 run_build
expect_status 1 'failed bake aborts build'
test "$(cat "$sandbox/bake-journal")" = 'bake' || fail 'failed build must not retry or continue past bake'

printf '%s\n' 'test-build: all scenarios passed'
