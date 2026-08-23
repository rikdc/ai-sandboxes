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

func TestOpencodeLoginMsbSSHNotAuthorized(t *testing.T) {
	home := t.TempDir() // no authorized_keys
	e := execEnv{cwd: t.TempDir(), home: home, getenv: os.Getenv}
	client := &microsandbox.Client{Msb: "/nonexistent-msb-should-not-be-called"}

	var out, err bytes.Buffer
	code := executeOpencodeLogin(context.Background(), 5*time.Second, e, &out, &err, client)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !bytes.Contains(err.Bytes(), []byte("msb ssh authorize")) {
		t.Errorf("stderr should mention the remediation, got: %q", err.String())
	}
}

func TestOpencodeLoginNoSandbox(t *testing.T) {
	home := makeAuthorizedHome(t)
	// Give FindWorkspace a real checkout to hand back.
	cwd := t.TempDir()
	makeCheckout(t, cwd)
	stub := writeCodexListStub(t, `[]`)
	client := &microsandbox.Client{Msb: stub}
	e := execEnv{cwd: cwd, home: home, getenv: os.Getenv}

	var out, err bytes.Buffer
	code := executeOpencodeLogin(context.Background(), 5*time.Second, e, &out, &err, client)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !bytes.Contains(err.Bytes(), []byte("no running opencode sandbox")) {
		t.Errorf("stderr should explain no sandbox, got: %q", err.String())
	}
	if !bytes.Contains(err.Bytes(), []byte("ai-sandbox run opencode")) {
		t.Errorf("stderr should suggest `ai-sandbox run opencode`, got: %q", err.String())
	}
}

func TestOpencodeLoginMultipleSandboxes(t *testing.T) {
	home := makeAuthorizedHome(t)
	cwd := t.TempDir()
	makeCheckout(t, cwd)
	payload := `[
	  {"name":"opencode-1","image":"ai-sandboxes-opencode:local","status":"Running"},
	  {"name":"opencode-2","image":"ai-sandboxes-opencode:local","status":"Running"}
	]`
	stub := writeCodexListStub(t, payload)
	client := &microsandbox.Client{Msb: stub}
	e := execEnv{cwd: cwd, home: home, getenv: os.Getenv}

	var out, err bytes.Buffer
	code := executeOpencodeLogin(context.Background(), 5*time.Second, e, &out, &err, client)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !bytes.Contains(err.Bytes(), []byte("multiple running opencode sandboxes")) {
		t.Errorf("stderr should explain ambiguity, got: %q", err.String())
	}
}

func TestOpencodeLoginOrchestrationSuccessPath(t *testing.T) {
	home := makeAuthorizedHome(t)
	cwd := t.TempDir()
	makeCheckout(t, cwd)

	var tunneledHostPort, tunneledGuestPort int
	openTunnelFn = func(_ *microsandbox.Client, _ string, hostPort, guestPort int) (*microsandbox.Tunnel, int, error) {
		tunneledHostPort = hostPort
		tunneledGuestPort = guestPort
		return &microsandbox.Tunnel{}, opencodeCallbackPort, nil
	}
	t.Cleanup(func() { openTunnelFn = defaultOpenTunnel })

	stub, record := writeSuccessMsbStub(t, "opencode-1")
	client := &microsandbox.Client{Msb: stub}
	e := execEnv{cwd: cwd, home: home, getenv: os.Getenv}

	var out, err bytes.Buffer
	code := executeOpencodeLogin(context.Background(), 5*time.Second, e, &out, &err, client)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, err.String())
	}
	if tunneledHostPort != opencodeCallbackPort || tunneledGuestPort != opencodeCallbackPort {
		t.Errorf("tunnel ports = host %d guest %d, want both %d",
			tunneledHostPort, tunneledGuestPort, opencodeCallbackPort)
	}
	recorded, readErr := os.ReadFile(record)
	if readErr != nil {
		t.Fatal(readErr)
	}
	wantSuffix := "exec opencode-1 -- opencode auth login"
	if !strings.HasSuffix(strings.TrimSpace(string(recorded)), wantSuffix) {
		t.Errorf("recorded argv = %q, want suffix %q", string(recorded), wantSuffix)
	}
}
