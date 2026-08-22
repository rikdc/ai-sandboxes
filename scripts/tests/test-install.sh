#!/usr/bin/env bash
# Exercises scripts/install without touching Docker, Go, or msb: the test runs
# a copy of the repository whose individual install-step scripts have been
# replaced by recording stubs, then asserts the preflight gate, ordering,
# failure propagation, reconciliation-marker bookkeeping, and the closing
# output.
set -o pipefail
cd "$(dirname "$0")/../.." || exit 1
repo_root=$(pwd)

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

sandbox=$(mktemp -d) || exit 1
cleanup() {
  rm -rf "$sandbox"
}
trap cleanup EXIT

state_dir="$sandbox/state"
export XDG_STATE_HOME="$state_dir"
marker="$state_dir/ai-sandboxes/installed-head"

journal="$sandbox/journal"
exitcodes="$sandbox/exitcodes"
mkdir -p "$exitcodes" "$sandbox/scripts" "$sandbox/.git" || exit 1

cp "$repo_root/scripts/install" "$sandbox/scripts/install" || fail 'could not copy scripts/install'
chmod 0755 "$sandbox/scripts/install"

# Recording stubs for the four orchestrated steps. Each appends its name to
# the journal before exiting with the code set for it in $exitcodes (0 when
# none is set).
for step in install-ai-sandbox build verify load-msb; do
  cat >"$sandbox/scripts/$step" <<EOF || exit 1
#!/usr/bin/env bash
printf '%s\n' '$step' >>'$journal'
code=\$(cat '$exitcodes/$step' 2>/dev/null) || code=0
exit "\$code"
EOF
  chmod 0755 "$sandbox/scripts/$step" || exit 1
done

set_exit_code() {
  local step=$1 code=$2
  printf '%s\n' "$code" >"$exitcodes/$step" || exit 1
}

clear_exit_codes() {
  rm -f "$exitcodes"/* || exit 1
}

# Controlled stand-ins for every host tool the preflight probes. The rest of
# the host PATH is exposed through a symlink farm that drops exactly these
# names, so an "absent" tool can never fall through to the real one.
bindir="$sandbox/bin"
mkdir -p "$bindir" || exit 1
cat >"$bindir/git" <<'EOF' || exit 1
#!/usr/bin/env bash
if test "${1:-}" = rev-parse; then
  printf 'abc1234\n'
fi
exit 0
EOF
cat >"$bindir/go" <<'EOF' || exit 1
#!/usr/bin/env bash
exit 0
EOF
cat >"$bindir/docker" <<EOF || exit 1
#!/usr/bin/env bash
case "\$1" in
  info) exit "\${DOCKER_INFO_STATUS:-0}" ;;
  version|buildx) exit "\${DOCKER_BUILDX_STATUS:-0}" ;;
esac
exit 0
EOF
cat >"$bindir/msb" <<'EOF' || exit 1
#!/usr/bin/env bash
exit 0
EOF
cat >"$bindir/uname" <<'EOF' || exit 1
#!/usr/bin/env bash
printf '%s\n' "${UNAME_ARCH:-arm64}"
EOF
chmod 0755 "$bindir"/* || exit 1

base_path=$PATH
farm="$sandbox/path-farm"
mkdir -p "$farm" || exit 1
old_ifs=$IFS
IFS=: read -r -a base_dirs <<<"$base_path"
for dir in "${base_dirs[@]}"; do
  test -d "$dir" || continue
  for src in "$dir"/*; do
    name=${src##*/}
    case "$name" in
      git | go | docker | msb | uname) continue ;;
    esac
    test -f "$src" && test -x "$src" || continue
    if test ! -e "$farm/$name"; then
      ln -s "$src" "$farm/$name"
    fi
  done
done
IFS=$old_ifs

run_install() {
  : >"$journal"
  rm -f "$marker"
  PATH="$bindir:$farm" "$sandbox/scripts/install" >"$sandbox/out.log" 2>"$sandbox/err.log"
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

expect_stderr() {
  local want=$1 title=$2
  grep -qF -- "$want" "$sandbox/err.log" || fail "$title: stderr missing '$want'"
}

expect_journal() {
  local title=$1 want=$2
  test "$(cat "$journal")" = "$want" \
    || fail "$title: unexpected journal $(tr '\n' ' ' <"$journal")"
}

# 1. A missing prerequisite aborts before any mutation: no step runs, the
#    message names the tool, and nothing is recorded as installed.
clear_exit_codes
rm -f "$bindir/msb"
run_install
expect_status 1 'missing msb preflight'
expect_journal 'missing msb preflight' ''
expect_stderr "prerequisite 'msb' not found" 'missing msb preflight message'
test ! -e "$marker" || fail 'missing msb preflight must not write the marker'
cat >"$bindir/msb" <<'EOF' || exit 1
#!/usr/bin/env bash
exit 0
EOF
chmod 0755 "$bindir/msb"

for tool in git go docker; do
  mv "$bindir/$tool" "$bindir/$tool.hidden"
  run_install
  expect_status 1 "missing $tool preflight"
  expect_journal "missing $tool preflight" ''
  expect_stderr "prerequisite '$tool' not found" "missing $tool preflight message"
  mv "$bindir/$tool.hidden" "$bindir/$tool"
done

# 2. An unreachable Docker daemon is caught in the preflight too.
DOCKER_INFO_STATUS=1 run_install
expect_status 1 'unreachable docker daemon'
expect_journal 'unreachable docker daemon' ''
expect_stderr 'docker daemon is not reachable' 'unreachable daemon message'

# 3. So is a missing buildx plugin.
DOCKER_BUILDX_STATUS=1 run_install
expect_status 1 'missing buildx'
expect_journal 'missing buildx' ''
expect_stderr 'docker buildx plugin not found' 'missing buildx message'

# 4. A non-arm64 host is rejected up front.
UNAME_ARCH=x86_64 run_install
expect_status 1 'unsupported architecture'
expect_journal 'unsupported architecture' ''
expect_stderr 'unsupported architecture' 'architecture message'

# 5. Success path: all four steps run — load-msb before verify, so images are
#    imported exactly once — and the reconciliation marker records HEAD.
clear_exit_codes
UNAME_ARCH=arm64 run_install
expect_status 0 'success run'
expect_journal 'success run' "$(printf 'install-ai-sandbox\nbuild\nload-msb\nverify')"
grep -qF './scripts/install-fish-functions' "$sandbox/out.log" \
  || fail 'success run: closing output does not mention install-fish-functions'
test "$(cat "$marker")" = 'abc1234' || fail "marker contents were $(cat "$marker" 2>/dev/null)"
test ! -e "$state_dir/ai-sandboxes/fish-wrappers-head" \
  || fail 'install must not mark fish wrappers as managed'

# 6. Failure propagation: a failing build aborts before load-msb and verify,
#    exits with that step's status, names the failed script — and does not
#    write the marker, so update --check keeps reporting incomplete state.
set_exit_code build 3
run_install
expect_status 3 'failing build'
expect_journal 'failing build' "$(printf 'install-ai-sandbox\nbuild')"
expect_stderr 'build failed' 'failing build'
expect_stderr 'scripts/install' 'failing build'
test ! -e "$marker" || fail 'failed install must not write the marker'

# 7. A failure on the very first step runs nothing else.
set_exit_code install-ai-sandbox 1
run_install
expect_status 1 'failing first step'
expect_journal 'failing first step' 'install-ai-sandbox'
test ! -e "$marker" || fail 'failed first step must not write the marker'

# 8. A failure on the final step still reports failure even though everything
#    before it succeeded.
clear_exit_codes
set_exit_code verify 7
run_install
expect_status 7 'failing last step'
expect_journal 'failing last step' "$(printf 'install-ai-sandbox\nbuild\nload-msb\nverify')"
test ! -e "$marker" || fail 'failed final step must not write the marker'

printf '%s\n' 'test-install: all scenarios passed'
