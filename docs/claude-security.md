# Claude security and recovery

Claude and Codex both run deny-by-default: only the HTTPS hosts listed in their
egress allowlist (`~/.config/microvms/claude-egress`,
`~/.config/microvms/codex-egress`) plus gateway DNS are reachable, each
launched as `node` in Microsandbox's `restricted` security profile. This page
covers Claude; Codex follows the same model with its own allowlist file.

The `claude` Fish launcher runs Claude Code as `node` in Microsandbox's `restricted` security profile. It mounts the current Git worktree (or current directory outside Git) read/write, uses a separate persistent home volume, and refuses to mount `/` or the complete host home directory.

## Network policy

Claude starts with deny-by-default network access: gateway DNS and HTTPS hosts listed in `~/.config/microvms/claude-egress` are allowed. Begin with `config/claude-egress.example`, then add only services you actually need.

```fish
mkdir -p ~/.config/microvms
cp /path/to/ai-sandboxes/config/claude-egress.example ~/.config/microvms/claude-egress
chmod 600 ~/.config/microvms/claude-egress
```

Use one hostname per line; wildcard subdomains are supported. The allowlist is a host-owned policy file and must not contain credentials.

For a temporary compatibility exception, run:

```fish
CLAUDE_MSB_PUBLIC_EGRESS=1 claude
```

This enables public Internet access but does not make arbitrary public destinations safe. Treat it as a per-session exception.

## Session-image builds

`claude-session --profile /absolute/path/to/session.json` can build a cached,
derived Claude image with validated apt, npm, curated-tool, and marketplace
selections. On a cache miss, the **host-side build** needs its own explicit
opt-in:

```fish
CLAUDE_MSB_BUILD_EGRESS=1 claude-session --profile /absolute/path/to/session.json
```

This does not grant the guest VM public egress; its runtime policy remains the
allowlist above unless `CLAUDE_MSB_PUBLIC_EGRESS=1` is also set. Keep profiles
outside the mounted project and do not put credentials, private registry URLs,
or arbitrary installer commands in them. See [session images](session-images.md)
for the supported schema and shared-state behavior.

## Working safely

The project mount is writable. Claude can change or delete every file in it, including ignored files. Keep secrets outside the project, commit or stash important work before autonomous sessions, and review the resulting Git diff. Authenticate with narrowly scoped credentials where possible.

The persistent home and project mount outlive the VM. If either may have been compromised, recover the repository with Git, reset the affected volume, and rotate credentials that were exposed to the guest.

```console
msb volume list
msb volume remove claude-home-hardened
```

Removing a volume is irreversible and requires authentication again on the next Claude run.

## Verify the boundary

Inside a normal Claude session, this quick check should report user `node`, `NoNewPrivs: 1`, permit the allowlisted Anthropic and GitHub endpoints, and reject an unlisted host:

```console
whoami
grep NoNewPrivs /proc/self/status
curl -sSI --connect-timeout 5 https://api.anthropic.com/
curl -sSI --connect-timeout 5 https://api.github.com/
curl -sSI --connect-timeout 5 https://example.com/
```
