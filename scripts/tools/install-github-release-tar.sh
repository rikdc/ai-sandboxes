#!/usr/bin/env bash
set -o pipefail

catalog=${1:?usage: install-github-release-tar.sh CATALOG SELECTION TOOL_ID DESTINATION}
selection=${2:?usage: install-github-release-tar.sh CATALOG SELECTION TOOL_ID DESTINATION}
tool_id=${3:?usage: install-github-release-tar.sh CATALOG SELECTION TOOL_ID DESTINATION}
destination=${4:?usage: install-github-release-tar.sh CATALOG SELECTION TOOL_ID DESTINATION}

entry=$(jq -ce --arg id "$tool_id" '.tools[] | select(.id == $id)' "$catalog") || exit 1
selected=$(jq -ce --arg id "$tool_id" '.tools[] | select(.id == $id)' "$selection") || exit 1
repository=$(jq -er '.repository' <<<"$entry") || exit 1
asset=$(jq -er '.asset' <<<"$entry") || exit 1
binary=$(jq -er '.binary' <<<"$entry") || exit 1
archive_member=$(jq -er '.archive_member' <<<"$entry") || exit 1
version=$(jq -er '.version' <<<"$selected") || exit 1
checksum=$(jq -er '.sha256' <<<"$selected") || exit 1
archive=$(mktemp) || exit 1
extract_dir=$(mktemp -d) || exit 1
trap 'rm -rf "$archive" "$extract_dir"' EXIT
curl -fsSL "https://github.com/${repository}/releases/download/${version}/${asset}" -o "$archive" || exit 1
echo "${checksum}  ${archive}" | sha256sum -c - || exit 1
tar -xzf "$archive" -C "$extract_dir" "$archive_member" || exit 1
install -Dm 0755 "$extract_dir/$archive_member" "$destination/$binary" || exit 1
