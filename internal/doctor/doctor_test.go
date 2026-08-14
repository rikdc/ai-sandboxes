package doctor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeEnv builds an Env with a stubbed runner and a temp home/checkout layout,
// so doctor runs without Docker or Microsandbox.
func fakeEnv(t *testing.T, withMsb, withEgress bool) (*Env, string) {
	t.Helper()
	home := t.TempDir()
	checkout := t.TempDir()

	os.MkdirAll(filepath.Join(checkout, "config"), 0o755)
	os.WriteFile(filepath.Join(checkout, "versions.env"), []byte("CODEX_VERSION=0.147.0\n"), 0o644)
	os.WriteFile(filepath.Join(checkout, "config", "runtime.json"), []byte(`{"shared_state": null}`), 0o644)

	if withEgress {
		dir := filepath.Join(home, ".config", "microvms")
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "claude-egress"), []byte("api.anthropic.com\ngithub.com\n"), 0o600)
		os.WriteFile(filepath.Join(dir, "codex-egress"), []byte("api.openai.com\ngithub.com\n"), 0o600)
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
				return []byte("ai-sandboxes-claude:local\nai-sandboxes-codex:local\n"), nil
			case name == "msb" && len(args) > 1 && args[0] == "volume" && args[1] == "list":
				return []byte("claude-home-hardened\ncodex-home\n"), nil
			case name == "msb" && len(args) > 1 && args[0] == "image" && args[1] == "inspect":
				return []byte(imageInspect), nil
			}
			return nil, errors.New("unexpected command " + name + " " + strings.Join(args, " "))
		},
	}
	// Match the CLI's resolved default so tests that don't care about the
	// override still exercise a realistic install path.
	installDir := filepath.Join(home, ".local", "libexec", "ai-sandboxes")
	return &Env{Home: home, Checkout: checkout, InstallDir: installDir, Runner: runner}, home
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
	installWrapper(t, home, "claude", "command ai-sandbox run claude -- $argv")
	installWrapper(t, home, "codex", "command ai-sandbox run codex -- $argv")

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

func TestDoctorDetectsImageDigestMismatch(t *testing.T) {
	env, _ := fakeEnv(t, true, true)
	// Docker and msb report different digests for the same tag.
	env.Runner.Run = func(name string, args ...string) ([]byte, error) {
		if name == "msb" && len(args) > 0 && args[0] == "image" && args[1] == "inspect" {
			return []byte(`{"config":{"digest":"sha256:abc"}}`), nil
		}
		if name == "docker" && len(args) > 0 && args[0] == "image" && args[1] == "inspect" {
			return []byte("sha256:different"), nil
		}
		if name == "msb" && len(args) > 0 && args[0] == "image" && args[1] == "list" {
			return []byte("ai-sandboxes-claude:local\nai-sandboxes-codex:local\n"), nil
		}
		if name == "msb" && len(args) > 0 && args[0] == "volume" && args[1] == "list" {
			return []byte("claude-home-hardened\ncodex-home\n"), nil
		}
		return nil, errors.New("unexpected")
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
	if err := os.WriteFile(filepath.Join(env.Checkout, "config", "runtime.json"),
		[]byte(`{"shared_state":{"id":"work","quota":"4G"}}`), 0o644); err != nil {
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
	// runtime.json is the default `{"shared_state": null}` from fakeEnv.
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

func installWrapper(t *testing.T, home, agent, body string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "fish", "functions")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, agent+".fish"), []byte("function "+agent+"\n  "+body+"\nend\n"), 0o644)
}
