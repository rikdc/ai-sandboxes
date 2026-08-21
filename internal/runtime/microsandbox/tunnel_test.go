package microsandbox

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
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

// runHelper re-execs the current test binary as a subprocess in one of a few
// fixed modes, giving tests a real OS process to monitor without depending on
// any external tool (nc, python, ...). See TestHelperProcess.
func runHelper(t *testing.T, mode string, extraEnv ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1", "HELPER_MODE="+mode)
	cmd.Env = append(cmd.Env, extraEnv...)
	return cmd
}

// TestHelperProcess is not a real test. It is re-executed by runHelper as a
// subprocess that either listens on a port, exits immediately, or hangs
// without listening, so the readiness/monitoring tests exercise a real
// process lifecycle instead of a simulated one.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	switch os.Getenv("HELPER_MODE") {
	case "listen":
		l, err := net.Listen("tcp", "127.0.0.1:"+os.Getenv("HELPER_PORT"))
		if err != nil {
			os.Exit(1)
		}
		defer l.Close()
		time.Sleep(10 * time.Second)
	case "exit":
		os.Exit(1)
	case "hang":
		time.Sleep(10 * time.Second)
	case "ignore-sigint":
		// Consume and discard SIGINT so it can never terminate this process,
		// standing in for a child that swallows the signal (a handler, or a
		// shell wrapper that doesn't forward it) instead of exiting on it.
		c := make(chan os.Signal, 1)
		signal.Notify(c, syscall.SIGINT)
		time.Sleep(10 * time.Second)
	}
	os.Exit(0)
}

func TestPreflightPortOccupied(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port

	err = preflightPort("127.0.0.1", port)
	if !errors.Is(err, ErrCallbackPortOccupied) {
		t.Errorf("preflightPort(occupied) = %v, want ErrCallbackPortOccupied", err)
	}
}

func TestPreflightPortFree(t *testing.T) {
	port, err := PickLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	if err := preflightPort("127.0.0.1", port); err != nil {
		t.Errorf("preflightPort(free) = %v, want nil", err)
	}
}

func TestMonitoredProcessReadySuccess(t *testing.T) {
	port, err := PickLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	cmd := runHelper(t, "listen", "HELPER_PORT="+strconv.Itoa(port))
	mp, err := startMonitored(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mp.stop() }()

	if err := mp.waitReady("127.0.0.1", port, 3*time.Second); err != nil {
		t.Errorf("waitReady = %v, want nil", err)
	}
}

// TestMonitoredProcessExitsBeforeReady covers "a child that exits before
// readiness": the process dies immediately and waitReady must report that
// promptly rather than waiting out the full readiness budget.
func TestMonitoredProcessExitsBeforeReady(t *testing.T) {
	port, err := PickLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	cmd := runHelper(t, "exit")
	mp, err := startMonitored(cmd)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	err = mp.waitReady("127.0.0.1", port, 5*time.Second)
	if err == nil {
		t.Fatal("waitReady should report an error when the process exits before becoming ready")
	}
	if !strings.Contains(err.Error(), "exited before becoming ready") {
		t.Errorf("error = %v, want it to say the process exited before becoming ready", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("waitReady took %v to detect the exit; should return promptly instead of waiting out the budget", elapsed)
	}
}

// TestMonitoredProcessDifferentListenerNotTreatedAsReady is the regression
// test for the false-success sequence: a callback port is already occupied
// by an unrelated process, the child that was supposed to own it exits
// immediately (as OpenSSH does on a bind collision with
// ExitOnForwardFailure=yes), and a plain "can I connect?" check would
// wrongly report success against the pre-existing listener. This test fails
// against a plain-listener-check implementation and must pass against the
// process-aware one.
func TestMonitoredProcessDifferentListenerNotTreatedAsReady(t *testing.T) {
	impostor, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer impostor.Close()
	port := impostor.Addr().(*net.TCPAddr).Port

	cmd := runHelper(t, "exit")
	mp, err := startMonitored(cmd)
	if err != nil {
		t.Fatal(err)
	}

	err = mp.waitReady("127.0.0.1", port, 3*time.Second)
	if err == nil {
		t.Fatal("waitReady must not report success from a listener the monitored process does not own")
	}
	if !strings.Contains(err.Error(), "different process is listening") {
		t.Errorf("error = %v, want it to name the unrelated listener", err)
	}
}

func TestMonitoredProcessStopIdempotent(t *testing.T) {
	cmd := runHelper(t, "hang")
	mp, err := startMonitored(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if err := mp.stop(); err != nil {
		t.Errorf("first stop = %v, want nil", err)
	}
	if err := mp.stop(); err != nil {
		t.Errorf("second stop = %v, want nil (idempotent)", err)
	}
}

// TestMonitoredProcessStopEscalatesToKill covers the case that motivated
// stopGraceWindow: a child that receives SIGINT and ignores it. Before
// stopGraceWindow existed, stop would block on <-mp.done forever in this
// case; this test fails (times out) against that implementation.
func TestMonitoredProcessStopEscalatesToKill(t *testing.T) {
	cmd := runHelper(t, "ignore-sigint")
	mp, err := startMonitored(cmd)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- mp.stop() }()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("stop = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not return within 5s; a SIGINT-ignoring child should be escalated to Kill")
	}
}

// writeExitScript writes a tiny shell script that exits with the given code
// without opening any listener, standing in for `msb ssh serve` or OpenSSH
// failing outright.
func writeExitScript(t *testing.T, code int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-msb")
	script := "#!/bin/sh\nexit " + strconv.Itoa(code) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestOpenLoopbackTunnelOccupiedCallbackPort covers "an already occupied
// fixed callback port": the preflight check must reject it before any child
// process is started (Msb points at a nonexistent binary, which would fail
// loudly and differently if the preflight were skipped).
func TestOpenLoopbackTunnelOccupiedCallbackPort(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port

	c := &Client{Msb: "/nonexistent-msb-should-not-be-invoked"}
	_, err = c.OpenLoopbackTunnel("some-sandbox", port, port)
	if !errors.Is(err, ErrCallbackPortOccupied) {
		t.Errorf("OpenLoopbackTunnel(occupied port) = %v, want ErrCallbackPortOccupied", err)
	}
}

// TestOpenLoopbackTunnelServeExitsBeforeReady covers "a child that exits
// before readiness" at the Tunnel level: `msb ssh serve` fails outright, and
// the failure must surface as ErrServeFailed rather than a readiness
// timeout, with the process reaped so no zombie or leaked goroutine remains.
func TestOpenLoopbackTunnelServeExitsBeforeReady(t *testing.T) {
	fakeMsb := writeExitScript(t, 1)
	port, err := PickLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{Msb: fakeMsb}
	_, err = c.OpenLoopbackTunnel("some-sandbox", port, port)
	if !errors.Is(err, ErrServeFailed) {
		t.Errorf("OpenLoopbackTunnel(serve exits) = %v, want ErrServeFailed", err)
	}
}

// TestTunnelCloseIdempotent covers "repeated Close" and "cleanup after
// partial startup failure": Close must be safe to call more than once and
// must reap both children exactly once each.
func TestTunnelCloseIdempotent(t *testing.T) {
	serve, err := startMonitored(runHelper(t, "hang"))
	if err != nil {
		t.Fatal(err)
	}
	ssh, err := startMonitored(runHelper(t, "hang"))
	if err != nil {
		t.Fatal(err)
	}
	tun := &Tunnel{serve: serve, ssh: ssh}

	if err := tun.Close(); err != nil {
		t.Errorf("first Close = %v, want nil", err)
	}
	if err := tun.Close(); err != nil {
		t.Errorf("second Close = %v, want nil (idempotent)", err)
	}
}
