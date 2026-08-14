# ai-sandboxes

ARM64 Microsandbox images and Fish launchers for Claude Code and Codex.

## Get started

Requires Apple Silicon, Docker Desktop, Git, Fish, and Microsandbox (`msb`).

```console
./scripts/build
./scripts/verify
./scripts/load-msb
```

Install the Fish launchers (`claude`, `codex`, and `claude-session`):

```console
./scripts/install-fish-functions
```

This writes small wrapper functions into `~/.config/fish/functions/`, copied
(not symlinked) from the checkout, plus a shared guard snippet under
`~/.config/ai-sandboxes/trusted/`. Do not symlink these launchers into
`~/.config/fish/functions/` yourself, and do not use any of them with a
project that is, or contains, the ai-sandboxes checkout or either of those
two installed directories: a launcher sourced from a location a guest agent
can also write to would let that guest tamper with host-trusted launcher code
for a later invocation to run with full host access. The installed wrapper
refuses to run whenever the mounted workspace overlaps any of those paths;
re-run `./scripts/install-fish-functions` after updating ai-sandboxes to
refresh the installed copies.

Claude uses an HTTPS allowlist by default, and Codex now does too. Create them before first run:

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
- After changing configuration or versions, run the build commands again.

See [configuration details](docs/configuration.md) and [Claude security and recovery](docs/claude-security.md) for the operational reference.

## Useful commands

```console
./scripts/build             # build local images
./scripts/verify            # validate images and launchers
./scripts/load-msb          # import images into Microsandbox
./scripts/lint-dockerfiles  # run Hadolint locally
```

Claude and Codex default to an intentionally restricted network. Use `CLAUDE_MSB_PUBLIC_EGRESS=1 claude` or `CODEX_MSB_PUBLIC_EGRESS=1 codex` only when a session needs public Internet access. The mounted project is writable, so keep secrets out of it and review agent changes with Git.
