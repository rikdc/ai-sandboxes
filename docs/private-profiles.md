# Private profiles

Use a private profile repository when you want to keep personal or team agent
policy separate from this public runtime. A profile selects reviewed
marketplaces, optional tools, and shared-state settings; it is not a fork and
does not copy or modify the upstream source.

The profile pins one exact upstream commit, overlays its three configuration
files onto a temporary checkout of that commit, and builds the resulting
images. The pin makes the profile's configuration schema and build behavior
reproducible.

## Profile repository layout

Create a private repository with this minimal layout:

```text
agent-profile/
├── config/
│   ├── marketplaces.json
│   ├── runtime.json
│   └── tools.json
└── upstream.lock
```

Start the three configuration files from this repository's
`config/*.example.json` files. They contain only selections that the pinned
upstream already supports:

- Marketplace and skill sources must be canonical public GitHub URLs pinned to
  full commit SHAs. Do not put credentials in URLs or manifests.
- Tool selections must use the pinned upstream's `config/tool-catalog.json`.
  A profile cannot introduce arbitrary installers, commands, or source URLs.
- Shared state is intentionally shared by all images with the same profile ID.
  Keep credentials out of it and use a distinct, non-secret ID for each
  profile.

Keep the upstream choice in `upstream.lock` as two literal values:

```text
UPSTREAM_REPOSITORY=https://github.com/rikdc/ai-sandboxes.git
UPSTREAM_REF=FULL_40_CHARACTER_COMMIT_SHA
```

Review the selected upstream commit before changing the pin. Do not use a
branch, tag, or abbreviated SHA: a profile is compatible only with the exact
upstream revision it declares.

## Build and verify

The profile's build script should perform these steps:

1. Create a temporary directory and clone the public upstream without using
   the profile repository as a build context.
2. Check out `UPSTREAM_REF` detached and verify that `HEAD` equals the locked
   40-character SHA.
3. Confirm that the three expected upstream configuration files exist, then
   replace them with the profile's `config/` files.
4. Run the upstream `./scripts/build` and `./scripts/verify` from that
   temporary checkout.
5. Always remove the temporary directory on exit.

Validate profile manifests before copying them when practical. In particular,
reject credentials in URLs and marketplace sources outside public GitHub. Do
not pass secrets as Docker build arguments; this project does not support
private marketplace sources.

## Publish and consume images

Publish profile images to a private registry only after build and verification
succeed. Use immutable tags that include the profile commit, for example:

```text
ghcr.io/OWNER/agent-profile-claude:sha-PROFILE_COMMIT
ghcr.io/OWNER/agent-profile-codex:sha-PROFILE_COMMIT
```

Do not publish a moving `latest` tag. Resolve the selected immutable tag to a
digest before deployment, pull that digest, then tag it locally as
`ai-sandboxes-claude:local` or `ai-sandboxes-codex:local`. The existing
`./scripts/load-msb` command and Fish launchers can then be used unchanged.

Use a narrowly scoped registry credential on the host for private image pulls.
Never mount that credential into an agent environment.

## Keep policy private

This repository provides the neutral runtime, the configuration schemas, and
sanitized examples. Keep actual marketplace selections, tool pins, shared-state
identifiers, registry namespaces, deployment digests, and any organization
policy in the private profile repository. That prevents accidental disclosure
and lets the runtime evolve independently of profile-specific choices.
