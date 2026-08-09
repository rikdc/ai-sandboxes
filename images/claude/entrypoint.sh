#!/usr/bin/env bash
set -euo pipefail

# Plugin installations live in the immutable image cache, while Claude stores
# enablement in its user home. Merge the image's selected-plugin defaults into
# the persistent home on every launch. Existing values win so a user can keep a
# plugin disabled, and unrelated Claude settings are left untouched.
seed=/opt/claude-plugin-seed/settings.json
settings="$HOME/.claude/settings.json"

if [[ -f "$seed" ]]; then
  mkdir -p "$(dirname "$settings")"
  if [[ ! -e "$settings" ]]; then
    cp "$seed" "$settings"
  else
    temporary=$(mktemp "${settings}.XXXXXX")
    trap 'rm -f "$temporary"' EXIT
    jq -s '
      .[0] as $current |
      .[1].enabledPlugins as $seed |
      $current + {
        enabledPlugins: ($seed + ($current.enabledPlugins // {}))
      }
    ' "$settings" "$seed" >"$temporary"
    mv "$temporary" "$settings"
    trap - EXIT
  fi
fi

exec "$@"
