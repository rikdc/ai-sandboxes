#!/usr/bin/env bash
# Exercises scripts/install-ai-sandbox end to end: it actually builds the
# binary (go is a hard prerequisite of the script itself), so each scenario
# below points AI_SANDBOX_INSTALL_DIR / AI_SANDBOX_BIN_DIR at fresh temp
# directories rather than mocking go.
set -o pipefail
cd "$(dirname "$0")/../.." || exit 1
repo_root=$(pwd)

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

mockdir=$(mktemp -d) || exit 1
cleanup() {
  rm -rf "$mockdir"
}
trap cleanup EXIT

run_install() {
  local install_dir=$1 bin_dir=$2
  AI_SANDBOX_INSTALL_DIR="$install_dir" AI_SANDBOX_BIN_DIR="$bin_dir" \
    "$repo_root/scripts/install-ai-sandbox" >"$mockdir/out.log" 2>"$mockdir/err.log"
  rc=$?
}

expect_status() {
  local want=$1 title=$2
  if test "$rc" != "$want"; then
    printf -- '--- stdout ---\n' >&2
    cat "$mockdir/out.log" >&2
    printf -- '--- stderr ---\n' >&2
    cat "$mockdir/err.log" >&2
    fail "$title (want exit $want, got $rc)"
  fi
}

# 1. Fresh installation: binary and symlink both created, symlink resolves
#    to the binary, built binary reports its version.
install1="$mockdir/install1"
bin1="$mockdir/bin1"
run_install "$install1" "$bin1"
expect_status 0 'fresh install'
test -x "$install1/ai-sandbox" || fail 'fresh install: binary missing'
test -L "$bin1/ai-sandbox" || fail 'fresh install: symlink missing'
target=$(readlink "$bin1/ai-sandbox") || fail 'fresh install: could not read symlink'
test "$target" = "$install1/ai-sandbox" || fail "fresh install: symlink target = $target, want $install1/ai-sandbox"
"$bin1/ai-sandbox" version 2>/dev/null | grep -q '^ai-sandbox ' || fail 'fresh install: binary via symlink does not report a version'

# 2. Idempotent reinstallation: running again against the same dirs succeeds
#    and the symlink still resolves correctly.
run_install "$install1" "$bin1"
expect_status 0 'idempotent reinstall'
target=$(readlink "$bin1/ai-sandbox") || fail 'idempotent reinstall: could not read symlink'
test "$target" = "$install1/ai-sandbox" || fail "idempotent reinstall: symlink target = $target, want $install1/ai-sandbox"

# 3. Custom install and bin directories (distinct from the defaults and from
#    each other) both get used correctly.
install2="$mockdir/nested/custom-install"
bin2="$mockdir/nested/custom-bin"
run_install "$install2" "$bin2"
expect_status 0 'custom install and bin dirs'
test -x "$install2/ai-sandbox" || fail 'custom dirs: binary missing at custom install dir'
target=$(readlink "$bin2/ai-sandbox") || fail 'custom dirs: could not read symlink'
test "$target" = "$install2/ai-sandbox" || fail "custom dirs: symlink target = $target, want $install2/ai-sandbox"

# 4. An existing conflicting regular file at the symlink path: install must
#    refuse rather than silently deleting the user's file, and must fail
#    before replacing the (nonexistent, in this case) libexec binary.
install3="$mockdir/install3"
bin3="$mockdir/bin3"
mkdir -p "$bin3"
printf 'not an ai-sandbox binary\n' >"$bin3/ai-sandbox"
before_sum=$(cat "$bin3/ai-sandbox")
run_install "$install3" "$bin3"
if test "$rc" = 0; then
  fail 'conflicting regular file at symlink path: install should have refused'
fi
grep -Fq 'ai-sandbox' "$mockdir/err.log" || fail 'conflicting regular file: stderr should name the conflict'
after_sum=$(cat "$bin3/ai-sandbox")
test "$before_sum" = "$after_sum" || fail 'conflicting regular file: the pre-existing file must not be modified'
test -e "$install3/ai-sandbox" && fail 'conflicting regular file: libexec binary must not be installed when the symlink preflight fails'

# 5. Symlinked directory layout: AI_SANDBOX_BIN_DIR itself is a symlink to
#    another directory; install must follow it rather than refusing.
real_bin4="$mockdir/real-bin4"
mkdir -p "$real_bin4"
bin4_link="$mockdir/bin4-link"
ln -s "$real_bin4" "$bin4_link"
install4="$mockdir/install4"
run_install "$install4" "$bin4_link"
expect_status 0 'bin dir is itself a symlink'
test -L "$real_bin4/ai-sandbox" || fail 'symlinked bin dir: symlink not created through the directory symlink'

echo ok
