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
  test ! -e "$destination/$binary" || die "refusing to install $binary into $destination: destination already exists (collision with a base-image command or another tool?)"
  install -Dm 0755 "$extract_dir/$archive_member" "$destination/$binary" \
    || die "could not install $binary to $destination"
  exit 0
fi

test -d "$extract_dir/$archive_member" || die "could not extract $archive_member for $tool_id"

# Directory member: install the whole (self-contained) tree as a toolchain
# prefix and expose exactly the executables the catalog names. This is what a
# single-binary install cannot express (e.g. go needs its GOROOT pkg/tool tree
# beside bin/go). The prefix sits under /usr/local/libexec/ai-sandboxes-tools
# so /usr/local/bin stays a directory of small launchers/symlinks, and two
# tools whose archives use a generic member name (bin, linux-arm64, ...)
# cannot silently overwrite one another.
# The default prefix root is a root-owned path inside the final image.
# AI_SANDBOXES_TOOLS_PREFIX_ROOT is a test hook: hermetic adapter tests
# (scripts/tools/tests/test-adapters.sh) redirect installs into a scratch
# directory. Production callers -- install-selected.sh under a Docker
# build -- never set this variable.
prefix_root=${AI_SANDBOXES_TOOLS_PREFIX_ROOT:-/usr/local/libexec/ai-sandboxes-tools}
prefix=$prefix_root/$tool_id
install -d "$prefix_root" || die "could not create $prefix_root"
test ! -e "$prefix" || die "refusing to overwrite existing prefix $prefix (another tool already installed here?)"
cp -a "$extract_dir/$archive_member" "$prefix" || die "could not install $archive_member to $prefix"

readarray -t expose < <(jq -r '(.expose // [.binary])[]' <<<"$entry") \
  || die "catalog entry has malformed expose list: $tool_id"
test "${#expose[@]}" -gt 0 || die "catalog entry exposes no binaries: $tool_id"
matched=
for name in "${expose[@]}"; do
  tool_path=$prefix/bin/$name
  test -x "$tool_path" || die "$tool_id exposes $name but $tool_path is missing or not executable"
  test ! -e "$destination/$name" || die "refusing to install $name into $destination: destination already exists (collision with another tool?)"
  ln -s "$tool_path" "$destination/$name" || die "could not link $name into $destination"
  test "$name" = "$binary" && matched=1
done
test "$matched" = 1 || die "$tool_id expose list does not include its primary binary $binary"
