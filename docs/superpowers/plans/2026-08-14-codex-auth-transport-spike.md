# Codex Auth Transport Spike Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Empirically determine which host→guest transport actually reaches a guest-`127.0.0.1` listener under our installed Microsandbox, so we can pick the right shape for a Codex login fix (issue #42) without shipping a schema built on an unverified assumption.

**Architecture:** Add a self-contained, opt-in shell spike under `scripts/spikes/issue-42-transport/` that boots a throwaway MicroVM, starts a probe listener inside it, and exercises three candidate transports (`--port H:G` loopback, `--port H:G` with a `0.0.0.0` guest bind, and `msb ssh serve` + `ssh -L`). Results are printed as a pass/fail matrix and appended to a `RESULTS.md` the operator commits back to the issue.

**Tech Stack:** bash, `msb` (Microsandbox 0.6.8+ CLI, already installed at `/opt/homebrew/bin/msb`), OpenSSH client, `python3` inside a stock image for the probe listener.

**Spec:** github.com/rikdc/ai-sandboxes issue #42 (final comment) — argues that `--port H:G` targets the guest network interface, not guest loopback, and proposes a Go-managed SSH `-L` tunnel scoped to a login operation. This plan produces the evidence that gates that follow-up.

## Global Constraints

- Do not modify any production code paths (`cmd/`, `internal/`, `scripts/session/`, `scripts/tools/`). This plan adds only under `scripts/spikes/`.
- Spike must be skippable in CI: exit 0 with a `skip:` message if `msb` is not on `PATH`.
- No secrets, no long-lived sandboxes: every probe boots, tests, and tears down its own MicroVM with a unique `--name`.
- Every probe records both what was attempted and the observed outcome — a red result is a valid deliverable.
- Bind the host side of every `--port` and `ssh` listener to `127.0.0.1` explicitly. Never bind to `0.0.0.0` on the host.

---

### Task 1: Spike scaffolding and probe listener

**Files:**
- Create: `scripts/spikes/issue-42-transport/README.md`
- Create: `scripts/spikes/issue-42-transport/run.sh`
- Create: `scripts/spikes/issue-42-transport/lib.sh`
- Create: `scripts/spikes/issue-42-transport/RESULTS.md`

**Interfaces:**
- Consumes: `msb` on PATH, `python3` and `ssh` on host PATH, an available stock image tag (default `python:3.12-slim`, overridable via `SPIKE_IMAGE`).
- Produces: `run.sh` entrypoint that executes all probes in sequence and prints a final `PASS/FAIL` matrix; `lib.sh` exporting `boot_probe_vm`, `stop_probe_vm`, `start_guest_listener HOST_BIND`, `curl_host PORT`, all used unchanged by later tasks.

- [ ] **Step 1: Write the scaffolding**

`scripts/spikes/issue-42-transport/README.md`:

```markdown
# Issue #42 transport spike

Boots throwaway Microsandbox VMs to determine which transport reaches a
guest-loopback listener. Results go into `RESULTS.md` in this directory,
which is then attached to issue #42.

Prerequisites: `msb`, `python3`, `ssh` on PATH. Skip on CI when `msb` absent.

Run: `bash scripts/spikes/issue-42-transport/run.sh`

Override the guest image with `SPIKE_IMAGE=<tag>`; defaults to
`python:3.12-slim`.
```

`scripts/spikes/issue-42-transport/lib.sh`:

```bash
#!/usr/bin/env bash
# Shared helpers for the issue-42 transport spike. Sourced by run.sh.
set -o pipefail

SPIKE_IMAGE="${SPIKE_IMAGE:-python:3.12-slim}"
SPIKE_HOST_PORT="${SPIKE_HOST_PORT:-14551}"
SPIKE_GUEST_PORT="${SPIKE_GUEST_PORT:-1455}"
SPIKE_SSH_PORT="${SPIKE_SSH_PORT:-14552}"

# boot_probe_vm NAME EXTRA_MSB_ARGS...
# Boots a detached VM running `sleep infinity` and returns its name on stdout.
boot_probe_vm() {
  local name="$1"; shift
  msb run --detach --no-tty --name "$name" "$@" "$SPIKE_IMAGE" -- \
    /bin/sh -c 'sleep infinity' >/dev/null
  printf '%s\n' "$name"
}

stop_probe_vm() {
  local name="$1"
  msb stop "$name" >/dev/null 2>&1 || true
  msb rm "$name" >/dev/null 2>&1 || true
}

# start_guest_listener NAME BIND_ADDR PORT
# Starts a background python http.server inside the guest bound to BIND_ADDR:PORT.
start_guest_listener() {
  local name="$1" bind="$2" port="$3"
  msb exec "$name" -- /bin/sh -c \
    "nohup python3 -m http.server $port --bind $bind >/tmp/probe.log 2>&1 &"
  # Give the listener a moment to bind before probing.
  sleep 1
}

# curl_host PORT — returns 0 iff a TCP connection to 127.0.0.1:PORT succeeds
# and returns any HTTP body. Uses --max-time to fail fast on filtered ports.
curl_host() {
  local port="$1"
  curl --silent --show-error --max-time 3 "http://127.0.0.1:${port}/" >/dev/null
}

record() {
  # record LABEL RESULT NOTE
  printf '| %-40s | %-4s | %s\n' "$1" "$2" "$3" >>"$SPIKE_RESULTS"
}
```

`scripts/spikes/issue-42-transport/run.sh`:

```bash
#!/usr/bin/env bash
set -o pipefail
cd "$(dirname "$0")"

if ! command -v msb >/dev/null 2>&1; then
  echo 'skip: msb not installed' >&2
  exit 0
fi

# shellcheck source=lib.sh
. ./lib.sh

SPIKE_RESULTS="$(pwd)/RESULTS.md"
: >"$SPIKE_RESULTS"
{
  echo "# Issue #42 transport spike results"
  echo
  echo "msb version: $(msb --version)"
  echo "date: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo
  echo '| Probe                                    | Res  | Notes'
  echo '|------------------------------------------|------|------'
} >>"$SPIKE_RESULTS"

# Individual probes append here; see later tasks.
echo "spike complete; see RESULTS.md" >&2
```

`scripts/spikes/issue-42-transport/RESULTS.md`: empty placeholder (will be overwritten by `run.sh`).

- [ ] **Step 2: Verify scaffolding runs green with zero probes**

Run: `chmod +x scripts/spikes/issue-42-transport/run.sh && scripts/spikes/issue-42-transport/run.sh`
Expected: exits 0, `RESULTS.md` contains header only, no VMs left behind (`msb ls` shows nothing new).

- [ ] **Step 3: Commit**

```bash
git add scripts/spikes/issue-42-transport/README.md \
        scripts/spikes/issue-42-transport/run.sh \
        scripts/spikes/issue-42-transport/lib.sh \
        scripts/spikes/issue-42-transport/RESULTS.md
git commit -m "spike(#42): scaffold Codex-auth transport probes"
```

---

### Task 2: Probe A — `--port` with guest bound to loopback (expected FAIL)

**Files:**
- Modify: `scripts/spikes/issue-42-transport/run.sh` (append probe A before the final `echo`)

**Interfaces:**
- Consumes: `boot_probe_vm`, `start_guest_listener`, `curl_host`, `record` from Task 1.
- Produces: appends a row to `RESULTS.md` labelled `A: --port H:G, guest bind 127.0.0.1`.

- [ ] **Step 1: Append probe A**

Insert immediately before `echo "spike complete..."`:

```bash
probe_a() {
  local name="spike42-a"
  trap 'stop_probe_vm "$name"' RETURN
  boot_probe_vm "$name" --port "127.0.0.1:${SPIKE_HOST_PORT}:${SPIKE_GUEST_PORT}" >/dev/null
  start_guest_listener "$name" 127.0.0.1 "$SPIKE_GUEST_PORT"
  if curl_host "$SPIKE_HOST_PORT"; then
    record 'A: --port H:G, guest bind 127.0.0.1' PASS 'host reached guest-loopback listener'
  else
    record 'A: --port H:G, guest bind 127.0.0.1' FAIL 'host could not reach guest-loopback (expected — confirms comment)'
  fi
}
probe_a
```

- [ ] **Step 2: Run and inspect**

Run: `scripts/spikes/issue-42-transport/run.sh && cat scripts/spikes/issue-42-transport/RESULTS.md`
Expected: row A recorded. FAIL is the hypothesis; either outcome is a valid data point — do not "fix" a FAIL, that is the finding.

- [ ] **Step 3: Commit**

```bash
git add scripts/spikes/issue-42-transport/run.sh scripts/spikes/issue-42-transport/RESULTS.md
git commit -m "spike(#42): probe A — --port H:G with guest loopback bind"
```

---

### Task 3: Probe B — `--port` with guest bound to `0.0.0.0` (expected PASS)

**Files:**
- Modify: `scripts/spikes/issue-42-transport/run.sh` (append probe B)

**Interfaces:** same as Task 2.

- [ ] **Step 1: Append probe B**

```bash
probe_b() {
  local name="spike42-b"
  trap 'stop_probe_vm "$name"' RETURN
  boot_probe_vm "$name" --port "127.0.0.1:${SPIKE_HOST_PORT}:${SPIKE_GUEST_PORT}" >/dev/null
  start_guest_listener "$name" 0.0.0.0 "$SPIKE_GUEST_PORT"
  if curl_host "$SPIKE_HOST_PORT"; then
    record 'B: --port H:G, guest bind 0.0.0.0' PASS 'confirms --port targets guest network interface'
  else
    record 'B: --port H:G, guest bind 0.0.0.0' FAIL 'unexpected — investigate before Phase 1'
  fi
}
probe_b
```

- [ ] **Step 2: Run and inspect**

Run: `scripts/spikes/issue-42-transport/run.sh && cat scripts/spikes/issue-42-transport/RESULTS.md`
Expected: row B is PASS. A FAIL here means our understanding of `--port` is also wrong; stop and re-read msb source before proceeding.

- [ ] **Step 3: Commit**

```bash
git add scripts/spikes/issue-42-transport/run.sh scripts/spikes/issue-42-transport/RESULTS.md
git commit -m "spike(#42): probe B — --port H:G with 0.0.0.0 guest bind"
```

---

### Task 4: Probe C — `msb ssh serve` + `ssh -L` reaches guest loopback (expected PASS)

**Files:**
- Modify: `scripts/spikes/issue-42-transport/run.sh` (append probe C)
- Modify: `scripts/spikes/issue-42-transport/lib.sh` (add `ssh_tunnel_open`, `ssh_tunnel_close`)

**Interfaces:**
- Consumes: `boot_probe_vm`, `start_guest_listener`, `curl_host`, `record`, plus a stock OpenSSH client on the host.
- Produces: proves (or disproves) the transport the issue #42 final comment proposes for Phase 1.

- [ ] **Step 1: Extend `lib.sh`**

Append to `lib.sh`:

```bash
# ssh_tunnel_open NAME LOCAL_PORT GUEST_PORT
# Starts `msb ssh serve` on 127.0.0.1:$SPIKE_SSH_PORT, then opens an OpenSSH
# -L LOCAL_PORT:127.0.0.1:GUEST_PORT tunnel through it. Prints the PIDs of
# the serve process and the ssh process on separate lines.
ssh_tunnel_open() {
  local name="$1" local_port="$2" guest_port="$3"
  msb ssh serve --host 127.0.0.1 --port "$SPIKE_SSH_PORT" "$name" \
    >/tmp/spike42-serve.log 2>&1 &
  local serve_pid=$!
  sleep 1
  ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
      -o ExitOnForwardFailure=yes -N -f \
      -L "127.0.0.1:${local_port}:127.0.0.1:${guest_port}" \
      -p "$SPIKE_SSH_PORT" root@127.0.0.1 \
      >/tmp/spike42-ssh.log 2>&1
  local ssh_rc=$?
  printf '%s\n%s\n' "$serve_pid" "$ssh_rc"
}

ssh_tunnel_close() {
  local serve_pid="$1"
  pkill -f "ssh .* -L 127.0.0.1:${SPIKE_HOST_PORT}:127.0.0.1" 2>/dev/null || true
  kill "$serve_pid" 2>/dev/null || true
}
```

- [ ] **Step 2: Append probe C to `run.sh`**

```bash
probe_c() {
  local name="spike42-c"
  trap 'ssh_tunnel_close "$serve_pid"; stop_probe_vm "$name"' RETURN
  boot_probe_vm "$name" >/dev/null
  start_guest_listener "$name" 127.0.0.1 "$SPIKE_GUEST_PORT"
  local out serve_pid ssh_rc
  out=$(ssh_tunnel_open "$name" "$SPIKE_HOST_PORT" "$SPIKE_GUEST_PORT") || true
  serve_pid=$(printf '%s' "$out" | sed -n '1p')
  ssh_rc=$(printf '%s' "$out" | sed -n '2p')
  if [ "${ssh_rc:-1}" != "0" ]; then
    record 'C: msb ssh serve + ssh -L' FAIL "ssh -L exited $ssh_rc — see /tmp/spike42-ssh.log and /tmp/spike42-serve.log"
    return
  fi
  if curl_host "$SPIKE_HOST_PORT"; then
    record 'C: msb ssh serve + ssh -L' PASS 'host reached guest-loopback listener via SSH tunnel'
  else
    record 'C: msb ssh serve + ssh -L' FAIL 'tunnel opened but forward did not reach listener'
  fi
}
probe_c
```

- [ ] **Step 3: Run and inspect**

Run: `scripts/spikes/issue-42-transport/run.sh && cat scripts/spikes/issue-42-transport/RESULTS.md`
Expected outcomes:
- PASS: Phase 1 (SSH-tunnel-based `ai-sandbox codex login`) is viable — proceed to write that plan.
- FAIL because `msb ssh serve` is not available or refuses non-interactive use: escalate — Phase 1 needs a different transport (candidates: `msb exec` piping stdio to a host-side listener, or an upstream feature request).
- FAIL because the forward opens but does not reach the listener: same escalation.

- [ ] **Step 4: Commit**

```bash
git add scripts/spikes/issue-42-transport/lib.sh \
        scripts/spikes/issue-42-transport/run.sh \
        scripts/spikes/issue-42-transport/RESULTS.md
git commit -m "spike(#42): probe C — msb ssh serve + ssh -L to guest loopback"
```

---

### Task 5: Record findings on the issue

**Files:**
- Modify: `scripts/spikes/issue-42-transport/RESULTS.md` (final human-written summary block)

- [ ] **Step 1: Append a summary section**

At the bottom of `RESULTS.md`, hand-write a 3–5 line conclusion in prose stating which probes passed, and — based on results — which of the two branches to take:

- All-as-expected (A FAIL, B PASS, C PASS) → write Phase 1 plan: `ai-sandbox codex login` opens a Go-managed msb-ssh `-L` tunnel scoped to the login operation.
- C FAIL → open an upstream issue against Microsandbox and pause Phase 1 until a supported transport exists. Do not fall back to `ports: [1455]` in the session profile schema.

- [ ] **Step 2: Post to GitHub**

Run:

```bash
gh issue comment 42 --repo rikdc/ai-sandboxes --body-file scripts/spikes/issue-42-transport/RESULTS.md
```

- [ ] **Step 3: Open PR from `fix/issue-42-codex-auth-tunnel`**

Run:

```bash
git push -u origin fix/issue-42-codex-auth-tunnel
gh pr create --title "spike(#42): Codex auth transport probes" \
  --body "$(cat <<'EOF'
## Summary
- Adds an opt-in, skippable spike under `scripts/spikes/issue-42-transport/` that empirically tests three host→guest transports against a guest-loopback listener.
- No production code paths touched. Results are attached to issue #42.

## Test plan
- [ ] `scripts/spikes/issue-42-transport/run.sh` on a Mac with msb installed
- [ ] Inspect `RESULTS.md`; confirm A=FAIL, B=PASS, C=PASS (or record deviations)
- [ ] Confirm no leftover `msb` sandboxes after run (`msb ls`)
EOF
)"
```

---

## Self-Review

**Spec coverage:** Issue #42 final comment asks for an integration spike proving (or disproving) that `--port H:G` reaches a guest-loopback listener, before choosing a schema. Task 2 tests exactly that; Task 3 confirms the interpretation (network-interface bind); Task 4 tests the proposed alternative transport. Task 5 gates the follow-up on the observed result. Covered.

**Placeholder scan:** All shell blocks contain runnable content. The only free-form text is the `RESULTS.md` summary in Task 5, which is deliberately human-written interpretation, not code.

**Type consistency:** Env-var names (`SPIKE_IMAGE`, `SPIKE_HOST_PORT`, `SPIKE_GUEST_PORT`, `SPIKE_SSH_PORT`, `SPIKE_RESULTS`) and helper signatures (`boot_probe_vm NAME EXTRA...`, `start_guest_listener NAME BIND PORT`, `curl_host PORT`, `record LABEL RES NOTE`, `ssh_tunnel_open NAME LOCAL GUEST`) are used identically in every task that references them.
