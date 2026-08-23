# Configuration

Your configuration lives outside this checkout, in
`${XDG_CONFIG_HOME:-$HOME/.config}/ai-sandboxes/` (override the location with
`AI_SANDBOX_CONFIG_DIR`). Keeping it out of the repository means your edits
survive `git pull`, never enter a Docker build context from an untracked file,
and never show up as dirty working-tree noise. Set `AI_SANDBOX_CONFIG_DIR` to
an absolute path when you want per-host or per-purpose configurations.

The first `./scripts/install` or `./scripts/build` creates the directory with
mode 0700 and seeds any missing file from the checked-in neutral defaults, so
the flow is:

```console
./scripts/install   # creates ~/.config/ai-sandboxes/ and seeds the defaults
$EDITOR ~/.config/ai-sandboxes/marketplaces.json
./scripts/update    # detects the change by digest, rebuilds, reloads
```

Initialize before editing: opening a file that does not exist yet would have
your editor create an empty one, which the installer then treats as existing
user content and leaves untouched instead of seeding.

`./scripts/update` detects changed configuration by digest and rebuilds and
reloads automatically, so after the initial install you normally just edit the
files and run `./scripts/update`. You can always rebuild manually:

```console
./scripts/build
./scripts/verify
./scripts/load-msb
```

The checked-in `config/*.json` files in the repository are neutral defaults
that seed your files — they are not read at build time. The one exception is
`config/tool-catalog.json`, which stays repository policy: it reviews and pins
every tool that may be installed. For additional Claude-only software or
marketplaces that should remain separate from the public runtime, keep an
explicit JSON session profile in a personal or team repository and run
`claude-session --profile /absolute/path/to/session.json`.
Session profiles are validated host-side, are never discovered from the mounted
project, and build a cached derived image locally; see
[session images](session-images.md).

Migrating an existing installation: if you previously edited `config/*.json`
inside the checkout, copy your modified files into
`~/.config/ai-sandboxes/`, then restore the checkout copies with
`git restore config/` before updating.

## Marketplaces and skills

Edit `~/.config/ai-sandboxes/marketplaces.json`, starting from the shape in
`config/marketplaces.example.json`.

- Claude entries must be public canonical GitHub URLs, pinned to a full commit SHA. The selected source must contain `.claude-plugin/marketplace.json` at `path`.
- `plugins` is an optional allowlist. Omit it or use `[]` to register a marketplace without installing plugins. Selected plugins are installed and enabled when a fresh Claude sandbox home starts; an existing user disablement is preserved.
- Codex entries must be pinned to a commit SHA and point `skills_path` at directories containing native `SKILL.md` files.
- Do not put credentials in the configuration or repository URLs.

Claude-specific commands, hooks, agents, and MCP settings are not converted into Codex skills.

## Optional tools

`~/.config/ai-sandboxes/tools.json` selects tools for the agent images. Copy
the structure in `config/tools.example.json`; each selected tool must be
present in the reviewed `config/tool-catalog.json` and use its required
version and checksum pins. The default selection is empty.

## Shared state

Set `shared_state` in `~/.config/ai-sandboxes/runtime.json` to the shape shown
in `config/runtime.example.json` to give opted-in agent images a shared,
persistent directory at `/var/lib/agent-state`. Its named volume is
`agent-state-<id>-v1`.

For a standalone launcher or diagnostic, set `AI_SANDBOX_RUNTIME_CONFIG` to an absolute path to a runtime configuration file. Set it to `none` to explicitly disable shared state. `ai-sandbox run`, `ai-sandbox plan`, and `ai-sandbox doctor` resolve this setting identically; rebuilding is required whenever the selected policy differs from the base image's shared-state labels.

Shared state is visible to every image that opts into the same profile. It does not grant host filesystem or network access, but its contents are untrusted input. Keep credentials out of it. Remove the named volume with `msb volume remove` to reset it; removal is irreversible.

## SSH access profiles

`ai-sandbox run claude --access <name>` (and `ai-sandbox plan ... --access
<name>`) mounts one dedicated, host-owned SSH key directory read-only into the
guest at `/run/ai-sandbox/ssh`, allows exactly the profile's destination
through the deny-by-default network, and wires plain `ssh <name>` inside the
session to a hardened configuration by mounting the generated config at
`/etc/ssh/ssh_config.d/99-ai-sandbox-access.conf` — a stock Debian include
location, so every ssh invocation picks it up.

A profile pins exactly one destination: one host, one account, one port. To
reach several machines, create one access profile per machine. Two files
define an access profile, both under
`${XDG_CONFIG_HOME:-$HOME/.config}/ai-sandboxes/`:

1. The profile `access/<name>.json` — copy the shape from
   `config/access.example.json`. The profile name is the guest-side ssh alias,
   so this file is reachable as `ssh homelab`:

   ```json
   {
     "schema_version": 1,
     "host": "nas.home.lan",
     "port": 22,
     "user": "claude",
     "host_keys": ["nas.home.lan ssh-ed25519 AAAA..."]
   }
   ```

2. The key directory `access/keys/<name>/` — mode 0700, holding
   `id_ed25519` (mode 0600) and `id_ed25519.pub`. Create the key outside any
   mounted location:

   ```console
   mkdir -p ~/.config/ai-sandboxes/access/keys/homelab
   chmod 700 ~/.config/ai-sandboxes/access/keys/homelab
   ssh-keygen -t ed25519 -f ~/.config/ai-sandboxes/access/keys/homelab/id_ed25519 -C claude-homelab
   chmod 600 ~/.config/ai-sandboxes/access/keys/homelab/id_ed25519*
   ```

Before each run the control plane rewrites `config` and `known_hosts` inside
the key directory from the profile; edit the profile, not those files. The
profile requires pinned `host_keys` lines (from
`ssh-keyscan nas.home.lan` — verify the fingerprint through an independent
trusted channel before trusting it; the scan itself authenticates nothing),
and the launcher refuses anything that would defeat them: unknown JSON fields,
unpinned destinations, malformed or mismatched host-key lines, symlinked key
directories pointing outside `access/keys/`, loose permissions, or a workspace
overlapping the key directory.

Access runs also adjust guest DNS so internal names resolve. The launcher
discovers the host's upstream resolvers (System Configuration on macOS,
`/etc/resolv.conf` elsewhere) and pins them with `--dns-nameserver`, because
Microsandbox's own auto-discovery is not reliable on every boot and internal
zones (`.lan`, split-horizon corporate names) exist nowhere else. It also
passes `--no-dns-rebind-protection`: rebind protection drops answers pointing
at private RFC1918 addresses, which is what every LAN destination resolves to.
This changes DNS behavior for the whole session; public-egress runs keep
Microsandbox's defaults — the trade-off is deliberate and scoped to
`--access`.

The SSH connection originates in the guest VM. With the pinned resolvers,
LAN-only names your host resolves should resolve in the guest too; an IPv4
literal always works as a fallback. For a server on a non-standard port,
collect its host key with `ssh-keyscan -p <port> <host>` and set `port` in the
profile — the launcher renders the known_hosts selector as `<host>` for port
22 and `[<host>]:<port>` otherwise, so you never write the selector yourself.

The remote account must be distinct from your own and restricted on the
server side — see [the security notes](claude-security.md#ssh-access) for why,
what pinned host keys do and do not protect against, and how to revoke a
credential you suspect was copied.

## Versions

`versions.env` pins the runtime and agent versions and image digests. VM resource quotas (CPUs, memory, root/workspace/home) live in `internal/config/config.go` as explicit per-agent values; they are deliberate Go configuration so the launcher cannot silently inherit a missing quota from a shell default. Use `./scripts/build` rather than invoking Docker Bake directly: the script loads this file and validates the selected configuration.

### Agent-version release markers

`BASE_VERSION` is the project's own release line, bumped by hand when cutting
a release (e.g. `git tag v$BASE_VERSION`). After ARM64 image verification
succeeds on `main`, a change to `CODEX_VERSION` or `CLAUDE_CODE_VERSION`
creates an immutable GitHub Release riding on top of that same line, tagged
`v<BASE_VERSION>+codex-<CODEX_VERSION>-claude-<CLAUDE_CODE_VERSION>` and
pointing at the verified upstream commit — one linear tag lineage instead of
a separate namespace for agent-version bumps. The release includes a
`release.json` asset with this schema:

```json
{
  "schema_version": 2,
  "upstream_commit": "40-character lowercase Git commit SHA",
  "base_version": "exact BASE_VERSION value",
  "codex_version": "exact CODEX_VERSION value",
  "claude_code_version": "exact CLAUDE_CODE_VERSION value",
  "created_at": "RFC 3339 UTC timestamp"
}
```

The tag and marker are immutable. A workflow rerun reuses an existing release
only when its downloaded marker exactly matches the commit and all three
versions; otherwise it fails instead of changing a release or tag. A
prerelease `BASE_VERSION` (any value containing `-`, e.g. `0.1.0-alpha`) keeps
these releases out of GitHub's "Latest" slot automatically, the same as any
other prerelease.

Every six hours, the **Agent version watch** workflow reads the `latest` npm
dist-tags for `@openai/codex` and `@anthropic-ai/claude-code`. When either pin
has changed, it updates `versions.env` on a single automation pull request.
Merging that pull request runs the normal ARM64 verification on `main` and then
publishes the immutable release marker.

### Claude Code distribution

`CLAUDE_CODE_VERSION` is an exact Claude Code release pin for the fixed `linux/arm64` image target. The Claude image downloads that version's `manifest.json` and detached signature from Anthropic, imports Anthropic's release key, and requires it to match the `CLAUDE_RELEASE_KEY_FINGERPRINT` pin before verifying the signature. It then downloads only the manifest's `linux-arm64` `claude` binary and verifies its SHA-256 checksum before installing it at `/usr/local/bin/claude`.

The image does not run Anthropic's installer or install Claude Code from npm. `DISABLE_UPDATES=1` blocks both background and manual Claude updates at runtime, so changing the installed version requires updating `CLAUDE_CODE_VERSION` and rebuilding the image.
