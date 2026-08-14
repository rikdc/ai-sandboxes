#!/bin/bash
set -o pipefail

die() {
  printf '%s\n' "codex-entrypoint: $*" >&2
  exit 1
}

: "${HOME:?HOME must be set}"

seed_dir="${CODEX_SKILLS_SEED_DIR:-/opt/codex-skills}"
skills_home="$HOME/.codex/skills"

# If no seed directory, nothing to do.
if [ ! -d "$seed_dir" ]; then
  (( $# > 0 )) || die 'missing command'
  exec "$@"
fi

mkdir -p "$skills_home" || die "could not create $skills_home"

# Phase 1: Ensure every managed skill is present as an immutable symlink.
for skill in "$seed_dir"/*; do
  [ -d "$skill" ] || continue
  name=$(basename "$skill")
  target="$skills_home/$name"

  if [ ! -e "$target" ]; then
    ln -s -- "$skill" "$target" || die "could not link managed skill $name"
  elif [ -L "$target" ]; then
    current=$(readlink -- "$target") || die "could not read symlink $name"
    if [ "$current" != "$skill" ]; then
      die "managed skill $name collides with existing symlink: $current"
    fi
  else
    die "managed skill $name collides with user-owned entry in $skills_home"
  fi
done

# Phase 2: Remove stale managed symlinks (skills removed from the image).
if [ -d "$skills_home" ]; then
  for entry in "$skills_home"/*; do
    [ -e "$entry" ] || continue  # guard against empty dir after glob
    [ -L "$entry" ] || continue  # only manage symlinks; leave user files alone

    current=$(readlink -- "$entry") || continue
    case "$current" in
      "$seed_dir"/*)
        if [ ! -e "$current" ]; then
          rm -- "$entry" || die "could not remove stale managed skill $(basename "$entry")"
        fi
        ;;
    esac
  done
fi

(( $# > 0 )) || die 'missing command'
exec "$@"
