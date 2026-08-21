// Package doctor validates the host prerequisites for running agents without
// mutating anything: platform, Docker, Microsandbox, image and volume state,
// launcher placement, and Claude's egress allowlist. Every check is read-only.
package doctor

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/rikdc/ai-sandboxes/internal/config"
	"github.com/rikdc/ai-sandboxes/internal/plan"
	"github.com/rikdc/ai-sandboxes/internal/runtime/microsandbox"
	"github.com/rikdc/ai-sandboxes/internal/runtimepolicy"
)

type statusLabel = string

const (
	statusOK   = "ok"
	statusWarn = "warn"
	statusFail = "fail"
	statusSkip = "skip"
)

// Check is one named validated property.
type Check struct {
	Name   string
	Status string
	Detail string
}

// Runner abstracts subprocess execution so doctor is testable without Docker
// or Microsandbox. The real implementation runs the binaries on PATH.
type Runner struct {
	LookPath func(string) (string, error)
	Run      func(name string, args ...string) ([]byte, error)
}

// New builds an Env against the real host. home is the user's home directory;
// checkout is the ai-sandboxes checkout root (or "" to skip checkout-derived
// checks); installDir is the trusted ai-sandbox install directory and must
// match what scripts/install-ai-sandbox writes to — callers resolve the
// AI_SANDBOX_INSTALL_DIR override before calling in so the guard, the
// installer, and this diagnostic all agree on one path.
func New(home, checkout, installDir, runtimeConfig string) *Env {
	return &Env{
		Home:          home,
		Checkout:      checkout,
		InstallDir:    installDir,
		RuntimeConfig: runtimeConfig,
		Runner: Runner{
			LookPath: execLookPath,
			Run:      execRun,
		},
	}
}

// Env carries the host context doctor inspects. InstallDir must be non-empty
// when Run is called; the CLI resolves it once and passes it in, and tests
// that construct Env directly are expected to do the same.
type Env struct {
	Home          string
	Checkout      string
	InstallDir    string
	RuntimeConfig string
	Runner        Runner
}

var hostnameRE = regexp.MustCompile(`^(\*\.)?[A-Za-z0-9][A-Za-z0-9.-]*$`)

// Run performs every check. None of them mutate the host.
func (e *Env) Run() []Check {
	var checks []Check
	add := func(c Check) { checks = append(checks, c) }

	add(e.checkPlatform())

	msbPath, msbErr := e.Runner.LookPath("msb")
	dockerErr := e.dockerIsUsable()
	shared, policyErr := runtimepolicy.Resolve(e.Checkout, e.RuntimeConfig)
	if policyErr != nil {
		add(Check{Name: "runtime policy", Status: statusFail, Detail: policyErr.Error()})
	}

	if msbErr != nil {
		add(Check{Name: "msb", Status: statusFail, Detail: "not installed or not on PATH"})
	} else {
		add(Check{Name: "msb", Status: statusOK, Detail: msbPath})
	}

	var imageTags, volumeNames []string
	if msbErr == nil {
		if out, err := e.Runner.Run("msb", "image", "list", "--quiet"); err != nil {
			add(Check{Name: "msb image list", Status: statusFail, Detail: err.Error()})
		} else {
			imageTags = splitLines(out)
			for _, tag := range []string{"ai-sandboxes-claude:local", "ai-sandboxes-codex:local"} {
				if contains(imageTags, tag) {
					add(Check{Name: "msb image " + tag, Status: statusOK, Detail: "loaded"})
				} else {
					add(Check{Name: "msb image " + tag, Status: statusFail, Detail: "not loaded; run ./scripts/load-msb"})
				}
			}
		}
		if out, err := e.Runner.Run("msb", "volume", "list", "--quiet"); err != nil {
			add(Check{Name: "msb volume list", Status: statusFail, Detail: err.Error()})
		} else {
			volumeNames = splitLines(out)
		}
		e.checkSharedState(add, imageTags, shared, policyErr)
		e.checkVolumes(add, volumeNames, shared)
	}

	e.checkDockerImages(add, dockerErr)
	e.checkLauncher(add)
	e.checkEgress(add)
	e.checkVersions(add)

	return checks
}

func (e *Env) dockerIsUsable() error {
	if _, err := e.Runner.LookPath("docker"); err != nil {
		return err
	}
	return nil
}

func (e *Env) checkPlatform() Check {
	detail := runtime.GOOS + "/" + runtime.GOARCH
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		return Check{Name: "platform", Status: statusOK, Detail: detail}
	}
	return Check{Name: "platform", Status: statusWarn, Detail: "expected darwin/arm64, running " + detail}
}

func (e *Env) checkDockerImages(add func(Check), dockerErr error) {
	if dockerErr != nil {
		add(Check{Name: "docker", Status: statusFail, Detail: "not installed or not on PATH"})
		return
	}
	if out, err := e.Runner.Run("docker", "version", "--format", "{{.Server.Version}}"); err != nil || strings.TrimSpace(string(out)) == "" {
		add(Check{Name: "docker", Status: statusFail, Detail: "daemon unreachable"})
	} else {
		add(Check{Name: "docker", Status: statusOK, Detail: "server " + strings.TrimSpace(string(out))})
	}
	if out, err := e.Runner.Run("docker", "buildx", "version"); err != nil {
		add(Check{Name: "docker buildx", Status: statusWarn, Detail: "missing; only image builds need it (not agent runs)"})
	} else {
		add(Check{Name: "docker buildx", Status: statusOK, Detail: strings.TrimSpace(string(out))})
	}
	for _, image := range []string{"ai-sandboxes-base:local", "ai-sandboxes-tools:local", "ai-sandboxes-claude:local", "ai-sandboxes-codex:local"} {
		if _, err := e.Runner.Run("docker", "image", "inspect", image); err != nil {
			add(Check{Name: "docker image " + image, Status: statusFail, Detail: "not built; run ./scripts/build"})
		} else {
			add(Check{Name: "docker image " + image, Status: statusOK, Detail: "built"})
		}
	}
}

func (e *Env) checkSharedState(add func(Check), imageTags []string, expected *plan.SharedState, policyErr error) {
	for _, tag := range []string{"ai-sandboxes-claude:local", "ai-sandboxes-codex:local"} {
		if !contains(imageTags, tag) {
			add(Check{Name: "image identity " + tag, Status: statusSkip, Detail: "image not loaded in msb"})
			continue
		}
		out, err := e.Runner.Run("msb", "image", "inspect", "--format", "json", tag)
		if err != nil {
			add(Check{Name: "image identity " + tag, Status: statusSkip, Detail: "cannot inspect: " + err.Error()})
			continue
		}
		meta, err := microsandbox.ParseImageMetadata(out)
		if err != nil {
			add(Check{Name: "image identity " + tag, Status: statusFail, Detail: "cannot parse metadata: " + err.Error()})
			continue
		}
		dockerOut, err := e.Runner.Run("docker", "image", "inspect", "--format", "{{.Id}}", tag)
		if err != nil {
			add(Check{Name: "image identity " + tag, Status: statusFail, Detail: "cannot inspect Docker image: " + err.Error()})
			continue
		}
		labelOut, labelErr := e.Runner.Run("docker", "image", "inspect", "--format", "{{json .Config.Labels}}", tag)
		if labelErr != nil {
			add(Check{Name: "shared state " + tag, Status: statusFail, Detail: "cannot read Docker image labels: " + labelErr.Error()})
			continue
		}
		labels, err := runtimepolicy.DockerSharedStateLabels(labelOut)
		if err != nil {
			add(Check{Name: "shared state " + tag, Status: statusFail, Detail: err.Error()})
			continue
		}
		if policyErr != nil {
			if err := microsandbox.MatchDigests(string(dockerOut), meta.ConfigDigest); err != nil {
				add(Check{Name: "image identity " + tag, Status: statusFail, Detail: err.Error()})
			} else {
				add(Check{Name: "image identity " + tag, Status: statusOK, Detail: "verified"})
			}
			add(Check{Name: "shared state " + tag, Status: statusSkip, Detail: "runtime policy could not be resolved"})
			continue
		}
		if err := runtimepolicy.ReconcileBaseImage(string(dockerOut), meta.ConfigDigest, labels, expected); err != nil {
			if errors.Is(err, microsandbox.ErrDigestMismatch) {
				add(Check{Name: "image identity " + tag, Status: statusFail, Detail: err.Error()})
				add(Check{Name: "shared state " + tag, Status: statusSkip, Detail: "image identity is not verified"})
			} else {
				add(Check{Name: "image identity " + tag, Status: statusOK, Detail: "verified"})
				add(Check{Name: "shared state " + tag, Status: statusFail, Detail: err.Error()})
			}
			continue
		}
		add(Check{Name: "image identity " + tag, Status: statusOK, Detail: "verified"})
		if expected == nil {
			add(Check{Name: "shared state " + tag, Status: statusOK, Detail: "none"})
		} else {
			add(Check{Name: "shared state " + tag, Status: statusOK, Detail: expected.Mount})
		}
	}
}

func (e *Env) checkVolumes(add func(Check), volumeNames []string, shared *plan.SharedState) {
	for _, name := range []string{"claude-home-hardened", "codex-home"} {
		if contains(volumeNames, name) {
			add(Check{Name: "msb volume " + name, Status: statusOK, Detail: "present"})
		} else {
			add(Check{Name: "msb volume " + name, Status: statusWarn, Detail: "missing; created on first run"})
		}
	}
	if shared != nil {
		if contains(volumeNames, shared.Volume) {
			add(Check{Name: "msb volume " + shared.Volume, Status: statusOK, Detail: "present"})
		} else {
			add(Check{Name: "msb volume " + shared.Volume, Status: statusWarn, Detail: "missing; created on first run"})
		}
	}
}

// wrapperContract describes what a correctly generated Fish wrapper for one
// agent must contain, mirroring scripts/install-fish-functions' two wrapper
// shapes: pass-through wrappers (claude, codex) append "-- $argv" after a
// quoted agent name, while claude-session hardcodes an unquoted "claude" and
// forwards $argv directly (its own argv already starts with --profile).
type wrapperContract struct {
	file         string
	agentToken   string
	hasSeparator bool
}

var wrapperContracts = []wrapperContract{
	{file: "claude.fish", agentToken: "claude", hasSeparator: true},
	{file: "codex.fish", agentToken: "codex", hasSeparator: true},
	{file: "claude-session.fish", agentToken: "claude", hasSeparator: false},
}

func (e *Env) checkLauncher(add func(Check)) {
	if e.Home == "" {
		add(Check{Name: "launcher placement", Status: statusFail, Detail: "cannot determine home directory"})
		return
	}
	installedBin := filepath.Join(e.InstallDir, "ai-sandbox")
	binOK := false
	if _, err := os.Stat(installedBin); err == nil {
		add(Check{Name: "ai-sandbox binary", Status: statusOK, Detail: installedBin})
		binOK = true
	} else if path, lerr := e.Runner.LookPath("ai-sandbox"); lerr == nil {
		add(Check{Name: "ai-sandbox binary", Status: statusWarn,
			Detail: "installed copy missing at " + installedBin + "; the Fish wrapper invokes it by absolute path. Falling back to PATH copy at " + path + ". Re-run scripts/install-ai-sandbox to restore the trusted install."})
	} else {
		add(Check{Name: "ai-sandbox binary", Status: statusFail,
			Detail: "not found; run scripts/install-ai-sandbox to build and install it to " + installedBin})
	}

	e.checkInstalledRevision(add, installedBin, binOK)

	functionsDir := filepath.Join(e.Home, ".config", "fish", "functions")
	trustedDir := filepath.Join(e.Home, ".config", "ai-sandboxes", "trusted")
	for _, c := range wrapperContracts {
		e.checkWrapper(add, c, filepath.Join(functionsDir, c.file), installedBin)
	}

	e.checkTrustGuard(add, trustedDir)
}

// checkInstalledRevision compares the revision the installed binary reports
// (via `ai-sandbox version`) against the checkout's own git HEAD, so a
// binary that predates the current checkout is caught even though its file
// merely existing would otherwise look healthy.
func (e *Env) checkInstalledRevision(add func(Check), installedBin string, binOK bool) {
	const name = "ai-sandbox binary revision"
	if !binOK {
		add(Check{Name: name, Status: statusSkip, Detail: "installed binary not found"})
		return
	}
	if e.Checkout == "" {
		add(Check{Name: name, Status: statusSkip, Detail: "checkout not found"})
		return
	}
	checkoutRev, err := gitRevision(e.Runner, e.Checkout)
	if err != nil {
		add(Check{Name: name, Status: statusWarn, Detail: "cannot determine checkout revision (not a git checkout?): " + err.Error()})
		return
	}
	out, err := e.Runner.Run(installedBin, "version")
	if err != nil {
		add(Check{Name: name, Status: statusFail, Detail: "installed binary failed to report its version: " + err.Error()})
		return
	}
	_, installedRev, ok := parseVersionOutput(string(out))
	if !ok {
		add(Check{Name: name, Status: statusFail, Detail: "installed binary version output missing a revision: " + strings.TrimSpace(string(out))})
		return
	}
	if installedRev == "unknown" || checkoutRev == "unknown" {
		add(Check{Name: name, Status: statusWarn,
			Detail: fmt.Sprintf("revision unknown (installed=%s, checkout=%s); rebuild from a git checkout with scripts/install-ai-sandbox", installedRev, checkoutRev)})
		return
	}
	if installedRev != checkoutRev {
		add(Check{Name: name, Status: statusFail,
			Detail: fmt.Sprintf("installed binary is stale (installed rev %s, checkout rev %s); run scripts/install-ai-sandbox", installedRev, checkoutRev)})
		return
	}
	add(Check{Name: name, Status: statusOK, Detail: installedRev})
}

// gitRevision returns the checkout's HEAD commit, suffixed "+dirty" if the
// worktree has uncommitted or untracked changes, matching exactly what
// scripts/install-ai-sandbox embeds into the binary it builds.
func gitRevision(r Runner, checkout string) (string, error) {
	out, err := r.Run("git", "-C", checkout, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	rev := strings.TrimSpace(string(out))
	if rev == "" {
		return "", errors.New("git rev-parse HEAD returned no output")
	}
	statusOut, err := r.Run("git", "-C", checkout, "status", "--porcelain")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(statusOut)) != "" {
		rev += "+dirty"
	}
	return rev, nil
}

var versionOutputRE = regexp.MustCompile(`^ai-sandbox (\S+) \(revision (\S+)\)`)

// parseVersionOutput extracts the version and revision from `ai-sandbox
// version`'s output ("ai-sandbox 0.1.0 (revision <rev>)").
func parseVersionOutput(s string) (ver, rev string, ok bool) {
	m := versionOutputRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// wrapperCommandRE matches the `command env AI_SANDBOXES_ROOT=...` line
// scripts/install-fish-functions generates, capturing everything after the
// `=` for takeFishToken to walk token by token.
var wrapperCommandRE = regexp.MustCompile(`(?m)^\s*command env AI_SANDBOXES_ROOT=(.*)$`)

// parseWrapperCommandLine extracts the embedded checkout root, installed
// binary path, agent token, and whether a "--" separator precedes $argv from
// a generated wrapper's body. It is the exact inverse of
// scripts/install-fish-functions' fish_quote and heredoc shape, chosen over
// re-rendering the wrapper in Go so there is only one implementation of Fish
// quoting to keep correct (the shell one).
func parseWrapperCommandLine(content string) (root, bin, agentToken string, hasSeparator, ok bool) {
	m := wrapperCommandRE.FindStringSubmatch(content)
	if m == nil {
		return "", "", "", false, false
	}
	rest := m[1]
	root, rest, ok = takeFishToken(rest)
	if !ok {
		return "", "", "", false, false
	}
	rest = strings.TrimPrefix(rest, " ")
	bin, rest, ok = takeFishToken(rest)
	if !ok {
		return "", "", "", false, false
	}
	rest = strings.TrimSpace(rest)
	if !strings.HasPrefix(rest, "run ") {
		return "", "", "", false, false
	}
	rest = strings.TrimPrefix(rest, "run ")
	if strings.HasPrefix(rest, "'") {
		agentToken, rest, ok = takeFishToken(rest)
		if !ok {
			return "", "", "", false, false
		}
	} else {
		fields := strings.SplitN(rest, " ", 2)
		agentToken = fields[0]
		rest = ""
		if len(fields) > 1 {
			rest = fields[1]
		}
	}
	rest = strings.TrimSpace(rest)
	hasSeparator = rest == "--" || strings.HasPrefix(rest, "-- ")
	return root, bin, agentToken, hasSeparator, true
}

// takeFishToken parses a single-quoted Fish token from the start of s,
// unescaping \\ and \' exactly as scripts/install-fish-functions' fish_quote
// escapes them, and returns the remainder of s after the closing quote.
func takeFishToken(s string) (value, rest string, ok bool) {
	if !strings.HasPrefix(s, "'") {
		return "", s, false
	}
	var b strings.Builder
	i := 1
	for i < len(s) {
		c := s[i]
		if c == '\\' && i+1 < len(s) && (s[i+1] == '\\' || s[i+1] == '\'') {
			b.WriteByte(s[i+1])
			i += 2
			continue
		}
		if c == '\'' {
			return b.String(), s[i+1:], true
		}
		b.WriteByte(c)
		i++
	}
	return "", s, false
}

func (e *Env) checkWrapper(add func(Check), c wrapperContract, wrapperPath, installedBin string) {
	name := "launcher " + strings.TrimSuffix(c.file, ".fish")
	data, err := os.ReadFile(wrapperPath)
	if err != nil {
		add(Check{Name: name, Status: statusWarn, Detail: "no installed wrapper; run scripts/install-fish-functions"})
		return
	}
	root, bin, agentToken, hasSeparator, ok := parseWrapperCommandLine(string(data))
	if !ok {
		add(Check{Name: name, Status: statusWarn, Detail: "stale wrapper (does not use the ai-sandbox run contract); re-run scripts/install-fish-functions"})
		return
	}
	resolvedRoot := root
	if r, err := filepath.EvalSymlinks(root); err == nil {
		resolvedRoot = r
	}
	resolvedCheckout := e.Checkout
	if r, err := filepath.EvalSymlinks(e.Checkout); err == nil {
		resolvedCheckout = r
	}
	if e.Checkout != "" && resolvedRoot != resolvedCheckout {
		add(Check{Name: name, Status: statusWarn, Detail: "wrapper points at checkout " + root + ", current checkout is " + e.Checkout + "; re-run scripts/install-fish-functions"})
		return
	}
	if bin != installedBin {
		add(Check{Name: name, Status: statusFail, Detail: "wrapper invokes " + bin + " instead of the installed binary " + installedBin + "; re-run scripts/install-fish-functions"})
		return
	}
	if agentToken != c.agentToken || hasSeparator != c.hasSeparator {
		add(Check{Name: name, Status: statusFail, Detail: "wrapper does not match the expected " + c.agentToken + " contract; re-run scripts/install-fish-functions"})
		return
	}
	add(Check{Name: name, Status: statusOK, Detail: "installed pass-through wrapper for " + root})
}

// checkTrustGuard compares the installed guard.fish byte-for-byte against
// the checkout's copy: scripts/install-fish-functions installs it with a
// plain `install`, so any drift means the installed guard predates (or was
// modified after) the checkout it is meant to protect.
func (e *Env) checkTrustGuard(add func(Check), trustedDir string) {
	installedPath := filepath.Join(trustedDir, "guard.fish")
	installed, err := os.ReadFile(installedPath)
	if err != nil {
		add(Check{Name: "trust guard", Status: statusWarn, Detail: "missing; re-run scripts/install-fish-functions"})
		return
	}
	if e.Checkout == "" {
		add(Check{Name: "trust guard", Status: statusSkip, Detail: "checkout not found; cannot verify against source"})
		return
	}
	sourcePath := filepath.Join(e.Checkout, "shell", "fish", "trusted", "guard.fish")
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		add(Check{Name: "trust guard", Status: statusWarn, Detail: "cannot read checkout's guard.fish: " + err.Error()})
		return
	}
	if !bytes.Equal(installed, source) {
		add(Check{Name: "trust guard", Status: statusFail, Detail: "installed guard differs from the checkout's; re-run scripts/install-fish-functions"})
		return
	}
	add(Check{Name: "trust guard", Status: statusOK, Detail: "installed"})
}

func (e *Env) checkEgress(add func(Check)) {
	if e.Home == "" {
		return
	}
	// Claude and Codex both run deny-by-default with per-agent allowlists.
	// The override env var and file both derive from the agent name so the
	// two agents share one check body.
	for _, agent := range []string{"claude", "codex"} {
		e.checkAgentEgress(add, agent)
	}
}

func (e *Env) checkAgentEgress(add func(Check), agent string) {
	label := agent + " egress"
	envVar := strings.ToUpper(agent) + "_MSB_PUBLIC_EGRESS"
	egressFile := filepath.Join(e.Home, ".config", "microvms", agent+"-egress")
	if os.Getenv(envVar) == "1" {
		add(Check{Name: label, Status: statusOK, Detail: fmt.Sprintf("public egress (%s=1); allowlist ignored", envVar)})
		return
	}
	data, err := os.ReadFile(egressFile)
	if err != nil {
		add(Check{Name: label, Status: statusFail, Detail: "missing allowlist " + egressFile + "; copy config/" + agent + "-egress.example there"})
		return
	}
	hosts := 0
	for _, line := range splitLines(data) {
		if strings.HasPrefix(line, "#") {
			continue
		}
		if !hostnameRE.MatchString(line) {
			add(Check{Name: label, Status: statusFail, Detail: "invalid hostname in " + egressFile + ": " + line})
			return
		}
		hosts++
	}
	perm := ""
	if fi, err := os.Stat(egressFile); err == nil {
		perm = fmt.Sprintf(" (mode %04o)", fi.Mode().Perm())
	}
	if hosts == 0 {
		add(Check{Name: label, Status: statusWarn, Detail: "allowlist is empty; " + agent + " will have no HTTPS egress" + perm})
	} else {
		add(Check{Name: label, Status: statusOK, Detail: fmt.Sprintf("%d allowlisted hosts%s", hosts, perm)})
	}
}

func (e *Env) checkVersions(add func(Check)) {
	if e.Checkout == "" {
		return
	}
	if _, err := config.LoadVersions(filepath.Join(e.Checkout, "versions.env")); err != nil {
		add(Check{Name: "versions.env", Status: statusFail, Detail: err.Error()})
	} else {
		add(Check{Name: "versions.env", Status: statusOK, Detail: "readable in " + e.Checkout})
	}
}

// Report writes the checks in a stable, aligned format and returns whether any
// failed.
func Report(w io.Writer, checks []Check) (hadFailures bool) {
	for _, c := range checks {
		switch c.Status {
		case statusOK:
			fmt.Fprintf(w, "[ ok ]  %s: %s\n", c.Name, c.Detail)
		case statusWarn:
			fmt.Fprintf(w, "[warn]  %s: %s\n", c.Name, c.Detail)
		case statusFail:
			fmt.Fprintf(w, "[fail]  %s: %s\n", c.Name, c.Detail)
		case statusSkip:
			fmt.Fprintf(w, "[skip]  %s: %s\n", c.Name, c.Detail)
		}
		if c.Status == statusFail {
			hadFailures = true
		}
	}
	return hadFailures
}

func splitLines(out []byte) []string {
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func execLookPath(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	return path, nil
}

func execRun(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	return cmd.Output()
}
