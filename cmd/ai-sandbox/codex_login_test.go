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

// makeAuthorizedHome writes an authorized_keys file at
// <home>/.microsandbox/ssh/authorized_keys so EnsureMsbSSHAuthorized passes.
func makeAuthorizedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".microsandbox", "ssh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "authorized_keys"), []byte("ssh-ed25519 AAAA...\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

// writeCodexListStub writes a stub `msb` that returns `listPayload` for
// `msb list ...` invocations. All other subcommands exit 0 with no output.
func writeCodexListStub(t *testing.T, listPayload string) string {
	t.Helper()
	stub := filepath.Join(t.TempDir(), "msb")
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

func TestCodexLoginMsbSSHNotAuthorized(t *testing.T) {
	home := t.TempDir() // no authorized_keys
	e := execEnv{cwd: t.TempDir(), home: home, getenv: os.Getenv}
	client := &microsandbox.Client{Msb: "/nonexistent-msb-should-not-be-called"}

	var out, err bytes.Buffer
	code := executeCodexLogin(context.Background(), 5*time.Second, e, &out, &err, client)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(err.String(), "msb ssh authorize") {
		t.Errorf("stderr should mention the remediation, got: %q", err.String())
	}
}

func TestCodexLoginNoSandbox(t *testing.T) {
	home := makeAuthorizedHome(t)
	// Give FindWorkspace a real checkout to hand back.
	cwd := t.TempDir()
	makeCheckout(t, cwd)
	stub := writeCodexListStub(t, `[]`)
	client := &microsandbox.Client{Msb: stub}
	e := execEnv{cwd: cwd, home: home, getenv: os.Getenv}

	var out, err bytes.Buffer
	code := executeCodexLogin(context.Background(), 5*time.Second, e, &out, &err, client)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(err.String(), "no running codex sandbox") {
		t.Errorf("stderr should explain no sandbox, got: %q", err.String())
	}
	if !strings.Contains(err.String(), "ai-sandbox run codex") {
		t.Errorf("stderr should suggest `ai-sandbox run codex`, got: %q", err.String())
	}
}

func TestCodexLoginMultipleSandboxes(t *testing.T) {
	home := makeAuthorizedHome(t)
	cwd := t.TempDir()
	makeCheckout(t, cwd)
	payload := `[
	  {"name":"codex-1","image":"ai-sandboxes-codex:local","status":"Running"},
	  {"name":"codex-2","image":"ai-sandboxes-codex:local","status":"Running"}
	]`
	stub := writeCodexListStub(t, payload)
	client := &microsandbox.Client{Msb: stub}
	e := execEnv{cwd: cwd, home: home, getenv: os.Getenv}

	var out, err bytes.Buffer
	code := executeCodexLogin(context.Background(), 5*time.Second, e, &out, &err, client)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(err.String(), "multiple running codex sandboxes") {
		t.Errorf("stderr should explain ambiguity, got: %q", err.String())
	}
}
