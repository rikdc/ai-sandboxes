# syntax=docker/dockerfile:1.7
# `base` is the Bake named build context, not an unpinned registry image.
# hadolint ignore=DL3006
FROM base AS build
SHELL ["/bin/bash", "-o", "pipefail", "-c"]
USER root
ARG CLAUDE_CODE_VERSION
ARG CLAUDE_RELEASE_KEY_FINGERPRINT
COPY --chmod=0755 scripts/claude/install-binary.sh /tmp/install-claude-binary.sh
RUN /tmp/install-claude-binary.sh "$CLAUDE_CODE_VERSION" "$CLAUDE_RELEASE_KEY_FINGERPRINT" /usr/local/bin \
 && rm /tmp/install-claude-binary.sh \
 && install -d -m 0755 /usr/local/lib/ai-sandboxes \
 && install -d -o node -g node -m 0755 /opt/claude-plugin-cache /opt/claude-plugin-seed /opt/claude-build-home /opt/claude-marketplaces
# marketplaces.json comes from the "userconfig" named local context (the
# resolved user configuration directory, see scripts/lib/config-dir.sh).
COPY --from=userconfig --chown=node:node marketplaces.json /opt/marketplaces.json
COPY --chown=node:node --chmod=0755 scripts/marketplaces/install-claude.sh /usr/local/lib/ai-sandboxes/install-claude-marketplaces.sh
USER node
ENV HOME=/opt/claude-build-home CLAUDE_CODE_PLUGIN_CACHE_DIR=/opt/claude-plugin-cache CLAUDE_CODE_PLUGIN_SEED_DIR=/opt/claude-plugin-seed
RUN /usr/local/lib/ai-sandboxes/install-claude-marketplaces.sh /opt/marketplaces.json
USER root
RUN if test -f /opt/claude-build-home/.claude/settings.json; then \
      install -D -o node -g node -m 0644 /opt/claude-build-home/.claude/settings.json /opt/claude-plugin-seed/settings.json; \
    fi \
 && chmod -R a-w /opt/claude-plugin-cache /opt/claude-plugin-seed
USER node
# `base` is the Bake named build context, not an unpinned registry image.
# hadolint ignore=DL3006
FROM base
USER root
COPY --from=build /usr/local /usr/local
COPY --from=build --chown=root:root /opt/claude-plugin-cache /opt/claude-plugin-cache
COPY --from=build --chown=root:root /opt/claude-plugin-seed /opt/claude-plugin-seed
COPY images/claude/entrypoint.sh /usr/local/bin/claude-entrypoint
ENV CLAUDE_CODE_PLUGIN_CACHE_DIR=/opt/claude-plugin-cache CLAUDE_CODE_PLUGIN_SEED_DIR=/opt/claude-plugin-seed DISABLE_UPDATES=1
# `/opt/claude-plugin-cache` is an immutable seed: root-owned and non-writable
# for the container's lifetime. Claude Code rewrites `known_marketplaces.json`
# and `installed_plugins.json` atomically (via `.tmp.XXX` + rename) and grows
# `marketplaces/`, `data/`, and `cache/` at runtime, which needs write access
# to the whole cache tree. Carving writable holes into the seed (root, then
# `data/`, then `marketplaces/`) chased each failure one layer deeper and
# ultimately left the cache root writable — enough for the unprivileged
# runtime user to rename/remove seeded top-level files and shadow the tree.
# Instead the entrypoint materialises a fresh, fully writable per-session copy
# of this seed under `/tmp` and repoints CLAUDE_CODE_PLUGIN_CACHE_DIR at it;
# the seed itself is never written to at runtime. `settings.json` follows the
# same split (`/opt/claude-plugin-seed` + `$HOME/.claude/settings.json`).
RUN chmod 0755 /usr/local/bin/claude-entrypoint
USER node
WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/claude-entrypoint"]
CMD ["claude"]
