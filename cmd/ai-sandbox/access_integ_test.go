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
// configuration, and stays inside the deny-by-default network boundary.
//
// When AI_SANDBOX_ACCESS_INTEG_SSH=1 is also set, a second VM running sshd
// stands in for the remote server and the test additionally proves the full
// SSH leg through the pinned credential path: the configured key
// authenticates, a different key is rejected, a modified pinned host key is
// rejected, the non-default port destination works end to end, and ports
// outside the profile's exact allow@host:tcp:port rules stay unreachable. The
// profile's destination Hosts are pointed at the stand-in VM's real address
// with its real ssh-ed25519 host key, so the leg exercises the same
// materialization path production runs use.
//
// Run with:
//
//	AI_SANDBOX_MSB_INTEG=1 go test -tags integration -run TestAccessProfileEndToEnd ./cmd/ai-sandbox
//	AI_SANDBOX_MSB_INTEG=1 AI_SANDBOX_ACCESS_INTEG_SSH=1 \
//	  go test -tags integration -run TestAccessProfileEndToEnd ./cmd/ai-sandbox
//
// Skipped unless AI_SANDBOX_MSB_INTEG=1 and msb/docker are usable, matching
// tunnel_integ_test.go.
func TestAccessProfileEndToEnd(t *testing.T) {
	if os.Getenv("AI_SANDBOX_MSB_INTEG") != "1" {
		t.Skip("set AI_SANDBOX_MSB_INTEG=1 to run the msb integration tests")
	}
	for _, tool := range []string{"msb", "docker", "ssh-keygen"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
	sshLeg := os.Getenv("AI_SANDBOX_ACCESS_INTEG_SSH") == "1"
	if sshLeg {
		if _, err := exec.LookPath("ssh-keyscan"); err != nil {
			t.Skip("ssh-keyscan not on PATH")
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

	// The SSH leg needs a live destination: the stand-in VM must exist (and
	// have contributed its address and host key to the profile) before
	// executeRun materializes config and known_hosts from the profile.
	var (
		serverName string
		serverIP   string
		hostKeys   []string
	)
	if sshLeg {
		serverName, serverIP, hostKeys = startSSHServerVM(t, filepath.Join(keyDir, "id_ed25519.pub"))
	}

	profileHost := "nas.test"
	if sshLeg {
		profileHost = serverIP
	}
	dests := []integDest{{alias: "nas", host: profileHost, port: 22}}
	if sshLeg {
		dests = append(dests, integDest{alias: "nasalt", host: serverIP, port: 2222})
	}
	writeProfile(t, filepath.Join(configDir, "access", "homelab.json"), dests, hostKeys)

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
	sandbox := fmt.Sprintf("ai-sandbox-access-integ-%d", time.Now().Unix())
	probe := accessProbeScript(profileHost)
	if sshLeg {
		probe += "\n" + sshLegProbeScript(serverIP)
	}
	launchAccessSandbox(t, sandbox, p, probe)

	// The probe runs as the sandbox's main process (the only place the
	// injected environment exists; `msb exec` sessions do not inherit it)
	// and reports through the shared workspace.
	waitForProbeLog(t, workspaceHostPath(t, p), "PROBE-OK", time.Minute)
	if serverName != "" {
		t.Logf("ssh stand-in server %s at %s stayed up for the whole probe", serverName, serverIP)
	}
}

// accessProbeScript runs inside the guest and asserts every property the
// --access design promises at runtime. hostname is the destination Host the
// profile pins (a test name or the stand-in server's address).
func accessProbeScript(hostname string) string {
	return fmt.Sprintf(`
set -eu
echo "user=$(id -un)"
for f in config known_hosts id_ed25519 id_ed25519.pub; do
  test -f "/run/ai-sandbox/ssh/$f" || { echo "MISSING $f"; exit 1; }
done
if touch /run/ai-sandbox/ssh/probe 2>/dev/null; then
  echo "MOUNT IS WRITABLE"; exit 1
fi
test "$AI_SANDBOX_SSH_CONFIG" = "/run/ai-sandbox/ssh/config" || { echo "BAD ENV: $AI_SANDBOX_SSH_CONFIG"; exit 1; }
# The generated config must also be reachable as a system-wide include, so
# bare "ssh nas" (no -F) resolves the alias with the hardened options.
ssh -G nas > /tmp/ssh-G.txt 2>&1 || { cat /tmp/ssh-G.txt; exit 1; }
for want in "^user claude$" "^hostname %s$" "^port 22$" "^identityfile .*/run/ai-sandbox/ssh/id_ed25519$" "^stricthostkeychecking (yes|true)$" "^forwardagent no$" "^clearallforwardings yes$" "^identitiesonly yes$" "^passwordauthentication no$"; do
  grep -Eiq "$want" /tmp/ssh-G.txt || { echo "CONFIG MISSING $want"; cat /tmp/ssh-G.txt; exit 1; }
done
if curl -sS --connect-timeout 5 https://example.com/ >/dev/null 2>&1; then
  echo "PUBLIC EGRESS LEAK"; exit 1
fi
echo PROBE-OK
`, hostname)
}

// sshLegProbeScript extends the guest probe with the live SSH assertions:
// the pinned key authenticates on both allowed destinations (default and
// non-default port), a throwaway key is rejected, a modified pinned host key
// is rejected, and a port neither allow@host:tcp:port rule covers is
// unreachable on the destination host. The blocked-port check uses curl
// because /dev/tcp is a bashism and the probe runs under /bin/sh.
func sshLegProbeScript(serverIP string) string {
	return fmt.Sprintf(`
if ! ssh -o BatchMode=yes nas true 2>/tmp/ssh-ok.err; then
  echo "SSH KEY AUTH FAILED"; cat /tmp/ssh-ok.err; exit 1
fi
if ! ssh -o BatchMode=yes nasalt true 2>/tmp/ssh-alt.err; then
  echo "SSH NON-DEFAULT PORT FAILED"; cat /tmp/ssh-alt.err; exit 1
fi
ssh -G nasalt > /tmp/ssh-G-alt.txt 2>&1 || { cat /tmp/ssh-G-alt.txt; exit 1; }
grep -Eiq "^port 2222$" /tmp/ssh-G-alt.txt || { echo "ALT CONFIG MISSING PORT 2222"; cat /tmp/ssh-G-alt.txt; exit 1; }
ssh-keygen -q -t ed25519 -N '' -f /tmp/wrongkey || { echo "NO GUEST SSH-KEYGEN"; exit 1; }
if ssh -o BatchMode=yes -i /tmp/wrongkey -o IdentitiesOnly=yes nas true 2>/tmp/ssh-bad.err; then
  echo "WRONG KEY ACCEPTED"; exit 1
fi
grep -q "Permission denied" /tmp/ssh-bad.err || { echo "UNEXPECTED WRONG-KEY ERROR"; cat /tmp/ssh-bad.err; exit 1; }
cp /run/ai-sandbox/ssh/known_hosts /tmp/tampered_known_hosts
# Corrupt one character inside the pinned key body, keeping the selector and
# algorithm intact so ssh finds the entry but must reject the mismatched key.
sed -i 's/AAAAC3NzaC1lZDI1NTE5AAAAI/AAAAC3NzaC1lZDI1NTE5AAAAB/' /tmp/tampered_known_hosts
if ssh -o BatchMode=yes -o UserKnownHostsFile=/tmp/tampered_known_hosts \
    -o StrictHostKeyChecking=yes -p 22 claude@%s true 2>/tmp/ssh-tamper.err; then
  echo "TAMPERED HOST KEY ACCEPTED"; exit 1
fi
grep -qi "host key verification failed" /tmp/ssh-tamper.err || { echo "UNEXPECTED TAMPER ERROR"; cat /tmp/ssh-tamper.err; exit 1; }
if curl -sS --connect-timeout 5 http://%s:8080/ >/dev/null 2>&1; then
  echo "UNLISTED PORT REACHABLE"; exit 1
fi
echo SSH-LEG-OK
`, serverIP, serverIP)
}

// startSSHServerVM launches the detached stand-in server VM, installs and
// starts sshd listening on ports 22 and 2222 with the test's public key as
// claude's only login path, starts a decoy listener on an unlisted port
// (8080), waits for sshd to answer, and returns its name, address, and
// public host key line. Host-side cleanup is registered for the whole test.
func startSSHServerVM(t *testing.T, pubKeyPath string) (name, ip string, hostKeys []string) {
	t.Helper()
	pub, err := os.ReadFile(pubKeyPath)
	if err != nil {
		t.Fatalf("read generated public key: %v", err)
	}
	script := fmt.Sprintf(`
set -eu
apt-get update -qq
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq openssh-server >/dev/null
useradd -m -s /bin/sh claude
install -d -m 700 -o claude -g claude /home/claude/.ssh
printf '%%s\n' %s > /home/claude/.ssh/authorized_keys
chown claude:claude /home/claude/.ssh/authorized_keys
chmod 600 /home/claude/.ssh/authorized_keys
printf 'Port 22\nPort 2222\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nPubkeyAuthentication yes\n' \
  > /etc/ssh/sshd_config.d/99-integ.conf
mkdir -p /run/sshd
/usr/sbin/sshd
nohup node -e "require('net').createServer(s=>s.end()).listen(8080,'0.0.0.0')" >/tmp/decoy.log 2>&1 &
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

	keyOut, err := exec.Command("msb", "exec", name, "--", "/bin/sh", "-c",
		"cat /etc/ssh/ssh_host_ed25519_key.pub").Output()
	if err != nil {
		t.Fatalf("read stand-in host key: %v\n%s", err, keyOut)
	}
	line := strings.TrimSpace(string(keyOut))
	if len(strings.Fields(line)) < 2 {
		t.Fatalf("unexpected stand-in host key output: %q", line)
	}
	hostKeys = []string{line}

	// Fail early if the guest would never trust this server: keyscan from
	// the host proves the key we pinned matches what the server presents on
	// both allowed ports.
	for _, port := range []int{22, 2222} {
		scan, err := exec.Command("ssh-keyscan", "-p", strconv.Itoa(port),
			"-T", "10", "-t", "ed25519", ip).Output()
		if err != nil || !strings.Contains(string(scan), strings.Fields(line)[1]) {
			t.Fatalf("ssh-keyscan -p %d of %s did not confirm the pinned host key (err %v):\n%s",
				port, ip, err, scan)
		}
	}
	return name, ip, hostKeys
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

// integDest is one profile destination entry for the integration test.
type integDest struct {
	alias string
	host  string
	port  int
}

func writeProfile(t *testing.T, path string, dests []integDest, hostKeys []string) {
	t.Helper()
	var b strings.Builder
	b.WriteString(`{
	  "schema_version": 1,
	  "destinations": [
`)
	for i, d := range dests {
		comma := ","
		if i == len(dests)-1 {
			comma = ""
		}
		key := fmt.Sprintf("%s ssh-ed25519 AAAAplaceholder", d.host)
		if i < len(hostKeys) {
			// Live server: pin its real "<algo> <key>" line; the renderer
			// derives the per-port selector.
			key = hostKeys[i]
		}
		fmt.Fprintf(&b,
			"	    {\"alias\": %q, \"host\": %q, \"port\": %d, \"user\": \"claude\",\n"+
				"	     \"host_keys\": [%q]}%s\n",
			d.alias, d.host, d.port, key, comma)
	}
	b.WriteString(`	  ]
	}`)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}
