# ast-grep tool + skill for Claude Code and Codex — design

Date: 2026-09-03
Status: approved for planning

## Goal

Give both agents inside the sandbox access to
[ast-grep](https://github.com/ast-grep/ast-grep) structural code search:

1. The `ast-grep` binary on `PATH` in the tools image (inherited by the
   `claude` and `codex` images).
2. The [ast-grep agent skill](https://github.com/ast-grep/agent-skill) —
   both skills it ships (`ast-grep` and `outline`) — installed for Claude
   Code (as a plugin) and Codex (as native skills).

Both are **on by default**: the committed `config/tools.json` and
`config/marketplaces.json` seeds carry the entries, so a fresh install
builds them without further configuration. Existing installs pick them up
by editing their own `~/.config/ai-sandboxes/` copies (or reseeding).

## Non-goals

- No new tool adapter. The existing `github-release-tar` adapter is
  extended in place to also accept `.zip` release assets.
- No egress/allowlist changes. The binary is fetched at image build time;
  `ast-grep` runs purely locally on the mounted workspace at runtime.
- No `versions.env` change. Optional-tool version pins live in
  `config/tools.json` / `config/tools.example.json`, like every other
  curated tool.
- The `sg` alias is not installed (it collides with util-linux `sg`).
- No new ADR. The adapter change is a mechanical extension of an existing,
  already-documented trust tier.

## Decisions (from brainstorming)

| Decision | Choice |
|---|---|
| Binary delivery | Extend `github-release-tar` adapter to handle `.zip`; keep its id and script name |
| Default state | Default-on: committed `tools.json` + `marketplaces.json` seeds |
| Skill scope | Both skills the repo ships: `ast-grep` + `outline` |
| Version pin | ast-grep `0.45.3` |

## Upstream facts (verified 2026-09-03)

- ast-grep release `0.45.3` publishes `app-aarch64-unknown-linux-gnu.zip`
  among its assets. Tags have **no** `v` prefix, so the download path is
  `releases/download/0.45.3/app-aarch64-unknown-linux-gnu.zip` — matches
  the adapter's existing `releases/download/${version}/${asset}` template
  with `version: "0.45.3"`.
- `unzip` is already installed in the base image
  (`images/base/Dockerfile`, used today by the awscli-zip path).
- `ast-grep/agent-skill` HEAD on `main` is
  `6b668aa526afdc623c1a9ed1d6ae920e04a717ad`. Layout:
  - `.claude-plugin/marketplace.json` at repo root — marketplace name
    `ast-grep-marketplace`, one plugin `ast-grep` (source `./ast-grep`).
  - `ast-grep/skills/ast-grep/SKILL.md` (+ `references/rule_reference.md`)
  - `ast-grep/skills/outline/SKILL.md`
  - The Claude plugin `ast-grep` bundles **both** skill directories.
- **Open item for implementation:** the `outline` skill drives
  `ast-grep outline`, a subcommand not shown in the upstream README.
  Implementation MUST confirm `ast-grep outline` exists in the built
  `0.45.3` image (`ast-grep --help`). If it does not:
  - bump the pin to the earliest release that has it (update the version
    and sha256 in `tools.json` + `tools.example.json` and this spec), or
  - if no released version has it, document the limitation in
    `docs/configuration.md` and leave the skill in place (a
    missing-subcommand error is inert, not harmful). Prefer the version
    bump.

## Change set

### 1. Extend the `github-release-tar` adapter to accept `.zip`

**`scripts/tools/install-github-release-tar.sh`**

After the sha256 check, branch on the `asset` suffix instead of always
calling `tar`:

- `*.tar.gz` / `*.tgz` → existing `tar -xzf "$archive" -C "$extract_dir"
  "$archive_member"`.
- `*.zip` → `unzip -o -q "$archive" "$archive_member" -d "$extract_dir"`.
- anything else → `die` with a clear "unsupported asset extension"
  message.

Everything after extraction is unchanged: `path_is_absent` collision
guard on `$destination/$binary`, `mkdir -p`, `cp --
"$extract_dir/$archive_member" "$destination/$binary"`, `chmod 0755`.
`$archive_member` keeps its meaning: the path of the wanted file inside
the archive (`ast-grep` for this zip — confirm the zip is flat and not
nested under a directory when computing the sha256).

**`scripts/tools/validate-selection.sh`**

In the `github-release-tar)` branch of `validate_catalog_entry`, add an
assertion that `.asset` ends in one of `.tar.gz`, `.tgz`, `.zip`
(alongside the existing `^[A-Za-z0-9][A-Za-z0-9._-]*$` shape check). The
allowed `keys | sort` sets are unchanged.

**`scripts/tools/lib.sh`** — no change (`KNOWN_ADAPTERS` still lists
`github-release-tar`).

**`scripts/tools/install-selected.sh`** — no change. The
`runtime:github-release-tar` branch already dispatches to this script;
ast-grep has no `state_wrapper`, so it takes the plain `/usr/local/bin`
path.

**`scripts/session/resolve-image.sh`** — no change to the file list in
`renderer_hash` (the script name is unchanged); its **content** changes,
which correctly busts the session-image cache.

### 2. Tests

**`scripts/tools/tests/test-adapters.sh`** — add, next to the existing
`github-release-tar` tar cases:

- a `.zip` happy path: build a tiny zip containing a member, point a
  catalog entry with `"asset": "x.zip"` at it via whatever local
  fixture/serving mechanism the existing tar happy-path uses; assert the
  binary is installed and executable.
- a `.zip` destination-collision case mirroring the tar collision test
  (expects `refusing to install`).
- (optional) an unsupported-extension case asserting the new `die`.

**`scripts/session/tests/test-render-dockerfile.sh`** — no change
(filename assertions still hold).

### 3. Tool catalog + selection

**`config/tool-catalog.json`** — new entry in `tools`:

```json
{
  "id": "ast-grep",
  "adapter": "github-release-tar",
  "repository": "ast-grep/ast-grep",
  "asset": "app-aarch64-unknown-linux-gnu.zip",
  "archive_member": "ast-grep",
  "binary": "ast-grep"
}
```

Passes `validate-selection.sh`: `id` / `binary` match `^[a-z][a-z0-9-]*$`;
`asset` matches the shape check and (new) the `.zip` suffix check;
`archive_member` matches `^[A-Za-z0-9][A-Za-z0-9._/-]*$`; `keys | sort`
equals the no-`state_wrapper` set.

**`config/tools.example.json`** — add a placeholder line:

```json
{ "id": "ast-grep", "version": "0.45.3", "sha256": "LOWERCASE_64_CHARACTER_SHA256" }
```

**`config/tools.json`** (committed seed, currently `{"tools": []}`) —
default-on:

```json
{ "tools": [ { "id": "ast-grep", "version": "0.45.3", "sha256": "<real sha256 of the 0.45.3 zip>" } ] }
```

Implementation computes the sha256 by downloading
`https://github.com/ast-grep/ast-grep/releases/download/0.45.3/app-aarch64-unknown-linux-gnu.zip`
and running `sha256sum` (lowercase, 64 hex — the format
`validate-selection.sh` requires).

### 4. Marketplace wiring

**`config/marketplaces.json`** (committed seed, currently
`{"claude": [], "codex": []}`) — default-on:

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

Shape checks that must pass (verified against the install scripts):

- Claude: URL matches
  `^https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\.git$`; `ref` is
  40 lowercase hex; `path` is `.`; plugin name `ast-grep` matches
  `^[a-z0-9][a-z0-9-]*[a-z0-9]$`; the pinned manifest's `name` is
  `ast-grep-marketplace` (matches `^[a-z][a-z0-9-]*$`); the manifest
  declares a plugin named `ast-grep` (`validate_selected_plugins`).
- Codex: `skills_path` `ast-grep/skills` matches
  `^[A-Za-z0-9][A-Za-z0-9._/-]*$`; the directory exists and contains
  subdirectories with `SKILL.md`. `install-codex.sh` copies each subdir
  to `/opt/codex-skills/<basename>` → `/opt/codex-skills/ast-grep` and
  `/opt/codex-skills/outline`.

**`config/marketplaces.example.json`** — unchanged; it documents shape
with `OWNER` / `FULL_COMMIT_SHA` placeholders.

### 5. Docs

- **`docs/configuration.md`**
  - "Optional tools": replace "The default selection is empty." with a
    description of the `ast-grep` default and how to remove it (drop the
    entry from `~/.config/ai-sandboxes/tools.json`).
  - "Marketplaces and skills": note the default `ast-grep` skill entry
    for both agents.
  - If the `outline` open item resolves to "document the limitation", add
    a sentence here.
- **`docs/session-images.md`** (~line 262–269): the sentence listing what
  the adapters extract — note `github-release-tar` accepts a `.tar.gz` or
  `.zip` asset.
- **`docs/adr/0004-apt-npm-tools-trust-tiers.md`** (~line 42–46): the
  "downloads only a fixed catalog-pinned URL whose sha256 is verified
  before extraction" sentence still holds for zip; add "(tarball or zip)"
  so the wording is not tar-specific.
- **`README.md`**: one line under "Configure" that ast-grep ships enabled
  by default.

## Verification

1. `bash scripts/tools/tests/test-adapters.sh` — existing tar cases plus
   the new zip cases pass.
2. `bash scripts/session/tests/test-render-dockerfile.sh` — still passes.
3. `./scripts/tools/validate-selection.sh config/tool-catalog.json
   config/tools.json config/runtime.json` — passes with the new entry.
4. `go test ./...` — expected unaffected (no Go changes).
5. `./scripts/build` then `./scripts/verify`.
6. Manual, in the built images:
   - `ast-grep --version` prints `0.45.3` in both `claude` and `codex`
     images.
   - `ast-grep --help` — confirm the `outline` subcommand (resolves the
     open item above).
   - Claude: the `ast-grep` plugin is installed/enabled and both skills
     are present.
   - Codex: `/opt/codex-skills/ast-grep` and `/opt/codex-skills/outline`
     exist with their `SKILL.md`.
7. `.github/workflows/lint.yml` — shellcheck/hadolint clean on the
   modified script.

## Rollout

Merging changes the digest of `config/*.json` and
`scripts/tools/install-github-release-tar.sh`; `./scripts/update` detects
this and rebuilds/reloads. Build time grows by one ~few-MB download.
Existing users who have already seeded `~/.config/ai-sandboxes/` must add
the two entries to their own `tools.json` / `marketplaces.json` (or
delete those files and let the next build reseed them).

## Risks

- **`outline` subcommand may not exist in `0.45.3`.** Mitigated by the
  mandatory implementation check and the version-bump fallback above.
- **Default-on couples every fresh build to an external repo + release.**
  Both are pinned (release tag `0.45.3`, commit `6b668aa5…`); a
  disappearing pin fails the build loudly rather than silently drifting.
  Same exposure the existing `marketplaces.runtime-test.json` already
  accepts.
- **`config/tools.json` is no longer empty by default.** Confirm no CI
  job or Go test asserts an empty selection (grep during implementation).
