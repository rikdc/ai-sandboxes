# syntax=docker/dockerfile:1.7
FROM base AS build
SHELL ["/bin/bash", "-o", "pipefail", "-c"]
USER root
ARG CLAUDE_CODE_VERSION
RUN npm install --global --omit=dev "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" \
 && install -d -o node -g node -m 0755 /opt/claude-plugin-cache /opt/claude-plugin-seed /opt/claude-build-home /opt/claude-marketplaces
COPY --chown=node:node config/marketplaces.json /opt/marketplaces.json
USER node
ENV HOME=/opt/claude-build-home CLAUDE_CODE_PLUGIN_CACHE_DIR=/opt/claude-plugin-cache CLAUDE_CODE_PLUGIN_SEED_DIR=/opt/claude-plugin-seed
RUN set -euo pipefail; \
    jq -e '(.claude | type == "array") and all(.claude[]; (.url | type == "string") and (.ref | type == "string") and (.path | type == "string"))' /opt/marketplaces.json >/dev/null; \
    index=0; \
    jq -c '.claude[]' /opt/marketplaces.json | while IFS= read -r spec; do \
      url=$(jq -er '.url' <<<"$spec"); ref=$(jq -er '.ref' <<<"$spec"); path=$(jq -er '.path' <<<"$spec"); dir="/opt/claude-marketplaces/$index"; index=$((index + 1)); \
      git clone "$url" "$dir"; git -C "$dir" checkout --detach "$ref"; manifest="$dir/$path/.claude-plugin/marketplace.json"; test -f "$manifest"; \
      marketplace=$(jq -er '.name' "$manifest"); claude plugin marketplace add "$dir/$path"; \
      jq -er '.plugins[] | .name' "$manifest" | while IFS= read -r plugin; do claude plugin install "${plugin}@${marketplace}" --scope user; done; \
    done
FROM base
USER root
COPY --from=build /usr/local /usr/local
COPY --from=build --chown=root:root /opt/claude-plugin-cache /opt/claude-plugin-cache
COPY --from=build --chown=root:root /opt/claude-plugin-seed /opt/claude-plugin-seed
RUN chmod -R a-w /opt/claude-plugin-cache /opt/claude-plugin-seed
ENV CLAUDE_CODE_PLUGIN_CACHE_DIR=/opt/claude-plugin-cache CLAUDE_CODE_PLUGIN_SEED_DIR=/opt/claude-plugin-seed DISABLE_AUTOUPDATER=1
USER node
WORKDIR /workspace
CMD ["claude"]
