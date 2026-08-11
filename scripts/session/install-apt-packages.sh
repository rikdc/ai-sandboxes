#!/usr/bin/env bash
set -euo pipefail

apt_json=${1:?usage: install-apt-packages.sh APT_PACKAGES_JSON RESOLVED_JSON}
resolved_json=${2:?usage: install-apt-packages.sh APT_PACKAGES_JSON RESOLVED_JSON}

die() {
  printf 'install-apt-packages: %s\n' "$*" >&2
  exit 1
}

mapfile -t specs < <(jq -r '.apt[] | if has("version") then "\(.name)=\(.version)" else .name end' "$apt_json")
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
actual=$(jq -cn '[]')
while IFS= read -r name; do
  version=$(dpkg-query -W -f='${Version}' "$name") \
    || die "dpkg-query could not find an installed version for $name"
  actual=$(jq -c --arg name "$name" --arg version "$version" \
    '. + [{name: $name, version: $version}]' <<<"$actual") \
    || die "could not record installed version for $name"
done < <(jq -r '.apt[].name' "$apt_json")

patched=$(mktemp) || die 'could not create a scratch file for resolved.json'
trap 'rm -f -- "$patched"' EXIT
jq --argjson actual "$actual" '.packages.apt = $actual' "$resolved_json" >"$patched" \
  || die "could not patch $resolved_json with actual apt versions"
mv -f -- "$patched" "$resolved_json" \
  || die "could not install patched $resolved_json"

rm -rf /var/lib/apt/lists/*
