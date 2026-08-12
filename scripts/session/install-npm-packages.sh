#!/usr/bin/env bash
set -o pipefail

npm_json=${1:?usage: install-npm-packages.sh NPM_PACKAGES_JSON}
prefix=/opt/claude-session/npm
cache_dir=/tmp/claude-session-npm-cache

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

npm install --global --prefix "$prefix" --cache "$cache_dir" "${specs[@]}" \
  || die "npm install failed for: ${specs[*]}"

# Do not ask npm where its cache is after lifecycle hooks have run: a hook can
# write npm configuration in the still-writable prefix. The command line above
# fixes the cache location for this install, so cleanup has one known-safe,
# build-only target.
rm -rf -- "$cache_dir" || die "could not remove npm cache directory: $cache_dir"
