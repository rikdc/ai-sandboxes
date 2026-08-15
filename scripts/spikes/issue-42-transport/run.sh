#!/usr/bin/env bash
set -o pipefail
cd "$(dirname "$0")" || exit 1

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

probe_a() {
  local name="spike42-a"
  trap 'stop_probe_vm "$name"' RETURN
  boot_probe_vm "$name" --port "127.0.0.1:${SPIKE_HOST_PORT}:${SPIKE_GUEST_PORT}" >/dev/null
  start_guest_listener "$name" 127.0.0.1 "$SPIKE_GUEST_PORT"
  if curl_host "$SPIKE_HOST_PORT"; then
    record 'A: --port H:G, guest bind 127.0.0.1' PASS 'host reached guest-loopback listener'
  else
    record 'A: --port H:G, guest bind 127.0.0.1' FAIL 'host could not reach guest-loopback (expected — confirms #42 comment)'
  fi
}

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

probe_c() {
  local name="spike42-c"
  local serve_pid="" ssh_rc=""
  trap '[ -n "$serve_pid" ] && ssh_tunnel_close "$serve_pid"; stop_probe_vm "$name"' RETURN
  if [ ! -f "$HOME/.microsandbox/ssh/authorized_keys" ]; then
    record 'C: msb ssh serve + ssh -L' SKIP "msb ssh not authorized; run 'msb ssh authorize --file ~/.ssh/id_ed25519.pub' and re-run"
    return
  fi
  boot_probe_vm "$name" >/dev/null
  start_guest_listener "$name" 127.0.0.1 "$SPIKE_GUEST_PORT"
  local out
  out=$(ssh_tunnel_open "$name" "$SPIKE_HOST_PORT" "$SPIKE_GUEST_PORT") || true
  serve_pid=$(printf '%s' "$out" | sed -n '1p')
  ssh_rc=$(printf '%s' "$out" | sed -n '2p')
  if [ "${ssh_rc:-1}" != "0" ]; then
    record 'C: msb ssh serve + ssh -L' FAIL "ssh -L exited ${ssh_rc:-?} — see /tmp/spike42-ssh.log and /tmp/spike42-serve.log"
    return
  fi
  if curl_host "$SPIKE_HOST_PORT"; then
    record 'C: msb ssh serve + ssh -L' PASS 'host reached guest-loopback listener via SSH tunnel'
  else
    record 'C: msb ssh serve + ssh -L' FAIL 'tunnel opened but forward did not reach listener'
  fi
}

probe_a
probe_b
probe_c

echo "spike complete; see RESULTS.md" >&2
