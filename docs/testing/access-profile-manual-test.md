# Manual test plan: SSH access profiles

End-to-end verification of `--access` against a real SSH server you control.
The automated suite proves mount, environment, and network-boundary behavior
without a server (`go test ./...`), and a gated integration test drives a full
SSH round-trip through a throwaway sshd VM:

```sh
AI_SANDBOX_MSB_INTEG=1 AI_SANDBOX_ACCESS_INTEG_SSH=1 \
  go test -tags integration -run TestAccessProfileEndToEnd ./cmd/ai-sandbox
```

This walk-through adds what automation cannot: a round-trip to a machine you
designate, on your own hardware.

## Prerequisites

- Microsandbox installed and its daemon running (`msb list` succeeds)
- Docker running with `ai-sandboxes-claude:local` loaded (`./scripts/load-msb`)
- Go 1.26+ to build the CLI from this branch
- A server you can administer over SSH (the "designated server"), reachable
  from your network on port 22

## Build and install from this branch

```sh
git checkout feat/access-profiles
go build ./...
./scripts/install-ai-sandbox
```

Confirm the binary picks up the new flag:

```sh
ai-sandbox help        # usage mentions --access
```

## Prepare the designated server

Create a dedicated, restricted account on the server — do **not** point the
profile at your own account (see docs/claude-security.md, "SSH access": the
guest can copy the private key).

```sh
# on the server
sudo useradd -m -s /bin/bash sandbox-ssh
sudo install -d -m 700 -o sandbox-ssh -g sandbox-ssh /home/sandbox-ssh/.ssh
```

## Create the profile and key pair

```sh
mkdir -p ~/.config/ai-sandboxes/access/keys/homelab
chmod 700 ~/.config/ai-sandboxes/access/keys/homelab
ssh-keygen -t ed25519 -f ~/.config/ai-sandboxes/access/keys/homelab/id_ed25519
```

Authorize the public key on the server, with restrictions:

```sh
# on the server, as root
PUB=$(cat ~you/.config/ai-sandboxes/access/keys/homelab/id_ed25519.pub)  # or paste it
echo 'restrict,command="echo ok",from="192.168.1.0/24" '"$PUB" \
  > /home/sandbox-ssh/.ssh/authorized_keys
chown sandbox-ssh:sandbox-ssh /home/sandbox-ssh/.ssh/authorized_keys
chmod 600 /home/sandbox-ssh/.ssh/authorized_keys
```

(`restrict` disables forwarding and pty; drop the `command=` pin if you want
an interactive shell for the first pass, then tighten it.)

Write the profile. You need the server's host key line:

```sh
ssh-keyscan nas.example.internal
# paste the "<algo> <key>" tail into host_keys below (the selector prefix is optional)

cat > ~/.config/ai-sandboxes/access/homelab.json <<'EOF'
{
  "schema_version": 1,
  "host": "nas.example.internal",
  "port": 22,
  "user": "sandbox-ssh",
  "host_keys": [
    "nas.example.internal ssh-ed25519 AAAA...your-keyscan-line"
  ]
}
EOF
chmod 600 ~/.config/ai-sandboxes/access/homelab.json
```

The profile name is the guest-side ssh alias: this destination is reachable as
`ssh homelab`. One profile pins exactly one destination; create one access
profile per machine.

For a server on a non-standard port, scan with `ssh-keyscan -p <port>
nas.example.internal`. The launcher derives the known_hosts selector from the
profile's `port` field — `<host>` for port 22, `[<host>]:<port>`
otherwise. A pasted three-field line must carry that exact selector; you can
also drop the selector and pin just `<algo> <key>`. The profile host must
resolve inside Microsandbox — the SSH connection originates in the guest VM,
not on your Mac — or be an IPv4 literal.

`ssh-keyscan` does not authenticate the key it returns: anything between you
and the server can hand you its own key. Verify the fingerprint against an
independent trusted channel (the server's console, `ssh-keygen -lf` on a key
you obtained out-of-band) before pinning it.

## Inspect the plan without launching

```sh
cd some-project
ai-sandbox plan claude --access homelab
```

Expect in the printed plan: a `rule: allow@nas.example.internal:tcp:22`
line under `network`, a `dns args:` line with the pinned host resolvers and
`--no-dns-rebind-protection`, `access mount: <keydir>:/run/ai-sandbox/ssh:ro`,
and `access config mount: <keydir>/config:/etc/ssh/ssh_config.d/99-ai-sandbox-access.conf:ro`
(the full `msb run argv:` block shows the same values as flags). `plan` must
not create or modify anything under
`~/.config/ai-sandboxes/access/keys/homelab`.

## Run and verify the round trip

```sh
ai-sandbox run claude --access homelab
```

Before each launch the control plane regenerates `config` and `known_hosts`
in the key directory — edit the profile, never the generated files.

Once the Claude session is up, ask it to run:

```sh
ls -la /run/ai-sandbox/ssh                        # config, known_hosts, keys
touch /run/ai-sandbox/ssh/probe                   # must fail: read-only
cat /etc/ssh/ssh_config.d/99-ai-sandbox-access.conf  # same hardened config
grep '^nameserver' /etc/resolv.conf               # your Mac's resolver (pinned)
ssh -o BatchMode=yes homelab                      # runs; prints ok under a command= pin
```

Expected results:

| Check | Expected |
| --- | --- |
| `ssh -o BatchMode=yes homelab true` | Authenticates with the profile key, no host-key prompt (pinned) |
| Host key mismatch (change a byte in the pinned key body in profile `host_keys`, re-run) | `Host key verification failed` — connection refused, no prompt |
| Unapproved destination: `curl -m5 https://example.com` | Times out (deny-by-default egress) |
| Approved host, unlisted port: `curl -m5 http://nas.example.internal:2222/` | Times out — the rule blocks even the TCP handshake |

If the authorized entry uses `command=`, `ssh homelab echo hi` prints `ok`
regardless of the requested command — proof the server-side restriction holds.

### Troubleshooting: destination does not resolve

Access runs pin your Mac's resolvers (`scutil --dns`) as the sandbox's DNS
upstreams and disable rebind protection, so `home.lan`-style names should
resolve exactly as they do on the host. If `ssh homelab` still fails with
`Could not resolve hostname`:

1. Check `ai-sandbox plan claude --access homelab --verbose` shows
   `dns args:      --dns-nameserver <ip> ... --no-dns-rebind-protection`.
   Missing entries mean resolver discovery failed — check `scutil --dns | head`.
2. Inside the guest, run `cat /etc/resolv.conf` then
   `getent hosts nas.example.internal` to confirm.
3. Fallback workaround: use the raw IPv4 literal as the profile `host` and
   re-pin `host_keys` with `ssh-keyscan <ip>` (verify the fingerprint before
   trusting it).

Note: a fresh VM answering NXDOMAIN for *everything* (public names included)
is a known Microsandbox auto-discovery flake; relaunching fixes it. Pinned
resolvers avoid it entirely.

## Cleanup

```sh
rm -rf ~/.config/ai-sandboxes/access/keys/homelab \
       ~/.config/ai-sandboxes/access/homelab.json
# remove the server account once testing is done
```

## Known limitations (MVP)

- Private keys are readable by the guest by design; treat them as exposed and
  keep the server account restricted. Revoking the key on the server is the
  expiry mechanism — there is no certificate issuance.
- Pinned host-key checking is enforced by the generated ssh config only; the
  guest can point its own ssh client elsewhere. The real boundaries are the
  network rules, the dedicated account, and the server-side
  `authorized_keys` restrictions (see docs/claude-security.md).
- Only one key per profile (`id_ed25519`); one destination per profile —
  create one access profile per machine.
