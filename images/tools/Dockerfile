# syntax=docker/dockerfile:1.7
# This is a profile layer. The neutral `base` target never installs these tools.

# `base` is the Bake named build context, not an unpinned registry image.
# hadolint ignore=DL3006
FROM base
SHELL ["/bin/bash", "-o", "pipefail", "-c"]
USER root
ARG SHARED_STATE_ID
ARG SHARED_STATE_QUOTA
LABEL io.ai-sandboxes.shared-state.id="$SHARED_STATE_ID" \
      io.ai-sandboxes.shared-state.quota="$SHARED_STATE_QUOTA"
COPY config/tool-catalog.json config/tools.json config/runtime.json /opt/tools/
COPY --chmod=0755 scripts/tools/ /usr/local/lib/ai-sandboxes/tools/
RUN /usr/local/lib/ai-sandboxes/tools/validate-selection.sh \
    /opt/tools/tool-catalog.json \
    /opt/tools/tools.json \
    /opt/tools/runtime.json
RUN /usr/local/lib/ai-sandboxes/tools/validate-runtime-labels.sh \
    /opt/tools/runtime.json \
    "$SHARED_STATE_ID" \
    "$SHARED_STATE_QUOTA"
RUN install -d /usr/local/libexec \
 && /usr/local/lib/ai-sandboxes/tools/install-selected.sh \
    runtime /opt/tools/tool-catalog.json /opt/tools/tools.json
USER node
WORKDIR /workspace
