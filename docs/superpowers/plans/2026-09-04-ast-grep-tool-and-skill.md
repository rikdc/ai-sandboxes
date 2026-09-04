# ast-grep Tool + Skill Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship `ast-grep` on `PATH` and its two upstream skills (`ast-grep`, `outline`) enabled by default for both the `claude` and `codex` images.

**Architecture:** Extend the existing `github-release-tar` tool adapter to handle `.zip` release assets (in addition to its current `.tar.gz`/`.tgz` support), add a default-on catalog entry + selection pin for the `ast-grep` binary, and add default-on marketplace entries that install the upstream `ast-grep/agent-skill` repo's Claude plugin and Codex skills. No new adapter, no new ADR, no egress changes.

**Tech Stack:** Bash (adapter/validation scripts), `jq` (JSON validation and manipulation), `unzip`/`tar` (archive extraction), Docker/Buildx (image build), Go (`go test ./...` — expected unaffected, no Go changes).

**Spec:** `docs/superpowers/specs/2026-09-03-ast-grep-tool-and-skill-design.md`

## Global Constraints

- ast-grep version pin: `0.45.3` (tag has no `v` prefix).
- Release asset: `app-aarch64-unknown-linux-gnu.zip`, archive member `ast-grep` (flat zip — confirmed below, no nested directory).
- Verified sha256 of the pinned zip (lowercase, 64 hex): `b39cfbc58da4b869a88b8a4bc57bd5deb0d24541e704cf7c257da7b53ec81c8f`.
- `ast-grep/agent-skill` pinned commit: `6b668aa526afdc623c1a9ed1d6ae920e04a717ad` (confirmed present on `main` at that commit; layout matches the spec exactly — `.claude-plugin/marketplace.json` name `ast-grep-marketplace`, plugin `ast-grep`, skills `ast-grep/skills/ast-grep` and `ast-grep/skills/outline`).
- **Open item from the spec is resolved:** `ast-grep --help` in the built `0.45.3` binary lists an `outline` subcommand ("Explore code structure for symbols, imports, exports, and members"). No version bump and no docs-limitation note are needed.
- The `sg` alias/binary inside the zip is never installed — only `ast-grep` (`archive_member: "ast-grep"`).
- No changes to `scripts/tools/lib.sh`, `scripts/tools/install-selected.sh`, `scripts/session/resolve-image.sh` (file list unchanged — only script *content* changes, which correctly busts its cache), `config/marketplaces.example.json`, `images/tools/Dockerfile`, `images/claude/Dockerfile`, `images/codex/Dockerfile`, or `docker-bake.hcl`. Verified during research: `images/tools/Dockerfile` copies the whole `scripts/tools/` directory rather than dispatching per-adapter, and `scripts/session/render-dockerfile.sh`'s per-adapter dispatch already iterates over every adapter present in the *full* `config/tool-catalog.json` (not just the selection), so adding another `github-release-tar` entry changes nothing there.
- `unzip` is already installed in `images/base/Dockerfile` (line 16, alongside `bash ca-certificates curl git gnupg jq openssh-client ripgrep`).
- No test or Go code asserts `config/tools.json` / `config/marketplaces.json` are empty (checked: `scripts/tests/test-build.sh`, `test-install.sh`, `test-update.sh`, `test-config-dir.sh` only iterate filenames, never assert content; `cmd/ai-sandbox/*.go` has zero references to either file).
- **Environment note for whoever executes this plan:** this sandbox has no `zip` binary and no `docker`. `scripts/tools/tests/test-adapters.sh` checks for `zip` up front and **silently exits 0 ("skip")** if it's missing — on a host without `zip` (e.g. `apt-get install -y zip`), Task 1's test additions will not actually run. Confirm `command -v zip` succeeds before treating Task 1's test step as a real pass. Docker-dependent steps in Task 5 need an environment with `docker buildx bake` available.

---

## File Structure

| File | Change |
|---|---|
| `scripts/tools/install-github-release-tar.sh` | Modify — branch extraction on asset suffix (`.tar.gz`/`.tgz` → `tar`, `.zip` → `unzip`, else `die`) |
| `scripts/tools/validate-selection.sh` | Modify — `github-release-tar` branch's `.asset` check gains an `endswith` suffix assertion |
| `scripts/tools/tests/test-adapters.sh` | Modify — add zip happy-path, zip destination-collision, unsupported-extension, and validate-selection asset-suffix test cases |
| `config/tool-catalog.json` | Modify — add the `ast-grep` catalog entry |
| `config/tools.example.json` | Modify — add the `ast-grep` placeholder pin |
| `config/tools.json` | Modify — add the real, default-on `ast-grep` pin |
| `config/marketplaces.json` | Modify — add the default-on Claude + Codex `ast-grep/agent-skill` entries |
| `docs/configuration.md` | Modify — "Optional tools" and "Marketplaces and skills" sections |
| `docs/session-images.md` | Modify — one sentence noting `github-release-tar` accepts `.tar.gz` or `.zip` |
| `docs/adr/0004-apt-npm-tools-trust-tiers.md` | Modify — one clause noting zip is also covered |
| `README.md` | Modify — one line under "Configure" |

---

### Task 1: Extend `github-release-tar` adapter to accept `.zip`

**Files:**
- Modify: `scripts/tools/install-github-release-tar.sh:31`
- Modify: `scripts/tools/validate-selection.sh` (the `github-release-tar)` branch inside `validate_catalog_entry`)
- Test: `scripts/tools/tests/test-adapters.sh`

**Interfaces:**
- Consumes: nothing new — same four positional args (`CATALOG SELECTION TOOL_ID DESTINATION`) and same catalog fields (`repository`, `asset`, `binary`, `archive_member`).
- Produces: the adapter now accepts a catalog `asset` ending in `.tar.gz`, `.tgz`, or `.zip` (previously only `.tar.gz`/`.tgz` worked, undocumented, via a hardcoded `tar -xzf`). Any other suffix now dies with `unsupported asset extension: $asset` instead of failing inside `tar` with a confusing message. Task 2's `ast-grep` catalog entry (asset `app-aarch64-unknown-linux-gnu.zip`) depends on this.

- [ ] **Step 1: Write the failing test — zip happy path**

Open `scripts/tools/tests/test-adapters.sh`. Find the existing github-release-tar block (search for `# ----- github-release-tar: destination collision`). Immediately **after** the existing block's last line —

```bash
grep -q 'refusing to install' "$work/err" || fail "github-release-tar dangling-symlink error message missing"
pass "github-release-tar refuses existing destination (dangling symlink)"
```

— insert:

```bash
# ----- github-release-tar: .zip asset happy path -----------------------------
# Mirrors the real ast-grep release: a flat zip (member at the archive root,
# not nested under a directory) containing one executable.
mkdir -p "$work/fx-gh-zip"
printf '#!/bin/sh\necho hello-zip\n' >"$work/fx-gh-zip/hello"
chmod +x "$work/fx-gh-zip/hello"
(cd "$work/fx-gh-zip" && zip -q "$work/hello.zip" hello)
ghzip_sha=$(sha256_of "$work/hello.zip")
cat >"$work/gh-zip-catalog.json" <<'EOF'
{
  "schema_version": 1,
  "tools": [
    {
      "id": "hello-zip",
      "adapter": "github-release-tar",
      "repository": "o/r",
      "asset": "hello.zip",
      "archive_member": "hello",
      "binary": "hello"
    }
  ]
}
EOF
cat >"$work/gh-zip-sel.json" <<EOF
{"tools":[{"id":"hello-zip","version":"v1","sha256":"$ghzip_sha"}]}
EOF

dest=$(mk_dest)
run_github_release_tar "$work/hello.zip" "$work/gh-zip-catalog.json" "$work/gh-zip-sel.json" hello-zip "$dest" \
  || fail "github-release-tar zip happy path failed"
test -x "$dest/hello" || fail "github-release-tar zip did not install hello"
pass "github-release-tar zip happy path installs the binary"

# ----- github-release-tar: .zip asset destination collision ------------------
dest=$(mk_dest)
touch "$dest/hello"
if run_github_release_tar "$work/hello.zip" "$work/gh-zip-catalog.json" "$work/gh-zip-sel.json" hello-zip "$dest" 2>"$work/err"; then
  fail "github-release-tar zip destination collision was accepted"
fi
grep -q 'refusing to install' "$work/err" || fail "github-release-tar zip collision error message missing"
pass "github-release-tar zip refuses existing destination"

# ----- github-release-tar: unsupported asset extension dies clearly ----------
# Reuses the existing hello.tar.gz fixture/checksum from the block above
# ($gh_sha) — the extension branch runs before extraction is ever attempted,
# so the archive's real content does not matter here.
cat >"$work/gh-bad-ext-catalog.json" <<'EOF'
{
  "schema_version": 1,
  "tools": [
    {
      "id": "hello-bad-ext",
      "adapter": "github-release-tar",
      "repository": "o/r",
      "asset": "hello.exe",
      "archive_member": "hello",
      "binary": "hello"
    }
  ]
}
EOF
cat >"$work/gh-bad-ext-sel.json" <<EOF
{"tools":[{"id":"hello-bad-ext","version":"v1","sha256":"$gh_sha"}]}
EOF
dest=$(mk_dest)
if run_github_release_tar "$work/hello.tar.gz" "$work/gh-bad-ext-catalog.json" "$work/gh-bad-ext-sel.json" hello-bad-ext "$dest" 2>"$work/err"; then
  fail "unsupported asset extension was accepted"
fi
grep -q 'unsupported asset extension' "$work/err" || fail "unsupported extension error message missing"
pass "github-release-tar rejects unsupported asset extension"
```

Then find the existing block `# ----- validate-selection: cross-tool binary collision` and insert **before** it:

```bash
# ----- validate-selection: github-release-tar asset needs an approved extension --
cat >"$work/badasset-catalog.json" <<'EOF'
{
  "schema_version": 1,
  "tools": [
    {"id":"badasset","adapter":"github-release-tar","repository":"o/r","asset":"tool.exe","archive_member":"tool","binary":"tool"}
  ]
}
EOF
cat >"$work/badasset-sel.json" <<'EOF'
{"tools":[{"id":"badasset","version":"v1","sha256":"0000000000000000000000000000000000000000000000000000000000000000"}]}
EOF
if bash "$repo/scripts/tools/validate-selection.sh" "$work/badasset-catalog.json" "$work/badasset-sel.json" "$runtime_null" 2>"$work/err"; then
  fail "github-release-tar asset without approved extension was accepted"
fi
grep -q 'invalid catalog entry' "$work/err" || fail "bad asset extension error message missing"
pass "validate-selection rejects github-release-tar asset without an approved extension"
```

- [ ] **Step 2: Run the test suite to verify it fails**

Run: `command -v zip >/dev/null && bash scripts/tools/tests/test-adapters.sh || echo "zip unavailable — install zip (e.g. apt-get install -y zip) to run this test"`

Expected: FAIL. The zip happy-path case fails because `install-github-release-tar.sh` still unconditionally calls `tar -xzf` on a real zip file (`tar` will error, e.g. "not in gzip format" or similar). The unsupported-extension case fails because there is no `unsupported asset extension` message yet — `tar` runs on `hello.tar.gz` regardless of the `.exe` name and actually *succeeds*, so `fail "unsupported asset extension was accepted"` triggers. The validate-selection case fails because `tool.exe` currently passes the existing shape check (no suffix constraint yet).

- [ ] **Step 3: Modify `install-github-release-tar.sh` to branch on asset suffix**

Replace line 31:

```bash
tar -xzf "$archive" -C "$extract_dir" "$archive_member" || die "could not extract $archive_member from archive"
```

with:

```bash
case "$asset" in
  *.tar.gz | *.tgz)
    tar -xzf "$archive" -C "$extract_dir" "$archive_member" || die "could not extract $archive_member from archive"
    ;;
  *.zip)
    unzip -o -q "$archive" "$archive_member" -d "$extract_dir" || die "could not extract $archive_member from archive"
    ;;
  *)
    die "unsupported asset extension: $asset"
    ;;
esac
```

- [ ] **Step 4: Modify `validate-selection.sh` to require an approved asset suffix**

In `scripts/tools/validate-selection.sh`, inside `validate_catalog_entry`'s `github-release-tar)` branch, the current `.asset` check reads:

```
        (.asset | type == "string" and test("^[A-Za-z0-9][A-Za-z0-9._-]*$") and (contains("..") | not)) and
```

Change it to:

```
        (.asset | type == "string" and test("^[A-Za-z0-9][A-Za-z0-9._-]*$") and (contains("..") | not) and (endswith(".tar.gz") or endswith(".tgz") or endswith(".zip"))) and
```

- [ ] **Step 5: Run the test suite to verify it passes**

Run: `bash scripts/tools/tests/test-adapters.sh`

Expected: every line ends with `ok:` and the script prints a final `ok`, including the four new cases (`github-release-tar zip happy path installs the binary`, `github-release-tar zip refuses existing destination`, `github-release-tar rejects unsupported asset extension`, `validate-selection rejects github-release-tar asset without an approved extension`). If `zip` is unavailable on the host, the whole script prints `skip: zip not available on this host` and exits 0 — that is not a pass; install `zip` and re-run before trusting this step.

- [ ] **Step 6: Shellcheck the modified scripts**

Run: `shellcheck scripts/tools/install-github-release-tar.sh scripts/tools/validate-selection.sh scripts/tools/tests/test-adapters.sh`

Expected: no output, exit 0.

- [ ] **Step 7: Commit**

```bash
git add scripts/tools/install-github-release-tar.sh scripts/tools/validate-selection.sh scripts/tools/tests/test-adapters.sh
git commit -m "feat(tools): accept .zip release assets in the github-release-tar adapter"
```

---

### Task 2: Add `ast-grep` to the tool catalog and default selection

**Files:**
- Modify: `config/tool-catalog.json`
- Modify: `config/tools.example.json`
- Modify: `config/tools.json`

**Interfaces:**
- Consumes: Task 1's `.zip`-capable adapter and its suffix validation.
- Produces: a catalog entry with `id: "ast-grep"` that later tasks and the real build reference (Task 5's manual verification runs `ast-grep --version` / `ast-grep --help`).

- [ ] **Step 1: Add the catalog entry**

In `config/tool-catalog.json`, the `tools` array currently ends with the `awscli` entry:

```json
    {
      "id": "awscli",
      "adapter": "awscli-zip",
      "url_template": "https://awscli.amazonaws.com/awscli-exe-linux-aarch64-{{version}}.zip",
      "binary": "aws"
    }
  ]
}
```

Change it to:

```json
    {
      "id": "awscli",
      "adapter": "awscli-zip",
      "url_template": "https://awscli.amazonaws.com/awscli-exe-linux-aarch64-{{version}}.zip",
      "binary": "aws"
    },
    {
      "id": "ast-grep",
      "adapter": "github-release-tar",
      "repository": "ast-grep/ast-grep",
      "asset": "app-aarch64-unknown-linux-gnu.zip",
      "archive_member": "ast-grep",
      "binary": "ast-grep"
    }
  ]
}
```

- [ ] **Step 2: Add the example placeholder**

In `config/tools.example.json`, the `tools` array currently ends with the `awscli` entry:

```json
    {
      "id": "awscli",
      "version": "X.Y.Z",
      "sha256": "LOWERCASE_64_CHARACTER_SHA256"
    }
  ]
}
```

Change it to:

```json
    {
      "id": "awscli",
      "version": "X.Y.Z",
      "sha256": "LOWERCASE_64_CHARACTER_SHA256"
    },
    {
      "id": "ast-grep",
      "version": "X.Y.Z",
      "sha256": "LOWERCASE_64_CHARACTER_SHA256"
    }
  ]
}
```

- [ ] **Step 3: Make `ast-grep` the default-on selection**

Replace the entire contents of `config/tools.json` (currently `{"tools": []}`) with:

```json
{
  "tools": [
    {
      "id": "ast-grep",
      "version": "0.45.3",
      "sha256": "b39cfbc58da4b869a88b8a4bc57bd5deb0d24541e704cf7c257da7b53ec81c8f"
    }
  ]
}
```

This sha256 was computed directly against the pinned release asset during planning:

```console
$ curl -fsSL -o /tmp/ast-grep.zip \
    https://github.com/ast-grep/ast-grep/releases/download/0.45.3/app-aarch64-unknown-linux-gnu.zip
$ sha256sum /tmp/ast-grep.zip
b39cfbc58da4b869a88b8a4bc57bd5deb0d24541e704cf7c257da7b53ec81c8f  /tmp/ast-grep.zip
$ unzip -l /tmp/ast-grep.zip
  Length      Date    Time    Name
---------  ---------- -----   ----
   447488  ...          sg
 51995576  ...          ast-grep
```

(Confirms the zip is flat — `ast-grep` sits at the archive root, not nested under a directory — so `archive_member: "ast-grep"` is correct as-is.)

- [ ] **Step 4: Validate the catalog + selection**

Run: `./scripts/tools/validate-selection.sh config/tool-catalog.json config/tools.json config/runtime.json`

Expected: no output, exit 0. (This exercises the new `.zip` suffix check from Task 1 against the real `ast-grep` entry, and confirms `config/runtime.json`'s existing shape still satisfies the runtime document check.)

- [ ] **Step 5: Re-run the adapter test suite for regressions**

Run: `bash scripts/tools/tests/test-adapters.sh`

Expected: unchanged from Task 1's Step 5 — this step is just a regression guard after touching catalog-adjacent files.

- [ ] **Step 6: Commit**

```bash
git add config/tool-catalog.json config/tools.example.json config/tools.json
git commit -m "feat(tools): default-enable ast-grep 0.45.3"
```

---

### Task 3: Default-enable the ast-grep Claude plugin and Codex skills

**Files:**
- Modify: `config/marketplaces.json`

**Interfaces:**
- Consumes: nothing from Tasks 1–2 (marketplace wiring is independent of the tool-catalog changes; it only needs the pinned upstream commit).
- Produces: the default marketplace selection that `scripts/marketplaces/install-claude.sh` and `scripts/marketplaces/install-codex.sh` consume at image-build time. Task 5's manual verification checks both agents pick this up.

- [ ] **Step 1: Replace the empty default with the ast-grep entries**

Replace the entire contents of `config/marketplaces.json` (currently `{"claude": [], "codex": []}`) with:

```json
{
  "claude": [
    {
      "url": "https://github.com/ast-grep/agent-skill.git",
      "ref": "6b668aa526afdc623c1a9ed1d6ae920e04a717ad",
      "path": ".",
      "plugins": ["ast-grep"]
    }
  ],
  "codex": [
    {
      "url": "https://github.com/ast-grep/agent-skill.git",
      "ref": "6b668aa526afdc623c1a9ed1d6ae920e04a717ad",
      "skills_path": "ast-grep/skills"
    }
  ]
}
```

- [ ] **Step 2: Verify the Claude entry against `install-claude.sh`'s own shape check**

`scripts/marketplaces/install-claude.sh` validates its input with a `jq -e` expression before ever cloning anything. Run that exact expression against the new file to confirm it passes without needing Docker:

Run:
```bash
jq -e '
  def selected_plugins: (.plugins? // []);
  (.claude | type == "array") and
  all(.claude[];
    (.url | type == "string" and test("^https://github\\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\\.git$") and (contains("..") | not)) and
    (.ref | type == "string" and test("^[0-9a-f]{40}$")) and
    (.path | type == "string" and (. == "." or (test("^[A-Za-z0-9][A-Za-z0-9._/-]*$") and (contains("..") | not)))) and
    (selected_plugins | type == "array") and
    (selected_plugins | all(.[]; type == "string" and test("^[a-z0-9][a-z0-9-]*[a-z0-9]$|^[a-z0-9]$"))) and
    ((selected_plugins | length) == (selected_plugins | unique | length))
  )
' config/marketplaces.json
```

Expected: prints `true`.

- [ ] **Step 3: Verify the Codex entry against `install-codex.sh`'s own shape check**

Run:
```bash
jq -e '
  (.codex | type == "array") and
  all(.codex[];
    (.url | type == "string" and test("^https://github\\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\\.git$") and (contains("..") | not)) and
    (.ref | type == "string" and test("^[0-9a-f]{40}$")) and
    (.skills_path | type == "string" and (. == "." or (test("^[A-Za-z0-9][A-Za-z0-9._/-]*$") and (contains("..") | not))))
  )
' config/marketplaces.json
```

Expected: prints `true`.

- [ ] **Step 4: Confirm the pinned commit's on-disk layout still matches** (defense against upstream force-push/rewrite between the design doc and implementation)

Run:
```bash
rm -rf /tmp/agent-skill-verify
git clone -q https://github.com/ast-grep/agent-skill.git /tmp/agent-skill-verify
git -C /tmp/agent-skill-verify checkout -q 6b668aa526afdc623c1a9ed1d6ae920e04a717ad
jq -er '.name' /tmp/agent-skill-verify/.claude-plugin/marketplace.json
jq -er '.plugins[].name' /tmp/agent-skill-verify/.claude-plugin/marketplace.json
test -f /tmp/agent-skill-verify/ast-grep/skills/ast-grep/SKILL.md && echo "ast-grep skill: present"
test -f /tmp/agent-skill-verify/ast-grep/skills/outline/SKILL.md && echo "outline skill: present"
rm -rf /tmp/agent-skill-verify
```

Expected:
```
ast-grep-marketplace
ast-grep
ast-grep skill: present
outline skill: present
```

- [ ] **Step 5: Commit**

```bash
git add config/marketplaces.json
git commit -m "feat(marketplaces): default-enable the ast-grep Claude plugin and Codex skills"
```

---

### Task 4: Documentation

**Files:**
- Modify: `docs/configuration.md`
- Modify: `docs/session-images.md`
- Modify: `docs/adr/0004-apt-npm-tools-trust-tiers.md`
- Modify: `README.md`

**Interfaces:**
- Consumes: the shipped defaults from Tasks 2–3 (this task only documents them; no code).
- Produces: nothing consumed by later tasks.

- [ ] **Step 1: `docs/configuration.md` — "Optional tools" section**

Find:
```markdown
## Optional tools

`~/.config/ai-sandboxes/tools.json` selects tools for the agent images. Copy
the structure in `config/tools.example.json`; each selected tool must be
present in the reviewed `config/tool-catalog.json` and use its required
version and checksum pins. The default selection is empty.
```

Replace with:
```markdown
## Optional tools

`~/.config/ai-sandboxes/tools.json` selects tools for the agent images. Copy
the structure in `config/tools.example.json`; each selected tool must be
present in the reviewed `config/tool-catalog.json` and use its required
version and checksum pins. The default selection installs `ast-grep`
(structural code search on `PATH` in both images); remove its entry from
`~/.config/ai-sandboxes/tools.json` to opt out.
```

- [ ] **Step 2: `docs/configuration.md` — "Marketplaces and skills" section**

Find:
```markdown
- Codex entries must be pinned to a commit SHA and point `skills_path` at directories containing native `SKILL.md` files.
- Do not put credentials in the configuration or repository URLs.
```

Replace with:
```markdown
- Codex entries must be pinned to a commit SHA and point `skills_path` at directories containing native `SKILL.md` files.
- Do not put credentials in the configuration or repository URLs.
- The default configuration registers the upstream `ast-grep/agent-skill` marketplace for both agents: the `ast-grep` Claude plugin (bundling the `ast-grep` and `outline` skills) and the same two skills natively for Codex. Remove its entry from `~/.config/ai-sandboxes/marketplaces.json` to opt out.
```

- [ ] **Step 3: `docs/session-images.md` — adapter list sentence**

Find:
```markdown
`scripts/tools/install-selected.sh` (phase `runtime`) dispatches to one of
several adapter installers — currently `install-github-release-tar.sh`,
`install-https-tar.sh`, and `install-awscli-zip.sh` — all copied
```

Replace with:
```markdown
`scripts/tools/install-selected.sh` (phase `runtime`) dispatches to one of
several adapter installers — currently `install-github-release-tar.sh`
(accepting a `.tar.gz`, `.tgz`, or `.zip` release asset), `install-https-tar.sh`,
and `install-awscli-zip.sh` — all copied
```

- [ ] **Step 4: `docs/adr/0004-apt-npm-tools-trust-tiers.md` — trust-tier sentence**

Find:
```markdown
github-release-tar install runs no lifecycle hooks and downloads only a
fixed catalog-pinned URL whose sha256 is verified before extraction.
```

Replace with:
```markdown
github-release-tar install runs no lifecycle hooks and downloads only a
fixed catalog-pinned URL whose sha256 is verified before extraction
(tarball or zip).
```

- [ ] **Step 5: `README.md` — "Configure" section**

Find:
```markdown
- Choose optional tools in `~/.config/ai-sandboxes/tools.json`; their allowed sources are reviewed in `config/tool-catalog.json`.
```

Replace with:
```markdown
- Choose optional tools in `~/.config/ai-sandboxes/tools.json`; their allowed sources are reviewed in `config/tool-catalog.json`. `ast-grep` ships enabled by default.
```

- [ ] **Step 6: Lint markdown**

Run: `docker run --rm -v "$PWD:/work" -w /work davidanson/markdownlint-cli2:v0.14.0 "docs/configuration.md" "docs/session-images.md" "docs/adr/0004-apt-npm-tools-trust-tiers.md" "README.md"` (matches the pinned action in `.github/workflows/lint.yml`; if Docker is unavailable, visually diff each edit against surrounding style — headings, list markers, line wrapping — as a fallback).

Expected: no findings.

- [ ] **Step 7: Commit**

```bash
git add docs/configuration.md docs/session-images.md docs/adr/0004-apt-npm-tools-trust-tiers.md README.md
git commit -m "docs: note the default-on ast-grep tool and skill"
```

---

### Task 5: Full verification

**Files:** none (verification only).

**Interfaces:**
- Consumes: everything from Tasks 1–4.
- Produces: nothing — this is the final gate.

- [ ] **Step 1: Full adapter + render-dockerfile test suites**

Run:
```bash
bash scripts/tools/tests/test-adapters.sh
bash scripts/session/tests/test-render-dockerfile.sh
```

Expected: both print `ok` with no `FAIL` lines. (`test-render-dockerfile.sh` is unaffected by this feature — its filename assertions still hold — this just guards against an accidental regression.)

- [ ] **Step 2: Full catalog/selection validation**

Run: `./scripts/tools/validate-selection.sh config/tool-catalog.json config/tools.json config/runtime.json`

Expected: no output, exit 0.

- [ ] **Step 3: Go test suite (expected unaffected)**

Run: `go test ./...`

Expected: all packages pass — no Go files were touched by this feature.

- [ ] **Step 4: Shellcheck everything touched**

Run: `shellcheck scripts/tools/install-github-release-tar.sh scripts/tools/validate-selection.sh scripts/tools/tests/test-adapters.sh`

Expected: no output, exit 0.

- [ ] **Step 5: Dockerfile lint**

Run: `./scripts/lint-dockerfiles`

Expected: clean (no Dockerfile changed in this feature, but this mirrors the CI `lint` job's full gate).

- [ ] **Step 6: Full build and verify** (requires Docker + Buildx; this sandbox does not have `docker` installed — run this step in an environment that does, e.g. the dev host or CI)

Run:
```bash
./scripts/build
./scripts/verify
```

Expected: both succeed. `./scripts/build` will now download the `ast-grep` release and the `ast-grep/agent-skill` repo during the image build (a few-MB, one-time cost per cache invalidation) since `config/tools.json` and `config/marketplaces.json` are no longer empty defaults.

- [ ] **Step 7: Manual verification in the built images**

Run:
```bash
docker run --rm ai-sandboxes-claude:local ast-grep --version
docker run --rm ai-sandboxes-claude:local ast-grep --help
docker run --rm ai-sandboxes-codex:local ast-grep --version
```

Expected:
- `ast-grep --version` prints `ast-grep 0.45.3` in both images.
- `ast-grep --help` lists `outline` among the `Commands:` (already confirmed against the real binary during planning — this step re-confirms it survived the actual build/install path, not just the standalone download).

Then:
```bash
docker run --rm --user node -e HOME=/tmp/ast-grep-verify ai-sandboxes-claude:local claude plugin list
```
Expected: shows `ast-grep@ast-grep-marketplace` with `Status: ✔ enabled` (same assertion pattern as `scripts/session/tests/test-session-marketplace.sh`).

Then:
```bash
docker run --rm ai-sandboxes-codex:local sh -c 'test -f /opt/codex-skills/ast-grep/SKILL.md && test -f /opt/codex-skills/outline/SKILL.md && echo both-present'
```
Expected: prints `both-present`.

- [ ] **Step 8: Final review against the spec's Rollout note**

No code step here — just confirm by inspection that anyone with an already-seeded `~/.config/ai-sandboxes/tools.json` / `marketplaces.json` needs to manually add these two entries (or delete the files and let the next build reseed them) to pick up the new defaults; this is expected and already called out in `docs/configuration.md`'s "Migrating an existing installation" paragraph plus this feature's own doc updates from Task 4.

- [ ] **Step 9: Commit** (only if any of the above steps required fixes; otherwise nothing to commit — this task is verification-only)

---

## Self-Review Notes

- **Spec coverage:** Change-set sections 1 (adapter), 2 (tests), 3 (catalog/selection), 4 (marketplace wiring), 5 (docs) each map to Tasks 1–4; the spec's "Verification" section maps to Task 5. The spec's "Open item" (confirm `ast-grep outline` exists) was resolved during planning research (see Global Constraints) rather than deferred to implementation, since the answer was verifiable now and changes nothing about the plan.
- **Placeholder scan:** no TBD/TODO; every step has literal file content, exact commands, and expected output.
- **Type/name consistency:** catalog `id`/`binary`/`archive_member` values (`ast-grep`) are identical across Tasks 2 and the Task 5 manual checks; marketplace `plugins: ["ast-grep"]` and `skills_path: "ast-grep/skills"` are identical across Task 3 and Task 5's checks.
