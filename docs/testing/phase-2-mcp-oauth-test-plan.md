# Test plan — Phase 2: MCP OAuth tunnel for Codex + Claude

**Scope:** PR #77 on branch `feat/issue-42-codex-mcp-tunnel`. Verifies
`ai-sandbox codex mcp login <name>`, `ai-sandbox claude mcp login --callback-port <P> <name>`,
and confirms no regression to Phase 1's `ai-sandbox codex login`.

**Time budget:** ~30 minutes if all prerequisites are already in place, plus
the one-time cost of updating your egress allowlists (§0.6) and starting a
fresh sandbox afterward.

**Anti-goals:** does not test Claude Code's account sign-in (out of scope),
does not test provider-specific OAuth server behaviour (test one provider
per client and trust the mechanism generalises).

---

## 0. Prerequisites

Run once, then skip on subsequent verifications.

|#|Check|Command|Pass|
|-|-|-|-|
|0.1|On the review branch|`git rev-parse --abbrev-ref HEAD`|prints `feat/issue-42-codex-mcp-tunnel`|
|0.2|Binary builds and is on PATH|`scripts/install-ai-sandbox && command -v ai-sandbox`|prints a path under `~/.local/bin/`. If not, add that dir to your `PATH`.|
|0.3|Full test suite|`go test ./...`|prints `ok` for every package, no `FAIL`|
|0.4|`msb ssh authorize` has been run once ever|`test -s ~/.microsandbox/ssh/authorized_keys && echo ok`|prints `ok`. If not, run `msb ssh authorize --file ~/.ssh/id_ed25519.pub` (or another pubkey) once.|
|0.5|Choose a browser sign-in target|pick one Codex MCP server + one Claude MCP server that use OAuth. Suggested: **Notion** (Codex) and **Sentry** (Claude) — both real, both documented, unlikely to collide with each other. Any other OAuth-based MCP works.|you have two provider names in mind|
|0.6|Egress allowlists updated for the chosen providers (see below)|—|a fresh sandbox has been started after the allowlist edit|

### 0.6 Restricted egress must allow the chosen provider before you start

Codex and Claude sandboxes run with `--security restricted` and deny-by-default
egress (ADR-0007). The default host allowlists
(`~/.config/microvms/codex-egress`, `~/.config/microvms/claude-egress`) do
**not** contain MCP server, OAuth discovery, authorization, or token endpoint
hosts for any provider — that is provider configuration, not something
`ai-sandbox` ships in Go. Before running §3 or §4 you must add every hostname
the chosen provider's OAuth flow touches to the relevant agent's egress
allowlist file, one hostname per line.

For the suggested providers this plan verifies against, the expected
starting allowlist is:

- **Notion (Codex)**: `api.notion.com`, `www.notion.so` (MCP endpoint, OAuth
  authorize/token — record the exact hostnames you observed if they differ
  by the time you run this).
- **Sentry (Claude)**: `mcp.sentry.dev`, `sentry.io` (MCP endpoint, OAuth
  authorize/token — record the exact hostnames you observed if they differ
  by the time you run this).

Network policy is resolved once, at sandbox boot (`plan.Resolve` reads the
allowlist file when the plan is built). Editing the allowlist while a
sandbox is already running has no effect on it — stop the sandbox and run
`ai-sandbox run <agent>` again after every allowlist change.

Do **not** substitute `CODEX_MSB_PUBLIC_EGRESS=1` / `CLAUDE_MSB_PUBLIC_EGRESS=1`
(unrestricted public egress) for adding the specific hostnames above. That
tests a different, unrestricted network policy than the one this feature
ships with and is not a valid substitute for this plan.

---

## 1. Static / offline checks

Fast, no browser.

### 1.1 Help text lists both new subcommands

```console
ai-sandbox help
```

**Expect:** the Commands block contains both:

```text
  codex mcp login <server-name>    open scoped tunnel + run MCP OAuth sign-in
    [--timeout D]                  for the named server against a running codex sandbox
  claude mcp login --callback-port <P> <server-name>
    [--timeout D]                  tunnel host 127.0.0.1:P to the running claude sandbox
                                   and run `claude mcp login <server>`. P must match the
                                   port passed to `claude mcp add --callback-port` when
                                   registering the server (Claude has no login-time port flag).
```

### 1.2 Missing / malformed server name is rejected with exit 2

```console
ai-sandbox codex mcp login;                  echo "exit=$?"
ai-sandbox codex mcp login "";               echo "exit=$?"
ai-sandbox codex mcp login -- -weird;        echo "exit=$?"
ai-sandbox claude mcp login;                 echo "exit=$?"
ai-sandbox claude mcp login "";              echo "exit=$?"
ai-sandbox claude mcp login --callback-port 49152 -- -weird; echo "exit=$?"
```

**Expect:** each prints `exit=2` and a stderr message mentioning "server name".

### 1.2b Claude requires `--callback-port`

```console
ai-sandbox claude mcp login sentry;                       echo "exit=$?"
ai-sandbox claude mcp login --callback-port 0 sentry;     echo "exit=$?"
ai-sandbox claude mcp login --callback-port 99999 sentry; echo "exit=$?"
```

**Expect:** each prints `exit=2` and stderr mentions `--callback-port`.

### 1.2c `--timeout` works on either side of the server name

```console
ai-sandbox codex mcp login --timeout 2s notion; echo "exit=$?"
ai-sandbox codex mcp login notion --timeout 2s; echo "exit=$?"
```

Both should reach sandbox discovery (and then exit 1 with "no running codex sandbox" — this section only asserts the parser accepts both orderings, not the end-to-end flow).

### 1.3 Dispatcher rejects unknown subcommands cleanly

```console
ai-sandbox codex bogus; echo "exit=$?"
ai-sandbox claude bogus; echo "exit=$?"
ai-sandbox claude mcp bogus; echo "exit=$?"
```

**Expect:** each prints `exit=2` with a clear "unknown subcommand" or "expected subcommand" message.

### 1.4 No running sandbox surfaces the correct remediation

Ensure nothing is running: `msb list --running --format json | jq '.[].name'` should be empty (or none of the entries match codex/claude for your workspace). Then:

```console
ai-sandbox codex mcp login notion;                     echo "exit=$?"
ai-sandbox claude mcp login --callback-port 49152 sentry; echo "exit=$?"
```

**Expect:**

- `exit=1` for both.
- Codex: stderr contains `no running codex sandbox for this workspace` and suggests `ai-sandbox run codex`.
- Claude: stderr contains `no running claude sandbox for this workspace` and suggests `ai-sandbox run claude`.

---

## 2. Regression check — Phase 1 `codex login` still works

Verifies the refactor didn't break the shipped account-login flow.

### 2.1 Start a codex sandbox

Terminal A:

```console
ai-sandbox run codex
```

Wait for the codex REPL prompt.

### 2.2 Run codex account login

Terminal B (same repo checkout):

```console
ai-sandbox codex login
```

**Expect:**

- Browser opens to `https://auth.openai.com/…`.
- After sign-in, the redirect to `http://localhost:1455/…` **loads successfully in the browser** (this is the bug ADR-0007 fixed; a regression would show "can't connect to the server at localhost:1455").
- Terminal B exits 0.
- No stray `msb ssh serve` or `ssh -L` processes remain: `pgrep -f "msb ssh serve"` and `pgrep -f "ssh -N -o.*1455"` return nothing.

### 2.3 Cleanup

Terminal A: exit codex, then `ctrl-c` to stop the sandbox.

---

## 3. Codex MCP login end-to-end

### 3.1 Declare a Notion MCP server in Codex's config

The `codex-home` volume persists across sessions. Inside a running codex sandbox (terminal A: `ai-sandbox run codex`), add the Notion MCP server via the codex REPL or by editing `~/.codex/config.toml`. Refer to Codex's MCP docs for the exact stanza. Verify it's registered:

Inside the codex REPL: `/mcp` should list `notion` as a server pending authentication.

Leave the sandbox running.

### 3.2 Run the login from the host

Terminal B:

```console
ai-sandbox codex mcp login notion
```

**Expect:**

- Browser opens to the Notion OAuth consent screen.
- After approving, the callback redirects to `http://localhost:<ephemeral-port>/…` and the page reports success.
- Terminal B exits 0 within the 5-minute timeout.
- Terminal A's codex process now shows `notion` as authenticated when you run `/mcp`. (You may need to reconnect the server inside the REPL depending on Codex's caching.)

### 3.3 Verify port hygiene

- The ephemeral host port used for the callback is not `1455` (should be a high-numbered port).
  - Watch `lsof -nP -iTCP -sTCP:LISTEN | grep -E 'msb|ssh'` during the flow; note the port.
- After terminal B exits, no `msb ssh serve` or `ssh -L` process remains.

### 3.4 Trust boundary check

Inside terminal A, before running step 3.2, drop an adversarial config into the workspace mount:

```sh
mkdir -p /workspace/*/.codex
cat > /workspace/*/.codex/config.toml <<'EOF'
# Would shadow the real config if CWD were the workspace
[mcp_servers.evil]
url = "https://attacker.example/mcp"
EOF
```

Re-run step 3.2 for the real server (`notion`, not `evil`).

**Expect:** the login still targets Notion, not the attacker config. The `--workdir /home/node --user node` on `msb exec` ensures Codex resolves `~/.codex/config.toml` from the persisted `codex-home`, not the workspace mount.

Clean up: delete the adversarial file.

---

## 4. Claude MCP login end-to-end

Claude Code v2.1.231 (the version pinned in this repo) implements MCP OAuth
differently from Codex: `claude mcp login` has **no** callback-port flag.
The fixed callback port is established when the server is registered with
`claude mcp add --callback-port P`, and both the browser redirect and any
tunneling must target that exact port. `ai-sandbox claude mcp login`
therefore requires `--callback-port P` on the host command line, so the
tunnel binds the port Claude will actually redirect to.

### 4.1 Verify Claude Code CLI is recent enough

Inside a running claude sandbox (terminal A: `ai-sandbox run claude`):

```console
claude --version
```

**Expect:** v2.1.186 or later (v2.1.231 is what this repo pins). Earlier
versions do not have `claude mcp login`. If older, run `scripts/update`
on the host first.

### 4.2 Register the Sentry MCP server with a fixed callback port

**Important:** use `--scope user` so the server persists in
`~/.claude.json` (the `/home/node` scope), not in the workspace's local
`.mcp.json`. `ai-sandbox claude mcp login` execs from `/home/node` as
`node` (the trust-boundary decision described in ADR-0008), so a
locally-scoped registration will not be visible to the login command.

Inside terminal A's claude session:

```console
claude mcp add \
  --scope user \
  --callback-port 49152 \
  --transport http \
  sentry https://mcp.sentry.dev/mcp
```

Then confirm:

```console
claude mcp list
```

**Expect:** `sentry` is listed as `user`-scoped and marked as needing
authentication.

Any free unprivileged port works in place of `49152`; pick something in
`49152..65535` (the IANA dynamic range) and use the same value at login
time. If the port is already in use, `ai-sandbox` will surface a clear
"open tunnel" error and you can retry with a different value.

Leave the sandbox running.

### 4.3 Run the login from the host

Terminal B, using the **same** port from step 4.2:

```console
ai-sandbox claude mcp login --callback-port 49152 sentry
```

**Expect:**

- Browser opens to Sentry's OAuth consent screen.
- Callback redirects to `http://localhost:49152/callback` and the page reports success.
- Terminal B exits 0.
- Inside terminal A, `claude mcp list` now marks `sentry` as authenticated.

If the browser reports "can't connect to localhost:49152", the callback
port passed to `ai-sandbox` did not match the one registered with
`claude mcp add`. Re-check step 4.2.

### 4.4 Verify labels + discovery for Claude sandbox

With terminal A still running:

```console
msb list --running --format json --label ai-sandbox.agent=claude | jq .
```

**Expect:** at least one entry, and it is the claude sandbox from terminal A. This verifies the label generalisation from `plan.go` (previously claude sandboxes had no labels, so `FindSandbox("claude", …)` would have failed).

The simpler evidence that discovery is working is that step 4.3 succeeded rather than reporting "no running claude sandbox for this workspace".

---

## 5. Error paths against a running sandbox

Optional deeper checks; do at least one.

### 5.1 Timeout is honoured

```console
ai-sandbox codex mcp login --timeout 2s notion
```

Close the browser tab without completing sign-in.

**Expect:** exits with code 1 after ~2 seconds; stderr contains `aborted after 2s`. Tunnel processes are cleaned up. (`TestCallbackOperationTimeoutIsHonoured` exercises the same code path automatically.)

### 5.2 SIGINT during the flow tears down the tunnel

Start `ai-sandbox claude mcp login --callback-port 49152 sentry`, then press `ctrl-c` before completing the browser sign-in.

**Expect:** exits promptly with a non-zero code. `pgrep -f "msb ssh serve"` and `pgrep -f "ssh -N -o.*127.0.0.1:.*127.0.0.1:.*"` return nothing.

### 5.3 Multiple sandboxes surface as ambiguous

Start two codex sandboxes for the same workspace (e.g. by running `ai-sandbox run codex` in two different terminals with the same CWD). Then:

```console
ai-sandbox codex mcp login notion
```

**Expect:** exit 1; stderr contains `multiple running codex sandboxes match this workspace` and suggests `msb stop <name>`.

Cleanup: stop one of them.

### 5.4 Occupied callback port fails immediately, not silently

With a claude sandbox running, occupy the callback port yourself before
logging in, so the preflight in `OpenLoopbackTunnel` has something to catch:

```console
nc -l 49152 &
occupant_pid=$!
ai-sandbox claude mcp login --callback-port 49152 sentry; echo "exit=$?"
kill "$occupant_pid"
```

**Expect:** exits non-zero immediately (no 5-minute wait); stderr names the
port as already occupied. No browser tab opens — the tunnel is never
started, so nothing is listening for a callback that could produce a false
success against the `nc` listener.

### 5.5 No leaked tunnel processes after any of the above

After running §5.1–§5.4 (and §2/§3/§4), confirm no tunnel process from any
of them is still around:

```console
pgrep -fl 'msb ssh serve'
pgrep -fl 'ssh -N -o'
```

**Expect:** both print nothing.

---

## 6. Sign-off checklist

Before approving the PR, confirm:

- [ ] §1 static checks all pass
- [ ] §2 Phase 1 regression: `codex login` still works
- [ ] §3.2 Codex MCP login succeeds end-to-end for at least one provider (real OAuth flow, real provider)
- [ ] §3.4 trust-boundary check: workspace-mounted `.codex/config.toml` cannot shadow the persisted registry
- [ ] §4.3 Claude MCP login succeeds end-to-end for at least one provider (real OAuth flow, real provider)
- [ ] §4.4 (or the successful login itself) confirms claude sandboxes are discoverable via labels
- [ ] §5.1 or §5.2: at least one timeout or SIGINT cleanup flow checked
- [ ] §5.4: occupied callback-port failure checked
- [ ] §5.5: no dangling `msb ssh serve` or `ssh -L` processes after any test
- [ ] All of the above ran with restricted egress and an explicit, provider-specific allowlist (§0.6) — not `*_MSB_PUBLIC_EGRESS=1`
- [ ] `git diff main` matches expectations; no unexpected files touched

---

## Appendix — what changed and why

|Area|Change|Why|
|-|-|-|
|`internal/plan/plan.go`|Apply `ai-sandbox.agent` + `ai-sandbox.workspace` labels to every agent (was: codex only).|Blocker for Claude MCP: `FindSandbox("claude", …)` would otherwise always return `ErrNoSandbox`.|
|`internal/runtime/microsandbox/discovery.go`|`FindCodexSandbox` → `FindSandbox(agent, hash)`; errors renamed.|The label was always agent-parametric; the codex naming was accidental specialisation.|
|`internal/runtime/microsandbox/tunnel.go`|`pickLoopbackPort` → `PickLoopbackPort`. Readiness is now process-aware: the fixed host port is preflighted with a real `net.Listen`, and both `msb ssh serve` and the OpenSSH forward are monitored, so an unrelated listener is very unlikely to be mistaken for the child becoming ready (a 300 ms grace window is a heuristic, not a guarantee, on this single-user host threat model).|Closed the false-success sequence where a stale listener on the requested port satisfied a bare TCP check while the real child had already exited (e.g. OpenSSH on a bind collision with `ExitOnForwardFailure=yes`).|
|`cmd/ai-sandbox/callback_op.go` (new)|`CallbackOperation` + `executeCallbackOperation`. Owns SSH-authorized preflight, workspace resolve/validate, sandbox discovery, tunnel open, `msb exec` with timeout/signal handling. Retries only a self-picked ephemeral port that lost the pick/bind race; every other failure (including a caller-fixed port) surfaces immediately.|Shared orchestration for every callback-based auth flow.|
|`cmd/ai-sandbox/codex_login.go`|Collapsed to a `CallbackOperation` builder. Behaviour unchanged.|Extraction, not rewrite.|
|`cmd/ai-sandbox/codex_mcp_login.go` (new)|`codex mcp login <name>` — ephemeral port, guest argv `codex -c mcp_oauth_callback_port=P mcp login <name>`.|Per-client wrapper for Codex MCP.|
|`cmd/ai-sandbox/claude_mcp_login.go` (new)|`claude mcp login --callback-port <P> <name>` — fixed port supplied by the caller (must match `claude mcp add --callback-port`; validated as `1024..65535`, the unprivileged range); guest argv is `claude mcp login <name>` (Claude Code v2.1.231 rejects `--callback-port` on `mcp login`; the flag lives on `mcp add`).|Per-client wrapper for Claude Code MCP.|
|`cmd/ai-sandbox/argv.go` (new)|`reorderFlagsFirst` — splits argv so flags can appear on either side of the positional server name (Go's `flag.Parse` stops at first positional otherwise). Applied to `codex login`, `codex mcp login`, `claude mcp login`.|Documented `--timeout … <name>` and `<name> --timeout …` both work.|
|`scripts/install-ai-sandbox`|Also symlinks `~/.local/bin/ai-sandbox` to the libexec install (dir configurable via `AI_SANDBOX_BIN_DIR`); preflights symlink-path conflicts before replacing the libexec binary. Warns if that dir is not on `PATH`.|The Fish wrappers only route `codex`/`claude` launches; auth subcommands are typed directly and need `PATH` resolution.|
|`cmd/ai-sandbox/main.go`|`protectedRoots` unconditionally protects the PATH symlink directory (default or `AI_SANDBOX_BIN_DIR`) and resolves the actual invocation path from `os.Args[0]`, not just `os.Executable`.|`os.Executable`'s result may be the symlink or its resolved target depending on the OS; relying on it alone could silently drop the symlink directory from the guard.|
|`docs/adr/0008-codex-mcp-oauth-tunnel.md` (new)|Records the client-adapter approach; rejects a host-owned registry; reaffirms no generic listener publishing; supersedes ADR-0007's Consequences bullet.|Design record.|
