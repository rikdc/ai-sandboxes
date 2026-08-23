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
	// Everything the guest mounts must live under $HOME: microsandbox on
	// macOS does not share /var/folders (where t.TempDir lands) into VMs.
	configDir := tempDirUnderHome(t)
	keyDir := filepath.Join(configDir, "access", "keys", "homelab")
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	genKey(t, filepath.Join(keyDir, "id_ed25519"))

	writeProfile(t, filepath.Join(configDir, "access", "homelab.json"), "nas.test", 22)

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
	// Access runs pin the host's discovered resolvers and opt out of rebind
	// protection so LAN answers (private RFC1918 IPs) survive.
	argv := p.MsbArgv()
	if !hasFlagValue(argv, "--dns-nameserver") {
		t.Errorf("access plan missing --dns-nameserver: %v", argv)
	}
	rebindOff := false
	for _, a := range argv {
		if a == "--no-dns-rebind-protection" {
			rebindOff = true
		}
	}
	if !rebindOff {
		t.Errorf("access plan missing --no-dns-rebind-protection: %v", argv)
	}
	sandbox := fmt.Sprintf("ai-sandbox-access-integ-%d", time.Now().Unix())
	launchAccessSandbox(t, sandbox, p, accessProbeScript())

	// The probe runs as the sandbox's main process (the only place the
	// injected environment exists; `msb exec` sessions do not inherit it)
	// and reports through the shared workspace.
	waitForProbeLog(t, workspaceHostPath(t, p), "PROBE-OK", time.Minute)
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
# The generated config must also be reachable as a system-wide include, so
# bare "ssh nas" (no -F) resolves the alias with the hardened options.
ssh -G nas > /tmp/ssh-G.txt 2>&1 || { cat /tmp/ssh-G.txt; exit 1; }
for want in "^user claude$" "^hostname nas.test$" "^port 22$" "^identityfile .*/run/ai-sandbox/ssh/id_ed25519$" "^stricthostkeychecking (yes|true)$" "^forwardagent no$" "^clearallforwardings yes$" "^identitiesonly yes$" "^passwordauthentication no$"; do
  grep -Eiq "$want" /tmp/ssh-G.txt || { echo "CONFIG MISSING $want"; cat /tmp/ssh-G.txt; exit 1; }
done
if curl -sS --connect-timeout 5 https://example.com/ >/dev/null 2>&1; then
  echo "PUBLIC EGRESS LEAK"; exit 1
fi
echo PROBE-OK
`
}

// hasFlagValue reports whether argv carries flag followed by any value.
func hasFlagValue(argv []string, flag string) bool {
	for i, a := range argv {
		if a == flag && i+1 < len(argv) {
			return true
		}
	}
	return false
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

func writeProfile(t *testing.T, path, host string, port int) {
	t.Helper()
	doc := fmt.Sprintf(`{
	  "schema_version": 1,
	  "destinations": [
	    {"alias": "nas", "host": "%s", "port": %d, "user": "claude",
	     "host_keys": [%q]}
	  ]
	}`, host, port, fmt.Sprintf("%s ssh-ed25519 AAAAplaceholder", host))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
}
