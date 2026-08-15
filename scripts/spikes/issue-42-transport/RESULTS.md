# Issue #42 transport spike results

msb version: msb 0.6.8
date: 2026-08-15T00:39:16Z

| Probe                                    | Res  | Notes
|------------------------------------------|------|------
| A: --port H:G, guest bind 127.0.0.1      | FAIL | host could not reach guest-loopback (expected — confirms #42 comment)
| B: --port H:G, guest bind 0.0.0.0        | PASS | confirms --port targets guest network interface
| C: msb ssh serve + ssh -L                | PASS | host reached guest-loopback listener via SSH tunnel

## Conclusion

Results match the hypothesis in the issue #42 final comment exactly:

- `msb --port H:G` publishes host `127.0.0.1:H` to the guest **network
  interface**, not guest loopback (A=FAIL, B=PASS). A schema built on
  `ports: [1455]` therefore cannot reach Codex's guest-loopback callback
  listener and must not ship.
- `msb ssh serve` + OpenSSH `-L 127.0.0.1:H:127.0.0.1:G` does reach a
  guest-loopback listener (C=PASS). This is the transport for Phase 1.

**Recommended next step:** write a Phase 1 plan implementing an
`ai-sandbox codex login` subcommand that opens a Go-managed msb-ssh
`-L` tunnel scoped to the login operation, then tears it down. Keep the
tunnel out of `plan.RuntimePlan`; it is an operation-scoped side-channel,
not a run-flag. Do not add a `ports` field to the session-profile schema.

**One-time host setup discovered by the spike:** `msb ssh serve` requires
`msb ssh authorize --file <pubkey>` to be run once. Phase 1's launcher
must detect a missing `~/.microsandbox/ssh/authorized_keys` and prompt
the user (or fail with the exact remediation command).
