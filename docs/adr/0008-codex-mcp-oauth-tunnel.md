# ADR-0008: Codex MCP OAuth reuses the operation-scoped SSH tunnel

## Status

Accepted.

## Context

ADR-0007 shipped a scoped `msb ssh serve` + OpenSSH `-L` tunnel for
Codex's account-login OAuth callback on `127.0.0.1:1455`. Its
Consequences section flagged MCP OAuth callbacks (Slack, Notion, …) as
a follow-up and speculated that each provider would need its own
subcommand or discovery mechanism. That conclusion was wrong.

Codex already exposes a stable client-level adapter for MCP OAuth:
`codex mcp login <server-name>` performs the sign-in against a server
declared in Codex's own `~/.codex/config.toml`, and
`mcp_oauth_callback_port` lets the caller pin the callback port so a
loopback tunnel can be established up-front. That collapses the "one
subcommand per provider" fan-out to one host-side wrapper per client.

## Decision

`ai-sandbox codex mcp login <server-name>` reuses the ADR-0007 tunnel
primitive with a per-invocation ephemeral loopback port `P`, tunnels
`127.0.0.1:P → 127.0.0.1:P`, and execs
`codex -c mcp_oauth_callback_port=P mcp login <server-name>` inside the
running codex sandbox. The server name is opaque to `ai-sandbox`;
Codex resolves it from the persisted `codex-home` volume.

The host-side implementation is one shared orchestrator
(`executeCallbackOperation`) plus a ~40-line per-client wrapper. Adding
a new MCP provider requires no Go change and no host-side registry —
it is a user edit to Codex's own config.

**Rejected alternative — host-owned `auth.json` registry.** Would have
mapped `provider → {agent, port, argv}` on the host so
`ai-sandbox auth <provider>` could dispatch generically. Rejected as
premature: Codex's client adapter already covers every provider, so a
registry would manufacture a small badly-documented templating language
for speculative Claude/other-client requirements. Revisit only after
2–3 genuinely different clients need the same treatment.

**Reaffirmed — no generic listener publishing.** The session-profile
schema is still not extended with a `ports` capability. All host↔guest
callback transport goes through the operation-scoped SSH tunnel; the
`msb --port` publishing model remains unused for the same reasons
recorded in ADR-0007 (probe A failed against `127.0.0.1` guest binds).

**Correction to ADR-0007 Consequences.** The bullet "each provider
needs its own subcommand or discovery mechanism" is superseded: one
`codex mcp login <name>` wrapper covers every Codex MCP provider.

## Consequences

- MCP browser sign-in works without host-side per-provider code or
  configuration.
- The MCP server registry stays where it belongs — in Codex's persisted
  config — and `ai-sandbox` never reads or writes it.
- `codex mcp login` execs with `--workdir /home/node --user node` so an
  agent-writable project-mounted `.codex/config.toml` cannot shadow the
  persisted MCP server registry.
- The server name is validated (non-empty, must not start with `-`) to
  prevent flag smuggling into `codex mcp login`.
- Ephemeral host port picks retry up to three times on OpenSSH
  collision (`ExitOnForwardFailure=yes`). Two collisions on the same
  invocation indicate a real problem worth surfacing.
- Account login (`ai-sandbox codex login`) is unchanged; it stays on
  the Codex-imposed fixed port 1455.
- Extending this pattern to another client (Claude, etc.) is a ~40-line
  wrapper file plus an ADR paragraph, provided the client exposes both
  a `<client> mcp login <name>` subcommand and a callback-port override
  equivalent.

## References

- ADR-0007 — the tunnel primitive and account-login flow.
- Issue #42 — original bug report; MCP OAuth was in scope.
- OpenAI MCP documentation — `mcp_oauth_callback_port` semantics.
