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

// codexMCPLoginCommand parses the `codex mcp login <server-name>` subcommand
// args, validates the server name, and dispatches to executeCodexMCPLogin
// which opens an ephemeral loopback tunnel and execs
// `codex -c mcp_oauth_callback_port=<port> mcp login <server-name>` inside
// the running codex sandbox.
func codexMCPLoginCommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("codex mcp login", flag.ContinueOnError)
	fs.SetOutput(stderr)
	timeout := fs.Duration("timeout", 5*time.Minute, "abort login after this duration")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprintf(stderr, "codex mcp login: server name required\nusage: ai-sandbox codex mcp login <server-name>\n")
		return 2
	}
	if len(rest) > 1 {
		fmt.Fprintf(stderr, "codex mcp login: expected one server name, got %d args\n", len(rest))
		return 2
	}
	name := rest[0]
	if name == "" || strings.HasPrefix(name, "-") {
		fmt.Fprintf(stderr, "codex mcp login: server name must be non-empty and must not start with '-', got %q\n", name)
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
	return executeCodexMCPLogin(ctx, *timeout, name, e, stdout, stderr, client)
}

// executeCodexMCPLogin builds the MCP-login CallbackOperation for the given
// server name and runs it. Split from codexMCPLoginCommand so tests can drive
// it without stubbing signal handling or LookPathMsb.
func executeCodexMCPLogin(ctx context.Context, timeout time.Duration, serverName string, e execEnv, stdout, stderr io.Writer, client *microsandbox.Client) int {
	op := CallbackOperation{
		Agent:   "codex",
		Timeout: timeout,
		GuestArgv: func(port int) []string {
			return []string{
				"codex",
				"-c", fmt.Sprintf("mcp_oauth_callback_port=%d", port),
				"mcp", "login", serverName,
			}
		},
		Workdir:   "/home/node",
		User:      "node",
		LogPrefix: "codex mcp login " + serverName,
	}
	return executeCallbackOperation(ctx, op, e, stdout, stderr, client)
}
