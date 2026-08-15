# Test plan — Phase 2: MCP OAuth tunnel for Codex + Claude

**Scope:** PR #77 on branch `feat/issue-42-codex-mcp-tunnel`. Verifies
`ai-sandbox codex mcp login <name>`, `ai-sandbox claude mcp login <name>`,
and confirms no regression to Phase 1's `ai-sandbox codex login`.

**Time budget:** ~30 minutes if all prerequisites are already in place.

**Anti-goals:** does not test Claude Code's account sign-in (out of scope),
does not test provider-specific OAuth server behaviour (test one provider
per client and trust the mechanism generalises).

---

## 0. Prerequisites

Run once, then skip on subsequent verifications.

| # | Check | Command | Pass |
|---|-------|---------|------|
| 0.1 | On the review branch | `git rev-parse --abbrev-ref HEAD` | prints `feat/issue-42-codex-mcp-tunnel` |
| 0.2 | Binary builds | `go build -o ai-sandbox ./cmd/ai-sandbox` | exit 0, `./ai-sandbox` exists |
| 0.3 | Full test suite | `go test ./...` | prints `ok` for all packages, no `FAIL`, 112 tests total |
| 0.4 | `msb ssh authorize` has been run once ever | `test -s ~/.microsandbox/ssh/authorized_keys && echo ok` | prints `ok`. If not, run `msb ssh authorize --file ~/.ssh/id_ed25519.pub` (or another pubkey) once. |
| 0.5 | Choose a browser sign-in target | pick one Codex MCP server + one Claude MCP server that use OAuth. Suggested: **Notion** (Codex) and **Sentry** (Claude) — both real, both documented, unlikely to collide with each other. Any other OAuth-based MCP works. | you have two provider names in mind |

---

## 1. Static / offline checks

Fast, no browser.

### 1.1 Help text lists both new subcommands

```
./ai-sandbox help
```

**Expect:** the Commands block contains both:

```
  codex mcp login <server-name>    open scoped tunnel + run MCP OAuth sign-in
    [--timeout D]                  for the named server against a running codex sandbox
  claude mcp login <server-name>   open scoped tunnel + run MCP OAuth sign-in
    [--timeout D]                  for the named server against a running claude sandbox
```

### 1.2 Missing / malformed server name is rejected with exit 2

```
./ai-sandbox codex mcp login;     echo "exit=$?"
./ai-sandbox codex mcp login "";  echo "exit=$?"
./ai-sandbox codex mcp login -- -weird; echo "exit=$?"
./ai-sandbox claude mcp login;    echo "exit=$?"
./ai-sandbox claude mcp login ""; echo "exit=$?"
./ai-sandbox claude mcp login -- -weird; echo "exit=$?"
```

**Expect:** each prints `exit=2` and a stderr message mentioning "server name".

### 1.3 Dispatcher rejects unknown subcommands cleanly

```
./ai-sandbox codex bogus; echo "exit=$?"
./ai-sandbox claude bogus; echo "exit=$?"
./ai-sandbox claude mcp bogus; echo "exit=$?"
```

**Expect:** each prints `exit=2` with a clear "unknown subcommand" or "expected subcommand" message.

### 1.4 No running sandbox surfaces the correct remediation

Ensure nothing is running: `msb list --running --format json | jq '.[].name'` should be empty (or none of the entries match codex/claude for your workspace). Then:

```
./ai-sandbox codex mcp login notion;    echo "exit=$?"
./ai-sandbox claude mcp login sentry;   echo "exit=$?"
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

```
./ai-sandbox run codex
```

Wait for the codex REPL prompt.

### 2.2 Run codex account login

Terminal B (same repo checkout):

```
./ai-sandbox codex login
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

The `codex-home` volume persists across sessions. Inside a running codex sandbox (terminal A: `./ai-sandbox run codex`), add the Notion MCP server via the codex REPL or by editing `~/.codex/config.toml`. Refer to Codex's MCP docs for the exact stanza. Verify it's registered:

Inside the codex REPL: `/mcp` should list `notion` as a server pending authentication.

Leave the sandbox running.

### 3.2 Run the login from the host

Terminal B:

```
./ai-sandbox codex mcp login notion
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

```
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

### 4.1 Verify Claude Code CLI is recent enough

Inside a running claude sandbox (terminal A: `./ai-sandbox run claude`):

```
claude --version
```

**Expect:** v2.1.186 or later. Earlier versions do not have `claude mcp login`. If older, `ai-sandbox run claude` should have pulled an up-to-date image; if it didn't, run `./scripts/update` on the host first.

### 4.2 Declare a Sentry MCP server in Claude Code's config

Inside terminal A's claude session, add the Sentry MCP server per Claude Code's docs (`claude mcp add sentry --transport http https://mcp.sentry.dev/mcp` or equivalent — check current docs at https://code.claude.com/docs/en/mcp for the exact command). Verify:

```
claude mcp list
```

**Expect:** `sentry` is listed, marked as needing authentication.

Leave the sandbox running.

### 4.3 Run the login from the host

Terminal B:

```
./ai-sandbox claude mcp login sentry
```

**Expect:**
- Browser opens to Sentry's OAuth consent screen.
- Callback redirects to `http://localhost:<ephemeral-port>/callback` (Claude Code uses `/callback`, differing from Codex's default path — this is expected).
- Terminal B exits 0.
- Inside terminal A, `claude mcp list` now marks `sentry` as authenticated.

### 4.4 Verify labels + discovery for Claude sandbox

With terminal A still running:

```
msb list --running --format json \
  --label ai-sandbox.agent=claude \
  --label ai-sandbox.workspace=$(git rev-parse HEAD | cut -c1-12 || echo unknown)
```

**Expect:** exactly one sandbox listed — the claude sandbox from terminal A. This verifies the label generalisation from `plan.go`. If empty, the label change didn't take effect and the discovery would have failed in step 4.3.

*Note:* the exact `workspace` hash value differs per agent (claude uses `git-blob`, codex uses `sha256`), so the command above only works reliably against a known-correct hash. The simpler evidence that discovery is working is that step 4.3 succeeded rather than reporting "no running claude sandbox".

---

## 5. Error paths against a running sandbox

Optional deeper checks; do at least one.

### 5.1 Timeout is honoured

```
./ai-sandbox codex mcp login notion --timeout 2s
```

Close the browser tab without completing sign-in.

**Expect:** exits with code 1 after ~2 seconds; stderr contains `aborted after 2s`. Tunnel processes are cleaned up.

### 5.2 SIGINT during the flow tears down the tunnel

Start `./ai-sandbox claude mcp login sentry`, then press `ctrl-c` before completing the browser sign-in.

**Expect:** exits promptly with a non-zero code. `pgrep -f "msb ssh serve"` and `pgrep -f "ssh -N -o.*127.0.0.1:.*127.0.0.1:.*"` return nothing.

### 5.3 Multiple sandboxes surface as ambiguous

Start two codex sandboxes for the same workspace (e.g. by running `./ai-sandbox run codex` in two different terminals with the same CWD). Then:

```
./ai-sandbox codex mcp login notion
```

**Expect:** exit 1; stderr contains `multiple running codex sandboxes match this workspace` and suggests `msb stop <name>`.

Cleanup: stop one of them.

---

## 6. Sign-off checklist

Before approving the PR, confirm:

- [ ] §1 static checks all pass
- [ ] §2 Phase 1 regression: `codex login` still works
- [ ] §3.2 Codex MCP login succeeds end-to-end for at least one provider
- [ ] §3.4 trust-boundary check: workspace-mounted `.codex/config.toml` cannot shadow the persisted registry
- [ ] §4.3 Claude MCP login succeeds end-to-end for at least one provider
- [ ] §4.4 (or the successful login itself) confirms claude sandboxes are discoverable via labels
- [ ] At least one §5 error path checked
- [ ] No dangling `msb ssh serve` or `ssh -L` processes after any test
- [ ] `git diff main` matches expectations; no unexpected files touched

---

## Appendix — what changed and why

| Area | Change | Why |
|---|---|---|
| `internal/plan/plan.go` | Apply `ai-sandbox.agent` + `ai-sandbox.workspace` labels to every agent (was: codex only). | Blocker for Claude MCP: `FindSandbox("claude", …)` would otherwise always return `ErrNoSandbox`. |
| `internal/runtime/microsandbox/discovery.go` | `FindCodexSandbox` → `FindSandbox(agent, hash)`; errors renamed. | The label was always agent-parametric; the codex naming was accidental specialisation. |
| `internal/runtime/microsandbox/tunnel.go` | `pickLoopbackPort` → `PickLoopbackPort`. | Callers outside the package now need it (ephemeral-port retry loop). |
| `cmd/ai-sandbox/callback_op.go` (new) | `CallbackOperation` + `executeCallbackOperation`. Owns SSH-authorized preflight, workspace resolve/validate, sandbox discovery, tunnel open (with retry on ephemeral collisions), `msb exec` with timeout/signal handling. | Shared orchestration for every callback-based auth flow. |
| `cmd/ai-sandbox/codex_login.go` | Collapsed to a `CallbackOperation` builder. Behaviour unchanged. | Extraction, not rewrite. |
| `cmd/ai-sandbox/codex_mcp_login.go` (new) | `codex mcp login <name>` — ephemeral port, guest argv `codex -c mcp_oauth_callback_port=P mcp login <name>`. | Per-client wrapper for Codex MCP. |
| `cmd/ai-sandbox/claude_mcp_login.go` (new) | `claude mcp login <name>` — ephemeral port, guest argv `claude mcp login --callback-port P <name>`. | Per-client wrapper for Claude Code MCP. |
| `docs/adr/0008-codex-mcp-oauth-tunnel.md` (new) | Records the client-adapter approach; rejects a host-owned registry; reaffirms no generic listener publishing; supersedes ADR-0007's Consequences bullet. | Design record. |
