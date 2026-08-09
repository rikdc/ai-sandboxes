# ai-sandboxes

ARM64 Microsandbox images and Fish launchers for Claude Code and Codex.

## Get started

Requires Apple Silicon, Docker Desktop, Git, Fish, and Microsandbox (`msb`).

```console
./scripts/build
./scripts/verify
./scripts/load-msb
```

Install the Fish launchers:

```fish
mkdir -p ~/.config/fish/functions
ln -sf /path/to/ai-sandboxes/shell/fish/claude.fish ~/.config/fish/functions/claude.fish
ln -sf /path/to/ai-sandboxes/shell/fish/codex.fish ~/.config/fish/functions/codex.fish
```

Claude uses an HTTPS allowlist by default. Create it before its first run:

```fish
mkdir -p ~/.config/microvms
cp /path/to/ai-sandboxes/config/claude-egress.example ~/.config/microvms/claude-egress
chmod 600 ~/.config/microvms/claude-egress
```

From a project directory, run `claude` or `codex`.

## Configure

- Copy and edit `config/marketplaces.example.json` to add reviewed Claude marketplaces or Codex skills.
- Choose optional tools in `config/tools.json`; their allowed sources are in `config/tool-catalog.json`.
- Configure optional shared state in `config/runtime.json` (see `config/runtime.example.json`).
- After changing configuration or versions, run the build commands again.

See [configuration details](docs/configuration.md) and [Claude security and recovery](docs/claude-security.md) for the operational reference.

## Useful commands

```console
./scripts/build             # build local images
./scripts/verify            # validate images and launchers
./scripts/load-msb          # import images into Microsandbox
./scripts/lint-dockerfiles  # run Hadolint locally
```

Claude's default network is intentionally restricted. Use `CLAUDE_MSB_PUBLIC_EGRESS=1 claude` only when a session needs public Internet access. The mounted project is writable, so keep secrets out of it and review agent changes with Git.
