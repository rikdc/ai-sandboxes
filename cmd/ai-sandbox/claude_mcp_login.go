package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/rikdc/ai-sandboxes/internal/runtime/microsandbox"
)

// claudeMCPLoginCommand parses `claude mcp login --callback-port <P> <server>`,
// tunnels host 127.0.0.1:P to guest 127.0.0.1:P, then execs
// `claude mcp login <server>` inside the running claude sandbox.
//
// The port must be supplied on the host command line because Claude Code's
// `claude mcp login` has no port-override flag — the fixed callback port is
// established when the server was registered with
// `claude mcp add --scope user --callback-port P --transport http <server> <url>`.
// Passing the same P through this wrapper tunnels the exact loopback address
// Claude's OAuth flow will redirect the browser to.
func claudeMCPLoginCommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("claude mcp login", flag.ContinueOnError)
	fs.SetOutput(stderr)
	timeout := fs.Duration("timeout", 5*time.Minute, "abort login after this duration")
	callbackPort := fs.Int("callback-port", 0, "loopback port pre-registered via `claude mcp add --callback-port` (required)")
	if err := fs.Parse(reorderFlagsFirst(args, mcpLoginValuedFlags)); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprintf(stderr, "claude mcp login: server name required\nusage: ai-sandbox claude mcp login --callback-port <P> <server-name>\n")
		return 2
	}
	if len(rest) > 1 {
		fmt.Fprintf(stderr, "claude mcp login: expected one server name, got %d args\n", len(rest))
		return 2
	}
	name := rest[0]
	if name == "" || strings.HasPrefix(name, "-") {
		fmt.Fprintf(stderr, "claude mcp login: server name must be non-empty and must not start with '-', got %q\n", name)
		return 2
	}
	if *callbackPort < 1024 || *callbackPort > 65535 {
		fmt.Fprintf(stderr,
			"claude mcp login: --callback-port <1024..65535> is required (unprivileged range; the IANA dynamic range 49152..65535 is recommended); use the same port you passed to `claude mcp add --callback-port` when registering %q\n",
			name)
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
	return executeClaudeMCPLogin(ctx, *timeout, *callbackPort, name, e, stdout, stderr, client)
}

// executeClaudeMCPLogin builds the MCP-login CallbackOperation for the given
// callback port + server name and runs it. Split from claudeMCPLoginCommand so
// tests can drive it without stubbing signal handling or LookPathMsb.
func executeClaudeMCPLogin(ctx context.Context, timeout time.Duration, callbackPort int, serverName string, e execEnv, stdout, stderr io.Writer, client *microsandbox.Client) int {
	op := CallbackOperation{
		Agent:     "claude",
		HostPort:  callbackPort,
		GuestPort: callbackPort,
		Timeout:   timeout,
		GuestArgv: func(int) []string {
			return []string{"claude", "mcp", "login", serverName}
		},
		Workdir:   "/home/node",
		User:      "node",
		LogPrefix: "claude mcp login " + serverName,
	}
	return executeCallbackOperation(ctx, op, e, stdout, stderr, client)
}
