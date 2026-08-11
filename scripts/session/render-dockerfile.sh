#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "$0")/../.." && pwd)

context_dir=${1:?usage: render-dockerfile.sh CONTEXT_DIR BASE_IMAGE_REF CANONICAL_PROFILE_JSON}
base_image_ref=${2:?usage: render-dockerfile.sh CONTEXT_DIR BASE_IMAGE_REF CANONICAL_PROFILE_JSON}
canonical_profile=${3:?usage: render-dockerfile.sh CONTEXT_DIR BASE_IMAGE_REF CANONICAL_PROFILE_JSON}

test -f "$context_dir/resolved.json" || {
  echo 'render-dockerfile: missing resolved.json in context' >&2
  exit 1
}

marketplaces=$(jq -c '.claude_marketplaces // []' <<<"$canonical_profile")
marketplace_count=$(jq 'length' <<<"$marketplaces")

if test "$marketplace_count" -eq 0; then
  # FROM references the caller's private, pre-verified pin of the base
  # image's exact content (see resolve-image.sh), not the mutable
  # ai-sandboxes-claude:local tag directly: a concurrent `./scripts/build`
  # could otherwise retag the base between when its digest was captured and
  # when BuildKit resolves this Dockerfile, silently building from different
  # content than the cache key claims. This must be a plain local tag, not a
  # name@digest reference: for a purely local (never pushed) image, BuildKit
  # resolves name@digest as a registry manifest lookup and fails, rather
  # than matching it against the local image store.
  cat >"$context_dir/Dockerfile" <<EOF
# syntax=docker/dockerfile:1.7
FROM $base_image_ref
USER root
COPY --chown=root:root --chmod=0444 resolved.json /opt/session-profile/resolved.json
USER node
EOF
  exit 0
fi

# Session marketplaces reuse the base image's own pinned installer
# (scripts/marketplaces/install-claude.sh, copied in verbatim below — this
# context dir must never reference the checkout path directly from the
# Dockerfile) and install additively into the SAME /opt/claude-plugin-cache
# and /opt/claude-plugin-seed paths the base image already uses, not a
# second, separate root: Claude resolves a registered marketplace's code
# relative to CLAUDE_CODE_PLUGIN_CACHE_DIR at runtime (confirmed empirically
# — a marketplace registered against a cache directory nothing points to at
# runtime shows as "No marketplaces configured" even though its settings.json
# entry and cloned content both exist), so a separate session-only cache root
# would register cleanly but never actually load. Those base paths are
# read-only in the base image; this build stage temporarily reclaims write
# access, installs, merges the resulting settings.json with any pre-existing
# base seed (scripts/session/merge-plugin-seed.sh, session values winning),
# and re-locks before the final stage copies the augmented directories back
# to their standard paths. Mirrors images/claude/Dockerfile's own build/final
# split: install in a discarded build stage with a throwaway HOME, then copy
# only the resulting cache/seed directories into the final image. The
# build-stage relock (`chmod -R a-w`) sweeps /opt/claude-plugin-cache/data
# too, since it already exists (copied in with the rest of the cache from
# the base image this build stage starts FROM) — the base Dockerfile
# deliberately keeps that one subdirectory node-owned and writable after
# locking everything else, for Claude's own runtime plugin state, so the
# final stage below must recreate it the same way after the COPY, or a
# marketplace-derived session image ships with that directory read-only.
jq -n --argjson claude "$marketplaces" '{claude: $claude, codex: []}' \
  >"$context_dir/session-marketplaces.json"
cp -- "$repo_root/scripts/marketplaces/install-claude.sh" "$context_dir/install-claude-marketplaces.sh"
cp -- "$repo_root/scripts/session/merge-plugin-seed.sh" "$context_dir/merge-plugin-seed.sh"

cat >"$context_dir/Dockerfile" <<EOF
# syntax=docker/dockerfile:1.7
FROM $base_image_ref AS build
USER root
COPY --chown=node:node session-marketplaces.json /opt/session-marketplaces.json
COPY --chown=node:node --chmod=0755 install-claude-marketplaces.sh /usr/local/lib/ai-sandboxes/install-session-claude-marketplaces.sh
COPY --chown=node:node --chmod=0755 merge-plugin-seed.sh /usr/local/lib/ai-sandboxes/merge-session-plugin-seed.sh
RUN chown -R node:node /opt/claude-plugin-cache /opt/claude-plugin-seed \\
 && chmod -R u+w /opt/claude-plugin-cache /opt/claude-plugin-seed \\
 && install -d -o node -g node -m 0755 /opt/claude-session-build-home /opt/claude-marketplaces
USER node
ENV HOME=/opt/claude-session-build-home CLAUDE_CODE_PLUGIN_CACHE_DIR=/opt/claude-plugin-cache CLAUDE_CODE_PLUGIN_SEED_DIR=/opt/claude-plugin-seed
RUN /usr/local/lib/ai-sandboxes/install-session-claude-marketplaces.sh /opt/session-marketplaces.json
USER root
RUN /usr/local/lib/ai-sandboxes/merge-session-plugin-seed.sh /opt/claude-session-build-home/.claude/settings.json /opt/claude-plugin-seed/settings.json \\
 && chown -R root:root /opt/claude-plugin-cache /opt/claude-plugin-seed \\
 && chmod -R a-w /opt/claude-plugin-cache /opt/claude-plugin-seed
USER node
FROM $base_image_ref
USER root
COPY --from=build --chown=root:root /opt/claude-plugin-cache /opt/claude-plugin-cache
COPY --from=build --chown=root:root /opt/claude-plugin-seed /opt/claude-plugin-seed
RUN install -d -o node -g node -m 0755 /opt/claude-plugin-cache/data
COPY --chown=root:root --chmod=0444 resolved.json /opt/session-profile/resolved.json
USER node
EOF
