#!/usr/bin/env bash
set -euo pipefail

profile_path=${1:?usage: validate-profile.sh PROFILE_PATH}

die() {
  printf 'validate-profile: %s\n' "$*" >&2
  exit 2
}

max_bytes=32768
max_field_length=200
max_packages=50

test -r "$profile_path" || die "cannot read profile: $profile_path"

# Snapshot once so a profile living under a writable/mounted path cannot change
# between the size check below and the validation/canonicalization passes that
# follow: every subsequent read comes from this private copy, not the original.
snapshot=$(mktemp)
trap 'rm -f "$snapshot"' EXIT
cp -- "$profile_path" "$snapshot" || die "cannot read profile: $profile_path"

size=$(wc -c <"$snapshot")
test "$size" -le "$max_bytes" || die "profile exceeds $max_bytes bytes"

jq -e . "$snapshot" >/dev/null 2>&1 || die 'profile is not valid JSON'

# The renderer emits an apt/npm/Python-free Dockerfile: those layers are not
# implemented yet, so a profile requesting them would validate but be
# silently dropped from the built image. Reject them explicitly.
# claude_marketplaces IS implemented (see render-dockerfile.sh) and is
# structurally validated separately, below.
jq -e '
  ((.apt // []) | length) == 0 and
  ((.npm // []) | length) == 0 and
  (((.python // {}).enabled // false) == false) and
  (((.python // {}).packages // []) | length) == 0
' "$snapshot" >/dev/null 2>&1 \
  || die 'apt, npm, and python are not yet supported; see docs/session-images.md'

jq -e --argjson max_len "$max_field_length" --argjson max_pkgs "$max_packages" '
  def short_string: type == "string" and length > 0 and length <= $max_len;
  def apt_name: short_string and test("^[a-z0-9][a-z0-9.+-]*$");
  def apt_version: short_string and test("^[A-Za-z0-9][A-Za-z0-9.:+~-]*$");
  def pkg_name: short_string and test("^(@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*$");
  def pkg_version: short_string and test("^[A-Za-z0-9][A-Za-z0-9.+_-]*$");
  def marketplace_url: short_string and test("^https://github\\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\\.git$") and (contains("..") | not);
  def marketplace_ref: short_string and test("^[0-9a-f]{40}$");
  def marketplace_path: short_string and (. == "." or (test("^[A-Za-z0-9][A-Za-z0-9._/-]*$") and (contains("..") | not)));
  def plugin_name: short_string and test("^[a-z0-9]([a-z0-9-]*[a-z0-9])?$");

  def valid_apt_entry:
    type == "object" and
    ((keys - ["name","version"]) == []) and
    (.name | apt_name) and
    ((has("version") | not) or (.version | apt_version));

  def valid_pkg_entry:
    type == "object" and
    ((keys | sort) == ["package","version"]) and
    (.package | pkg_name) and
    (.version | pkg_version);

  def valid_marketplace_entry:
    type == "object" and
    ((keys - ["url","ref","path","plugins"]) == []) and
    (.url | marketplace_url) and
    (.ref | marketplace_ref) and
    ((.path // ".") | marketplace_path) and
    ((.plugins // []) as $p |
      ($p | type == "array") and
      all($p[]; plugin_name) and
      (($p | length) == ($p | unique | length)));

  (type == "object") and
  ((keys - ["schema_version","apt","npm","python","claude_marketplaces"]) == []) and
  (.schema_version == 1) and
  ((.apt // []) as $apt | ($apt | type == "array") and all($apt[]; valid_apt_entry)) and
  ((.npm // []) as $npm | ($npm | type == "array") and all($npm[]; valid_pkg_entry)) and
  ((.python // {}) as $py |
    ($py | type == "object") and
    ((($py | keys) - ["enabled","packages"]) == []) and
    (($py.enabled // false) | type == "boolean") and
    (($py.packages // []) as $pp | ($pp | type == "array") and all($pp[]; valid_pkg_entry))) and
  ((.claude_marketplaces // []) as $mp | ($mp | type == "array") and all($mp[]; valid_marketplace_entry)) and
  ((((.apt // []) | length) + ((.npm // []) | length) + (((.python.packages) // []) | length)) <= $max_pkgs)
' "$snapshot" >/dev/null || die 'invalid session profile'

jq -Sc . "$snapshot"
