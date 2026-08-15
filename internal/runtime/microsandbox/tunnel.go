package microsandbox

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// EnsureMsbSSHAuthorized fails if ~/.microsandbox/ssh/authorized_keys is
// absent. `msb ssh serve` requires the file to exist; the codex login
// command surfaces this as a hard failure with the exact remediation
// command rather than silently modifying the user's msb state.
func EnsureMsbSSHAuthorized(home string) error {
	p := filepath.Join(home, ".microsandbox", "ssh", "authorized_keys")
	_, err := os.Stat(p)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("msb ssh not authorized: run `msb ssh authorize --file ~/.ssh/id_ed25519.pub` (or another pubkey) and retry")
	}
	if err != nil {
		return fmt.Errorf("stat %s: %w", p, err)
	}
	return nil
}

// serveArgv builds the argv for `msb ssh serve` bound to host loopback on
// the given port.
func serveArgv(sandbox string, port int) []string {
	return []string{
		"ssh", "serve",
		"--host", "127.0.0.1",
		"--port", fmt.Sprintf("%d", port),
		sandbox,
	}
}

// sshForwardArgv builds the argv for the OpenSSH client that forwards
// host 127.0.0.1:hostPort to guest 127.0.0.1:guestPort through the SSH
// endpoint on 127.0.0.1:servePort. The command blocks (no `-f`) so its
// lifecycle is bound to the returned *exec.Cmd.
func sshForwardArgv(hostPort, guestPort, servePort int) []string {
	return []string{
		"-N",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ExitOnForwardFailure=yes",
		"-L", fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", hostPort, guestPort),
		"-p", fmt.Sprintf("%d", servePort),
		"root@127.0.0.1",
	}
}

// PickLoopbackPort asks the kernel for a free TCP port on 127.0.0.1.
// There is an unavoidable TOCTOU window between close and the caller
// binding the port; acceptable because the port is only ever used for a
// short-lived local SSH endpoint.
func PickLoopbackPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port, nil
}

// waitForListener polls host:port with short DialTimeout until it accepts
// a connection or the budget expires.
func waitForListener(host string, port int, budget time.Duration) error {
	deadline := time.Now().Add(budget)
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %s to accept connections", addr)
}

// Tunnel owns the two subprocesses that make up a host->guest loopback
// forward: `msb ssh serve` on a random host port and `ssh -N -L ...`
// against it. Both are killed by Close.
type Tunnel struct {
	serve *exec.Cmd
	ssh   *exec.Cmd
}

// OpenLoopbackTunnel starts `msb ssh serve` on a random host loopback port,
// waits for it to accept connections, then opens an OpenSSH -L forward
// from 127.0.0.1:hostPort to guest 127.0.0.1:guestPort through the serve
// endpoint. Returns a *Tunnel whose Close tears both subprocesses down.
func (c *Client) OpenLoopbackTunnel(sandbox string, hostPort, guestPort int) (*Tunnel, error) {
	servePort, err := PickLoopbackPort()
	if err != nil {
		return nil, fmt.Errorf("pick host port: %w", err)
	}
	serve := exec.Command(c.msb(), serveArgv(sandbox, servePort)...)
	if len(c.Env) > 0 {
		serve.Env = c.Env
	}
	serve.Stdout = c.Out
	serve.Stderr = c.Out
	if err := serve.Start(); err != nil {
		return nil, fmt.Errorf("start msb ssh serve: %w", err)
	}
	if err := waitForListener("127.0.0.1", servePort, 5*time.Second); err != nil {
		_ = serve.Process.Kill()
		_, _ = serve.Process.Wait()
		return nil, fmt.Errorf("msb ssh serve did not become ready: %w", err)
	}

	ssh := exec.Command("ssh", sshForwardArgv(hostPort, guestPort, servePort)...)
	ssh.Stdout = c.Out
	ssh.Stderr = c.Out
	if err := ssh.Start(); err != nil {
		_ = serve.Process.Kill()
		_, _ = serve.Process.Wait()
		return nil, fmt.Errorf("start ssh forward: %w", err)
	}
	if err := waitForListener("127.0.0.1", hostPort, 5*time.Second); err != nil {
		_ = ssh.Process.Kill()
		_, _ = ssh.Process.Wait()
		_ = serve.Process.Kill()
		_, _ = serve.Process.Wait()
		return nil, fmt.Errorf("ssh forward did not become ready: %w", err)
	}

	return &Tunnel{serve: serve, ssh: ssh}, nil
}

// Close signals both subprocesses, waits for them, and returns the first
// non-nil error. Repeated calls are safe.
func (t *Tunnel) Close() error {
	var firstErr error
	killWait := func(cmd *exec.Cmd) {
		if cmd == nil || cmd.Process == nil {
			return
		}
		if err := cmd.Process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
			_ = cmd.Process.Kill()
		}
		if _, err := cmd.Process.Wait(); err != nil && firstErr == nil {
			// Non-zero exit from a killed process is expected; only
			// surface errors that indicate we could not reap the child.
			if _, ok := err.(*exec.ExitError); !ok {
				firstErr = err
			}
		}
	}
	killWait(t.ssh)
	killWait(t.serve)
	t.ssh = nil
	t.serve = nil
	return firstErr
}
