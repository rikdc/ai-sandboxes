#!/usr/bin/env bash
set -euo pipefail

context_dir=${1:?usage: render-dockerfile.sh CONTEXT_DIR BASE_IMAGE_REF}
base_image_ref=${2:?usage: render-dockerfile.sh CONTEXT_DIR BASE_IMAGE_REF}

test -f "$context_dir/resolved.json" || {
  echo 'render-dockerfile: missing resolved.json in context' >&2
  exit 1
}

# FROM references the caller's private, pre-verified pin of the base image's
# exact content (see resolve-image.sh), not the mutable ai-sandboxes-claude:local
# tag directly: a concurrent `./scripts/build` could otherwise retag the base
# between when its digest was captured and when BuildKit resolves this
# Dockerfile, silently building from different content than the cache key
# claims. This must be a plain local tag, not a name@digest reference: for a
# purely local (never pushed) image, BuildKit resolves name@digest as a
# registry manifest lookup and fails, rather than matching it against the
# local image store.
cat >"$context_dir/Dockerfile" <<EOF
# syntax=docker/dockerfile:1.7
FROM $base_image_ref
USER root
COPY --chown=root:root --chmod=0444 resolved.json /opt/session-profile/resolved.json
USER node
EOF
