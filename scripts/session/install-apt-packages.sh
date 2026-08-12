#!/usr/bin/env bash
set -euo pipefail

apt_json=${1:?usage: install-apt-packages.sh APT_PACKAGES_JSON FRAGMENT_OUTPUT_PATH}
fragment_output=${2:?usage: install-apt-packages.sh APT_PACKAGES_JSON FRAGMENT_OUTPUT_PATH}

die() {
  printf 'install-apt-packages: %s\n' "$*" >&2
  exit 1
}

specs_json=$(jq -r '.apt[] | if has("version") then "\(.name)=\(.version)" else .name end' "$apt_json") \
  || die "could not read apt package list from $apt_json"
specs=()
if test -n "$specs_json"; then
  mapfile -t specs <<<"$specs_json"
fi
test "${#specs[@]}" -gt 0 || die "no apt packages listed in $apt_json"

apt-get update \
  || die 'apt-get update failed'
apt-get install -y --no-install-recommends "${specs[@]}" \
  || die "apt-get install failed for: ${specs[*]}"

# Apt versions are optional in the profile, and even a pinned version can
# resolve differently depending on the apt repository's state at build time.
# Query the real installed version for every package (pinned or not) so
# resolved.json's provenance always reflects what actually landed in the
# image, not just what was requested.
names_json=$(jq -r '.apt[].name' "$apt_json") \
  || die "could not read apt package names from $apt_json"
actual=$(jq -cn '[]')
if test -n "$names_json"; then
  while IFS= read -r name; do
    version=$(dpkg-query -W -f='${Version}' "$name") \
      || die "dpkg-query could not find an installed version for $name (if this is a virtual package, request the providing package by its real name instead)"
    test -n "$version" || die "dpkg-query returned an empty version for $name"
    actual=$(jq -c --arg name "$name" --arg version "$version" \
      '. + [{name: $name, version: $version}]' <<<"$actual") \
      || die "could not record installed version for $name"
  done <<<"$names_json"
fi

printf '%s\n' "$actual" >"$fragment_output" \
  || die "could not write $fragment_output"

rm -rf /var/lib/apt/lists/*
