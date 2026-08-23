#!/bin/bash
set -o pipefail

die() {
  printf '%s\n' "opencode-entrypoint: $*" >&2
  exit 1
}

: "${HOME:?HOME must be set}"

# Two managed seed trees: skills (SKILL.md directories) and plugins (local
# .js/.ts files), both symlinked into the OpenCode config home. npm-installed
# plugins are deliberately unsupported (they would need registry egress at
# first guest start); see scripts/marketplaces/install-opencode.sh.
skills_seed_dir="${OPENCODE_SKILLS_SEED_DIR:-/opt/opencode-skills}"
plugins_seed_dir="${OPENCODE_PLUGINS_SEED_DIR:-/opt/opencode-plugins}"
skills_home="$HOME/.config/opencode/skills"
plugins_home="$HOME/.config/opencode/plugins"

sync_tree() {
  local seed_dir=$1 managed_home=$2 kind=$3

  # If no seed directory, nothing to do for this tree.
  [ -d "$seed_dir" ] || return 0

  mkdir -p "$managed_home" || die "could not create $managed_home"

  # Phase 1: Ensure every managed entry is present as an immutable symlink.
  for entry in "$seed_dir"/*; do
    [ -e "$entry" ] || continue
    name=$(basename "$entry")
    target="$managed_home/$name"

    if [ ! -e "$target" ]; then
      ln -s -- "$entry" "$target" || die "could not link managed $kind $name"
    elif [ -L "$target" ]; then
      current=$(readlink -- "$target") || die "could not read symlink $name"
      if [ "$current" != "$entry" ]; then
        die "managed $kind $name collides with existing symlink: $current"
      fi
    else
      die "managed $kind $name collides with user-owned entry in $managed_home"
    fi
  done

  # Phase 2: Remove stale managed symlinks (entries removed from the image).
  # Dangling symlinks are false under [ -e ], so check -L first — otherwise the
  # very case we want to clean up (target gone) would be silently skipped.
  for path in "$managed_home"/*; do
    [ -L "$path" ] || [ -e "$path" ] || continue  # skip empty-glob literal
    [ -L "$path" ] || continue                    # only manage symlinks

    current=$(readlink -- "$path") || continue
    case "$current" in
      "$seed_dir"/*)
        if [ ! -e "$current" ]; then
          rm -- "$path" || die "could not remove stale managed $kind $(basename "$path")"
        fi
        ;;
    esac
  done
}

sync_tree "$skills_seed_dir" "$skills_home" skill
sync_tree "$plugins_seed_dir" "$plugins_home" plugin

(( $# > 0 )) || die 'missing command'
exec "$@"
