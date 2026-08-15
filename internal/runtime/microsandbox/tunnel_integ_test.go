//go:build integration

package microsandbox

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestOpenLoopbackTunnelIntegration boots a real labelled MicroVM, starts a
// guest-loopback HTTP listener inside it, opens the tunnel, curls the host
// port to prove the forward reaches the listener, then confirms Close tears
// everything down. Skipped unless AI_SANDBOX_MSB_INTEG=1 and both `msb` and
// `ssh` are on PATH.
func TestOpenLoopbackTunnelIntegration(t *testing.T) {
	if os.Getenv("AI_SANDBOX_MSB_INTEG") != "1" {
		t.Skip("set AI_SANDBOX_MSB_INTEG=1 to run the msb integration tests")
	}
	if _, err := exec.LookPath("msb"); err != nil {
		t.Skip("msb not on PATH")
	}
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("ssh not on PATH")
	}

	sandbox := fmt.Sprintf("ai-sandbox-tunnel-integ-%d", time.Now().Unix())
	image := "node:22-bookworm"
	if v := os.Getenv("AI_SANDBOX_MSB_INTEG_IMAGE"); v != "" {
		image = v
	}
	guestPort := 18455
	hostPort, err := pickLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}

	if err := exec.Command("msb", "run", "--detach", "--no-tty",
		"--name", sandbox,
		"--label", "ai-sandbox.agent=codex",
		"--label", "ai-sandbox.workspace=integ",
		image, "--", "/bin/sh", "-c", "sleep 300").Run(); err != nil {
		t.Fatalf("msb run: %v", err)
	}
	t.Cleanup(func() {
		_ = exec.Command("msb", "stop", sandbox).Run()
		_ = exec.Command("msb", "rm", sandbox).Run()
	})

	// Start a guest-loopback HTTP listener.
	listener := fmt.Sprintf(
		"nohup node -e \"require('http').createServer((_,r)=>r.end('ok')).listen(%d,'127.0.0.1')\" >/tmp/probe.log 2>&1 &",
		guestPort,
	)
	if err := exec.Command("msb", "exec", sandbox, "--", "/bin/sh", "-c", listener).Run(); err != nil {
		t.Fatalf("start guest listener: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	c := &Client{}
	tun, err := c.OpenLoopbackTunnel(sandbox, hostPort, guestPort)
	if err != nil {
		t.Fatalf("OpenLoopbackTunnel: %v", err)
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/", hostPort))
	if err != nil {
		_ = tun.Close()
		t.Fatalf("GET through tunnel: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "ok" {
		_ = tun.Close()
		t.Errorf("body = %q, want %q", string(body), "ok")
	}

	if err := tun.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	// After Close, the host port must stop accepting connections.
	if conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", hostPort), 500*time.Millisecond); err == nil {
		_ = conn.Close()
		t.Errorf("host port %d still reachable after Close", hostPort)
	}
}
