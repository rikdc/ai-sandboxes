// Command ai-sandbox is the control plane that resolves a Claude or Codex
// invocation into one deliberate RuntimePlan and launches it in Microsandbox.
// The Fish/Bash/Zsh integrations are thin pass-through shims to this binary.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/rikdc/ai-sandboxes/internal/config"
	"github.com/rikdc/ai-sandboxes/internal/doctor"
	"github.com/rikdc/ai-sandboxes/internal/plan"
	"github.com/rikdc/ai-sandboxes/internal/runtime/microsandbox"
	"github.com/rikdc/ai-sandboxes/internal/runtimepolicy"
	"github.com/rikdc/ai-sandboxes/internal/session"
)

const version = "0.1.0"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	verbose := false
	for len(args) > 0 {
		switch args[0] {
		case "--verbose", "-v":
			verbose = true
			args = args[1:]
		default:
			goto parsed
		}
	}
parsed:
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "run":
		return runCommand(args[1:], verbose, stdout, stderr)
	case "plan":
		return planCommand(args[1:], verbose, stdout, stderr)
	case "doctor":
		return doctorCommand(args[1:], stdout, stderr)
	case "codex":
		if len(args) < 2 {
			fmt.Fprintf(stderr, "ai-sandbox codex: expected subcommand 'login' or 'mcp login <name>'\n")
			return 2
		}
		switch args[1] {
		case "login":
			return codexLoginCommand(args[2:], stdout, stderr)
		case "mcp":
			if len(args) < 3 || args[2] != "login" {
				fmt.Fprintf(stderr, "ai-sandbox codex mcp: expected subcommand 'login <name>'\n")
				return 2
			}
			return codexMCPLoginCommand(args[3:], stdout, stderr)
		default:
			fmt.Fprintf(stderr, "ai-sandbox codex: unknown subcommand %q\n", args[1])
			return 2
		}
	case "help", "--help", "-h":
		usage(stdout)
		return 0
	case "version", "--version", "-V":
		fmt.Fprintf(stdout, "ai-sandbox %s\n", version)
		return 0
	default:
		fmt.Fprintf(stderr, "ai-sandbox: unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, "usage: ai-sandbox <command> [options]\n\n"+
		"Commands:\n"+
		"  run <agent> [-- AGENT_ARGS...]   resolve and launch an agent in Microsandbox\n"+
		"  plan <agent> [-- AGENT_ARGS...]  print the resolved plan without launching\n"+
		"  doctor                           validate host prerequisites without mutation\n"+
		"  codex login [--timeout D]        open scoped tunnel + run browser sign-in\n"+
		"                                   against a running 'run codex' sandbox\n"+
		"  codex mcp login <server-name>    open scoped tunnel + run MCP OAuth sign-in\n"+
		"    [--timeout D]                  for the named server against a running codex sandbox\n"+
		"  version                          print the version\n"+
		"  help                             show this help\n\n"+
		"Agents: claude, codex. Put agent arguments after `--`; they are\n"+
		"forwarded verbatim. The installed fish wrappers invoke this binary, so most\n"+
		"users never call it directly.\n")
}

// runOptions is the parsed `run`/`plan` command line.
type runOptions struct {
	agent     string
	agentArgs []string
	profile   string
	verbose   bool
	help      bool
}

func parseAgentArgs(args []string) (runOptions, error) {
	opts := runOptions{}
	if len(args) == 0 {
		return opts, errors.New("missing agent name")
	}
	if args[0] == "-h" || args[0] == "--help" {
		opts.help = true
		return opts, nil
	}
	opts.agent = args[0]
	args = args[1:]
	profileSeen := false
	for len(args) > 0 {
		a := args[0]
		if profileSeen {
			// The claude-session contract is "profile first, then the agent's
			// own arguments verbatim": everything after --profile VALUE belongs
			// to the agent, presented after an explicit -- when present.
			if a == "--" {
				opts.agentArgs = args[1:]
				return opts, nil
			}
			opts.agentArgs = args
			return opts, nil
		}
		switch {
		case a == "--":
			opts.agentArgs = args[1:]
			return opts, nil
		case a == "-h" || a == "--help":
			opts.help = true
			return opts, nil
		case a == "--profile" || a == "-p":
			if len(args) < 2 {
				return opts, fmt.Errorf("%s requires a value", a)
			}
			opts.profile = args[1]
			profileSeen = true
			args = args[2:]
		case strings.HasPrefix(a, "--profile="):
			opts.profile = strings.TrimPrefix(a, "--profile=")
			profileSeen = true
			args = args[1:]
		case a == "--verbose" || a == "-v":
			opts.verbose = true
			args = args[1:]
		case strings.HasPrefix(a, "-"):
			return opts, fmt.Errorf("unknown option %q (put agent arguments after --)", a)
		default:
			opts.agentArgs = args
			return opts, nil
		}
	}
	return opts, nil
}

func runCommand(args []string, verbose bool, stdout, stderr io.Writer) int {
	opts, err := parseAgentArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "ai-sandbox run: %s\n", err)
		usageCommand("run", stderr)
		return 2
	}
	if opts.help {
		usageCommand("run", stdout)
		return 0
	}
	if verbose {
		opts.verbose = true
	}
	msbPath, err := microsandbox.LookPathMsb()
	if err != nil {
		fmt.Fprintf(stderr, "ai-sandbox: %s\n", err)
		return 127
	}
	e := currentEnv()
	client := &microsandbox.Client{Out: stderr}
	launch := func(argv []string) error { return microsandbox.Launch(msbPath, argv) }
	ctx, cancel := signalContext()
	defer cancel()
	return executeRun(ctx, opts, e, stderr, client, launch)
}

func planCommand(args []string, verbose bool, stdout, stderr io.Writer) int {
	opts, err := parseAgentArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "ai-sandbox plan: %s\n", err)
		usageCommand("plan", stderr)
		return 2
	}
	if opts.help {
		usageCommand("plan", stdout)
		return 0
	}
	if verbose {
		opts.verbose = true
	}
	if _, err := microsandbox.LookPathMsb(); err != nil {
		fmt.Fprintf(stderr, "ai-sandbox: %s\n", err)
		return 127
	}
	e := currentEnv()
	client := &microsandbox.Client{Out: stderr}
	ctx, cancel := signalContext()
	defer cancel()
	return executePlan(ctx, opts, e, stdout, stderr, client)
}

func usageCommand(name string, w io.Writer) {
	fmt.Fprintf(w, "usage: ai-sandbox %s <agent> [-- AGENT_ARGS...]\n", name)
}

// execEnv is the host context the resolver needs. Every field is injectable so
// the orchestration is testable without a real host.
type execEnv struct {
	cwd      string
	home     string
	exe      string
	checkout string
	getenv   func(string) string
	// run executes a host program and returns its stdout, streaming stderr to
	// the terminal. It backs the session-image orchestration
	// (scripts/session/resolve-image.sh, load-image.sh, docker, msb) and is
	// injectable so tests can fake it.
	run func(ctx context.Context, name string, args ...string) ([]byte, error)
}

// execCapture runs name with ctx cancellation and returns its stdout. stderr
// streams straight to the terminal: resolve-image.sh and load-image.sh can
// take minutes on a cold cache, and buffering their progress until
// (non-)failure would leave the user staring at a blank screen. Only stdout is
// captured, because that is what carries the descriptor and digests the
// session resolver parses.
func execCapture(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stderr = os.Stderr
	return cmd.Output()
}

// signalContext returns a context canceled on the usual termination signals,
// so a hung host subprocess (image build, network fetch) is killed when the
// user presses Ctrl-C instead of leaving ai-sandbox blocked forever.
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func currentEnv() execEnv {
	exe, _ := os.Executable()
	cwd, _ := os.Getwd()
	home := os.Getenv("HOME")
	if resolved, err := os.UserHomeDir(); err == nil && home == "" {
		home = resolved
	}
	return execEnv{
		cwd:      cwd,
		home:     home,
		exe:      exe,
		checkout: findCheckout(exe, cwd),
		getenv:   os.Getenv,
		run:      execCapture,
	}
}

// resolvedCheckout returns the checkout already computed for this env, or
// falls back to re-deriving it so a hand-built execEnv still resolves. All
// callers should read checkout from here rather than re-running findCheckout,
// so the workspace guard and the versions.env reader can never disagree on
// which checkout is authoritative.
//
// When several anchors resolve, the binary's own checkout wins over the
// current directory. That is deliberate: the guard protects the checkout that
// provides the code of the binary actually running, and matches the anchor
// order findCheckout documents and doctorCommand/currentEnv already use.
func (e execEnv) resolvedCheckout() string {
	if e.checkout != "" {
		return e.checkout
	}
	return findCheckout(e.exe, e.cwd)
}

func (e execEnv) homeResolved() (string, error) {
	if e.home == "" {
		return "", errors.New("HOME is not set; refusing to guess a home directory")
	}
	resolved, err := filepath.EvalSymlinks(e.home)
	if err != nil {
		return e.home, nil
	}
	return resolved, nil
}

func (e execEnv) protectedRoots(checkout string) []string {
	var roots []string
	if checkout != "" {
		roots = append(roots, checkout)
	}
	// Only protect directories that actually exist: there is nothing to
	// tamper with in a directory that is not installed. RefuseOverlap still
	// fails closed if a protected root exists but cannot be resolved.
	candidates := []string{
		filepath.Join(e.home, ".config", "fish", "functions"),
		filepath.Join(e.home, ".config", "ai-sandboxes", "trusted"),
		filepath.Join(e.home, ".config", "microvms"),
		aiSandboxInstallDir(e.home, e.getenv),
	}
	// Also protect the directory the running binary itself lives in when it
	// resolves outside the checkout. An attacker who replaced the executable
	// there would run as host on the next invocation. Guard both the resolved
	// install directory and, when they differ, the symlink source so a
	// PATH-convenience link like ~/.local/bin/ai-sandbox cannot be replaced.
	exe := e.exe
	if exe == "" {
		exe, _ = os.Executable()
	}
	if exe != "" {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates, exeDir)
		if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
			if dir := filepath.Dir(resolved); dir != exeDir {
				candidates = append(candidates, dir)
			}
		}
	}
	for _, p := range candidates {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			roots = append(roots, p)
		}
	}
	return roots
}

// msbClient is the subset of the Microsandbox adapter the orchestration uses,
// so tests can stand in a fake.
type msbClient interface {
	ImagePresent(tag string) (bool, error)
	ImageMetadata(tag string) (*microsandbox.ImageMetadata, error)
	VolumePresent(name string) (bool, error)
	VolumeCreate(name string) error
	InitSharedState(image string, st *plan.SharedState) error
}

func resolvePlan(ctx context.Context, opts runOptions, e execEnv, stderr io.Writer, client msbClient, loadSessionImage bool) (*plan.RuntimePlan, int) {
	// Reject a session profile for any agent other than claude before anything
	// else, so `run codex --profile foo` fails with the reason, not a generic
	// unknown-option error.
	if opts.profile != "" && opts.agent != "claude" {
		fmt.Fprintf(stderr, "ai-sandbox: --profile is only supported for claude (claude-session); got agent %q\n", opts.agent)
		return nil, 2
	}
	agentCfg, err := config.AgentConfig(opts.agent)
	if err != nil {
		fmt.Fprintf(stderr, "ai-sandbox: %s\n", err)
		return nil, 2
	}

	workspace, err := plan.FindWorkspace(e.cwd)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %s\n", opts.agent, err)
		return nil, 2
	}
	home, err := e.homeResolved()
	if err != nil {
		fmt.Fprintf(stderr, "ai-sandbox: %s\n", err)
		return nil, 2
	}
	// home is the canonical form of $HOME; it exists only so the workspace
	// guard refuses to mount the complete home directory, resolved or not.
	if err := plan.ValidateWorkspace(workspace, home); err != nil {
		fmt.Fprintf(stderr, "%s: %s\n", opts.agent, err)
		return nil, 2
	}
	root := e.resolvedCheckout()
	if err := plan.RefuseOverlap(workspace, e.protectedRoots(root)); err != nil {
		fmt.Fprintln(stderr, err)
		return nil, 1
	}

	// A session run replaces the agent's baked base image with a profile-
	// derived image. The image build (resolve-image.sh) and transport
	// (load-image.sh) stay in Bash; everything downstream is the same plan
	// resolution the base agents use, so network, security, mounts, and argv
	// cannot drift between Claude and claude-session. imageOverride is non-empty
	// exactly when a profile was given, so it is the single gate for the
	// base-image-only checks below.
	imageOverride := ""
	var shared *plan.SharedState
	if opts.profile != "" {
		resolver := session.Resolver{Checkout: root, Home: e.home, Run: e.run}
		desc, rerr := resolver.Resolve(ctx, opts.profile, loadSessionImage)
		if rerr != nil {
			fmt.Fprintf(stderr, "%s: %s\n", opts.agent, rerr)
			return nil, 1
		}
		imageOverride = desc.Image
		if desc.SharedState != nil {
			shared, err = plan.ParseSharedStateRequest(desc.SharedState.ID, desc.SharedState.Quota)
			if err != nil {
				fmt.Fprintf(stderr, "%s: %s\n", opts.agent, err)
				return nil, 2
			}
		}
	}

	// Base agents require their image to be loaded; session images were loaded
	// and digest-verified by the resolver moments ago, and `plan` must not
	// require a load it deliberately skipped. Gate the default image path only.
	if imageOverride == "" {
		present, err := client.ImagePresent(agentCfg.Image)
		if err != nil {
			fmt.Fprintf(stderr, "ai-sandbox: %s\n", err)
			return nil, 1
		}
		if !present {
			fmt.Fprintf(stderr, "%s: image %s is not loaded in Microsandbox; run ./scripts/load-msb first\n", opts.agent, agentCfg.Image)
			return nil, 1
		}
	}

	if imageOverride == "" {
		meta, err := client.ImageMetadata(agentCfg.Image)
		if err != nil {
			fmt.Fprintf(stderr, "ai-sandbox: %s\n", err)
			return nil, 1
		}
		shared, err = runtimepolicy.Resolve(root, e.getenv("AI_SANDBOX_RUNTIME_CONFIG"))
		if err != nil {
			fmt.Fprintf(stderr, "%s: %s\n", opts.agent, err)
			return nil, 2
		}
		if e.run == nil {
			fmt.Fprintf(stderr, "%s: cannot verify image identity: no command runner configured\n", opts.agent)
			return nil, 1
		}
		dockerDigest, err := e.run(ctx, "docker", "image", "inspect", "--format", "{{.Id}}", agentCfg.Image)
		if err != nil {
			fmt.Fprintf(stderr, "%s: could not verify image identity: %v\n", opts.agent, err)
			return nil, 1
		}
		labelOut, err := e.run(ctx, "docker", "image", "inspect", "--format", "{{json .Config.Labels}}", agentCfg.Image)
		if err != nil {
			fmt.Fprintf(stderr, "%s: could not read image shared-state labels: %v\n", opts.agent, err)
			return nil, 1
		}
		labels, err := runtimepolicy.DockerSharedStateLabels(labelOut)
		if err != nil {
			fmt.Fprintf(stderr, "%s: %s\n", opts.agent, err)
			return nil, 1
		}
		if err := runtimepolicy.ReconcileBaseImage(string(dockerDigest), meta.ConfigDigest, labels, shared); err != nil {
			fmt.Fprintf(stderr, "%s: base image verification failed: %v\n", opts.agent, err)
			return nil, 1
		}
	}

	// The egress allowlist lives at the literal $HOME: the installer, doctor,
	// and user documentation all reference $HOME/.config/microvms/<agent>-egress
	// without resolving symlinks, so pass e.home (not the canonical home from
	// above). On dotfiles-managed layouts ~/.config may itself be a symlink,
	// and resolving $HOME first would point at a different directory.
	network, err := e.resolveNetwork(agentCfg, e.home)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %s\n", opts.agent, err)
		return nil, 1
	}

	p, err := plan.Resolve(agentCfg, plan.Input{
		Agent:         opts.agent,
		AgentArgs:     opts.agentArgs,
		Workspace:     workspace,
		SharedState:   shared,
		Network:       network,
		ImageOverride: imageOverride,
	})
	if err != nil {
		fmt.Fprintf(stderr, "ai-sandbox: %s\n", err)
		return nil, 2
	}
	return p, 0
}

func (e execEnv) resolveNetwork(cfg config.Agent, home string) (plan.Network, error) {
	if cfg.Net != "" {
		if cfg.Net == "public" {
			return plan.Network{Public: true}, nil
		}
		return plan.Network{}, fmt.Errorf("unsupported fixed network mode %q", cfg.Net)
	}
	// Both env override and allowlist path derive from the agent name, so
	// codex and claude follow the same deny-by-default model without a
	// per-agent branch here: CLAUDE_MSB_PUBLIC_EGRESS / claude-egress vs
	// CODEX_MSB_PUBLIC_EGRESS / codex-egress.
	overrideVar := strings.ToUpper(cfg.Name) + "_MSB_PUBLIC_EGRESS"
	public := e.getenv(overrideVar) == "1"
	egressFile := filepath.Join(home, ".config", "microvms", cfg.Name+"-egress")
	return plan.ResolveNetwork(public, egressFile, cfg.BaseNetRules)
}

func executeRun(ctx context.Context, opts runOptions, e execEnv, stderr io.Writer, client msbClient, launch func([]string) error) int {
	p, code := resolvePlan(ctx, opts, e, stderr, client, true)
	if code != 0 {
		return code
	}
	if opts.verbose {
		fmt.Fprintf(stderr, "ai-sandbox: resolved plan\n")
		plan.Print(stderr, p)
	}

	agentCfg, err := config.AgentConfig(opts.agent)
	if err != nil {
		fmt.Fprintf(stderr, "ai-sandbox: %s\n", err)
		return 2
	}
	if agentCfg.CreateHomeVolume {
		ok, err := client.VolumePresent(agentCfg.HomeVolume)
		if err != nil {
			fmt.Fprintf(stderr, "ai-sandbox: %s\n", err)
			return 1
		}
		if !ok {
			if err := client.VolumeCreate(agentCfg.HomeVolume); err != nil {
				fmt.Fprintf(stderr, "ai-sandbox: %s\n", err)
				return 1
			}
		}
	}
	if p.SharedState != nil {
		ok, err := client.VolumePresent(p.SharedState.Volume)
		if err != nil {
			fmt.Fprintf(stderr, "ai-sandbox: %s\n", err)
			return 1
		}
		if !ok {
			if err := client.InitSharedState(p.Image, p.SharedState); err != nil {
				fmt.Fprintf(stderr, "%s: %s\n", opts.agent, err)
				return 1
			}
		}
	}

	if err := launch(p.MsbArgv()); err != nil {
		fmt.Fprintf(stderr, "ai-sandbox: could not exec msb: %s\n", err)
		return 1
	}
	return 0
}

func executePlan(ctx context.Context, opts runOptions, e execEnv, stdout, stderr io.Writer, client msbClient) int {
	p, code := resolvePlan(ctx, opts, e, stderr, client, false)
	if code != 0 {
		return code
	}
	plan.Print(stdout, p)
	return 0
}

func doctorCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(stdout, "usage: ai-sandbox doctor")
		return 0
	}
	if len(args) > 0 {
		fmt.Fprintf(stderr, "ai-sandbox doctor: unexpected argument %q\n", args[0])
		fmt.Fprintln(stderr, "usage: ai-sandbox doctor")
		return 2
	}
	exe, _ := os.Executable()
	cwd, _ := os.Getwd()
	home := os.Getenv("HOME")
	checkout := findCheckout(exe, cwd)
	installDir := aiSandboxInstallDir(home, os.Getenv)
	env := doctor.New(home, checkout, installDir, os.Getenv("AI_SANDBOX_RUNTIME_CONFIG"))
	checks := env.Run()
	hadFailures := doctor.Report(stdout, checks)
	if hadFailures {
		return 1
	}
	return 0
}

// aiSandboxInstallDir returns the directory scripts/install-ai-sandbox writes
// the binary to. Kept in one place so both the installer's default and the
// guard's protected root agree.
func aiSandboxInstallDir(home string, getenv func(string) string) string {
	if getenv != nil {
		if v := getenv("AI_SANDBOX_INSTALL_DIR"); v != "" {
			return v
		}
	}
	return filepath.Join(home, ".local", "libexec", "ai-sandboxes")
}

// findCheckout walks upward from several anchors looking for the ai-sandboxes
// checkout that provides the launcher its code: the binary's own directory,
// the current directory, and AI_SANDBOXES_ROOT. The first directory that looks
// like the checkout wins.
func findCheckout(exe, cwd string) string {
	starts := []string{}
	if v := os.Getenv("AI_SANDBOXES_ROOT"); v != "" {
		starts = append(starts, v)
	}
	if exe != "" {
		starts = append(starts, exe)
	}
	if cwd != "" {
		starts = append(starts, cwd)
	}
	for _, s := range starts {
		if dir := walkUpCheckout(s); dir != "" {
			return dir
		}
	}
	return ""
}

func walkUpCheckout(start string) string {
	dir := start
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	for {
		if isCheckout(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func isCheckout(dir string) bool {
	for _, name := range []string{"versions.env", "config", "shell", "docker-bake.hcl"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			return false
		}
	}
	return true
}
