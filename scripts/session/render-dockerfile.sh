#!/usr/bin/env bash
set -euo pipefail

context_dir=${1:?usage: render-dockerfile.sh CONTEXT_DIR}

test -f "$context_dir/resolved.json" || {
  echo 'render-dockerfile: missing resolved.json in context' >&2
  exit 1
}

cat >"$context_dir/Dockerfile" <<'EOF'
# syntax=docker/dockerfile:1.7
FROM ai-sandboxes-claude:local
USER root
COPY --chown=root:root --chmod=0444 resolved.json /opt/session-profile/resolved.json
USER node
EOF
