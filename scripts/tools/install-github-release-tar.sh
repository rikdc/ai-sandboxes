#!/usr/bin/env bash
set -o pipefail

catalog=${1:?usage: install-github-release-tar.sh CATALOG SELECTION TOOL_ID DESTINATION}
selection=${2:?usage: install-github-release-tar.sh CATALOG SELECTION TOOL_ID DESTINATION}
tool_id=${3:?usage: install-github-release-tar.sh CATALOG SELECTION TOOL_ID DESTINATION}
destination=${4:?usage: install-github-release-tar.sh CATALOG SELECTION TOOL_ID DESTINATION}

die() {
  printf 'install-github-release-tar: %s\n' "$*" >&2
  exit 1
}

entry=$(jq -ce --arg id "$tool_id" '.tools[] | select(.id == $id)' "$catalog") || die "unknown tool id: $tool_id"
selected=$(jq -ce --arg id "$tool_id" '.tools[] | select(.id == $id)' "$selection") || die "no selection for tool id: $tool_id"
repository=$(jq -er '.repository' <<<"$entry") || die "catalog entry missing repository: $tool_id"
asset=$(jq -er '.asset' <<<"$entry") || die "catalog entry missing asset: $tool_id"
binary=$(jq -er '.binary' <<<"$entry") || die "catalog entry missing binary: $tool_id"
archive_member=$(jq -er '.archive_member' <<<"$entry") || die "catalog entry missing archive_member: $tool_id"
version=$(jq -er '.version' <<<"$selected") || die "selection missing version: $tool_id"
checksum=$(jq -er '.sha256' <<<"$selected") || die "selection missing sha256: $tool_id"
archive=$(mktemp) || die 'could not create a scratch file for the downloaded archive'
extract_dir=$(mktemp -d) || die 'could not create a scratch directory for extraction'
trap 'rm -rf "$archive" "$extract_dir"' EXIT
curl -fsSL "https://github.com/${repository}/releases/download/${version}/${asset}" -o "$archive" \
  || die "download failed for $repository $version $asset"
echo "${checksum}  ${archive}" | sha256sum -c - >/dev/null || die "checksum mismatch for $repository $version $asset"
tar -xzf "$archive" -C "$extract_dir" "$archive_member" || die "could not extract $archive_member from archive"
# Refuse rather than overwrite: a catalog entry that happens to name a
# base-image command (claude, git, curl, ...) must not silently replace it.
# Every fresh session-image build starts from a base with no curated tools
# already in place, so a pre-existing destination here always indicates a
# collision worth stopping the build over.
test ! -e "$destination/$binary" || die "refusing to install $binary into $destination: destination already exists (collision with a base-image command or another tool?)"
install -Dm 0755 "$extract_dir/$archive_member" "$destination/$binary" || die "could not install $binary to $destination"
