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

func TestCodexMCPLoginRejectsEmptyName(t *testing.T) {
	var out, err bytes.Buffer
	code := codexMCPLoginCommand(nil, &out, &err)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(err.String(), "server name") {
		t.Errorf("stderr should mention required server name, got: %q", err.String())
	}
}

func TestCodexMCPLoginRejectsFlagLikeName(t *testing.T) {
	// After `--` the flag package stops parsing, so a leading-dash argv
	// element reaches our validator. This is the case worth guarding
	// against; bare `--evil` is already rejected by flag.Parse.
	var out, err bytes.Buffer
	code := codexMCPLoginCommand([]string{"--", "-evil"}, &out, &err)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(err.String(), "server name") {
		t.Errorf("stderr should reject flag-like server names, got: %q", err.String())
	}
}

func TestCodexMCPLoginMsbSSHNotAuthorized(t *testing.T) {
	home := t.TempDir() // no authorized_keys
	e := execEnv{cwd: t.TempDir(), home: home, getenv: os.Getenv}
	client := &microsandbox.Client{Msb: "/nonexistent-msb-should-not-be-called"}

	var out, err bytes.Buffer
	code := executeCodexMCPLogin(context.Background(), 5*time.Second, "notion", e, &out, &err, client)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(err.String(), "msb ssh authorize") {
		t.Errorf("stderr should mention the remediation, got: %q", err.String())
	}
	if !strings.Contains(err.String(), "codex mcp login notion") {
		t.Errorf("stderr should carry the operation prefix, got: %q", err.String())
	}
}

func TestCodexMCPLoginNoSandbox(t *testing.T) {
	home := makeAuthorizedHome(t)
	cwd := t.TempDir()
	makeCheckout(t, cwd)
	stub := writeCodexListStub(t, `[]`)
	client := &microsandbox.Client{Msb: stub}
	e := execEnv{cwd: cwd, home: home, getenv: os.Getenv}

	var out, err bytes.Buffer
	code := executeCodexMCPLogin(context.Background(), 5*time.Second, "notion", e, &out, &err, client)
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

func TestCodexMCPLoginMultipleSandboxes(t *testing.T) {
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
	code := executeCodexMCPLogin(context.Background(), 5*time.Second, "notion", e, &out, &err, client)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(err.String(), "multiple running codex sandboxes") {
		t.Errorf("stderr should explain ambiguity, got: %q", err.String())
	}
}
