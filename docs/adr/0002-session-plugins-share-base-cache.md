# ADR-0002: Session plugins install into the base image's plugin cache

## Status

Accepted. Replaces an earlier design that used a separate session cache
merged at runtime.

## Context

Claude plugins need to be present in a plugin cache directory before the
Claude process starts, since a session's guest home is ephemeral and
starts empty on first launch. The design question is which cache.

The original session-image design shipped a *separate*
`/opt/claude-session/plugin-cache` and `/opt/claude-session/plugin-seed`,
then merged an `extraKnownMarketplaces` entry into `settings.json` at
container launch pointing at the session cache. It looked correct: the
settings merge succeeded, the marketplace showed as enabled in Claude,
and validation passed.

The plugins never actually loaded.

Empirical debugging (overriding `CLAUDE_CODE_PLUGIN_CACHE_DIR` at runtime
to point at the separate session cache made the plugin appear; leaving
the variable at its default did not) confirmed that Claude resolves a
registered marketplace's *code* relative to
`CLAUDE_CODE_PLUGIN_CACHE_DIR` at runtime, not relative to whatever path
the `extraKnownMarketplaces` entry named. That variable only ever points
at the base image's cache directory. A marketplace cloned into a second,
unreferenced root registers cleanly but is unreachable.

## Decision

Session marketplaces install additively into the base image's own
`/opt/claude-plugin-cache` and `/opt/claude-plugin-seed` at build time.
The session build stage temporarily reclaims write access to those base
paths (`chown`/`chmod` back to node-writable as root, then `USER node` to
run the installer — the same lifecycle the base image's own build already
uses, re-opened for one more layer), installs the profile's marketplaces
alongside whatever the base image already has, merges the resulting
`settings.json` with any pre-existing base seed via
`scripts/session/merge-plugin-seed.sh` (session values winning on
conflicts, preserving the original additive intent), and re-locks
(`chown root:root`, `chmod -R a-w`) before the final stage copies the
augmented directories back to their standard paths.

At runtime, the entrypoint reads `CLAUDE_CODE_PLUGIN_SEED_DIR` from the
environment (required, exported by the Dockerfile) and merges that one
seed into `settings.json` on every launch via jq's recursive `*` operator
(right side winning per key). The user's already-persisted settings take
precedence over the seed; unrelated Claude settings are untouched.

## Consequences

Because the build produces one already-merged seed at the standard
`CLAUDE_CODE_PLUGIN_SEED_DIR` path, a session image is indistinguishable
at runtime from a base image that shipped with those marketplaces built
in. `images/claude/entrypoint.sh` needs no session-specific branching.

The mechanism moved from runtime to build time; the "session is additive
on top of base" intent is preserved. Recursive merge means
`extraKnownMarketplaces` is covered alongside `enabledPlugins`, which the
earlier single-key merge did not do.

The build stage re-opening base paths for writes is a controlled
privilege dance restricted to the build; the resulting final image locks
them again.
