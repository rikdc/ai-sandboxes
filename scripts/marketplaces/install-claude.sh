#!/usr/bin/env bash
set -o pipefail

marketplaces=${1:?usage: install-claude.sh MARKETPLACES_JSON}

die() {
  printf '%s\n' "error: $*" >&2
  exit 1
}

validate_selected_plugins() {
  local manifest=$1
  shift
  local plugin
  for plugin in "$@"; do
    jq -e --arg plugin "$plugin" 'any(.plugins[]?; .name == $plugin)' "$manifest" >/dev/null \
      || die "plugin $plugin is not declared by $manifest"
  done
}

jq -e '
  def selected_plugins: (.plugins? // []);
  (.claude | type == "array") and
  all(.claude[];
    (.url | type == "string" and test("^https://github\\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\\.git$") and (contains("..") | not)) and
    (.ref | type == "string" and test("^[0-9a-f]{40}$")) and
    (.path | type == "string" and (. == "." or (test("^[A-Za-z0-9][A-Za-z0-9._/-]*$") and (contains("..") | not)))) and
    (selected_plugins | type == "array") and
    (selected_plugins | all(.[]; type == "string" and test("^[a-z0-9][a-z0-9-]*[a-z0-9]$|^[a-z0-9]$"))) and
    ((selected_plugins | length) == (selected_plugins | unique | length))
  )
' "$marketplaces" >/dev/null || die "invalid Claude marketplace selection"

# Marketplace entries may identify public GitHub sources without choosing a
# transport. Claude currently resolves those entries through SSH. Keep strict
# host verification intact by using HTTPS (and its normal CA validation)
# instead of seeding a mutable SSH known_hosts entry into the image.
git config --global url."https://github.com/".insteadOf git@github.com: \
  || die 'could not configure git HTTPS transport rewrite'
git config --global --add url."https://github.com/".insteadOf ssh://git@github.com/ \
  || die 'could not configure git HTTPS transport rewrite'

index=0
while IFS= read -r spec; do
  url=$(jq -er '.url' <<<"$spec") || die 'marketplace entry missing url'
  ref=$(jq -er '.ref' <<<"$spec") || die 'marketplace entry missing ref'
  path=$(jq -er '.path' <<<"$spec") || die 'marketplace entry missing path'
  repository=${url#https://github.com/}
  repository=${repository%.git}
  source_dir="/opt/claude-marketplaces/$index"
  index=$((index + 1))

  git clone --no-checkout --no-single-branch -- "$url" "$source_dir" || die "could not clone $url"
  git -C "$source_dir" checkout --detach "$ref" || die "could not check out $ref in $url"
  manifest="$source_dir/$path/.claude-plugin/marketplace.json"
  test -f "$manifest" || die "marketplace manifest not found at $path in $url"
  marketplace=$(jq -er '.name' "$manifest") || die "marketplace manifest missing name: $manifest"
  [[ "$marketplace" =~ ^[a-z][a-z0-9-]*$ ]] || die "invalid marketplace name: $marketplace"
  mapfile -t selected_plugins < <(jq -r '(.plugins? // [])[]' <<<"$spec") \
    || die "could not read selected plugins for $marketplace"
  validate_selected_plugins "$manifest" "${selected_plugins[@]}"

  # Register by GitHub identity rather than the build-stage checkout. Claude
  # can then reload this marketplace from its persisted cache at runtime.
  claude plugin marketplace add "$repository" || die "could not register marketplace $repository"
  cache_dir="$CLAUDE_CODE_PLUGIN_CACHE_DIR/marketplaces/$marketplace"
  test -d "$cache_dir" || die "Claude did not create marketplace cache: $marketplace"
  git -C "$cache_dir" fetch --depth=1 origin "$ref" || die "could not fetch $ref into marketplace cache: $marketplace"
  git -C "$cache_dir" checkout --detach "$ref" || die "could not check out $ref in marketplace cache: $marketplace"
  test "$(git -C "$cache_dir" rev-parse HEAD)" = "$ref" || die "marketplace cache is not pinned: $marketplace"
  manifest="$cache_dir/$path/.claude-plugin/marketplace.json"
  test -f "$manifest" || die "marketplace manifest is missing from pinned cache: $marketplace"
  test "$(jq -er '.name' "$manifest")" = "$marketplace" || die "marketplace name changed after cache pinning: $marketplace"
  validate_selected_plugins "$manifest" "${selected_plugins[@]}"

  for plugin in "${selected_plugins[@]}"; do
    claude plugin install "${plugin}@${marketplace}" --scope user || die "could not install plugin $plugin@$marketplace"
  done
done < <(jq -c '.claude[]' "$marketplaces")
