# Session Images: Minimum Vertical Slice Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship an explicit `claude-session --profile ...` launcher that resolves, builds (on cache miss), caches, and runs an empty-profile derived image on top of `ai-sandboxes-claude:local`, with the full hardened runtime policy intact.

**Architecture:** A new `scripts/session/` toolchain validates a session-profile JSON file with `jq`, renders a minimal trusted Dockerfile into an isolated temp build context, computes a content-addressed cache key, and builds/loads the result into `msb` under a distinct tag. A new `shell/fish/claude-session.fish` launcher wires this resolver to the *same* Claude network/mount/security construction `claude.fish` already uses, extracted into a shared library function so both launchers stay byte-for-byte identical at the `msb run` boundary. This slice carries no apt/npm/python/plugin installation yet — those are follow-on plans (spec tasks 8–12) that build on the resolver and Dockerfile renderer introduced here.

**Tech Stack:** Bash (`set -euo pipefail`, `jq -e` validation — the pattern already used by `scripts/marketplaces/install-claude.sh` and `scripts/tools/validate-selection.sh`), Fish (`shell/fish/lib/ai-sandbox.fish` `__ai_sandbox_*` helpers), Docker Buildx, Microsandbox (`msb`).

## Global Constraints

- Target image platform is `linux/arm64` only (matches `docker-bake.hcl`).
- Claude always runs as `node`, `--security restricted`, no `sudo`; system paths under `/opt` are read-only at runtime.
- A session profile path must always be supplied explicitly by the host user (as a literal path or as a bare name resolved under `~/.config/microvms/profiles/`); it must never be auto-discovered from the project mount.
- Session profiles accept only public sources: public GitHub over HTTPS pinned to a full 40-character commit SHA (marketplaces), public npm/PyPI package names, default apt sources. No credentials, arbitrary URLs, shell syntax, package-manager options, local package files, or build args are ever accepted from a profile.
- A session image build must never use the project checkout as Docker context, and must receive no project mount, host home mount, Docker/registry credentials, SSH agent, or BuildKit secret.
- A cache miss that requires a build needs an explicit host opt-in via `CLAUDE_MSB_BUILD_EGRESS=1`; a cache hit needs none.
- The msb loader must never overwrite or remove `ai-sandboxes-claude:local`, and must never touch base/tools/Claude/Codex image tags.
- Follow existing conventions exactly: `set -euo pipefail` bash scripts, `jq -e` boolean-predicate validation (no new schema-validation dependency), Fish `__ai_sandbox_*`-prefixed shared helpers in `shell/fish/lib/ai-sandbox.fish`.
- Spec reference: `docs/session-images.md`. This plan covers spec tasks 1–7 (the "minimum vertical slice") only; spec tasks 8–12 (apt/npm, Python, plugin overlay, GC, doc migration) are separate follow-on plans.

---

## Task 1: Session profile example and validation fixtures

**Files:**
- Create: `config/session-profile.example.json`
- Create: `scripts/session/fixtures/valid/empty.json`
- Create: `scripts/session/fixtures/valid/full.json`
- Create: `scripts/session/fixtures/invalid/unknown-field.json`
- Create: `scripts/session/fixtures/invalid/bad-marketplace-ref.json`
- Create: `scripts/session/fixtures/invalid/credential-in-npm-version.json`
- Create: `scripts/session/fixtures/invalid/shell-metacharacter-package-name.json`
- Create: `scripts/session/fixtures/invalid/oversized-field.json`
- Create: `scripts/session/fixtures/invalid/missing-npm-version.json`

**Interfaces:**
- Produces: `config/session-profile.example.json` — the shipped starting-point profile (schema_version only, no selections). `scripts/session/fixtures/valid/*.json` and `scripts/session/fixtures/invalid/*.json` — inputs Task 2's validator test consumes by filename glob.

- [ ] **Step 1: Create the shipped example profile**

```json
{
  "schema_version": 1
}
```

Save as `config/session-profile.example.json`.

- [ ] **Step 2: Create the valid fixtures**

`scripts/session/fixtures/valid/empty.json` (identical to the example above):

```json
{
  "schema_version": 1
}
```

`scripts/session/fixtures/valid/full.json` (one entry of every kind, all within the limits Task 2 will enforce):

```json
{
  "schema_version": 1,
  "apt": [
    { "name": "graphviz", "version": "2.42.2-8" },
    { "name": "postgresql-client" }
  ],
  "npm": [
    { "package": "@modelcontextprotocol/inspector", "version": "0.14.0" }
  ],
  "python": {
    "enabled": true,
    "packages": [
      { "package": "ruff", "version": "0.9.1" }
    ]
  },
  "claude_marketplaces": [
    {
      "url": "https://github.com/rikdc/ai-skills.git",
      "ref": "1111111111111111111111111111111111111111",
      "path": ".",
      "plugins": ["dev-skills"]
    }
  ]
}
```

- [ ] **Step 3: Create the invalid fixtures, one violation each**

`scripts/session/fixtures/invalid/unknown-field.json`:

```json
{
  "schema_version": 1,
  "docker_args": ["--privileged"]
}
```

`scripts/session/fixtures/invalid/bad-marketplace-ref.json` (ref is a branch name, not a full 40-hex SHA):

```json
{
  "schema_version": 1,
  "claude_marketplaces": [
    { "url": "https://github.com/rikdc/ai-skills.git", "ref": "main", "path": "." }
  ]
}
```

`scripts/session/fixtures/invalid/credential-in-npm-version.json` (version field is not a plain version string):

```json
{
  "schema_version": 1,
  "npm": [
    { "package": "left-pad", "version": "https://user:pass@example.com/left-pad.tgz" }
  ]
}
```

`scripts/session/fixtures/invalid/shell-metacharacter-package-name.json`:

```json
{
  "schema_version": 1,
  "apt": [
    { "name": "graphviz; rm -rf /" }
  ]
}
```

`scripts/session/fixtures/invalid/missing-npm-version.json` (npm version is mandatory):

```json
{
  "schema_version": 1,
  "npm": [
    { "package": "left-pad" }
  ]
}
```

`scripts/session/fixtures/invalid/oversized-field.json` — generate with a command rather than hand-typing 201 characters:

```bash
mkdir -p scripts/session/fixtures/invalid
jq -n --arg name "$(printf 'a%.0s' $(seq 1 201))" '{schema_version: 1, apt: [{name: $name}]}' \
  >scripts/session/fixtures/invalid/oversized-field.json
```

- [ ] **Step 4: Verify every fixture is well-formed JSON**

Run: `for f in config/session-profile.example.json scripts/session/fixtures/valid/*.json scripts/session/fixtures/invalid/*.json; do jq empty "$f" || echo "BAD: $f"; done`
Expected: no `BAD:` lines printed.

- [ ] **Step 5: Commit**

```bash
git add config/session-profile.example.json scripts/session/fixtures
git commit -m "feat: add session profile example and validation fixtures"
```

---

## Task 2: Session profile validator

**Files:**
- Create: `scripts/session/validate-profile.sh`
- Create: `scripts/session/tests/test-validate-profile.sh`
- Modify: `scripts/verify` (add `scripts/session/*.sh` and `scripts/session/tests/*.sh` to the existing `bash -n` syntax-check loop)

**Interfaces:**
- Consumes: fixtures from Task 1 (`scripts/session/fixtures/valid/*.json`, `scripts/session/fixtures/invalid/*.json`).
- Produces: `scripts/session/validate-profile.sh PROFILE_PATH` — on a valid profile, prints canonical compact JSON (`jq -Sc`) to stdout and exits 0; on an invalid profile, prints `validate-profile: ...` to stderr and exits 2. Task 4 depends on this exact stdout/exit contract.

- [ ] **Step 1: Write the failing test**

Create `scripts/session/tests/test-validate-profile.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../../.."

fail=0

for fixture in scripts/session/fixtures/valid/*.json; do
  canonical=$(scripts/session/validate-profile.sh "$fixture") || { echo "FAIL (should be valid): $fixture" >&2; fail=1; continue; }
  jq empty <<<"$canonical" || { echo "FAIL (stdout not JSON): $fixture" >&2; fail=1; continue; }
  canonical_again=$(scripts/session/validate-profile.sh "$fixture")
  test "$canonical" = "$canonical_again" || { echo "FAIL (not stable): $fixture" >&2; fail=1; }
done

for fixture in scripts/session/fixtures/invalid/*.json; do
  if scripts/session/validate-profile.sh "$fixture" >/dev/null 2>/tmp/validate-profile-stderr.$$; then
    echo "FAIL (should be invalid): $fixture" >&2
    fail=1
  else
    test -s /tmp/validate-profile-stderr.$$ || { echo "FAIL (no stderr message): $fixture" >&2; fail=1; }
  fi
  rm -f /tmp/validate-profile-stderr.$$
done

test "$fail" -eq 0
echo ok
```

Make it executable: `chmod +x scripts/session/tests/test-validate-profile.sh`

- [ ] **Step 2: Run test to verify it fails**

Run: `bash scripts/session/tests/test-validate-profile.sh`
Expected: FAIL — `scripts/session/validate-profile.sh: No such file or directory`

- [ ] **Step 3: Write the validator**

Create `scripts/session/validate-profile.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

profile_path=${1:?usage: validate-profile.sh PROFILE_PATH}

die() {
  printf 'validate-profile: %s\n' "$*" >&2
  exit 2
}

max_bytes=32768
max_field_length=200
max_packages=50

test -r "$profile_path" || die "cannot read profile: $profile_path"

size=$(wc -c <"$profile_path")
test "$size" -le "$max_bytes" || die "profile exceeds $max_bytes bytes"

jq -e . "$profile_path" >/dev/null 2>&1 || die 'profile is not valid JSON'

jq -e --argjson max_len "$max_field_length" --argjson max_pkgs "$max_packages" '
  def short_string: type == "string" and length > 0 and length <= $max_len;
  def apt_name: short_string and test("^[a-z0-9][a-z0-9.+-]*$");
  def apt_version: short_string and test("^[A-Za-z0-9][A-Za-z0-9.:+~-]*$");
  def pkg_name: short_string and test("^(@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*$");
  def pkg_version: short_string and test("^[A-Za-z0-9][A-Za-z0-9.+_-]*$");
  def marketplace_url: short_string and test("^https://github\\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\\.git$") and (contains("..") | not);
  def marketplace_ref: short_string and test("^[0-9a-f]{40}$");
  def marketplace_path: short_string and (. == "." or (test("^[A-Za-z0-9][A-Za-z0-9._/-]*$") and (contains("..") | not)));
  def plugin_name: short_string and test("^[a-z0-9]([a-z0-9-]*[a-z0-9])?$");

  def valid_apt_entry:
    type == "object" and
    ((keys - ["name","version"]) == []) and
    (.name | apt_name) and
    ((has("version") | not) or (.version | apt_version));

  def valid_pkg_entry:
    type == "object" and
    ((keys | sort) == ["package","version"]) and
    (.package | pkg_name) and
    (.version | pkg_version);

  def valid_marketplace_entry:
    type == "object" and
    ((keys - ["url","ref","path","plugins"]) == []) and
    (.url | marketplace_url) and
    (.ref | marketplace_ref) and
    ((.path // ".") | marketplace_path) and
    ((.plugins // []) as $p |
      ($p | type == "array") and
      all($p[]; plugin_name) and
      (($p | length) == ($p | unique | length)));

  (type == "object") and
  ((keys - ["schema_version","apt","npm","python","claude_marketplaces"]) == []) and
  (.schema_version == 1) and
  ((.apt // []) as $apt | ($apt | type == "array") and all($apt[]; valid_apt_entry)) and
  ((.npm // []) as $npm | ($npm | type == "array") and all($npm[]; valid_pkg_entry)) and
  ((.python // {}) as $py |
    ($py | type == "object") and
    ((($py | keys) - ["enabled","packages"]) == []) and
    (($py.enabled // false) | type == "boolean") and
    (($py.packages // []) as $pp | ($pp | type == "array") and all($pp[]; valid_pkg_entry))) and
  ((.claude_marketplaces // []) as $mp | ($mp | type == "array") and all($mp[]; valid_marketplace_entry)) and
  ((((.apt // []) | length) + ((.npm // []) | length) + (((.python.packages) // []) | length)) <= $max_pkgs)
' "$profile_path" >/dev/null || die 'invalid session profile'

jq -Sc . "$profile_path"
```

Make it executable: `chmod +x scripts/session/validate-profile.sh`

- [ ] **Step 4: Run test to verify it passes**

Run: `bash scripts/session/tests/test-validate-profile.sh`
Expected: `ok` printed, exit 0. If any fixture fails, read the printed `FAIL` line, fix the corresponding jq predicate or fixture, and re-run.

- [ ] **Step 5: Add the new scripts to `scripts/verify`'s syntax-check loop**

In `scripts/verify`, change:

```bash
for file in scripts/build scripts/lint-dockerfiles scripts/load-msb scripts/verify images/claude/entrypoint.sh images/codex/entrypoint.sh scripts/claude/*.sh scripts/marketplaces/*.sh scripts/tools/*.sh; do bash -n "$file"; done
```

to:

```bash
for file in scripts/build scripts/lint-dockerfiles scripts/load-msb scripts/verify images/claude/entrypoint.sh images/codex/entrypoint.sh scripts/claude/*.sh scripts/marketplaces/*.sh scripts/tools/*.sh scripts/session/*.sh scripts/session/tests/*.sh; do bash -n "$file"; done
```

Run: `bash -n scripts/session/validate-profile.sh && bash -n scripts/session/tests/test-validate-profile.sh`
Expected: no output, exit 0.

- [ ] **Step 6: Commit**

```bash
git add scripts/session/validate-profile.sh scripts/session/tests/test-validate-profile.sh scripts/verify
git commit -m "feat: add session profile validator"
```

---

## Task 3: Safe Dockerfile renderer

**Files:**
- Create: `scripts/session/render-dockerfile.sh`
- Create: `scripts/session/tests/test-render-dockerfile.sh`

**Interfaces:**
- Consumes: nothing at call time from Tasks 1–2; only requires its caller to have already placed a `resolved.json` file in the target context directory (a file-based contract, checked at runtime).
- Produces: `scripts/session/render-dockerfile.sh CONTEXT_DIR` — writes `CONTEXT_DIR/Dockerfile`; exits 1 with a stderr message if `CONTEXT_DIR/resolved.json` is missing. Task 4 depends on this file being written after it stages `resolved.json`.

- [ ] **Step 1: Write the failing test**

Create `scripts/session/tests/test-render-dockerfile.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../../.."

context_dir=$(mktemp -d)
trap 'rm -rf "$context_dir"' EXIT

if scripts/session/render-dockerfile.sh "$context_dir" 2>/dev/null; then
  echo 'FAIL: should refuse a context dir with no resolved.json' >&2
  exit 1
fi

echo '{"ok":true}' >"$context_dir/resolved.json"
scripts/session/render-dockerfile.sh "$context_dir"

test -f "$context_dir/Dockerfile"
grep -qFx 'FROM ai-sandboxes-claude:local' "$context_dir/Dockerfile"
grep -qFx 'USER node' "$context_dir/Dockerfile"
test "$(find "$context_dir" -maxdepth 1 -type f | wc -l)" -eq 2

echo ok
```

Make it executable: `chmod +x scripts/session/tests/test-render-dockerfile.sh`

- [ ] **Step 2: Run test to verify it fails**

Run: `bash scripts/session/tests/test-render-dockerfile.sh`
Expected: FAIL — `scripts/session/render-dockerfile.sh: No such file or directory`

- [ ] **Step 3: Write the renderer**

Create `scripts/session/render-dockerfile.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

context_dir=${1:?usage: render-dockerfile.sh CONTEXT_DIR}

test -f "$context_dir/resolved.json" || {
  echo 'render-dockerfile: missing resolved.json in context' >&2
  exit 1
}

cat >"$context_dir/Dockerfile" <<'EOF'
# syntax=docker/dockerfile:1.7
FROM ai-sandboxes-claude:local
USER root
COPY --chown=root:root --chmod=0444 resolved.json /opt/session-profile/resolved.json
USER node
EOF
```

Make it executable: `chmod +x scripts/session/render-dockerfile.sh`

Note: `resolved.json` is written as a plain file, never interpolated into the Dockerfile text or a shell command — this is what keeps the renderer safe from injection even once later tasks add apt/npm/python layers on top of it.

- [ ] **Step 4: Run test to verify it passes**

Run: `bash scripts/session/tests/test-render-dockerfile.sh`
Expected: `ok` printed, exit 0.

- [ ] **Step 5: Add to `scripts/verify`'s syntax-check loop**

Already covered by the `scripts/session/*.sh` and `scripts/session/tests/*.sh` globs added in Task 2, Step 5. No further edit needed.

Run: `bash -n scripts/session/render-dockerfile.sh && bash -n scripts/session/tests/test-render-dockerfile.sh`
Expected: no output, exit 0.

- [ ] **Step 6: Commit**

```bash
git add scripts/session/render-dockerfile.sh scripts/session/tests/test-render-dockerfile.sh
git commit -m "feat: add safe session Dockerfile renderer"
```

---

## Task 4: Session image resolver and cache key

**Files:**
- Create: `scripts/session/resolve-image.sh`
- Create: `scripts/session/tests/test-resolve-image.sh`

**Interfaces:**
- Consumes: `scripts/session/validate-profile.sh PROFILE_PATH` (Task 2: stdout canonical JSON, exit 0/2) and `scripts/session/render-dockerfile.sh CONTEXT_DIR` (Task 3).
- Produces: `scripts/session/resolve-image.sh PROFILE_PATH` — prints the resolved image tag (e.g. `ai-sandboxes-claude-session:sha-<64 hex chars>`) to stdout on success, exit 0; prints `resolve-image: ...` to stderr and exits 1 on failure. Reads `CLAUDE_MSB_BUILD_EGRESS` (only consulted on a cache miss). Task 6 depends on this exact stdout/exit contract.

This task requires Docker and a locally-built `ai-sandboxes-claude:local` (run `./scripts/build` first if it is missing).

- [ ] **Step 1: Write the failing test**

Create `scripts/session/tests/test-resolve-image.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../../.."

if ! docker image inspect ai-sandboxes-claude:local >/dev/null 2>&1; then
  echo 'skip: ai-sandboxes-claude:local not built (run ./scripts/build)' >&2
  exit 0
fi

cleanup() {
  test -n "${tag_empty:-}" && docker image rm -f "$tag_empty" >/dev/null 2>&1 || true
}
trap cleanup EXIT

if scripts/session/resolve-image.sh scripts/session/fixtures/valid/full.json >/dev/null 2>/tmp/resolve-image-stderr.$$; then
  echo 'FAIL: cache miss should require CLAUDE_MSB_BUILD_EGRESS=1' >&2
  exit 1
fi
grep -q CLAUDE_MSB_BUILD_EGRESS /tmp/resolve-image-stderr.$$
rm -f /tmp/resolve-image-stderr.$$

tag_empty=$(CLAUDE_MSB_BUILD_EGRESS=1 scripts/session/resolve-image.sh scripts/session/fixtures/valid/empty.json)
docker image inspect "$tag_empty" >/dev/null

tag_empty_again=$(scripts/session/resolve-image.sh scripts/session/fixtures/valid/empty.json)
test "$tag_empty" = "$tag_empty_again"

echo ok
```

Make it executable: `chmod +x scripts/session/tests/test-resolve-image.sh`

- [ ] **Step 2: Run test to verify it fails**

Run: `bash scripts/session/tests/test-resolve-image.sh`
Expected: FAIL — `scripts/session/resolve-image.sh: No such file or directory` (or `skip:` if `ai-sandboxes-claude:local` is not built yet — build it first with `./scripts/build`, then re-run).

- [ ] **Step 3: Write the resolver**

Create `scripts/session/resolve-image.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$repo_root"

profile_path=${1:?usage: resolve-image.sh PROFILE_PATH}
platform=linux/arm64
schema_version=1
launcher_version=1
base_image=ai-sandboxes-claude:local

die() {
  printf 'resolve-image: %s\n' "$*" >&2
  exit 1
}

canonical=$(scripts/session/validate-profile.sh "$profile_path") || exit 1

base_digest=$(docker image inspect --format '{{.Id}}' "$base_image" 2>/dev/null) \
  || die "base image not found: $base_image (run ./scripts/build first)"

cache_key=$(printf '%s\n%s\n%s\n%s\n%s\n' "$base_digest" "$canonical" "$platform" "$schema_version" "$launcher_version" \
  | shasum -a 256 | awk '{print $1}')
tag="ai-sandboxes-claude-session:sha-$cache_key"

if docker image inspect "$tag" >/dev/null 2>&1; then
  printf '%s\n' "$tag"
  exit 0
fi

lock_dir="${TMPDIR:-/tmp}/ai-sandboxes-session-lock-$cache_key"
attempt=0
until mkdir "$lock_dir" 2>/dev/null; do
  attempt=$((attempt + 1))
  test "$attempt" -lt 600 || die 'timed out waiting for another build of this session image'
  sleep 0.5
done
trap 'rmdir "$lock_dir" 2>/dev/null || true' EXIT

# Re-check now that the lock is held: another process may have finished
# building this exact key while we were waiting.
if docker image inspect "$tag" >/dev/null 2>&1; then
  printf '%s\n' "$tag"
  exit 0
fi

test "${CLAUDE_MSB_BUILD_EGRESS:-}" = 1 \
  || die 'cache miss requires CLAUDE_MSB_BUILD_EGRESS=1 to build (see docs/session-images.md)'

context_dir=$(mktemp -d)
trap 'rmdir "$lock_dir" 2>/dev/null || true; rm -rf "$context_dir"' EXIT

built_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
jq -n \
  --argjson request "$canonical" \
  --arg base_image "$base_image" \
  --arg base_digest "$base_digest" \
  --arg platform "$platform" \
  --argjson schema_version "$schema_version" \
  --argjson launcher_version "$launcher_version" \
  --arg built_at "$built_at" \
  --arg cache_key "$cache_key" \
  '{
    canonical_request: $request,
    base_image: $base_image,
    base_digest: $base_digest,
    platform: $platform,
    schema_version: $schema_version,
    launcher_version: $launcher_version,
    packages: {
      apt: ($request.apt // []),
      npm: ($request.npm // []),
      python: (($request.python // {}).packages // [])
    },
    claude_marketplaces: ($request.claude_marketplaces // []),
    built_at: $built_at,
    cache_key: $cache_key
  }' >"$context_dir/resolved.json"

scripts/session/render-dockerfile.sh "$context_dir"

docker build \
  --platform "$platform" \
  --tag "$tag" \
  --label io.ai-sandboxes.session-image=1 \
  --label io.ai-sandboxes.session-cache-key="$cache_key" \
  "$context_dir" >&2

printf '%s\n' "$tag"
```

Make it executable: `chmod +x scripts/session/resolve-image.sh`

- [ ] **Step 4: Run test to verify it passes**

Run: `bash scripts/session/tests/test-resolve-image.sh`
Expected: `ok` printed, exit 0. The first `resolve-image.sh` call performs a real `docker build` (a few seconds); the second is a cache hit with no build output.

- [ ] **Step 5: Syntax check**

Run: `bash -n scripts/session/resolve-image.sh && bash -n scripts/session/tests/test-resolve-image.sh`
Expected: no output, exit 0.

- [ ] **Step 6: Commit**

```bash
git add scripts/session/resolve-image.sh scripts/session/tests/test-resolve-image.sh
git commit -m "feat: add session image resolver with content-addressed cache key"
```

---

## Task 5: Exact msb image loader

**Files:**
- Create: `scripts/lib/msb-image.sh`
- Create: `scripts/session/load-image.sh`
- Create: `scripts/session/tests/test-load-image.sh`
- Modify: `scripts/load-msb`
- Modify: `scripts/verify` (add `scripts/lib/*.sh` to the syntax-check loop)

**Interfaces:**
- Produces: `msb_image_present TAG` (function; returns 0 if `TAG` is present in `msb image list`, 1 otherwise) and `msb_load_image DOCKER_REF MSB_REF` (function; `docker save`s `DOCKER_REF` and `msb load --tag`s it as `MSB_REF`), both in `scripts/lib/msb-image.sh`. `scripts/session/load-image.sh TAG` — loads `TAG` into msb, skipping if already present; never calls `msb image remove`.

This task is independent of Tasks 1–4 and can be done in any order relative to them, but Task 6 needs it.

- [ ] **Step 1: Write the failing test**

Create `scripts/session/tests/test-load-image.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../../.."

if ! command -v msb >/dev/null 2>&1; then
  echo 'skip: msb not installed' >&2
  exit 0
fi

test_tag="ai-sandboxes-session-loader-test:local"
cleanup() {
  docker image rm -f "$test_tag" >/dev/null 2>&1 || true
  msb image remove "$test_tag" >/dev/null 2>&1 || true
}
trap cleanup EXIT

build_dir=$(mktemp -d)
printf 'FROM scratch\nCOPY resolved.json /resolved.json\n' >"$build_dir/Dockerfile"
echo '{}' >"$build_dir/resolved.json"
docker build --tag "$test_tag" "$build_dir" >/dev/null
rm -rf "$build_dir"

claude_present_before=false
msb image list --quiet | grep -Fxq ai-sandboxes-claude:local && claude_present_before=true

scripts/session/load-image.sh "$test_tag"
msb image list --quiet | grep -Fxq "$test_tag"

# Second call must skip (already present) rather than remove-and-reload.
scripts/session/load-image.sh "$test_tag"
msb image list --quiet | grep -Fxq "$test_tag"

claude_present_after=false
msb image list --quiet | grep -Fxq ai-sandboxes-claude:local && claude_present_after=true
test "$claude_present_before" = "$claude_present_after"

echo ok
```

Make it executable: `chmod +x scripts/session/tests/test-load-image.sh`

- [ ] **Step 2: Run test to verify it fails**

Run: `bash scripts/session/tests/test-load-image.sh`
Expected: FAIL — `scripts/session/load-image.sh: No such file or directory` (or `skip:` if `msb` is not installed in this environment).

- [ ] **Step 3: Extract the shared library and write the loader**

Create `scripts/lib/msb-image.sh`:

```bash
#!/usr/bin/env bash

msb_image_present() {
  local tag=$1
  msb image list --quiet | grep -Fxq "$tag"
}

msb_load_image() {
  local docker_ref=$1 msb_ref=$2
  docker save "$docker_ref" | msb load --tag "$msb_ref"
}
```

Modify `scripts/load-msb` to source it instead of redefining the logic inline:

```bash
#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."
source scripts/lib/msb-image.sh

load_image() {
  local docker_ref=$1 msb_ref=$2
  if msb_image_present "$msb_ref"; then
    msb image remove "$msb_ref"
  fi
  msb_load_image "$docker_ref" "$msb_ref"
}

load_image ai-sandboxes-claude:local ai-sandboxes-claude:local
load_image ai-sandboxes-codex:local ai-sandboxes-codex:local
```

This preserves `scripts/load-msb`'s exact existing behavior (always remove-then-reload for the mutable `:local` tags) while sharing the two primitives with the new loader.

Create `scripts/session/load-image.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
source scripts/lib/msb-image.sh

tag=${1:?usage: load-image.sh TAG}

if msb_image_present "$tag"; then
  exit 0
fi

msb_load_image "$tag" "$tag"
```

Make it executable: `chmod +x scripts/session/load-image.sh`

- [ ] **Step 4: Run test to verify it passes**

Run: `bash scripts/session/tests/test-load-image.sh`
Expected: `ok` printed, exit 0.

- [ ] **Step 5: Confirm `scripts/load-msb` still behaves as before**

Run: `bash -n scripts/load-msb && bash -n scripts/lib/msb-image.sh`
Expected: no output, exit 0.
If `msb` is installed, also run: `./scripts/load-msb` — expected: exits 0, `msb image list --quiet` still contains `ai-sandboxes-claude:local` and `ai-sandboxes-codex:local`.

- [ ] **Step 6: Add `scripts/lib/*.sh` to `scripts/verify`'s syntax-check loop**

In `scripts/verify`, extend the same loop again:

```bash
for file in scripts/build scripts/lint-dockerfiles scripts/load-msb scripts/verify images/claude/entrypoint.sh images/codex/entrypoint.sh scripts/claude/*.sh scripts/marketplaces/*.sh scripts/tools/*.sh scripts/session/*.sh scripts/session/tests/*.sh scripts/lib/*.sh; do bash -n "$file"; done
```

- [ ] **Step 7: Commit**

```bash
git add scripts/lib/msb-image.sh scripts/session/load-image.sh scripts/session/tests/test-load-image.sh scripts/load-msb scripts/verify
git commit -m "feat: extract reusable msb image load logic and add session image loader"
```

---

## Task 6: `claude-session` Fish launcher

**Files:**
- Modify: `shell/fish/lib/ai-sandbox.fish` (add `__ai_sandbox_run_claude`)
- Modify: `shell/fish/claude.fish` (reduce to call the shared function)
- Create: `shell/fish/claude-session.fish`
- Create: `scripts/session/tests/test-claude-session-args.sh`

**Interfaces:**
- Consumes: `scripts/session/resolve-image.sh PROFILE_PATH` (Task 4: stdout tag, exit 0/1) and `scripts/session/load-image.sh TAG` (Task 5).
- Produces: `__ai_sandbox_run_claude --argument-names image` in `shell/fish/lib/ai-sandbox.fish` (runs the existing hardened Claude `msb run` policy against whatever `image` tag it's given; remaining `$argv` is passed through to `claude` inside the guest). `claude-session` function in `shell/fish/claude-session.fish`.

- [ ] **Step 1: Write the failing test**

Create `scripts/session/tests/test-claude-session-args.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../../.."

if ! command -v fish >/dev/null 2>&1; then
  echo 'skip: fish not installed' >&2
  exit 0
fi

output=$(fish -c 'source shell/fish/claude-session.fish; claude-session' 2>&1) && status=0 || status=$?
test "$status" -eq 2
printf '%s\n' "$output" | grep -q -- '--profile'

output=$(fish -c 'source shell/fish/claude-session.fish; claude-session --profile /no/such/profile.json' 2>&1) && status=0 || status=$?
test "$status" -eq 1
printf '%s\n' "$output" | grep -q 'profile not found'

echo ok
```

Make it executable: `chmod +x scripts/session/tests/test-claude-session-args.sh`

- [ ] **Step 2: Run test to verify it fails**

Run: `bash scripts/session/tests/test-claude-session-args.sh`
Expected: FAIL — fish reports `Unknown command: claude-session` (or `skip:` if fish is not installed in this environment).

- [ ] **Step 3: Extract the shared Claude run function**

In `shell/fish/lib/ai-sandbox.fish`, add (this is `claude.fish`'s current body, verbatim, with the hardcoded image tag replaced by a parameter):

```fish
function __ai_sandbox_run_claude --argument-names image
    set -l profile_volume 'claude-home-hardened'
    set -l egress_file "$HOME/.config/microvms/claude-egress"
    set -l workspace_quota '10G'
    set -l root_disk '10G'
    # Let Microsandbox's gateway DNS follow the host resolver.  An external
    # resolver is not reachable through every public-network gateway.
    set -l network_args \
        --no-net \
        --net-rule 'allow@host:udp:53' \
        --net-rule 'allow@host:tcp:53'

    if not type -q msb
        echo 'claude: msb is not installed or is not on PATH' >&2
        return 127
    end

    if set -q CLAUDE_MSB_PUBLIC_EGRESS; and test "$CLAUDE_MSB_PUBLIC_EGRESS" = 1
        set network_args --net public
    else
        if not test -f "$egress_file"
            echo "claude: missing egress allowlist: $egress_file" >&2
            echo 'claude: copy config/claude-egress.example there and review its hosts' >&2
            return 1
        end

        while read -l egress_host
            set egress_host (string trim -- "$egress_host")
            if test -z "$egress_host"; or string match -q '#*' -- "$egress_host"
                continue
            end

            # The allowlist contains hostnames only: one HTTPS destination per line.
            if not string match -rq '^(\*\.)?[A-Za-z0-9][A-Za-z0-9.-]*$' -- "$egress_host"
                echo "claude: invalid hostname in $egress_file: $egress_host" >&2
                return 1
            end
            set -a network_args --net-rule "allow@$egress_host:tcp:443"
        end < "$egress_file"
    end

    set -l shared_state_args (__ai_sandbox_prepare_shared_state claude "$image"); or return $status

    set -l host_workspace (command git rev-parse --show-toplevel 2>/dev/null)
    if test $status -ne 0
        set host_workspace (pwd -P)
    end
    set host_workspace (realpath "$host_workspace")

    set -l home_path (realpath "$HOME")
    if test -z "$host_workspace"; or test "$host_workspace" = /; or test "$host_workspace" = "$home_path"
        echo 'claude: refusing to mount an empty path, /, or the complete home directory' >&2
        return 2
    end

    set -l project_name (basename "$host_workspace" | string replace --all --regex '[^A-Za-z0-9._-]' '-')
    set -l project_hash (printf '%s' "$host_workspace" | git hash-object --stdin | string sub --length 12)
    set -l guest_workspace "/workspace/$project_name-$project_hash"

    command msb run \
        --tty \
        --pull never \
        --user node \
        --cpus 4 \
        --memory 8G \
        --root-disk "$root_disk" \
        --security restricted \
        $network_args \
        --mount-dir "$host_workspace:$guest_workspace:rw,quota=$workspace_quota" \
        --mount-named "$profile_volume:/home/node:kind=dir,quota=4G" \
        $shared_state_args \
        --workdir "$guest_workspace" \
        "$image" \
        -- env \
            CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 \
            ENABLE_CLAUDEAI_MCP_SERVERS=false \
            claude $argv
end
```

Append this function to the end of `shell/fish/lib/ai-sandbox.fish` (after the existing `__ai_sandbox_launch` function).

- [ ] **Step 4: Reduce `claude.fish` to call the shared function**

Replace the entire contents of `shell/fish/claude.fish` with:

```fish
source (dirname (realpath (status filename)))/lib/ai-sandbox.fish

function claude --description 'Run Claude Code in a hardened Microsandbox VM'
    __ai_sandbox_run_claude ai-sandboxes-claude:local $argv
end
```

- [ ] **Step 5: Write `claude-session.fish`**

Create `shell/fish/claude-session.fish`:

```fish
source (dirname (realpath (status filename)))/lib/ai-sandbox.fish

function claude-session --description 'Run Claude Code in a session image built from an explicit profile'
    if test (count $argv) -lt 2; or test "$argv[1]" != --profile
        echo 'claude-session: usage: claude-session --profile PATH_OR_NAME [claude arguments...]' >&2
        return 2
    end

    set -l repo_root (dirname (dirname (dirname (realpath (status filename)))))
    set -l profile_value $argv[2]
    set -l claude_args $argv[3..-1]

    set -l profile_path $profile_value
    if not string match -q '*/*' -- "$profile_value"
        set profile_path "$HOME/.config/microvms/profiles/$profile_value.json"
    end

    if not test -f "$profile_path"
        echo "claude-session: profile not found: $profile_path" >&2
        return 1
    end

    if not type -q msb
        echo 'claude-session: msb is not installed or is not on PATH' >&2
        return 127
    end

    set -l resolved_image ("$repo_root/scripts/session/resolve-image.sh" "$profile_path"); or return $status
    "$repo_root/scripts/session/load-image.sh" "$resolved_image"; or return $status

    __ai_sandbox_run_claude "$resolved_image" $claude_args
end
```

- [ ] **Step 6: Run test to verify it passes**

Run: `bash scripts/session/tests/test-claude-session-args.sh`
Expected: `ok` printed, exit 0.

Also run the existing Fish syntax check the same way `scripts/verify` does:
Run: `find shell/fish -type f -name '*.fish' -exec fish --no-execute {} +`
Expected: no output, exit 0.

- [ ] **Step 7: Manually confirm `claude` still behaves identically**

If `msb` and the egress allowlist are set up (see `docs/claude-security.md`), run: `fish -c 'source shell/fish/claude.fish; claude --version'` from a Git checkout.
Expected: prints the Claude Code version, exactly as before this change (this call now goes through `__ai_sandbox_run_claude` but with identical arguments to the pre-refactor inline body).

- [ ] **Step 8: Commit**

```bash
git add shell/fish/lib/ai-sandbox.fish shell/fish/claude.fish shell/fish/claude-session.fish scripts/session/tests/test-claude-session-args.sh
git commit -m "feat: add claude-session launcher, extract shared Claude run policy"
```

---

## Task 7: Empty-profile end-to-end verification

**Files:**
- Modify: `scripts/verify` (append session-image assertions inside the existing `if command -v msb` block)

**Interfaces:**
- Consumes: `config/session-profile.example.json` (Task 1), `scripts/session/resolve-image.sh` (Task 4), `scripts/session/load-image.sh` (Task 5).
- Produces: nothing new — this is the terminal check for the vertical slice, run via `./scripts/verify`.

- [ ] **Step 1: Write the failing verification block**

In `scripts/verify`, find the existing block:

```bash
if command -v msb >/dev/null 2>&1; then
  ./scripts/load-msb
  msb run --pull never --no-tty --user node -e HOME=/home/node ai-sandboxes-claude:local -- claude --version
  msb run --pull never --no-tty --user node -e HOME=/home/node ai-sandboxes-codex:local -- codex --version
  msb volume remove verify-home 2>/dev/null || true
  msb volume create verify-home
  # shellcheck disable=SC2016 # $HOME expands in the guest shell, not on the host.
  msb run --pull never --no-tty --user node --mount-named verify-home:/home/node:rw ai-sandboxes-codex:local -- bash -lc 'touch "$HOME/.survives"'
  msb run --pull never --no-tty --user node --mount-named verify-home:/home/node:rw ai-sandboxes-codex:local -- test -f /home/node/.survives
  msb volume remove verify-home
fi
```

Append, just before the closing `fi`:

```bash
  session_tag_first=$(CLAUDE_MSB_BUILD_EGRESS=1 ./scripts/session/resolve-image.sh config/session-profile.example.json)
  session_tag_second=$(./scripts/session/resolve-image.sh config/session-profile.example.json)
  test "$session_tag_first" = "$session_tag_second"

  ./scripts/session/load-image.sh "$session_tag_first"
  msb image list --quiet | grep -Fxq "$session_tag_first"
  msb image list --quiet | grep -Fxq ai-sandboxes-claude:local

  msb run --pull never --no-tty --user node --security restricted "$session_tag_first" -- whoami | grep -Fxq node
  msb run --pull never --no-tty --user node --security restricted "$session_tag_first" -- sh -c '! command -v sudo'
  msb run --pull never --no-tty --user node --security restricted "$session_tag_first" -- sh -c '! touch /opt/session-profile/resolved.json 2>/dev/null'

  docker image rm "$session_tag_first" >/dev/null 2>&1 || true
  msb image remove "$session_tag_first" >/dev/null 2>&1 || true
```

- [ ] **Step 2: Run to verify it fails on a clean checkout without Tasks 1–6**

(Only applicable if you are validating this task in isolation without the prior tasks landed.) Run: `./scripts/verify`
Expected: FAIL at `./scripts/session/resolve-image.sh: No such file or directory` if Tasks 1–6 are missing. If Tasks 1–6 are already in place (the normal case when working through this plan in order), skip to Step 3.

- [ ] **Step 3: Run the full verification**

Run: `./scripts/build && ./scripts/verify`
Expected: the script runs to completion with exit 0. The new block specifically confirms:
- `resolve-image.sh` is idempotent (same tag on cache hit as on the initial build).
- the session tag and `ai-sandboxes-claude:local` both remain present in `msb image list` afterward.
- the session image runs as `node`, has no `sudo`, and cannot write to `/opt/session-profile/resolved.json`.

- [ ] **Step 4: Commit**

```bash
git add scripts/verify
git commit -m "test: verify empty-profile session image end-to-end"
```

---

## Done Criteria for This Slice

`./scripts/build && ./scripts/verify` passes with `msb` installed, and:

```fish
claude-session --profile config/session-profile.example.json --version
```

run from a Git checkout prints the Claude Code version, using a distinct `ai-sandboxes-claude-session:sha-<hash>` msb image tag that leaves `ai-sandboxes-claude:local` untouched.

**Verification status as of this branch:** no docker build, msb load, or fish
execution path has actually been run. Only `bash -n` syntax checks and the
pure-bash/jq tests covering Tasks 1–3's logic (profile validation and
Dockerfile rendering) have been verified in the environment this branch was
developed in, which has no Docker, `msb`, or `fish` available. Before this
slice is trusted, `./scripts/build && ./scripts/verify` must be run to
completion on a host with Docker Desktop and `msb` installed, and a real
`claude` and `claude-session` launch must be exercised on that host —
checking that arguments actually reach the guest `claude` command correctly
(e.g. a multi-word prompt or flag combination), not just a bare `--version`
call, since argument passing through the Fish layer is exactly the kind of
thing that can look right in review and still be wrong at runtime.

Follow-on plans (not in this slice): apt/npm layers (spec task 8), Python layer (task 9), Claude marketplace/plugin overlay with dual-seed merge (task 10), image GC (task 11), and retiring `docs/private-profiles.md` plus README/configuration doc updates (task 12).
