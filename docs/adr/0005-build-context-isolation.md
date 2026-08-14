# ADR-0005: Build context is isolated from host and guest state

## Status

Accepted.

## Context

Session images are built on the host, using host tools, on a host user's
authority. Package and plugin install scripts running inside those
builds are not host-authored code, though — they are third-party install
scripts from public registries. The build environment is the containment
for that code, and its inputs need to be scoped accordingly. Build-time
network access is likewise its own authority, distinct from what the
running guest is allowed to reach.

## Decision

**Build egress is a separate opt-in.** A cache miss that needs a build
requires `CLAUDE_MSB_BUILD_EGRESS=1` on the host invocation. A cache hit
needs no build egress. `CLAUDE_MSB_PUBLIC_EGRESS=1` (the guest's
runtime-network opt-in) does not imply authority to run a host-side
build, and vice versa.

**Builds receive no ambient authority.** No project mount, no host home
mount, no Docker or registry credentials, no SSH agent, no BuildKit
secret, no profile-supplied build mount. The build context contains
only generated files and trusted installer scripts. The profile is
trusted host-user input; package and plugin install scripts remain
untrusted code, and these restrictions contain that code to the build
environment.

**The base-image reference is pinned per-build.** Before building, the
resolver creates a private `ai-sandboxes-claude-session-base:<hash>` tag
pointing at the base image's current content and verifies it still
matches the digest the cache key was computed from. The generated
Dockerfile's `FROM` uses that private pin, not the mutable
`ai-sandboxes-claude:local` tag directly. A concurrent
`./scripts/build` cannot change what the build actually uses without
also changing the cache key.

## Consequences

An install script cannot exfiltrate host secrets, credentials, or
project source through the build. A cached image cannot silently drift
away from the base it was built against, because both are pinned to the
same digest at build time and verified via labels afterwards (see
[ADR-0003](0003-label-verified-cache-keys.md)). Runtime-network
opt-ins remain independent of build-network opt-ins; a host cannot
accidentally grant either by granting the other.
