package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rikdc/ai-sandboxes/internal/runtime/microsandbox"
)

// writeSuccessMsbStub writes an `msb` stub that handles `list` (returning
// exactly one sandbox with the given name) and records every non-`list`
// invocation's argv to a file. Returns the stub path and the record path.
func writeSuccessMsbStub(t *testing.T, sandboxName string) (stub, record string) {
	t.Helper()
	dir := t.TempDir()
	stub = filepath.Join(dir, "msb")
	record = filepath.Join(dir, "record")
	script := `#!/bin/sh
if [ "$1" = "list" ]; then
  cat <<JSON
[{"name":"` + sandboxName + `","image":"ai-sandboxes-x:local","status":"Running"}]
JSON
  exit 0
fi
printf '%s\n' "$*" >> "` + record + `"
exit 0
`
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return stub, record
}

// swapTunnelOpener installs a fake openTunnelFn for the duration of a test
// and returns a pointer to the last-seen call args so the test can assert
// on them. If simulatePickedPort is non-zero, ephemeral (host=0) requests
// resolve to that port on both sides.
type tunnelCall struct {
	sandboxName        string
	hostPort           int
	guestPort          int
	simulatePickedPort int
}

func swapTunnelOpener(t *testing.T, simulatePickedPort int) *tunnelCall {
	t.Helper()
	call := &tunnelCall{simulatePickedPort: simulatePickedPort}
	prev := openTunnelFn
	openTunnelFn = func(_ *microsandbox.Client, name string, host, guest int) (*microsandbox.Tunnel, int, error) {
		call.sandboxName = name
		call.hostPort = host
		call.guestPort = guest
		port := guest
		if host == 0 {
			port = simulatePickedPort
		}
		return &microsandbox.Tunnel{}, port, nil
	}
	t.Cleanup(func() { openTunnelFn = prev })
	return call
}

func TestCodexMCPLoginOrchestrationSuccessPath(t *testing.T) {
	home := makeAuthorizedHome(t)
	cwd := t.TempDir()
	makeCheckout(t, cwd)
	stub, record := writeSuccessMsbStub(t, "codex-abc")
	call := swapTunnelOpener(t, 54321)
	client := &microsandbox.Client{Msb: stub}
	e := execEnv{cwd: cwd, home: home, getenv: os.Getenv}

	var out, errBuf bytes.Buffer
	code := executeCodexMCPLogin(context.Background(), 30*time.Second, "notion", e, &out, &errBuf, client)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr=%q", code, errBuf.String())
	}

	// Ephemeral request: host==0, guest==0. Orchestrator resolves via
	// the injected picker (54321) and threads that port through both the
	// tunnel and the guest argv.
	if call.sandboxName != "codex-abc" {
		t.Errorf("tunnel sandbox = %q, want codex-abc", call.sandboxName)
	}
	if call.hostPort != 0 || call.guestPort != 0 {
		t.Errorf("expected ephemeral (0/0) tunnel request, got %d/%d", call.hostPort, call.guestPort)
	}

	rec, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("no msb exec recorded: %v", err)
	}
	got := strings.TrimSpace(string(rec))
	want := "exec --workdir /home/node --user node codex-abc -- codex -c mcp_oauth_callback_port=54321 mcp login notion"
	if got != want {
		t.Errorf("msb argv mismatch:\n got:  %q\n want: %q", got, want)
	}
}

func TestClaudeMCPLoginOrchestrationSuccessPath(t *testing.T) {
	home := makeAuthorizedHome(t)
	cwd := t.TempDir()
	makeCheckout(t, cwd)
	stub, record := writeSuccessMsbStub(t, "claude-xyz")
	call := swapTunnelOpener(t, 0) // fixed port; picker not exercised
	client := &microsandbox.Client{Msb: stub}
	e := execEnv{cwd: cwd, home: home, getenv: os.Getenv}

	var out, errBuf bytes.Buffer
	code := executeClaudeMCPLogin(context.Background(), 30*time.Second, 49152, "sentry", e, &out, &errBuf, client)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr=%q", code, errBuf.String())
	}

	// Fixed-port request: host==guest==49152, no ephemeral pick.
	if call.sandboxName != "claude-xyz" {
		t.Errorf("tunnel sandbox = %q, want claude-xyz", call.sandboxName)
	}
	if call.hostPort != 49152 || call.guestPort != 49152 {
		t.Errorf("expected fixed 49152/49152 tunnel request, got %d/%d", call.hostPort, call.guestPort)
	}

	// Claude's guest argv deliberately does NOT include --callback-port —
	// Claude Code v2.1.231 rejects that flag on `claude mcp login`; the
	// port is pre-registered via `claude mcp add --callback-port`.
	rec, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("no msb exec recorded: %v", err)
	}
	got := strings.TrimSpace(string(rec))
	want := "exec --workdir /home/node --user node claude-xyz -- claude mcp login sentry"
	if got != want {
		t.Errorf("msb argv mismatch:\n got:  %q\n want: %q", got, want)
	}
}

func TestCallbackOperationTimeoutIsHonoured(t *testing.T) {
	home := makeAuthorizedHome(t)
	cwd := t.TempDir()
	makeCheckout(t, cwd)
	// Stub msb: list returns one sandbox; exec runs a sleep that outlives
	// the timeout so CommandContext.Kill fires.
	dir := t.TempDir()
	stub := filepath.Join(dir, "msb")
	script := `#!/bin/sh
if [ "$1" = "list" ]; then
  echo '[{"name":"codex-abc","image":"x","status":"Running"}]'
  exit 0
fi
exec sleep 30
`
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	swapTunnelOpener(t, 54321)
	client := &microsandbox.Client{Msb: stub}
	e := execEnv{cwd: cwd, home: home, getenv: os.Getenv}

	var out, errBuf bytes.Buffer
	start := time.Now()
	code := executeCodexMCPLogin(context.Background(), 200*time.Millisecond, "notion", e, &out, &errBuf, client)
	elapsed := time.Since(start)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if elapsed > 3*time.Second {
		t.Errorf("timeout not honoured: took %s", elapsed)
	}
	if !strings.Contains(errBuf.String(), "aborted after") {
		t.Errorf("stderr should mention timeout, got: %q", errBuf.String())
	}
}
