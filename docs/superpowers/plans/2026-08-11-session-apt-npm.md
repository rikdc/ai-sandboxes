# Session apt and npm derived layers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a session profile's `apt` and `npm` fields actually install packages into the derived session image, composable with the existing `claude_marketplaces` layer.

**Architecture:** `scripts/session/render-dockerfile.sh` grows from a binary (empty / marketplace) branch into three independently-present, fixed-order layers (apt, then npm, then marketplace) appended into whichever Dockerfile stage is final. Two new trusted installer scripts (`install-apt-packages.sh`, `install-npm-packages.sh`) are copied into the build context exactly like the existing marketplace installer. The apt installer patches `resolved.json`'s recorded apt versions in place with real `dpkg-query` output after install.

**Tech Stack:** Bash, jq, Docker/BuildKit, apt-get, npm.

## Global Constraints

- Never attribute any commit to Claude/Anthropic — no `Co-Authored-By: Claude ...` trailer, ever. Grep `git log` for `Co-Authored-By` before considering any task's commits final.
- `schema_version` is always `1`. Profile field names are exactly `apt`, `npm`, `python`, `claude_marketplaces` — do not add or rename fields.
- `python` stays rejected by `validate-profile.sh` (task 9, separate PR, out of scope here).
- apt entries: `{"name": "...", "version": "..."?}` — `name` required, `version` optional. npm entries: `{"package": "...", "version": "..."}` — both required (npm versions are mandatory, unlike apt).
- Existing structural validation regexes (`apt_name`, `apt_version`, `pkg_name`, `pkg_version` in `scripts/session/validate-profile.sh`) are unchanged — only the reject-if-non-empty check and the dedup check change.
- Canonical layer order when multiple are present, always: **apt, then npm, then the marketplace build-stage output** — regardless of the order fields appear in the profile JSON, so the same profile always renders the same Dockerfile byte-for-byte.
- The apt layer runs directly in the final stage (never a discarded build stage — its footprint isn't confined to one copyable directory like npm's prefix or the marketplace cache paths are).
- npm installs into `/opt/claude-session/npm` via `npm install --global --prefix /opt/claude-session/npm <pkg>@<version> ...`, runs as root, and the prefix is locked with `chown -R root:root` + `chmod -R a-w` after install. Final stage sets `ENV PATH=/opt/claude-session/npm/bin:$PATH`.
- apt runs `apt-get update && apt-get install -y --no-install-recommends <specs>`, then `rm -rf /var/lib/apt/lists/*`, matching `images/base/Dockerfile`'s own pattern.
- `resolved.json` is copied in writable (root-owned, no `--chmod=0444`) when an apt layer is present, patched in place by `install-apt-packages.sh`, then locked with a final `RUN chmod 0444 /opt/session-profile/resolved.json`. When no apt layer is present, it keeps today's behavior: copied in already `--chmod=0444`.
- All new trusted scripts (`install-apt-packages.sh`, `install-npm-packages.sh`) live in `scripts/session/`, get copied into the build context by the renderer (mirroring `install-claude-marketplaces.sh`/`merge-plugin-seed.sh`), and are covered by `scripts/verify`'s `bash -n` syntax-check loop over `scripts/session/*.sh` — no changes needed to that loop, the glob already matches new files.
- New Docker-gated (not msb-gated) tests skip cleanly with `echo 'skip: ...' >&2; exit 0` when `ai-sandboxes-claude:local` isn't built, matching `test-session-marketplace.sh` and `test-resolve-image.sh`.
- Full spec: `docs/superpowers/specs/2026-08-11-session-apt-npm-design.md`.

---

### Task 1: Accept apt/npm in profile validation, add duplicate-name rejection

**Files:**
- Modify: `scripts/session/validate-profile.sh`
- Create: `scripts/session/fixtures/valid/apt-packages.json`
- Create: `scripts/session/fixtures/valid/npm-packages.json`
- Create: `scripts/session/fixtures/valid/apt-npm-marketplaces.json`
- Create: `scripts/session/fixtures/invalid/duplicate-apt-package.json`
- Create: `scripts/session/fixtures/invalid/duplicate-npm-package.json`
- Test: `scripts/session/tests/test-validate-profile.sh` (unchanged — it already iterates `fixtures/valid/*.json` and `fixtures/invalid/*.json` generically, so new fixtures are picked up automatically)

**Interfaces:**
- Consumes: nothing from other tasks.
- Produces: `scripts/session/validate-profile.sh` accepts non-empty `apt`/`npm`, still rejects non-empty `python`, and rejects duplicate package names within `apt` and within `npm`. `scripts/session/fixtures/valid/apt-npm-marketplaces.json` is reused by Task 4's renderer test as the three-layer composability fixture.

- [ ] **Step 1: Add the new fixtures**

`scripts/session/fixtures/valid/apt-packages.json`:

```json
{
  "schema_version": 1,
  "apt": [
    { "name": "graphviz", "version": "2.42.2-8" },
    { "name": "postgresql-client" }
  ]
}
```

`scripts/session/fixtures/valid/npm-packages.json`:

```json
{
  "schema_version": 1,
  "npm": [
    { "package": "@modelcontextprotocol/inspector", "version": "0.14.0" }
  ]
}
```

`scripts/session/fixtures/valid/apt-npm-marketplaces.json`:

```json
{
  "schema_version": 1,
  "apt": [
    { "name": "graphviz", "version": "2.42.2-8" }
  ],
  "npm": [
    { "package": "@modelcontextprotocol/inspector", "version": "0.14.0" }
  ],
  "claude_marketplaces": [
    {
      "url": "https://github.com/rikdc/ai-skills.git",
      "ref": "d66daa0504ff859a9d51c86b9175eb9fe768cd25",
      "path": ".",
      "plugins": ["dev-skills"]
    }
  ]
}
```

`scripts/session/fixtures/invalid/duplicate-apt-package.json`:

```json
{
  "schema_version": 1,
  "apt": [
    { "name": "jq" },
    { "name": "jq", "version": "1.7-1" }
  ]
}
```

`scripts/session/fixtures/invalid/duplicate-npm-package.json`:

```json
{
  "schema_version": 1,
  "npm": [
    { "package": "left-pad", "version": "1.3.0" },
    { "package": "left-pad", "version": "1.3.0" }
  ]
}
```

- [ ] **Step 2: Run the fixture tests to see the new fixtures fail**

Run: `bash scripts/session/tests/test-validate-profile.sh`

Expected: FAIL — the three new valid fixtures are rejected (apt/npm still hard-rejected), and the two new invalid fixtures pass validation when they shouldn't yet (no dedup check exists), so `test-validate-profile.sh` reports multiple `FAIL` lines and exits non-zero.

- [ ] **Step 3: Update the reject-if-non-empty check**

In `scripts/session/validate-profile.sh`, replace this block:

```bash
# The renderer emits an apt/npm/Python-free Dockerfile: those layers are not
# implemented yet, so a profile requesting them would validate but be
# silently dropped from the built image. Reject them explicitly.
# claude_marketplaces IS implemented (see render-dockerfile.sh) and is
# structurally validated separately, below.
jq -e '
  ((.apt // []) | length) == 0 and
  ((.npm // []) | length) == 0 and
  (((.python // {}).enabled // false) == false) and
  (((.python // {}).packages // []) | length) == 0
' "$snapshot" >/dev/null 2>&1 \
  || die 'apt, npm, and python are not yet supported; see docs/session-images.md'
```

with:

```bash
# The renderer does not yet implement the Python layer (task 9): a profile
# requesting it would validate but be silently dropped from the built image.
# Reject it explicitly. apt, npm, and claude_marketplaces ARE implemented
# (see render-dockerfile.sh) and are structurally validated separately, below.
jq -e '
  (((.python // {}).enabled // false) == false) and
  (((.python // {}).packages // []) | length) == 0
' "$snapshot" >/dev/null 2>&1 \
  || die 'python is not yet supported; see docs/session-images.md'
```

- [ ] **Step 4: Add duplicate-name checks to the structural validation**

In the same file, in the big structural `jq -e --argjson max_len ... --argjson max_pkgs ...` expression, replace:

```bash
  ((.apt // []) as $apt | ($apt | type == "array") and all($apt[]; valid_apt_entry)) and
  ((.npm // []) as $npm | ($npm | type == "array") and all($npm[]; valid_pkg_entry)) and
```

with:

```bash
  ((.apt // []) as $apt | ($apt | type == "array") and all($apt[]; valid_apt_entry) and (($apt | map(.name) | length) == ($apt | map(.name) | unique | length))) and
  ((.npm // []) as $npm | ($npm | type == "array") and all($npm[]; valid_pkg_entry) and (($npm | map(.package) | length) == ($npm | map(.package) | unique | length))) and
```

This mirrors the existing `claude_marketplaces` `plugins` dedup check (`(($p | length) == ($p | unique | length))`) already in the same file.

- [ ] **Step 5: Run the fixture tests to verify they pass**

Run: `bash scripts/session/tests/test-validate-profile.sh`

Expected: `ok` — this exercises every fixture in `scripts/session/fixtures/valid/*.json` and `scripts/session/fixtures/invalid/*.json`, including all fixtures added in Step 1 and the pre-existing ones (in particular `scripts/session/fixtures/invalid/non-empty-packages.json`, which stays invalid because it still has `python.enabled: true` — confirm this fixture is still present and still rejected, now specifically because of `python`, not `apt`/`npm`).

- [ ] **Step 6: Commit**

```bash
git add scripts/session/validate-profile.sh scripts/session/fixtures/valid/apt-packages.json scripts/session/fixtures/valid/npm-packages.json scripts/session/fixtures/valid/apt-npm-marketplaces.json scripts/session/fixtures/invalid/duplicate-apt-package.json scripts/session/fixtures/invalid/duplicate-npm-package.json
git commit -m "feat: accept apt and npm fields in session profile validation"
```

Do not add a `Co-Authored-By` trailer.

---

### Task 2: `install-apt-packages.sh` — install apt packages and record actual versions

**Files:**
- Create: `scripts/session/install-apt-packages.sh`

**Interfaces:**
- Consumes: nothing from other tasks (this script is invoked by the Dockerfile that Task 4 generates, but is self-contained and independently testable by direct invocation in a Debian container or, for the provenance-patching step alone, against already-installed packages on any Debian-based host).
- Produces: `scripts/session/install-apt-packages.sh APT_PACKAGES_JSON RESOLVED_JSON` — installs the packages listed in `APT_PACKAGES_JSON` (shape `{"apt": [{"name": "...", "version": "..."?}]}`), then patches `RESOLVED_JSON`'s `.packages.apt` array in place with the actually-installed `dpkg-query` version of each package. Task 4's renderer copies this file into the build context and invokes it with exactly these two positional arguments, in that order.

- [ ] **Step 1: Write the script**

```bash
#!/usr/bin/env bash
set -euo pipefail

apt_json=${1:?usage: install-apt-packages.sh APT_PACKAGES_JSON RESOLVED_JSON}
resolved_json=${2:?usage: install-apt-packages.sh APT_PACKAGES_JSON RESOLVED_JSON}

die() {
  printf 'install-apt-packages: %s\n' "$*" >&2
  exit 1
}

mapfile -t specs < <(jq -r '.apt[] | if has("version") then "\(.name)=\(.version)" else .name end' "$apt_json")
test "${#specs[@]}" -gt 0 || die "no apt packages listed in $apt_json"

apt-get update \
  || die 'apt-get update failed'
apt-get install -y --no-install-recommends "${specs[@]}" \
  || die "apt-get install failed for: ${specs[*]}"

# Apt versions are optional in the profile, and even a pinned version can
# resolve differently depending on the apt repository's state at build time.
# Query the real installed version for every package (pinned or not) so
# resolved.json's provenance always reflects what actually landed in the
# image, not just what was requested.
actual=$(jq -cn '[]')
while IFS= read -r name; do
  version=$(dpkg-query -W -f='${Version}' "$name") \
    || die "dpkg-query could not find an installed version for $name"
  actual=$(jq -c --arg name "$name" --arg version "$version" \
    '. + [{name: $name, version: $version}]' <<<"$actual") \
    || die "could not record installed version for $name"
done < <(jq -r '.apt[].name' "$apt_json")

patched=$(mktemp) || die 'could not create a scratch file for resolved.json'
trap 'rm -f -- "$patched"' EXIT
jq --argjson actual "$actual" '.packages.apt = $actual' "$resolved_json" >"$patched" \
  || die "could not patch $resolved_json with actual apt versions"
mv -f -- "$patched" "$resolved_json" \
  || die "could not install patched $resolved_json"

rm -rf /var/lib/apt/lists/*
```

- [ ] **Step 2: Make it executable and syntax-check it**

Run: `chmod +x scripts/session/install-apt-packages.sh && bash -n scripts/session/install-apt-packages.sh`

Expected: no output, exit 0. (This same `bash -n` check also runs automatically for every file matching `scripts/session/*.sh` as part of `scripts/verify`'s syntax-check loop — no changes needed there.)

- [ ] **Step 3: Manually verify the provenance-patching logic against real `dpkg-query` output**

`apt-get install` itself needs network egress and can't be exercised in every environment, but the patching logic (from `# Apt versions are optional...` through the `mv -f` above) has no such dependency — it only needs `jq` and `dpkg-query`, and can be checked against any two already-installed packages. Run this by hand (adjust package names to whatever is already installed on the machine you're working on, e.g. `bash` and `coreutils` on a Debian/Ubuntu host):

```bash
printf '[{"name": "bash"}, {"name": "coreutils", "version": "9.4-3.1"}]\n' >/tmp/apt-check.json
printf '{"packages": {"apt": [{"name": "bash"}, {"name": "coreutils", "version": "9.4-3.1"}]}}\n' >/tmp/resolved-check.json
dpkg-query -W -f='${Version}' bash
```

Expected: `dpkg-query` prints a real version string (e.g. `5.2.37-2+b9`). Confirms `dpkg-query -W -f='${Version}' <name>` is the right invocation before it's load-bearing inside a Docker build where mistakes are slower to iterate on. Full end-to-end behavior (a real `apt-get install` writing packages that this script then queries) is exercised by Task 6's integration test.

- [ ] **Step 4: Commit**

```bash
git add scripts/session/install-apt-packages.sh
git commit -m "feat: add install-apt-packages.sh session installer"
```

Do not add a `Co-Authored-By` trailer.

---

### Task 3: `install-npm-packages.sh` — install npm packages into an image-local prefix

**Files:**
- Create: `scripts/session/install-npm-packages.sh`

**Interfaces:**
- Consumes: nothing from other tasks.
- Produces: `scripts/session/install-npm-packages.sh NPM_PACKAGES_JSON` — installs every package listed in `NPM_PACKAGES_JSON` (shape `{"npm": [{"package": "...", "version": "..."}]}`) into `/opt/claude-session/npm` via one `npm install --global --prefix` invocation, clears npm's cache, and locks the prefix `chown -R root:root` + `chmod -R a-w`. Task 4's renderer copies this file into the build context, invokes it with the one positional argument, and separately emits `ENV PATH=/opt/claude-session/npm/bin:$PATH` in the Dockerfile (not this script's job — this script only installs and locks).

- [ ] **Step 1: Write the script**

```bash
#!/usr/bin/env bash
set -euo pipefail

npm_json=${1:?usage: install-npm-packages.sh NPM_PACKAGES_JSON}
prefix=/opt/claude-session/npm

die() {
  printf 'install-npm-packages: %s\n' "$*" >&2
  exit 1
}

mapfile -t specs < <(jq -r '.npm[] | "\(.package)@\(.version)"' "$npm_json")
test "${#specs[@]}" -gt 0 || die "no npm packages listed in $npm_json"

npm install --global --prefix "$prefix" "${specs[@]}" \
  || die "npm install failed for: ${specs[*]}"

cache_dir=$(npm config get cache) \
  || die 'could not determine npm cache directory'
rm -rf "$cache_dir"

chown -R root:root "$prefix" \
  || die "could not lock ownership of $prefix"
chmod -R a-w "$prefix" \
  || die "could not lock permissions of $prefix"
```

A global-prefix install produces `<prefix>/bin/<binary>` as a relative symlink into `<prefix>/lib/node_modules/<package>/...`, already executable — verified empirically against a local fake npm package before this plan was written, since this design's registry access can't be assumed everywhere the script runs. `chmod -R a-w` only clears write bits, so the existing executable bits on the installed binaries and their symlinks are preserved.

- [ ] **Step 2: Make it executable and syntax-check it**

Run: `chmod +x scripts/session/install-npm-packages.sh && bash -n scripts/session/install-npm-packages.sh`

Expected: no output, exit 0.

- [ ] **Step 3: Commit**

```bash
git add scripts/session/install-npm-packages.sh
git commit -m "feat: add install-npm-packages.sh session installer"
```

Do not add a `Co-Authored-By` trailer.

---

### Task 4: Restructure the renderer for composable apt/npm/marketplace layers

**Files:**
- Modify: `scripts/session/render-dockerfile.sh`
- Modify: `scripts/session/tests/test-render-dockerfile.sh`

**Interfaces:**
- Consumes: `scripts/session/install-apt-packages.sh` (Task 2) and `scripts/session/install-npm-packages.sh` (Task 3) by path (copied into the build context, same pattern as the existing marketplace installer copy). `scripts/session/fixtures/valid/apt-npm-marketplaces.json` (Task 1) as the three-layer test fixture.
- Produces: `scripts/session/render-dockerfile.sh CONTEXT_DIR BASE_IMAGE_REF CANONICAL_PROFILE_JSON` now emits apt/npm/marketplace layers in any combination (including none — this behavior is unchanged and byte-identical to today for the empty-profile case). This is what Task 5's cache-key hash and Task 6's integration test build against.

- [ ] **Step 1: Extend the render test to assert the new layers (written first, expected to fail)**

Replace the full contents of `scripts/session/tests/test-render-dockerfile.sh` with:

```bash
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
grep -qFx 'FROM ai-sandboxes-claude-session-base:deadbeef' "$apt_context_dir/Dockerfile"
grep -qF 'COPY --chown=root:root resolved.json /opt/session-profile/resolved.json' "$apt_context_dir/Dockerfile"
grep -qFx 'RUN /usr/local/lib/ai-sandboxes/install-session-apt-packages.sh /opt/session-apt-packages.json /opt/session-profile/resolved.json' "$apt_context_dir/Dockerfile"
grep -qFx 'RUN chmod 0444 /opt/session-profile/resolved.json' "$apt_context_dir/Dockerfile"
# apt makes resolved.json writable-then-locked (patched by the installer, then
# locked as the very last step) instead of copied in already read-only.
if grep -qF -- '--chmod=0444 resolved.json' "$apt_context_dir/Dockerfile"; then
  echo 'FAIL: apt-only render should not copy resolved.json already read-only' >&2
  exit 1
fi
diff -q "$apt_context_dir/install-apt-packages.sh" scripts/session/install-apt-packages.sh
jq -e '.apt | length == 1 and .[0].name == "tree"' "$apt_context_dir/session-apt-packages.json" >/dev/null
test "$(find "$apt_context_dir" -maxdepth 1 -type f | wc -l)" -eq 4

echo '{"ok":true}' >"$npm_context_dir/resolved.json"
profile_with_npm='{"schema_version":1,"npm":[{"package":"cowsay","version":"1.6.0"}]}'
scripts/session/render-dockerfile.sh "$npm_context_dir" 'ai-sandboxes-claude-session-base:deadbeef' "$profile_with_npm"

test -f "$npm_context_dir/Dockerfile"
test -f "$npm_context_dir/session-npm-packages.json"
test -f "$npm_context_dir/install-npm-packages.sh"
grep -qFx 'RUN /usr/local/lib/ai-sandboxes/install-session-npm-packages.sh /opt/session-npm-packages.json' "$npm_context_dir/Dockerfile"
grep -qFx 'ENV PATH=/opt/claude-session/npm/bin:$PATH' "$npm_context_dir/Dockerfile"
grep -qF -- '--chmod=0444 resolved.json' "$npm_context_dir/Dockerfile"
diff -q "$npm_context_dir/install-npm-packages.sh" scripts/session/install-npm-packages.sh
jq -e '.npm | length == 1 and .[0].package == "cowsay"' "$npm_context_dir/session-npm-packages.json" >/dev/null
test "$(find "$npm_context_dir" -maxdepth 1 -type f | wc -l)" -eq 4

echo '{"ok":true}' >"$combined_context_dir/resolved.json"
profile_combined=$(cat scripts/session/fixtures/valid/apt-npm-marketplaces.json)
scripts/session/render-dockerfile.sh "$combined_context_dir" 'ai-sandboxes-claude-session-base:deadbeef' "$profile_combined"

test -f "$combined_context_dir/Dockerfile"
# Canonical layer order regardless of profile field order: apt, then npm,
# then the marketplace build stage's output.
apt_line=$(grep -n 'install-session-apt-packages.sh /opt/session-apt-packages.json /opt/session-profile/resolved.json' "$combined_context_dir/Dockerfile" | cut -d: -f1)
npm_line=$(grep -n 'install-session-npm-packages.sh /opt/session-npm-packages.json' "$combined_context_dir/Dockerfile" | cut -d: -f1)
marketplace_copy_line=$(grep -n 'COPY --from=build --chown=root:root /opt/claude-plugin-cache /opt/claude-plugin-cache' "$combined_context_dir/Dockerfile" | cut -d: -f1)
test "$apt_line" -lt "$npm_line"
test "$npm_line" -lt "$marketplace_copy_line"
test "$(find "$combined_context_dir" -maxdepth 1 -type f | wc -l)" -eq 9

echo ok
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `bash scripts/session/tests/test-render-dockerfile.sh`

Expected: FAIL — the renderer doesn't emit apt/npm layers yet, so `test -f "$apt_context_dir/session-apt-packages.json"` (and the npm/combined equivalents) fail.

- [ ] **Step 3: Rewrite `render-dockerfile.sh`**

Replace the full contents of `scripts/session/render-dockerfile.sh` with:

```bash
#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "$0")/../.." && pwd)

context_dir=${1:?usage: render-dockerfile.sh CONTEXT_DIR BASE_IMAGE_REF CANONICAL_PROFILE_JSON}
base_image_ref=${2:?usage: render-dockerfile.sh CONTEXT_DIR BASE_IMAGE_REF CANONICAL_PROFILE_JSON}
canonical_profile=${3:?usage: render-dockerfile.sh CONTEXT_DIR BASE_IMAGE_REF CANONICAL_PROFILE_JSON}

test -f "$context_dir/resolved.json" || {
  echo 'render-dockerfile: missing resolved.json in context' >&2
  exit 1
}

apt_packages=$(jq -c '.apt // []' <<<"$canonical_profile")
apt_count=$(jq 'length' <<<"$apt_packages")
npm_packages=$(jq -c '.npm // []' <<<"$canonical_profile")
npm_count=$(jq 'length' <<<"$npm_packages")
marketplaces=$(jq -c '.claude_marketplaces // []' <<<"$canonical_profile")
marketplace_count=$(jq 'length' <<<"$marketplaces")

if test "$apt_count" -eq 0 && test "$npm_count" -eq 0 && test "$marketplace_count" -eq 0; then
  cat >"$context_dir/Dockerfile" <<EOF
# syntax=docker/dockerfile:1.7
FROM $base_image_ref
USER root
COPY --chown=root:root --chmod=0444 resolved.json /opt/session-profile/resolved.json
USER node
EOF
  exit 0
fi

dockerfile="$context_dir/Dockerfile"
: >"$dockerfile"
printf '# syntax=docker/dockerfile:1.7\n' >>"$dockerfile"

if test "$marketplace_count" -gt 0; then
  jq -n --argjson claude "$marketplaces" '{claude: $claude, codex: []}' \
    >"$context_dir/session-marketplaces.json"
  cp -- "$repo_root/scripts/marketplaces/install-claude.sh" "$context_dir/install-claude-marketplaces.sh"
  cp -- "$repo_root/scripts/session/merge-plugin-seed.sh" "$context_dir/merge-plugin-seed.sh"

  cat >>"$dockerfile" <<EOF
FROM $base_image_ref AS build
USER root
COPY --chown=node:node session-marketplaces.json /opt/session-marketplaces.json
COPY --chown=node:node --chmod=0755 install-claude-marketplaces.sh /usr/local/lib/ai-sandboxes/install-session-claude-marketplaces.sh
COPY --chown=node:node --chmod=0755 merge-plugin-seed.sh /usr/local/lib/ai-sandboxes/merge-session-plugin-seed.sh
RUN chown -R node:node /opt/claude-plugin-cache /opt/claude-plugin-seed \\
 && chmod -R u+w /opt/claude-plugin-cache /opt/claude-plugin-seed \\
 && install -d -o node -g node -m 0755 /opt/claude-session-build-home /opt/claude-marketplaces
USER node
ENV HOME=/opt/claude-session-build-home CLAUDE_CODE_PLUGIN_CACHE_DIR=/opt/claude-plugin-cache CLAUDE_CODE_PLUGIN_SEED_DIR=/opt/claude-plugin-seed
RUN /usr/local/lib/ai-sandboxes/install-session-claude-marketplaces.sh /opt/session-marketplaces.json
USER root
RUN /usr/local/lib/ai-sandboxes/merge-session-plugin-seed.sh /opt/claude-session-build-home/.claude/settings.json /opt/claude-plugin-seed/settings.json \\
 && chown -R root:root /opt/claude-plugin-cache /opt/claude-plugin-seed \\
 && chmod -R a-w /opt/claude-plugin-cache /opt/claude-plugin-seed
USER node
FROM $base_image_ref
USER root
EOF
else
  cat >>"$dockerfile" <<EOF
FROM $base_image_ref
USER root
EOF
fi

if test "$apt_count" -gt 0; then
  jq -n --argjson apt "$apt_packages" '{apt: $apt}' >"$context_dir/session-apt-packages.json"
  cp -- "$repo_root/scripts/session/install-apt-packages.sh" "$context_dir/install-apt-packages.sh"
  cat >>"$dockerfile" <<EOF
COPY --chown=root:root session-apt-packages.json /opt/session-apt-packages.json
COPY --chown=root:root --chmod=0755 install-apt-packages.sh /usr/local/lib/ai-sandboxes/install-session-apt-packages.sh
COPY --chown=root:root resolved.json /opt/session-profile/resolved.json
RUN /usr/local/lib/ai-sandboxes/install-session-apt-packages.sh /opt/session-apt-packages.json /opt/session-profile/resolved.json
EOF
fi

if test "$npm_count" -gt 0; then
  jq -n --argjson npm "$npm_packages" '{npm: $npm}' >"$context_dir/session-npm-packages.json"
  cp -- "$repo_root/scripts/session/install-npm-packages.sh" "$context_dir/install-npm-packages.sh"
  cat >>"$dockerfile" <<EOF
COPY --chown=root:root session-npm-packages.json /opt/session-npm-packages.json
COPY --chown=root:root --chmod=0755 install-npm-packages.sh /usr/local/lib/ai-sandboxes/install-session-npm-packages.sh
RUN /usr/local/lib/ai-sandboxes/install-session-npm-packages.sh /opt/session-npm-packages.json
ENV PATH=/opt/claude-session/npm/bin:\$PATH
EOF
fi

if test "$marketplace_count" -gt 0; then
  cat >>"$dockerfile" <<EOF
COPY --from=build --chown=root:root /opt/claude-plugin-cache /opt/claude-plugin-cache
COPY --from=build --chown=root:root /opt/claude-plugin-seed /opt/claude-plugin-seed
RUN install -d -o node -g node -m 0755 /opt/claude-plugin-cache/data
EOF
fi

if test "$apt_count" -eq 0; then
  cat >>"$dockerfile" <<EOF
COPY --chown=root:root --chmod=0444 resolved.json /opt/session-profile/resolved.json
EOF
else
  cat >>"$dockerfile" <<EOF
RUN chmod 0444 /opt/session-profile/resolved.json
EOF
fi

printf 'USER node\n' >>"$dockerfile"
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `bash scripts/session/tests/test-render-dockerfile.sh`

Expected: `ok`. This exact renderer script and this exact test were validated together by hand (against a scratch copy, with placeholder installer files standing in for Tasks 2/3's real scripts) before being written into this plan: all five cases — empty, apt-only, npm-only, apt+npm, and apt+npm+marketplace — were rendered and produced the file counts and content asserted above (2, 4, 4, 6, and 9 files respectively; empty-profile output byte-identical to what ships today).

- [ ] **Step 5: Commit**

```bash
git add scripts/session/render-dockerfile.sh scripts/session/tests/test-render-dockerfile.sh
git commit -m "feat: render composable apt/npm/marketplace session image layers"
```

Do not add a `Co-Authored-By` trailer.

---

### Task 5: Extend the resolve-image.sh cache-key hash to cover the new installers

**Files:**
- Modify: `scripts/session/resolve-image.sh`

**Interfaces:**
- Consumes: `scripts/session/install-apt-packages.sh` (Task 2), `scripts/session/install-npm-packages.sh` (Task 3) by path.
- Produces: no interface change — `resolve-image.sh`'s cache key now also depends on the two new scripts' contents, so a future change to either busts the session-image cache. `packages.apt` in the generated `resolved.json` metadata is unchanged (`($request.apt // [])`, the initial verbatim echo of the request — `install-apt-packages.sh` patches this file in place at build time, after `resolve-image.sh` has already written it).

- [ ] **Step 1: Extend the `renderer_hash` computation**

In `scripts/session/resolve-image.sh`, replace:

```bash
renderer_hash=$(shasum -a 256 scripts/session/render-dockerfile.sh scripts/marketplaces/install-claude.sh scripts/session/merge-plugin-seed.sh \
  | shasum -a 256 | awk '{print $1}')
```

with:

```bash
renderer_hash=$(shasum -a 256 scripts/session/render-dockerfile.sh scripts/marketplaces/install-claude.sh scripts/session/merge-plugin-seed.sh scripts/session/install-apt-packages.sh scripts/session/install-npm-packages.sh \
  | shasum -a 256 | awk '{print $1}')
```

- [ ] **Step 2: Manually verify the hash changes when either new file changes**

This only needs `shasum`/`jq`, not a full Docker build:

```bash
shasum -a 256 scripts/session/render-dockerfile.sh scripts/marketplaces/install-claude.sh scripts/session/merge-plugin-seed.sh scripts/session/install-apt-packages.sh scripts/session/install-npm-packages.sh | shasum -a 256 | awk '{print $1}'
printf '# touch\n' >>scripts/session/install-npm-packages.sh
shasum -a 256 scripts/session/render-dockerfile.sh scripts/marketplaces/install-claude.sh scripts/session/merge-plugin-seed.sh scripts/session/install-apt-packages.sh scripts/session/install-npm-packages.sh | shasum -a 256 | awk '{print $1}'
git checkout -- scripts/session/install-npm-packages.sh
```

Expected: the two printed hashes differ, and `git status` shows a clean tree again after the `git checkout --`.

- [ ] **Step 3: Run the existing resolve-image test (Docker-gated, skips cleanly without a built base image)**

Run: `bash scripts/session/tests/test-resolve-image.sh`

Expected: `ok`, or `skip: ai-sandboxes-claude:local not built (run ./scripts/build)` if no base image is available in this environment — either is a pass for this step.

- [ ] **Step 4: Commit**

```bash
git add scripts/session/resolve-image.sh
git commit -m "fix: bust session-image cache key on apt/npm installer changes"
```

Do not add a `Co-Authored-By` trailer.

---

### Task 6: End-to-end apt+npm session image integration test

**Files:**
- Create: `scripts/session/fixtures/valid/apt-npm-packages.json`
- Create: `scripts/session/tests/test-session-apt-npm.sh`
- Modify: `scripts/verify`

**Interfaces:**
- Consumes: `scripts/session/resolve-image.sh` (Task 5), the full renderer + installer chain (Tasks 2-4).
- Produces: a real, Docker-built session image proving apt and npm packages actually install, run, and are provenance-recorded — this is the task that actually exercises `apt-get install` and `npm install` for the first time in this plan (Tasks 2 and 3 could only be verified structurally/by hand, not run end-to-end, without this test).

- [ ] **Step 1: Add the integration fixture**

`scripts/session/fixtures/valid/apt-npm-packages.json`:

```json
{
  "schema_version": 1,
  "apt": [
    { "name": "tree" }
  ],
  "npm": [
    { "package": "cowsay", "version": "1.6.0" }
  ]
}
```

`tree` is a small apt package not already installed by `images/base/Dockerfile` (which installs `bash ca-certificates curl git gnupg jq openssh-client ripgrep` plus `gh`), so a working `tree` binary in the derived image is real evidence the apt layer ran. `tree` has no pinned version here, so this fixture also exercises the unpinned-package path of the provenance patch. `cowsay` is a small, dependency-free npm package with a CLI binary, matching the kind of package this layer targets.

- [ ] **Step 2: Write the test**

```bash
#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../../.." || exit 1

if ! docker image inspect ai-sandboxes-claude:local >/dev/null 2>&1; then
  echo 'skip: ai-sandboxes-claude:local not built (run ./scripts/build)' >&2
  exit 0
fi

session_tag=''
cleanup() {
  if test -n "$session_tag"; then
    docker image rm -f "$session_tag" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

session_tag=$(CLAUDE_MSB_BUILD_EGRESS=1 scripts/session/resolve-image.sh scripts/session/fixtures/valid/apt-npm-packages.json)

docker run --rm --user node "$session_tag" tree --version >/dev/null \
  || { echo 'apt-installed tree binary does not run' >&2; exit 1; }

recorded_version=$(docker run --rm --user node "$session_tag" sh -c "jq -r '.packages.apt[] | select(.name==\"tree\") | .version' /opt/session-profile/resolved.json")
actual_version=$(docker run --rm --user root "$session_tag" dpkg-query -W -f='${Version}' tree)
test -n "$recorded_version" \
  || { echo 'resolved.json has no recorded apt version for tree' >&2; exit 1; }
test "$recorded_version" = "$actual_version" \
  || { echo "resolved.json apt version ($recorded_version) does not match dpkg ($actual_version)" >&2; exit 1; }

cowsay_output=$(docker run --rm --user node "$session_tag" cowsay moo 2>&1) \
  || { echo 'npm-installed cowsay binary does not run via PATH' >&2; printf '%s\n' "$cowsay_output" >&2; exit 1; }
printf '%s\n' "$cowsay_output" | grep -q moo \
  || { echo 'cowsay output missing expected text' >&2; exit 1; }

docker run --rm --user node "$session_tag" sh -c '! touch /opt/claude-session/npm/.write-test 2>/dev/null' \
  || { echo 'npm prefix is writable by node' >&2; exit 1; }
docker run --rm --user node "$session_tag" sh -c '! touch /usr/bin/.write-test 2>/dev/null' \
  || { echo 'apt system path is writable by node' >&2; exit 1; }

echo ok
```

- [ ] **Step 3: Wire the test into `scripts/verify`**

In `scripts/verify`, the unconditional Docker-only test list currently reads:

```bash
bash scripts/session/tests/test-validate-profile.sh
bash scripts/session/tests/test-render-dockerfile.sh
bash scripts/session/tests/test-resolve-image.sh
bash scripts/session/tests/test-session-marketplace.sh
bash scripts/session/tests/test-load-image.sh
```

Add the new test right after `test-session-marketplace.sh`:

```bash
bash scripts/session/tests/test-validate-profile.sh
bash scripts/session/tests/test-render-dockerfile.sh
bash scripts/session/tests/test-resolve-image.sh
bash scripts/session/tests/test-session-marketplace.sh
bash scripts/session/tests/test-session-apt-npm.sh
bash scripts/session/tests/test-load-image.sh
```

- [ ] **Step 4: Run the new test directly**

Run: `chmod +x scripts/session/tests/test-session-apt-npm.sh && bash scripts/session/tests/test-session-apt-npm.sh`

Expected: `ok` if `ai-sandboxes-claude:local` is built and `CLAUDE_MSB_BUILD_EGRESS=1`-gated network access works in this environment; `skip: ai-sandboxes-claude:local not built (run ./scripts/build)` otherwise. If the base image is available but this environment has no real network egress, the build itself will fail with a clear `apt-get update failed` or `npm install failed` error from Task 2/3's `die` calls — that is expected outside a fully networked environment and is not a defect in the test or the installers; it must still be run and pass in CI/on a networked host before this task is considered done.

- [ ] **Step 5: Commit**

```bash
git add scripts/session/fixtures/valid/apt-npm-packages.json scripts/session/tests/test-session-apt-npm.sh scripts/verify
git commit -m "test: add end-to-end apt+npm session image integration test"
```

Do not add a `Co-Authored-By` trailer.

---

### Task 7: Update `docs/session-images.md`

**Files:**
- Modify: `docs/session-images.md`

**Interfaces:**
- Consumes: nothing (documentation only).
- Produces: nothing consumed by other tasks — this is the last task.

- [ ] **Step 1: Update the "Session profile schema" section**

Replace:

```markdown
The renderer emits the fixed, package-free Dockerfile described under
Implementation task 7 unless a profile's `claude_marketplaces` field is
non-empty, in which case it also emits the marketplace-install layer
described under "Claude marketplaces and plugins" below (Implementation task
10). `apt`, `npm`, and `python` are not yet installed by the renderer, so
validation still rejects any profile with a non-empty `apt`, `npm`, or
`python` field rather than accepting and silently dropping it. Only an empty
profile, or one with only `claude_marketplaces` populated, validates until
tasks 8-9 land.
```

with:

```markdown
The renderer emits the fixed, package-free Dockerfile described under
Implementation task 7 unless a profile's `apt`, `npm`, or
`claude_marketplaces` fields are non-empty, in which case it emits the
corresponding layers described under "Package layers" and "Claude
marketplaces and plugins" below (Implementation tasks 8 and 10); any
combination of these fields may be populated together, in a fixed
apt-then-npm-then-marketplace order regardless of the profile's own field
order. `python` is not yet installed by the renderer, so validation still
rejects any profile with a non-empty `python` field rather than accepting
and silently dropping it. Only a profile with `python` left unset validates
until task 9 lands.
```

- [ ] **Step 2: Update the "Apt" package-layer section**

Replace:

```markdown
### Apt

The generated Dockerfile installs only validated package specifications under
`USER root`, removes apt lists afterwards, and then returns to `USER node`.
The renderer, not the profile, constructs command syntax. This is an image-build
operation; there is no runtime `apt-get` capability.
```

with:

```markdown
### Apt

The generated Dockerfile installs only validated package specifications under
`USER root`, removes apt lists afterwards, and then returns to `USER node`.
The renderer, not the profile, constructs command syntax. This is an image-build
operation; there is no runtime `apt-get` capability.

Apt versions are optional in the profile, and even a pinned version can
resolve differently depending on the apt repository's state at build time.
After install, `scripts/session/install-apt-packages.sh` queries
`dpkg-query` for each package's actual installed version and patches
`/opt/session-profile/resolved.json` with it before the file is locked
read-only, so the recorded provenance always reflects what was actually
installed, not just what was requested.
```

- [ ] **Step 3: Update the "npm" package-layer section**

Replace:

```markdown
### npm

npm packages install during the build into an image-local prefix such as
`/opt/claude-session/npm`. The final prefix is root-owned and read-only, with
the selected bin directory added to `PATH`. The precise bin shim layout will be
verified against npm before implementation.
```

with:

```markdown
### npm

npm packages install during the build into an image-local prefix at
`/opt/claude-session/npm`, via a single `npm install --global --prefix`
invocation. The final prefix is root-owned and read-only
(`chown -R root:root` + `chmod -R a-w`), with its `bin` directory added to
`PATH`. A global-prefix install produces `<prefix>/bin/<binary>` as a
relative symlink into `<prefix>/lib/node_modules/<package>/...`, which
resolves correctly regardless of where the prefix ends up in the final
image.
```

- [ ] **Step 4: Commit**

```bash
git add docs/session-images.md
git commit -m "docs: describe implemented apt and npm session layers"
```

Do not add a `Co-Authored-By` trailer.

---

## Self-Review

**Spec coverage:**
- Purpose/non-goals (python out of scope) → Task 1 keeps python rejected.
- Composability, fixed layer order → Task 4, verified empirically against a scratch copy before being written here.
- apt layer (no discarded stage, `apt-get update`/`install`/cleanup, provenance patch) → Tasks 2 and 4.
- npm layer (prefix, lock, `PATH`) → Tasks 3 and 4.
- Provenance write-then-lock `resolved.json` → Task 4 (renderer), Task 2 (patch).
- Validation (accept apt/npm, dedup check) → Task 1.
- Testing (valid/invalid fixtures, render-test extension, Docker-gated integration test) → Tasks 1, 4, 6.
- Cache key extension → Task 5.
- Docs → Task 7.

**Placeholder scan:** No `TBD`/`TODO`/"add appropriate handling" language; every step has literal file contents or literal commands.

**Type/name consistency:** `install-apt-packages.sh` takes `(APT_PACKAGES_JSON, RESOLVED_JSON)` consistently across Tasks 2 and 4. `install-npm-packages.sh` takes `(NPM_PACKAGES_JSON)` consistently across Tasks 3 and 4. Prefix path `/opt/claude-session/npm` consistent across Tasks 3, 4, 6, and 7. Installed-script destination paths (`/usr/local/lib/ai-sandboxes/install-session-apt-packages.sh`, `/usr/local/lib/ai-sandboxes/install-session-npm-packages.sh`) consistent between the renderer (Task 4) and what the `RUN` lines invoke.
