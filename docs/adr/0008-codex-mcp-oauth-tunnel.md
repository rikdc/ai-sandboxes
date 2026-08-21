# ADR-0008: MCP OAuth reuses the operation-scoped SSH tunnel (Codex + Claude)

## Status

Accepted.

## Context

ADR-0007 shipped a scoped `msb ssh serve` + OpenSSH `-L` tunnel for
Codex's account-login OAuth callback on `127.0.0.1:1455`. Its
Consequences section flagged MCP OAuth callbacks (Slack, Notion, …) as
a follow-up and speculated that each provider would need its own
subcommand or discovery mechanism. That conclusion was wrong.

Both Codex and Claude Code expose a stable client-level adapter for
MCP OAuth, but their port-override models differ:

- **Codex** — `codex mcp login <server-name>` accepts
  `-c mcp_oauth_callback_port=P` at login time. `ai-sandbox` picks an
  ephemeral loopback port `P` per invocation and threads it through.
- **Claude Code** (v2.1.231, the pinned version) — `claude mcp login`
  has **no** callback-port flag. The port is registered *up front* via
  `claude mcp add --scope user --callback-port P --transport http <name> <url>`
  and thereafter the OAuth redirect always targets that port.
  `ai-sandbox claude mcp login --callback-port P <server-name>` tunnels
  that same fixed `P` and execs `claude mcp login <server-name>` inside
  the sandbox.

Each collapses the "one subcommand per provider" fan-out to one
host-side wrapper per client.

## Decision

Two symmetric subcommands, both sharing the ADR-0007 tunnel primitive
through one host-side orchestrator (`executeCallbackOperation`):

- `ai-sandbox codex mcp login <server-name>` picks an ephemeral loopback
  port `P`, tunnels `127.0.0.1:P → 127.0.0.1:P`, and execs
  `codex -c mcp_oauth_callback_port=P mcp login <server-name>` inside
  the running codex sandbox.
- `ai-sandbox claude mcp login --callback-port P <server-name>` tunnels
  the caller-supplied port `P` and execs `claude mcp login <server-name>`
  inside the running claude sandbox. `P` must be the same value the user
  passed to `claude mcp add --callback-port` when registering the server.

Sandbox discovery uses the existing `ai-sandbox.agent` and
`ai-sandbox.workspace` labels, generalised in this change to apply to
all agents (previously codex-only, an accidental specialisation). The
server name is opaque to `ai-sandbox`; each CLI resolves it from its
own persisted config in the agent's home volume.

Each per-client wrapper is a ~40-line file. Adding a new MCP provider
requires no Go change and no host-side registry — it is a user edit to
the CLI's own config.

**Rejected alternative — host-owned `auth.json` registry.** Would have
mapped `provider → {agent, port, argv}` on the host so
`ai-sandbox auth <provider>` could dispatch generically. Rejected as
premature: each client's own adapter already covers every provider it
knows about, so a registry would manufacture a small badly-documented
templating language for speculative requirements. Revisit only after a
future client fails to expose an `mcp login` + callback-port equivalent.

**Reaffirmed — no generic listener publishing.** The session-profile
schema is still not extended with a `ports` capability. All host↔guest
callback transport goes through the operation-scoped SSH tunnel; the
`msb --port` publishing model remains unused for the same reasons
recorded in ADR-0007 (probe A failed against `127.0.0.1` guest binds).

**Correction to ADR-0007 Consequences.** The bullet "each provider
needs its own subcommand or discovery mechanism" is superseded: one
`<client> mcp login <name>` wrapper covers every provider that client
knows about.

## Consequences

- MCP browser sign-in works for both Codex and Claude Code without
  host-side per-provider code or configuration.
- Each MCP server registry stays where it belongs — in the CLI's
  persisted config — and `ai-sandbox` never reads or writes it.
- `codex mcp login` and `claude mcp login` exec with
  `--workdir /home/node --user node` so an agent-writable
  project-mounted config file cannot shadow the persisted MCP server
  registry.
- The server name is validated (non-empty, must not start with `-`) to
  prevent flag smuggling into either CLI's `mcp login`.
- Tunnel readiness is process-aware, not a bare "can I connect?"
  check: the host loopback port is preflighted before any child
  starts, and both `msb ssh serve` and the OpenSSH forward are
  monitored so an unrelated listener already on the expected port can
  never be mistaken for the child becoming ready. Retrying is narrowly
  scoped to the one case where it is safe — a self-picked ephemeral
  port losing the pick/bind TOCTOU race — and even then it is bounded
  and never applied to a caller-fixed port (Claude's registered
  callback port) or to any other failure mode (SSH not authorised,
  `msb ssh serve` not starting, forward bind collision, readiness
  timeout), each of which surfaces as a distinct, immediate error.
- Account login (`ai-sandbox codex login`) is unchanged; it stays on
  the Codex-imposed fixed port 1455.
- Claude has no account-login equivalent under `ai-sandbox` — Claude
  Code's sign-in is out of scope here; only MCP OAuth is covered.
- Sandbox labels are now applied to every agent, not just codex. No
  existing behaviour depends on their absence; `msb list --label`
  queries simply now return matches for claude sandboxes too.
- Extending this pattern to a third client is a ~40-line wrapper file
  plus an ADR paragraph, provided the client exposes both a
  `<client> mcp login <name>` subcommand and a callback-port override
  equivalent.

## References

- ADR-0007 — the tunnel primitive and account-login flow.
- Issue #42 — original bug report; MCP OAuth was in scope.
- OpenAI MCP documentation — `mcp_oauth_callback_port` semantics.
- [Claude Code MCP documentation](https://code.claude.com/docs/en/mcp) —
  `claude mcp login` (no port flag) and `claude mcp add --callback-port`.
