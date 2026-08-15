#!/usr/bin/env bash
# Shared helpers for the issue-42 transport spike. Sourced by run.sh.
set -o pipefail

SPIKE_IMAGE="${SPIKE_IMAGE:-node:22-bookworm}"
SPIKE_HOST_PORT="${SPIKE_HOST_PORT:-14551}"
SPIKE_GUEST_PORT="${SPIKE_GUEST_PORT:-1455}"
SPIKE_SSH_PORT="${SPIKE_SSH_PORT:-14552}"

# boot_probe_vm NAME EXTRA_MSB_ARGS...
# Boots a detached VM running `sleep infinity`. Returns the name on stdout.
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
# Starts a background Node HTTP listener inside the guest bound to BIND_ADDR:PORT.
start_guest_listener() {
  local name="$1" bind="$2" port="$3"
  msb exec "$name" -- /bin/sh -c \
    "nohup node -e \"require('http').createServer((_,r)=>r.end('ok')).listen($port,'$bind')\" >/tmp/probe.log 2>&1 &"
  # Give the listener a moment to bind before probing.
  sleep 1
}

# curl_host PORT — returns 0 iff a TCP connection to 127.0.0.1:PORT succeeds.
curl_host() {
  local port="$1"
  curl --silent --show-error --max-time 3 "http://127.0.0.1:${port}/" >/dev/null
}

# record LABEL RESULT NOTE — appends a table row to $SPIKE_RESULTS.
record() {
  printf '| %-40s | %-4s | %s\n' "$1" "$2" "$3" >>"$SPIKE_RESULTS"
}

# ssh_tunnel_open NAME LOCAL_PORT GUEST_PORT
# Starts `msb ssh serve` on 127.0.0.1:$SPIKE_SSH_PORT, then opens an OpenSSH
# -L LOCAL_PORT:127.0.0.1:GUEST_PORT tunnel through it. Prints two lines:
# the serve PID, then the ssh exit code.
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
