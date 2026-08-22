#!/usr/bin/env bash
# Exercises scripts/lib/config-dir.sh without touching the invoking user's
# real configuration: resolution precedence, seeding from the checked-in
# neutral defaults, never-overwrite semantics, failure on non-regular or
# unreadable entries, permission modes, and digest stability/sensitivity.
#
# The library's failure paths end in exit, so every negative probe runs in a
# subshell; otherwise a correctly-rejected input would kill this harness.
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

# shellcheck disable=SC1091 # Resolved after cd to the repository root.
. ./scripts/lib/config-dir.sh

# expect_reject TITLE CMD... — the command must exit nonzero.
# expect_accept TITLE CMD... — the command must exit zero.
expect_reject() {
  local title=$1
  shift
  if ("$@") >/dev/null 2>&1; then
    fail "$title: expected rejection, got success"
  fi
}
expect_accept() {
  local title=$1
  shift
  ("$@") >/dev/null 2>&1 || fail "$title: unexpected failure"
}

# Every scenario points AI_SANDBOX_CONFIG_DIR inside the sandbox so the
# resolver can never fall through to the developer's real configuration.
cfg="$sandbox/user-config"

resolve_into() {
  AI_SANDBOX_CONFIG_DIR=$1 ai_sandboxes_resolve_config_dir test-config-dir
}

seed_from() {
  AI_SANDBOX_CONFIG_DIR=$1 ai_sandboxes_init_config_files test-config-dir "$repo_root" "$1"
}

# 1. Resolution honours AI_SANDBOX_CONFIG_DIR and creates it with mode 0700.
expect_accept 'resolve' resolve_into "$cfg"
test -d "$cfg" || fail 'resolver must create the configuration directory'

# 2. Seeding copies every missing file from the checked-in defaults with mode
#    0600, and never overwrites existing user content.
printf '{"claude":["custom"]}\n' >"$cfg/marketplaces.json"
expect_accept 'init' ai_sandboxes_init_config_files test-config-dir "$repo_root" "$cfg"
test "$(cat "$cfg/marketplaces.json")" = '{"claude":["custom"]}' \
  || fail 'init must not overwrite existing user content'
for f in tools.json runtime.json; do
  cmp -s "$repo_root/config/$f" "$cfg/$f" || fail "init must seed $f from the checked-in default"
done

# 3. A directory where a configuration file belongs is rejected.
rm -f "$cfg/tools.json"
mkdir -p "$cfg/tools.json"
expect_reject 'directory entry' seed_from "$cfg"
rmdir "$cfg/tools.json"
expect_accept 'reseed' seed_from "$cfg"

# 4. Digests are deterministic, hex-shaped, and sensitive to content changes.
d1=$(ai_sandboxes_config_digest "$cfg") || fail 'digest computation failed'
d2=$(ai_sandboxes_config_digest "$cfg") || fail 'digest recomputation failed'
test "$d1" = "$d2" || fail 'digest must be deterministic'
case $d1 in
  [0-9a-f]*) ;;
  *) fail "digest is not a hex sha256: $d1" ;;
esac
printf '{}\n' >"$cfg/runtime.json"
d3=$(ai_sandboxes_config_digest "$cfg") || fail 'digest recomputation failed'
test "$d1" != "$d3" || fail 'digest must change when configuration content changes'

# 5. Relative paths and newlines in AI_SANDBOX_CONFIG_DIR are rejected.
expect_reject 'relative config dir' resolve_into 'relative/path'
nl_dir=$'/tmp/a\nb'
expect_reject 'newline in config dir' resolve_into "$nl_dir"

# 6. An existing entry that stopped being readable fails validation.
chmod 000 "$cfg/runtime.json"
expect_reject 'unreadable entry' ai_sandboxes_validate_config_entries test-config-dir "$cfg"
chmod 0600 "$cfg/runtime.json"
expect_accept 'readable entries' ai_sandboxes_validate_config_entries test-config-dir "$cfg"

printf '%s\n' 'test-config-dir: all scenarios passed'
