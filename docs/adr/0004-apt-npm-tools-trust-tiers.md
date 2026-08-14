# ADR-0004: apt, npm, and curated tools have different trust tiers

## Status

Accepted.

## Context

A session image can install packages from three sources: apt, npm, and
the curated `config/tool-catalog.json`. It's tempting to treat them
uniformly — validate the request, install as root, done — but the
install-time trust properties genuinely differ, and the design should
reflect that rather than pretend otherwise.

## Decision

**apt is trusted build-time code.** `apt-get install` runs as root, and a
package's maintainer scripts (`postinst` and friends) run as root too,
with no sandboxing beyond the build environment itself. A profile author
can request anything the public apt repositories serve, including
packages that introduce their own privileged artifacts (`sudo`,
setuid/setgid binaries, Linux capabilities) into the image. This is the
same trust relationship as a host running `docker build` with their own
Dockerfile. Session profiles are host-supplied input, never
guest/agent-discoverable (see [ADR-0001](0001-explicit-host-profile-path.md)),
so it does not open a new privilege-escalation path for the guest agent
— which still runs as `node` under the unchanged `restricted` runtime
policy regardless of what the image contains. It does mean a host can
silently bake privileged artifacts into an image with no explicit signal
that they did so; there is currently no allowlist, deny-list, or
post-install policy check.

**npm is untrusted install-time code.** Registry installs run
third-party lifecycle hooks as whichever user invokes `npm install`.
npm therefore installs into an image-local prefix at
`/opt/claude-session/npm`, and its `bin` is *appended* to `PATH` — not
prepended — so a session-installed package cannot shadow a base-image
command the harness itself depends on (`claude`, `git`, `curl`, ...). The
final prefix is `chown -R root:root` + `chmod -R a-w`.

**Curated tools carry no install-time third-party code.** A
github-release-tar install runs no lifecycle hooks and downloads only a
fixed catalog-pinned URL whose sha256 is verified before extraction.
Tools install through the exact chain the base image's runtime tool
mechanism already uses
(`scripts/tools/install-selected.sh` + `scripts/tools/install-github-release-tar.sh`,
copied unmodified into the build context alongside a copy of
`config/tool-catalog.json`), under `USER root` in the final stage.
Unlike npm, this needs no discardable-build-stage or node-then-relock
privilege dance, because there is no third-party code to contain.

Installed binaries land under root-owned, non-writable paths exactly as
the base image's runtime tool mechanism already does. A tool with no
`state_wrapper` installs to `/usr/local/bin/<binary>`. A tool with a
`state_wrapper` (`icm`) installs its real binary to
`/usr/local/libexec/<binary>` and a launcher symlink at
`/usr/local/bin/<binary>` pointing at the shared
`/usr/local/libexec/ai-sandboxes-state-wrapper`, which — except for a
`--version` bypass — refuses to run unless `/var/lib/agent-state` is
present and writable. `/usr/local/bin` and `/usr/local/libexec` are
never made writable to `node`.

## Consequences

Uniform handling would either over-privilege catalog tools (needless
sandboxing) or under-contain npm (arbitrary install-time code with too
much authority). The tiered approach matches each source's actual
install-time trust properties. The "apt can bake in privileged
artifacts" property is documented rather than fought — layering an
allowlist without teeth is worse than an honest note that host-supplied
apt selections are host-trusted.
