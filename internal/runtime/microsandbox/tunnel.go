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

// Sentinel errors distinguishing why a tunnel failed to open. Callers use
// errors.Is against these to decide whether a failure is worth a narrowly
// scoped retry (ErrCallbackPortOccupied on a self-picked ephemeral port) or
// must be surfaced verbatim (everything else, including on a caller-fixed
// port such as Claude's registered callback port).
var (
	// ErrCallbackPortOccupied means the requested host loopback port is
	// already bound to something else and cannot be forwarded to.
	ErrCallbackPortOccupied = errors.New("callback port already occupied")
	// ErrServeFailed means `msb ssh serve` failed to start, exited before
	// becoming ready, or never opened its listener in time.
	ErrServeFailed = errors.New("msb ssh serve failed")
	// ErrForwardFailed means the OpenSSH -L forward failed to start, exited
	// before becoming ready, or never opened its listener in time.
	ErrForwardFailed = errors.New("ssh forward failed")
	// ErrReadinessTimeout means a child process was still running but never
	// opened its expected listener within the readiness budget.
	ErrReadinessTimeout = errors.New("readiness timeout")
)

// preflightPort verifies host:port is not already bound to something else by
// attempting to listen on it and immediately releasing it. There is an
// unavoidable TOCTOU window between this check and the child process binding
// the same address, but it turns the common case -- a stale process still
// holding the registered callback port -- into an immediate, actionable
// failure instead of a tunnel that silently forwards to the wrong process.
func preflightPort(host string, port int) error {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrCallbackPortOccupied, addr, err)
	}
	return l.Close()
}

// monitoredProcess wraps an *exec.Cmd whose exit is observed exactly once by
// a background goroutine. Readiness polling races a listener check against
// process death instead of trusting a successful TCP dial in isolation, so a
// connection that only succeeds because some unrelated process already held
// the port can never be mistaken for the child becoming ready.
type monitoredProcess struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
}

// startMonitored starts cmd and reaps it exactly once in a background
// goroutine. The goroutine exits as soon as the process does, so it never
// leaks past Close/stop.
func startMonitored(cmd *exec.Cmd) (*monitoredProcess, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	mp := &monitoredProcess{cmd: cmd, done: make(chan struct{})}
	go func() {
		mp.err = cmd.Wait()
		close(mp.done)
	}()
	return mp, nil
}

// exited reports whether the process has already been reaped.
func (mp *monitoredProcess) exited() bool {
	select {
	case <-mp.done:
		return true
	default:
		return false
	}
}

// waitReady polls host:port until it accepts a connection, the process
// exits, or budget expires. A connection that succeeds only after the
// process has already been reaped is treated as an unrelated listener, not
// proof of readiness: this is what prevents the false-success sequence where
// a stale process on the requested port satisfies a plain TCP check while
// the child that was supposed to own it already died (e.g. OpenSSH exiting
// immediately on a bind collision because of ExitOnForwardFailure=yes).
// listenerGraceWindow bounds how long waitReady waits, after a successful
// dial, to see whether the monitored process immediately exits anyway.
// OpenSSH with ExitOnForwardFailure=yes -- and `msb ssh serve` on a bind
// failure -- exits promptly on a bind collision, so a process still running
// this long after its expected listener answered is treated as that
// listener's owner. A connection that succeeds only because an unrelated
// process already held the port is caught here instead of being mistaken
// for readiness.
const listenerGraceWindow = 300 * time.Millisecond

func (mp *monitoredProcess) waitReady(host string, port int, budget time.Duration) error {
	deadline := time.Now().Add(budget)
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	for {
		conn, dialErr := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			select {
			case <-mp.done:
				return fmt.Errorf("exited before becoming ready (a different process is listening on %s): %v", addr, mp.err)
			case <-time.After(listenerGraceWindow):
				return nil
			}
		}
		if mp.exited() {
			return fmt.Errorf("exited before becoming ready (waiting for %s): %v", addr, mp.err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w: %s", ErrReadinessTimeout, addr)
		}
		select {
		case <-mp.done:
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// stop signals the process, waits for the monitor goroutine to observe its
// exit exactly once, and classifies the result. Safe to call multiple times
// and safe to call after the process already exited on its own.
func (mp *monitoredProcess) stop() error {
	if mp == nil || mp.cmd == nil || mp.cmd.Process == nil {
		return nil
	}
	if !mp.exited() {
		if err := mp.cmd.Process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
			_ = mp.cmd.Process.Kill()
		}
	}
	<-mp.done
	if mp.err != nil {
		// Non-zero exit from a signalled/killed process is expected; only
		// surface errors that indicate we could not reap the child.
		if _, ok := mp.err.(*exec.ExitError); ok {
			return nil
		}
		return mp.err
	}
	return nil
}

// Tunnel owns the two subprocesses that make up a host->guest loopback
// forward: `msb ssh serve` on a random host port and `ssh -N -L ...`
// against it. Both are killed by Close.
type Tunnel struct {
	serve *monitoredProcess
	ssh   *monitoredProcess
}

// OpenLoopbackTunnel starts `msb ssh serve` on a random host loopback port,
// waits for it to become process-verified ready, then opens an OpenSSH -L
// forward from 127.0.0.1:hostPort to guest 127.0.0.1:guestPort through the
// serve endpoint. hostPort is treated as fixed: it is preflighted before any
// child starts, and this function never retries a bind collision on it --
// callers that self-pick an ephemeral port and want a narrow, specifically
// scoped retry on ErrCallbackPortOccupied must implement that themselves
// (see cmd/ai-sandbox's defaultOpenTunnel), because retrying here would risk
// silently retrying every other failure mode as though it were a collision.
// Returns a *Tunnel whose Close tears both subprocesses down.
func (c *Client) OpenLoopbackTunnel(sandbox string, hostPort, guestPort int) (*Tunnel, error) {
	if err := preflightPort("127.0.0.1", hostPort); err != nil {
		return nil, err
	}

	servePort, err := PickLoopbackPort()
	if err != nil {
		return nil, fmt.Errorf("pick host port: %w", err)
	}
	serveCmd := exec.Command(c.msb(), serveArgv(sandbox, servePort)...)
	if len(c.Env) > 0 {
		serveCmd.Env = c.Env
	}
	serveCmd.Stdout = c.Out
	serveCmd.Stderr = c.Out
	serve, err := startMonitored(serveCmd)
	if err != nil {
		return nil, fmt.Errorf("%w: start: %w", ErrServeFailed, err)
	}
	if err := serve.waitReady("127.0.0.1", servePort, 5*time.Second); err != nil {
		_ = serve.stop()
		return nil, fmt.Errorf("%w: %w", ErrServeFailed, err)
	}

	sshCmd := exec.Command("ssh", sshForwardArgv(hostPort, guestPort, servePort)...)
	sshCmd.Stdout = c.Out
	sshCmd.Stderr = c.Out
	ssh, err := startMonitored(sshCmd)
	if err != nil {
		_ = serve.stop()
		return nil, fmt.Errorf("%w: start: %w", ErrForwardFailed, err)
	}
	if err := ssh.waitReady("127.0.0.1", hostPort, 5*time.Second); err != nil {
		_ = ssh.stop()
		_ = serve.stop()
		return nil, fmt.Errorf("%w: %w", ErrForwardFailed, err)
	}

	return &Tunnel{serve: serve, ssh: ssh}, nil
}

// Close signals both subprocesses, waits for the monitor goroutines to reap
// them exactly once, and returns the first non-nil error. Repeated calls are
// safe: each field is nilled out after its first stop, so a second Close is
// a no-op.
func (t *Tunnel) Close() error {
	var firstErr error
	if t.ssh != nil {
		if err := t.ssh.stop(); err != nil && firstErr == nil {
			firstErr = err
		}
		t.ssh = nil
	}
	if t.serve != nil {
		if err := t.serve.stop(); err != nil && firstErr == nil {
			firstErr = err
		}
		t.serve = nil
	}
	return firstErr
}
