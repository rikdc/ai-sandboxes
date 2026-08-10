#!/usr/bin/env bash
set -euo pipefail

die() {
  printf '%s\n' "claude-entrypoint: $*" >&2
  exit 1
}

# Plugin installations live in the immutable image cache, while Claude stores
# enablement in its user home. Merge the image's selected-plugin defaults into
# the persistent home on every launch. Existing values win so a user can keep a
# plugin disabled, and unrelated Claude settings are left untouched.
: "${HOME:?HOME must be set}"
seed=/opt/claude-plugin-seed/settings.json
settings="$HOME/.claude/settings.json"

if [[ -f "$seed" ]]; then
  settings_dir=$(dirname "$settings") || die 'could not determine the Claude settings directory'
  mkdir -p "$settings_dir" || die "could not create $settings_dir"
  temporary=$(mktemp "${settings}.XXXXXX") || die "could not create temporary settings file"
  trap 'rm -f -- "$temporary"' EXIT

  if [[ ! -e "$settings" ]]; then
    cp -- "$seed" "$temporary" || die "could not seed $settings"
    # Linking is atomic and fails if another process created the settings file
    # after the existence check. Do not overwrite that competing state.
    ln -- "$temporary" "$settings" || die "refusing to overwrite newly created $settings"
    rm -f -- "$temporary" || die "could not remove temporary settings file"
  else
    jq -s '
      .[0] as $current |
      .[1].enabledPlugins as $seed |
      $current + {
        enabledPlugins: ($seed + ($current.enabledPlugins // {}))
      }
    ' "$settings" "$seed" >"$temporary" || die "could not merge selected plugins into $settings"
    mv -f -- "$temporary" "$settings" || die "could not update $settings"
  fi
  trap - EXIT
fi

(( $# > 0 )) || die 'missing command'
exec "$@"
