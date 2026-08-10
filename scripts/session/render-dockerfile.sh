#!/usr/bin/env bash
set -euo pipefail

context_dir=${1:?usage: render-dockerfile.sh CONTEXT_DIR BASE_IMAGE_REF}
base_image_ref=${2:?usage: render-dockerfile.sh CONTEXT_DIR BASE_IMAGE_REF}

test -f "$context_dir/resolved.json" || {
  echo 'render-dockerfile: missing resolved.json in context' >&2
  exit 1
}

# Pin FROM to name@digest using the exact base image ID the caller already
# resolved and hashed into the cache key, rather than the mutable
# ai-sandboxes-claude:local tag: a concurrent `./scripts/build` could
# otherwise retag the base between when the digest was captured and when
# BuildKit resolves this Dockerfile, silently building from different content
# than the cache key claims. A bare digest (without the name@ prefix) is not
# enough: BuildKit parses it as a repository named "sha256" and tries to pull
# it from a registry instead of resolving it against the local image store.
cat >"$context_dir/Dockerfile" <<EOF
# syntax=docker/dockerfile:1.7
FROM $base_image_ref
USER root
COPY --chown=root:root --chmod=0444 resolved.json /opt/session-profile/resolved.json
USER node
EOF
