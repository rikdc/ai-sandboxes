#!/usr/bin/env bash
# Regression test: every release-marker invocation inside the verify-time
# check is fatal. scripts/verify once called `release-marker create` and
# `validate` with a stale argument shape; both printed usage errors (exit 64)
# and, because nothing checked the exit codes, verification still passed —
# the marker tests were decorative. These scenarios pin the contract that a
# failing marker command fails the check, and therefore scripts/verify.
set -o pipefail
cd "$(dirname "$0")/../.." || exit 1

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

# 1. The check passes on the real repository with the real marker tool.
bash scripts/lib/release-marker-check.sh \
  || fail 'marker check failed on a clean repository'

sandbox=$(mktemp -d) || exit 1
cleanup() {
  rm -rf "$sandbox"
}
trap cleanup EXIT
mkdir -p "$sandbox/scripts/lib" "$sandbox/.github/workflows" || exit 1
cp scripts/lib/release-marker-check.sh "$sandbox/scripts/lib/" || exit 1
cp versions.env "$sandbox/" || exit 1

stub_marker() {
  cat >"$sandbox/.github/workflows/release-marker" <<SH
#!/usr/bin/env bash
exit $1
SH
  chmod +x "$sandbox/.github/workflows/release-marker"
}

run_check() {
  bash "$sandbox/scripts/lib/release-marker-check.sh" >/dev/null 2>&1
}

# 2. A usage error (the exact failure mode this regression guards: stale
#    argument shape) fails the check.
stub_marker 64
if run_check; then
  fail 'a release-marker usage error did not fail the check'
fi

# 3. Any nonzero marker exit fails the check.
stub_marker 1
if run_check; then
  fail 'a failed release-marker command did not fail the check'
fi

# 4. A marker that lies — reporting changed on identical input — is caught by
#    the guard-the-guard branch instead of passing silently.
cat >"$sandbox/.github/workflows/release-marker" <<'SH'
#!/usr/bin/env bash
exit 0
SH
chmod +x "$sandbox/.github/workflows/release-marker"
if run_check; then
  fail 'an unchanged versions.env reported as changed did not fail the check'
fi

printf '%s\n' 'test-release-marker-check: ok'
