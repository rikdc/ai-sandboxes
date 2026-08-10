#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../../.."

fail=0

for fixture in scripts/session/fixtures/valid/*.json; do
  canonical=$(scripts/session/validate-profile.sh "$fixture") || { echo "FAIL (should be valid): $fixture" >&2; fail=1; continue; }
  jq empty <<<"$canonical" || { echo "FAIL (stdout not JSON): $fixture" >&2; fail=1; continue; }
  canonical_again=$(scripts/session/validate-profile.sh "$fixture")
  test "$canonical" = "$canonical_again" || { echo "FAIL (not stable): $fixture" >&2; fail=1; }
done

for fixture in scripts/session/fixtures/invalid/*.json; do
  if scripts/session/validate-profile.sh "$fixture" >/dev/null 2>/tmp/validate-profile-stderr.$$; then
    echo "FAIL (should be invalid): $fixture" >&2
    fail=1
  else
    test -s /tmp/validate-profile-stderr.$$ || { echo "FAIL (no stderr message): $fixture" >&2; fail=1; }
  fi
  rm -f /tmp/validate-profile-stderr.$$
done

test "$fail" -eq 0
echo ok
