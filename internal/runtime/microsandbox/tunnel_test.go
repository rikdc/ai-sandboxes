package microsandbox

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestEnsureMsbSSHAuthorized(t *testing.T) {
	home := t.TempDir()

	if err := EnsureMsbSSHAuthorized(home); err == nil {
		t.Fatal("missing authorized_keys should error")
	} else if !strings.Contains(err.Error(), "msb ssh authorize") {
		t.Errorf("error should mention the remediation command, got: %v", err)
	}

	dir := filepath.Join(home, ".microsandbox", "ssh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "authorized_keys"), []byte("ssh-ed25519 AAAA...\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureMsbSSHAuthorized(home); err != nil {
		t.Errorf("present authorized_keys should succeed, got: %v", err)
	}
}

func TestServeArgv(t *testing.T) {
	got := serveArgv("codex-abc", 14552)
	want := []string{"ssh", "serve", "--host", "127.0.0.1", "--port", "14552", "codex-abc"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("serveArgv = %v, want %v", got, want)
	}
}

func TestSshForwardArgv(t *testing.T) {
	got := sshForwardArgv(1455, 1455, 14552)
	want := []string{
		"-N",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ExitOnForwardFailure=yes",
		"-L", "127.0.0.1:1455:127.0.0.1:1455",
		"-p", "14552",
		"root@127.0.0.1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sshForwardArgv = %v, want %v", got, want)
	}
}

func TestPickLoopbackPort(t *testing.T) {
	port, err := PickLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	if port <= 0 || port > 65535 {
		t.Errorf("port = %d, want in 1..65535", port)
	}
}

func TestWaitForListenerSucceeds(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	if err := waitForListener("127.0.0.1", port, 500*time.Millisecond); err != nil {
		t.Errorf("waitForListener on active socket = %v", err)
	}
}

func TestWaitForListenerTimesOut(t *testing.T) {
	// Pick a port then immediately release it, giving us a socket unlikely
	// to be occupied for the short polling window.
	port, err := PickLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err = waitForListener("127.0.0.1", port, 300*time.Millisecond)
	if err == nil {
		t.Fatalf("expected timeout on unused port %d", port)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("%d", port)) {
		t.Errorf("error should mention the port, got: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond {
		t.Errorf("waitForListener returned too fast (%v); polling loop misconfigured", elapsed)
	}
}
