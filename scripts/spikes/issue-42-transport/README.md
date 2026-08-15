# Issue #42 transport spike

Boots throwaway Microsandbox VMs to determine which transport reaches a
guest-loopback listener. Results go into `RESULTS.md` in this directory,
which is then attached to issue #42.

Prerequisites: `msb`, `python3`, `ssh` on PATH. Skip on CI when `msb` absent.

Run: `bash scripts/spikes/issue-42-transport/run.sh`

Override the guest image with `SPIKE_IMAGE=<tag>`; defaults to
`python:3.12-slim`.

## What the probes prove

- **A** — `msb --port 127.0.0.1:H:G` with the guest listener on `127.0.0.1:G`.
  Hypothesis (per issue #42 final comment): **FAIL**, because `--port`
  forwards to the guest network interface, not guest loopback.
- **B** — same `--port` mapping but guest listener bound to `0.0.0.0`.
  Hypothesis: **PASS**, confirming (A)'s interpretation.
- **C** — `msb ssh serve` on a host port, then OpenSSH
  `-L 127.0.0.1:H:127.0.0.1:G` against it. Hypothesis: **PASS**, proving
  the transport proposed for a Go-managed `ai-sandbox codex login` command.

A PASS/FAIL matrix is written to `RESULTS.md`. Interpret the matrix per
the plan at `docs/superpowers/plans/2026-08-14-codex-auth-transport-spike.md`.
