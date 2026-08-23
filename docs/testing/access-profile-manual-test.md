# Manual test plan: SSH access profiles

End-to-end verification of `--access` against a real SSH server you control.
The automated suite (`go test -tags integration`) proves mount, environment,
and network-boundary behavior; this walk-through adds what automation cannot:
a genuine SSH round-trip to a machine you designate.

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
sudo mkdir -p /home/sandbox-ssh/.ssh
sudo chmod 700 /home/sandbox-ssh/.ssh
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

## Inspect the plan without launching

```sh
cd some-project
ai-sandbox plan claude --access homelab
```

Expect: an `allow@nas.example.internal:tcp:22` net rule, a read-only
`--mount-dir ...:/run/ai-sandbox/ssh:ro` mount, a read-only
`--mount-file ...:/etc/ssh/ssh_config.d/99-ai-sandbox-access.conf:ro` mount,
and `AI_SANDBOX_SSH_CONFIG` among the injected variables. `plan` must not
create or modify anything under
`~/.config/ai-sandboxes/access/keys/homelab`.

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
ssh -o BatchMode=yes nas                          # interactive check
```

Expected results:

| Check | Expected |
| --- | --- |
| `ssh nas` | Authenticates with the profile key, no host-key prompt (pinned) |
| Wrong key forced: `ssh -o IdentitiesOnly=yes -i /tmp/other nas` | Permission denied |
| Host key mismatch (change a byte in profile `host_keys`, re-run) | `REMOTE HOST IDENTIFICATION HAS CHANGED` — connection refused |
| Unapproved destination: `curl -m5 https://example.com` | Times out (deny-by-default egress) |
| Approved host, unlisted port: `nc -z nas.example.internal 2222` | Refused/times out |

If the authorized entry uses `command=`, `ssh nas echo hi` prints `ok`
regardless of the requested command — proof the server-side restriction holds.

### Troubleshooting: destination does not resolve

Access runs pin your Mac's resolvers (`scutil --dns`) as the sandbox's DNS
upstreams and disable rebind protection, so `home.lan`-style names should
resolve exactly as they do on the host. If `ssh nas` still fails with
`Could not resolve hostname`:

1. Check `ai-sandbox plan claude --access homelab --verbose` shows
   `dns args: --dns-nameserver <ip> ... --no-dns-rebind-protection`. Missing
   entries mean resolver discovery failed — check `scutil --dns | head`.
2. Inside the guest, `cat /etc/resolv.conf` then
   `getent hosts nas.example.internal`.
3. Fallback workaround: use the raw IP address as the profile `host` — the
   connection is made from the host side, so no guest-side resolution is
   involved — and re-pin `host_keys` with `ssh-keyscan <ip>`.

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
  keep the server account restricted. Short-lived certificates (Vault-signed)
  are planned as a follow-up.
- Only one key per profile (`id_ed25519`); additional destinations go in the
  same profile file.
