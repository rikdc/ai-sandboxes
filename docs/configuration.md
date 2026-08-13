# Configuration

Configuration lives in `config/`. Rebuild, verify, and reload Microsandbox after changing it:

```console
./scripts/build
./scripts/verify
./scripts/load-msb
```

The checked-in configuration controls the neutral base images. Changes to it
require rebuilding, verifying, and reloading those images. For additional
Claude-only software or marketplaces that should remain separate from the
public runtime, keep an explicit JSON session profile in a personal or team
repository and run `claude-session --profile /absolute/path/to/session.json`.
Session profiles are validated host-side, are never discovered from the mounted
project, and build a cached derived image locally; see
[session images](session-images.md).

## Marketplaces and skills

Start with `config/marketplaces.example.json` and copy its entries into `config/marketplaces.json`.

- Claude entries must be public canonical GitHub URLs, pinned to a full commit SHA. The selected source must contain `.claude-plugin/marketplace.json` at `path`.
- `plugins` is an optional allowlist. Omit it or use `[]` to register a marketplace without installing plugins. Selected plugins are installed and enabled when a fresh Claude sandbox home starts; an existing user disablement is preserved.
- Codex entries must be pinned to a commit SHA and point `skills_path` at directories containing native `SKILL.md` files.
- Do not put credentials in the configuration or repository URLs.

Claude-specific commands, hooks, agents, and MCP settings are not converted into Codex skills.

## Optional tools

`config/tools.json` selects tools for the agent images. Copy the structure in `config/tools.example.json`; each selected tool must be present in the reviewed `config/tool-catalog.json` and use its required version and checksum pins. The checked-in selection is empty.

## Shared state

Set `shared_state` in `config/runtime.json` to the shape shown in `config/runtime.example.json` to give opted-in agent images a shared, persistent directory at `/var/lib/agent-state`. Its named volume is `agent-state-<id>-v1`.

Shared state is visible to every image that opts into the same profile. It does not grant host filesystem or network access, but its contents are untrusted input. Keep credentials out of it. Remove the named volume with `msb volume remove` to reset it; removal is irreversible.

## Versions

`versions.env` pins the runtime and agent versions, image digests, and VM quotas. Use `./scripts/build` rather than invoking Docker Bake directly: the script loads this file and validates the selected configuration.

### Claude Code distribution

`CLAUDE_CODE_VERSION` is an exact Claude Code release pin for the fixed `linux/arm64` image target. The Claude image downloads that version's `manifest.json` and detached signature from Anthropic, imports Anthropic's release key, and requires it to match the `CLAUDE_RELEASE_KEY_FINGERPRINT` pin before verifying the signature. It then downloads only the manifest's `linux-arm64` `claude` binary and verifies its SHA-256 checksum before installing it at `/usr/local/bin/claude`.

The image does not run Anthropic's installer or install Claude Code from npm. `DISABLE_UPDATES=1` blocks both background and manual Claude updates at runtime, so changing the installed version requires updating `CLAUDE_CODE_VERSION` and rebuilding the image.
