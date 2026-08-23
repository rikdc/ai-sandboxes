// Command ai-sandbox is the control plane that resolves a Claude or Codex
// invocation into one deliberate RuntimePlan and launches it in Microsandbox.
// The Fish/Bash/Zsh integrations are thin pass-through shims to this binary.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/rikdc/ai-sandboxes/internal/access"
	"github.com/rikdc/ai-sandboxes/internal/config"
	"github.com/rikdc/ai-sandboxes/internal/doctor"
	"github.com/rikdc/ai-sandboxes/internal/plan"
	"github.com/rikdc/ai-sandboxes/internal/runtime/microsandbox"
	"github.com/rikdc/ai-sandboxes/internal/runtimepolicy"
	"github.com/rikdc/ai-sandboxes/internal/session"
)

const version = "0.1.0"

// revision is the exact git commit this binary was built from, injected by
// scripts/install-ai-sandbox via -ldflags "-X main.revision=...". A build
// from a dirty worktree carries a "+dirty" suffix (see that script); a build
// that didn't go through it (e.g. `go build` or `go run` during development)
// keeps this zero value so doctor's revision check degrades to a warning
// instead of a false staleness report.
var revision = "unknown"

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
	case "claude":
		if len(args) < 2 {
			fmt.Fprintf(stderr, "ai-sandbox claude: expected subcommand 'mcp login <name>'\n")
			return 2
		}
		switch args[1] {
		case "mcp":
			if len(args) < 3 || args[2] != "login" {
				fmt.Fprintf(stderr, "ai-sandbox claude mcp: expected subcommand 'login <name>'\n")
				return 2
			}
			return claudeMCPLoginCommand(args[3:], stdout, stderr)
		default:
			fmt.Fprintf(stderr, "ai-sandbox claude: unknown subcommand %q\n", args[1])
			return 2
		}
	case "help", "--help", "-h":
		usage(stdout)
		return 0
	case "version", "--version", "-V":
		fmt.Fprintf(stdout, "ai-sandbox %s (revision %s)\n", version, revision)
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
		"  claude mcp login --callback-port <P> <server-name>\n"+
		"    [--timeout D]                  tunnel host 127.0.0.1:P to the running claude sandbox\n"+
		"                                   and run `claude mcp login <server>`. P must match the\n"+
		"                                   port passed to `claude mcp add --callback-port` when\n"+
		"                                   registering the server (Claude has no login-time port flag).\n"+
		"  version                          print the version\n"+
		"  help                             show this help\n\n"+
		"Agents: claude, codex. Put agent arguments after `--`; they are\n"+
		"forwarded verbatim. `run` and `plan` accept --profile PROFILE\n"+
		"(claude session image) and --access NAME (a runtime SSH access profile:\n"+
		"exact destinations with pinned host keys, mounted read-only from\n"+
		"~/.config/ai-sandboxes/access/keys/<name>).\n"+
		"The installed fish wrappers invoke this binary, so most\n"+
		"users never call it directly.\n")
}

// runOptions is the parsed `run`/`plan` command line.
type runOptions struct {
	agent     string
	agentArgs []string
	profile   string
	access    string
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
		case a == "--access":
			if len(args) < 2 {
				return opts, fmt.Errorf("%s requires a value", a)
			}
			opts.access = args[1]
			args = args[2:]
		case strings.HasPrefix(a, "--access="):
			opts.access = strings.TrimPrefix(a, "--access=")
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
	argv0    string
	checkout string
	getenv   func(string) string
	// run executes a host program and returns its stdout, streaming stderr to
	// the terminal. It backs the session-image orchestration
	// (scripts/session/resolve-image.sh, load-image.sh, docker, msb) and is
	// injectable so tests can fake it.
	run func(ctx context.Context, name string, args ...string) ([]byte, error)
	// resolvConfPath overrides the fallback resolver file read by
	// hostDNSNameservers ("/etc/resolv.conf"). Tests set it so DNS discovery
	// is deterministic and never touches the developer's real configuration.
	resolvConfPath string
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
		argv0:    resolveArgv0(os.Args[0], cwd),
		checkout: findCheckout(exe, cwd),
		getenv:   os.Getenv,
		run:      execCapture,
	}
}

// resolveArgv0 resolves os.Args[0] to an absolute path without relying on
// os.Executable, whose result "may be a symlink or the path it points to"
// depending on the OS (see the os.Executable doc comment). Preserving how
// the process was actually invoked lets the guard protect the PATH symlink
// directory (~/.local/bin/ai-sandbox) even when os.Executable already
// resolved through it to the libexec target.
func resolveArgv0(arg0, cwd string) string {
	if arg0 == "" {
		return ""
	}
	if filepath.IsAbs(arg0) {
		return arg0
	}
	if strings.ContainsRune(arg0, filepath.Separator) {
		if cwd == "" {
			return ""
		}
		return filepath.Join(cwd, arg0)
	}
	// A bare name means the shell resolved it via PATH (the common case for
	// the ~/.local/bin/ai-sandbox symlink); look it up the same way.
	if p, err := exec.LookPath(arg0); err == nil {
		return p
	}
	return ""
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

// userConfigDir resolves the ai-sandboxes user configuration directory for
// access profiles. AI_SANDBOX_CONFIG_DIR is honored through the injectable
// getenv so tests never touch a developer's real configuration; otherwise
// config.UserConfigDir applies the standard XDG resolution.
func (e execEnv) userConfigDir() (string, error) {
	if dir := e.getenv("AI_SANDBOX_CONFIG_DIR"); dir != "" {
		if !filepath.IsAbs(dir) {
			return "", fmt.Errorf("AI_SANDBOX_CONFIG_DIR must be an absolute path: %q", dir)
		}
		return dir, nil
	}
	return config.UserConfigDir()
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
		// The installer symlinks ~/.local/bin/ai-sandbox (or
		// AI_SANDBOX_BIN_DIR) into the libexec install so the auth
		// subcommands are reachable on PATH. That directory holds
		// host-trusted state (the symlink itself) independent of whichever
		// path this particular invocation happened to resolve through, so
		// it is protected unconditionally rather than only when exe/argv0
		// happen to point at it.
		aiSandboxBinDir(e.home, e.getenv),
	}
	// Also protect the directory the running binary itself lives in when it
	// resolves outside the checkout. An attacker who replaced the executable
	// there would run as host on the next invocation. Guard both the
	// resolved install directory and, when they differ, the symlink source
	// so a PATH-convenience link like ~/.local/bin/ai-sandbox cannot be
	// replaced. Both os.Executable's result (e.exe) and the actual
	// invocation path (e.argv0, from os.Args[0]) are considered: Go
	// explicitly documents that os.Executable may return either the symlink
	// or its resolved target depending on the OS, so relying on it alone
	// can silently drop the symlink directory from this list.
	exe := e.exe
	if exe == "" {
		exe, _ = os.Executable()
	}
	for _, p := range []string{exe, e.argv0} {
		if p == "" {
			continue
		}
		dir := filepath.Dir(p)
		candidates = append(candidates, dir)
		if resolved, rerr := filepath.EvalSymlinks(p); rerr == nil {
			if resolvedDir := filepath.Dir(resolved); resolvedDir != dir {
				candidates = append(candidates, resolvedDir)
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

// resolvePlan resolves the complete runtime plan for opts. writeAccessMaterial
// is true only for `run`: it materializes the rendered ssh config and pinned
// known_hosts into the profile's key directory before launch. `plan` passes
// false so printing a plan never writes anything.
func resolvePlan(ctx context.Context, opts runOptions, e execEnv, stderr io.Writer, client msbClient, loadSessionImage, writeAccessMaterial bool) (*plan.RuntimePlan, int) {
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

	// An access profile contributes exact per-destination network rules, one
	// read-only credential mount, and the guest ssh-config environment
	// variable. Validation is read-only; only executeRun materializes the
	// rendered config and known_hosts, so `plan` performs no writes.
	acc, code := e.resolveAccess(ctx, opts.agent, opts.access, network, workspace, writeAccessMaterial, stderr)
	if code != 0 {
		return nil, code
	}

	p, err := plan.Resolve(agentCfg, plan.Input{
		Agent:             opts.agent,
		AgentArgs:         opts.agentArgs,
		Workspace:         workspace,
		SharedState:       shared,
		Network:           network,
		ImageOverride:     imageOverride,
		AccessMount:       acc.mount,
		AccessConfigMount: acc.configMount,
		AccessRules:       acc.rules,
		AccessEnv:         acc.env,
		DnsArgs:           acc.dnsArgs,
	})
	if err != nil {
		fmt.Fprintf(stderr, "ai-sandbox: %s\n", err)
		return nil, 2
	}
	return p, 0
}

// accessResolution is everything an --access profile contributes to the
// runtime plan: the credential mount, the ssh_config include mount, the
// per-destination network rules, the guest environment, and the DNS flags.
type accessResolution struct {
	mount       string
	configMount string
	rules       []string
	env         []string
	dnsArgs     []string
}

// resolveAccess validates the named access profile and derives its
// contribution to the plan. A zero accessResolution and exit code 0 is
// returned when name is empty. writeAccessMaterial is true only for `run`: it
// materializes the rendered ssh config and pinned known_hosts into the
// profile's key directory before launch; `plan` passes false so printing a
// plan never writes anything.
func (e execEnv) resolveAccess(ctx context.Context, agent, name string, network plan.Network, workspace string, writeAccessMaterial bool, stderr io.Writer) (accessResolution, int) {
	var a accessResolution
	if name == "" {
		return a, 0
	}
	cfgDir, err := e.userConfigDir()
	if err != nil {
		fmt.Fprintf(stderr, "ai-sandbox: %s\n", err)
		return a, 2
	}
	prof, err := access.Load(cfgDir, name)
	if err != nil {
		fmt.Fprintf(stderr, "%s: --access %s: %s\n", agent, name, err)
		return a, 2
	}
	keyDir, err := access.ResolveKeyDir(cfgDir, name)
	if err != nil {
		fmt.Fprintf(stderr, "%s: --access %s: %s\n", agent, name, err)
		return a, 2
	}
	if err := access.ValidateKeyDir(keyDir); err != nil {
		fmt.Fprintf(stderr, "%s: --access %s: %s\n", agent, name, err)
		return a, 2
	}
	// The whitelist already rules out ~/.ssh and everything else outside the
	// key root; this additionally refuses a workspace that contains (or is
	// contained in) the mounted key directory.
	if err := plan.RefuseOverlap(workspace, []string{keyDir}); err != nil {
		fmt.Fprintf(stderr, "%s: --access %s: %s\n", agent, name, err)
		return a, 1
	}
	if network.Public {
		fmt.Fprintf(stderr, "%s: warning: public egress is enabled; --access adds no network restriction while it lasts\n", agent)
	}
	// plan.Resolve drops these when the network is public, so they are
	// always computed here rather than duplicating that check.
	a.rules = prof.NetRules()
	a.env = []string{access.SSHConfigEnvVar + "=" + access.GuestDir + "/config"}
	a.mount = keyDir + ":" + access.GuestDir + ":ro"
	a.configMount = access.ConfigIncludeMount(keyDir)
	if !network.Public {
		// LAN destinations only resolve if guest DNS queries reach the host's
		// own resolvers: internal zones (home.lan, split-horizon corporate
		// names) exist nowhere else, and msb's macOS upstream auto-discovery
		// is not reliable on every boot. Pin the discovered resolvers
		// explicitly. Rebind protection must also go: it drops answers
		// pointing at private RFC1918 addresses, which is exactly what every
		// LAN destination resolves to (verified against msb v0.6.13).
		// Public-egress runs keep msb's defaults.
		servers := e.hostDNSNameservers(ctx)
		for _, s := range servers {
			a.dnsArgs = append(a.dnsArgs, "--dns-nameserver", s)
		}
		a.dnsArgs = append(a.dnsArgs, "--no-dns-rebind-protection")
		if len(servers) == 0 {
			fmt.Fprintf(stderr, "%s: warning: could not discover host DNS resolvers; relying on microsandbox auto-discovery\n", agent)
		}
	}
	if writeAccessMaterial {
		if err := access.Materialize(keyDir, prof); err != nil {
			fmt.Fprintf(stderr, "%s: --access %s: %s\n", agent, name, err)
			return a, 1
		}
	}
	return a, 0
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

// hostDNSNameservers returns the host's upstream resolver addresses in
// priority order. macOS reads them from the system configuration store via
// `scutil --dns` (the primary resolver block only); everything else falls back
// to /etc/resolv.conf. A nil e.run (tests) or any discovery failure returns
// nil; the caller warns and lets microsandbox auto-discover.
func (e execEnv) hostDNSNameservers(ctx context.Context) []string {
	path := e.resolvConfPath
	if path == "" {
		path = "/etc/resolv.conf"
	}
	if runtime.GOOS == "darwin" && e.run != nil {
		if out, err := e.run(ctx, "scutil", "--dns"); err == nil {
			if ns := parseScutilNameservers(out); len(ns) > 0 {
				return ns
			}
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return parseResolvConfNameservers(data)
}

// parseScutilNameservers extracts the nameserver entries from the first
// (default) resolver block of `scutil --dns` output. Later blocks cover
// scoped or special-purpose domains (mDNS, .local, VPN splits) that would
// resolve nothing useful as blanket upstreams.
func parseScutilNameservers(out []byte) []string {
	var ns []string
	inPrimary := false
	for _, line := range strings.Split(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "resolver #") {
			if inPrimary {
				break // left the primary block
			}
			inPrimary = strings.HasPrefix(trimmed, "resolver #1")
			continue
		}
		name, value, ok := strings.Cut(trimmed, ":")
		if !ok || !inPrimary || !strings.HasPrefix(name, "nameserver[") {
			continue
		}
		if addr, err := netip.ParseAddr(strings.TrimSpace(value)); err == nil {
			ns = append(ns, addr.String())
		}
	}
	return dedupe(ns)
}

// parseResolvConfNameservers extracts every nameserver address from an
// /etc/resolv.conf-style file.
func parseResolvConfNameservers(data []byte) []string {
	var ns []string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "nameserver" {
			if addr, err := netip.ParseAddr(fields[1]); err == nil {
				ns = append(ns, addr.String())
			}
		}
	}
	return dedupe(ns)
}

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := in[:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func executeRun(ctx context.Context, opts runOptions, e execEnv, stderr io.Writer, client msbClient, launch func([]string) error) int {
	p, code := resolvePlan(ctx, opts, e, stderr, client, true, true)
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
	p, code := resolvePlan(ctx, opts, e, stderr, client, false, false)
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

// aiSandboxBinDir returns the directory scripts/install-ai-sandbox symlinks
// the ai-sandbox binary into. Kept in one place so both the installer's
// default and the guard's protected root agree, exactly like
// aiSandboxInstallDir.
func aiSandboxBinDir(home string, getenv func(string) string) string {
	if getenv != nil {
		if v := getenv("AI_SANDBOX_BIN_DIR"); v != "" {
			return v
		}
	}
	return filepath.Join(home, ".local", "bin")
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
