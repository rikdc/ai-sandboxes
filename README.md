# ai-sandboxes

Run Claude Code and Codex inside deny-by-default microVMs on Apple Silicon.
The agent gets its own Linux machine instead of running against your Mac.

Your project directory stays writable and agent configuration survives
between sessions. Everything else (credentials, dotfiles, home directory,
the wider network) stays outside the guest until you deliberately expose
it, and public egress is one environment variable away when a session
needs it.

## Get started

Requires Apple Silicon, Docker Desktop, Git, Microsandbox (`msb`), and
Go to build the `ai-sandbox` control plane.

```console
./scripts/install
```

That preflights the prerequisites (Git, Go, Docker with a reachable daemon
and buildx, `msb`), then runs the full sequence: build and install the
control plane binary to `~/.local/libexec/ai-sandboxes/ai-sandbox`, build the
base/tools/claude/codex images, load them into Microsandbox once, and verify.
It fails before mutating anything when a prerequisite is missing, fails fast
when a step fails, and names the individual script to re-run. Each step
remains available for maintainers: `scripts/install-ai-sandbox`,
`scripts/build`, `scripts/load-msb`, `scripts/verify`.

Install the Fish launchers (`claude`, `codex`, and `claude-session`) — this
one is host-shell-specific, so it is opt-in rather than part of
`./scripts/install`. Running it once marks the wrappers as managed;
`scripts/update` refreshes them only after that first opt-in:

```console
./scripts/install-fish-functions
```

This copies small wrapper functions into `~/.config/fish/functions/` (not
symlinks) plus a shared guard snippet under `~/.config/ai-sandboxes/trusted/`.
The `claude` and `codex` wrappers are pass-throughs; after the guard check
they hand off to `ai-sandbox run`, which resolves the invocation into a
single runtime plan (image, workspace, network, egress, shared-state handoff)
and launches it.

Don't symlink these launchers yourself or run them from a project containing
the ai-sandboxes checkout or either installed directory: a launcher somewhere
a guest agent can also write is one that guest can rewrite, and the next
invocation would then run with full host access. The wrapper checks for this
overlap at startup and refuses to run when it finds one. Re-run
`./scripts/install-fish-functions` after moving the checkout.

Claude and Codex both use an HTTPS allowlist by default. Create the files
before first run:

```fish
mkdir -p ~/.config/microvms
cp /path/to/ai-sandboxes/config/claude-egress.example ~/.config/microvms/claude-egress
cp /path/to/ai-sandboxes/config/codex-egress.example ~/.config/microvms/codex-egress
chmod 600 ~/.config/microvms/claude-egress ~/.config/microvms/codex-egress
```

From a project directory, run `claude` or `codex`.

## Configure

- Copy and edit `config/marketplaces.example.json` to add reviewed Claude marketplaces or Codex skills.
- Choose optional tools in `config/tools.json`; their allowed sources are in `config/tool-catalog.json`.
- Configure optional shared state in `config/runtime.json` (see `config/runtime.example.json`).
- Keep personal or team session configuration in a separate repository as an explicit `session.json`, then run `claude-session --profile /absolute/path/to/session.json` from the project you want Claude to edit. See [session images](docs/session-images.md).
- After changing configuration or versions, run `./scripts/install` (or the individual build/verify/load commands) again.

See [configuration details](docs/configuration.md) and [Claude security and recovery](docs/claude-security.md) for the operational reference.

## Useful commands

```console
ai-sandbox run claude       # launch Claude Code (what the `claude` wrapper calls)
ai-sandbox plan claude      # resolve and print the runtime plan without launching
ai-sandbox doctor           # diagnose host, Docker, msb, and launcher health
ai-sandbox codex login      # sign in to Codex against a running `run codex` sandbox
go test ./...               # control-plane unit tests
./scripts/build             # build local images
./scripts/verify            # validate images, launchers, and control plane
./scripts/load-msb          # import images into Microsandbox
./scripts/lint-dockerfiles  # run Hadolint locally
```

### How auth works

Codex and Claude Code each handle their own browser-based OAuth sign-in:
account login (Codex only) and MCP server login (both). The CLI binds its
OAuth callback on a loopback port *inside the guest*, where the host browser
cannot reach it, so `ai-sandbox` opens an SSH tunnel scoped to that single
login operation: up just before the sign-in flow starts, gone as soon as it
finishes or fails. No port is published to the LAN or the public Internet
and nothing is left listening afterward. See
[ADR-0007](docs/adr/0007-codex-auth-tunnel.md) and
[ADR-0008](docs/adr/0008-codex-mcp-oauth-tunnel.md) for the design rationale.

One-time host setup, required before any of the subcommands below:

```console
msb ssh authorize --file ~/.ssh/id_ed25519.pub
```

**Codex account login**: Codex's browser sign-in binds its OAuth callback on
`127.0.0.1:1455` inside the guest. With the sandbox running, run the login
subcommand in a second terminal:

```console
# terminal 1
ai-sandbox run codex
# terminal 2
ai-sandbox codex login
```

After the first sign-in succeeds, exit and re-run `codex`: the running
process reads its auth token at startup and won't pick up the new credential
until it restarts. Later runs re-use the persisted token from the
`codex-home` volume, so you pay this once.

**MCP server login (Codex or Claude Code)**: signs an already-running sandbox
into an MCP server's own OAuth (Slack, Notion), same tunnel mechanism:

```console
# Codex: ai-sandbox picks an ephemeral loopback port per invocation
ai-sandbox codex mcp login <server-name>

# Claude Code: the port must match what you registered with
# `claude mcp add --scope user --callback-port <P> --transport http <server-name> <url>`
ai-sandbox claude mcp login --callback-port <P> <server-name>
```

Each MCP server's registry lives in that CLI's own persisted config;
`ai-sandbox` never reads or writes it.

Claude and Codex default to an intentionally restricted network. Use
`CLAUDE_MSB_PUBLIC_EGRESS=1 claude` or `CODEX_MSB_PUBLIC_EGRESS=1 codex` only
when a session needs public Internet access. The mounted project is writable,
so keep secrets out of it and review agent changes with Git.
