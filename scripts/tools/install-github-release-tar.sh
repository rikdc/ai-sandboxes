#!/usr/bin/env bash
set -euo pipefail

catalog=${1:?usage: install-github-release-tar.sh CATALOG SELECTION TOOL_ID DESTINATION}
selection=${2:?usage: install-github-release-tar.sh CATALOG SELECTION TOOL_ID DESTINATION}
tool_id=${3:?usage: install-github-release-tar.sh CATALOG SELECTION TOOL_ID DESTINATION}
destination=${4:?usage: install-github-release-tar.sh CATALOG SELECTION TOOL_ID DESTINATION}

entry=$(jq -ce --arg id "$tool_id" '.tools[] | select(.id == $id)' "$catalog")
selected=$(jq -ce --arg id "$tool_id" '.tools[] | select(.id == $id)' "$selection")
repository=$(jq -er '.repository' <<<"$entry")
asset=$(jq -er '.asset' <<<"$entry")
binary=$(jq -er '.binary' <<<"$entry")
archive_member=$(jq -er '.archive_member' <<<"$entry")
version=$(jq -er '.version' <<<"$selected")
checksum=$(jq -er '.sha256' <<<"$selected")
archive=$(mktemp)
extract_dir=$(mktemp -d)
trap 'rm -rf "$archive" "$extract_dir"' EXIT
curl -fsSL "https://github.com/${repository}/releases/download/${version}/${asset}" -o "$archive"
echo "${checksum}  ${archive}" | sha256sum -c -
tar -xzf "$archive" -C "$extract_dir" "$archive_member"
install -Dm 0755 "$extract_dir/$archive_member" "$destination/$binary"
