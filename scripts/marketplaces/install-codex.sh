#!/usr/bin/env bash
# Install Codex skills from pinned GitHub marketplaces into the immutable
# /opt/codex-skills tree at image build time.
set -o pipefail

marketplaces=${1:?usage: install-codex.sh MARKETPLACES_JSON}

die() {
  printf '%s\n' "error: $*" >&2
  exit 1
}

jq -e '
  (.codex | type == "array") and
  all(.codex[];
    (.url | type == "string" and test("^https://github\\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\\.git$") and (contains("..") | not)) and
    (.ref | type == "string" and test("^[0-9a-f]{40}$")) and
    (.skills_path | type == "string" and (. == "." or (test("^[A-Za-z0-9][A-Za-z0-9._/-]*$") and (contains("..") | not))))
  )
' "$marketplaces" >/dev/null || die "invalid Codex marketplace selection"

index=0
while IFS= read -r spec; do
  url=$(jq -er '.url' <<<"$spec") || die 'marketplace entry missing url'
  ref=$(jq -er '.ref' <<<"$spec") || die 'marketplace entry missing ref'
  path=$(jq -er '.skills_path' <<<"$spec") || die 'marketplace entry missing skills_path'
  source_dir="/opt/codex-sources/$index"
  index=$((index + 1))

  git clone -- "$url" "$source_dir" || die "could not clone $url"
  git -C "$source_dir" checkout --detach "$ref" || die "could not check out $ref in $url"
  skills="$source_dir/$path"
  test -d "$skills" || die "skills directory not found at $path in $url"

  skills_list=$(find -L "$skills" -mindepth 1 -maxdepth 1 -type d) || die "could not read skills from $skills"
  if [ -n "$skills_list" ]; then
    while IFS= read -r skill; do
      target="/opt/codex-skills/$(basename "$skill")"
      test ! -e "$target" || die "a skill already exists at $target"
      cp -RL -- "$skill" "$target" || die "could not copy skill $skill"
    done <<<"$skills_list"
  fi
done < <(jq -c '.codex[]' "$marketplaces")
