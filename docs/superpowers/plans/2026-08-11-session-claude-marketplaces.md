# Session Claude marketplace/plugin overlay Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a session profile's `claude_marketplaces` field actually install and enable plugins in the derived session image (Implementation task 10 from `docs/session-images.md`), instead of being rejected by validation.

**Architecture:** `validate-profile.sh` stops rejecting `claude_marketplaces`. `render-dockerfile.sh` gains a third argument (the canonical profile JSON) and conditionally emits a multi-stage Dockerfile that reuses `scripts/marketplaces/install-claude.sh` verbatim to install into a second, session-specific plugin cache/seed path. `resolve-image.sh` passes the profile through and extends its cache-key hash to cover the installer script too. `images/claude/entrypoint.sh` generalizes its seed merge to combine the base seed and an optional session seed via a recursive jq merge, with the user's persisted settings always winning.

**Tech Stack:** Bash (`set -euo pipefail`), `jq`, Docker BuildKit (`docker buildx build`), Microsandbox (`msb`). No new dependencies.

## Global Constraints

- `apt`, `npm`, and `python` session profile fields remain rejected — out of scope for this plan (tasks 8/9 in `docs/session-images.md`).
- `scripts/marketplaces/install-claude.sh` must not be modified — it is already fully parametrized via `CLAUDE_CODE_PLUGIN_CACHE_DIR` and a marketplaces-JSON path argument.
- Session builds must never reference the project checkout directly from a generated Dockerfile; only files copied into the context dir may be referenced.
- Session builds must never retag, rebuild, or otherwise disturb the shared `ai-sandboxes-claude:local` tag used elsewhere in `scripts/verify`.
- Every new/modified shell script keeps this repo's existing style: `#!/usr/bin/env bash`, `set -euo pipefail`, a local `die()` helper that prints `<component>: message` to stderr and exits non-zero.
- Every docker/msb-dependent test must skip cleanly (printing `skip: ...` to stderr and exiting 0) when `docker`/`msb`/the required image isn't available, matching the existing pattern in `scripts/session/tests/test-resolve-image.sh` and `test-load-image.sh`.

---

### Task 1: Accept `claude_marketplaces` in profile validation

**Files:**
- Modify: `scripts/session/validate-profile.sh`
- Create: `scripts/session/fixtures/valid/claude-marketplaces.json`
- Test: `scripts/session/tests/test-validate-profile.sh` (existing — iterates `fixtures/valid/*.json` and `fixtures/invalid/*.json` generically, no code changes needed)

**Interfaces:**
- Produces: a profile with a non-empty `claude_marketplaces` field and empty/absent `apt`/`npm`/`python` now validates successfully; `apt`/`npm`/`python` non-empty still fails with the existing rejection message (adjusted wording).

- [ ] **Step 1: Add the new valid fixture**

Create `scripts/session/fixtures/valid/claude-marketplaces.json`:

```json
{
  "schema_version": 1,
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

This reuses the exact marketplace/plugin already proven to work in `scripts/verify`'s own base-image marketplace runtime test (`config/marketplaces.runtime-test.json`), so later tasks that build a real image from it are exercising a known-good marketplace.

- [ ] **Step 2: Run the validator test to confirm the new fixture currently fails**

Run: `bash scripts/session/tests/test-validate-profile.sh`
Expected: `FAIL (should be valid): scripts/session/fixtures/valid/claude-marketplaces.json` printed to stderr, non-zero exit — because `validate-profile.sh` still rejects any non-empty `claude_marketplaces`.

- [ ] **Step 3: Update the rejection check in `validate-profile.sh`**

In `scripts/session/validate-profile.sh`, find this block:

```bash
# The renderer currently emits a fixed Dockerfile with no apt/npm/Python/
# marketplace layers (empty-profile vertical slice only), so a profile
# requesting any of them would validate but be silently dropped from the
# built image. Reject them explicitly until those layers are implemented.
jq -e '
  ((.apt // []) | length) == 0 and
  ((.npm // []) | length) == 0 and
  (((.python // {}).enabled // false) == false) and
  (((.python // {}).packages // []) | length) == 0 and
  ((.claude_marketplaces // []) | length) == 0
' "$snapshot" >/dev/null 2>&1 \
  || die 'apt, npm, python, and claude_marketplaces are not yet supported; only an empty profile is accepted (see docs/session-images.md)'
```

Replace it with:

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

- [ ] **Step 4: Run the test to verify it passes**

Run: `bash scripts/session/tests/test-validate-profile.sh`
Expected: `ok` printed, exit 0. This also re-confirms every existing fixture (including `scripts/session/fixtures/invalid/non-empty-packages.json`, which has `apt`/`npm`/`python` populated) still validates/rejects exactly as before.

- [ ] **Step 5: Commit**

```bash
git add scripts/session/validate-profile.sh scripts/session/fixtures/valid/claude-marketplaces.json
git commit -m "feat: accept claude_marketplaces in session profile validation"
```

---

### Task 2: Generalize the entrypoint's plugin-seed merge to a base+session merge

**Files:**
- Modify: `images/claude/entrypoint.sh`
- Test: `scripts/session/tests/test-claude-entrypoint-seed-merge.sh` (new)
- Modify: `scripts/verify` (wire in the new test)

**Interfaces:**
- Consumes: `HOME` (existing), `CLAUDE_CODE_PLUGIN_SEED_DIR` (now required — already set by both `images/claude/Dockerfile`'s build and final stages; see Task 3 for the new session equivalent), `CLAUDE_CODE_SESSION_PLUGIN_SEED_DIR` (new, optional — unset in images without a session marketplace overlay).
- Produces: `$HOME/.claude/settings.json` merged as `(base_seed * session_seed) * current_settings`, current settings winning per-key, unrelated keys untouched.

This task is testable entirely without Docker: `CLAUDE_CODE_PLUGIN_SEED_DIR` and `CLAUDE_CODE_SESSION_PLUGIN_SEED_DIR` can point at plain temp directories on the host, and `entrypoint.sh` itself can run directly (it only touches paths named by those env vars and `$HOME`).

- [ ] **Step 1: Write the failing test**

Create `scripts/session/tests/test-claude-entrypoint-seed-merge.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../../.."

fake_home=$(mktemp -d)
base_seed_dir=$(mktemp -d)
session_seed_dir=$(mktemp -d)
trap 'rm -rf "$fake_home" "$base_seed_dir" "$session_seed_dir"' EXIT

cat >"$base_seed_dir/settings.json" <<'JSON'
{"enabledPlugins":{"a@m1":true,"b@m1":true}}
JSON
cat >"$session_seed_dir/settings.json" <<'JSON'
{"enabledPlugins":{"c@m2":true}}
JSON

# Fresh home, both a base and a session seed: both must be enabled.
HOME="$fake_home" CLAUDE_CODE_PLUGIN_SEED_DIR="$base_seed_dir" \
  CLAUDE_CODE_SESSION_PLUGIN_SEED_DIR="$session_seed_dir" \
  images/claude/entrypoint.sh true

jq -e '.enabledPlugins["a@m1"] == true' "$fake_home/.claude/settings.json" >/dev/null
jq -e '.enabledPlugins["b@m1"] == true' "$fake_home/.claude/settings.json" >/dev/null
jq -e '.enabledPlugins["c@m2"] == true' "$fake_home/.claude/settings.json" >/dev/null

# Second launch: the user has since disabled a@m1 and set an unrelated key.
# Both must survive the merge; still-missing defaults must still be added.
jq -n '{enabledPlugins: {"a@m1": false}, theme: "dark"}' >"$fake_home/.claude/settings.json"

HOME="$fake_home" CLAUDE_CODE_PLUGIN_SEED_DIR="$base_seed_dir" \
  CLAUDE_CODE_SESSION_PLUGIN_SEED_DIR="$session_seed_dir" \
  images/claude/entrypoint.sh true

jq -e '.enabledPlugins["a@m1"] == false' "$fake_home/.claude/settings.json" >/dev/null
jq -e '.enabledPlugins["b@m1"] == true' "$fake_home/.claude/settings.json" >/dev/null
jq -e '.enabledPlugins["c@m2"] == true' "$fake_home/.claude/settings.json" >/dev/null
jq -e '.theme == "dark"' "$fake_home/.claude/settings.json" >/dev/null

# Base seed only (today's exact scenario: no session overlay at all).
fake_home2=$(mktemp -d)
trap 'rm -rf "$fake_home" "$base_seed_dir" "$session_seed_dir" "$fake_home2"' EXIT
HOME="$fake_home2" CLAUDE_CODE_PLUGIN_SEED_DIR="$base_seed_dir" images/claude/entrypoint.sh true
jq -e '.enabledPlugins["a@m1"] == true' "$fake_home2/.claude/settings.json" >/dev/null
jq -e '.enabledPlugins["c@m2"]? == null' "$fake_home2/.claude/settings.json" >/dev/null

# No base seed and no session seed at all: must no-op cleanly.
fake_home3=$(mktemp -d)
empty_seed_dir=$(mktemp -d)
trap 'rm -rf "$fake_home" "$base_seed_dir" "$session_seed_dir" "$fake_home2" "$fake_home3" "$empty_seed_dir"' EXIT
HOME="$fake_home3" CLAUDE_CODE_PLUGIN_SEED_DIR="$empty_seed_dir" images/claude/entrypoint.sh true
test ! -e "$fake_home3/.claude/settings.json"

echo ok
```

- [ ] **Step 2: Run it to verify it fails**

Run: `chmod +x scripts/session/tests/test-claude-entrypoint-seed-merge.sh && bash scripts/session/tests/test-claude-entrypoint-seed-merge.sh`
Expected: FAIL. Today's `entrypoint.sh` hardcodes `/opt/claude-plugin-seed/settings.json` (a container-internal path that doesn't exist on the test host) and never reads `CLAUDE_CODE_SESSION_PLUGIN_SEED_DIR`, so the `[[ -f "$seed" ]]` check is false, the whole merge block is skipped, and `$fake_home/.claude/settings.json` never gets created — the first `jq -e` assertion fails with a "No such file or directory" error from jq.

- [ ] **Step 3: Rewrite `images/claude/entrypoint.sh`**

Replace the full contents of `images/claude/entrypoint.sh` with:

```bash
#!/usr/bin/env bash
set -euo pipefail

die() {
  printf '%s\n' "claude-entrypoint: $*" >&2
  exit 1
}

# Plugin installations live in the immutable image cache, while Claude stores
# enablement in its user home. Merge the image's selected-plugin defaults
# into the persistent home on every launch: the base image's own seed, plus
# an optional session-image seed (see docs/session-images.md) with session
# values taking precedence over base values for the same key. The user's
# already-persisted settings take precedence over both defaults, and
# unrelated Claude settings are left untouched.
: "${HOME:?HOME must be set}"
: "${CLAUDE_CODE_PLUGIN_SEED_DIR:?CLAUDE_CODE_PLUGIN_SEED_DIR must be set}"
base_seed="$CLAUDE_CODE_PLUGIN_SEED_DIR/settings.json"
session_seed_dir=${CLAUDE_CODE_SESSION_PLUGIN_SEED_DIR:-}
settings="$HOME/.claude/settings.json"

have_base_seed=false
[[ -f "$base_seed" ]] && have_base_seed=true
have_session_seed=false
[[ -n "$session_seed_dir" && -f "$session_seed_dir/settings.json" ]] && have_session_seed=true

if [[ "$have_base_seed" == true || "$have_session_seed" == true ]]; then
  settings_dir=$(dirname "$settings") || die 'could not determine the Claude settings directory'
  mkdir -p "$settings_dir" || die "could not create $settings_dir"

  defaults=$(mktemp "${settings}.XXXXXX") || die "could not create a scratch defaults file"
  trap 'rm -f -- "$defaults"' EXIT
  if [[ "$have_base_seed" == true && "$have_session_seed" == true ]]; then
    jq -s '.[0] * .[1]' "$base_seed" "$session_seed_dir/settings.json" >"$defaults" \
      || die 'could not merge base and session plugin seeds'
  elif [[ "$have_session_seed" == true ]]; then
    cp -- "$session_seed_dir/settings.json" "$defaults" || die 'could not read the session plugin seed'
  else
    cp -- "$base_seed" "$defaults" || die 'could not read the base plugin seed'
  fi

  temporary=$(mktemp "${settings}.XXXXXX") || die "could not create temporary settings file"
  trap 'rm -f -- "$defaults" "$temporary"' EXIT

  if [[ ! -e "$settings" ]]; then
    cp -- "$defaults" "$temporary" || die "could not seed $settings"
    # Linking is atomic and fails if another process created the settings file
    # after the existence check. Do not overwrite that competing state.
    if ! ln -- "$temporary" "$settings"; then
      [[ -e "$settings" ]] || die "could not create $settings"
    fi
    rm -f -- "$temporary" || die "could not remove temporary settings file"
  else
    jq -s '.[1] * .[0]' "$settings" "$defaults" >"$temporary" \
      || die "could not merge plugin defaults into $settings"
    mv -f -- "$temporary" "$settings" || die "could not update $settings"
  fi
  trap - EXIT
fi

(( $# > 0 )) || die 'missing command'
exec "$@"
```

This code has already been validated locally (all four scenarios in the test above were run against it directly before writing this plan).

- [ ] **Step 4: Run the test to verify it passes**

Run: `bash scripts/session/tests/test-claude-entrypoint-seed-merge.sh`
Expected: `ok`, exit 0.

- [ ] **Step 5: Wire the test into `scripts/verify`**

In `scripts/verify`, find this line:

```bash
bash scripts/session/tests/test-install-fish-functions.sh
```

Add immediately after it:

```bash
bash scripts/session/tests/test-claude-entrypoint-seed-merge.sh
```

Also add `images/claude/entrypoint.sh` is already covered by the existing `bash -n` loop's explicit `images/claude/entrypoint.sh` entry — no change needed there.

- [ ] **Step 6: Run `scripts/verify`'s syntax and non-Docker test steps**

Run: `bash -n images/claude/entrypoint.sh && bash -n scripts/session/tests/test-claude-entrypoint-seed-merge.sh`
Expected: no output, exit 0 (both files syntactically valid).

- [ ] **Step 7: Commit**

```bash
git add images/claude/entrypoint.sh scripts/session/tests/test-claude-entrypoint-seed-merge.sh scripts/verify
git commit -m "feat: merge base and session Claude plugin seeds in the entrypoint"
```

---

### Task 3: Render a marketplace-install layer in the session Dockerfile

**Files:**
- Modify: `scripts/session/render-dockerfile.sh`
- Modify: `scripts/session/tests/test-render-dockerfile.sh`

**Interfaces:**
- Consumes: `scripts/marketplaces/install-claude.sh` (copied verbatim, unmodified).
- Produces: new signature `render-dockerfile.sh CONTEXT_DIR BASE_IMAGE_REF CANONICAL_PROFILE_JSON` (was `CONTEXT_DIR BASE_IMAGE_REF`). `CANONICAL_PROFILE_JSON` is a JSON *string* (not a file path). When `.claude_marketplaces` is empty, output is byte-identical to today's single-stage Dockerfile. When non-empty, also writes `session-marketplaces.json` and `install-claude-marketplaces.sh` into the context dir, and sets `ENV CLAUDE_CODE_SESSION_PLUGIN_SEED_DIR=/opt/claude-session/plugin-seed` (consumed by Task 2's entrypoint.sh) and `CLAUDE_CODE_SESSION_PLUGIN_CACHE_DIR=/opt/claude-session/plugin-cache` in the final stage.

This task is testable entirely without Docker — it only generates files and text, never invokes `docker`.

- [ ] **Step 1: Write the failing test**

Replace the full contents of `scripts/session/tests/test-render-dockerfile.sh` with:

```bash
#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../../.."

context_dir=$(mktemp -d)
marketplace_context_dir=$(mktemp -d)
trap 'rm -rf "$context_dir" "$marketplace_context_dir"' EXIT

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
grep -qFx 'FROM ai-sandboxes-claude-session-base:deadbeef AS build' "$marketplace_context_dir/Dockerfile"
grep -qF 'CLAUDE_CODE_PLUGIN_CACHE_DIR=/opt/claude-session/plugin-cache' "$marketplace_context_dir/Dockerfile"
grep -qF 'CLAUDE_CODE_SESSION_PLUGIN_SEED_DIR=/opt/claude-session/plugin-seed' "$marketplace_context_dir/Dockerfile"
grep -qF 'CLAUDE_CODE_SESSION_PLUGIN_CACHE_DIR=/opt/claude-session/plugin-cache' "$marketplace_context_dir/Dockerfile"
grep -qFx 'USER node' "$marketplace_context_dir/Dockerfile"
test "$(find "$marketplace_context_dir" -maxdepth 1 -type f | wc -l)" -eq 4
jq -e '.claude | length == 1' "$marketplace_context_dir/session-marketplaces.json" >/dev/null
jq -e '.claude[0].url == "https://github.com/rikdc/ai-skills.git"' "$marketplace_context_dir/session-marketplaces.json" >/dev/null
jq -e '.codex == []' "$marketplace_context_dir/session-marketplaces.json" >/dev/null
diff -q "$marketplace_context_dir/install-claude-marketplaces.sh" scripts/marketplaces/install-claude.sh

echo ok
```

- [ ] **Step 2: Run it to verify it fails**

Run: `bash scripts/session/tests/test-render-dockerfile.sh`
Expected: FAIL. Today's script only reads `$1`/`$2` and silently ignores a third argument, so both invocations in this test succeed and both produce the same plain single-stage Dockerfile — there's no marketplace-awareness yet. The first three assertions (against `$context_dir`, the empty-marketplaces case) pass, but `test -f "$marketplace_context_dir/session-marketplaces.json"` fails, since today's script never writes that file.

- [ ] **Step 3: Rewrite `scripts/session/render-dockerfile.sh`**

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

marketplaces=$(jq -c '.claude_marketplaces // []' <<<"$canonical_profile")
marketplace_count=$(jq 'length' <<<"$marketplaces")

if test "$marketplace_count" -eq 0; then
  # FROM references the caller's private, pre-verified pin of the base
  # image's exact content (see resolve-image.sh), not the mutable
  # ai-sandboxes-claude:local tag directly: a concurrent `./scripts/build`
  # could otherwise retag the base between when its digest was captured and
  # when BuildKit resolves this Dockerfile, silently building from different
  # content than the cache key claims. This must be a plain local tag, not a
  # name@digest reference: for a purely local (never pushed) image, BuildKit
  # resolves name@digest as a registry manifest lookup and fails, rather
  # than matching it against the local image store.
  cat >"$context_dir/Dockerfile" <<EOF
# syntax=docker/dockerfile:1.7
FROM $base_image_ref
USER root
COPY --chown=root:root --chmod=0444 resolved.json /opt/session-profile/resolved.json
USER node
EOF
  exit 0
fi

# Session marketplaces reuse the base image's own pinned installer
# (scripts/marketplaces/install-claude.sh, copied in verbatim below — this
# context dir must never reference the checkout path directly from the
# Dockerfile) but install into a second, session-specific cache/seed path
# rather than the base image's /opt/claude-plugin-cache and
# /opt/claude-plugin-seed, so the base image's own marketplace selection is
# untouched. Mirrors images/claude/Dockerfile's own build/final split:
# install in a discarded build stage with a throwaway HOME, then copy only
# the resulting cache/seed directories into the final image.
jq -n --argjson claude "$marketplaces" '{claude: $claude, codex: []}' \
  >"$context_dir/session-marketplaces.json"
cp -- "$repo_root/scripts/marketplaces/install-claude.sh" "$context_dir/install-claude-marketplaces.sh"

cat >"$context_dir/Dockerfile" <<EOF
# syntax=docker/dockerfile:1.7
FROM $base_image_ref AS build
USER root
COPY --chown=node:node session-marketplaces.json /opt/session-marketplaces.json
COPY --chown=node:node --chmod=0755 install-claude-marketplaces.sh /usr/local/lib/ai-sandboxes/install-session-claude-marketplaces.sh
RUN install -d -o node -g node -m 0755 /opt/claude-session/plugin-cache /opt/claude-session/plugin-seed /opt/claude-session-build-home /opt/claude-marketplaces
USER node
ENV HOME=/opt/claude-session-build-home CLAUDE_CODE_PLUGIN_CACHE_DIR=/opt/claude-session/plugin-cache CLAUDE_CODE_PLUGIN_SEED_DIR=/opt/claude-session/plugin-seed
RUN /usr/local/lib/ai-sandboxes/install-session-claude-marketplaces.sh /opt/session-marketplaces.json
USER root
RUN if test -f /opt/claude-session-build-home/.claude/settings.json; then \\
      install -D -o node -g node -m 0644 /opt/claude-session-build-home/.claude/settings.json /opt/claude-session/plugin-seed/settings.json; \\
    fi \\
 && chmod -R a-w /opt/claude-session/plugin-cache /opt/claude-session/plugin-seed
USER node
FROM $base_image_ref
USER root
COPY --from=build --chown=root:root /opt/claude-session/plugin-cache /opt/claude-session/plugin-cache
COPY --from=build --chown=root:root /opt/claude-session/plugin-seed /opt/claude-session/plugin-seed
ENV CLAUDE_CODE_SESSION_PLUGIN_SEED_DIR=/opt/claude-session/plugin-seed CLAUDE_CODE_SESSION_PLUGIN_CACHE_DIR=/opt/claude-session/plugin-cache
COPY --chown=root:root --chmod=0444 resolved.json /opt/session-profile/resolved.json
USER node
EOF
```

Note the doubled backslashes (`\\`) at the end of the two `RUN if ...` continuation lines: this is a bash unquoted-heredoc requirement, not a typo. An unquoted heredoc (needed here for `$base_image_ref` interpolation) treats a single trailing `\` before a newline as a line-continuation and strips both, collapsing the multi-line `RUN` into one line and losing the shell block structure Dockerfile needs. `\\` produces a literal single `\` followed by a real newline in the generated file, which is exactly the syntax Dockerfile's own RUN command expects for its multi-line form. This was verified directly before writing this plan:

```bash
$ printf 'x=hello\ncat <<EOF\nRUN foo \\\\\n && bar $x\nEOF\n' | bash
RUN foo \
 && bar hello
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `chmod +x scripts/session/render-dockerfile.sh && bash scripts/session/tests/test-render-dockerfile.sh`
Expected: `ok`, exit 0.

- [ ] **Step 5: Commit**

```bash
git add scripts/session/render-dockerfile.sh scripts/session/tests/test-render-dockerfile.sh
git commit -m "feat: render a marketplace-install layer for session Dockerfiles"
```

---

### Task 4: Wire resolve-image.sh to pass the profile through and extend the cache key

**Files:**
- Modify: `scripts/session/resolve-image.sh`

**Interfaces:**
- Consumes: `render-dockerfile.sh`'s new 3-argument signature from Task 3.
- Produces: no change to `resolve-image.sh`'s own external interface (still `resolve-image.sh PROFILE_PATH`); the cache key now also depends on `scripts/marketplaces/install-claude.sh`'s content.

- [ ] **Step 1: Update the `renderer_hash` computation**

In `scripts/session/resolve-image.sh`, find:

```bash
renderer_hash=$(shasum -a 256 scripts/session/render-dockerfile.sh | awk '{print $1}')
```

Replace with:

```bash
# A change to the marketplace installer should also bust the session-image
# cache, for the same reason the renderer itself is hashed: both are trusted
# inputs to what a cached tag's content actually is.
renderer_hash=$(cat scripts/session/render-dockerfile.sh scripts/marketplaces/install-claude.sh | shasum -a 256 | awk '{print $1}')
```

- [ ] **Step 2: Pass the canonical profile through to the renderer**

In `scripts/session/resolve-image.sh`, find:

```bash
scripts/session/render-dockerfile.sh "$context_dir" "$pinned_base" \
  || die "failed to render Dockerfile for context $context_dir"
```

Replace with:

```bash
scripts/session/render-dockerfile.sh "$context_dir" "$pinned_base" "$canonical" \
  || die "failed to render Dockerfile for context $context_dir"
```

- [ ] **Step 3: Run the syntax check and the existing resolve-image test**

Run: `bash -n scripts/session/resolve-image.sh && bash scripts/session/tests/test-resolve-image.sh`
Expected: no syntax errors. `test-resolve-image.sh` prints `skip: ai-sandboxes-claude:local not built (run ./scripts/build)` and exits 0 if Docker/the base image aren't available in this environment; if they are, it prints `ok`. Either outcome is a pass — this task doesn't change that test's behavior, it only changes what `resolve-image.sh` passes to a function `test-resolve-image.sh` doesn't touch directly.

- [ ] **Step 4: Commit**

```bash
git add scripts/session/resolve-image.sh
git commit -m "feat: pass the canonical profile to the renderer, hash the marketplace installer into the cache key"
```

---

### Task 5: End-to-end session marketplace test

**Files:**
- Create: `scripts/session/tests/test-session-marketplace.sh`
- Modify: `scripts/verify` (wire in the new test)

**Interfaces:**
- Consumes: `scripts/session/fixtures/valid/claude-marketplaces.json` (Task 1), `scripts/session/resolve-image.sh` (Task 4), `scripts/session/load-image.sh` (existing, unmodified).

This is the definitive end-to-end proof that the whole pipeline works, but it requires a real Docker daemon and `msb`, so it skips cleanly when either isn't available — this environment has neither, so it cannot be run here; it must be run in CI or a real dev host as part of executing this plan.

Scope note: this test verifies the *session* marketplace/plugin shows as enabled after a fresh-home launch. It does not additionally verify a *base*-image marketplace at the same time, because the shared `ai-sandboxes-claude:local` tag ships with an empty `config/marketplaces.json` by default in this repo, and this plan must not retag or rebuild that shared tag (see Global Constraints) to give it one just for this test. The "both base and session seeds merge with correct precedence" property is already proven independently, without Docker, by Task 2's `test-claude-entrypoint-seed-merge.sh`.

- [ ] **Step 1: Write the test**

Create `scripts/session/tests/test-session-marketplace.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../../.."

if ! command -v msb >/dev/null 2>&1; then
  echo 'skip: msb not installed' >&2
  exit 0
fi

if ! docker image inspect ai-sandboxes-claude:local >/dev/null 2>&1; then
  echo 'skip: ai-sandboxes-claude:local not built (run ./scripts/build)' >&2
  exit 0
fi

session_tag=''
cleanup() {
  if test -n "$session_tag"; then
    docker image rm -f "$session_tag" >/dev/null 2>&1 || true
    msb image remove "$session_tag" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

session_tag=$(CLAUDE_MSB_BUILD_EGRESS=1 scripts/session/resolve-image.sh scripts/session/fixtures/valid/claude-marketplaces.json)
scripts/session/load-image.sh "$session_tag"

plugin_output=$(msb run --pull never --no-tty --user node --security restricted -e HOME=/tmp/claude-session-marketplace-test "$session_tag" -- claude plugin list 2>&1)
printf '%s\n' "$plugin_output" | awk '
  /dev-skills@ai-skills/ { found = 1; next }
  found && /Status: ✔ enabled/ { enabled = 1; exit }
  END { exit !(found && enabled) }
'

echo ok
```

- [ ] **Step 2: Check syntax**

Run: `chmod +x scripts/session/tests/test-session-marketplace.sh && bash -n scripts/session/tests/test-session-marketplace.sh`
Expected: no output, exit 0.

- [ ] **Step 3: Wire the test into `scripts/verify`**

In `scripts/verify`, inside the `if command -v msb >/dev/null 2>&1; then ... fi` block, find:

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
fi
```

Add a call to the new test script immediately before the closing `fi`:

```bash
  docker image rm "$session_tag_first" >/dev/null 2>&1 || true
  msb image remove "$session_tag_first" >/dev/null 2>&1 || true

  bash scripts/session/tests/test-session-marketplace.sh
fi
```

- [ ] **Step 4: Run `scripts/verify`'s full non-Docker-dependent checks once more**

Run: `bash -n scripts/session/tests/test-session-marketplace.sh && bash -n scripts/verify`
Expected: no output, exit 0. The test itself cannot be executed to completion in this environment (no `msb`/Docker); it will run for real in CI's `verify` job (`.github/workflows/image-verify.yml`), which this repo's CI already confirmed has both available (prior runs on this same session-images work built and loaded session images successfully).

- [ ] **Step 5: Commit**

```bash
git add scripts/session/tests/test-session-marketplace.sh scripts/verify
git commit -m "test: verify session Claude marketplaces install and enable end-to-end"
```

---

### Task 6: Update documentation

**Files:**
- Modify: `docs/session-images.md`

**Interfaces:** none — documentation only.

- [ ] **Step 1: Update the "Session profile schema" section**

Find this paragraph (near the end of the "Session profile schema" section):

```markdown
The renderer in this vertical slice only ever emits the fixed, package-free
Dockerfile described under Implementation task 7; it does not yet install
`apt`, `npm`, `python`, or `claude_marketplaces` entries. Validation therefore
rejects any profile with a non-empty `apt`, `npm`, `python`, or
`claude_marketplaces` field rather than accepting and silently dropping it.
Only the empty profile (`{"schema_version": 1}`) validates until tasks 8-10
land.
```

Replace with:

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

- [ ] **Step 2: Update the "Claude marketplaces and plugins" section**

Find this paragraph:

```markdown
Session marketplaces reuse the existing pinned installer model, but install
into a second, session-specific cache and seed path rather than the base
image's `/opt/claude-plugin-cache` and `/opt/claude-plugin-seed`. The session
build sets a distinct `CLAUDE_CODE_SESSION_PLUGIN_SEED_DIR` (and matching
session plugin-cache variable) to that path; the base image's own
`CLAUDE_CODE_PLUGIN_SEED_DIR` is untouched, so both seeds are present in the
final image.
```

Replace with:

```markdown
Session marketplaces reuse the existing pinned installer
(`scripts/marketplaces/install-claude.sh`, copied into the build context
unmodified) but install into a second, session-specific cache and seed path,
`/opt/claude-session/plugin-cache` and `/opt/claude-session/plugin-seed`,
rather than the base image's `/opt/claude-plugin-cache` and
`/opt/claude-plugin-seed`. The session build sets
`CLAUDE_CODE_SESSION_PLUGIN_SEED_DIR` and
`CLAUDE_CODE_SESSION_PLUGIN_CACHE_DIR` to those paths; the base image's own
`CLAUDE_CODE_PLUGIN_SEED_DIR`/`CLAUDE_CODE_PLUGIN_CACHE_DIR` are untouched, so
both seeds are present in the final image. The install runs in a discarded
build stage with a throwaway `HOME`, mirroring `images/claude/Dockerfile`'s
own build/final split; only the resulting cache and seed directories are
copied into the final image, root-owned and read-only.
```

Find the following paragraph:

```markdown
`images/claude/entrypoint.sh` currently hardcodes `/opt/claude-plugin-seed`
instead of honoring the `CLAUDE_CODE_PLUGIN_SEED_DIR` the Dockerfile already
exports. It must change to merge every seed it finds — the base seed at
`CLAUDE_CODE_PLUGIN_SEED_DIR` and, when set, the session seed at
`CLAUDE_CODE_SESSION_PLUGIN_SEED_DIR` — into `settings.json` on every launch,
with session values taking precedence over base values for the same plugin
key. This keeps a session's extra marketplaces additive: they layer on top of
whatever the base image already selected rather than replacing it. The build
then makes the session cache and seed root-owned/read-only, matching the base
image's existing immutability.
```

Replace with:

```markdown
`images/claude/entrypoint.sh` reads `CLAUDE_CODE_PLUGIN_SEED_DIR` (required)
for the base seed and `CLAUDE_CODE_SESSION_PLUGIN_SEED_DIR` (optional) for
the session seed, and merges every seed it finds into `settings.json` on
every launch via a recursive merge (`(base * session) * current`, jq's `*`
operator, right side winning per key): session values take precedence over
base values for the same key, and the user's already-persisted settings take
precedence over both. This keeps a session's extra marketplaces additive:
they layer on top of whatever the base image already selected rather than
replacing it, and unrelated Claude settings are left untouched.
```

- [ ] **Step 3: Verify the doc renders without markdownlint issues**

Run: `grep -n '^#' docs/session-images.md | head -20`
Expected: headings list unchanged in structure (this is a sanity check that the edits didn't corrupt heading levels; full markdownlint runs in CI's `lint` job).

- [ ] **Step 4: Commit**

```bash
git add docs/session-images.md
git commit -m "docs: describe the shipped session Claude marketplace overlay"
```

---

## Self-Review Notes

- **Spec coverage:** validate-profile.sh (Task 1), render-dockerfile.sh + resolve-image.sh (Tasks 3-4), entrypoint.sh (Task 2), verification-step findings folded directly into Task 2 since the merge algorithm was validated empirically while writing this plan (see Task 2 Step 3's note), testing (Tasks 1, 2, 3, 5), documentation (Task 6). All spec sections are covered.
- **Empirical verification finding:** the spec's "Verification step" asked whether Claude's settings.json uses object-shaped or array-shaped keys, and whether marketplace registration paths are absolute or relative. This plan does not resolve that with a real `claude` binary (none available while writing the plan), but Task 2's merge design and test only depend on `enabledPlugins` being object-shaped, which the pre-existing (already-shipped) single-seed merge already assumed and relied on. If a real end-to-end run in Task 5 shows a session plugin registered but not loading, re-open Task 2 and re-run the spec's Verification step for real before changing the merge algorithm.
- **Placeholder scan:** no TBD/TODO; every step has literal code or an exact command.
- **Type/name consistency:** `render-dockerfile.sh`'s 3-arg signature is introduced in Task 3 and consumed identically in Task 4. `CLAUDE_CODE_SESSION_PLUGIN_SEED_DIR`/`CLAUDE_CODE_SESSION_PLUGIN_CACHE_DIR` are set in Task 3 and read in Task 2 (order doesn't matter for correctness since they're independent files, but Task 2 is sequenced first in this plan since it's fully self-testable without any dependency on Task 3's output).
