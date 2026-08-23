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
# paste each output line into host_keys below

cat > ~/.config/ai-sandboxes/access/homelab.json <<'EOF'
{
  "schema_version": 1,
  "destinations": [
    {
      "alias": "nas",
      "host": "nas.example.internal",
      "port": 22,
      "user": "sandbox-ssh",
      "host_keys": [
        "nas.example.internal ssh-ed25519 AAAA...your-keyscan-line"
      ]
    }
  ]
}
EOF
chmod 600 ~/.config/ai-sandboxes/access/homelab.json
```

For a server on a non-standard port, scan with `ssh-keyscan -p <port>
nas.example.internal`. The launcher derives the known_hosts selector from the
destination's `port` field — `<host>` for port 22, `[<host>]:<port>`
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

Expect in the printed plan: a `  rule: allow@nas.example.internal:tcp:22`
line under `network`, `access mount: <keydir>:/run/ai-sandbox/ssh:ro`,
`access config mount: <keydir>/config:/etc/ssh/ssh_config.d/99-ai-sandbox-access.conf:ro`,
and `environment: AI_SANDBOX_SSH_CONFIG=/run/ai-sandbox/ssh/config` (the full
`msb run argv:` block shows the same values as flags). `plan` must not create
or modify anything under `~/.config/ai-sandboxes/access/keys/homelab`.

## Run and verify the round trip

```sh
ai-sandbox run claude --access homelab
```

Before each launch the control plane regenerates `config` and `known_hosts`
in the key directory — edit the profile, never the generated files.

Once the Claude session is up, ask it to run:

```sh
echo "$AI_SANDBOX_SSH_CONFIG"                     # /run/ai-sandbox/ssh/config
ls -la /run/ai-sandbox/ssh                        # config, known_hosts, keys
touch /run/ai-sandbox/ssh/probe                   # must fail: read-only
cat /etc/ssh/ssh_config.d/99-ai-sandbox-access.conf  # same hardened config
ssh -o BatchMode=yes nas                          # runs; prints ok under a command= pin
```

Expected results:

| Check | Expected |
| --- | --- |
| `ssh -o BatchMode=yes nas true` | Authenticates with the profile key, no host-key prompt (pinned) |
| Wrong key forced (commands below) | Permission denied — and **not** a silent success via the pinned key |
| Host key mismatch (change a byte in the pinned key body in profile `host_keys`, re-run) | `Host key verification failed` — connection refused, no prompt |
| Unapproved destination: `curl -m5 https://example.com` | Times out (deny-by-default egress) |
| Approved host, unlisted port: `curl -m5 http://nas.example.internal:2222/` | Times out — the rule blocks even the TCP handshake |

The wrong-key check must bypass the generated config. A plain
`ssh -i /tmp/other nas` does **not** work: command-line `-i` is appended to
the config's `IdentityFile` list, so ssh would silently fall back to the
pinned key and authenticate. Force the wrong identity to be the only one:

```sh
ssh-keygen -q -t ed25519 -N '' -f /tmp/other
ssh -o BatchMode=yes -F /dev/null \
    -o UserKnownHostsFile=/run/ai-sandbox/ssh/known_hosts \
    -o StrictHostKeyChecking=yes -o IdentitiesOnly=yes -i /tmp/other \
    sandbox-ssh@nas.example.internal true   # expect: Permission denied
```

If the authorized entry uses `command=`, `ssh nas echo hi` prints `ok`
regardless of the requested command — proof the server-side restriction holds.

### Troubleshooting: destination does not resolve

The SSH connection originates inside the guest VM, so `ssh nas` resolves
`nas.example.internal` with Microsandbox's guest-side DNS. If it fails with
`Could not resolve hostname`:

1. Inside the guest, run `getent hosts nas.example.internal` to confirm.
2. Use an IPv4 literal as the profile `host` instead of a name your host's
   resolver knows but the guest's does not, and re-pin `host_keys` with
   `ssh-keyscan <ip>` (verify the fingerprint before trusting it).

There is no resolver pinning or DNS bypass: v1 requires destinations that
resolve normally inside Microsandbox.

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
- Only one key per profile (`id_ed25519`); additional destinations go in the
  same profile file.
