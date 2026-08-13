# Profile-driven ephemeral Claude session images

## Purpose

Claude sessions sometimes need additional tools without changing the neutral
runtime image. This design adds an explicit host-side `claude-session` launcher
that builds a cached, short-lived image derived from
`ai-sandboxes-claude:local`, then launches it with the existing hardened
Microsandbox policy.

The design supports real `apt` installation during an image build while
preserving the runtime boundary: the Claude process still runs as `node` in the
`restricted` security profile, receives no `sudo`, and cannot modify the
system toolchain at runtime.

This design replaces the former private-profile image-overlay mechanism.
Personal and team customization is an explicit, publicly validated JSON file
that can itself be kept in a private repository, rather than a repository
overlay that rebuilds the neutral base image from a pinned upstream commit.

## Non-goals

- Installing packages into a running Claude VM.
- Giving the agent `sudo` or a writable system prefix.
- Accepting arbitrary Dockerfiles, shell snippets, build arguments, or package
  source URLs from a profile.
- Automatically loading a profile from the mounted project directory.
- Supporting Codex session images in the first implementation.
- Supporting private or internal package sources: apt mirrors, private npm or
  PyPI registries, or any credentialed registry. Only public default sources
  are supported, regardless of where the profile file itself is stored.
- Publishing or pulling prebuilt session images through a registry. A session
  image is always resolved and cached locally on the host that runs it; teams
  share the (non-secret, validated) profile file, not a built image.

## Launcher flow

```text
explicit host profile path
  -> validate and canonicalize profile
  -> resolve cached or build derived image
  -> load that exact image into msb
  -> run the current Claude Microsandbox policy
```

The initial command is:

```fish
claude-session --profile /absolute/path/to/session.json [claude arguments...]
```

The profile path must be supplied explicitly by the host user. In particular,
the launcher must not discover `session.json` from the project mount: an agent
can modify project files and must not be able to influence a future host-side
build implicitly.

`claude-session` (and `claude`/`codex`) refuse to launch if the workspace they
would mount overlaps any protected ai-sandboxes path. The resolver and its
helpers run as host-side scripts with real Docker authority before the guest
ever starts; if the mounted project overlapped one of these paths, a guest
with write access to the mount could tamper with launcher code for a later
host invocation to trust. A check placed inside the checkout's own Fish files
cannot be the primary control here, since a guest with write access to those
files could simply edit the check back out along with anything else. The
actual trust boundary is `./scripts/install-fish-functions` (see the
top-level README): it copies (never symlinks) small wrapper functions and a
shared guard snippet to `~/.config/fish/functions/` and
`~/.config/ai-sandboxes/trusted/`, outside any checkout a guest could write
to, and the wrapper runs the overlap check *before* sourcing any
checkout-provided code at all. The protected paths are the ai-sandboxes
checkout itself *and* the wrapper/guard's own installed directories:
protecting only the checkout would still let a guest mounted at, say,
`~/.config/fish` tamper with the installed wrapper directly. The in-checkout
copies of the same check (`shell/fish/lib/ai-sandbox.fish`) remain as defense
in depth for direct or pre-wrapper invocations, not as the security boundary
itself.

The final `msb run` invocation retains the existing policy:

- `--user node`
- `--security restricted`
- the current project-mount, home-volume, and quota rules
- the current runtime network policy, including the separate
  `CLAUDE_MSB_PUBLIC_EGRESS=1` escape hatch

## Session profile schema

Version 1 uses JSON because the host-side tooling already depends on `jq`.
YAML is deferred rather than introducing a second parser dependency.

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
  "tools": [
    { "id": "rtk", "version": "v0.45.0", "sha256": "80a746dd305ef944ff50ef011ae4ce3878dd5ba88dfe35d859d05498191637c3" }
  ],
  "shared_state": {
    "id": "personal",
    "quota": "2G"
  },
  "claude_marketplaces": [
    {
      "url": "https://github.com/org/plugins.git",
      "ref": "full-40-character-commit-sha",
      "path": ".",
      "plugins": ["example-plugin"]
    }
  ]
}
```

Validation must reject unknown fields, credentials, arbitrary URLs, shell
syntax, package-manager options, local package files, source repository
changes, malformed package names/versions, and duplicate package names within
`apt` or within `npm`. Marketplace entries reuse the existing public-GitHub,
full-commit-SHA, safe-path, and plugin-name constraints. Profiles also have
package-count, field-length, and estimated-size limits.

`tools` selects zero or more curated, pinned binary tools from the
repository-controlled `config/tool-catalog.json` — this is not arbitrary
binary download support; a profile can only select a catalog `id` that
already exists, and can only pin a `version` and `sha256`, never a
repository, release asset name, archive path, adapter, or install command.
Generic Python packages, arbitrary GitHub releases outside the catalog,
private registries, and a new package manager are explicitly out of scope.
Each `tools` entry is validated by *delegating* to
`scripts/tools/validate-selection.sh` — the same script the base image's own
`config/tools.json`/`config/runtime.json` mechanism already uses (see
`images/tools/Dockerfile`) — rather than re-implementing its id/version/
sha256/shared-state regexes a second time in `validate-profile.sh`, which
would risk the two mechanisms drifting apart. `shared_state` reuses
`runtime.json`'s validation rules exactly: `id` matches
`^[a-z0-9][a-z0-9-]{0,62}$`, `quota` matches `^[1-9][0-9]*[KMGT]$`. A
catalog tool with `state_wrapper` (currently: `icm`) requires a non-null
`shared_state` in the same profile — this direction is enforced by
`scripts/tools/validate-selection.sh` itself. The reverse is *not* something
`scripts/tools/validate-selection.sh` enforces (the base-image mechanism has
no notion of "unused" shared state, since a host deliberately maintains
`config/runtime.json`), but a session profile is per-invocation, host-supplied
data, so `validate-profile.sh` adds a session-specific rule on top: a
`shared_state` request with no selected tool that actually needs it is
rejected, rather than silently provisioning an unused host-side volume.

Direct npm and Python package versions are mandatory and must be an exact
semantic version (optionally with a pre-release or build-metadata suffix,
e.g. `1.2.3-beta.1`); dist-tags and ranges (`latest`, `1.x`, `^1.2.3`) are
rejected, so `resolved.json` always records an exact, unambiguous direct
version rather than a moving target. This pin covers only the profile's
directly-requested package, not its transitive dependency tree: `npm
install` resolves each dependency's own version ranges at build time, and
neither the resulting dependency versions nor their integrity hashes are
locked or recorded in `resolved.json`, so two builds of an otherwise
unchanged profile can still install different transitive dependency
versions. Full reproducibility (a lockfile with integrity hashes, recorded
alongside the rest of the provenance) is a known limitation, not yet
implemented. Apt versions are supported and encouraged, but reproducibility
remains limited by the apt
repository state until a future snapshot-repository design is introduced.

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

## Storing and selecting profiles

This repository ships `config/session-profile.example.json`, following the
same convention as `config/marketplaces.example.json`, as a starting point
with no packages or marketplaces selected.

A profile is still always supplied explicitly (see Launcher flow); nothing is
auto-discovered from the project mount. `claude-session --profile <value>`
resolves `<value>` in one of two ways:

- a value containing `/` is used as a literal path, exactly as today;
- a bare name (no `/`) resolves to `~/.config/microvms/profiles/<name>.json`,
  mirroring the existing `~/.config/microvms/claude-egress` convention.

This lets a team keep its session profiles in a private repository, synced or
symlinked into `~/.config/microvms/profiles/`, and reference them by name. The
profile's contents are validated identically regardless of where the file is
stored: private storage changes who can read the file, not what the schema
allows it to contain.

## Build and image identity

The resolver calculates a session-image key from:

- digest of `ai-sandboxes-claude:local`;
- canonicalized profile bytes;
- target platform;
- profile-schema version; and
- renderer/launcher version.

It uses the key in an image tag such as
`ai-sandboxes-claude-session:sha-<hash>` and as image labels. An existing local
image is only a cache hit if its `io.ai-sandboxes.session-image` and
`io.ai-sandboxes.session-cache-key` labels match; a tag match alone is not
trusted, since a tag is a mutable pointer that something other than the
resolver could have written. A tag that exists but does not carry the
expected labels fails closed rather than being silently rebuilt over or
reused. `claude-session` also verifies the msb-side image after loading,
since msb keeps a separate image store from Docker's and `load-image.sh` (a
generic loader also used for non-session images) only checks whether *a*
image exists under the tag, not whether it is the right one. `msb load`
does not retain OCI labels, so this second check compares the preserved OCI
config digest reported by msb with Docker's image ID instead.

The same label-stripping behavior means `msb load` cannot carry a session
image's `shared_state` request the way the base image carries its own (fixed,
build-arg-derived) shared-state labels for `__ai_sandbox_shared_state_mount_args`
to read. Instead, `resolve-image.sh` prints a small JSON descriptor —
`{"image": "<tag>", "shared_state": {"id": "...", "quota": "..."}|null}` —
computed entirely from the already-validated, host-side canonical profile
snapshot, never from anything guest-controlled or re-read from disk after
validation. `claude-session` parses this descriptor and calls
`__ai_sandbox_shared_state_request_args` (a narrow, standalone helper that
validates the id/quota shape and builds the same `--mount-named
agent-state-<id>-v1:/var/lib/agent-state:kind=dir,quota=<quota>` argument the
label-based path produces) followed by the existing, unmodified
`__ai_sandbox_initialize_shared_state`. The base image's own
`claude`/`codex` launchers are unaffected: they still derive shared state from
msb image labels via `__ai_sandbox_shared_state_mount_args`, which this
mechanism does not touch.

Otherwise, the resolver creates a temporary build context that contains only
generated files and trusted installer scripts; it must never use the project
checkout as Docker context. Before building, the resolver also creates a
private `ai-sandboxes-claude-session-base:<hash>` tag pointing at the base
image's current content and verifies it still matches the digest the cache
key was computed from; the generated Dockerfile's `FROM` uses that private
pin, not the mutable `ai-sandboxes-claude:local` tag directly, so a
concurrent `./scripts/build` cannot
change what the build actually uses without also changing the cache key.

The derived Dockerfile begins with the already built Claude image. BuildKit
therefore reuses the existing operating-system, shared-tool, Claude, and baked
plugin layers; only requested layers are built. Apt, npm, Python, and plugin
steps are separate layers to maximize cache reuse.

Each derived image contains a root-owned `/opt/session-profile/resolved.json`
recording the canonical request, base digest, installed package inventories,
resolved plugin commits, build time, and launcher version.

## Package layers

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

Apt packages are trusted build-time code, not merely data: `apt-get
install` runs as root, and a package's maintainer scripts (`postinst` and
friends) run as root too, with no sandboxing beyond the build environment
itself. A profile author can therefore request anything the public apt
repositories serve, including packages that introduce their own privileged
artifacts (`sudo`, setuid/setgid binaries, Linux capabilities) into the
image. This is the same trust relationship as a host running `docker build`
with their own Dockerfile — session profiles are host-supplied input, never
guest/agent-discoverable (see "Launcher flow" above) — so it does not open a
new privilege-escalation path for the guest agent, which still runs as
`node` under the unchanged `restricted` runtime policy regardless of what
the image contains. It does mean a host can silently bake privileged
artifacts into an image without any explicit signal that they did so; there
is currently no allowlist, deny-list, or post-install policy check against
this.

### npm

npm packages install during the build into an image-local prefix at
`/opt/claude-session/npm`, via a single `npm install --global --prefix`
invocation. The final prefix is root-owned and read-only
(`chown -R root:root` + `chmod -R a-w`), with its `bin` directory appended
to `PATH` — appended, not prepended, so a session-installed package can
never shadow a base-image command the harness itself depends on (`claude`,
`git`, `curl`, ...); the base image's own directories are always resolved
first. A global-prefix install produces `<prefix>/bin/<binary>` as a
relative symlink into `<prefix>/lib/node_modules/<package>/...`, which
resolves correctly regardless of where the prefix ends up in the final
image.

### Python

When `python.enabled` is true, the build installs the selected Python runtime
and venv support through apt, creates an image-local virtual environment at
`/opt/claude-session/python`, and installs validated Python dependencies there.
The final virtual environment is root-owned and read-only, and its bin directory
is added to `PATH`.

### Curated tools

`tools` entries install via the exact same trusted chain the base image's
`config/tools.json`/`config/runtime.json` mechanism already uses:
`scripts/tools/install-selected.sh` (phase `runtime`) and
`scripts/tools/install-github-release-tar.sh`, both copied into the build
context unmodified, alongside a copy of the trusted
`config/tool-catalog.json` and a profile-derived selection document. This
layer runs after npm and before the marketplace layer, under `USER root` in
the final stage (not a discarded build stage, since a tool's installed
footprint under `/usr/local/bin`/`/usr/local/libexec` isn't confined to one
copyable directory the way npm's prefix is) — unlike npm's registry
installs, a github-release-tar install runs no third-party lifecycle hooks
and downloads only a fixed, catalog-pinned URL whose SHA-256 is verified
before extraction, so it carries none of npm's "arbitrary install-time code"
risk and does not need npm's node-then-relock privilege dance.

Installed binaries land under root-owned, non-writable paths exactly as the
base image's runtime tool mechanism already does: a tool with no
`state_wrapper` (`rtk`) installs directly to `/usr/local/bin/<binary>`,
runnable by `node`. A tool with `state_wrapper` (`icm`) installs its real
binary to `/usr/local/libexec/<binary>` and a launcher symlink at
`/usr/local/bin/<binary>` pointing at the shared
`/usr/local/libexec/ai-sandboxes-state-wrapper`, which — except for a
`--version` bypass — refuses to run unless `/var/lib/agent-state` is
present and writable, then exports the tool's configured environment
variable (`ICM_DB_PATH` for `icm`) pointing under
`/var/lib/agent-state/<state directory>/`, and only then execs the real
binary. Direct use of `icm` therefore fails safely with no shared-state
mount, and only `/var/lib/agent-state` is ever writable for its state —
`/usr/local/bin` and `/usr/local/libexec` themselves are never made
writable to `node`. `resolved.json` records each selected tool's exact
`id`/`version`/`sha256`, the catalog's `binary` name for it
(`packages.tools`), a `tool_catalog_sha256` identifying the exact catalog
content used, and the requested `shared_state` (or `null`).

### Claude marketplaces and plugins

Session marketplaces reuse the existing pinned installer
(`scripts/marketplaces/install-claude.sh`, copied into the build context
unmodified) and install **additively into the base image's own**
`/opt/claude-plugin-cache` and `/opt/claude-plugin-seed`, not a second,
session-specific path. This was not the original design — an earlier version
of this mechanism used a separate `/opt/claude-session/plugin-cache` and
`/opt/claude-session/plugin-seed`, merged into `settings.json` at container
launch. That design shipped a marketplace registration
(`extraKnownMarketplaces` in `settings.json`) that looked correct but never
actually loaded: Claude resolves a registered marketplace's code relative to
`CLAUDE_CODE_PLUGIN_CACHE_DIR` at runtime, and that variable only ever points
at the base image's cache directory — a marketplace cloned into a second,
unreferenced root registers cleanly and shows as enabled, but its code is
unreachable. This was confirmed empirically (overriding
`CLAUDE_CODE_PLUGIN_CACHE_DIR` at runtime to point at the separate session
cache made the marketplace appear; leaving it at the base path did not),
not just inferred from documentation.

The fix installs into the same cache Claude already reads. Those base paths
are read-only in the base image, so the session build stage temporarily
reclaims write access (`chown`/`chmod` back to node-writable, as root, before
switching to `USER node` to run the installer — the same lifecycle the base
image's own build already uses, just re-opened for one more layer), installs
the profile's marketplaces alongside whatever the base image already has,
merges the resulting `settings.json` with any pre-existing base seed via
`scripts/session/merge-plugin-seed.sh` (session values winning on conflicts,
matching the original "session is additive, layers on top of the base"
intent — the mechanism moved from runtime to build time, not the
precedence), and re-locks (`chown root:root`, `chmod -R a-w`) before the
final stage copies the augmented directories back to their standard paths.
The install runs in a discarded build stage with a throwaway `HOME`,
mirroring `images/claude/Dockerfile`'s own build/final split.

Because the session build produces one already-merged seed at the standard
`CLAUDE_CODE_PLUGIN_SEED_DIR` path, it is indistinguishable at runtime from a
base image that shipped with those marketplaces built in.
`images/claude/entrypoint.sh` needs no session-specific logic: it reads
`CLAUDE_CODE_PLUGIN_SEED_DIR` (required, already exported by the Dockerfile)
and merges that one seed into `settings.json` on every launch via a
recursive merge (jq's `*` operator, right side winning per key), the user's
already-persisted settings taking precedence over the seed and unrelated
Claude settings left untouched — the same shape this file used before this
mechanism existed, just reading the seed path from an environment variable
instead of a hardcoded path, and merging recursively (covering
`extraKnownMarketplaces` alongside `enabledPlugins`) instead of only the
single `enabledPlugins` key.

## Build-network policy

Package and plugin installation needs build-time egress, which is separate from
the guest VM's runtime network policy. A cache miss that requires a build needs
an explicit host opt-in:

```fish
CLAUDE_MSB_BUILD_EGRESS=1 claude-session --profile ...
```

A cache hit needs no build egress. `CLAUDE_MSB_PUBLIC_EGRESS=1` does not imply
authority to run a host-side build.

Builds must receive no project mount, host home mount, Docker/registry
credentials, SSH agent, BuildKit secret, or profile-supplied build mount. The
profile is trusted host-user input, but package and plugin install scripts remain
untrusted code; these restrictions contain that code to the build environment.

## msb loading and cleanup

Derived images are imported into msb under their exact session tag. The loader
must not overwrite or remove `ai-sandboxes-claude:local`; it skips import when
the matching session image is already present.

There is no automatic session-image garbage collector yet. Derived images stay
in the local Docker and msb image stores until the host user removes a known
session tag; the base, tools, Claude, and Codex image tags must never be used
as cleanup targets. Automatic, dry-run-first garbage collection and host-local
last-used metadata remain future work.

## Implementation tasks

The tasks below are independently reviewable. Parenthetical dependencies name
the tasks that must land first.

1. **Profile schema and fixtures** (independent)
   - Add a version-1 JSON schema document, valid examples, and invalid fixtures.
   - Ship `config/session-profile.example.json` as the user-facing starting
     point, alongside the schema's test fixtures.
   - Define limits and the public error contract.

2. **Profile validation and canonicalization** (depends on 1)
   - Implement `jq`/Bash validation and canonical JSON generation.
   - Add stable-hash and injection-rejection tests.

3. **Safe Dockerfile renderer** (depends on 1, 2)
   - Generate a minimal Dockerfile and build context from trusted templates.
   - Unit-test escaping and assert that only validated structured values reach
     package-manager commands.

4. **Session image resolver and cache key** (depends on 2, 3)
   - Resolve the base image digest, calculate the tag/labels, add per-key
     locking, build on a cache miss, and write resolved provenance.

5. **Exact msb image loader** (independent)
   - Extract reusable load logic from `scripts/load-msb`.
   - Load/inspect a distinct session tag without replacing base-image tags.

6. **`claude-session` Fish launcher** (depends on 4, 5)
   - Parse explicit profile arguments and build-egress opt-in.
   - Resolve a bare `--profile` name against
     `~/.config/microvms/profiles/<name>.json`; treat any value containing
     `/` as a literal path.
   - Reuse the existing Claude network/mount/security construction verbatim
     after resolving the image.

7. **Empty-profile end-to-end verification** (depends on 4, 5, 6)
   - Build and launch an empty-profile session image.
   - Assert runtime user, no sudo, non-writable system paths, cache hit, and
     distinct msb tag behavior.

8. **Apt and npm derived layers** (depends on 3, 4)
   - Implement structured apt and npm installation, immutable final prefixes,
     inventories, and integration tests.

9. **Python derived layer** (depends on 8)
   - Implement Python bootstrap, image-local venv, pinned package install, and
     validation/integration coverage.

10. **Session Claude marketplace/plugin overlay** (depends on 3, 4)
    - Reuse the existing marketplace validation/installation model to install
      a session profile's marketplaces additively into the base image's own
      `/opt/claude-plugin-cache`/`/opt/claude-plugin-seed` at build time
      (not a separate session-specific cache/seed path merged at runtime —
      Claude resolves a marketplace's code relative to
      `CLAUDE_CODE_PLUGIN_CACHE_DIR` at runtime, so a separate root nothing
      points to would register but never load; see "Claude marketplaces and
      plugins" above), and test that the session marketplace's plugin is
      enabled after a fresh-home launch.
    - Make the entrypoint read `CLAUDE_CODE_PLUGIN_SEED_DIR` from the
      environment instead of a hardcoded path, and merge that seed
      recursively (covering `extraKnownMarketplaces` alongside
      `enabledPlugins`) instead of only a single hardcoded key.

11. **Image GC and host-local metadata** (future work; depends on 4, 5, 6)
    - Implement discovery, dry-run output, `--apply`, TTL/size policy, and
      safety tests.
    - Store last-used metadata at `~/.cache/ai-sandboxes/session-images.json`,
      updated on every launch.

12. **Documentation, verification, and rollout** (completed except for the
    future GC work in task 11)
    - Keep organization policy in a private session-profile repository when
      appropriate, but keep credentials out of profiles and configuration.
      README, `docs/configuration.md`, and `docs/claude-security.md` describe
      session profiles as the customization mechanism.
    - Add shell/fish checks and networked integration-test policy.
    - Document rollback: use `claude` with the base image and delete only
      labeled session images.

## Suggested delivery order

Land tasks 1-7 as the minimum vertical slice. It introduces the explicit
profile, cache identity, isolated build context, distinct msb loading, and
hardened launch without granting additional package capabilities. Then add apt
and npm (task 8), plugins (task 10), Python (task 9), and cleanup/documentation
(tasks 11-12).

This order makes the first user-visible feature deliberately small while
preserving the important invariant: session customization is an explicit,
cached host-side image build, never a privilege grant to the guest agent.
