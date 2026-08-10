# syntax=docker/dockerfile:1.7
# `base` is the Bake named build context, not an unpinned registry image.
# hadolint ignore=DL3006
FROM base AS build
SHELL ["/bin/bash", "-o", "pipefail", "-c"]
USER root
ARG CLAUDE_CODE_VERSION
ARG MARKETPLACES_CONFIG=config/marketplaces.json
RUN install -d -m 0700 /tmp/claude-release-keyring \
 && export GNUPGHOME=/tmp/claude-release-keyring \
 && curl -fsSL https://downloads.claude.ai/keys/claude-code.asc -o /tmp/claude-code.asc \
 && gpg --batch --dearmor -o /tmp/claude-code.gpg /tmp/claude-code.asc \
 && test "$(gpg --show-keys --with-colons /tmp/claude-code.gpg | awk -F: '$1 == "fpr" { print $10 }' | sort -u)" = 31DDDE24DDFAB679F42D7BD2BAA929FF1A7ECACE \
 && curl -fsSL "https://downloads.claude.ai/claude-code-releases/${CLAUDE_CODE_VERSION}/manifest.json" -o /tmp/manifest.json \
 && curl -fsSL "https://downloads.claude.ai/claude-code-releases/${CLAUDE_CODE_VERSION}/manifest.json.sig" -o /tmp/manifest.json.sig \
 && gpg --batch --no-default-keyring --keyring /tmp/claude-code.gpg --verify /tmp/manifest.json.sig /tmp/manifest.json \
 && jq -e --arg version "$CLAUDE_CODE_VERSION" '.version == $version' /tmp/manifest.json >/dev/null \
 && expected_checksum="$(jq -er '.platforms["linux-arm64"].checksum | select(test("^[[:xdigit:]]{64}$"))' /tmp/manifest.json)" \
 && curl -fsSL "https://downloads.claude.ai/claude-code-releases/${CLAUDE_CODE_VERSION}/linux-arm64/claude" -o /tmp/claude \
 && echo "${expected_checksum}  /tmp/claude" | sha256sum --check --status \
 && install -m 0755 /tmp/claude /usr/local/bin/claude \
 && test "$(claude --version | awk '{print $1}')" = "$CLAUDE_CODE_VERSION" \
 && rm -rf /tmp/claude-release-keyring /tmp/claude-code.asc /tmp/claude-code.gpg /tmp/manifest.json /tmp/manifest.json.sig /tmp/claude \
 && install -d -o node -g node -m 0755 /opt/claude-plugin-cache /opt/claude-plugin-seed /opt/claude-build-home /opt/claude-marketplaces
COPY --chown=node:node ${MARKETPLACES_CONFIG} /opt/marketplaces.json
COPY --chown=node:node scripts/marketplaces/install-claude.sh /usr/local/lib/ai-sandboxes/install-claude-marketplaces.sh
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
# Claude Code stores plugin runtime state here. Keep the downloaded cache
# immutable while allowing the unprivileged runtime user to create that state.
RUN chmod 0755 /usr/local/bin/claude-entrypoint \
 && install -d -o node -g node -m 0755 /opt/claude-plugin-cache/data
USER node
WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/claude-entrypoint"]
CMD ["claude"]
