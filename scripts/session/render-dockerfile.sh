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

apt_packages=$(jq -c '.apt // []' <<<"$canonical_profile")
apt_count=$(jq 'length' <<<"$apt_packages")
npm_packages=$(jq -c '.npm // []' <<<"$canonical_profile")
npm_count=$(jq 'length' <<<"$npm_packages")
marketplaces=$(jq -c '.claude_marketplaces // []' <<<"$canonical_profile")
marketplace_count=$(jq 'length' <<<"$marketplaces")

if test "$apt_count" -eq 0 && test "$npm_count" -eq 0 && test "$marketplace_count" -eq 0; then
  cat >"$context_dir/Dockerfile" <<EOF
# syntax=docker/dockerfile:1.7
FROM $base_image_ref
USER root
COPY --chown=root:root --chmod=0444 resolved.json /opt/session-profile/resolved.json
USER node
EOF
  exit 0
fi

dockerfile="$context_dir/Dockerfile"
: >"$dockerfile"
printf '# syntax=docker/dockerfile:1.7\n' >>"$dockerfile"

if test "$marketplace_count" -gt 0; then
  jq -n --argjson claude "$marketplaces" '{claude: $claude, codex: []}' \
    >"$context_dir/session-marketplaces.json"
  cp -- "$repo_root/scripts/marketplaces/install-claude.sh" "$context_dir/install-claude-marketplaces.sh"
  cp -- "$repo_root/scripts/session/merge-plugin-seed.sh" "$context_dir/merge-plugin-seed.sh"

  cat >>"$dockerfile" <<EOF
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
EOF
else
  cat >>"$dockerfile" <<EOF
FROM $base_image_ref
USER root
EOF
fi

if test "$apt_count" -gt 0; then
  jq -n --argjson apt "$apt_packages" '{apt: $apt}' >"$context_dir/session-apt-packages.json"
  cp -- "$repo_root/scripts/session/install-apt-packages.sh" "$context_dir/install-apt-packages.sh"
  cat >>"$dockerfile" <<EOF
COPY --chown=root:root session-apt-packages.json /opt/session-apt-packages.json
COPY --chown=root:root --chmod=0755 install-apt-packages.sh /usr/local/lib/ai-sandboxes/install-session-apt-packages.sh
RUN /usr/local/lib/ai-sandboxes/install-session-apt-packages.sh /opt/session-apt-packages.json /opt/session-apt-installed.json
EOF
fi

if test "$npm_count" -gt 0; then
  jq -n --argjson npm "$npm_packages" '{npm: $npm}' >"$context_dir/session-npm-packages.json"
  cp -- "$repo_root/scripts/session/install-npm-packages.sh" "$context_dir/install-npm-packages.sh"
  cat >>"$dockerfile" <<EOF
COPY --chown=root:root session-npm-packages.json /opt/session-npm-packages.json
COPY --chown=root:root --chmod=0755 install-npm-packages.sh /usr/local/lib/ai-sandboxes/install-session-npm-packages.sh
RUN install -d -o node -g node -m 0755 /opt/claude-session/npm
USER node
RUN /usr/local/lib/ai-sandboxes/install-session-npm-packages.sh /opt/session-npm-packages.json
USER root
RUN chown -R root:root /opt/claude-session/npm \\
 && chmod -R a-w /opt/claude-session/npm
ENV PATH=/opt/claude-session/npm/bin:\$PATH
EOF
fi

if test "$marketplace_count" -gt 0; then
  cat >>"$dockerfile" <<EOF
COPY --from=build --chown=root:root /opt/claude-plugin-cache /opt/claude-plugin-cache
COPY --from=build --chown=root:root /opt/claude-plugin-seed /opt/claude-plugin-seed
RUN install -d -o node -g node -m 0755 /opt/claude-plugin-cache/data
EOF
fi

if test "$apt_count" -eq 0; then
  cat >>"$dockerfile" <<EOF
COPY --chown=root:root --chmod=0444 resolved.json /opt/session-profile/resolved.json
EOF
else
  cp -- "$repo_root/scripts/session/patch-apt-provenance.sh" "$context_dir/patch-apt-provenance.sh"
  cat >>"$dockerfile" <<EOF
COPY --chown=root:root resolved.json /opt/session-profile/resolved.json
COPY --chown=root:root --chmod=0755 patch-apt-provenance.sh /usr/local/lib/ai-sandboxes/patch-apt-provenance.sh
RUN /usr/local/lib/ai-sandboxes/patch-apt-provenance.sh /opt/session-profile/resolved.json /opt/session-apt-installed.json \\
 && chmod 0444 /opt/session-profile/resolved.json
EOF
fi

printf 'USER node\n' >>"$dockerfile"
