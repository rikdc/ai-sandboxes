package doctor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testRevision is the git HEAD fakeEnv's stubbed git commands and installed
// binary both report by default, so a fresh fakeEnv looks like a healthy,
// up-to-date install unless a test overrides one side of the comparison.
const testRevision = "deadbeef1234567890abcdef1234567890abcdef"

// fakeEnv builds an Env with a stubbed runner and a temp home/checkout layout,
// so doctor runs without Docker or Microsandbox.
func fakeEnv(t *testing.T, withMsb, withEgress bool) (*Env, string) {
	t.Helper()
	home := t.TempDir()
	checkout := t.TempDir()

	os.MkdirAll(filepath.Join(checkout, "config"), 0o755)
	os.WriteFile(filepath.Join(checkout, "versions.env"), []byte("CODEX_VERSION=0.147.0\n"), 0o644)
	// Runtime configuration lives in the user config directory, not the
	// checkout; pin the policy source to an explicit neutral override file so
	// tests never read (or create) the invoking user's real configuration.
	runtimeConfig := filepath.Join(t.TempDir(), "runtime.json")
	os.WriteFile(runtimeConfig, []byte(`{"shared_state": null}`), 0o600)

	if withEgress {
		dir := filepath.Join(home, ".config", "microvms")
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "claude-egress"), []byte("api.anthropic.com\ngithub.com\n"), 0o600)
		os.WriteFile(filepath.Join(dir, "codex-egress"), []byte("api.openai.com\ngithub.com\n"), 0o600)
		os.WriteFile(filepath.Join(dir, "opencode-egress"), []byte("api.openai.com\ngithub.com\n"), 0o600)
	}

	imageInspect := `{"config":{"digest":"sha256:abc","Labels":{}}}`
	runner := Runner{
		LookPath: func(name string) (string, error) {
			switch name {
			case "msb":
				if !withMsb {
					return "", errors.New("not found")
				}
				return "/fake/msb", nil
			case "docker":
				return "/fake/docker", nil
			case "ai-sandbox":
				return "/fake/ai-sandbox", nil
			}
			return "", errors.New("not found")
		},
		Run: func(name string, args ...string) ([]byte, error) {
			switch {
			case name == "docker" && len(args) > 0 && args[0] == "version":
				return []byte("26.1.0"), nil
			case name == "docker" && len(args) > 0 && args[0] == "buildx":
				return []byte("github.com/docker/buildx v0.20.0"), nil
			case name == "docker" && len(args) >= 4 && args[0] == "image" && args[1] == "inspect" && args[2] == "--format":
				if strings.Contains(args[3], ".Config.Labels") {
					return []byte("null"), nil
				}
				return []byte("sha256:abc"), nil
			case name == "docker" && len(args) > 0 && args[0] == "image":
				return []byte(`[{}]`), nil
			case name == "msb" && len(args) > 1 && args[0] == "image" && args[1] == "list":
				return []byte("ai-sandboxes-claude:local\nai-sandboxes-codex:local\nai-sandboxes-opencode:local\n"), nil
			case name == "msb" && len(args) > 1 && args[0] == "volume" && args[1] == "list":
				return []byte("claude-home-hardened\ncodex-home\nopencode-home\n"), nil
			case name == "msb" && len(args) > 1 && args[0] == "image" && args[1] == "inspect":
				return []byte(imageInspect), nil
			case name == "git" && len(args) >= 4 && args[0] == "-C" && args[2] == "rev-parse" && args[3] == "HEAD":
				return []byte(testRevision + "\n"), nil
			case name == "git" && len(args) >= 4 && args[0] == "-C" && args[2] == "status" && args[3] == "--porcelain":
				return []byte(""), nil
			case len(args) == 1 && args[0] == "version" && strings.HasSuffix(name, "ai-sandbox"):
				return []byte(fmt.Sprintf("ai-sandbox 0.1.0 (revision %s)\n", testRevision)), nil
			}
			return nil, errors.New("unexpected command " + name + " " + strings.Join(args, " "))
		},
	}
	// Match the CLI's resolved default so tests that don't care about the
	// override still exercise a realistic install path.
	installDir := filepath.Join(home, ".local", "libexec", "ai-sandboxes")
	return &Env{Home: home, Checkout: checkout, InstallDir: installDir, RuntimeConfig: runtimeConfig, Runner: runner}, home
}

func checkStatus(checks []Check, name string) string {
	for _, c := range checks {
		if c.Name == name {
			return c.Status
		}
	}
	return "missing"
}

func TestDoctorHealthy(t *testing.T) {
	env, home := fakeEnv(t, true, true)
	bin := installBinary(t, env.InstallDir)
	installRealWrapper(t, home, "claude", env.Checkout, bin)
	installRealWrapper(t, home, "codex", env.Checkout, bin)
	installRealWrapper(t, home, "opencode", env.Checkout, bin)
	installRealWrapper(t, home, "claude-session", env.Checkout, bin)
	installTrustGuard(t, home, env.Checkout, "guard contents\n")

	checks := env.Run()
	for _, c := range checks {
		if c.Status == statusFail {
			t.Errorf("unexpected failure: %s: %s", c.Name, c.Detail)
		}
	}
	if s := checkStatus(checks, "msb"); s != statusOK {
		t.Errorf("msb = %s", s)
	}
	if s := checkStatus(checks, "claude egress"); s != statusOK {
		t.Errorf("claude egress = %s", s)
	}
	if s := checkStatus(checks, "launcher claude"); s != statusOK {
		t.Errorf("launcher claude = %s", s)
	}
	if s := checkStatus(checks, "launcher codex"); s != statusOK {
		t.Errorf("launcher codex = %s", s)
	}
	if s := checkStatus(checks, "launcher opencode"); s != statusOK {
		t.Errorf("launcher opencode = %s", s)
	}
	if s := checkStatus(checks, "launcher claude-session"); s != statusOK {
		t.Errorf("launcher claude-session = %s", s)
	}
	if s := checkStatus(checks, "trust guard"); s != statusOK {
		t.Errorf("trust guard = %s", s)
	}
	if s := checkStatus(checks, "ai-sandbox binary revision"); s != statusOK {
		t.Errorf("ai-sandbox binary revision = %s", s)
	}
	if s := checkStatus(checks, "image identity ai-sandboxes-claude:local"); s != statusOK {
		t.Errorf("image identity claude = %s", s)
	}
	if s := checkStatus(checks, "image identity ai-sandboxes-codex:local"); s != statusOK {
		t.Errorf("image identity codex = %s", s)
	}
	if s := checkStatus(checks, "shared state ai-sandboxes-claude:local"); s != statusOK {
		t.Errorf("shared state claude = %s", s)
	}
	if s := checkStatus(checks, "shared state ai-sandboxes-codex:local"); s != statusOK {
		t.Errorf("shared state codex = %s", s)
	}
}

func TestDoctorDetectsFailures(t *testing.T) {
	env, _ := fakeEnv(t, false, false)
	checks := env.Run()
	if s := checkStatus(checks, "msb"); s != statusFail {
		t.Errorf("msb = %s, want fail", s)
	}
	if s := checkStatus(checks, "claude egress"); s != statusFail {
		t.Errorf("claude egress = %s, want fail", s)
	}
	// PATH-only fallback: the installed binary is missing, but LookPath finds
	// an ai-sandbox on PATH. Doctor should warn (not fail) and tell the user
	// to run scripts/install-ai-sandbox.
	if s := checkStatus(checks, "ai-sandbox binary"); s != statusWarn {
		t.Errorf("ai-sandbox binary = %s, want warn (PATH fallback)", s)
	}
}

func TestDoctorReportsBinaryInstalled(t *testing.T) {
	env, home := fakeEnv(t, true, true)
	installDir := filepath.Join(home, ".local", "libexec", "ai-sandboxes")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "ai-sandbox"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	checks := env.Run()
	if s := checkStatus(checks, "ai-sandbox binary"); s != statusOK {
		t.Errorf("ai-sandbox binary = %s, want ok when installed", s)
	}
}

func TestDoctorHonoursInstallDirOverride(t *testing.T) {
	env, _ := fakeEnv(t, true, true)
	// Simulate AI_SANDBOX_INSTALL_DIR pointing outside $HOME/.local/libexec.
	installDir := t.TempDir()
	env.InstallDir = installDir
	if err := os.WriteFile(filepath.Join(installDir, "ai-sandbox"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	checks := env.Run()
	if s := checkStatus(checks, "ai-sandbox binary"); s != statusOK {
		t.Errorf("ai-sandbox binary = %s, want ok when installed under override dir", s)
	}
	for _, c := range checks {
		if c.Name == "ai-sandbox binary" && !strings.Contains(c.Detail, installDir) {
			t.Errorf("ai-sandbox binary detail = %q, want it to reference override dir %q", c.Detail, installDir)
		}
	}
}

func TestDoctorInstallDirOverrideReportedOnFailure(t *testing.T) {
	env, _ := fakeEnv(t, true, true)
	// Point the override somewhere with no binary, and drop the PATH stub so
	// the "not found anywhere" branch fires. The remediation message must
	// name the override dir — the user set AI_SANDBOX_INSTALL_DIR and would
	// otherwise be told to (re)install to the default location.
	installDir := t.TempDir()
	env.InstallDir = installDir
	prev := env.Runner.LookPath
	env.Runner.LookPath = func(name string) (string, error) {
		if name == "ai-sandbox" {
			return "", errors.New("not found")
		}
		return prev(name)
	}
	var found bool
	for _, c := range env.Run() {
		if c.Name != "ai-sandbox binary" {
			continue
		}
		found = true
		if c.Status != statusFail {
			t.Errorf("status = %s, want fail", c.Status)
		}
		if !strings.Contains(c.Detail, installDir) {
			t.Errorf("detail = %q, should reference override dir %q", c.Detail, installDir)
		}
	}
	if !found {
		t.Fatal("no ai-sandbox binary check reported")
	}
}

func TestDoctorFailsWhenBinaryMissing(t *testing.T) {
	env, _ := fakeEnv(t, true, true)
	// No install file present, and drop the PATH stub for ai-sandbox so the
	// fallback also fails.
	prev := env.Runner.LookPath
	env.Runner.LookPath = func(name string) (string, error) {
		if name == "ai-sandbox" {
			return "", errors.New("not found")
		}
		return prev(name)
	}
	checks := env.Run()
	if s := checkStatus(checks, "ai-sandbox binary"); s != statusFail {
		t.Errorf("ai-sandbox binary = %s, want fail when missing everywhere", s)
	}
}

func TestDoctorWarnsOnStaleWrapper(t *testing.T) {
	env, home := fakeEnv(t, true, true)
	// Old-style wrapper that sources the checkout lib instead of ai-sandbox.
	installWrapper(t, home, "claude", "source /opt/ai-sandboxes/lib/ai-sandbox.fish")
	checks := env.Run()
	if s := checkStatus(checks, "launcher claude"); s != statusWarn {
		t.Errorf("launcher claude = %s, want warn", s)
	}
}

// installTrustGuard writes matching guard.fish content into both the
// checkout (the source of truth) and the installed trusted dir, so tests
// that don't care about guard drift start from a passing baseline.
func installTrustGuard(t *testing.T, home, checkout, content string) {
	t.Helper()
	checkoutDir := filepath.Join(checkout, "shell", "fish", "trusted")
	if err := os.MkdirAll(checkoutDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkoutDir, "guard.fish"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	installedDir := filepath.Join(home, ".config", "ai-sandboxes", "trusted")
	if err := os.MkdirAll(installedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installedDir, "guard.fish"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDoctorRevisionMatches covers the healthy case explicitly: installed
// binary and checkout report the same git HEAD.
func TestDoctorRevisionMatches(t *testing.T) {
	env, _ := fakeEnv(t, true, true)
	installBinary(t, env.InstallDir)
	checks := env.Run()
	if s := checkStatus(checks, "ai-sandbox binary revision"); s != statusOK {
		t.Errorf("ai-sandbox binary revision = %s, want ok", s)
	}
}

// TestDoctorRevisionStale is the regression test for "the installed binary
// predates the checkout": the installed binary reports an older commit than
// the checkout's current HEAD, so doctor must fail with an actionable
// message rather than treating file existence as health.
func TestDoctorRevisionStale(t *testing.T) {
	env, _ := fakeEnv(t, true, true)
	bin := installBinary(t, env.InstallDir)
	baseRun := env.Runner.Run
	env.Runner.Run = func(name string, args ...string) ([]byte, error) {
		if name == bin && len(args) == 1 && args[0] == "version" {
			return []byte("ai-sandbox 0.1.0 (revision old0000000000000000000000000000000000000)\n"), nil
		}
		return baseRun(name, args...)
	}
	checks := env.Run()
	var detail string
	for _, c := range checks {
		if c.Name == "ai-sandbox binary revision" {
			detail = c.Detail
		}
	}
	if s := checkStatus(checks, "ai-sandbox binary revision"); s != statusFail {
		t.Fatalf("ai-sandbox binary revision = %s, want fail; detail=%s", s, detail)
	}
	if !strings.Contains(detail, "scripts/install-ai-sandbox") {
		t.Errorf("stale revision detail = %q, want it to name the remediation command", detail)
	}
}

// TestDoctorRevisionDirtyBuildTreatedAsStale covers "a build from a dirty
// worktree must be explicitly identified as dirty or unknown, never
// masquerade as clean": the checkout has uncommitted changes (git status
// reports output), so its revision carries "+dirty" and cannot match a
// binary built from a plain commit.
func TestDoctorRevisionDirtyBuildTreatedAsStale(t *testing.T) {
	env, _ := fakeEnv(t, true, true)
	installBinary(t, env.InstallDir)
	baseRun := env.Runner.Run
	env.Runner.Run = func(name string, args ...string) ([]byte, error) {
		if name == "git" && len(args) >= 4 && args[2] == "status" && args[3] == "--porcelain" {
			return []byte(" M some/file.go\n"), nil
		}
		return baseRun(name, args...)
	}
	checks := env.Run()
	if s := checkStatus(checks, "ai-sandbox binary revision"); s != statusFail {
		t.Errorf("ai-sandbox binary revision = %s, want fail (dirty checkout != clean installed rev)", s)
	}
}

// TestDoctorRevisionUnknownWarns covers a binary that wasn't built through
// scripts/install-ai-sandbox (no ldflags revision) or a checkout that isn't
// a git repository: doctor must warn, not silently pass or hard-fail.
func TestDoctorRevisionUnknownWarns(t *testing.T) {
	env, _ := fakeEnv(t, true, true)
	bin := installBinary(t, env.InstallDir)
	baseRun := env.Runner.Run
	env.Runner.Run = func(name string, args ...string) ([]byte, error) {
		if name == bin && len(args) == 1 && args[0] == "version" {
			return []byte("ai-sandbox 0.1.0 (revision unknown)\n"), nil
		}
		return baseRun(name, args...)
	}
	checks := env.Run()
	if s := checkStatus(checks, "ai-sandbox binary revision"); s != statusWarn {
		t.Errorf("ai-sandbox binary revision = %s, want warn for an unknown installed revision", s)
	}
}

// TestDoctorRevisionUnknownForNonGitCheckout covers a checkout that isn't a
// git repository at all: gitRevision fails, and doctor must warn instead of
// failing the whole check or claiming a match.
func TestDoctorRevisionUnknownForNonGitCheckout(t *testing.T) {
	env, _ := fakeEnv(t, true, true)
	installBinary(t, env.InstallDir)
	baseRun := env.Runner.Run
	env.Runner.Run = func(name string, args ...string) ([]byte, error) {
		if name == "git" && len(args) >= 4 && args[2] == "rev-parse" {
			return nil, errors.New("not a git repository")
		}
		return baseRun(name, args...)
	}
	checks := env.Run()
	if s := checkStatus(checks, "ai-sandbox binary revision"); s != statusWarn {
		t.Errorf("ai-sandbox binary revision = %s, want warn for a non-git checkout", s)
	}
}

// TestDoctorWrapperWrongBinaryRejected is the regression test for a wrapper
// that contains "ai-sandbox run" (so a substring check would accept it) but
// invokes a different installed binary than the one doctor resolved.
func TestDoctorWrapperWrongBinaryRejected(t *testing.T) {
	env, home := fakeEnv(t, true, true)
	wrongBin := filepath.Join(t.TempDir(), "ai-sandbox")
	installRealWrapper(t, home, "claude", env.Checkout, wrongBin)
	checks := env.Run()
	var detail string
	for _, c := range checks {
		if c.Name == "launcher claude" {
			detail = c.Detail
		}
	}
	if s := checkStatus(checks, "launcher claude"); s != statusFail {
		t.Fatalf("launcher claude = %s, want fail; detail=%s", s, detail)
	}
	if !strings.Contains(detail, wrongBin) {
		t.Errorf("wrong-binary detail = %q, want it to name the wrapper's embedded binary path", detail)
	}
}

// TestDoctorWrapperOldCheckoutRejected covers "the checkout has been moved":
// the wrapper's embedded AI_SANDBOXES_ROOT still points at the old location.
func TestDoctorWrapperOldCheckoutRejected(t *testing.T) {
	env, home := fakeEnv(t, true, true)
	bin := filepath.Join(env.InstallDir, "ai-sandbox")
	oldCheckout := filepath.Join(t.TempDir(), "old-checkout")
	installRealWrapper(t, home, "claude", oldCheckout, bin)
	checks := env.Run()
	if s := checkStatus(checks, "launcher claude"); s != statusWarn {
		t.Errorf("launcher claude = %s, want warn for a wrapper pointing at a moved checkout", s)
	}
}

// TestDoctorClaudeSessionMissingDetected covers the third installed
// wrapper, claude-session, which the pre-existing doctor never checked at
// all.
func TestDoctorClaudeSessionMissingDetected(t *testing.T) {
	env, home := fakeEnv(t, true, true)
	bin := installBinary(t, env.InstallDir)
	installRealWrapper(t, home, "claude", env.Checkout, bin)
	installRealWrapper(t, home, "codex", env.Checkout, bin)
	// claude-session deliberately not installed.
	checks := env.Run()
	if s := checkStatus(checks, "launcher claude-session"); s != statusWarn {
		t.Errorf("launcher claude-session = %s, want warn when missing", s)
	}
}

// TestDoctorClaudeSessionStaleDetected covers a claude-session wrapper that
// still points at an old checkout.
func TestDoctorClaudeSessionStaleDetected(t *testing.T) {
	env, home := fakeEnv(t, true, true)
	bin := installBinary(t, env.InstallDir)
	oldCheckout := filepath.Join(t.TempDir(), "old-checkout")
	installRealWrapper(t, home, "claude-session", oldCheckout, bin)
	checks := env.Run()
	if s := checkStatus(checks, "launcher claude-session"); s != statusWarn {
		t.Errorf("launcher claude-session = %s, want warn for a stale wrapper", s)
	}
}

// TestDoctorTrustGuardMissingDetected covers "missing ... trust guard
// detected": no guard.fish installed at all.
func TestDoctorTrustGuardMissingDetected(t *testing.T) {
	env, _ := fakeEnv(t, true, true)
	checks := env.Run()
	if s := checkStatus(checks, "trust guard"); s != statusWarn {
		t.Errorf("trust guard = %s, want warn when missing", s)
	}
}

// TestDoctorTrustGuardModifiedDetected covers "modified trust guard
// detected": the installed guard differs from the checkout's copy, so
// merely existing is not enough to call it healthy.
func TestDoctorTrustGuardModifiedDetected(t *testing.T) {
	env, home := fakeEnv(t, true, true)
	installTrustGuard(t, home, env.Checkout, "original guard\n")
	installedPath := filepath.Join(home, ".config", "ai-sandboxes", "trusted", "guard.fish")
	if err := os.WriteFile(installedPath, []byte("tampered guard\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	checks := env.Run()
	if s := checkStatus(checks, "trust guard"); s != statusFail {
		t.Errorf("trust guard = %s, want fail when the installed guard differs from the checkout", s)
	}
}

// TestDoctorWrapperCustomInstallAndBinDirs covers "custom
// AI_SANDBOX_INSTALL_DIR and AI_SANDBOX_BIN_DIR configurations must
// continue to work" for wrapper and revision validation, not just plain
// binary existence (already covered by TestDoctorHonoursInstallDirOverride).
func TestDoctorWrapperCustomInstallAndBinDirs(t *testing.T) {
	env, home := fakeEnv(t, true, true)
	env.InstallDir = filepath.Join(t.TempDir(), "custom-install")
	bin := installBinary(t, env.InstallDir)
	installRealWrapper(t, home, "claude", env.Checkout, bin)
	checks := env.Run()
	if s := checkStatus(checks, "launcher claude"); s != statusOK {
		t.Errorf("launcher claude = %s, want ok with a custom install dir", s)
	}
	if s := checkStatus(checks, "ai-sandbox binary revision"); s != statusOK {
		t.Errorf("ai-sandbox binary revision = %s, want ok with a custom install dir", s)
	}
}

// TestDoctorWrapperPathsWithApostropheAndSpace covers checkout and install
// paths containing an apostrophe and a space: the wrapper embeds them
// through fish_quote's escaping, and doctor's takeFishToken must be its
// exact inverse rather than breaking on the escape sequence.
func TestDoctorWrapperPathsWithApostropheAndSpace(t *testing.T) {
	home := t.TempDir()
	checkout := filepath.Join(t.TempDir(), "o'brien's checkout")
	if err := os.MkdirAll(filepath.Join(checkout, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(checkout, "versions.env"), []byte("CODEX_VERSION=0.147.0\n"), 0o644)
	runtimeConfig := filepath.Join(t.TempDir(), "runtime.json")
	os.WriteFile(runtimeConfig, []byte(`{"shared_state": null}`), 0o600)

	installDir := filepath.Join(t.TempDir(), "install 'dir'")
	bin := installBinary(t, installDir)
	installRealWrapper(t, home, "claude", checkout, bin)

	env := &Env{
		Home:          home,
		Checkout:      checkout,
		InstallDir:    installDir,
		RuntimeConfig: runtimeConfig,
		Runner: Runner{
			LookPath: func(string) (string, error) { return "", errors.New("not found") },
			Run: func(name string, args ...string) ([]byte, error) {
				switch {
				case name == "git" && len(args) >= 4 && args[2] == "rev-parse":
					return []byte(testRevision + "\n"), nil
				case name == "git" && len(args) >= 4 && args[2] == "status":
					return []byte(""), nil
				case name == bin && len(args) == 1 && args[0] == "version":
					return []byte(fmt.Sprintf("ai-sandbox 0.1.0 (revision %s)\n", testRevision)), nil
				}
				return nil, errors.New("unexpected command " + name + " " + strings.Join(args, " "))
			},
		},
	}
	checks := env.Run()
	if s := checkStatus(checks, "launcher claude"); s != statusOK {
		var detail string
		for _, c := range checks {
			if c.Name == "launcher claude" {
				detail = c.Detail
			}
		}
		t.Errorf("launcher claude = %s (detail=%s), want ok for a checkout/install path with apostrophes and spaces", s, detail)
	}
}

// TestDoctorWrapperSymlinkedCheckout covers a checkout accessed through a
// symlink: the wrapper embeds the path as scripts/install-fish-functions
// resolved it at install time (which may be the symlink, not its target),
// while doctor's own checkout resolution follows symlinks. Both sides must
// be normalized the same way for a fresh install to read as healthy.
func TestDoctorWrapperSymlinkedCheckout(t *testing.T) {
	env, home := fakeEnv(t, true, true)
	bin := installBinary(t, env.InstallDir)
	link := filepath.Join(t.TempDir(), "checkout-link")
	if err := os.Symlink(env.Checkout, link); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}
	// The wrapper was generated while AI_SANDBOXES_ROOT resolved through the
	// symlink; doctor's env.Checkout is already the resolved target.
	installRealWrapper(t, home, "claude", link, bin)
	checks := env.Run()
	if s := checkStatus(checks, "launcher claude"); s != statusOK {
		var detail string
		for _, c := range checks {
			if c.Name == "launcher claude" {
				detail = c.Detail
			}
		}
		t.Errorf("launcher claude = %s (detail=%s), want ok when the wrapper's embedded root resolves to the same checkout via a symlink", s, detail)
	}
}

// TestDoctorReadOnly asserts Run never modifies any file it inspects: it
// snapshots mtimes and content of the binary, both wrappers, and the trust
// guard before and after Run, and requires them to be byte-for-byte and
// mtime-for-mtime identical.
func TestDoctorReadOnly(t *testing.T) {
	env, home := fakeEnv(t, true, true)
	bin := installBinary(t, env.InstallDir)
	installRealWrapper(t, home, "claude", env.Checkout, bin)
	installTrustGuard(t, home, env.Checkout, "guard contents\n")

	paths := []string{
		bin,
		filepath.Join(home, ".config", "fish", "functions", "claude.fish"),
		filepath.Join(home, ".config", "ai-sandboxes", "trusted", "guard.fish"),
		filepath.Join(env.Checkout, "shell", "fish", "trusted", "guard.fish"),
	}
	type snapshot struct {
		content []byte
		modTime int64
	}
	before := make(map[string]snapshot, len(paths))
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		before[p] = snapshot{content: data, modTime: fi.ModTime().UnixNano()}
	}

	env.Run()

	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		want := before[p]
		if string(data) != string(want.content) {
			t.Errorf("%s content changed after doctor.Run", p)
		}
		if fi.ModTime().UnixNano() != want.modTime {
			t.Errorf("%s mtime changed after doctor.Run", p)
		}
	}
}

func TestDoctorDetectsImageDigestMismatch(t *testing.T) {
	env, _ := fakeEnv(t, true, true)
	baseRun := env.Runner.Run
	// Docker and msb report different digests for the same tag.
	env.Runner.Run = func(name string, args ...string) ([]byte, error) {
		if name == "msb" && len(args) > 0 && args[0] == "image" && args[1] == "inspect" {
			return []byte(`{"config":{"digest":"sha256:abc"}}`), nil
		}
		if name == "docker" && len(args) >= 4 && args[0] == "image" && args[1] == "inspect" && strings.Contains(args[3], ".Config.Labels") {
			return []byte("null"), nil
		}
		if name == "docker" && len(args) > 0 && args[0] == "image" && args[1] == "inspect" {
			return []byte("sha256:different"), nil
		}
		return baseRun(name, args...)
	}
	checks := env.Run()
	if s := checkStatus(checks, "image identity ai-sandboxes-claude:local"); s != statusFail {
		t.Errorf("image identity claude = %s, want fail", s)
	}
}

// TestDoctorDetectsSharedStateLabelDrift guards the H1 fix: a digest match
// alone does not prove the loaded image was built with the current
// runtime.json. If runtime.json requests id/quota that do not match the
// preserved Docker labels stamped at build time, doctor must fail — otherwise
// changing runtime.json without rebuilding silently mounts a mismatched
// shared-state volume into an image whose contract is different.
func TestDoctorDetectsSharedStateLabelDrift(t *testing.T) {
	env, _ := fakeEnv(t, true, true)
	// runtime.json requests work:4G; image was built with client:8G.
	if err := os.WriteFile(env.RuntimeConfig,
		[]byte(`{"shared_state":{"id":"work","quota":"4G"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env.Runner.Run = func(name string, args ...string) ([]byte, error) {
		switch {
		case name == "msb" && len(args) > 1 && args[0] == "image" && args[1] == "list":
			return []byte("ai-sandboxes-claude:local\nai-sandboxes-codex:local\n"), nil
		case name == "msb" && len(args) > 1 && args[0] == "volume" && args[1] == "list":
			return []byte(""), nil
		case name == "msb" && len(args) > 1 && args[0] == "image" && args[1] == "inspect":
			return []byte(`{"config":{"digest":"sha256:abc"}}`), nil
		case name == "docker" && len(args) >= 4 && args[0] == "image" && args[1] == "inspect" && args[2] == "--format":
			if strings.Contains(args[3], ".Config.Labels") {
				return []byte(`{"io.ai-sandboxes.shared-state.id":"client","io.ai-sandboxes.shared-state.quota":"8G"}`), nil
			}
			return []byte("sha256:abc"), nil
		case name == "docker" && len(args) > 0 && args[0] == "image":
			return []byte(`[{}]`), nil
		case name == "docker" && len(args) > 0 && (args[0] == "version" || args[0] == "buildx"):
			return []byte("x"), nil
		}
		return nil, errors.New("unexpected: " + name + " " + strings.Join(args, " "))
	}
	checks := env.Run()
	got := checkStatus(checks, "shared state ai-sandboxes-claude:local")
	if got != statusFail {
		t.Fatalf("shared state claude = %s, want fail on label drift", got)
	}
	var detail string
	for _, c := range checks {
		if c.Name == "shared state ai-sandboxes-claude:local" {
			detail = c.Detail
		}
	}
	for _, want := range []string{"work", "4G", "client", "8G", "rebuild"} {
		if !strings.Contains(detail, want) {
			t.Errorf("drift detail missing %q: %s", want, detail)
		}
	}
}

// TestDoctorDetectsSharedStateLabelPresentButRuntimeNone catches the reverse
// drift: image was built with shared-state stamped, but runtime.json requests
// none. Silently ignoring the labels would leave dead configuration in the
// image; fail so the operator either rebuilds or updates runtime.json.
func TestDoctorDetectsSharedStateLabelPresentButRuntimeNone(t *testing.T) {
	env, _ := fakeEnv(t, true, true)
	// runtime.json is the neutral `{"shared_state": null}` from fakeEnv.
	env.Runner.Run = func(name string, args ...string) ([]byte, error) {
		switch {
		case name == "msb" && len(args) > 1 && args[0] == "image" && args[1] == "list":
			return []byte("ai-sandboxes-claude:local\nai-sandboxes-codex:local\n"), nil
		case name == "msb" && len(args) > 1 && args[0] == "volume" && args[1] == "list":
			return []byte(""), nil
		case name == "msb" && len(args) > 1 && args[0] == "image" && args[1] == "inspect":
			return []byte(`{"config":{"digest":"sha256:abc"}}`), nil
		case name == "docker" && len(args) >= 4 && args[0] == "image" && args[1] == "inspect" && args[2] == "--format":
			if strings.Contains(args[3], ".Config.Labels") {
				return []byte(`{"io.ai-sandboxes.shared-state.id":"leftover","io.ai-sandboxes.shared-state.quota":"2G"}`), nil
			}
			return []byte("sha256:abc"), nil
		case name == "docker" && len(args) > 0 && args[0] == "image":
			return []byte(`[{}]`), nil
		case name == "docker" && len(args) > 0 && (args[0] == "version" || args[0] == "buildx"):
			return []byte("x"), nil
		}
		return nil, errors.New("unexpected: " + name + " " + strings.Join(args, " "))
	}
	checks := env.Run()
	if got := checkStatus(checks, "shared state ai-sandboxes-claude:local"); got != statusFail {
		t.Fatalf("shared state claude = %s, want fail when image has stale labels", got)
	}
}

func TestDoctorUsesRuntimeConfigOverride(t *testing.T) {
	env, _ := fakeEnv(t, true, true)
	override := filepath.Join(t.TempDir(), "runtime.json")
	if err := os.WriteFile(override, []byte(`{"shared_state":{"id":"override","quota":"2G"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	env.RuntimeConfig = override
	baseRun := env.Runner.Run
	env.Runner.Run = func(name string, args ...string) ([]byte, error) {
		if name == "docker" && len(args) >= 4 && args[0] == "image" && args[1] == "inspect" && args[2] == "--format" && strings.Contains(args[3], ".Config.Labels") {
			return []byte(`{"io.ai-sandboxes.shared-state.id":"override","io.ai-sandboxes.shared-state.quota":"2G"}`), nil
		}
		return baseRun(name, args...)
	}
	checks := env.Run()
	if got := checkStatus(checks, "shared state ai-sandboxes-claude:local"); got != statusOK {
		t.Fatalf("shared state claude = %s, want override policy accepted", got)
	}
	if got := checkStatus(checks, "msb volume agent-state-override-v1"); got != statusWarn {
		t.Fatalf("override volume = %s, want missing-volume warning", got)
	}
}

func TestDoctorFailsForRelativeRuntimeConfigOverride(t *testing.T) {
	env, _ := fakeEnv(t, true, true)
	env.RuntimeConfig = "runtime.json"
	checks := env.Run()
	if got := checkStatus(checks, "runtime policy"); got != statusFail {
		t.Fatalf("runtime policy = %s, want fail for relative override", got)
	}
	var detail string
	for _, c := range checks {
		if c.Name == "runtime policy" {
			detail = c.Detail
		}
	}
	if !strings.Contains(detail, "absolute path") {
		t.Fatalf("runtime policy detail = %q, want absolute-path error", detail)
	}
}

func installWrapper(t *testing.T, home, agent, body string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "fish", "functions")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, agent+".fish"), []byte("function "+agent+"\n  "+body+"\nend\n"), 0o644)
}

// fishQuoteForTest mirrors scripts/install-fish-functions' fish_quote, so
// fixtures built with it are exact inverses of doctor's takeFishToken.
func fishQuoteForTest(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return "'" + s + "'"
}

// renderWrapperCommandLine builds the "command env AI_SANDBOXES_ROOT=..."
// line scripts/install-fish-functions generates for a given root/bin/agent,
// so wrapper-parsing tests exercise fixtures shaped exactly like the real
// installer's output instead of a hand-rolled approximation.
func renderWrapperCommandLine(root, bin, agentToken string, quoteAgent, hasSeparator bool) string {
	agentPart := agentToken
	if quoteAgent {
		agentPart = fishQuoteForTest(agentToken)
	}
	suffix := "$argv"
	if hasSeparator {
		suffix = "-- $argv"
	}
	return fmt.Sprintf("command env AI_SANDBOXES_ROOT=%s %s run %s %s",
		fishQuoteForTest(root), fishQuoteForTest(bin), agentPart, suffix)
}

// installRealWrapper writes a wrapper for agent shaped exactly like
// scripts/install-fish-functions would generate it for the given root/bin,
// honouring each agent's real contract (claude-session hardcodes an
// unquoted "claude" and has no "--" separator).
func installRealWrapper(t *testing.T, home, agent, root, bin string) {
	t.Helper()
	quoteAgent, hasSeparator, agentToken := true, true, agent
	if agent == "claude-session" {
		quoteAgent, hasSeparator, agentToken = false, false, "claude"
	}
	installWrapper(t, home, agent, renderWrapperCommandLine(root, bin, agentToken, quoteAgent, hasSeparator))
}

// installBinary writes a stand-in file at env's InstallDir so ai-sandbox
// binary existence checks (which only os.Stat the path) see it as present.
func installBinary(t *testing.T, installDir string) string {
	t.Helper()
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(installDir, "ai-sandbox")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}
