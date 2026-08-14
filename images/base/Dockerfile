# syntax=docker/dockerfile:1.7
ARG NODE_IMAGE
ARG TEA_IMAGE

# hadolint ignore=DL3006
FROM ${TEA_IMAGE} AS tea

# hadolint ignore=DL3006
FROM ${NODE_IMAGE}
ARG TARGETARCH
ARG GH_APT_KEY_FINGERPRINT
RUN test "$TARGETARCH" = arm64
# Versions resolve from signed repositories at build time.
# hadolint ignore=DL3008
RUN apt-get update \
 && apt-get install -y --no-install-recommends bash ca-certificates curl git gnupg jq openssh-client ripgrep unzip \
 && install -d -m 0755 /etc/apt/keyrings \
 && curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg -o /etc/apt/keyrings/githubcli-archive-keyring.gpg \
 && gpg --show-keys --with-colons /etc/apt/keyrings/githubcli-archive-keyring.gpg > /tmp/github-cli-key.txt \
 && test "$(awk -F: '$1 == "fpr" { print $10; exit }' /tmp/github-cli-key.txt)" = "$GH_APT_KEY_FINGERPRINT" \
 && rm /tmp/github-cli-key.txt \
 && echo "deb [arch=arm64 signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" > /etc/apt/sources.list.d/github-cli.list \
 && apt-get update \
 && apt-get install -y --no-install-recommends gh \
 && rm -rf /var/lib/apt/lists/*
COPY --from=tea /usr/bin/tea /usr/local/bin/tea
RUN install -d -o node -g node -m 0755 /workspace /home/node
ENV HOME=/home/node SHELL=/bin/bash PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
WORKDIR /workspace
USER node
