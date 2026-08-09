#!/usr/bin/env bash
set -euo pipefail

marketplaces=${1:?usage: install-claude.sh MARKETPLACES_JSON}

jq -e '(.claude | type == "array") and all(.claude[]; (.url | type == "string") and (.ref | type == "string") and (.path | type == "string"))' "$marketplaces" >/dev/null

index=0
while IFS= read -r spec; do
  url=$(jq -er '.url' <<<"$spec")
  ref=$(jq -er '.ref' <<<"$spec")
  path=$(jq -er '.path' <<<"$spec")
  source_dir="/opt/claude-marketplaces/$index"
  index=$((index + 1))

  git clone "$url" "$source_dir"
  git -C "$source_dir" checkout --detach "$ref"
  manifest="$source_dir/$path/.claude-plugin/marketplace.json"
  test -f "$manifest"
  marketplace=$(jq -er '.name' "$manifest")

  if test "$marketplace" = claude-plugins-official; then
    if test "$url" != https://github.com/anthropics/claude-plugins-official.git; then
      printf '%s\n' "claude-plugins-official must use https://github.com/anthropics/claude-plugins-official.git" >&2
      exit 1
    fi

    # Claude reserves this name for its GitHub source. Registering a local
    # pinned checkout is rejected, so establish the trusted source identity,
    # then replace Claude's cache with the profile-pinned revision before any
    # plugin is installed from it.
    claude plugin marketplace add anthropics/claude-plugins-official
    cache_dir="$CLAUDE_CODE_PLUGIN_CACHE_DIR/marketplaces/$marketplace"
    git -C "$cache_dir" fetch --depth=1 origin "$ref"
    git -C "$cache_dir" checkout --detach "$ref"
    test "$(git -C "$cache_dir" rev-parse HEAD)" = "$ref"
    manifest="$cache_dir/$path/.claude-plugin/marketplace.json"
    test -f "$manifest"
  else
    claude plugin marketplace add "$source_dir/$path"
  fi

  jq -er '.plugins[] | .name' "$manifest" | while IFS= read -r plugin; do
    claude plugin install "${plugin}@${marketplace}" --scope user
  done
done < <(jq -c '.claude[]' "$marketplaces")
