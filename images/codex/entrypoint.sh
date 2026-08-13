#!/bin/bash
set -o pipefail

die() {
  printf '%s\n' "codex-entrypoint: $*" >&2
  exit 1
}

# The named HOME volume starts empty. Only portable SKILL.md directories are copied;
# Claude agents, commands, hooks, MCP files, and marketplace metadata are excluded.
if [ -d /opt/codex-skills ]; then
  mkdir -p "$HOME/.codex/skills" || die "could not create $HOME/.codex/skills"
  for skill in /opt/codex-skills/*; do
    [ -d "$skill" ] || continue
    name=$(basename "$skill")
    if [ ! -e "$HOME/.codex/skills/$name" ]; then
      cp -R -- "$skill" "$HOME/.codex/skills/$name" || die "could not copy skill $name"
      chmod -R u+w "$HOME/.codex/skills/$name" || die "could not make skill writable: $name"
    fi
  done
fi
(( $# > 0 )) || die 'missing command'
exec "$@"
