#!/bin/bash
set -euo pipefail

# The named HOME volume starts empty. Only portable SKILL.md directories are copied;
# Claude agents, commands, hooks, MCP files, and marketplace metadata are excluded.
if [ -d /opt/codex-skills ]; then
  mkdir -p "$HOME/.codex/skills"
  for skill in /opt/codex-skills/*; do
    [ -d "$skill" ] || continue
    name=$(basename "$skill")
    if [ ! -e "$HOME/.codex/skills/$name" ]; then
      cp -R "$skill" "$HOME/.codex/skills/$name"
    fi
  done
fi
exec "$@"
