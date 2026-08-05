# ai-sandboxes

ARM64 Microsandbox images and Fish launchers for Claude Code and Codex. Skills and marketplaces stay in their own repositories; this repository builds and runs the agent environments.

## Quick start

Prerequisites: Apple Silicon, Docker Desktop, Git, Fish, and Microsandbox (`msb`). Confirm Docker is running with `docker version`.

Configure any optional Claude marketplaces or Codex skills in `config/marketplaces.json` (start with `config/marketplaces.example.json`). Use public URLs and reviewed commit SHAs—never credentials in URLs or configuration.

```console
./scripts/build
./scripts/verify
./scripts/load-msb
```

Install the Fish launchers:

```fish
mkdir -p ~/.config/fish/functions
ln -sf /absolute/path/to/ai-sandboxes/shell/fish/claude.fish ~/.config/fish/functions/claude.fish
ln -sf /absolute/path/to/ai-sandboxes/shell/fish/codex.fish ~/.config/fish/functions/codex.fish
```

Then, from a repository:

```fish
claude
codex
```

The functions mount only the current Git worktree (or current directory outside Git), refuse `/` and the complete home directory, and forward arguments once. Each agent has its own persistent home volume, so first-run authentication and `gh`/`tea` login persist.

## Configuration and updates

`versions.env` is the sole build configuration: pinned Node and Tea image digests, agent versions, the verified GitHub CLI key fingerprint, and `WORKSPACE_QUOTA` for the VM root disk. Microsandbox directory volumes are used for agent homes because they are writable by `node`; the current documented CLI provides no compatible quota-backed home mount for that user. The root-disk quota does not limit the host repository bind.

After changing versions or configured content, run the three quick-start commands again. `scripts/build` is the supported build entry point; it supplies `versions.env` to Bake. Direct builds must provide the GitHub CLI fingerprint explicitly and fail closed when it is absent.

Claude sources must contain `.claude-plugin/marketplace.json`; all declared plugins are seeded as `node`. Codex sources must expose native `SKILL.md` directories at the configured `skills_path`. Claude-only commands, hooks, agents, and MCP settings are not translated for Codex.

## Security and recovery

Each invocation is an unnamed VM with public networking and a writable repository mount. This is not a data-loss boundary: public networking permits exfiltration and the mounted repository remains writable.

To inspect or reset persistent state:

```console
msb volume list
msb volume remove claude-home
msb volume remove codex-home
```

Removing a volume is irreversible and requires re-authentication. If an image import fails, rerun `./scripts/load-msb`; if a command is missing, rebuild and run `./scripts/verify`.
