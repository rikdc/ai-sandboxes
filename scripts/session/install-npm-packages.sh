#!/usr/bin/env bash
set -euo pipefail

npm_json=${1:?usage: install-npm-packages.sh NPM_PACKAGES_JSON}
prefix=/opt/claude-session/npm

die() {
  printf 'install-npm-packages: %s\n' "$*" >&2
  exit 1
}

specs_json=$(jq -r '.npm[] | "\(.package)@\(.version)"' "$npm_json") \
  || die "could not read npm package list from $npm_json"
specs=()
if test -n "$specs_json"; then
  mapfile -t specs <<<"$specs_json"
fi
test "${#specs[@]}" -gt 0 || die "no npm packages listed in $npm_json"

npm install --global --prefix "$prefix" "${specs[@]}" \
  || die "npm install failed for: ${specs[*]}"

cache_dir=$(npm config get cache) \
  || die 'could not determine npm cache directory'
case "$cache_dir" in
  /*) rm -rf "$cache_dir" ;;
  *) die "npm cache directory is not an absolute path: $cache_dir" ;;
esac
