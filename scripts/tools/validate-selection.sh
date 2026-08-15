#!/usr/bin/env bash
set -o pipefail

catalog=${1:?usage: validate-selection.sh CATALOG SELECTION RUNTIME}
selection=${2:?usage: validate-selection.sh CATALOG SELECTION RUNTIME}
runtime=${3:?usage: validate-selection.sh CATALOG SELECTION RUNTIME}

fail() {
  echo "invalid tool configuration: $*" >&2
  exit 2
}

require_unique_ids() {
  local file=$1
  local duplicates
  duplicates=$(jq -r '.tools[].id' "$file" | sort | uniq -d) || fail "could not read tool ids from $file"
  test -z "$duplicates" || fail "duplicate tool id(s): $duplicates"
}

validate_url_template() {
  jq -e '
    (.url_template | type == "string") and
    (.url_template | startswith("https://")) and
    (.url_template | contains("{{version}}")) and
    (.url_template | gsub("\\{\\{version\\}\\}"; "") | (contains("{") or contains("}")) | not) and
    (.url_template | (contains("..") or contains("@") or contains(" ") or contains("\t")) | not)
  '
}

validate_catalog_entry() {
  local entry=$1
  local id adapter
  id=$(jq -er '.id' <<<"$entry") || fail 'catalog tool is missing an id'
  adapter=$(jq -er '.adapter' <<<"$entry") || fail "catalog entry missing adapter: $id"

  case "$adapter" in
    github-release-tar)
      jq -e '
        (.id | type == "string" and test("^[a-z][a-z0-9-]*$")) and
        (.adapter == "github-release-tar") and
        (.repository | type == "string" and test("^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$") and (contains("..") | not)) and
        (.asset | type == "string" and test("^[A-Za-z0-9][A-Za-z0-9._-]*$") and (contains("..") | not)) and
        (.archive_member | type == "string" and test("^[A-Za-z0-9][A-Za-z0-9._/-]*$") and (contains("..") | not)) and
        (.binary | type == "string" and test("^[a-z][a-z0-9-]*$")) and
        ((keys | sort) == ["adapter", "archive_member", "asset", "binary", "id", "repository"] or
         (keys | sort) == ["adapter", "archive_member", "asset", "binary", "id", "repository", "state_wrapper"])
      ' <<<"$entry" >/dev/null || fail "invalid catalog entry: $id"
      ;;
    https-tar)
      jq -e '
        (.id | type == "string" and test("^[a-z][a-z0-9-]*$")) and
        (.adapter == "https-tar") and
        (.archive_member | type == "string" and test("^[A-Za-z0-9][A-Za-z0-9._/-]*$") and (contains("..") | not) and (endswith("/") | not)) and
        (.binary | type == "string" and test("^[a-z][a-z0-9-]*$")) and
        ((.expose // null) == null or (.expose as $e |
          ($e | type == "array") and
          ($e | length > 0) and
          ($e | all(type == "string" and test("^[a-z][a-z0-9-]*$"))) and
          (($e | length) == ($e | unique | length)) and
          ($e | any(. == $binary))
        )) and
        ((keys | sort) == ["adapter", "archive_member", "binary", "id", "url_template"] or
         (keys | sort) == ["adapter", "archive_member", "binary", "expose", "id", "url_template"])
      ' --arg binary "$(jq -er '.binary' <<<"$entry")" <<<"$entry" >/dev/null || fail "invalid catalog entry: $id"
      validate_url_template <<<"$entry" >/dev/null || fail "invalid catalog entry: $id"
      ;;
    awscli-zip)
      jq -e '
        (.id | type == "string" and test("^[a-z][a-z0-9-]*$")) and
        (.adapter == "awscli-zip") and
        (.binary | type == "string" and test("^[a-z][a-z0-9-]*$")) and
        ((keys | sort) == ["adapter", "binary", "id", "url_template"])
      ' <<<"$entry" >/dev/null || fail "invalid catalog entry: $id"
      validate_url_template <<<"$entry" >/dev/null || fail "invalid catalog entry: $id"
      ;;
    *)
      fail "unknown adapter for $id: $adapter"
      ;;
  esac

  if jq -e '.state_wrapper != null' <<<"$entry" >/dev/null; then
    jq -e '
      (.state_wrapper | type == "object") and
      (.state_wrapper | keys | sort == ["database", "directory", "environment"]) and
      (.state_wrapper.directory | type == "string" and test("^[a-z][a-z0-9-]*$")) and
      (.state_wrapper.environment | type == "string" and test("^[A-Z][A-Z0-9_]*$")) and
      (.state_wrapper.database | type == "string" and test("^[a-z][a-z0-9._-]*$"))
    ' <<<"$entry" >/dev/null || fail "invalid state wrapper: $id"
  fi
}

jq -e 'type == "object" and (keys | sort) == ["schema_version", "tools"] and .schema_version == 1 and (.tools | type == "array") and all(.tools[]; type == "object")' "$catalog" >/dev/null \
  || fail 'invalid catalog document'
require_unique_ids "$catalog"
while IFS= read -r entry; do
  validate_catalog_entry "$entry"
done < <(jq -c '.tools[]' "$catalog")

jq -e 'type == "object" and (keys | sort) == ["tools"] and (.tools | type == "array") and all(.tools[]; type == "object" and (.id | type == "string"))' "$selection" >/dev/null \
  || fail 'invalid tool selection document'
require_unique_ids "$selection"

jq -e '
  type == "object" and
  (keys | sort) == ["shared_state"] and
  (.shared_state == null or (
    (.shared_state | type == "object") and
    (.shared_state | keys | sort == ["id", "quota"]) and
    (.shared_state.id | type == "string" and test("^[a-z0-9][a-z0-9-]{0,62}$")) and
    (.shared_state.quota | type == "string" and test("^[1-9][0-9]*[KMGT]$"))
  ))
' "$runtime" >/dev/null || fail 'invalid runtime document'

declare -A claimed_binaries=()
while IFS= read -r id; do
  entry=$(jq -ce --arg id "$id" '.tools[] | select(.id == $id)' "$catalog") || fail "unknown tool id: $id"
  selected=$(jq -ce --arg id "$id" '.tools[] | select(.id == $id)' "$selection") || fail "could not read selection entry: $id"
  jq -e '((keys | sort) == ["id", "sha256", "version"]) and (.version | type == "string" and test("^[A-Za-z0-9][A-Za-z0-9._-]*$") and (contains("..") | not)) and (.sha256 | type == "string" and test("^[0-9a-f]{64}$"))' <<<"$selected" >/dev/null \
    || fail "invalid release selection: $id"
  if jq -e '.state_wrapper != null' <<<"$entry" >/dev/null; then
    jq -e '.shared_state != null' "$runtime" >/dev/null || fail "$id requires shared state"
  fi
  # Collect the /usr/local/bin names this tool would install so two selected
  # tools cannot silently overwrite each other's launcher. The catalog's own
  # unique-id constraint does not cover this: two distinct catalog ids can
  # still expose the same command name.
  while IFS= read -r name; do
    test -n "$name" || continue
    if test -n "${claimed_binaries[$name]:-}"; then
      fail "binary name collision on '$name': ${claimed_binaries[$name]} and $id both install it"
    fi
    claimed_binaries[$name]=$id
  done < <(jq -r '(.expose // [.binary])[]' <<<"$entry")
done < <(jq -r '.tools[].id' "$selection")
