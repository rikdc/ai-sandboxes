#!/usr/bin/env bash
set -o pipefail

catalog=${1:?usage: install-awscli-zip.sh CATALOG SELECTION TOOL_ID DESTINATION}
selection=${2:?usage: install-awscli-zip.sh CATALOG SELECTION TOOL_ID DESTINATION}
tool_id=${3:?usage: install-awscli-zip.sh CATALOG SELECTION TOOL_ID DESTINATION}
destination=${4:?usage: install-awscli-zip.sh CATALOG SELECTION TOOL_ID DESTINATION}

die() {
  printf 'install-awscli-zip: %s\n' "$*" >&2
  exit 1
}

entry=$(jq -ce --arg id "$tool_id" '.tools[] | select(.id == $id)' "$catalog") || die "unknown tool id: $tool_id"
selected=$(jq -ce --arg id "$tool_id" '.tools[] | select(.id == $id)' "$selection") || die "no selection for tool id: $tool_id"
url_template=$(jq -er '.url_template' <<<"$entry") || die "catalog entry missing url_template: $tool_id"
binary=$(jq -er '.binary' <<<"$entry") || die "catalog entry missing binary: $tool_id"
version=$(jq -er '.version' <<<"$selected") || die "selection missing version: $tool_id"
checksum=$(jq -er '.sha256' <<<"$selected") || die "selection missing sha256: $tool_id"

url=${url_template//\{\{version\}\}/$version}
case "$url" in
  *'{{'*|*'}}'*) die "unexpanded template token in url for $tool_id: $url" ;;
  https://*) ;;
  *) die "refusing non-https url for $tool_id: $url" ;;
esac

archive=$(mktemp) || die 'could not create a scratch file for the downloaded archive'
extract_dir=$(mktemp -d) || die 'could not create a scratch directory for extraction'
trap 'rm -rf "$archive" "$extract_dir"' EXIT
curl -fsSL "$url" -o "$archive" || die "download failed for $tool_id: $url"
echo "${checksum}  ${archive}" | sha256sum -c - >/dev/null || die "checksum mismatch for $tool_id: $url"

# AWS CLI v2 ships as a zip that carries its own installer (aws/install) and a
# self-contained dist/ tree; there is no single movable binary. Run the
# bundled installer against a stable prefix derived from the destination so
# it lays out /usr/local/aws-cli/v2/<version> and re-pointers the binaries in
# $destination, all verified against the pinned sha256 above.
unzip -q "$archive" -d "$extract_dir" || die "could not extract $url"
test -x "$extract_dir/aws/install" || die "archive for $tool_id is missing aws/install"
install_dir=$(dirname -- "$destination")/aws-cli
"$extract_dir/aws/install" --install-dir "$install_dir" --bin-dir "$destination" --update \
  || die "aws v2 installer failed for $tool_id"
test -x "$destination/$binary" || die "$binary was not installed into $destination"
