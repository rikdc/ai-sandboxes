#!/usr/bin/env bash
set -euo pipefail

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
  duplicates=$(jq -r '.tools[].id' "$file" | sort | uniq -d)
  test -z "$duplicates" || fail "duplicate tool id(s): $duplicates"
}

validate_catalog_entry() {
  local entry=$1
  local id
  id=$(jq -er '.id' <<<"$entry") || fail 'catalog tool is missing an id'

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

while IFS= read -r id; do
  entry=$(jq -ce --arg id "$id" '.tools[] | select(.id == $id)' "$catalog") || fail "unknown tool id: $id"
  selected=$(jq -ce --arg id "$id" '.tools[] | select(.id == $id)' "$selection")
  jq -e '((keys | sort) == ["id", "sha256", "version"]) and (.version | type == "string" and test("^[A-Za-z0-9][A-Za-z0-9._-]*$") and (contains("..") | not)) and (.sha256 | type == "string" and test("^[0-9a-f]{64}$"))' <<<"$selected" >/dev/null \
    || fail "invalid release selection: $id"
  if jq -e '.state_wrapper != null' <<<"$entry" >/dev/null; then
    jq -e '.shared_state != null' "$runtime" >/dev/null || fail "$id requires shared state"
  fi
done < <(jq -r '.tools[].id' "$selection")
