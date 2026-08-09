# Agent profile example

This directory is a runnable starting point for a private personal or team
profile repository. Copy its contents into a new private repository; do not
build it in place or commit personal selections back to `ai-sandboxes`.

The profile keeps policy separate from the neutral runtime. It pins an exact
public upstream commit, overlays only its three configuration manifests, builds
and verifies the result in a temporary checkout, then optionally publishes
immutable private images.

## Set up the profile

1. Copy this directory's contents into a new private Git repository.
2. Replace the placeholder `UPSTREAM_REF` in `upstream.lock` with a reviewed,
   lowercase, full 40-character commit SHA from `rikdc/ai-sandboxes`.
3. Select public marketplaces, audited tools, and optional shared state in the
   files under `config/`. The supplied selections are intentionally empty.
4. Review [the private profile guide](../../docs/private-profiles.md) before
   building or publishing.

`upstream.lock` deliberately causes the scripts to fail until you set a commit
SHA. Do not replace it with a branch, tag, or abbreviated SHA.

## Build and verify locally

Install Git, Docker with Buildx, and `jq`, then run:

```console
./scripts/build-profile
```

The script creates and removes a temporary upstream checkout. It never pushes
an image.

## Publish immutable images

The example workflow publishes on pushes to `main`, version tags beginning
with `v`, and manual dispatch. It builds and verifies before publishing these
private GHCR tags:

```text
ghcr.io/OWNER/agent-profile-claude:sha-PROFILE_COMMIT
ghcr.io/OWNER/agent-profile-codex:sha-PROFILE_COMMIT
```

For an intentional local publish, authenticate Docker separately and run:

```console
GHCR_NAMESPACE=YOUR_GITHUB_USER ./scripts/publish-profile sha-PROFILE_COMMIT
```

Resolve a published tag to an image digest before deployment. Pull that digest,
tag it locally as `ai-sandboxes-claude:local` or `ai-sandboxes-codex:local`,
then run the upstream `./scripts/load-msb` command and its Fish launchers.

## Security boundary

Keep actual marketplace selections, tool pins, shared-state IDs, registry
namespaces, deployment digests, and credentials in the private profile
repository. Marketplace sources must be canonical public GitHub URLs and may
not contain credentials. Private marketplace sources are intentionally
unsupported, and Docker build arguments must never carry secrets.
