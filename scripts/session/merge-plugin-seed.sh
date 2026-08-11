#!/usr/bin/env bash
set -euo pipefail

die() {
  printf '%s\n' "merge-plugin-seed: $*" >&2
  exit 1
}

# Merge a freshly-built settings.json (produced by this build stage's own
# marketplace/plugin installs) into any pre-existing plugin seed from the
# base image, the fresh settings taking precedence over the existing seed
# for the same key, and install the result as the new seed. Session values
# must win here, the same way the base and session precedence was always
# intended, even though the merge now happens at build time instead of at
# container launch (see images/claude/entrypoint.sh and
# docs/session-images.md for why: Claude resolves a registered marketplace's
# code relative to CLAUDE_CODE_PLUGIN_CACHE_DIR at runtime, so a seed
# produced against a second, unreferenced cache directory would register
# cleanly but never actually load). If the base image had no prior seed, the
# fresh settings become the seed directly. If this build produced no
# settings.json at all (the profile's marketplace/plugin install produced
# none, which should not happen for a non-empty claude_marketplaces profile
# but is not this script's job to police), do nothing.
build_settings=${1:?usage: merge-plugin-seed.sh BUILD_SETTINGS_JSON SEED_PATH}
seed_path=${2:?usage: merge-plugin-seed.sh BUILD_SETTINGS_JSON SEED_PATH}

test -f "$build_settings" || exit 0

merged=$(mktemp) || die 'could not create a scratch merge file'
trap 'rm -f -- "$merged"' EXIT
if test -f "$seed_path"; then
  jq -s '.[0] * .[1]' "$seed_path" "$build_settings" >"$merged" \
    || die "could not merge $seed_path with $build_settings"
else
  cp -- "$build_settings" "$merged" || die "could not read $build_settings"
fi
install -D -o node -g node -m 0644 "$merged" "$seed_path" \
  || die "could not install $seed_path"
