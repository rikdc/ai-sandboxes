//go:build integration

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rikdc/ai-sandboxes/internal/access"
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
// stands in for the remote server and the test proves the full SSH leg:
// key auth succeeds, a different key is rejected, and unlisted ports stay
// unreachable.
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

	client := &microsandbox.Client{Out: os.Stderr}
	present, err := client.ImagePresent("ai-sandboxes-claude:local")
	if err != nil {
		t.Fatalf("msb image check: %v", err)
	}
	if !present {
		t.Skip("ai-sandboxes-claude:local not loaded; run ./scripts/load-msb first")
	}

	// --- Host-side setup: profile, dedicated key directory, egress file ---
	configDir := t.TempDir()
	keyDir := filepath.Join(configDir, "access", "keys", "homelab")
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	genKey(t, filepath.Join(keyDir, "id_ed25519"))

	writeProfile(t, filepath.Join(configDir, "access", "homelab.json"), "nas.test", 22)

	home := t.TempDir()
	egressDir := filepath.Join(home, ".config", "microvms")
	if err := os.MkdirAll(egressDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Deliberately empty allowlist: nothing beyond gateway DNS is permitted,
	// except what --access adds.
	os.WriteFile(filepath.Join(egressDir, "claude-egress"), []byte("# nothing\n"), 0o600)
	project := t.TempDir()

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
	launchAccessSandbox(t, sandbox, p, accessProbeScript())

	waitGuestReady(t, sandbox)

	out := msbExec(t, sandbox, accessProbeScript())
	t.Logf("guest probe output:\n%s", out)
	if !strings.Contains(out, "PROBE-OK") {
		t.Fatalf("guest probe did not pass:\n%s", out)
	}

	if os.Getenv("AI_SANDBOX_ACCESS_INTEG_SSH") == "1" {
		testSSHRoundTrip(t, configDir, keyDir, e, client)
	}
}

// accessProbeScript runs inside the guest and asserts every property the
// --access design promises at runtime.
func accessProbeScript() string {
	return `
set -eu
echo "user=$(id -un)"
for f in config known_hosts id_ed25519 id_ed25519.pub; do
  test -f "/run/ai-sandbox/ssh/$f" || { echo "MISSING $f"; exit 1; }
done
if touch /run/ai-sandbox/ssh/probe 2>/dev/null; then
  echo "MOUNT IS WRITABLE"; exit 1
fi
test "$AI_SANDBOX_SSH_CONFIG" = "/run/ai-sandbox/ssh/config" || { echo "BAD ENV: $AI_SANDBOX_SSH_CONFIG"; exit 1; }
ssh -F "$AI_SANDBOX_SSH_CONFIG" -G nas > /tmp/ssh-G.txt 2>&1 || { cat /tmp/ssh-G.txt; exit 1; }
for want in "^user claude$" "^hostname nas.test$" "^port 22$" "^identityfile .*/run/ai-sandbox/ssh/id_ed25519$" "^stricthostkeychecking yes$" "^forwardagent no$" "^clearallforwardings yes$" "^identitiesonly yes$" "^passwordauthentication no$"; do
  grep -qi "$want" /tmp/ssh-G.txt || { echo "CONFIG MISSING $want"; cat /tmp/ssh-G.txt; exit 1; }
done
if curl -sS --connect-timeout 5 https://example.com/ >/dev/null 2>&1; then
  echo "PUBLIC EGRESS LEAK"; exit 1
fi
echo PROBE-OK
`
}

// testSSHRoundTrip boots a throwaway sshd VM as the "remote server", pins its
// host key in the profile, relaunches the client VM, and proves: correct key
// authenticates, a different key is rejected, unlisted ports are unreachable.
func testSSHRoundTrip(t *testing.T, configDir, keyDir string, e execEnv, client *microsandbox.Client) {
	ctx := context.Background()
	server := fmt.Sprintf("ai-sandbox-access-integ-sshd-%d", time.Now().Unix())
	image := "node:22-bookworm"
	if v := os.Getenv("AI_SANDBOX_MSB_INTEG_IMAGE"); v != "" {
		image = v
	}
	runDetached(t, server, image)
	t.Cleanup(func() {
		_ = exec.Command("msb", "stop", server).Run()
		_ = exec.Command("msb", "rm", server).Run()
	})

	install := exec.CommandContext(ctx, "msb", "exec", server, "--", "/bin/sh", "-c",
		"apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq openssh-server")
	install.Stdout, install.Stderr = os.Stderr, os.Stderr
	if err := install.Run(); err != nil {
		t.Skipf("could not install openssh-server in the stand-in server VM: %v", err)
	}

	pubBytes, err := os.ReadFile(filepath.Join(keyDir, "id_ed25519.pub"))
	if err != nil {
		t.Fatal(err)
	}
	authz := fmt.Sprintf("mkdir -p /run/sshd /root/.ssh && echo '%s' > /root/.ssh/authorized_keys && chmod 700 /root/.ssh && chmod 600 /root/.ssh/authorized_keys", strings.TrimSpace(string(pubBytes)))
	msbRun(t, "exec", server, "--", "/bin/sh", "-c", authz)
	msbRun(t, "exec", server, "--", "/bin/sh", "-c", "nohup /usr/sbin/sshd >/dev/null 2>&1 &")
	time.Sleep(500 * time.Millisecond)

	ipOut := msbExec(t, server, "hostname -I")
	ip := strings.Fields(strings.TrimSpace(ipOut))
	if len(ip) == 0 {
		t.Fatalf("no server address: %q", ipOut)
	}
	addr := ip[0]

	// Pin the server host key exactly as a user would.
	keyscan, err := exec.CommandContext(ctx, "ssh-keyscan", "-T", "5", addr).Output()
	if err != nil || len(strings.TrimSpace(string(keyscan))) == 0 {
		t.Skipf("ssh-keyscan produced no host keys: %v", err)
	}

	writeProfileWithKeys(t, filepath.Join(configDir, "access", "homelab.json"), addr,
		strings.Split(string(keyscan), "\n"))
	prof, err := access.Load(configDir, "homelab")
	if err != nil {
		t.Fatal(err)
	}
	if err := access.Materialize(keyDir, prof); err != nil {
		t.Fatal(err)
	}

	opts := runOptions{agent: "claude", access: "homelab"}
	p := resolveForTest(t, opts, e, client)
	sandbox := fmt.Sprintf("ai-sandbox-access-integ-ssh-%d", time.Now().Unix())
	launchAccessSandbox(t, sandbox, p, "sleep 300")
	waitGuestReady(t, sandbox)

	goodKey := filepath.Join(keyDir, "id_ed25519")
	if out := msbExec(t, sandbox, fmt.Sprintf(
		`ssh -F "$AI_SANDBOX_SSH_CONFIG" -o BatchMode=yes -o ConnectTimeout=8 -i '%s' nas true && echo AUTH-OK`, goodKey)); !strings.Contains(out, "AUTH-OK") {
		t.Errorf("SSH auth with the profile key failed:\n%s", out)
	}

	wrongKey := filepath.Join(t.TempDir(), "wrong_key")
	genKey(t, wrongKey)
	if out := msbExec(t, sandbox, fmt.Sprintf(
		`ssh -F "$AI_SANDBOX_SSH_CONFIG" -o BatchMode=yes -o ConnectTimeout=8 -o IdentitiesOnly=yes -i '%s' nas true 2>/dev/null && echo WRONG-KEY-SUCCEEDED`, wrongKey)); strings.Contains(out, "WRONG-KEY-SUCCEEDED") {
		t.Error("a non-profile key authenticated; identity pinning failed")
	}

	// Unlisted port on the approved host must stay unreachable.
	if out := msbExec(t, sandbox, fmt.Sprintf(
		`/bin/bash -c '(echo > /dev/tcp/%s/2222) 2>/dev/null && echo PORT-OPEN'`, addr)); strings.Contains(out, "PORT-OPEN") {
		t.Error("unlisted port 2222 was reachable; the network rule leaked")
	}
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
	full = append(full, "/bin/sh", "-c", probe)
	msbRun(t, full...)
}

func runDetached(t *testing.T, name, image string) {
	t.Helper()
	msbRun(t, "run", "--name", name, "--detach", "--pull", "never", image, "--", "/bin/sh", "-c", "sleep 600")
}

func msbRun(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("msb", args...)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("msb %s: %v", strings.Join(args, " "), err)
	}
}

func msbExec(t *testing.T, sandbox, script string) string {
	t.Helper()
	out, err := exec.Command("msb", "exec", sandbox, "--", "/bin/sh", "-c", script).CombinedOutput()
	if err != nil {
		t.Logf("msb exec output:\n%s", out)
		t.Fatalf("msb exec %s: %v", sandbox, err)
	}
	return string(out)
}

func waitGuestReady(t *testing.T, sandbox string) {
	t.Helper()
	for i := 0; i < 30; i++ {
		if err := exec.Command("msb", "exec", sandbox, "--", "/bin/true").Run(); err == nil {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("sandbox %s never became ready", sandbox)
}

func genKey(t *testing.T, path string) {
	t.Helper()
	if err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "access-integ", "-f", path).Run(); err != nil {
		t.Fatalf("ssh-keygen: %v", err)
	}
}

func writeProfile(t *testing.T, path, host string, port int) {
	t.Helper()
	writeProfileWithKeys(t, path, host, []string{fmt.Sprintf("%s ssh-ed25519 AAAAplaceholder", host)})
}

func writeProfileWithKeys(t *testing.T, path, host string, hostKeys []string) {
	t.Helper()
	keys := make([]string, 0, len(hostKeys))
	for _, k := range hostKeys {
		k = strings.TrimSpace(k)
		if k != "" && !strings.HasPrefix(k, "#") {
			keys = append(keys, fmt.Sprintf("%q", k))
		}
	}
	doc := fmt.Sprintf(`{
	  "schema_version": 1,
	  "destinations": [
	    {"alias": "nas", "host": "%s", "port": 22, "user": "claude",
	     "host_keys": [%s]}
	  ]
	}`, host, strings.Join(keys, ","))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
}
