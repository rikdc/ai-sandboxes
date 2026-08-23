//go:build integration

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rikdc/ai-sandboxes/internal/plan"
	"github.com/rikdc/ai-sandboxes/internal/runtime/microsandbox"
)

// TestAccessProfileEndToEnd exercises the full --access runtime contract
// against real Microsandbox: the control plane resolves an access profile,
// materializes its ssh config and pinned known_hosts, and launches a VM whose
// guest sees the credential directory read-only, gets the hardened ssh
// configuration through the system-wide include, resolves the destination's
// hostname through the pinned host resolver (a dnsmasq inside the stand-in
// VM), authenticates with the dedicated key against a live sshd stand-in,
// rejects a tampered pinned host key, cannot reach an unlisted port even
// though it serves traffic, and exits successfully.
//
// Run with:
//
//	AI_SANDBOX_MSB_INTEG=1 go test -tags integration -run TestAccessProfileEndToEnd ./cmd/ai-sandbox
//
// Skipped unless AI_SANDBOX_MSB_INTEG=1 and msb/docker are usable, matching
// tunnel_integ_test.go.
func TestAccessProfileEndToEnd(t *testing.T) {
	if os.Getenv("AI_SANDBOX_MSB_INTEG") != "1" {
		t.Skip("set AI_SANDBOX_MSB_INTEG=1 to run the msb integration tests")
	}
	for _, tool := range []string{"msb", "docker", "ssh-keygen", "ssh-keyscan"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}

	client := &microsandbox.Client{Out: os.Stderr}
	present, err := client.ImagePresent("ai-sandboxes-claude:local")
	if err != nil {
		t.Fatalf("msb image check: %v", err)
	}
	if !present {
		t.Skip("ai-sandboxes-claude:local not loaded; run ./scripts/load-msb first")
	}

	// --- Host-side setup: profile, dedicated key directory, egress file ---
	// Everything the guest mounts must live under $HOME: microsandbox on
	// macOS does not share /var/folders (where t.TempDir lands) into VMs.
	configDir := tempDirUnderHome(t)
	keyDir := filepath.Join(configDir, "access", "keys", "homelab")
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	genKey(t, filepath.Join(keyDir, "id_ed25519"))

	// The live destination must exist (and have contributed its address and
	// host key to the profile) before executeRun materializes config and
	// known_hosts from the profile.
	const dnsName = "sshd.access-integ.test"
	serverName, serverIP, hostKey := startSSHServerVM(t, dnsName,
		filepath.Join(keyDir, "id_ed25519.pub"))

	writeProfile(t, filepath.Join(configDir, "access", "homelab.json"),
		dnsName, 22, "claude", hostKey)

	// Resolver override: pin the stand-in VM's dnsmasq as the only "host"
	// resolver, so --dns-nameserver carries an address we control and the
	// guest must actually resolve dnsName through it.
	resolvConf := filepath.Join(tempDirUnderHome(t), "resolv.conf")
	if err := os.WriteFile(resolvConf, []byte("nameserver "+serverIP+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	home := tempDirUnderHome(t)
	egressDir := filepath.Join(home, ".config", "microvms")
	if err := os.MkdirAll(egressDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Deliberately empty allowlist: nothing beyond gateway DNS is permitted,
	// except what --access adds.
	os.WriteFile(filepath.Join(egressDir, "claude-egress"), []byte("# nothing\n"), 0o600)
	project := tempDirUnderHome(t)

	e := currentEnv()
	e.cwd = project
	e.home = home
	e.resolvConfPath = resolvConf
	baseGetenv := e.getenv
	e.getenv = func(k string) string {
		if k == "AI_SANDBOX_CONFIG_DIR" {
			return configDir
		}
		return baseGetenv(k)
	}

	opts := runOptions{agent: "claude", access: "homelab"}
	code := executeRun(context.Background(), opts, e, os.Stderr, client,
		func([]string) error { return nil })
	if code != 0 {
		t.Fatalf("executeRun exit code = %d", code)
	}

	// The rendered material must exist on the host before launch.
	for _, f := range []string{"config", "known_hosts"} {
		if _, err := os.Stat(filepath.Join(keyDir, f)); err != nil {
			t.Fatalf("run did not materialize %s: %v", f, err)
		}
	}

	p := resolveForTest(t, opts, e, client)

	// Access runs pin the host's discovered resolvers and opt out of rebind
	// protection so private-address answers survive.
	argv := p.MsbArgv()
	if !hasFlagValue(argv, "--dns-nameserver") {
		t.Errorf("access plan missing --dns-nameserver: %v", argv)
	}
	if !hasFlag(argv, "--no-dns-rebind-protection") {
		t.Errorf("access plan missing --no-dns-rebind-protection: %v", argv)
	}
	resolverIP := flagValue(argv, "--dns-nameserver")

	sandbox := fmt.Sprintf("ai-sandbox-access-integ-%d", time.Now().Unix())
	launchAccessSandbox(t, sandbox, p, accessProbeScript(dnsName, serverIP, resolverIP))

	// The probe runs as the sandbox's main process (the only place the
	// injected environment exists; `msb exec` sessions do not inherit it)
	// and reports through the shared workspace. Its final line doubles as
	// proof the guest process ran to successful completion.
	waitForProbeLog(t, workspaceHostPath(t, p), "PROBE-OK", time.Minute)
	t.Logf("ssh stand-in server %s at %s stayed up for the whole probe", serverName, serverIP)
}

// hasFlag reports whether argv carries flag.
func hasFlag(argv []string, flag string) bool {
	for _, a := range argv {
		if a == flag {
			return true
		}
	}
	return false
}

// hasFlagValue reports whether argv carries flag followed by any value.
func hasFlagValue(argv []string, flag string) bool {
	return flagValue(argv, flag) != ""
}

// flagValue returns the value following flag in argv, or "".
func flagValue(argv []string, flag string) string {
	for i, a := range argv {
		if a == flag && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}

// accessProbeScript runs inside the guest and asserts every property the
// --access design promises at runtime: the credential mount is complete and
// read-only, bare `ssh homelab` resolves through the system-wide include with
// the hardened options, guest DNS uses the pinned host resolver, the pinned
// key authenticates against the live server, a tampered pinned host key is
// rejected, an unlisted port stays unreachable even though it serves traffic,
// and the script exits zero. dnsName is the hostname the profile pins;
// serverIP is the address the pinned resolver maps it to; resolverIP is the
// pinned DNS upstream from the plan argv.
func accessProbeScript(dnsName, serverIP, resolverIP string) string {
	return fmt.Sprintf(`
set -eu
echo "user=$(id -un)"
for f in config known_hosts id_ed25519 id_ed25519.pub; do
  test -f "/run/ai-sandbox/ssh/$f" || { echo "MISSING $f"; exit 1; }
done
if touch /run/ai-sandbox/ssh/probe 2>/dev/null; then
  echo "MOUNT IS WRITABLE"; exit 1
fi
# Bare "ssh homelab" (no -F) must resolve the profile name through the
# system-wide include with the hardened options.
ssh -G homelab > /tmp/ssh-G.txt 2>&1 || { cat /tmp/ssh-G.txt; exit 1; }
for want in "^user claude$" "^hostname %s$" "^port 22$" "^identityfile .*/run/ai-sandbox/ssh/id_ed25519$" "^stricthostkeychecking (yes|true)$" "^forwardagent no$" "^clearallforwardings yes$" "^identitiesonly yes$" "^passwordauthentication no$"; do
  grep -Eiq "$want" /tmp/ssh-G.txt || { echo "CONFIG MISSING $want"; cat /tmp/ssh-G.txt; exit 1; }
done
# Guest DNS must point at the pinned host resolver.
grep -q "^nameserver %s$" /etc/resolv.conf || { echo "RESOLVER NOT PINNED"; cat /etc/resolv.conf; exit 1; }
# The pinned resolver must actually answer: the profile pins a name, not an
# address, so every later step depends on this lookup succeeding.
got="$(getent hosts %s || true)"
case "$got" in
  "%s "*) ;;
  *) echo "DNS RESOLUTION FAILED"; echo "got: $got"; exit 1 ;;
esac
# The pinned key authenticates end to end (the HostName above resolves
# through the pinned resolver, then the key matches the pinned entry).
if ! ssh -o BatchMode=yes homelab true 2>/tmp/ssh-ok.err; then
  echo "SSH KEY AUTH FAILED"; cat /tmp/ssh-ok.err; exit 1
fi
# Corrupt one character inside the pinned key body, keeping the selector and
# algorithm intact so ssh finds the entry but must reject the mismatched key.
cp /run/ai-sandbox/ssh/known_hosts /tmp/tampered_known_hosts
sed -i 's/AAAAC3NzaC1lZDI1NTE5AAAAI/AAAAC3NzaC1lZDI1NTE5AAAAB/' /tmp/tampered_known_hosts
if ssh -o BatchMode=yes -o UserKnownHostsFile=/tmp/tampered_known_hosts \
    -o StrictHostKeyChecking=yes homelab true 2>/tmp/ssh-tamper.err; then
  echo "TAMPERED HOST KEY ACCEPTED"; exit 1
fi
grep -qi "host key verification failed" /tmp/ssh-tamper.err || { echo "UNEXPECTED TAMPER ERROR"; cat /tmp/ssh-tamper.err; exit 1; }
# A port outside the exact allow@host:tcp:22 rule stays unreachable. The
# decoy on 8080 serves real HTTP responses, so a success here would prove
# the network boundary failed rather than that nothing was listening.
if curl -sS --connect-timeout 5 http://%s:8080/ >/dev/null 2>&1; then
  echo "UNLISTED PORT REACHABLE"; exit 1
fi
echo PROBE-OK
`, dnsName, resolverIP, dnsName, serverIP, serverIP)
}

// startSSHServerVM launches the detached stand-in server VM, installs and
// starts sshd listening on port 22 with the test's public key as claude's
// only login path, starts an HTTP decoy on an unlisted port (8080) that
// answers every request with 200 (so the probe's curl assertion can only
// pass because the network blocks the connection, not because nothing
// responds), runs dnsmasq on 0.0.0.0:53 answering dnsName with the VM's own
// address, waits for all three to answer, and returns the VM name, address,
// and bare "<algo> <key>" host key. Host-side cleanup is registered for the
// whole test.
func startSSHServerVM(t *testing.T, dnsName, pubKeyPath string) (name, ip, hostKey string) {
	t.Helper()
	pub, err := os.ReadFile(pubKeyPath)
	if err != nil {
		t.Fatalf("read generated public key: %v", err)
	}
	script := fmt.Sprintf(`
set -eu
apt-get update -qq
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq openssh-server dnsmasq >/dev/null
useradd -m -s /bin/sh claude
install -d -m 700 -o claude -g claude /home/claude/.ssh
printf '%%s\n' %s > /home/claude/.ssh/authorized_keys
chown claude:claude /home/claude/.ssh/authorized_keys
chmod 600 /home/claude/.ssh/authorized_keys
printf 'PasswordAuthentication no\nKbdInteractiveAuthentication no\nPubkeyAuthentication yes\n' \
  > /etc/ssh/sshd_config.d/99-integ.conf
mkdir -p /run/sshd
/usr/sbin/sshd
nohup node -e "require('http').createServer((q,s)=>s.end('reachable')).listen(8080,'0.0.0.0')" >/tmp/decoy.log 2>&1 &
`, strconv.Quote(strings.TrimSpace(string(pub))))

	name = fmt.Sprintf("ai-sandbox-access-integ-server-%d", time.Now().Unix())
	msbRun(t, "run", "--detach", "--no-tty",
		"--name", name,
		"--label", "ai-sandbox.agent=codex",
		"--label", "ai-sandbox.workspace=integ",
		"node:22-bookworm", "--", "/bin/sh", "-c", script)
	t.Cleanup(func() {
		_ = exec.Command("msb", "stop", name).Run()
		_ = exec.Command("msb", "rm", name).Run()
	})

	// sshd comes up only after apt finishes installing it; poll until then.
	deadline := time.Now().Add(3 * time.Minute)
	for {
		out, err := exec.Command("msb", "exec", name, "--", "/bin/sh", "-c",
			"pgrep -x sshd >/dev/null && hostname -I").Output()
		if err == nil {
			fields := strings.Fields(string(out))
			if len(fields) == 0 {
				t.Fatalf("stand-in VM reported no address: %q", string(out))
			}
			ip = fields[0]
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("sshd did not start in stand-in VM within 3m: %v\n%s", err, out)
		}
		time.Sleep(2 * time.Second)
	}

	// dnsmasq needs the VM address for its host-record, so it is configured
	// only now that the address is known. A dedicated config file keeps the
	// resolver self-contained: answer dnsName with ip, nothing else. dnsmasq
	// daemonizes itself, so the exec returns once it is up.
	dnsCfg := fmt.Sprintf("listen-address=0.0.0.0\nbind-interfaces\nno-resolv\nno-hosts\nhost-record=%s,%s\n", dnsName, ip)
	startDNS := exec.Command("msb", "exec", name, "--", "/bin/sh", "-c",
		"cat > /etc/dnsmasq.integ.conf && dnsmasq --conf-file=/etc/dnsmasq.integ.conf --pid-file=/run/dnsmasq-integ.pid")
	startDNS.Stdin = strings.NewReader(dnsCfg)
	if out, err := startDNS.CombinedOutput(); err != nil {
		t.Fatalf("start dnsmasq in stand-in VM: %v\n%s", err, out)
	}
	for {
		var out []byte
		out, err = exec.Command("msb", "exec", name, "--", "/bin/sh", "-c",
			"pgrep -x dnsmasq >/dev/null").Output()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dnsmasq did not start in stand-in VM within 3m: %v\n%s", err, out)
		}
		time.Sleep(time.Second)
	}

	keyOut, err := exec.Command("msb", "exec", name, "--", "/bin/sh", "-c",
		"cat /etc/ssh/ssh_host_ed25519_key.pub").Output()
	if err != nil {
		t.Fatalf("read stand-in host key: %v\n%s", err, keyOut)
	}
	// The .pub file carries a trailing comment ("ssh-ed25519 AAAA... root@host");
	// three fields would parse as a known_hosts selector line and fail
	// validation. Pin the bare "<algo> <key>" form.
	fields := strings.Fields(strings.TrimSpace(string(keyOut)))
	if len(fields) < 2 {
		t.Fatalf("unexpected stand-in host key output: %q", string(keyOut))
	}
	hostKey = fields[0] + " " + fields[1]

	// Fail early if the guest would never trust this server: keyscan from
	// the host proves the key we pinned matches what the server presents.
	scan, err := exec.Command("ssh-keyscan", "-T", "10", "-t", "ed25519", ip).Output()
	if err != nil || !strings.Contains(string(scan), fields[1]) {
		t.Fatalf("ssh-keyscan of %s did not confirm the pinned host key (err %v):\n%s", ip, err, scan)
	}
	return name, ip, hostKey
}

// resolveForTest runs the production resolvePlan path (including real docker
// and msb verification) and returns the plan.
func resolveForTest(t *testing.T, opts runOptions, e execEnv, client msbClient) *plan.RuntimePlan {
	t.Helper()
	p, code := resolvePlan(context.Background(), opts, e, os.Stderr, client, false, true)
	if code != 0 || p == nil {
		t.Fatalf("resolvePlan exit code = %d", code)
	}
	return p
}

// launchAccessSandbox turns the plan's msb argv into a detached named run
// whose guest command is the given probe script, preserving the environment
// prefix, and starts it.
func launchAccessSandbox(t *testing.T, name string, p *plan.RuntimePlan, probe string) {
	t.Helper()
	argv := p.MsbArgv()
	cut := -1
	for i, a := range argv {
		if a == "--" {
			cut = i
			break
		}
	}
	if cut < 0 {
		t.Fatalf("no -- separator in argv: %v", argv)
	}
	full := []string{"run", "--name", name, "--detach"}
	for _, a := range argv[1:cut] {
		if a == "--tty" {
			continue
		}
		full = append(full, a)
	}
	full = append(full, "--", "env")
	full = append(full, p.Environment...)
	// Report through the rw workspace mount: the only channel visible to the
	// host from a detached sandbox's main process. The explicit newline
	// terminates the brace group (a bare ";" after the script's own trailing
	// newline is a POSIX-sh syntax error).
	full = append(full, "/bin/sh", "-c",
		fmt.Sprintf("{ %s\n} > %s/probe.log 2>&1", strings.TrimSpace(probe), p.WorkspaceGuest))
	msbRun(t, full...)
}

// waitForProbeLog polls the workspace's probe.log on the host until it
// contains want or the deadline passes.
func waitForProbeLog(t *testing.T, hostWorkspace, want string, d time.Duration) {
	t.Helper()
	log := filepath.Join(hostWorkspace, "probe.log")
	deadline := time.Now().Add(d)
	for {
		out, err := os.ReadFile(log)
		if err == nil && strings.Contains(string(out), want) {
			t.Logf("guest probe output:\n%s", out)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("guest probe did not report %q within %s:\n%s", want, d, out)
		}
		time.Sleep(time.Second)
	}
}

// workspaceHostPath finds the host-side path of the plan's workspace mount.
func workspaceHostPath(t *testing.T, p *plan.RuntimePlan) string {
	t.Helper()
	argv := p.MsbArgv()
	for i, a := range argv {
		if a == "--mount-dir" && i+1 < len(argv) && strings.Contains(argv[i+1], ":"+p.WorkspaceGuest+":") {
			host, _, _ := strings.Cut(argv[i+1], ":")
			return host
		}
	}
	t.Fatalf("workspace mount not found in argv for guest path %s", p.WorkspaceGuest)
	return ""
}

func msbRun(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("msb", args...)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("msb %s: %v", strings.Join(args, " "), err)
	}
}

func tempDirUnderHome(t *testing.T) string {
	t.Helper()
	base := filepath.Join(os.Getenv("HOME"), ".cache", "ai-sandbox-access-integ")
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(base, "run-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func genKey(t *testing.T, path string) {
	t.Helper()
	if err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "access-integ", "-f", path).Run(); err != nil {
		t.Fatalf("ssh-keygen: %v", err)
	}
}

// writeProfile writes a flat-schema access profile pinning one destination.
// The bare "<algo> <key>" host key exercises the same normalization
// production profiles get after pasting ssh-keyscan output.
func writeProfile(t *testing.T, path, host string, port int, user, hostKey string) {
	t.Helper()
	doc := fmt.Sprintf(`{
	  "schema_version": 1,
	  "host": %q,
	  "port": %d,
	  "user": %q,
	  "host_keys": [%q]
	}`, host, port, user, hostKey)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
}
