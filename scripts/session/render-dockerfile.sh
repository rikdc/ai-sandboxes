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
# Dockerfile) but install into a second, session-specific cache/seed path
# rather than the base image's /opt/claude-plugin-cache and
# /opt/claude-plugin-seed, so the base image's own marketplace selection is
# untouched. Mirrors images/claude/Dockerfile's own build/final split:
# install in a discarded build stage with a throwaway HOME, then copy only
# the resulting cache/seed directories into the final image.
jq -n --argjson claude "$marketplaces" '{claude: $claude, codex: []}' \
  >"$context_dir/session-marketplaces.json"
cp -- "$repo_root/scripts/marketplaces/install-claude.sh" "$context_dir/install-claude-marketplaces.sh"

cat >"$context_dir/Dockerfile" <<EOF
# syntax=docker/dockerfile:1.7
FROM $base_image_ref AS build
USER root
COPY --chown=node:node session-marketplaces.json /opt/session-marketplaces.json
COPY --chown=node:node --chmod=0755 install-claude-marketplaces.sh /usr/local/lib/ai-sandboxes/install-session-claude-marketplaces.sh
RUN install -d -o node -g node -m 0755 /opt/claude-session/plugin-cache /opt/claude-session/plugin-seed /opt/claude-session-build-home /opt/claude-marketplaces
USER node
ENV HOME=/opt/claude-session-build-home CLAUDE_CODE_PLUGIN_CACHE_DIR=/opt/claude-session/plugin-cache CLAUDE_CODE_PLUGIN_SEED_DIR=/opt/claude-session/plugin-seed
RUN /usr/local/lib/ai-sandboxes/install-session-claude-marketplaces.sh /opt/session-marketplaces.json
USER root
RUN if test -f /opt/claude-session-build-home/.claude/settings.json; then \\
      install -D -o node -g node -m 0644 /opt/claude-session-build-home/.claude/settings.json /opt/claude-session/plugin-seed/settings.json; \\
    fi \\
 && chmod -R a-w /opt/claude-session/plugin-cache /opt/claude-session/plugin-seed
USER node
FROM $base_image_ref
USER root
COPY --from=build --chown=root:root /opt/claude-session/plugin-cache /opt/claude-session/plugin-cache
COPY --from=build --chown=root:root /opt/claude-session/plugin-seed /opt/claude-session/plugin-seed
ENV CLAUDE_CODE_SESSION_PLUGIN_SEED_DIR=/opt/claude-session/plugin-seed CLAUDE_CODE_SESSION_PLUGIN_CACHE_DIR=/opt/claude-session/plugin-cache
COPY --chown=root:root --chmod=0444 resolved.json /opt/session-profile/resolved.json
USER node
EOF
