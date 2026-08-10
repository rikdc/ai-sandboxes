#!/usr/bin/env bash
set -euo pipefail

context_dir=${1:?usage: render-dockerfile.sh CONTEXT_DIR BASE_IMAGE_ID}
base_image_id=${2:?usage: render-dockerfile.sh CONTEXT_DIR BASE_IMAGE_ID}

test -f "$context_dir/resolved.json" || {
  echo 'render-dockerfile: missing resolved.json in context' >&2
  exit 1
}

# Pin FROM to the exact base image ID the caller already resolved and hashed
# into the cache key, rather than the mutable ai-sandboxes-claude:local tag: a
# concurrent `./scripts/build` could otherwise retag the base between when the
# digest was captured and when BuildKit resolves this Dockerfile, silently
# building from different content than the cache key claims.
cat >"$context_dir/Dockerfile" <<EOF
# syntax=docker/dockerfile:1.7
FROM $base_image_id
USER root
COPY --chown=root:root --chmod=0444 resolved.json /opt/session-profile/resolved.json
USER node
EOF
