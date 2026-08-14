// Command ai-sandbox is the control plane that resolves a Claude or Codex
// invocation into one deliberate RuntimePlan and launches it in Microsandbox.
// The Fish/Bash/Zsh integrations are thin pass-through shims to this binary.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rikdc/ai-sandboxes/internal/config"
	"github.com/rikdc/ai-sandboxes/internal/doctor"
	"github.com/rikdc/ai-sandboxes/internal/plan"
	"github.com/rikdc/ai-sandboxes/internal/runtime/microsandbox"
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
	fmt.Fprintf(w, `usage: ai-sandbox <command> [options]

Commands:
  run <agent> [-- AGENT_ARGS...]   resolve and launch an agent in Microsandbox
  plan <agent> [-- AGENT_ARGS...]  print the resolved plan without launching
  doctor                           validate host prerequisites without mutation
  version                          print the version
  help                             show this help

Agents: claude, codex. Put agent arguments after `+"`--`"+`; they are
forwarded verbatim. The installed fish wrappers invoke this binary, so most
users never call it directly.
`)
}

// runOptions is the parsed `run`/`plan` command line.
type runOptions struct {
	agent     string
	agentArgs []string
	profile   string
	verbose   bool
	help      bool
}

var errProfileNotImplemented = errors.New("--profile is not yet implemented in this version (planned for a later milestone)")

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
	for len(args) > 0 {
		a := args[0]
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
			args = args[2:]
		case strings.HasPrefix(a, "--profile="):
			opts.profile = strings.TrimPrefix(a, "--profile=")
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
	if opts.profile != "" {
		fmt.Fprintf(stderr, "ai-sandbox: %v\n", errProfileNotImplemented)
		return 2
	}
	msbPath, err := microsandbox.LookPathMsb()
	if err != nil {
		fmt.Fprintf(stderr, "ai-sandbox: %s\n", err)
		return 127
	}
	e := currentEnv()
	client := &microsandbox.Client{Out: stderr}
	launch := func(argv []string) error { return microsandbox.Launch(msbPath, argv) }
	return executeRun(opts, e, stderr, client, launch)
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
	if opts.profile != "" {
		fmt.Fprintf(stderr, "ai-sandbox: %v\n", errProfileNotImplemented)
		return 2
	}
	if _, err := microsandbox.LookPathMsb(); err != nil {
		fmt.Fprintf(stderr, "ai-sandbox: %s\n", err)
		return 127
	}
	e := currentEnv()
	client := &microsandbox.Client{Out: stderr}
	return executePlan(opts, e, stdout, stderr, client)
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
	}
}

// resolvedCheckout returns the checkout already computed for this env, or
// falls back to re-deriving it so a hand-built execEnv still resolves. All
// callers should read checkout from here rather than re-running findCheckout,
// so the workspace guard and the versions.env reader can never disagree on
// which checkout is authoritative.
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

func resolvePlan(opts runOptions, e execEnv, stderr io.Writer, client msbClient) (*plan.RuntimePlan, int) {
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
	if err := plan.ValidateWorkspace(workspace, home); err != nil {
		fmt.Fprintf(stderr, "%s: %s\n", opts.agent, err)
		return nil, 2
	}
	root := e.resolvedCheckout()
	if err := plan.RefuseOverlap(workspace, e.protectedRoots(root)); err != nil {
		fmt.Fprintln(stderr, err)
		return nil, 1
	}

	rootDiskVersions := ""
	if agentCfg.RootDiskFromVersions {
		root := e.resolvedCheckout()
		if root != "" {
			if v, verr := config.LoadVersions(filepath.Join(root, "versions.env")); verr == nil {
				rootDiskVersions = v.WorkspaceQuota
			}
		}
		if rootDiskVersions == "" {
			// Fall back to the baked policy default so an installed binary
			// that cannot locate a checkout still resolves.
			rootDiskVersions = agentCfg.RootDisk
		}
	}

	present, err := client.ImagePresent(agentCfg.Image)
	if err != nil {
		fmt.Fprintf(stderr, "ai-sandbox: %s\n", err)
		return nil, 1
	}
	if !present {
		fmt.Fprintf(stderr, "%s: image %s is not loaded in Microsandbox; run ./scripts/load-msb first\n", opts.agent, agentCfg.Image)
		return nil, 1
	}

	meta, err := client.ImageMetadata(agentCfg.Image)
	if err != nil {
		fmt.Fprintf(stderr, "ai-sandbox: %s\n", err)
		return nil, 1
	}
	shared, err := plan.SharedStateFromLabels(meta.Labels)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %s\n", opts.agent, err)
		return nil, 2
	}

	network, err := e.resolveNetwork(agentCfg, home)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %s\n", opts.agent, err)
		return nil, 1
	}

	p, err := plan.Resolve(agentCfg, plan.Input{
		Agent:                opts.agent,
		AgentArgs:            opts.agentArgs,
		Workspace:            workspace,
		SharedState:          shared,
		Network:              network,
		RootDiskFromVersions: rootDiskVersions,
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

func executeRun(opts runOptions, e execEnv, stderr io.Writer, client msbClient, launch func([]string) error) int {
	p, code := resolvePlan(opts, e, stderr, client)
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

func executePlan(opts runOptions, e execEnv, stdout, stderr io.Writer, client msbClient) int {
	p, code := resolvePlan(opts, e, stderr, client)
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
	env := doctor.New(home, checkout, installDir)
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
