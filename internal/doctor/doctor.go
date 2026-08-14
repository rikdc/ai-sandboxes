// Package doctor validates the host prerequisites for running agents without
// mutating anything: platform, Docker, Microsandbox, image and volume state,
// launcher placement, and Claude's egress allowlist. Every check is read-only.
package doctor

import (
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
// checks).
func New(home, checkout string) *Env {
	return &Env{
		Home:     home,
		Checkout: checkout,
		Runner: Runner{
			LookPath: execLookPath,
			Run:      execRun,
		},
	}
}

// Env carries the host context doctor inspects.
type Env struct {
	Home     string
	Checkout string
	Runner   Runner
}

var hostnameRE = regexp.MustCompile(`^(\*\.)?[A-Za-z0-9][A-Za-z0-9.-]*$`)

// Run performs every check. None of them mutate the host.
func (e *Env) Run() []Check {
	var checks []Check
	add := func(c Check) { checks = append(checks, c) }

	add(e.checkPlatform())

	msbPath, msbErr := e.Runner.LookPath("msb")
	dockerErr := e.dockerIsUsable()

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
		e.checkSharedState(add, imageTags)
		e.checkVolumes(add, volumeNames)
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

func (e *Env) checkSharedState(add func(Check), imageTags []string) {
	expectedID, expectedQuota := "", ""
	if e.Checkout != "" {
		rt, err := config.LoadRuntime(filepath.Join(e.Checkout, "config", "runtime.json"))
		if err != nil {
			add(Check{Name: "runtime.json", Status: statusWarn, Detail: "cannot parse: " + err.Error()})
		} else if rt.SharedState != nil {
			expectedID, expectedQuota = rt.SharedState.ID, rt.SharedState.Quota
		}
	}
	for _, tag := range []string{"ai-sandboxes-claude:local", "ai-sandboxes-codex:local"} {
		if !contains(imageTags, tag) {
			add(Check{Name: "shared state " + tag, Status: statusSkip, Detail: "image not loaded in msb"})
			continue
		}
		out, err := e.Runner.Run("msb", "image", "inspect", "--format", "json", tag)
		if err != nil {
			add(Check{Name: "shared state " + tag, Status: statusSkip, Detail: "cannot inspect: " + err.Error()})
			continue
		}
		meta, err := microsandbox.ParseImageMetadata(out)
		if err != nil {
			add(Check{Name: "shared state " + tag, Status: statusFail, Detail: "cannot parse metadata: " + err.Error()})
			continue
		}
		st, err := plan.SharedStateFromLabels(meta.Labels)
		if err != nil {
			add(Check{Name: "shared state " + tag, Status: statusFail, Detail: err.Error()})
			continue
		}
		actualID, actualQuota := "", ""
		if st != nil {
			actualID, actualQuota = st.ID, st.Quota
		}
		if actualID != expectedID || actualQuota != expectedQuota {
			add(Check{Name: "shared state " + tag, Status: statusFail,
				Detail: fmt.Sprintf("image carries %q/%q but config/runtime.json wants %q/%q; rebuild with ./scripts/build", actualID, actualQuota, expectedID, expectedQuota)})
		} else if st != nil {
			add(Check{Name: "shared state " + tag, Status: statusOK, Detail: st.Mount})
		} else {
			add(Check{Name: "shared state " + tag, Status: statusOK, Detail: "none"})
		}
	}
}

func (e *Env) checkVolumes(add func(Check), volumeNames []string) {
	for _, name := range []string{"claude-home-hardened", "codex-home"} {
		if contains(volumeNames, name) {
			add(Check{Name: "msb volume " + name, Status: statusOK, Detail: "present"})
		} else {
			add(Check{Name: "msb volume " + name, Status: statusWarn, Detail: "missing; created on first run"})
		}
	}
	if e.Checkout != "" {
		rt, err := config.LoadRuntime(filepath.Join(e.Checkout, "config", "runtime.json"))
		if err == nil && rt.SharedState != nil {
			volume := "agent-state-" + rt.SharedState.ID + "-v1"
			if contains(volumeNames, volume) {
				add(Check{Name: "msb volume " + volume, Status: statusOK, Detail: "present"})
			} else {
				add(Check{Name: "msb volume " + volume, Status: statusWarn, Detail: "missing; created on first run"})
			}
		}
	}
}

func (e *Env) checkLauncher(add func(Check)) {
	if e.Home == "" {
		add(Check{Name: "launcher placement", Status: statusFail, Detail: "cannot determine home directory"})
		return
	}
	installedBin := filepath.Join(e.Home, ".local", "libexec", "ai-sandboxes", "ai-sandbox")
	if _, err := os.Stat(installedBin); err == nil {
		add(Check{Name: "ai-sandbox binary", Status: statusOK, Detail: installedBin})
	} else if path, lerr := e.Runner.LookPath("ai-sandbox"); lerr == nil {
		add(Check{Name: "ai-sandbox binary", Status: statusWarn,
			Detail: "installed copy missing at " + installedBin + "; the Fish wrapper invokes it by absolute path. Falling back to PATH copy at " + path + ". Re-run scripts/install-ai-sandbox to restore the trusted install."})
	} else {
		add(Check{Name: "ai-sandbox binary", Status: statusFail,
			Detail: "not found; run scripts/install-ai-sandbox to build and install it to " + installedBin})
	}
	functionsDir := filepath.Join(e.Home, ".config", "fish", "functions")
	trustedDir := filepath.Join(e.Home, ".config", "ai-sandboxes", "trusted")
	for _, agent := range []string{"claude", "codex"} {
		wrapper := filepath.Join(functionsDir, agent+".fish")
		data, err := os.ReadFile(wrapper)
		if err != nil {
			add(Check{Name: "launcher " + agent, Status: statusWarn, Detail: "no installed wrapper; run scripts/install-fish-functions"})
			continue
		}
		if strings.Contains(string(data), "ai-sandbox run") {
			add(Check{Name: "launcher " + agent, Status: statusOK, Detail: "installed pass-through wrapper"})
		} else {
			add(Check{Name: "launcher " + agent, Status: statusWarn, Detail: "stale wrapper; re-run scripts/install-fish-functions"})
		}
	}
	if _, err := os.Stat(filepath.Join(trustedDir, "guard.fish")); err != nil {
		add(Check{Name: "trust guard", Status: statusWarn, Detail: "missing; re-run scripts/install-fish-functions"})
	} else {
		add(Check{Name: "trust guard", Status: statusOK, Detail: "installed"})
	}
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