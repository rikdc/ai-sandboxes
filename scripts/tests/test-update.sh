#!/usr/bin/env bash
set -o pipefail
cd "$(dirname "$0")/../.." || exit 1

# shellcheck disable=SC1091 # Resolved after cd to the repository root.
. ./versions.env

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

mockdir=$(mktemp -d) || exit 1
cleanup() {
  rm -rf "$mockdir"
}
trap cleanup EXIT

base_path=$PATH
sanitized_path() {
  # Mirror the host PATH but without the real `docker` and `msb` binary names,
  # so a scenario where they must be "absent" can never fall through to the
  # host's. We drop the two named binaries as individuals via a symlink farm
  # rather than removing whole directories: on Linux docker shares /usr/bin
  # with coreutils (cat, basename, dirname, mktemp), so dropping the directory
  # would starve the scripts under test of basic tools.
  local dir name linkdir="$mockdir/path" dirs
  mkdir -p "$linkdir"
  IFS=: read -r -a dirs <<<"$base_path"
  for dir in "${dirs[@]}"; do
    test -d "$dir" || continue
    for src in "$dir"/*; do
      if ! test -f "$src" || ! test -x "$src"; then
        continue
      fi
      name=${src##*/}
      if test "$name" = docker || test "$name" = msb; then
        continue
      fi
      if test ! -e "$linkdir/$name"; then
        ln -s "$src" "$linkdir/$name"
      fi
    done
  done
  printf '%s\n' "$linkdir"
}
base_sanitized=$(sanitized_path)

repo="$mockdir/repo"
mkdir -p "$repo/scripts" "$repo/.github/workflows" "$mockdir/gitbin" "$mockdir/extras/bin"
cp scripts/update "$repo/scripts/update"
cp .github/workflows/release-marker "$repo/.github/workflows/release-marker"
cp versions.env "$repo/versions.env"

old_head='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
new_head='bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'
third_head='cccccccccccccccccccccccccccccccccccccccc'

git_log="$mockdir/git.log"
op_log="$mockdir/op.log"
out_log="$mockdir/out.log"
err_log="$mockdir/err.log"
head_state="$mockdir/head"

state_dir="$mockdir/state"
mkdir -p "$state_dir"
export XDG_STATE_HOME="$state_dir"

export MOCK_GIT_LOG="$git_log" MOCK_UPDATE_LOG="$op_log" MOCK_GIT_HEAD_STATE="$head_state"
export MOCK_GIT_REMOTES MOCK_GIT_BRANCH MOCK_GIT_STATUS MOCK_GIT_FETCH_STATUS \
  MOCK_GIT_MERGE_STATUS MOCK_GIT_MERGE_BASE MOCK_GIT_BEHIND \
  MOCK_GIT_CHANGED_PATHS MOCK_GIT_OLD_ENV MOCK_GIT_NEW_ENV MOCK_GIT_OLD_HEAD \
  MOCK_GIT_NEW_HEAD MOCK_BUILD_STATUS MOCK_LOADMSB_STATUS MOCK_WRAPPER_STATUS \
  MOCK_BINARY_STATUS MOCK_CLAUDE_VERSION MOCK_CODEX_VERSION

# Scenario defaults; individual cases override what they need.
MOCK_GIT_REMOTES='origin'
MOCK_GIT_BRANCH='main'
MOCK_GIT_STATUS=''
MOCK_GIT_FETCH_STATUS=0
MOCK_GIT_MERGE_STATUS=0
MOCK_GIT_MERGE_BASE="$old_head"
MOCK_GIT_BEHIND=2
MOCK_GIT_CHANGED_PATHS=$'versions.env\nshell/fish/trusted/guard.fish\nconfig/tools.json\nimages/claude/entrypoint.sh'
MOCK_GIT_OLD_HEAD="$old_head"
MOCK_GIT_NEW_HEAD="$new_head"
MOCK_CLAUDE_VERSION="$CLAUDE_CODE_VERSION"
MOCK_CODEX_VERSION="$CODEX_VERSION"
MOCK_SEED_MARKER="$old_head"

cat >"$mockdir/old-changed.env" <<'EOF'
CODEX_VERSION=0.145.0
CLAUDE_CODE_VERSION=2.1.224
EOF
cat >"$mockdir/new-changed.env" <<COPY
CODEX_VERSION=$CODEX_VERSION
CLAUDE_CODE_VERSION=$CLAUDE_CODE_VERSION
COPY

MOCK_GIT_OLD_ENV="$mockdir/old-changed.env"
MOCK_GIT_NEW_ENV="$mockdir/new-changed.env"

cat >"$mockdir/gitbin/git" <<'SH'
#!/usr/bin/env bash
cmd=$1
shift
case "$cmd" in
  config)
    if test -n "$MOCK_GIT_REMOTES"; then
      printf '%s\n' "$MOCK_GIT_REMOTES"
      exit 0
    fi
    exit 1
    ;;
  remote)
    printf '%s\n' "$MOCK_GIT_REMOTES"
    ;;
  symbolic-ref)
    printf '%s\n' "${MOCK_GIT_BRANCH:-main}"
    ;;
  status)
    printf '%s' "$MOCK_GIT_STATUS"
    if test -n "$MOCK_GIT_STATUS"; then printf '\n'; fi
    ;;
  fetch)
    printf 'git fetch %s\n' "$*" >>"$MOCK_GIT_LOG"
    exit "${MOCK_GIT_FETCH_STATUS:-0}"
    ;;
  rev-parse)
    short=0
    ref=''
    for a in "$@"; do
      if test "$a" = --short; then short=1; else ref=$a; fi
    done
    if test "$ref" = HEAD; then
      if test -r "$MOCK_GIT_HEAD_STATE"; then
        sha=$(cat "$MOCK_GIT_HEAD_STATE")
      else
        sha=$MOCK_GIT_OLD_HEAD
      fi
    else
      sha=$MOCK_GIT_NEW_HEAD
    fi
    if test "$short" = 1; then
      printf '%s\n' "${sha:0:7}"
    else
      printf '%s\n' "$sha"
    fi
    ;;
  merge-base)
    printf '%s\n' "${MOCK_GIT_MERGE_BASE:-$MOCK_GIT_OLD_HEAD}"
    # The updater probes `merge-base --is-ancestor OLD NEW`: return success
    # only when the configured merge base is OLD itself (i.e. OLD is an
    # ancestor of NEW), otherwise report divergence.
    if test "${MOCK_GIT_MERGE_BASE:-}" = "$MOCK_GIT_OLD_HEAD"; then
      exit 0
    fi
    exit 1
    ;;
  rev-list)
    printf '%s\n' "${MOCK_GIT_BEHIND:-2}"
    ;;
  show)
    case "$1" in
      HEAD:versions.env) cat "$MOCK_GIT_OLD_ENV" ;;
      origin/main:versions.env) cat "$MOCK_GIT_NEW_ENV" ;;
      *) printf 'git show %s: unsupported\n' "$1" >&2; exit 2 ;;
    esac
    ;;
  merge)
    printf 'git merge %s\n' "$*" >>"$MOCK_GIT_LOG"
    if test "${MOCK_GIT_MERGE_STATUS:-0}" != 0; then
      exit "$MOCK_GIT_MERGE_STATUS"
    fi
    printf '%s\n' "$MOCK_GIT_NEW_HEAD" >"$MOCK_GIT_HEAD_STATE"
    ;;
  diff)
    printf 'git diff %s\n' "$*" >>"$MOCK_GIT_LOG"
    printf '%s' "$MOCK_GIT_CHANGED_PATHS"
    if test -n "$MOCK_GIT_CHANGED_PATHS"; then printf '\n'; fi
    ;;
  *) printf 'git %s: unexpected\n' "$cmd" >&2; exit 2 ;;
esac
SH
chmod +x "$mockdir/gitbin/git"

cat >"$mockdir/extras/bin/msb" <<'SH'
#!/usr/bin/env bash
printf 'msb %s\n' "$*" >>"$MOCK_UPDATE_LOG"
SH
chmod +x "$mockdir/extras/bin/msb"

cat >"$mockdir/extras/bin/docker" <<'SH'
#!/usr/bin/env bash
case "$*" in
  *claude*) printf '%s\n' "$MOCK_CLAUDE_VERSION" ;;
  *codex*) printf '%s\n' "$MOCK_CODEX_VERSION" ;;
esac
printf 'docker %s\n' "$*" >>"$MOCK_UPDATE_LOG"
SH
chmod +x "$mockdir/extras/bin/docker"

cat >"$repo/scripts/build" <<'SH'
#!/usr/bin/env bash
printf 'build\n' >>"$MOCK_UPDATE_LOG"
exit "${MOCK_BUILD_STATUS:-0}"
SH
chmod +x "$repo/scripts/build"

cat >"$repo/scripts/install-fish-functions" <<'SH'
#!/usr/bin/env bash
printf 'install-fish-functions\n' >>"$MOCK_UPDATE_LOG"
exit "${MOCK_WRAPPER_STATUS:-0}"
SH
chmod +x "$repo/scripts/install-fish-functions"

cat >"$repo/scripts/install-ai-sandbox" <<'SH'
#!/usr/bin/env bash
printf 'install-ai-sandbox\n' >>"$MOCK_UPDATE_LOG"
exit "${MOCK_BINARY_STATUS:-0}"
SH
chmod +x "$repo/scripts/install-ai-sandbox"

cat >"$repo/scripts/load-msb" <<'SH'
#!/usr/bin/env bash
printf 'load-msb\n' >>"$MOCK_UPDATE_LOG"
exit "${MOCK_LOADMSB_STATUS:-0}"
SH
chmod +x "$repo/scripts/load-msb"

cat >"$repo/scripts/verify" <<'SH'
#!/usr/bin/env bash
printf 'verify\n' >>"$MOCK_UPDATE_LOG"
SH
chmod +x "$repo/scripts/verify"

# The scripts above run under `scripts/update`'s sanitized PATH, which drops
# any directory that holds a real docker/msb. On Linux CI bash and docker share
# /usr/bin, so `env bash` would not be able to rediscover bash through that
# PATH. Pin every executed script to an absolute interpreter instead.
bash_bin=$(command -v bash) || bash_bin=/bin/bash
fix_shebag() {
  sed -e "1s|^#!.*|#!$bash_bin|" "$1" >"$1.ts" && mv "$1.ts" "$1" && chmod +x "$1"
}
for script in \
  "$repo/scripts/update" \
  "$repo/.github/workflows/release-marker" \
  "$repo/scripts/build" \
  "$repo/scripts/install-fish-functions" \
  "$repo/scripts/install-ai-sandbox" \
  "$repo/scripts/load-msb" \
  "$repo/scripts/verify" \
  "$mockdir/gitbin/git" \
  "$mockdir/extras/bin/msb" \
  "$mockdir/extras/bin/docker"; do
  fix_shebag "$script"
done

gitpath="$mockdir/gitbin"
allpath="$mockdir/gitbin:$mockdir/extras/bin"

run_update() {
  local extra_path=$1
  shift
  : >"$git_log"
  : >"$op_log"
  # Each scenario runs against a clean reconciliation marker unless the test
  # explicitly seeds one first (see the recovery scenarios below).
  if test "${MOCK_KEEP_MARKER:-0}" != 1; then
    rm -f "$state_dir/ai-sandboxes/installed-head"
  fi
  if test -n "${MOCK_SEED_MARKER:-}"; then
    mkdir -p "$state_dir/ai-sandboxes"
    printf '%s\n' "$MOCK_SEED_MARKER" >"$state_dir/ai-sandboxes/installed-head"
  fi
  printf '%s\n' "$MOCK_GIT_OLD_HEAD" >"$MOCK_GIT_HEAD_STATE"
  PATH="$extra_path:$base_sanitized" "$repo/scripts/update" "$@" >"$out_log" 2>"$err_log"
  rc=$?
}

expect_status() {
  local want=$1 title=$2
  if test "$rc" != "$want"; then
    dump_logs
    fail "$title (want exit $want, got $rc)"
  fi
}

dump_logs() {
  {
    printf '%s\n' '--- stdout ---'
    cat "$out_log"
    printf '%s\n' '--- stderr ---'
    cat "$err_log"
  } >&2
}

expect_stdout_contains() {
  local pat=$1 title=$2
  grep -Fq "$pat" "$out_log" || {
    fail "$title (stdout should contain: $pat)"
  }
}

expect_stdout_not_contains() {
  local pat=$1 title=$2
  grep -Fq "$pat" "$out_log" && fail "$title (stdout should not contain: $pat)"
}

expect_stderr_contains() {
  local pat=$1 title=$2
  grep -Fq "$pat" "$err_log" || {
    fail "$title (stderr should contain: $pat)"
  }
}

expect_op_sequence() {
  local title=$1 expected_file=$2
  if ! diff -u "$expected_file" "$op_log" >/dev/null 2>&1; then
    {
      printf 'FAIL: %s (operation order mismatch)\n' "$title"
      printf '%s\n' '--- expected ---'
      cat "$expected_file"
      printf '%s\n' '--- actual ---'
      cat "$op_log"
    } >&2
    exit 1
  fi
}

# 1. --check up to date: exit 0, "up to date", no merge.
MOCK_GIT_NEW_HEAD="$old_head"
run_update "$gitpath" --check
expect_status 0 'check up-to-date'
expect_stdout_contains 'up to date' 'check up-to-date stdout'
grep -Fq 'git merge' "$git_log" && fail 'check up-to-date should not merge'
MOCK_GIT_NEW_HEAD="$new_head"

# 2. --check behind with a versions.env change: exit 1, summary, one delta line.
run_update "$gitpath" --check
expect_status 1 'check behind'
expect_stdout_contains '2 commits behind origin/main' 'check behind summary'
expect_stdout_contains "Codex 0.145.0 -> $CODEX_VERSION, Claude Code 2.1.224 -> $CLAUDE_CODE_VERSION" 'check behind deltas'
grep -Fq 'git merge' "$git_log" && fail 'check behind should not merge'
grep -Fq 'git fetch' "$git_log" || fail 'check behind must fetch first'

# 3. --check behind without a versions.env change: no delta line.
MOCK_GIT_OLD_ENV="$repo/versions.env"
MOCK_GIT_NEW_ENV="$repo/versions.env"
run_update "$gitpath" --check
expect_status 1 'check behind, unchanged versions'
expect_stdout_contains '2 commits behind origin/main' 'check behind, unchanged versions summary'
expect_stdout_not_contains 'Codex' 'check behind, unchanged versions deltas'
MOCK_GIT_OLD_ENV="$mockdir/old-changed.env"
MOCK_GIT_NEW_ENV="$mockdir/new-changed.env"

# 4. --check diverged: exit 2, no fetch of history beyond the tracking ref.
MOCK_GIT_MERGE_BASE="$third_head"
run_update "$gitpath" --check
expect_status 2 'check diverged'
expect_stderr_contains 'diverged' 'check diverged message'
MOCK_GIT_MERGE_BASE="$old_head"

# 5. --check with a dirty working tree: exit 2, refuses before fetching.
MOCK_GIT_STATUS=$' M scripts/build\n?? stray'
run_update "$gitpath" --check
expect_status 2 'check dirty'
expect_stderr_contains 'dirty' 'check dirty message'
test -s "$git_log" && fail 'dirty tree must be refused before git fetch'
MOCK_GIT_STATUS=''

# 6. --check without an origin remote: exit 2.
MOCK_GIT_REMOTES=''
run_update "$gitpath" --check
expect_status 2 'no origin remote'
expect_stderr_contains 'origin' 'no origin remote message'
MOCK_GIT_REMOTES='origin'

# 7. --check not on main: exit 2.
MOCK_GIT_BRANCH='feature/x'
run_update "$gitpath" --check
expect_status 2 'not on main'
expect_stderr_contains 'main' 'not on main message'
MOCK_GIT_BRANCH='main'

# 8. Default mode, relevant changes, msb present: build, wrappers, load-msb,
#    then light docker probes, in that exact order.
cat >"$mockdir/scenario8.op" <<'EOF'
build
install-fish-functions
load-msb
docker run --rm --user node -e HOME=/home/node ai-sandboxes-claude:local bash -lc test "$DISABLE_UPDATES" = 1; claude --version
docker run --rm --user node -e HOME=/home/node ai-sandboxes-codex:local codex --version
EOF
cat >"$mockdir/scenario8.git" <<EOF
git fetch origin main --quiet
git merge --ff-only origin/main
git diff --name-only --end-of-options $old_head $new_head
EOF
run_update "$allpath"
expect_status 0 'default full update'
expect_op_sequence 'default full update operations' "$mockdir/scenario8.op"
diff -u "$mockdir/scenario8.git" "$git_log" >/dev/null 2>&1 || fail 'default full update git order'
expect_stdout_contains 'fast-forwarded 2 commits to bbbbbbb' 'default full update summary'
expect_stdout_contains 'rebuilt docker images' 'default full update summary built'
expect_stdout_contains 'reinstalled fish wrappers' 'default full update summary wrappers'
expect_stdout_contains 'loaded images into msb' 'default full update summary loaded'

# 9. Default mode, no shell/** change: wrappers must NOT be reinstalled.
MOCK_GIT_CHANGED_PATHS=$'versions.env\nconfig/tools.json\nimages/claude/entrypoint.sh'
cat >"$mockdir/scenario9.op" <<'EOF'
build
load-msb
docker run --rm --user node -e HOME=/home/node ai-sandboxes-claude:local bash -lc test "$DISABLE_UPDATES" = 1; claude --version
docker run --rm --user node -e HOME=/home/node ai-sandboxes-codex:local codex --version
EOF
run_update "$allpath"
expect_status 0 'default update without shell changes'
expect_op_sequence 'default update without shell changes' "$mockdir/scenario9.op"
grep -Fq 'install-fish-functions' "$op_log" && fail 'install-fish-functions must only run when shell/** changes'

# 10. Default mode, msb and docker both absent: build and wrappers still run,
#     load-msb is skipped, and missing docker is a warning, not a failure.
MOCK_GIT_CHANGED_PATHS=$'versions.env\nshell/fish/trusted/guard.fish'
cat >"$mockdir/scenario10.op" <<'EOF'
build
install-fish-functions
EOF
run_update "$gitpath"
expect_status 0 'default update without msb'
expect_op_sequence 'default update without msb' "$mockdir/scenario10.op"
grep -Fq 'load-msb' "$op_log" && fail 'load-msb should not run when msb is absent'
expect_stderr_contains 'docker not found' 'missing docker warning'

# 11. Default mode, nothing relevant changed: no build, no load-msb, no probes.
MOCK_GIT_CHANGED_PATHS=$'README.md\ndocs/dev/guide.md'
run_update "$allpath"
expect_status 0 'default update with no relevant changes'
test -s "$op_log" && fail 'no step should run when nothing relevant changed'
expect_stdout_contains 'fast-forwarded 2 commits to bbbbbbb' 'default update no build summary'
expect_stdout_not_contains 'built docker images' 'no build summary message'
MOCK_GIT_CHANGED_PATHS=$'versions.env\nshell/fish/trusted/guard.fish\nconfig/tools.json\nimages/claude/entrypoint.sh'

# 12. Default mode with a dirty tree: refuse before merging.
MOCK_GIT_STATUS=' M versions.env'
run_update "$allpath"
expect_status 2 'default dirty tree'
expect_stderr_contains 'dirty' 'default dirty tree message'
grep -Fq 'git merge' "$git_log" && fail 'dirty tree must refuse before merging'
MOCK_GIT_STATUS=''

# 13. Default mode when the build fails: stop immediately, do not load-msb.
MOCK_BUILD_STATUS=1
run_update "$allpath"
expect_status 2 'build failure'
expect_stderr_contains './scripts/build' 'build failure command'
expect_stderr_contains 're-run scripts/update' 'build failure next step'
grep -Fq 'load-msb' "$op_log" && fail 'load-msb must not run after a failed build'
grep -Fq 'docker' "$op_log" && fail 'docker probes must not run after a failed build'
MOCK_BUILD_STATUS=0

# 14. Default mode when the fast-forward merge fails: no build afterwards.
MOCK_GIT_MERGE_STATUS=1
run_update "$allpath"
expect_status 2 'merge failure'
expect_stderr_contains 'git merge --ff-only origin/main' 'merge failure command'
test -s "$op_log" && fail 'no build step should run after a failed merge'
MOCK_GIT_MERGE_STATUS=0

# 15. Default mode, already up to date: exit 0, no steps.
MOCK_GIT_NEW_HEAD="$old_head"
run_update "$allpath"
expect_status 0 'default up-to-date'
expect_stdout_contains 'up to date' 'default up-to-date stdout'
test -s "$op_log" && fail 'up-to-date should run no steps'
grep -Fq 'git merge' "$git_log" && fail 'up-to-date should not merge'
MOCK_GIT_NEW_HEAD="$new_head"

# 16. --verify runs the full verify script instead of light probes.
cat >"$mockdir/scenario16.op" <<'EOF'
build
install-fish-functions
load-msb
verify
EOF
run_update "$allpath" --verify
expect_status 0 'default with --verify'
expect_op_sequence 'default with --verify operations' "$mockdir/scenario16.op"

# 16b. cmd/**, internal/**, go.mod, go.sum, and scripts/install-ai-sandbox
#      trigger the binary install; a binary install also refreshes the fish
#      wrappers so newly generated absolute paths are baked in.
MOCK_GIT_CHANGED_PATHS=$'cmd/ai-sandbox/main.go\ninternal/plan/plan.go\ngo.mod\ngo.sum\nscripts/install-ai-sandbox'
cat >"$mockdir/scenario16b.op" <<'EOF'
install-ai-sandbox
install-fish-functions
EOF
run_update "$gitpath"
expect_status 0 'go changes trigger binary install'
expect_op_sequence 'go changes trigger binary install' "$mockdir/scenario16b.op"
expect_stdout_contains 'reinstalled ai-sandbox binary' 'binary install summary'
expect_stdout_contains 'reinstalled fish wrappers' 'wrappers rebaked after binary install'

# 16c. A failed binary install stops before the wrappers are touched.
MOCK_GIT_CHANGED_PATHS=$'cmd/ai-sandbox/main.go\ninternal/plan/plan.go\ngo.mod\ngo.sum\nscripts/install-ai-sandbox'
MOCK_BINARY_STATUS=1
run_update "$gitpath"
expect_status 2 'binary install failure'
expect_stderr_contains 'ai-sandbox binary install failed' 'binary install failure message'
grep -Fq 'install-fish-functions' "$op_log" && fail 'wrappers must not be reinstalled after a failed binary install'

# 16d. After a failed install, git has already fast-forwarded and reports
#      "up to date". The reconciliation marker written pre-install lets a
#      subsequent run redo the pending work anyway.
MOCK_BINARY_STATUS=0
MOCK_GIT_OLD_HEAD="$new_head"
MOCK_GIT_NEW_HEAD="$new_head"
MOCK_KEEP_MARKER=1
cat >"$mockdir/scenario16d.op" <<'EOF'
install-ai-sandbox
install-fish-functions
EOF
run_update "$gitpath"
MOCK_KEEP_MARKER=0
MOCK_GIT_OLD_HEAD="$old_head"
MOCK_GIT_NEW_HEAD="$new_head"
expect_status 0 'recovery reconciles pending install'
expect_op_sequence 'recovery reconciliation operations' "$mockdir/scenario16d.op"
expect_stdout_contains 'reconciling incomplete install' 'recovery reconciliation notice'
expect_stdout_contains 'reconciled install to' 'recovery reconciliation summary'

MOCK_GIT_CHANGED_PATHS=$'versions.env\nshell/fish/trusted/guard.fish\nconfig/tools.json\nimages/claude/entrypoint.sh'

# 17. Flag validation: --check --verify and unknown flags are usage errors.
run_update "$gitpath" --check --verify
expect_status 64 'check plus verify usage'
expect_stderr_contains 'usage' 'check plus verify usage message'
run_update "$gitpath" --frobnicate
expect_status 64 'unknown flag usage'
expect_stderr_contains 'usage' 'unknown flag usage message'

# 18. Missing marker with HEAD already at origin/main: reconcile everything.
MOCK_GIT_NEW_HEAD="$old_head"
MOCK_SEED_MARKER=''
cat >"$mockdir/scenario18.op" <<'EOF'
install-ai-sandbox
build
install-fish-functions
EOF
run_update "$gitpath"
expect_status 0 'missing marker reconciles all'
expect_op_sequence 'missing marker reconciles all' "$mockdir/scenario18.op"
expect_stdout_contains 'marker missing: reconciling install state' 'missing marker notice'
expect_stdout_contains 'reconciled install to' 'missing marker summary'
expect_stderr_contains 'docker not found' 'missing marker missing docker warning'
MOCK_GIT_NEW_HEAD="$new_head"

# 19. Invalid marker (non-hex) with HEAD at origin/main: treated as missing.
MOCK_GIT_NEW_HEAD="$old_head"
MOCK_SEED_MARKER='not-a-valid-sha'
cat >"$mockdir/scenario19.op" <<'EOF'
install-ai-sandbox
build
install-fish-functions
EOF
run_update "$gitpath"
expect_status 0 'invalid marker reconciles all'
expect_op_sequence 'invalid marker reconciles all' "$mockdir/scenario19.op"
expect_stdout_contains 'marker missing: reconciling install state' 'invalid marker notice'
MOCK_GIT_NEW_HEAD="$new_head"
MOCK_SEED_MARKER=''

# 20. --repair when already up to date: force all install steps.
MOCK_GIT_NEW_HEAD="$old_head"
MOCK_SEED_MARKER="$old_head"
cat >"$mockdir/scenario20.op" <<'EOF'
install-ai-sandbox
build
install-fish-functions
EOF
run_update "$gitpath" --repair
expect_status 0 'repair forces all steps'
expect_op_sequence 'repair forces all steps' "$mockdir/scenario20.op"
expect_stdout_contains 'repairing: forcing full reinstall' 'repair notice'
expect_stdout_contains 'repaired install at' 'repair summary'
expect_stderr_contains 'docker not found' 'repair missing docker warning'
MOCK_GIT_NEW_HEAD="$new_head"
MOCK_SEED_MARKER=''

# 21. --check with missing marker: report unknown state (exit 1).
MOCK_GIT_NEW_HEAD="$old_head"
MOCK_SEED_MARKER=''
run_update "$gitpath" --check
expect_status 1 'check missing marker'
expect_stdout_contains 'marker missing: install state unknown' 'check missing marker notice'
grep -Fq 'git merge' "$git_log" && fail 'check missing marker should not merge'
MOCK_GIT_NEW_HEAD="$new_head"

# 22. --check --repair together is a usage error.
run_update "$gitpath" --check --repair
expect_status 64 'check plus repair usage'
expect_stderr_contains 'usage' 'check plus repair usage message'

echo ok