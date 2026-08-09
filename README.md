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

Claude uses strict, deny-by-default egress by default. Create its host-owned
allowlist from the reviewed example. It contains no credentials and is
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

`versions.env` is the sole build configuration for the neutral runtime: pinned Node (Debian 13/Trixie) and Tea image digests, agent versions, the verified GitHub CLI key fingerprint, and VM quotas. Claude's hardened launcher uses 10 GiB caps for its writable root and workspace mount, plus a 4 GiB directory-backed home volume. The root-disk quota does not limit a host repository bind.

After changing versions or configured content, run the three quick-start commands again. `scripts/build` is the supported build entry point; it supplies `versions.env` to Bake. Direct builds must provide the GitHub CLI fingerprint explicitly and fail closed when it is absent.

Claude sources must contain `.claude-plugin/marketplace.json`; all declared plugins are seeded as `node`. Codex sources must expose native `SKILL.md` directories at the configured `skills_path`. Claude-only commands, hooks, agents, and MCP settings are not translated for Codex.

## Profile-selected agent tools

The base image is intentionally neutral. Profiles may opt into audited tool recipes through `config/tools.json`; the checked-in manifest selects none. A profile supplies only the selected tool IDs and their release, commit, or checksum pins. The public `config/tool-catalog.json` is the reviewed allowlist: it fixes each tool's upstream repository, installation adapter, executable, and any state wrapper. Generic adapters under `scripts/tools/` implement the supported installation methods, so profiles cannot introduce arbitrary installer commands or URLs. `images/tools/Dockerfile` is the profile layer between `base` and the agent images.

Profiles can declare a `shared_state` capability in `config/runtime.json`. The built image records only a validated, non-secret profile ID and quota; the launcher then mounts the fixed, quota-backed path `/var/lib/agent-state` from `agent-state-<profile-id>-v1`. A capability does not add host filesystem or network access, but it intentionally lets all opted-in images for that profile read and write persistent shared state. Treat that state as untrusted input, keep credentials out of it, and remove the named volume to reset it.

## Claude hardening, egress, and recovery

The Claude launcher uses Microsandbox's `restricted` profile, runs as `node`, limits its writable root to 10 GiB, and keeps a separate, quota-backed home volume. Its default network policy is deny-by-default: gateway DNS and only the HTTPS hosts in `~/.config/microvms/claude-egress` are reachable. DNS is handled by Microsandbox's gateway using the host resolver, so it remains compatible with VPN and split-horizon DNS configurations.

The launcher permits gateway DNS only, then derives HTTPS rules from `~/.config/microvms/claude-egress`. The example enables the core Claude endpoints and GitHub. Add only routine services you need (for example, `registry.npmjs.org` for npm); arbitrary WebFetch and package registries are otherwise blocked.

If a session needs the broader compatibility mode, use `CLAUDE_MSB_PUBLIC_EGRESS=1 claude`. This permits public Internet access while Microsandbox continues to deny the host, private networks, link-local addresses, and cloud metadata. Treat it as an explicit, per-session exception: public egress permits exfiltration to arbitrary public destinations.

Claude's repository mount remains read/write, so it can read, modify, and delete every file in the mounted workspace, including ignored files. Keep secrets out of the repository, commit or stash work before autonomous sessions, and review the resulting diff. The policy limits destinations, not what Claude can do with credentials authenticated inside its persistent home. Use dedicated, narrowly scoped credentials where practical.

For an inspection-only task, replace `rw` in the Claude launcher's `--mount-dir` option with `ro`. This is intentionally a manual, per-task choice rather than the development default.

`CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` and `ENABLE_CLAUDEAI_MCP_SERVERS=false` keep optional Claude traffic out of this profile. If you enable claude.ai MCP connectors, add `mcp-proxy.anthropic.com` to the egress file. Native updater, plugin, documentation, and other optional features may need their documented hosts added deliberately.

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

Expect `node`, `NoNewPrivs: 1`, no mounted macOS `/Users` tree, successful transport to the allowed hosts, and failure for `example.com`. Also test a known LAN address with a short timeout; it should remain unreachable in either network mode.

The VM root filesystem is disposable, but the named home and repository are not. If persistent Claude state may have been modified, replace its home volume and authenticate again. If the repository may have changed, recover it with Git. Revoke or rotate any credential that was exposed to the guest; deleting a VM cannot make a copied credential secret again.

To inspect or reset persistent state:

```console
msb volume list
msb volume remove claude-home-hardened
msb volume remove codex-home
```

Removing a volume is irreversible and requires re-authentication. If an image import fails, rerun `./scripts/load-msb`; if a command is missing, rebuild and run `./scripts/verify`.
