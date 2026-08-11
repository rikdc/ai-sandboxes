# Session apt and npm derived layers

## Purpose

Implement Implementation task 8 from `docs/session-images.md`: make a
session profile's `apt` and `npm` fields actually install packages into the
derived session image, instead of being rejected by validation. Python
(task 9) explicitly depends on this task and is out of scope here — it ships
as a separate, later PR.

## Non-goals

- `python` session profile field (task 9, separate PR — stays rejected by
  validation until then).
- Image GC or last-used metadata (task 11).
- `docs/private-profiles.md` removal or the broader documentation rollout
  (task 12).
- Any change to how the base image installs its own tools
  (`images/base/Dockerfile`, `images/tools/Dockerfile`) or how session
  marketplaces install (already shipped).

## Data flow

```text
validate-profile.sh (accepts apt/npm; still rejects python)
  -> resolve-image.sh (passes canonical profile JSON to the renderer, unchanged)
  -> render-dockerfile.sh (assembles composable layers: apt, npm, marketplaces —
     any combination, including none)
  -> docker buildx build
  -> apt layer's install script patches resolved.json with actual installed
     versions before the image is finalized
```

## Composability

`render-dockerfile.sh` currently has a binary branch: an empty profile emits
a static single-stage Dockerfile, a profile with `claude_marketplaces` emits
a multi-stage Dockerfile (marketplace install in a discarded build stage,
final stage copies over the augmented plugin cache/seed). This task
generalizes that into independently-present layers — a profile can have any
combination of `apt`, `npm`, and `claude_marketplaces` populated (including
combinations that were previously impossible, like apt *and* marketplaces
together), matching `docs/session-images.md`'s own stated intent ("Apt, npm,
Python, and plugin steps are separate layers to maximize cache reuse").

Concretely: the renderer builds a Dockerfile in three conceptual sections it
emits conditionally:

1. **A base `FROM` line.** If marketplaces are present, this is the existing
   `FROM $base_image_ref AS build` / final-stage split. If not, a single
   `FROM $base_image_ref`.
2. **apt and npm layers**, appended into whichever stage is "final" for this
   build (the marketplace case's second `FROM` block, or the single-stage
   block if there's no marketplace layer) — never the marketplace build
   stage, which exists only to be discarded. See below for why apt/npm don't
   need their own discarded build stage the way marketplaces did.
3. **The existing `resolved.json` COPY**, changed from immediately
   read-only to writable-then-locked (see Provenance below), since the apt
   layer needs to patch it.

When more than one layer is present, the renderer always emits them in a
fixed order — apt, then npm, then the marketplace build stage's output —
regardless of the order fields happen to appear in the profile JSON, so the
same profile always renders the same Dockerfile byte-for-byte (this also
keeps the cache key, which already hashes the canonicalized profile,
meaningful: two profiles with the same fields in a different JSON key order
canonicalize to the same bytes today, and must keep rendering to the same
Dockerfile too). apt before npm since an npm package's `postinstall` script
could plausibly depend on a system library or build tool the same profile
also requested via `apt` (e.g. native bindings needing a compiler); npm
before the marketplace stage is arbitrary — npm has no interaction with the
marketplace install at all — and is simply a stable, documented choice.

## apt layer

Unlike npm and marketplaces, apt has no separate image-local prefix —
packages land in standard system paths (`/usr/bin`, `/usr/lib`, `dpkg`'s own
database), which are already root-owned and unwritable to the unprivileged
`node` runtime user with no additional locking. This means apt cannot use a
discarded build stage the way marketplaces do (its footprint isn't confined
to one copyable directory) — it must run directly in whatever stage is
"final" for this build.

The base image removes its own apt lists after building
(`images/base/Dockerfile`: `rm -rf /var/lib/apt/lists/*`), so this layer
must run `apt-get update` first, requiring network egress — already gated by
`CLAUDE_MSB_BUILD_EGRESS=1` in `resolve-image.sh` before any build happens
at all, so no new gate is needed. Mirrors `images/base/Dockerfile`'s own
single-stage `apt-get install ... && rm -rf /var/lib/apt/lists/*` pattern —
no multi-stage trickery needed for cleanup, matching this repo's existing
precedent.

A new script, `scripts/session/install-apt-packages.sh`, takes two
arguments — a JSON file shaped `{"apt": [{"name": "...", "version":
"..."?}]}` (mirroring the `{"claude": [...], "codex": []}` convention
`install-claude.sh` uses for marketplaces), and the path to
`resolved.json` — and:

1. Builds the `apt-get install` package-spec list: `name=version` for
   pinned entries, bare `name` for unpinned ones.
2. Runs `apt-get update && apt-get install -y --no-install-recommends
   <specs>`.
3. For each requested package, runs `dpkg-query -W -f='${Version}'
   <name>` to get the actual installed version.
4. Patches `resolved.json`'s `.packages.apt` array in place with those
   actual versions directly (temp file → atomic `mv`, matching this
   codebase's established pattern in `merge-plugin-seed.sh` and
   `entrypoint.sh`) — no intermediate fragment file; this script is the
   only writer of apt provenance, so it can patch the real file itself
   rather than handing a fragment to something else to merge later.
5. `rm -rf /var/lib/apt/lists/*`.

## npm layer

Installs into `/opt/claude-session/npm` via `npm install --global --prefix
/opt/claude-session/npm <pkg>@<version> ...`. Verified empirically before
writing this spec (the design doc flagged this layout as needing
verification): a global-prefix install produces `<prefix>/bin/<binary>` as
a *relative* symlink into `<prefix>/lib/node_modules/<package>/...`, which
resolves correctly regardless of where the prefix directory ends up —
portable across the build/copy boundary. Package bin files come out
already executable; no extra `chmod` needed.

Runs as root directly — no need to switch to `node` first. `npm install` as
root is a standard, common pattern (this repo's own base images install
system tools as root without a non-root install step), and a package's
own install-time scripts (npm `postinstall` hooks) running as root inside
this already-isolated, no-project-mount, no-credential build environment
isn't a meaningfully different risk than running as `node` there — the
isolation, not the UID, is what's containing untrusted package-install code
per the existing "Build-network policy" section of `docs/session-images.md`.

A new script, `scripts/session/install-npm-packages.sh`, takes a JSON file
shaped `{"npm": [{"package": "...", "version": "..."}]}` and:

1. Runs `npm install --global --prefix <prefix> <pkg>@<version> ...` for
   the full list in one invocation.
2. `rm -rf` npm's own cache directory (`npm config get cache`, queried
   rather than hardcoded, in case the build environment's npm config
   differs from a bare default).

After install, the layer locks the prefix down: `chown -R root:root
/opt/claude-session/npm && chmod -R a-w /opt/claude-session/npm`, and the
Dockerfile sets `ENV PATH=/opt/claude-session/npm/bin:$PATH` in the final
stage so installed binaries are runnable without qualification. npm
versions are exact and mandatory in the schema already, so the installed
version *is* the requested version — no provenance-patching step needed for
npm, unlike apt.

## Provenance: patch `resolved.json` with actual apt versions

`resolve-image.sh` already writes `resolved.json` (including
`packages.apt`, currently an unmodified echo of the profile's request) into
the build context before `render-dockerfile.sh` runs — this doesn't change.
What changes is when the Dockerfile makes it read-only.

Today, `resolved.json` is copied in with `--chmod=0444` immediately (the
last or near-last line of the Dockerfile). For this task, when an apt layer
is present:

1. `resolved.json` is copied in **without** `--chmod=0444` (root-owned,
   normal writable mode) — positioned *before* the apt layer.
2. `install-apt-packages.sh` patches it in place as its own last step (see
   apt layer above).
3. The **last** step in the Dockerfile, after every present layer has had
   its chance to patch, does `RUN chmod 0444 /opt/session-profile/resolved.json`.

When no apt layer is present, `resolved.json` keeps today's behavior
(copied in already read-only) — no reason to add write-then-lock complexity
to a build that has nothing to patch.

## Validation

`validate-profile.sh`: remove `apt` and `npm` from the "reject if non-empty"
check (keep `python`). The existing structural validation for apt/npm
entries (name/version regexes, `keys` allowlisting) is already implemented
and unchanged.

Add one new check: reject duplicate package names within `apt` and within
`npm` (`(($list | map(.name) | length) == ($list | map(.name) | unique |
length))`-shaped, matching the dedup check `claude_marketplaces`' `plugins`
array already has in this same file). Installing the same package twice has
undefined/confusing behavior in both `apt-get` (implementation-dependent
whether the last spec wins or the command errors) and `npm` (silently
reinstalls, overwriting) — reject it explicitly rather than let it fail
obscurely at build time.

## Testing

- New valid fixtures: `scripts/session/fixtures/valid/apt-packages.json`
  (apt only), `scripts/session/fixtures/valid/npm-packages.json` (npm
  only), and a combined fixture exercising apt + npm + `claude_marketplaces`
  together to prove composability.
- New invalid fixtures: duplicate apt package name, duplicate npm package
  name.
- `test-validate-profile.sh` picks these up automatically (iterates
  `fixtures/valid/*.json` and `fixtures/invalid/*.json` generically).
- `test-render-dockerfile.sh`: extend for apt-only, npm-only, and the
  combined-with-marketplaces case — assert the right layers appear/don't,
  in the right stage, and that `resolved.json`'s COPY is writable-then-locked
  only when apt is present.
- New integration test(s), docker-gated (not msb — matches
  `test-session-marketplace.sh`'s reasoning: these only need Docker, and
  CI's runner doesn't have `msb`): build a session image with real apt and
  npm packages, assert the apt package's binary works and `dpkg-query`
  matches `resolved.json`'s recorded actual version, assert the npm
  package's `PATH`-resolved binary runs, and assert both `/opt/claude-session/npm`
  and system apt paths are unwritable by `node`.

## Cache key

`resolve-image.sh`'s `renderer_hash` already hashes `render-dockerfile.sh`
alongside the marketplace installer; extend it to also cover the two new
scripts (`install-apt-packages.sh`, `install-npm-packages.sh`), for the same
reason the others are hashed — a change to either should bust the
session-image cache.
