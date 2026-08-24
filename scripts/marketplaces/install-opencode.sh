#!/usr/bin/env bash
# Install OpenCode skills and local plugins from pinned GitHub marketplaces
# into the immutable /opt/opencode-skills and /opt/opencode-plugins trees at
# image build time.
#
# Unlike claude (plugin marketplaces) and codex (skills only), an opencode
# marketplace entry carries two optional component paths: skills_path for
# SKILL.md directories and plugins_path for .js/.ts plugin files. Both are
# copied out of the pinned checkout; npm plugins are deliberately unsupported
# because they would need registry egress at first guest start.
set -o pipefail

marketplaces=${1:?usage: install-opencode.sh MARKETPLACES_JSON}

die() {
  printf '%s\n' "error: $*" >&2
  exit 1
}

# Unlike the codex installer, component paths may start with a dot so
# conventional locations such as .opencode/plugins validate; ".." stays
# forbidden via the contains check.
component_path_test='(. == "." or (test("^[.A-Za-z0-9][A-Za-z0-9._/-]*$") and (contains("..") | not)))'

jq -e "
  (.opencode | type == \"array\") and
  all(.opencode[];
    (.url | type == \"string\" and test(\"^https://github\\\\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\\\\.git\$\") and (contains(\"..\") | not)) and
    (.ref | type == \"string\" and test(\"^[0-9a-f]{40}\$\")) and
    (.skills_path | type == \"string\" and $component_path_test) and
    ((has(\"plugins_path\") | not) or (.plugins_path | type == \"string\" and $component_path_test))
  )
" "$marketplaces" >/dev/null || die "invalid opencode marketplace selection"

index=0
while IFS= read -r spec; do
  url=$(jq -er '.url' <<<"$spec") || die 'marketplace entry missing url'
  ref=$(jq -er '.ref' <<<"$spec") || die 'marketplace entry missing ref'
  path=$(jq -er '.skills_path' <<<"$spec") || die 'marketplace entry missing skills_path'
  plugins_path=$(jq -er '.plugins_path // empty' <<<"$spec")
  source_dir="/opt/opencode-sources/$index"
  index=$((index + 1))

  git clone -- "$url" "$source_dir" || die "could not clone $url"
  git -C "$source_dir" checkout --detach "$ref" || die "could not check out $ref in $url"

  skills="$source_dir/$path"
  test -d "$skills" || die "skills directory not found at $path in $url"
  skills_list=$(find -L "$skills" -mindepth 1 -maxdepth 1 -type d) || die "could not read skills from $skills"
  if [ -n "$skills_list" ]; then
    while IFS= read -r skill; do
      target="/opt/opencode-skills/$(basename "$skill")"
      test ! -e "$target" || die "a skill already exists at $target"
      cp -RL -- "$skill" "$target" || die "could not copy skill $skill"
    done <<<"$skills_list"
  fi

  if [ -n "$plugins_path" ]; then
    plugins="$source_dir/$plugins_path"
    test -d "$plugins" || die "plugins directory not found at $plugins_path in $url"
    plugins_list=$(find "$plugins" -maxdepth 1 -type f \( -name '*.js' -o -name '*.ts' \)) \
      || die "could not read plugins from $plugins"
    if [ -n "$plugins_list" ]; then
      while IFS= read -r plugin; do
        target="/opt/opencode-plugins/$(basename "$plugin")"
        test ! -e "$target" || die "a plugin already exists at $target"
        install -m 0644 -- "$plugin" "$target" || die "could not copy plugin $plugin"
      done <<<"$plugins_list"
    fi
  fi
done < <(jq -c '.opencode[]' "$marketplaces")

# Plugin files must be readable but never writable by the guest user, so a
# compromised VM cannot edit its own extension code mid-flight.
chmod 0444 /opt/opencode-plugins/* 2>/dev/null || true
