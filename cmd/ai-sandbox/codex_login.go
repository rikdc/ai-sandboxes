package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/rikdc/ai-sandboxes/internal/runtime/microsandbox"
)

// codexCallbackPort is the fixed loopback port Codex binds its OAuth
// account-login callback listener to inside the guest.
const codexCallbackPort = 1455

// codexLoginCommand parses the `codex login` subcommand args, resolves the
// caller's workspace, finds the labelled running codex sandbox, opens a
// scoped msb-ssh -L tunnel from host 127.0.0.1:1455 to guest
// 127.0.0.1:1455, then execs `codex login` inside the guest.
func codexLoginCommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("codex login", flag.ContinueOnError)
	fs.SetOutput(stderr)
	timeout := fs.Duration("timeout", 5*time.Minute, "abort login after this duration")
	if err := fs.Parse(reorderFlagsFirst(args, mcpLoginValuedFlags)); err != nil {
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

// executeCodexLogin builds the account-login CallbackOperation and runs it.
func executeCodexLogin(ctx context.Context, timeout time.Duration, e execEnv, stdout, stderr io.Writer, client *microsandbox.Client) int {
	op := CallbackOperation{
		Agent:     "codex",
		HostPort:  codexCallbackPort,
		GuestPort: codexCallbackPort,
		Timeout:   timeout,
		GuestArgv: func(int) []string { return []string{"codex", "login"} },
		LogPrefix: "codex login",
	}
	return executeCallbackOperation(ctx, op, e, stdout, stderr, client)
}
