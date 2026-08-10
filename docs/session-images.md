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

This design replaces the private-profile repository mechanism previously
documented in `docs/private-profiles.md` (scheduled for removal once this
mechanism lands; see Implementation task 12). Personal and team
customization moves entirely to session profiles: a small, publicly validated
JSON file that can itself be kept in a private repository, rather than a full
repository overlay that rebuilds the base image from a pinned upstream commit.

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

Before running any resolver script, `claude-session` also refuses to launch if
the workspace it would mount overlaps the ai-sandboxes checkout providing the
launcher itself. The resolver and its helpers run as host-side scripts with
real Docker authority before the guest ever starts; if that checkout were also
the mounted project, a guest with write access to the mount could tamper with
those scripts for a later host invocation to trust. The base `claude`/`codex`
launchers carry the same check for the same reason, at smaller blast radius
since they only re-source Fish functions rather than exec host-side scripts.

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
changes, and malformed package names/versions. Marketplace entries reuse the
existing public-GitHub, full-commit-SHA, safe-path, and plugin-name constraints.
Profiles also have package-count, field-length, and estimated-size limits.

Direct npm and Python package versions are mandatory. Apt versions are
supported and encouraged, but reproducibility remains limited by the apt
repository state until a future snapshot-repository design is introduced.

The renderer in this vertical slice only ever emits the fixed, package-free
Dockerfile described under Implementation task 7; it does not yet install
`apt`, `npm`, `python`, or `claude_marketplaces` entries. Validation therefore
rejects any profile with a non-empty `apt`, `npm`, `python`, or
`claude_marketplaces` field rather than accepting and silently dropping it.
Only the empty profile (`{"schema_version": 1}`) validates until tasks 8-10
land.

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
reused. `claude-session` applies the same label check to the msb-side image
after loading, since msb keeps a separate image store from Docker's and
`load-image.sh` (a generic loader also used for non-session images) only
checks whether *a* image exists under the tag, not whether it is the right
one.

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

### npm

npm packages install during the build into an image-local prefix such as
`/opt/claude-session/npm`. The final prefix is root-owned and read-only, with
the selected bin directory added to `PATH`. The precise bin shim layout will be
verified against npm before implementation.

### Python

When `python.enabled` is true, the build installs the selected Python runtime
and venv support through apt, creates an image-local virtual environment at
`/opt/claude-session/python`, and installs validated Python dependencies there.
The final virtual environment is root-owned and read-only, and its bin directory
is added to `PATH`.

### Claude marketplaces and plugins

Session marketplaces reuse the existing pinned installer model, but install
into a second, session-specific cache and seed path rather than the base
image's `/opt/claude-plugin-cache` and `/opt/claude-plugin-seed`. The session
build sets a distinct `CLAUDE_CODE_SESSION_PLUGIN_SEED_DIR` (and matching
session plugin-cache variable) to that path; the base image's own
`CLAUDE_CODE_PLUGIN_SEED_DIR` is untouched, so both seeds are present in the
final image.

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

`claude-session gc` removes only labeled session images according to a default
14-day last-used TTL and a configurable total-size budget. Deletion requires an
explicit `--apply`; base, tools, Claude, and Codex images are never candidates.
Last-used metadata is host-local — at `~/.cache/ai-sandboxes/session-images.json`,
keyed by image tag — and is never stored in the agent home volume. Each
successful `claude-session` launch updates that file's entry for the image it
ran.

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
    - Make the entrypoint merge the base seed with an optional session seed
      (`CLAUDE_CODE_SESSION_PLUGIN_SEED_DIR`), session values taking
      precedence, instead of hardcoding a single seed path.
    - Reuse the existing marketplace validation/installation model for session
      cache/seed paths and test that base and session plugins are both
      enabled after a fresh-home launch.

11. **Image GC and host-local metadata** (depends on 4, 5, 6)
    - Implement discovery, dry-run output, `--apply`, TTL/size policy, and
      safety tests.
    - Store last-used metadata at `~/.cache/ai-sandboxes/session-images.json`,
      updated on every launch.

12. **Documentation, verification, and rollout** (depends on 7-11)
    - Remove `docs/private-profiles.md`; migrate its still-relevant guidance
      (keeping org policy out of the public repo, no credentials in
      configuration) into this document. Update README, `docs/configuration.md`,
      `docs/claude-security.md`, and issue #27 to describe session profiles as
      the customization mechanism.
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
