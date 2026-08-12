#!/usr/bin/env bash
set -euo pipefail

resolved_json=${1:?usage: patch-apt-provenance.sh RESOLVED_JSON FRAGMENT_JSON}
fragment_json=${2:?usage: patch-apt-provenance.sh RESOLVED_JSON FRAGMENT_JSON}

die() {
  printf 'patch-apt-provenance: %s\n' "$*" >&2
  exit 1
}

patched=$(mktemp) || die 'could not create a scratch file for resolved.json'
trap 'rm -f -- "$patched"' EXIT
jq --slurpfile fragment "$fragment_json" '.packages.apt = $fragment[0]' "$resolved_json" >"$patched" \
  || die "could not patch $resolved_json with $fragment_json"
chmod --reference="$resolved_json" "$patched" \
  || die "could not preserve mode of $resolved_json on scratch file"
mv -f -- "$patched" "$resolved_json" \
  || die "could not install patched $resolved_json"
