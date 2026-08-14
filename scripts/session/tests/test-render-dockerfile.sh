#!/usr/bin/env bash
set -o pipefail
cd "$(dirname "$0")/../../.." || exit 1

context_dir=$(mktemp -d) || exit 1
marketplace_context_dir=$(mktemp -d) || exit 1
apt_context_dir=$(mktemp -d) || exit 1
npm_context_dir=$(mktemp -d) || exit 1
tools_context_dir=$(mktemp -d) || exit 1
combined_context_dir=$(mktemp -d) || exit 1
trap 'rm -rf "$context_dir" "$marketplace_context_dir" "$apt_context_dir" "$npm_context_dir" "$tools_context_dir" "$combined_context_dir"' EXIT

if scripts/session/render-dockerfile.sh "$context_dir" 'ai-sandboxes-claude-session-base:deadbeef' '{"schema_version":1}' 2>/dev/null; then
  echo 'FAIL: should refuse a context dir with no resolved.json' >&2
  exit 1
fi

echo '{"ok":true}' >"$context_dir/resolved.json" || exit 1
scripts/session/render-dockerfile.sh "$context_dir" 'ai-sandboxes-claude-session-base:deadbeef' '{"schema_version":1}' || exit 1

test -f "$context_dir/Dockerfile" || exit 1
grep -qFx 'FROM ai-sandboxes-claude-session-base:deadbeef' "$context_dir/Dockerfile" || exit 1
grep -qFx 'USER node' "$context_dir/Dockerfile" || exit 1
test "$(find "$context_dir" -maxdepth 1 -type f | wc -l)" -eq 2 || exit 1

echo '{"ok":true}' >"$marketplace_context_dir/resolved.json" || exit 1
profile_with_marketplace='{"schema_version":1,"claude_marketplaces":[{"url":"https://github.com/rikdc/ai-skills.git","ref":"d66daa0504ff859a9d51c86b9175eb9fe768cd25","path":".","plugins":["dev-skills"]}]}'
scripts/session/render-dockerfile.sh "$marketplace_context_dir" 'ai-sandboxes-claude-session-base:deadbeef' "$profile_with_marketplace" || exit 1

test -f "$marketplace_context_dir/Dockerfile" || exit 1
test -f "$marketplace_context_dir/session-marketplaces.json" || exit 1
test -f "$marketplace_context_dir/install-claude-marketplaces.sh" || exit 1
test -f "$marketplace_context_dir/merge-plugin-seed.sh" || exit 1
grep -qFx 'FROM ai-sandboxes-claude-session-base:deadbeef AS build' "$marketplace_context_dir/Dockerfile" || exit 1
grep -qF 'CLAUDE_CODE_PLUGIN_CACHE_DIR=/opt/claude-plugin-cache' "$marketplace_context_dir/Dockerfile" || exit 1
grep -qF 'CLAUDE_CODE_PLUGIN_SEED_DIR=/opt/claude-plugin-seed' "$marketplace_context_dir/Dockerfile" || exit 1
grep -qF 'merge-session-plugin-seed.sh /opt/claude-session-build-home/.claude/settings.json /opt/claude-plugin-seed/settings.json' "$marketplace_context_dir/Dockerfile" || exit 1
# The final stage copies the augmented seed cache in read-only; it must NOT
# carve writable holes into it — the entrypoint materialises a per-session
# writable copy at runtime (see images/claude/entrypoint.sh).
if grep -qF 'install -d -o node -g node -m 0755 /opt/claude-plugin-cache' "$marketplace_context_dir/Dockerfile"; then
  echo 'FAIL: renderer must not make the seeded plugin cache writable' >&2
  exit 1
fi
grep -qFx 'COPY --from=build --chown=root:root /opt/claude-plugin-cache /opt/claude-plugin-cache' "$marketplace_context_dir/Dockerfile" || exit 1
grep -qFx 'COPY --from=build --chown=root:root /opt/claude-plugin-seed /opt/claude-plugin-seed' "$marketplace_context_dir/Dockerfile" || exit 1
grep -qFx 'USER node' "$marketplace_context_dir/Dockerfile" || exit 1
test "$(find "$marketplace_context_dir" -maxdepth 1 -type f | wc -l)" -eq 5 || exit 1
jq -e '.claude | length == 1' "$marketplace_context_dir/session-marketplaces.json" >/dev/null || exit 1
jq -e '.claude[0].url == "https://github.com/rikdc/ai-skills.git"' "$marketplace_context_dir/session-marketplaces.json" >/dev/null || exit 1
jq -e '.codex == []' "$marketplace_context_dir/session-marketplaces.json" >/dev/null || exit 1
diff -q "$marketplace_context_dir/install-claude-marketplaces.sh" scripts/marketplaces/install-claude.sh || exit 1
diff -q "$marketplace_context_dir/merge-plugin-seed.sh" scripts/session/merge-plugin-seed.sh || exit 1

echo '{"ok":true}' >"$apt_context_dir/resolved.json" || exit 1
profile_with_apt='{"schema_version":1,"apt":[{"name":"tree"}]}'
scripts/session/render-dockerfile.sh "$apt_context_dir" 'ai-sandboxes-claude-session-base:deadbeef' "$profile_with_apt" || exit 1

test -f "$apt_context_dir/Dockerfile" || exit 1
test -f "$apt_context_dir/session-apt-packages.json" || exit 1
test -f "$apt_context_dir/install-apt-packages.sh" || exit 1
test -f "$apt_context_dir/patch-apt-provenance.sh" || exit 1
grep -qFx 'FROM ai-sandboxes-claude-session-base:deadbeef' "$apt_context_dir/Dockerfile" || exit 1
grep -qFx 'RUN /usr/local/lib/ai-sandboxes/install-session-apt-packages.sh /opt/session-apt-packages.json /opt/session-apt-installed.json' "$apt_context_dir/Dockerfile" || exit 1
grep -qF 'COPY --chown=root:root resolved.json /opt/session-profile/resolved.json' "$apt_context_dir/Dockerfile" || exit 1
# shellcheck disable=SC1003 # trailing backslash is literal content being matched, not an escape
grep -qFx 'RUN /usr/local/lib/ai-sandboxes/patch-apt-provenance.sh /opt/session-profile/resolved.json /opt/session-apt-installed.json \' "$apt_context_dir/Dockerfile" || exit 1
grep -qFx ' && chmod 0444 /opt/session-profile/resolved.json' "$apt_context_dir/Dockerfile" || exit 1
# apt makes resolved.json writable-then-locked (patched by the installer, then
# locked as the very last step) instead of copied in already read-only, and
# the resolved.json COPY is positioned at the very end (after the apt RUN,
# not before it) so its per-build-unique content never busts the apt-get
# layer's own cache.
if grep -qF -- '--chmod=0444 resolved.json' "$apt_context_dir/Dockerfile"; then
  echo 'FAIL: apt-only render should not copy resolved.json already read-only' >&2
  exit 1
fi
resolved_copy_line=$(grep -n 'COPY --chown=root:root resolved.json /opt/session-profile/resolved.json' "$apt_context_dir/Dockerfile" | cut -d: -f1) || exit 1
apt_install_line=$(grep -n 'RUN /usr/local/lib/ai-sandboxes/install-session-apt-packages.sh' "$apt_context_dir/Dockerfile" | cut -d: -f1) || exit 1
test "$apt_install_line" -lt "$resolved_copy_line" || exit 1
diff -q "$apt_context_dir/install-apt-packages.sh" scripts/session/install-apt-packages.sh || exit 1
diff -q "$apt_context_dir/patch-apt-provenance.sh" scripts/session/patch-apt-provenance.sh || exit 1
jq -e '.apt | length == 1 and .[0].name == "tree"' "$apt_context_dir/session-apt-packages.json" >/dev/null || exit 1
test "$(find "$apt_context_dir" -maxdepth 1 -type f | wc -l)" -eq 5 || exit 1

echo '{"ok":true}' >"$npm_context_dir/resolved.json" || exit 1
profile_with_npm='{"schema_version":1,"npm":[{"package":"cowsay","version":"1.6.0"}]}'
scripts/session/render-dockerfile.sh "$npm_context_dir" 'ai-sandboxes-claude-session-base:deadbeef' "$profile_with_npm" || exit 1

test -f "$npm_context_dir/Dockerfile" || exit 1
test -f "$npm_context_dir/session-npm-packages.json" || exit 1
test -f "$npm_context_dir/install-npm-packages.sh" || exit 1
grep -qFx 'RUN install -d -o node -g node -m 0755 /opt/claude-session/npm' "$npm_context_dir/Dockerfile" || exit 1
grep -qFx 'USER node' "$npm_context_dir/Dockerfile" || exit 1
grep -qFx 'RUN /usr/local/lib/ai-sandboxes/install-session-npm-packages.sh /opt/session-npm-packages.json' "$npm_context_dir/Dockerfile" || exit 1
# npm installs as the unprivileged node user (a compromised postinstall
# script only ever has write access to its own not-yet-locked prefix, never
# the rest of the final image), then root re-locks the prefix read-only.
grep -qFx 'USER root' "$npm_context_dir/Dockerfile" || exit 1
# shellcheck disable=SC1003 # trailing backslash is literal content being matched, not an escape
grep -qFx 'RUN chown -R root:root /opt/claude-session/npm \' "$npm_context_dir/Dockerfile" || exit 1
# PATH is appended to, never prepended: a prepended npm bin dir could shadow
# base-image commands the harness itself depends on (claude, git, curl),
# letting a session-installed package silently replace what the agent
# actually executes. Appending guarantees base-image binaries always resolve
# first.
grep -qFx "ENV PATH=\$PATH:/opt/claude-session/npm/bin" "$npm_context_dir/Dockerfile" || exit 1
grep -qF -- '--chmod=0444 resolved.json' "$npm_context_dir/Dockerfile" || exit 1
diff -q "$npm_context_dir/install-npm-packages.sh" scripts/session/install-npm-packages.sh || exit 1
grep -qFx 'cache_dir=/tmp/claude-session-npm-cache' "$npm_context_dir/install-npm-packages.sh" || exit 1
grep -qF -- "npm install --global --prefix \"\$prefix\" --cache \"\$cache_dir\"" "$npm_context_dir/install-npm-packages.sh" || exit 1
grep -qF -- "rm -rf -- \"\$cache_dir\"" "$npm_context_dir/install-npm-packages.sh" || exit 1
if grep -qF -- 'npm config get cache' "$npm_context_dir/install-npm-packages.sh"; then
  echo 'FAIL: npm installer must not trust a cache path read after lifecycle hooks' >&2
  exit 1
fi
jq -e '.npm | length == 1 and .[0].package == "cowsay"' "$npm_context_dir/session-npm-packages.json" >/dev/null || exit 1
test "$(find "$npm_context_dir" -maxdepth 1 -type f | wc -l)" -eq 4 || exit 1

echo '{"ok":true}' >"$tools_context_dir/resolved.json" || exit 1
profile_with_tools='{"schema_version":1,"tools":[{"id":"rtk","version":"v0.45.0","sha256":"80a746dd305ef944ff50ef011ae4ce3878dd5ba88dfe35d859d05498191637c3"}]}'
scripts/session/render-dockerfile.sh "$tools_context_dir" 'ai-sandboxes-claude-session-base:deadbeef' "$profile_with_tools" || exit 1

test -f "$tools_context_dir/Dockerfile" || exit 1
test -f "$tools_context_dir/session-tool-catalog.json" || exit 1
test -f "$tools_context_dir/session-tools-selection.json" || exit 1
test -f "$tools_context_dir/install-selected.sh" || exit 1
test -f "$tools_context_dir/install-github-release-tar.sh" || exit 1
# The trailing backslash is literal Dockerfile content, not a shell escape.
grep -qFx "RUN install -d /usr/local/libexec \\" "$tools_context_dir/Dockerfile" || exit 1
grep -qFx ' && /usr/local/lib/ai-sandboxes/install-selected.sh runtime /opt/session-tool-catalog.json /opt/session-tools-selection.json' "$tools_context_dir/Dockerfile" || exit 1
grep -qF -- '--chmod=0444 resolved.json' "$tools_context_dir/Dockerfile" || exit 1
diff -q "$tools_context_dir/session-tool-catalog.json" config/tool-catalog.json || exit 1
diff -q "$tools_context_dir/install-selected.sh" scripts/tools/install-selected.sh || exit 1
diff -q "$tools_context_dir/install-github-release-tar.sh" scripts/tools/install-github-release-tar.sh || exit 1
jq -e '.tools | length == 1 and .[0].id == "rtk"' "$tools_context_dir/session-tools-selection.json" >/dev/null || exit 1
test "$(find "$tools_context_dir" -maxdepth 1 -type f | wc -l)" -eq 6 || exit 1

echo '{"ok":true}' >"$combined_context_dir/resolved.json" || exit 1
profile_combined=$(jq -c '. + {tools: [{"id":"rtk","version":"v0.45.0","sha256":"80a746dd305ef944ff50ef011ae4ce3878dd5ba88dfe35d859d05498191637c3"}]}' scripts/session/fixtures/valid/apt-npm-marketplaces.json) || exit 1
scripts/session/render-dockerfile.sh "$combined_context_dir" 'ai-sandboxes-claude-session-base:deadbeef' "$profile_combined" || exit 1

test -f "$combined_context_dir/Dockerfile" || exit 1
# Canonical layer order regardless of profile field order: apt, then npm,
# then curated tools, then the marketplace build stage's output, then
# resolved.json (patched and locked) last of all — so its per-build-unique
# content never busts the cache for any package-installing layer.
apt_line=$(grep -n 'RUN /usr/local/lib/ai-sandboxes/install-session-apt-packages.sh' "$combined_context_dir/Dockerfile" | cut -d: -f1) || exit 1
npm_line=$(grep -n 'RUN /usr/local/lib/ai-sandboxes/install-session-npm-packages.sh' "$combined_context_dir/Dockerfile" | cut -d: -f1) || exit 1
tools_line=$(grep -n 'install-selected.sh runtime /opt/session-tool-catalog.json' "$combined_context_dir/Dockerfile" | cut -d: -f1) || exit 1
marketplace_copy_line=$(grep -n 'COPY --from=build --chown=root:root /opt/claude-plugin-cache /opt/claude-plugin-cache' "$combined_context_dir/Dockerfile" | cut -d: -f1) || exit 1
resolved_copy_line=$(grep -n 'COPY --chown=root:root resolved.json /opt/session-profile/resolved.json' "$combined_context_dir/Dockerfile" | cut -d: -f1) || exit 1
patch_line=$(grep -n 'RUN /usr/local/lib/ai-sandboxes/patch-apt-provenance.sh' "$combined_context_dir/Dockerfile" | cut -d: -f1) || exit 1
test "$apt_line" -lt "$npm_line" || exit 1
test "$npm_line" -lt "$tools_line" || exit 1
test "$tools_line" -lt "$marketplace_copy_line" || exit 1
test "$marketplace_copy_line" -lt "$resolved_copy_line" || exit 1
test "$resolved_copy_line" -lt "$patch_line" || exit 1
if grep -qF -- '--chmod=0444 resolved.json' "$combined_context_dir/Dockerfile"; then
  echo 'FAIL: combined render should not copy resolved.json already read-only' >&2
  exit 1
fi
tail -3 "$combined_context_dir/Dockerfile" | grep -qF 'chmod 0444 /opt/session-profile/resolved.json' || exit 1
tail -1 "$combined_context_dir/Dockerfile" | grep -qFx 'USER node' || exit 1
test "$(find "$combined_context_dir" -maxdepth 1 -type f | wc -l)" -eq 14 || exit 1

echo ok
