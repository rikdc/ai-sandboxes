#!/usr/bin/env bash
set -o pipefail
cd "$(dirname "$0")/../../.." || exit 1

fail=0
stderr_file=$(mktemp) || exit 1
trap 'rm -f "$stderr_file"' EXIT

for fixture in scripts/session/fixtures/valid/*.json; do
  canonical=$(scripts/session/validate-profile.sh "$fixture") || { echo "FAIL (should be valid): $fixture" >&2; fail=1; continue; }
  jq empty <<<"$canonical" || { echo "FAIL (stdout not JSON): $fixture" >&2; fail=1; continue; }
  canonical_again=$(scripts/session/validate-profile.sh "$fixture") || { echo "FAIL (second call failed): $fixture" >&2; fail=1; continue; }
  test "$canonical" = "$canonical_again" || { echo "FAIL (not stable): $fixture" >&2; fail=1; }
done

for fixture in scripts/session/fixtures/invalid/*.json; do
  if scripts/session/validate-profile.sh "$fixture" >/dev/null 2>"$stderr_file"; then
    echo "FAIL (should be invalid): $fixture" >&2
    fail=1
  else
    test -s "$stderr_file" || { echo "FAIL (no stderr message): $fixture" >&2; fail=1; }
  fi
done

test "$fail" -eq 0 || exit 1
echo ok
