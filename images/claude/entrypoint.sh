#!/usr/bin/env bash
set -euo pipefail

die() {
  printf '%s\n' "claude-entrypoint: $*" >&2
  exit 1
}

# Plugin installations live in the immutable image cache, while Claude stores
# enablement in its user home. Merge the image's selected-plugin defaults
# into the persistent home on every launch: the base image's own seed, plus
# an optional session-image seed (see docs/session-images.md) with session
# values taking precedence over base values for the same key. The user's
# already-persisted settings take precedence over both defaults, and
# unrelated Claude settings are left untouched.
: "${HOME:?HOME must be set}"
: "${CLAUDE_CODE_PLUGIN_SEED_DIR:?CLAUDE_CODE_PLUGIN_SEED_DIR must be set}"
base_seed="$CLAUDE_CODE_PLUGIN_SEED_DIR/settings.json"
session_seed_dir=${CLAUDE_CODE_SESSION_PLUGIN_SEED_DIR:-}
settings="$HOME/.claude/settings.json"

have_base_seed=false
[[ -f "$base_seed" ]] && have_base_seed=true
have_session_seed=false
[[ -n "$session_seed_dir" && -f "$session_seed_dir/settings.json" ]] && have_session_seed=true

if [[ "$have_base_seed" == true || "$have_session_seed" == true ]]; then
  settings_dir=$(dirname "$settings") || die 'could not determine the Claude settings directory'
  mkdir -p "$settings_dir" || die "could not create $settings_dir"

  defaults=$(mktemp "${settings}.XXXXXX") || die "could not create a scratch defaults file"
  trap 'rm -f -- "$defaults"' EXIT
  if [[ "$have_base_seed" == true && "$have_session_seed" == true ]]; then
    jq -s '.[0] * .[1]' "$base_seed" "$session_seed_dir/settings.json" >"$defaults" \
      || die 'could not merge base and session plugin seeds'
  elif [[ "$have_session_seed" == true ]]; then
    cp -- "$session_seed_dir/settings.json" "$defaults" || die 'could not read the session plugin seed'
  else
    cp -- "$base_seed" "$defaults" || die 'could not read the base plugin seed'
  fi

  temporary=$(mktemp "${settings}.XXXXXX") || die "could not create temporary settings file"
  trap 'rm -f -- "$defaults" "$temporary"' EXIT

  if [[ ! -e "$settings" ]]; then
    cp -- "$defaults" "$temporary" || die "could not seed $settings"
    # Linking is atomic and fails if another process created the settings file
    # after the existence check. Do not overwrite that competing state.
    if ! ln -- "$temporary" "$settings"; then
      [[ -e "$settings" ]] || die "could not create $settings"
    fi
    rm -f -- "$temporary" || die "could not remove temporary settings file"
  else
    jq -s '.[1] * .[0]' "$settings" "$defaults" >"$temporary" \
      || die "could not merge plugin defaults into $settings"
    mv -f -- "$temporary" "$settings" || die "could not update $settings"
  fi
  rm -f -- "$defaults" || die "could not remove scratch defaults file"
  trap - EXIT
fi

(( $# > 0 )) || die 'missing command'
exec "$@"
