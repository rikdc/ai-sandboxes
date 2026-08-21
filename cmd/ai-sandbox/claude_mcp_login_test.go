package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rikdc/ai-sandboxes/internal/runtime/microsandbox"
)

// writeClaudeListStub writes a stub `msb` that returns `listPayload` for
// `msb list ...` invocations. All other subcommands exit 0 with no output.
func writeClaudeListStub(t *testing.T, listPayload string) string {
	t.Helper()
	stub := t.TempDir() + "/msb"
	script := `#!/bin/sh
if [ "$1" = "list" ]; then
  cat <<'JSON'
` + listPayload + `
JSON
fi
exit 0
`
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return stub
}

func TestClaudeMCPLoginRejectsEmptyName(t *testing.T) {
	var out, err bytes.Buffer
	code := claudeMCPLoginCommand(nil, &out, &err)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(err.String(), "server name") {
		t.Errorf("stderr should mention required server name, got: %q", err.String())
	}
}

func TestClaudeMCPLoginRequiresCallbackPort(t *testing.T) {
	var out, err bytes.Buffer
	code := claudeMCPLoginCommand([]string{"sentry"}, &out, &err)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(err.String(), "--callback-port") {
		t.Errorf("stderr should mention --callback-port requirement, got: %q", err.String())
	}
}

func TestClaudeMCPLoginRejectsInvalidCallbackPort(t *testing.T) {
	var out, err bytes.Buffer
	code := claudeMCPLoginCommand([]string{"--callback-port", "99999", "sentry"}, &out, &err)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(err.String(), "--callback-port") {
		t.Errorf("stderr should reject out-of-range port, got: %q", err.String())
	}
}

// TestClaudeMCPLoginCallbackPortBoundaries verifies the callback port is
// restricted to the unprivileged range 1024..65535: the host SSH process and
// the guest Claude process both run unprivileged, so ports 1..1023 can never
// be bound and must be rejected before a tunnel is attempted.
func TestClaudeMCPLoginCallbackPortBoundaries(t *testing.T) {
	cases := []struct {
		port    string
		wantErr bool
	}{
		{"1023", true},
		{"1024", false},
		{"65535", false},
		{"65536", true},
		{"0", true},
		{"-1", true},
	}
	for _, c := range cases {
		t.Run(c.port, func(t *testing.T) {
			var out, err bytes.Buffer
			args := []string{"--callback-port", c.port, "sentry"}
			code := claudeMCPLoginCommand(args, &out, &err)
			if c.wantErr {
				if code != 2 {
					t.Errorf("port %s: exit code = %d, want 2", c.port, code)
				}
				if !strings.Contains(err.String(), "--callback-port") {
					t.Errorf("port %s: stderr should reject out-of-range port, got: %q", c.port, err.String())
				}
				return
			}
			// In-range ports pass validation and proceed to msb ssh auth
			// checks, which fail fast in this test environment (no real
			// msb/home). What matters here is that the port itself was
			// never rejected as out-of-range.
			if strings.Contains(err.String(), "--callback-port") {
				t.Errorf("port %s: should be accepted as in-range, got: %q", c.port, err.String())
			}
		})
	}
}

// TestClaudeMCPLoginCallbackPortFlagOrdering verifies --callback-port is
// accepted whether it comes before or after the positional server name.
func TestClaudeMCPLoginCallbackPortFlagOrdering(t *testing.T) {
	t.Run("flag before server name", func(t *testing.T) {
		var out, err bytes.Buffer
		code := claudeMCPLoginCommand([]string{"--callback-port", "49200", "sentry"}, &out, &err)
		if code == 2 && strings.Contains(err.String(), "--callback-port") {
			t.Errorf("flag-before-name should not be rejected as missing/invalid port, got: %q", err.String())
		}
	})
	t.Run("flag after server name", func(t *testing.T) {
		var out, err bytes.Buffer
		code := claudeMCPLoginCommand([]string{"sentry", "--callback-port", "49200"}, &out, &err)
		if code == 2 && strings.Contains(err.String(), "--callback-port") {
			t.Errorf("flag-after-name should not be rejected as missing/invalid port, got: %q", err.String())
		}
	})
}

func TestClaudeMCPLoginRejectsFlagLikeName(t *testing.T) {
	var out, err bytes.Buffer
	code := claudeMCPLoginCommand([]string{"--", "-evil"}, &out, &err)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(err.String(), "server name") {
		t.Errorf("stderr should reject flag-like server names, got: %q", err.String())
	}
}

func TestClaudeMCPLoginMsbSSHNotAuthorized(t *testing.T) {
	home := t.TempDir()
	e := execEnv{cwd: t.TempDir(), home: home, getenv: os.Getenv}
	client := &microsandbox.Client{Msb: "/nonexistent-msb-should-not-be-called"}

	var out, err bytes.Buffer
	code := executeClaudeMCPLogin(context.Background(), 5*time.Second, 49152, "sentry", e, &out, &err, client)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(err.String(), "msb ssh authorize") {
		t.Errorf("stderr should mention the remediation, got: %q", err.String())
	}
	if !strings.Contains(err.String(), "claude mcp login sentry") {
		t.Errorf("stderr should carry the operation prefix, got: %q", err.String())
	}
}

func TestClaudeMCPLoginNoSandbox(t *testing.T) {
	home := makeAuthorizedHome(t)
	cwd := t.TempDir()
	makeCheckout(t, cwd)
	stub := writeClaudeListStub(t, `[]`)
	client := &microsandbox.Client{Msb: stub}
	e := execEnv{cwd: cwd, home: home, getenv: os.Getenv}

	var out, err bytes.Buffer
	code := executeClaudeMCPLogin(context.Background(), 5*time.Second, 49152, "sentry", e, &out, &err, client)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(err.String(), "no running claude sandbox") {
		t.Errorf("stderr should explain no sandbox, got: %q", err.String())
	}
	if !strings.Contains(err.String(), "ai-sandbox run claude") {
		t.Errorf("stderr should suggest `ai-sandbox run claude`, got: %q", err.String())
	}
}

func TestClaudeMCPLoginMultipleSandboxes(t *testing.T) {
	home := makeAuthorizedHome(t)
	cwd := t.TempDir()
	makeCheckout(t, cwd)
	payload := `[
	  {"name":"claude-1","image":"ai-sandboxes-claude:local","status":"Running"},
	  {"name":"claude-2","image":"ai-sandboxes-claude:local","status":"Running"}
	]`
	stub := writeClaudeListStub(t, payload)
	client := &microsandbox.Client{Msb: stub}
	e := execEnv{cwd: cwd, home: home, getenv: os.Getenv}

	var out, err bytes.Buffer
	code := executeClaudeMCPLogin(context.Background(), 5*time.Second, 49152, "sentry", e, &out, &err, client)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(err.String(), "multiple running claude sandboxes") {
		t.Errorf("stderr should explain ambiguity, got: %q", err.String())
	}
}
