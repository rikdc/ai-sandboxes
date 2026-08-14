#!/usr/bin/env bash
set -o pipefail

catalog=${1:?usage: install-https-tar.sh CATALOG SELECTION TOOL_ID DESTINATION}
selection=${2:?usage: install-https-tar.sh CATALOG SELECTION TOOL_ID DESTINATION}
tool_id=${3:?usage: install-https-tar.sh CATALOG SELECTION TOOL_ID DESTINATION}
destination=${4:?usage: install-https-tar.sh CATALOG SELECTION TOOL_ID DESTINATION}

die() {
  printf 'install-https-tar: %s\n' "$*" >&2
  exit 1
}

entry=$(jq -ce --arg id "$tool_id" '.tools[] | select(.id == $id)' "$catalog") || die "unknown tool id: $tool_id"
selected=$(jq -ce --arg id "$tool_id" '.tools[] | select(.id == $id)' "$selection") || die "no selection for tool id: $tool_id"
url_template=$(jq -er '.url_template' <<<"$entry") || die "catalog entry missing url_template: $tool_id"
archive_member=$(jq -er '.archive_member' <<<"$entry") || die "catalog entry missing archive_member: $tool_id"
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
tar -xzf "$archive" -C "$extract_dir" "$archive_member" || die "could not extract $archive_member from archive"

if test -f "$extract_dir/$archive_member"; then
  install -Dm 0755 "$extract_dir/$archive_member" "$destination/$binary" \
    || die "could not install $binary to $destination"
  exit 0
fi

test -d "$extract_dir/$archive_member" || die "could not extract $archive_member for $tool_id"

# Directory member: install the whole (self-contained) tree as a toolchain
# prefix and expose every executable under its bin/ on PATH. This is what a
# single-binary install cannot express (e.g. go needs its GOROOT pkg/tool tree
# beside bin/go). The prefix sits under /usr/local/libexec so /usr/local/bin
# stays a directory of small launchers/symlinks, exactly as it does for
# state-wrapper tools.
member_name=${archive_member##*/}
prefix=/usr/local/libexec/$member_name
rm -rf "$prefix" || die "could not clear existing prefix $prefix"
cp -a "$extract_dir/$archive_member" "$prefix" || die "could not install $archive_member to $prefix"

matched=
for tool_path in "$prefix"/bin/*; do
  test -e "$tool_path" || continue
  test -x "$tool_path" || die "non-executable entry in $member_name bin/: $tool_path"
  ln -sf "$tool_path" "$destination/$(basename "$tool_path")" || die "could not link $(basename "$tool_path") into $destination"
  test "$(basename "$tool_path")" = "$binary" && matched=1
done
test "$matched" = 1 || die "$member_name has no $binary executable in its bin/ directory"