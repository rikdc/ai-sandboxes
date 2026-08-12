#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../../.." || exit 1

context_dir=$(mktemp -d)
marketplace_context_dir=$(mktemp -d)
apt_context_dir=$(mktemp -d)
npm_context_dir=$(mktemp -d)
combined_context_dir=$(mktemp -d)
trap 'rm -rf "$context_dir" "$marketplace_context_dir" "$apt_context_dir" "$npm_context_dir" "$combined_context_dir"' EXIT

if scripts/session/render-dockerfile.sh "$context_dir" 'ai-sandboxes-claude-session-base:deadbeef' '{"schema_version":1}' 2>/dev/null; then
  echo 'FAIL: should refuse a context dir with no resolved.json' >&2
  exit 1
fi

echo '{"ok":true}' >"$context_dir/resolved.json"
scripts/session/render-dockerfile.sh "$context_dir" 'ai-sandboxes-claude-session-base:deadbeef' '{"schema_version":1}'

test -f "$context_dir/Dockerfile"
grep -qFx 'FROM ai-sandboxes-claude-session-base:deadbeef' "$context_dir/Dockerfile"
grep -qFx 'USER node' "$context_dir/Dockerfile"
test "$(find "$context_dir" -maxdepth 1 -type f | wc -l)" -eq 2

echo '{"ok":true}' >"$marketplace_context_dir/resolved.json"
profile_with_marketplace='{"schema_version":1,"claude_marketplaces":[{"url":"https://github.com/rikdc/ai-skills.git","ref":"d66daa0504ff859a9d51c86b9175eb9fe768cd25","path":".","plugins":["dev-skills"]}]}'
scripts/session/render-dockerfile.sh "$marketplace_context_dir" 'ai-sandboxes-claude-session-base:deadbeef' "$profile_with_marketplace"

test -f "$marketplace_context_dir/Dockerfile"
test -f "$marketplace_context_dir/session-marketplaces.json"
test -f "$marketplace_context_dir/install-claude-marketplaces.sh"
test -f "$marketplace_context_dir/merge-plugin-seed.sh"
grep -qFx 'FROM ai-sandboxes-claude-session-base:deadbeef AS build' "$marketplace_context_dir/Dockerfile"
grep -qF 'CLAUDE_CODE_PLUGIN_CACHE_DIR=/opt/claude-plugin-cache' "$marketplace_context_dir/Dockerfile"
grep -qF 'CLAUDE_CODE_PLUGIN_SEED_DIR=/opt/claude-plugin-seed' "$marketplace_context_dir/Dockerfile"
grep -qF 'merge-session-plugin-seed.sh /opt/claude-session-build-home/.claude/settings.json /opt/claude-plugin-seed/settings.json' "$marketplace_context_dir/Dockerfile"
grep -qFx 'RUN install -d -o node -g node -m 0755 /opt/claude-plugin-cache/data' "$marketplace_context_dir/Dockerfile"
grep -qFx 'USER node' "$marketplace_context_dir/Dockerfile"
test "$(find "$marketplace_context_dir" -maxdepth 1 -type f | wc -l)" -eq 5
jq -e '.claude | length == 1' "$marketplace_context_dir/session-marketplaces.json" >/dev/null
jq -e '.claude[0].url == "https://github.com/rikdc/ai-skills.git"' "$marketplace_context_dir/session-marketplaces.json" >/dev/null
jq -e '.codex == []' "$marketplace_context_dir/session-marketplaces.json" >/dev/null
diff -q "$marketplace_context_dir/install-claude-marketplaces.sh" scripts/marketplaces/install-claude.sh
diff -q "$marketplace_context_dir/merge-plugin-seed.sh" scripts/session/merge-plugin-seed.sh

echo '{"ok":true}' >"$apt_context_dir/resolved.json"
profile_with_apt='{"schema_version":1,"apt":[{"name":"tree"}]}'
scripts/session/render-dockerfile.sh "$apt_context_dir" 'ai-sandboxes-claude-session-base:deadbeef' "$profile_with_apt"

test -f "$apt_context_dir/Dockerfile"
test -f "$apt_context_dir/session-apt-packages.json"
test -f "$apt_context_dir/install-apt-packages.sh"
test -f "$apt_context_dir/patch-apt-provenance.sh"
grep -qFx 'FROM ai-sandboxes-claude-session-base:deadbeef' "$apt_context_dir/Dockerfile"
grep -qFx 'RUN /usr/local/lib/ai-sandboxes/install-session-apt-packages.sh /opt/session-apt-packages.json /opt/session-apt-installed.json' "$apt_context_dir/Dockerfile"
grep -qF 'COPY --chown=root:root resolved.json /opt/session-profile/resolved.json' "$apt_context_dir/Dockerfile"
grep -qFx 'RUN /usr/local/lib/ai-sandboxes/patch-apt-provenance.sh /opt/session-profile/resolved.json /opt/session-apt-installed.json \' "$apt_context_dir/Dockerfile"
grep -qFx ' && chmod 0444 /opt/session-profile/resolved.json' "$apt_context_dir/Dockerfile"
# apt makes resolved.json writable-then-locked (patched by the installer, then
# locked as the very last step) instead of copied in already read-only, and
# the resolved.json COPY is positioned at the very end (after the apt RUN,
# not before it) so its per-build-unique content never busts the apt-get
# layer's own cache.
if grep -qF -- '--chmod=0444 resolved.json' "$apt_context_dir/Dockerfile"; then
  echo 'FAIL: apt-only render should not copy resolved.json already read-only' >&2
  exit 1
fi
resolved_copy_line=$(grep -n 'COPY --chown=root:root resolved.json /opt/session-profile/resolved.json' "$apt_context_dir/Dockerfile" | cut -d: -f1)
apt_install_line=$(grep -n 'RUN /usr/local/lib/ai-sandboxes/install-session-apt-packages.sh' "$apt_context_dir/Dockerfile" | cut -d: -f1)
test "$apt_install_line" -lt "$resolved_copy_line"
diff -q "$apt_context_dir/install-apt-packages.sh" scripts/session/install-apt-packages.sh
diff -q "$apt_context_dir/patch-apt-provenance.sh" scripts/session/patch-apt-provenance.sh
jq -e '.apt | length == 1 and .[0].name == "tree"' "$apt_context_dir/session-apt-packages.json" >/dev/null
test "$(find "$apt_context_dir" -maxdepth 1 -type f | wc -l)" -eq 5

echo '{"ok":true}' >"$npm_context_dir/resolved.json"
profile_with_npm='{"schema_version":1,"npm":[{"package":"cowsay","version":"1.6.0"}]}'
scripts/session/render-dockerfile.sh "$npm_context_dir" 'ai-sandboxes-claude-session-base:deadbeef' "$profile_with_npm"

test -f "$npm_context_dir/Dockerfile"
test -f "$npm_context_dir/session-npm-packages.json"
test -f "$npm_context_dir/install-npm-packages.sh"
grep -qFx 'RUN install -d -o node -g node -m 0755 /opt/claude-session/npm' "$npm_context_dir/Dockerfile"
grep -qFx 'USER node' "$npm_context_dir/Dockerfile"
grep -qFx 'RUN /usr/local/lib/ai-sandboxes/install-session-npm-packages.sh /opt/session-npm-packages.json' "$npm_context_dir/Dockerfile"
# npm installs as the unprivileged node user (a compromised postinstall
# script only ever has write access to its own not-yet-locked prefix, never
# the rest of the final image), then root re-locks the prefix read-only.
grep -qFx 'USER root' "$npm_context_dir/Dockerfile"
grep -qFx 'RUN chown -R root:root /opt/claude-session/npm \' "$npm_context_dir/Dockerfile"
# PATH is appended to, never prepended: a prepended npm bin dir could shadow
# base-image commands the harness itself depends on (claude, git, curl),
# letting a session-installed package silently replace what the agent
# actually executes. Appending guarantees base-image binaries always resolve
# first.
grep -qFx "ENV PATH=\$PATH:/opt/claude-session/npm/bin" "$npm_context_dir/Dockerfile"
grep -qF -- '--chmod=0444 resolved.json' "$npm_context_dir/Dockerfile"
diff -q "$npm_context_dir/install-npm-packages.sh" scripts/session/install-npm-packages.sh
jq -e '.npm | length == 1 and .[0].package == "cowsay"' "$npm_context_dir/session-npm-packages.json" >/dev/null
test "$(find "$npm_context_dir" -maxdepth 1 -type f | wc -l)" -eq 4

echo '{"ok":true}' >"$combined_context_dir/resolved.json"
profile_combined=$(cat scripts/session/fixtures/valid/apt-npm-marketplaces.json)
scripts/session/render-dockerfile.sh "$combined_context_dir" 'ai-sandboxes-claude-session-base:deadbeef' "$profile_combined"

test -f "$combined_context_dir/Dockerfile"
# Canonical layer order regardless of profile field order: apt, then npm,
# then the marketplace build stage's output, then resolved.json (patched and
# locked) last of all — so its per-build-unique content never busts the
# cache for any package-installing layer.
apt_line=$(grep -n 'RUN /usr/local/lib/ai-sandboxes/install-session-apt-packages.sh' "$combined_context_dir/Dockerfile" | cut -d: -f1)
npm_line=$(grep -n 'RUN /usr/local/lib/ai-sandboxes/install-session-npm-packages.sh' "$combined_context_dir/Dockerfile" | cut -d: -f1)
marketplace_copy_line=$(grep -n 'COPY --from=build --chown=root:root /opt/claude-plugin-cache /opt/claude-plugin-cache' "$combined_context_dir/Dockerfile" | cut -d: -f1)
resolved_copy_line=$(grep -n 'COPY --chown=root:root resolved.json /opt/session-profile/resolved.json' "$combined_context_dir/Dockerfile" | cut -d: -f1)
patch_line=$(grep -n 'RUN /usr/local/lib/ai-sandboxes/patch-apt-provenance.sh' "$combined_context_dir/Dockerfile" | cut -d: -f1)
test "$apt_line" -lt "$npm_line"
test "$npm_line" -lt "$marketplace_copy_line"
test "$marketplace_copy_line" -lt "$resolved_copy_line"
test "$resolved_copy_line" -lt "$patch_line"
if grep -qF -- '--chmod=0444 resolved.json' "$combined_context_dir/Dockerfile"; then
  echo 'FAIL: combined render should not copy resolved.json already read-only' >&2
  exit 1
fi
tail -3 "$combined_context_dir/Dockerfile" | grep -qF 'chmod 0444 /opt/session-profile/resolved.json'
tail -1 "$combined_context_dir/Dockerfile" | grep -qFx 'USER node'
test "$(find "$combined_context_dir" -maxdepth 1 -type f | wc -l)" -eq 10

echo ok
