# syntax=docker/dockerfile:1.7
# `base` is the Bake named build context, not an unpinned registry image.
# hadolint ignore=DL3006
FROM base AS build
SHELL ["/bin/bash", "-o", "pipefail", "-c"]
USER root
ARG CODEX_VERSION
RUN npm install --global --omit=dev "@openai/codex@${CODEX_VERSION}" \
 && install -d -o node -g node -m 0755 /opt/codex-skills /opt/codex-sources
COPY --chown=node:node config/marketplaces.json /opt/marketplaces.json
USER node
RUN set -euo pipefail; \
    jq -e '(.codex | type == "array") and all(.codex[]; (.url | type == "string") and (.ref | type == "string") and (.skills_path | type == "string"))' /opt/marketplaces.json >/dev/null; \
    index=0; \
    jq -c '.codex[]' /opt/marketplaces.json | while IFS= read -r spec; do \
      url=$(jq -er '.url' <<<"$spec"); ref=$(jq -er '.ref' <<<"$spec"); path=$(jq -er '.skills_path' <<<"$spec"); dir="/opt/codex-sources/$index"; index=$((index + 1)); \
      git clone "$url" "$dir"; git -C "$dir" checkout --detach "$ref"; skills="$dir/$path"; test -d "$skills"; \
      find -L "$skills" -mindepth 1 -maxdepth 1 -type d | while IFS= read -r skill; do target="/opt/codex-skills/$(basename "$skill")"; test ! -e "$target"; cp -RL "$skill" "$target"; done; \
    done
# `base` is the Bake named build context, not an unpinned registry image.
# hadolint ignore=DL3006
FROM base
USER root
COPY --from=build /usr/local /usr/local
COPY --from=build --chown=root:root /opt/codex-skills /opt/codex-skills
COPY images/codex/entrypoint.sh /usr/local/bin/codex-entrypoint
RUN chmod 0755 /usr/local/bin/codex-entrypoint && chmod -R a-w /opt/codex-skills
USER node
WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/codex-entrypoint"]
CMD ["codex"]
