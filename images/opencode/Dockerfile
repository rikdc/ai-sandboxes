# syntax=docker/dockerfile:1.7
# `base` is the Bake named build context, not an unpinned registry image.
# hadolint ignore=DL3006
FROM base AS build
SHELL ["/bin/bash", "-o", "pipefail", "-c"]
USER root
ARG OPENCODE_VERSION
RUN npm install --global --omit=dev "opencode-ai@${OPENCODE_VERSION}" \
 && install -d -o node -g node -m 0755 /opt/opencode-skills /opt/opencode-plugins /opt/opencode-sources \
 && install -d -m 0755 /usr/local/lib/ai-sandboxes
# marketplaces.json comes from the "userconfig" named local context (the
# resolved user configuration directory, see scripts/lib/config-dir.sh).
# userconfig is a BuildKit named local context passed by docker-bake.hcl,
# not a FROM alias in this file; hadolint cannot know that.
# hadolint ignore=DL3022
COPY --from=userconfig --chown=node:node marketplaces.json /opt/marketplaces.json
COPY --chown=node:node --chmod=0755 scripts/marketplaces/install-opencode.sh /usr/local/lib/ai-sandboxes/install-opencode-marketplaces.sh
USER node
RUN /usr/local/lib/ai-sandboxes/install-opencode-marketplaces.sh /opt/marketplaces.json
# `base` is the Bake named build context, not an unpinned registry image.
# hadolint ignore=DL3006
FROM base
USER root
COPY --from=build /usr/local /usr/local
COPY --from=build --chown=root:root /opt/opencode-skills /opt/opencode-skills
COPY --from=build --chown=root:root /opt/opencode-plugins /opt/opencode-plugins
COPY images/opencode/entrypoint.sh /usr/local/bin/opencode-entrypoint
RUN chmod 0755 /usr/local/bin/opencode-entrypoint \
 && chmod -R a-w /opt/opencode-skills \
 && chmod -R a-w /opt/opencode-plugins
USER node
WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/opencode-entrypoint"]
CMD ["opencode"]
