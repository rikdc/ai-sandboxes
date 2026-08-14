# ADR-0003: Cache identity is verified by OCI labels, not tags

## Status

Accepted.

## Context

A Docker tag is a mutable pointer. Something other than the resolver
could write to `ai-sandboxes-claude-session:sha-<hash>` between the
moment the resolver computed the tag and the moment it decided the image
was a cache hit. The design needs a definition of "this tag really is
the image I would have built."

Microsandbox adds a second complication: `msb` keeps a separate image
store from Docker's, and `msb load` does not retain OCI labels. So even
after a labelled cache hit on the Docker side, the msb-side artifact
could be a different image loaded under the same tag by
`load-image.sh` (a generic loader also used for non-session images) that
only checks whether *some* image exists under the tag.

## Decision

The resolver writes two labels into every session image at build time:
`io.ai-sandboxes.session-image` and `io.ai-sandboxes.session-cache-key`.
An existing local image is treated as a cache hit only when both labels
match the computed cache key. A tag that exists without the expected
labels fails closed — it is not silently rebuilt over or reused.

After `msb load`, `claude-session` compares the OCI config digest msb
reports for the loaded image against Docker's image ID for the same tag
and refuses to run if they disagree.

Shared-state metadata cannot piggyback on msb image labels for the same
reason, so `resolve-image.sh` prints a small JSON descriptor
(`{"image": "<tag>", "shared_state": {...}|null}`) computed entirely from
the already-validated host-side canonical profile snapshot.
`claude-session` calls a narrow helper
(`__ai_sandbox_shared_state_request_args`) that revalidates the id/quota
shape and builds the same mount argument the base image's label-derived
path produces.

## Consequences

Tag squatting or accidental overwrite cannot produce a false cache hit.
A second load path that trusts `msb load`'s side-effects on shared-state
cannot silently mint an unrequested mount. The base image's own
`claude`/`codex` launchers still derive shared state from msb image
labels via `__ai_sandbox_shared_state_mount_args`; this mechanism does
not touch them.
