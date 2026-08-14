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

# Repoint CLAUDE_CODE_PLUGIN_CACHE_DIR at a fresh, fully writable per-session
# copy of the immutable seed cache (/opt/claude-plugin-cache). Claude Code
# atomically rewrites `known_marketplaces.json` / `installed_plugins.json`
# (via `.tmp.XXX` + rename) and grows `marketplaces/`, `data/`, `cache/` at
# runtime, all of which need write access to the cache root. Pointing it
# straight at the seed would force the whole cache to be writable (the
# carve-out pattern this replaces — see images/claude/Dockerfile). A
# throwaway per-launch copy under /tmp gives Claude full write freedom in its
# own tree while the seed stays root-owned and read-only, so a compromised
# node process cannot unlink, rename, or replace the seeded plugins that the
# next launch would load. Guard on the path actually existing so callers that
# do not set CLAUDE_CODE_PLUGIN_CACHE_DIR (e.g. unit tests exercising only the
# settings-merge path) still no-op cleanly.
if [[ -d "${CLAUDE_CODE_PLUGIN_CACHE_DIR:-}" ]]; then
  seed_cache="$CLAUDE_CODE_PLUGIN_CACHE_DIR"
  writable_cache=$(mktemp -d /tmp/ai-sandboxes-plugin-cache.XXXXXX) \
    || die 'could not create writable plugin cache'
  # Copy the whole tree, dotfiles included; flags inherited from the seed
  # (root-owned, a-w) are re-opened below so Claude can write every path.
  cp -a -- "$seed_cache"/. "$writable_cache" \
    || die "could not seed writable plugin cache from $seed_cache"
  chmod -R u+w "$writable_cache" \
    || die 'could not make writable plugin cache writable'
  export CLAUDE_CODE_PLUGIN_CACHE_DIR="$writable_cache"
fi

(( $# > 0 )) || die 'missing command'
exec "$@"
