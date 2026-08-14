# Session images

A **session image** is a short-lived Docker image, derived from
`ai-sandboxes-claude:local`, that adds a host-approved set of apt packages,
npm packages, curated tools, and Claude plugins to a Claude session without
changing the neutral runtime image or the guest's runtime authority. The
Claude process still runs as `node` under the `restricted` Microsandbox
policy; nothing in a session image grants the guest `sudo` or a writable
system prefix.

Session images are launched with:

```fish
claude-session --profile <name-or-path> [claude arguments...]
```

## Security invariants

These are the load-bearing properties of the design. Each links to the ADR
that argues it.

1. **A writable checkout must not contain host-trusted launch code.** The
   wrapper the host runs is installed outside every checkout by
   `./scripts/install-fish-functions`; a guest with write access to the
   project mount cannot edit it. See
   [ADR-0001](adr/0001-explicit-host-profile-path.md).
2. **Guest home state and VM root state have different lifetimes.** Session
   images bake tools into image layers (rebuilt on profile change); guest
   home state persists across launches independently.
3. **Build egress and runtime egress are separate authorities.**
   `CLAUDE_MSB_BUILD_EGRESS=1` authorises a host-side image build;
   `CLAUDE_MSB_PUBLIC_EGRESS=1` authorises the guest's runtime network. One
   never implies the other. See [ADR-0005](adr/0005-build-context-isolation.md).
4. **Mutable image tags are not identities.** The resolver trusts an image
   only when its `io.ai-sandboxes.session-image` and
   `io.ai-sandboxes.session-cache-key` labels match; a bare tag match fails
   closed. See [ADR-0003](adr/0003-label-verified-cache-keys.md).
5. **Plugin dependencies must exist before an ephemeral session starts.**
   Session marketplaces install additively into the base image's own
   `/opt/claude-plugin-cache` at build time, so plugins are present on first
   launch of a fresh guest home. See
   [ADR-0002](adr/0002-session-plugins-share-base-cache.md).
6. **Credentials inside the guest are readable by guest code.** Profiles
   therefore cannot carry credentials, private-registry URLs, or any
   authenticated source; only public default package sources are supported.
7. **The project mount is destructible from within the guest regardless of
   the VM boundary.** The launcher refuses to mount a workspace that
   overlaps any protected ai-sandboxes path (the checkout itself *and* the
   installed wrapper directories), because a guest with write access to
   those files could tamper with launcher code the host trusts on a later
   invocation. See [ADR-0001](adr/0001-explicit-host-profile-path.md).

## Launcher flow

```text
explicit host profile path
  -> validate and canonicalize profile
  -> resolve cached or build derived image
  -> load that exact image into msb
  -> run the current Claude Microsandbox policy
```

The profile path must be supplied explicitly by the host user; nothing is
auto-discovered from the project mount. `--profile` accepts either:

- a value containing `/` — used as a literal path;
- a bare name — resolved as `~/.config/ai-sandboxes/profiles/<name>.json`.

The final `msb run` invocation reuses the existing Claude policy verbatim:
`--user node`, `--security restricted`, and the current project-mount,
home-volume, quota, and runtime-network rules.

## Profile schema (version 1)

Profiles are JSON. YAML is not supported — see
[ADR-0006](adr/0006-json-not-yaml.md).

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

### Field reference

| Field | Required | Constraints |
| --- | --- | --- |
| `schema_version` | yes | Must be `1`. |
| `apt[].name` | yes | Debian package name. Validated against a package-name regex. |
| `apt[].version` | no | If set, exact apt version. Even pinned versions can still resolve differently as apt state changes; the actually-installed version is recorded to `resolved.json` post-install. |
| `npm[].package` | yes | Public npm package name. |
| `npm[].version` | yes | Exact semver, optionally with pre-release or build-metadata suffix. Dist-tags and ranges (`latest`, `^1.2.3`, `1.x`) are rejected. Transitive dependencies are not pinned — see Limitations. |
| `python.enabled` | no | Until the Python layer ships, any non-empty `python` field is rejected. |
| `python.packages[].package` | yes (if `enabled`) | Public PyPI package name. |
| `python.packages[].version` | yes (if `enabled`) | Exact version; ranges rejected. |
| `tools[].id` | yes | Must be an `id` present in the repository-controlled `config/tool-catalog.json`. |
| `tools[].version` | yes | Exact catalog version. |
| `tools[].sha256` | yes | Hex sha256 of the catalog artifact. |
| `shared_state.id` | yes (if set) | Matches `^[a-z0-9][a-z0-9-]{0,62}$`. |
| `shared_state.quota` | yes (if set) | Matches `^[1-9][0-9]*[KMGT]$`. Required when any selected tool has a `state_wrapper`; a `shared_state` block with no tool that needs it is rejected. |
| `claude_marketplaces[].url` | yes | Public GitHub URL. |
| `claude_marketplaces[].ref` | yes | Full 40-character commit SHA. |
| `claude_marketplaces[].path` | yes | Safe relative path within the repo. |
| `claude_marketplaces[].plugins` | yes | Non-empty list of plugin names. |

Validation rejects unknown fields, credentials, arbitrary URLs, shell
syntax, package-manager options, local package files, source-repository
overrides, malformed names/versions, and duplicate package names within
`apt` or within `npm`. Profiles also have package-count, field-length, and
estimated-size limits. `tools` validation is delegated to
`scripts/tools/validate-selection.sh` — the same script the base image's
own `config/tools.json`/`config/runtime.json` mechanism already uses — so
the two mechanisms cannot drift apart.

## Lifecycle

### 1. Build (cache miss only)

The resolver computes a cache key from: the digest of
`ai-sandboxes-claude:local`, the canonicalized profile bytes, the target
platform, the profile-schema version, and the renderer/launcher version.
The key becomes the tag `ai-sandboxes-claude-session:sha-<hash>` and is
also written as image labels.

On a cache miss:

- The build requires `CLAUDE_MSB_BUILD_EGRESS=1`. A cache hit needs no
  build egress.
- The resolver creates a private `ai-sandboxes-claude-session-base:<hash>`
  tag pointing at the base image's current content, verifies the digest
  still matches the cache key, and uses that private pin in the generated
  Dockerfile's `FROM`. A concurrent `./scripts/build` cannot change what
  the build actually uses without also changing the cache key.
- The build context contains only generated files and trusted installer
  scripts. It receives no project mount, host home mount, Docker or
  registry credentials, SSH agent, BuildKit secret, or profile-supplied
  build mount. See [ADR-0005](adr/0005-build-context-isolation.md).
- Layers are ordered apt → npm → tools → marketplaces, in the final stage,
  regardless of the profile's field order. Each is a separate layer for
  cache reuse.
- Each derived image contains a root-owned
  `/opt/session-profile/resolved.json` recording the canonical request,
  base digest, installed package inventories, resolved plugin commits,
  build time, and launcher version.

### 2. Cache resolution

An existing local image is a cache hit only when its
`io.ai-sandboxes.session-image` and `io.ai-sandboxes.session-cache-key`
labels match the computed key. A tag that exists without the expected
labels fails closed — it is not silently rebuilt over or reused. See
[ADR-0003](adr/0003-label-verified-cache-keys.md).

### 3. msb load

Session images are loaded into msb under their exact session tag; the
loader must not touch `ai-sandboxes-claude:local` or other base tags. Load
is skipped when the matching session image is already present.

`msb load` strips OCI labels, so `claude-session` performs a second
verification after loading: it compares the preserved OCI config digest
reported by msb with Docker's image ID. This is why shared-state can't
piggyback on msb image labels. For session images, `resolve-image.sh`
prints a small JSON descriptor with the shared-state request, computed from
the already-validated host-side profile snapshot, and `claude-session`
builds the shared-state mount args from that. For base `claude`/`codex`
images, the launcher reads shared state from the trusted checkout's
`config/runtime.json` and verifies the digest match before applying it.

### 4. Run

The guest runs under the unchanged Microsandbox policy: `--user node`,
`--security restricted`, existing mounts, quotas, and runtime network
policy. The image content is trusted host-supplied build output; the
runtime authority of the guest is unchanged.

### 5. Cleanup

There is no automatic session-image garbage collector. Derived images stay
in the local Docker and msb image stores until the host user removes a
known session tag. The base, tools, Claude, and Codex image tags must
never be used as cleanup targets.

## Package layers

### apt

Installed under `USER root` from validated specifications, apt lists
removed afterwards, then back to `USER node`. The renderer (not the
profile) constructs command syntax. There is no runtime `apt-get`
capability. After install, `scripts/session/install-apt-packages.sh`
queries `dpkg-query` for the actual installed version of each package and
patches `resolved.json` before it is locked read-only.

apt packages are trusted build-time code: maintainer scripts (`postinst`,
etc.) run as root in the build environment, and a profile author can bake
in privileged artifacts (setuid binaries, capabilities). This does not
change the guest's runtime authority. See
[ADR-0004](adr/0004-apt-npm-tools-trust-tiers.md).

### npm

Installed to an image-local prefix at `/opt/claude-session/npm` via a
single `npm install --global --prefix` invocation. The final prefix is
root-owned and read-only. Its `bin` is *appended* to `PATH` — not
prepended — so a session-installed package cannot shadow a base-image
command the harness depends on (`claude`, `git`, `curl`, ...).

### Python

When `python.enabled` is true, the build installs the Python runtime and
venv support through apt, creates an image-local venv at
`/opt/claude-session/python`, and installs validated packages there. The
venv is root-owned and read-only; its `bin` is added to `PATH`. Not yet
implemented — see Limitations.

### Curated tools

`tools` entries install through the exact chain the base image's
`config/tools.json`/`config/runtime.json` mechanism uses:
`scripts/tools/install-selected.sh` (phase `runtime`) dispatches to one of
several adapter installers — currently `install-github-release-tar.sh`,
`install-https-tar.sh`, and `install-awscli-zip.sh` — all copied
unmodified into the build context alongside a copy of
`config/tool-catalog.json`. Which adapter a catalog entry uses is fixed
by the catalog (`adapter` field), never by the profile; a profile can
only select a catalog `id` and pin its `version`/`sha256`. Every
download is verified against the profile-pinned sha256 before anything
in the archive is read. `github-release-tar` and `https-tar` extract a
known member and place it themselves; `awscli-zip` is a distinct trust
tier — AWS CLI v2 has no movable-binary distribution, so the
checksum-pinned archive's own `aws/install` script runs as root during
the image build to lay out its self-contained `dist/` tree and symlinks.
That is the entire third-party lifecycle surface across the tool
adapters and it is confined to that one catalog entry.

Installed binaries land under root-owned, non-writable paths. A tool with
no `state_wrapper` installs to `/usr/local/bin/<binary>` (for a plain
archive member) or to a self-contained prefix under
`/usr/local/libexec/ai-sandboxes-tools/<tool-id>` with the catalog's
`expose` list symlinked into `/usr/local/bin` (for a directory member,
e.g. the Go toolchain). The prefix is namespaced by catalog `id` (not
by the archive's own top-level directory) so two tools whose archives
happen to share a generic name — `bin`, `linux-arm64`, `tool` — cannot
overwrite each other, and only the names the catalog explicitly exposes
end up on `PATH`. The AWS CLI v2 installer lays out its own
`/usr/local/aws-cli/v2/<version>` tree and points `/usr/local/bin/aws`
at it directly. A tool with a `state_wrapper` (e.g. `icm`) installs its real binary
to `/usr/local/libexec/<binary>` and a launcher symlink at
`/usr/local/bin/<binary>` that refuses to run unless
`/var/lib/agent-state` is present and writable. Adapters whose member
shape is a directory (`https-tar` directory members, `awscli-zip`) cannot
be paired with a `state_wrapper`; validation rejects such catalog
entries. See [ADR-0004](adr/0004-apt-npm-tools-trust-tiers.md).

### Claude marketplaces

Session marketplaces install additively into the base image's own
`/opt/claude-plugin-cache` and `/opt/claude-plugin-seed` — not a
session-specific path. The build stage temporarily reclaims write access
to those base paths, runs the pinned installer
(`scripts/marketplaces/install-claude.sh`), merges the resulting
`settings.json` with the base seed via
`scripts/session/merge-plugin-seed.sh` (session values winning on
conflicts), and re-locks before the final stage copies the augmented
directories back to their standard paths.

At runtime, the entrypoint reads `CLAUDE_CODE_PLUGIN_SEED_DIR` from the
environment and merges that one seed into `settings.json` on every launch
with jq's recursive `*` operator (right side winning per key). The
user's already-persisted settings take precedence over the seed. See
[ADR-0002](adr/0002-session-plugins-share-base-cache.md) for why sessions
share the base cache instead of layering a separate one at runtime.

## Rollback

Fall back to `claude` with the base image directly. Delete only labelled
session images (`ai-sandboxes-claude-session:sha-<hash>`) — never the
base, tools, Claude, or Codex tags.

## Limitations

- **npm and Python transitive dependencies are not locked.** Only directly
  requested versions are pinned; two builds of an unchanged profile can
  still install different transitive versions. Full lockfile + integrity
  provenance is future work.
- **apt reproducibility is bounded by apt repository state.** A pinned
  apt version can still resolve differently over time. A snapshot-repository
  mechanism is future work.
- **No Python layer yet.** Any non-empty `python` field is rejected during
  validation.
- **No automatic image garbage collection.** Host users must remove
  session tags manually.
- **No Codex session images yet.**
- **No private-registry support.** Only public default package sources
  are supported. Private storage of the profile file itself is fine, but
  the profile's contents must still resolve through public sources.
