package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/rikdc/ai-sandboxes/internal/config"
	"github.com/rikdc/ai-sandboxes/internal/plan"
	"github.com/rikdc/ai-sandboxes/internal/runtime/microsandbox"
)

// CallbackOperation describes one host->guest tunnel + exec sequence used
// by browser-callback auth flows. The orchestrator opens a scoped msb-ssh
// -L tunnel, execs the guest command through msb exec, and tears the
// tunnel down when the command exits, is signalled, or hits Timeout.
type CallbackOperation struct {
	// Agent is the ai-sandbox.agent label to match when discovering the
	// running sandbox (e.g. "codex").
	Agent string
	// HostPort is the host loopback port to forward from. Zero means
	// "pick a free ephemeral port and use the same port on both sides",
	// which retries on tunnel-open collisions.
	HostPort int
	// GuestPort is the guest loopback port to forward to. Ignored when
	// HostPort is zero.
	GuestPort int
	// Timeout bounds the guest exec. The tunnel and msb exec are torn
	// down when it fires.
	Timeout time.Duration
	// GuestArgv builds the argv for `msb exec ... -- <argv>` given the
	// chosen guest port. Callers with a fixed port ignore the argument.
	GuestArgv func(guestPort int) []string
	// Workdir is passed as `msb exec --workdir`; empty means msb default.
	Workdir string
	// User is passed as `msb exec --user`; empty means msb default.
	User string
	// LogPrefix is used at the start of every stderr line the orchestrator
	// emits, e.g. "codex login" or "codex mcp login notion".
	LogPrefix string
}

// executeCallbackOperation is the shared orchestration for callback-based
// auth flows. Env + client are injected so it is testable without a real
// msb.
func executeCallbackOperation(ctx context.Context, op CallbackOperation, e execEnv, stdout, stderr io.Writer, client *microsandbox.Client) int {
	prefix := op.LogPrefix
	if err := microsandbox.EnsureMsbSSHAuthorized(e.home); err != nil {
		fmt.Fprintf(stderr, "%s: %s\n", prefix, err)
		return 1
	}
	home, err := e.homeResolved()
	if err != nil {
		fmt.Fprintf(stderr, "%s: %s\n", prefix, err)
		return 1
	}
	workspace, err := plan.FindWorkspace(e.cwd)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %s\n", prefix, err)
		return 2
	}
	if err := plan.ValidateWorkspace(workspace, home); err != nil {
		fmt.Fprintf(stderr, "%s: %s\n", prefix, err)
		return 2
	}
	cfg, err := config.AgentConfig(op.Agent)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %s\n", prefix, err)
		return 1
	}
	hash, err := plan.WorkspaceHash(cfg.WorkspaceHash, workspace)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %s\n", prefix, err)
		return 1
	}

	sandbox, err := client.FindSandbox(op.Agent, hash)
	if err != nil {
		switch {
		case errors.Is(err, microsandbox.ErrNoSandbox):
			fmt.Fprintf(stderr,
				"%s: no running %s sandbox for this workspace.\nStart one first in another terminal:\n  ai-sandbox run %s\n",
				prefix, op.Agent, op.Agent)
			return 1
		case errors.Is(err, microsandbox.ErrMultipleSandboxes):
			fmt.Fprintf(stderr,
				"%s: multiple running %s sandboxes match this workspace; stop the extras with `msb stop <name>` and retry\n",
				prefix, op.Agent)
			return 1
		default:
			fmt.Fprintf(stderr, "%s: %s\n", prefix, err)
			return 1
		}
	}

	tun, guestPort, err := openTunnelFn(client, sandbox.Name, op.HostPort, op.GuestPort)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %s\n", prefix, err)
		return 1
	}
	defer func() { _ = tun.Close() }()

	execCtx, cancel := context.WithTimeout(ctx, op.Timeout)
	defer cancel()

	argv := []string{"exec"}
	if op.Workdir != "" {
		argv = append(argv, "--workdir", op.Workdir)
	}
	if op.User != "" {
		argv = append(argv, "--user", op.User)
	}
	argv = append(argv, sandbox.Name, "--")
	argv = append(argv, op.GuestArgv(guestPort)...)

	cmd := exec.CommandContext(execCtx, client.Msb, argv...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			fmt.Fprintf(stderr, "%s: aborted after %s\n", prefix, op.Timeout)
			return 1
		}
		fmt.Fprintf(stderr, "%s: %s\n", prefix, err)
		return 1
	}
	return 0
}

// openTunnel returns a live tunnel and the guest port used. When hostPort
// is zero it picks a free ephemeral port and uses the same value on both
// sides; otherwise it treats hostPort/guestPort as fixed. It does not
// retry — every failure mode (SSH not authorised, `msb ssh serve` not
// starting, forward bind collision) surfaces as an error the caller can
// act on. Bind collisions on a freshly-picked ephemeral port are so rare
// in practice that a retry loop mostly serves to mask the real errors.
//
// Package-level indirection so tests can substitute a mock.
var openTunnelFn = defaultOpenTunnel

func defaultOpenTunnel(client *microsandbox.Client, sandboxName string, hostPort, guestPort int) (*microsandbox.Tunnel, int, error) {
	if hostPort != 0 {
		tun, err := client.OpenLoopbackTunnel(sandboxName, hostPort, guestPort)
		return tun, guestPort, err
	}
	p, err := microsandbox.PickLoopbackPort()
	if err != nil {
		return nil, 0, fmt.Errorf("pick host port: %w", err)
	}
	tun, err := client.OpenLoopbackTunnel(sandboxName, p, p)
	if err != nil {
		return nil, 0, err
	}
	return tun, p, nil
}
