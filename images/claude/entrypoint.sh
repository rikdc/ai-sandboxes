#!/usr/bin/env bash
set -euo pipefail

die() {
  printf '%s\n' "claude-entrypoint: $*" >&2
  exit 1
}

# Plugin installations live in the immutable image cache, while Claude stores
# enablement and marketplace registration in its user home. Merge the image's
# seed defaults into the persistent home on every launch. A session profile's
# marketplaces are merged into this same seed at build time (see
# scripts/session/merge-plugin-seed.sh and docs/session-images.md), not here:
# Claude resolves a registered marketplace's code relative to
# CLAUDE_CODE_PLUGIN_CACHE_DIR at runtime, so a session-only seed pointing at
# a second, unreferenced cache directory would register cleanly but never
# actually load. The recursive merge (jq's `*`, right side wins per key,
# recursing into nested objects) generalizes past the single enabledPlugins
# key the original version of this script merged, since the seed can also
# carry extraKnownMarketplaces and potentially other object-shaped keys.
# Existing values win so a user can keep a plugin disabled or a marketplace
# entry overridden, and unrelated Claude settings are left untouched.
: "${HOME:?HOME must be set}"
: "${CLAUDE_CODE_PLUGIN_SEED_DIR:?CLAUDE_CODE_PLUGIN_SEED_DIR must be set}"
seed="$CLAUDE_CODE_PLUGIN_SEED_DIR/settings.json"
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
    if ! ln -- "$temporary" "$settings"; then
      [[ -e "$settings" ]] || die "could not create $settings"
    fi
    rm -f -- "$temporary" || die "could not remove temporary settings file"
  else
    jq -s '.[1] * .[0]' "$settings" "$seed" >"$temporary" \
      || die "could not merge plugin defaults into $settings"
    mv -f -- "$temporary" "$settings" || die "could not update $settings"
  fi
  trap - EXIT
fi

(( $# > 0 )) || die 'missing command'
exec "$@"
