#!/usr/bin/env bash
set -o pipefail

repo_root=$(cd "$(dirname "$0")/../.." && pwd) || exit 1

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
snapshot=$(mktemp) || die 'could not create a scratch file for the profile snapshot'
trap 'rm -f "$snapshot"' EXIT
cp -- "$profile_path" "$snapshot" || die "cannot read profile: $profile_path"

size=$(wc -c <"$snapshot") || die "could not measure profile size: $profile_path"
test "$size" -le "$max_bytes" || die "profile exceeds $max_bytes bytes"

jq -e . "$snapshot" >/dev/null 2>&1 || die 'profile is not valid JSON'

# The renderer does not yet implement the Python layer (task 9): a profile
# requesting it would validate but be silently dropped from the built image.
# Reject it explicitly. apt, npm, and claude_marketplaces ARE implemented
# (see render-dockerfile.sh) and are structurally validated separately, below.
jq -e '
  (((.python // {}).enabled // false) == false) and
  (((.python // {}).packages // []) | length) == 0
' "$snapshot" >/dev/null 2>&1 \
  || die 'python is not yet supported; see docs/session-images.md'

jq -e --argjson max_len "$max_field_length" --argjson max_pkgs "$max_packages" '
  def short_string: type == "string" and length > 0 and length <= $max_len;
  def apt_name: short_string and test("^[a-z0-9][a-z0-9.+-]*$");
  def apt_version: short_string and test("^[A-Za-z0-9][A-Za-z0-9.:+~-]*$");
  def pkg_name: short_string and test("^(@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*$");
  def pkg_version: short_string and test("^[0-9]+\\.[0-9]+\\.[0-9]+(-[0-9A-Za-z-]+(\\.[0-9A-Za-z-]+)*)?(\\+[0-9A-Za-z-]+(\\.[0-9A-Za-z-]+)*)?$");
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
  ((keys - ["schema_version","apt","npm","python","claude_marketplaces","tools","shared_state"]) == []) and
  (.schema_version == 1) and
  ((.apt // []) as $apt | ($apt | type == "array") and all($apt[]; valid_apt_entry) and (($apt | map(.name) | length) == ($apt | map(.name) | unique | length))) and
  ((.npm // []) as $npm | ($npm | type == "array") and all($npm[]; valid_pkg_entry) and (($npm | map(.package) | length) == ($npm | map(.package) | unique | length))) and
  ((.python // {}) as $py |
    ($py | type == "object") and
    ((($py | keys) - ["enabled","packages"]) == []) and
    (($py.enabled // false) | type == "boolean") and
    (($py.packages // []) as $pp | ($pp | type == "array") and all($pp[]; valid_pkg_entry))) and
  ((.claude_marketplaces // []) as $mp | ($mp | type == "array") and all($mp[]; valid_marketplace_entry)) and
  ((((.apt // []) | length) + ((.npm // []) | length) + (((.python.packages) // []) | length)) <= $max_pkgs)
' "$snapshot" >/dev/null || die 'invalid session profile'

# tools/shared_state reuse the exact structural and semantic checks
# scripts/tools/validate-selection.sh already enforces for the base-image
# tool-selection mechanism (config/tools.json + config/runtime.json), rather
# than re-implementing the id/version/sha256/shared-state regexes a second
# time and risking the two copies drifting apart. Only config/tool-catalog.json
# (repository-controlled, never modified by a profile) is ever passed as the
# catalog; a profile can select an id already in it, never define a new one.
tools_selection=$(jq -c '{tools: (.tools // [])}' "$snapshot") || die 'could not read tools selection from profile'
tools_runtime=$(jq -c '{shared_state: (.shared_state // null)}' "$snapshot") || die 'could not read shared_state from profile'

tools_selection_tmp=$(mktemp) || die 'could not create a scratch file for the tools selection'
tools_runtime_tmp=$(mktemp) || die 'could not create a scratch file for the shared-state request'
trap 'rm -f "$snapshot" "$tools_selection_tmp" "$tools_runtime_tmp"' EXIT
printf '%s\n' "$tools_selection" >"$tools_selection_tmp" || die 'could not write tools selection scratch file'
printf '%s\n' "$tools_runtime" >"$tools_runtime_tmp" || die 'could not write shared-state scratch file'

"$repo_root/scripts/tools/validate-selection.sh" "$repo_root/config/tool-catalog.json" "$tools_selection_tmp" "$tools_runtime_tmp" >/dev/null 2>&1 \
  || die 'invalid tools or shared_state; see docs/session-images.md'

# scripts/tools/validate-selection.sh only enforces the direction a
# state_wrapper tool requires shared_state, not the reverse. A session
# profile is per-invocation, host-supplied data (unlike the base image's
# config/runtime.json, which a host maintains deliberately for its own
# fixed set of runtime-selected tools), so reject a shared_state request
# that no selected tool would actually consume, rather than silently
# provisioning an unused host-side volume. See docs/session-images.md.
jq -e --slurpfile catalog "$repo_root/config/tool-catalog.json" '
  (.shared_state // null) == null or
  ((.tools // []) as $selected |
    any($catalog[0].tools[]; (.id as $id | $selected | any(.id == $id)) and (.state_wrapper != null)))
' "$snapshot" >/dev/null 2>&1 || die 'shared_state is set but no selected tool requires shared state'

jq -Sc . "$snapshot"
