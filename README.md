# ai-sandboxes

ARM64 Linux execution images and Fish launchers for Claude Code and OpenAI Codex on an Apple Silicon Mac. It builds runtimes, seeds explicitly configured agent content, loads images into Microsandbox, and launches them; canonical skills remain in their own repositories.

## Security boundary

Each invocation is an unnamed Microsandbox VM with exactly one writable host bind: the current Git worktree, or the current directory when outside Git. It also gets one agent-specific persistent named volume (`claude-home` or `codex-home`). The launchers reject `/` and the complete host home directory. They use `--net public`, never `private` or `host` networking.

This is containment, not a data-loss boundary: the mounted repository is writable and public networking permits source-code or credential exfiltration. Review agent approvals and do not mount sensitive directories. The 10G named-volume disk is a real quota. Microsandbox 0.6.8 exposes no documented per-host-bind quota, so the documented 20G root-disk limit is not a quota on the repository mount.

## Prerequisites

Install Docker Desktop for Apple Silicon, Git, Fish, and Microsandbox (`msb`) and make sure Docker Desktop is running. The Docker daemon must be usable by your login user:

```console
docker version
msb doctor
fish --version
```

The builds target `linux/arm64`. They do not require Docker Hub credentials or an image registry. Builds need public DNS/network access to Debian, GitHub CLI, npm, and any sources you configure.

## Build, verify, and load

`versions.env` centralizes CLI versions. Marketplace and Codex-skill sources are configured separately in `config/marketplaces.json`.

```console
./scripts/build
./scripts/verify
./scripts/load-msb
```

The base uses `node:22-bookworm`, installs `gh` from GitHub's official Debian repository, and copies Tea from Gitea's official immutable release-image digest. Go is not in the final images. Claude Code and Codex are pinned npm packages; no curl-to-shell installer is used. The Claude package supports Linux ARM64; the base Dockerfile fails early if BuildKit's target architecture is not ARM64.

`load-msb` removes only an existing Microsandbox image with the exact local tag, then uses the documented `docker save … | msb load --tag …` interface. It does not remove containers, volumes, repositories, or unrelated images.

## Install the Fish functions

```fish
mkdir -p ~/.config/fish/functions
ln -sf /absolute/path/to/ai-sandboxes/shell/fish/claude.fish ~/.config/fish/functions/claude.fish
ln -sf /absolute/path/to/ai-sandboxes/shell/fish/codex.fish ~/.config/fish/functions/codex.fish
```

Open a new Fish shell, `cd` into a repository, then run normally:

```fish
claude
codex
claude --help
codex --help
```

All arguments after the function name are forwarded once to the selected agent. The guest path is `/workspace/<sanitized-basename>-<12-character-path-hash>`, so same-named repositories do not collide. The VM itself is unnamed and exits with the agent's status.

On its first launch, each agent authenticates interactively and stores state in its own named home volume. `gh auth login` and `tea login` run inside the relevant agent VM and likewise persist there; nothing is baked into an image.

## Configure marketplaces and Codex skills

`config/marketplaces.json` is intentionally empty and tracked. Copy the shape in `config/marketplaces.example.json`, then add as many sources as needed. Every `ref` must be a reviewed full commit SHA. This keeps an image definition inspectable and avoids baking a particular person's marketplace into the repository.

```json
{
  "claude": [{
    "url": "https://github.com/OWNER/claude-marketplace.git",
    "ref": "FULL_COMMIT_SHA",
    "path": "."
  }],
  "codex": [{
    "url": "https://github.com/OWNER/codex-skills.git",
    "ref": "FULL_COMMIT_SHA",
    "skills_path": ".codex/skills"
  }]
}
```

Each Claude source must contain `<path>/.claude-plugin/marketplace.json`. The build reads its real marketplace and plugin names, installs every declared plugin as `node`, and seeds them with `CLAUDE_CODE_PLUGIN_CACHE_DIR` and `CLAUDE_CODE_PLUGIN_SEED_DIR` under immutable `/opt` paths. Authentication, sessions, and preferences stay in the persistent home volume.

Each Codex source must expose a directory of native Codex `SKILL.md` directories at `skills_path` (for example, a repository's `.codex/skills`). The build copies only those declared directories into the image seed. It does not translate Claude agents, commands, hooks, MCP configuration, or marketplace metadata. At first execution, the entrypoint copies missing seeds into `$HOME/.codex/skills`, without overwriting user changes. Duplicate Codex skill names across configured sources fail the build rather than silently choose one.

The configuration is not for private credentials: use public clone URLs, or arrange a non-secret build-time Git transport separately. Do not put access tokens in this file or in Git URLs.

## Update and recovery

Edit `versions.env` for CLI versions and `config/marketplaces.json` for reviewed immutable content revisions, then rebuild, verify, and reload. The currently selected default versions are Claude Code 2.1.221, Codex 0.145.0, and Tea 0.14.2.

Inspect or intentionally remove persistent state:

```console
msb volume list
msb run --pull never --tty --mount-named claude-home:/home/node:rw ai-sandboxes-claude:local -- bash
msb volume remove claude-home
msb volume remove codex-home
```

Removing either volume is irreversible and forces fresh agent, `gh`, and Tea authentication on the next launch. To recover from a broken image import, rerun `./scripts/load-msb`; to recover from a wrong architecture, rebuild with `./scripts/build` on Docker Desktop for Apple Silicon and verify the architecture check.

## Troubleshooting

- `permission denied … docker.sock`: start Docker Desktop and ensure your user can run `docker version`.
- DNS or package download failure: restore public DNS/networking, then rebuild; no offline dependency cache is included.
- `command not found` in a VM: run `./scripts/verify` after rebuilding to identify which image failed.
- Marketplace or plugin failure: confirm each configured ref contains the declared `.claude-plugin/marketplace.json` or Codex `skills_path`, then rebuild. The build intentionally installs all marketplace entries, not guessed names.
- Microsandbox image replacement failure: stop sandboxes using that exact image, then rerun `./scripts/load-msb`. The script only replaces the two `ai-sandboxes-*:local` references.
