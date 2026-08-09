# syntax=docker/dockerfile:1.7
# `base` is the Bake named build context, not an unpinned registry image.
# hadolint ignore=DL3006
FROM base AS build
SHELL ["/bin/bash", "-o", "pipefail", "-c"]
USER root
ARG CLAUDE_CODE_VERSION
ARG MARKETPLACES_CONFIG=config/marketplaces.json
RUN npm install --global --omit=dev "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" \
 && install -d -o node -g node -m 0755 /opt/claude-plugin-cache /opt/claude-plugin-seed /opt/claude-build-home /opt/claude-marketplaces
COPY --chown=node:node ${MARKETPLACES_CONFIG} /opt/marketplaces.json
COPY --chown=node:node scripts/marketplaces/install-claude.sh /usr/local/lib/ai-sandboxes/install-claude-marketplaces.sh
USER node
ENV HOME=/opt/claude-build-home CLAUDE_CODE_PLUGIN_CACHE_DIR=/opt/claude-plugin-cache CLAUDE_CODE_PLUGIN_SEED_DIR=/opt/claude-plugin-seed
RUN /usr/local/lib/ai-sandboxes/install-claude-marketplaces.sh /opt/marketplaces.json
USER root
RUN chmod -R a-w /opt/claude-plugin-cache /opt/claude-plugin-seed
USER node
# `base` is the Bake named build context, not an unpinned registry image.
# hadolint ignore=DL3006
FROM base
USER root
COPY --from=build /usr/local /usr/local
COPY --from=build --chown=root:root /opt/claude-plugin-cache /opt/claude-plugin-cache
COPY --from=build --chown=root:root /opt/claude-plugin-seed /opt/claude-plugin-seed
ENV CLAUDE_CODE_PLUGIN_CACHE_DIR=/opt/claude-plugin-cache CLAUDE_CODE_PLUGIN_SEED_DIR=/opt/claude-plugin-seed DISABLE_AUTOUPDATER=1
USER node
WORKDIR /workspace
CMD ["claude"]
