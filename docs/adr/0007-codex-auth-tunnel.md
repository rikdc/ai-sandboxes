# ADR-0007: Codex OAuth callback uses a scoped SSH tunnel, not published ports

## Status

Accepted.

## Context

Codex's browser sign-in binds an OAuth callback listener on
`127.0.0.1:1455` inside the guest MicroVM. The host browser then
redirects to `http://localhost:1455/…`, which points at the host — not
the guest — so sign-in fails with "can't connect to the server at
localhost:1455". Issue #42 tracks the bug and the failed sign-in flow.

The obvious fix is to add a host-supplied `ports` capability to the
session-profile schema and pass `msb --port 1455:1455`. An integration
spike (`scripts/spikes/issue-42-transport/`) proved this does not work:
`msb --port H:G` publishes host `127.0.0.1:H` to the guest **network
interface**, not the guest loopback interface. Probe A (guest bound to
`127.0.0.1`) failed as predicted; probe B (guest bound to `0.0.0.0`)
succeeded, confirming the interpretation.

The same spike (probe C) showed that `msb ssh serve` + OpenSSH
`-L 127.0.0.1:H:127.0.0.1:G` does reach a guest-loopback listener.

## Decision

`ai-sandbox codex login` opens an operation-scoped SSH tunnel through
`msb ssh serve`, forwarding host `127.0.0.1:1455` to guest
`127.0.0.1:1455`, then execs `codex login` inside the guest. The tunnel
is torn down when the login command exits, is signalled, or hits its
`--timeout` (default 5 minutes).

The session-profile schema is **not** extended with a `ports` capability.
The tunnel is out-of-band: it does not appear in `plan.RuntimePlan`,
does not participate in the derived-image cache key, and does not change
`--security restricted` or the deny-by-default egress model.

`run codex` labels its sandbox with `ai-sandbox.agent=codex` and
`ai-sandbox.workspace=<hash>` so `codex login` can find the caller's
sandbox with `msb list --label`. Login uses the attach model only: it
never boots its own VM.

## Consequences

- Browser-based Codex sign-in works without exposing any guest service
  to the LAN or public Internet.
- The mechanism is Codex-specific today. Generalising to MCP OAuth
  callbacks (Slack, Notion, …) is possible — the tunnel API takes any
  host/guest port pair. **Update:** ADR-0008 supersedes the claim that
  each provider needs its own subcommand; `codex mcp login <name>`
  covers every Codex MCP provider through one wrapper.
- A one-time host setup is required: `msb ssh authorize --file <pubkey>`
  writes the SSH pubkey `msb ssh serve` will accept. `codex login` fails
  fast with the exact remediation command when the file is absent, so
  the tool never implicitly modifies host state.
- Two concurrent `codex login` invocations for the same workspace would
  both want host `127.0.0.1:1455`; the second one will fail cleanly
  because `-o ExitOnForwardFailure=yes` refuses to open the second
  forward. Acceptable — Codex login is inherently interactive and
  single-user.
- After a successful `codex login`, the `codex` process already running
  in the attached sandbox does not pick up the new credential — Codex
  reads the auth token at startup and caches it in-process. The user
  must exit and restart `codex` in terminal 1 once. Subsequent boots
  reuse the persisted token from the `codex-home` volume. A launcher-
  driven auto-restart was considered and rejected: it adds surprise,
  the one-time cost is trivial, and any in-process reload dance would
  couple us tightly to a Codex CLI internal we do not control.

## References

- Issue #42 — bug report and design discussion.
- `scripts/spikes/issue-42-transport/RESULTS.md` — spike results
  matrix (A=FAIL, B=PASS, C=PASS).
- `docs/superpowers/plans/2026-08-15-codex-login-subcommand.md` —
  implementation plan.
