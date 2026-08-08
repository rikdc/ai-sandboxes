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

The strict egress allowlist is opt-in. If you choose to enable it, create the
host-owned policy from the reviewed example. It contains no credentials and is
intentionally outside both this repository and the guest.

```fish
mkdir -p ~/.config/microvms
cp /absolute/path/to/ai-sandboxes/config/claude-egress.example ~/.config/microvms/claude-egress
chmod 600 ~/.config/microvms/claude-egress
```

Then, from a repository:

```fish
claude
codex
```

The functions mount only the current Git worktree (or current directory outside Git), refuse `/` and the complete home directory, and forward arguments once. Codex has its own persistent home volume. Claude uses a fresh `claude-home-hardened` volume, so authenticate it again on its first run.

## Configuration and updates

`versions.env` is the sole build configuration: pinned Node and Tea image digests, agent versions, the verified GitHub CLI key fingerprint, and `WORKSPACE_QUOTA` for the Codex VM root disk. Claude's hardened launcher uses its own 10 GiB workspace-mount quota and a 4 GiB directory-backed home volume. The root-disk quota does not limit a host repository bind.

After changing versions or configured content, run the three quick-start commands again. `scripts/build` is the supported build entry point; it supplies `versions.env` to Bake. Direct builds must provide the GitHub CLI fingerprint explicitly and fail closed when it is absent.

Claude sources must contain `.claude-plugin/marketplace.json`; all declared plugins are seeded as `node`. Codex sources must expose native `SKILL.md` directories at the configured `skills_path`. Claude-only commands, hooks, agents, and MCP settings are not translated for Codex.

## Claude hardening, egress, and recovery

The Claude launcher uses Microsandbox's `restricted` profile, runs as `node`, and keeps a separate, quota-backed home volume. Its default `public` network profile permits public Internet access while Microsandbox continues to deny the host, private networks, link-local addresses, and cloud metadata. This is the compatible default for the current Claude native client.

Set `CLAUDE_MSB_STRICT_EGRESS=1` to try the deny-by-default policy from the tutorial. In that mode the launcher permits gateway DNS only, then derives HTTPS rules from `~/.config/microvms/claude-egress`. The example enables the core Claude endpoints and GitHub. Add only routine services you need (for example, `registry.npmjs.org` for npm); arbitrary WebFetch and package registries are otherwise blocked. The current Microsandbox allowlist path is retained as an experimental boundary because Claude's native client has shown intermittent connection failures despite the complete documented core host set. The strict mode pins DNS to `1.1.1.1` for the tested `msb 0.6.8` macOS configuration.

Claude's repository mount remains read/write, so it can read, modify, and delete every file in the mounted workspace, including ignored files. Keep secrets out of the repository, commit or stash work before autonomous sessions, and review the resulting diff. The policy limits destinations, not what Claude can do with credentials authenticated inside its persistent home. Use dedicated, narrowly scoped credentials where practical.

For an inspection-only task, replace `rw` in the Claude launcher's `--mount-dir` option with `ro`. This is intentionally a manual, per-task choice rather than the development default.

`CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` and `ENABLE_CLAUDEAI_MCP_SERVERS=false` keep optional Claude traffic out of this profile. In strict mode, if you enable claude.ai MCP connectors, add `mcp-proxy.anthropic.com` to the egress file. Native updater, plugin, documentation, and other optional features may need their documented hosts added deliberately.

Host-side secret injection is intentionally not enabled by default: authenticate normally in the new Claude home first, then decide whether its extra complexity is worthwhile. For a host-exported, scoped GitHub token, the optional Microsandbox form is `--secret 'GH_TOKEN@api.github.com,github.com'`. It keeps the reusable value host-side but does not reduce the token's authority when hostile guest code can use it against GitHub; scope that credential accordingly.

To verify the active Claude boundary, run this inside a normal Claude session:

```console
whoami
grep NoNewPrivs /proc/self/status
ls -la /Users
ls -la /workspace
curl -sSI --connect-timeout 5 https://api.anthropic.com/
curl -sSI --connect-timeout 5 https://api.github.com/
curl -sSI --connect-timeout 5 https://example.com/
```

Expect `node`, `NoNewPrivs: 1`, no mounted macOS `/Users` tree, successful transport to the allowed hosts, and failure for `example.com` when strict egress is enabled. Also test a known LAN address with a short timeout; it should remain unreachable in either network mode.

The VM root filesystem is disposable, but the named home and repository are not. If persistent Claude state may have been modified, replace its home volume and authenticate again. If the repository may have changed, recover it with Git. Revoke or rotate any credential that was exposed to the guest; deleting a VM cannot make a copied credential secret again.

To inspect or reset persistent state:

```console
msb volume list
msb volume remove claude-home-hardened
msb volume remove codex-home
```

Removing a volume is irreversible and requires re-authentication. If an image import fails, rerun `./scripts/load-msb`; if a command is missing, rebuild and run `./scripts/verify`.
