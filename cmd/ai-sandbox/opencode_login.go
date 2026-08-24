package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/rikdc/ai-sandboxes/internal/runtime/microsandbox"
)

// opencodeCallbackPort is the loopback port opencode's ChatGPT browser
// OAuth method binds its account-login callback listener to inside the
// guest (the same well-known port Codex uses).
const opencodeCallbackPort = 1455

// opencodeLoginCommand parses the `opencode login` subcommand args,
// resolves the caller's workspace, finds the labelled running opencode
// sandbox, opens a scoped msb-ssh -L tunnel from host 127.0.0.1:1455 to
// guest 127.0.0.1:1455, then execs `opencode auth login` inside the guest.
func opencodeLoginCommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("opencode login", flag.ContinueOnError)
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
	return executeOpencodeLogin(ctx, *timeout, e, stdout, stderr, client)
}

// executeOpencodeLogin builds the account-login CallbackOperation and runs it.
func executeOpencodeLogin(ctx context.Context, timeout time.Duration, e execEnv, stdout, stderr io.Writer, client *microsandbox.Client) int {
	op := CallbackOperation{
		Agent:     "opencode",
		HostPort:  opencodeCallbackPort,
		GuestPort: opencodeCallbackPort,
		Timeout:   timeout,
		GuestArgv: func(int) []string { return []string{"opencode", "auth", "login"} },
		LogPrefix: "opencode login",
	}
	return executeCallbackOperation(ctx, op, e, stdout, stderr, client)
}
