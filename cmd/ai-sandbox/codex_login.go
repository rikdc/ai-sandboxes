package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/rikdc/ai-sandboxes/internal/config"
	"github.com/rikdc/ai-sandboxes/internal/plan"
	"github.com/rikdc/ai-sandboxes/internal/runtime/microsandbox"
)

// codexCallbackPort is the fixed loopback port Codex binds its OAuth
// callback listener to inside the guest.
const codexCallbackPort = 1455

// codexLoginCommand parses the `codex login` subcommand args, resolves the
// caller's workspace, finds the labelled running codex sandbox, opens a
// scoped msb-ssh -L tunnel from host 127.0.0.1:1455 to guest
// 127.0.0.1:1455, then execs `codex login` inside the guest.
func codexLoginCommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("codex login", flag.ContinueOnError)
	fs.SetOutput(stderr)
	timeout := fs.Duration("timeout", 5*time.Minute, "abort login after this duration")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	msbPath, err := microsandbox.LookPathMsb()
	if err != nil {
		fmt.Fprintf(stderr, "ai-sandbox: %s\n", err)
		return 127
	}
	e := currentEnv()
	client := &microsandbox.Client{Msb: msbPath, Out: stderr}
	ctx, cancel := signalContext()
	defer cancel()
	return executeCodexLogin(ctx, *timeout, e, stdout, stderr, client)
}

// executeCodexLogin is the pure orchestration: env + client injected so it
// is testable without a real msb.
func executeCodexLogin(ctx context.Context, timeout time.Duration, e execEnv, stdout, stderr io.Writer, client *microsandbox.Client) int {
	if err := microsandbox.EnsureMsbSSHAuthorized(e.home); err != nil {
		fmt.Fprintf(stderr, "codex login: %s\n", err)
		return 1
	}
	home, err := e.homeResolved()
	if err != nil {
		fmt.Fprintf(stderr, "codex login: %s\n", err)
		return 1
	}
	workspace, err := plan.FindWorkspace(e.cwd)
	if err != nil {
		fmt.Fprintf(stderr, "codex login: %s\n", err)
		return 2
	}
	if err := plan.ValidateWorkspace(workspace, home); err != nil {
		fmt.Fprintf(stderr, "codex login: %s\n", err)
		return 2
	}
	cfg, err := config.AgentConfig("codex")
	if err != nil {
		fmt.Fprintf(stderr, "codex login: %s\n", err)
		return 1
	}
	hash, err := plan.WorkspaceHash(cfg.WorkspaceHash, workspace)
	if err != nil {
		fmt.Fprintf(stderr, "codex login: %s\n", err)
		return 1
	}

	sandbox, err := client.FindCodexSandbox(hash)
	if err != nil {
		switch {
		case errors.Is(err, microsandbox.ErrNoCodexSandbox):
			fmt.Fprintf(stderr,
				"codex login: no running codex sandbox for this workspace.\nStart one first in another terminal:\n  ai-sandbox run codex\n")
			return 1
		case errors.Is(err, microsandbox.ErrMultipleCodexSandboxes):
			fmt.Fprintf(stderr,
				"codex login: multiple running codex sandboxes match this workspace; stop the extras with `msb stop <name>` and retry\n")
			return 1
		default:
			fmt.Fprintf(stderr, "codex login: %s\n", err)
			return 1
		}
	}

	tun, err := client.OpenLoopbackTunnel(sandbox.Name, codexCallbackPort, codexCallbackPort)
	if err != nil {
		fmt.Fprintf(stderr, "codex login: %s\n", err)
		return 1
	}
	defer func() { _ = tun.Close() }()

	loginCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(loginCtx, client.Msb, "exec", sandbox.Name, "--", "codex", "login")
	cmd.Stdin = os.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if loginCtx.Err() == context.DeadlineExceeded {
			fmt.Fprintf(stderr, "codex login: aborted after %s\n", timeout)
			return 1
		}
		fmt.Fprintf(stderr, "codex login: %s\n", err)
		return 1
	}
	return 0
}
