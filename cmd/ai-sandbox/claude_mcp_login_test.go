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
