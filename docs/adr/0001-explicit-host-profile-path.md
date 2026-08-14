# ADR-0001: Session profiles come from an explicit host path

## Status

Accepted.

## Context

A session profile controls what packages, tools, and Claude plugins are
baked into a session image the host trusts to run. The convenient
alternative — auto-discovering a `session.json` from the mounted project
directory — is worth ruling out explicitly, because it's the first thing
someone new to the design tends to propose.

## Decision

The profile path is always supplied explicitly by the host user, either
as a literal `/`-containing path or as a bare name that resolves under
`~/.config/ai-sandboxes/profiles/<name>.json`. The launcher never reads a
profile from the project mount.

The launcher also refuses to mount any workspace that overlaps a set of
protected ai-sandboxes paths. Those paths include the ai-sandboxes
checkout itself **and** the directories `./scripts/install-fish-functions`
writes to (`~/.config/fish/functions/` and
`~/.config/ai-sandboxes/trusted/`).

## Consequences

An agent running inside a Claude session cannot influence a future
host-side build by editing files in the project mount.

The trust boundary is the installer, not the in-checkout guard. The
in-checkout copy of the overlap check
(`shell/fish/lib/ai-sandbox.fish`) remains as defense in depth for direct
or pre-wrapper invocations, but it is not the security control — a guest
with write access to those files could edit the check back out. The
authoritative wrapper is copied (not symlinked) outside every checkout by
`./scripts/install-fish-functions`, and runs the overlap check *before*
sourcing any checkout-provided code at all.

Protecting only the checkout was considered and rejected. A guest mounted
at, say, `~/.config/fish` could then tamper with the installed wrapper
directly. Protecting the wrapper's installation directories too closes
that.
