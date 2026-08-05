# syntax=docker/dockerfile:1.7
ARG NODE_IMAGE=node:22-bookworm
ARG TEA_VERSION=0.14.2

FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS tea-builder
ARG TARGETOS
ARG TARGETARCH
ARG TEA_VERSION
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go install gitea.dev/tea@v${TEA_VERSION}

FROM ${NODE_IMAGE}
ARG TARGETARCH
RUN test "$TARGETARCH" = arm64
RUN apt-get update \
 && apt-get install -y --no-install-recommends bash ca-certificates curl git gnupg jq openssh-client ripgrep \
 && install -d -m 0755 /etc/apt/keyrings \
 && curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg -o /etc/apt/keyrings/githubcli-archive-keyring.gpg \
 && echo "deb [arch=arm64 signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" > /etc/apt/sources.list.d/github-cli.list \
 && apt-get update \
 && apt-get install -y --no-install-recommends gh \
 && rm -rf /var/lib/apt/lists/*
COPY --from=tea-builder /go/bin/tea /usr/local/bin/tea
RUN install -d -o node -g node -m 0755 /workspace /home/node
ENV HOME=/home/node SHELL=/bin/bash PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
WORKDIR /workspace
USER node
