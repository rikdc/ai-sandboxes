#!/usr/bin/env bash
# Exercises scripts/install without touching Docker, Go, or msb: the test runs
# a copy of the repository whose individual install-step scripts have been
# replaced by recording stubs, then asserts ordering, failure propagation, and
# the closing output.
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

run_install() {
  PATH="$sandbox:$PATH" "$sandbox/scripts/install" >"$sandbox/out.log" 2>"$sandbox/err.log"
  rc=$?
}

expect_status() {
  local want=$1 title=$2
  if test "$rc" != "$want"; then
    printf -- '--- stdout ---\n' >&2
    cat "$sandbox/out.log" >&2
    printf -- '--- stderr ---\n' >&2
    cat "$sandbox/err.log" >&2
    fail "$title (want exit $want, got $rc)"
  fi
}

# Build a sandbox tree: scripts/install verbatim plus stub stand-ins for the
# four steps it orchestrates. Each stub appends its name to a journal before
# exiting with the code set for it in $exitcodes (0 when none is set).
mkdir -p "$sandbox/scripts" "$sandbox/.git"
cp "$repo_root/scripts/install" "$sandbox/scripts/install" || fail 'could not copy scripts/install'
chmod 0755 "$sandbox/scripts/install"

journal="$sandbox/journal"
exitcodes="$sandbox/exitcodes"
mkdir -p "$exitcodes"
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

# A git stub so the closing revision line works outside a real repository.
cat >"$sandbox/git" <<'EOF' || exit 1
#!/usr/bin/env bash
if test "$1" = rev-parse; then
  printf 'abc1234\n'
  exit 0
fi
exit 0
EOF
chmod 0755 "$sandbox/git" || exit 1

# 1. Success path: all four steps run in order and the fish note appears.
rm -f "$journal"
run_install
expect_status 0 'success run'
test "$(cat "$journal")" = "$(printf 'install-ai-sandbox\nbuild\nverify\nload-msb')" \
  || fail "success run: step order was $(tr '\n' ' ' <"$journal")"
grep -qF './scripts/install-fish-functions' "$sandbox/out.log" \
  || fail 'success run: closing output does not mention install-fish-functions'

expect_stderr() {
  local want=$1 title=$2
  grep -qF -- "$want" "$sandbox/err.log" || fail "$title: stderr missing '$want'"
}

# 2. Failure propagation: a failing build aborts before verify and load-msb,
#    exits with that step's status, and names the failed script.
clear_exit_codes
set_exit_code build 3
rm -f "$journal"
run_install
expect_status 3 'failing build'
test "$(cat "$journal")" = "$(printf 'install-ai-sandbox\nbuild')" \
  || fail "failing build: steps after build ran ($(tr '\n' ' ' <"$journal"))"
expect_stderr 'build failed' 'failing build'
expect_stderr 'scripts/install' 'failing build'

# 3. A failure on the very first step runs nothing else.
clear_exit_codes
set_exit_code install-ai-sandbox 1
rm -f "$journal"
run_install
expect_status 1 'failing first step'
test "$(cat "$journal")" = 'install-ai-sandbox' \
  || fail "failing first step: later steps ran ($(tr '\n' ' ' <"$journal"))"

# 4. A failure on the final step still reports failure even though everything
#    before it succeeded.
clear_exit_codes
set_exit_code load-msb 7
rm -f "$journal"
run_install
expect_status 7 'failing last step'
test "$(cat "$journal")" = "$(printf 'install-ai-sandbox\nbuild\nverify\nload-msb')" \
  || fail "failing last step: unexpected journal $(tr '\n' ' ' <"$journal")"

printf '%s\n' 'test-install: all scenarios passed'
