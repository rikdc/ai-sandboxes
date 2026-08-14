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
	os.WriteFile(filepath.Join(checkout, "versions.env"), []byte("WORKSPACE_QUOTA=20G\n"), 0o644)
	os.WriteFile(filepath.Join(checkout, "config", "runtime.json"), []byte(`{"shared_state": null}`), 0o644)

	if withEgress {
		dir := filepath.Join(home, ".config", "microvms")
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "claude-egress"), []byte("api.anthropic.com\ngithub.com\n"), 0o600)
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
			case name == "docker" && len(args) > 0 && args[0] == "image":
				return []byte(`[{}]`), nil
			case name == "msb" && len(args) > 0 && args[0] == "image" && args[1] == "list":
				return []byte("ai-sandboxes-claude:local\nai-sandboxes-codex:local\n"), nil
			case name == "msb" && len(args) > 0 && args[0] == "volume" && args[1] == "list":
				return []byte("claude-home-hardened\ncodex-home\n"), nil
			case name == "msb" && len(args) > 0 && args[0] == "image" && args[1] == "inspect":
				return []byte(imageInspect), nil
			}
			return nil, errors.New("unexpected command " + name + " " + strings.Join(args, " "))
		},
	}
	return &Env{Home: home, Checkout: checkout, Runner: runner}, home
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
	if s := checkStatus(checks, "ai-sandbox on PATH"); s != statusOK {
		t.Errorf("ai-sandbox on PATH = %s", s)
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

func TestDoctorDetectsSharedStateDrift(t *testing.T) {
	env, _ := fakeEnv(t, true, true)
	// Image labels carry a shared state that config/runtime.json (null) does
	// not request.
	env.Runner.Run = func(name string, args ...string) ([]byte, error) {
		if name == "msb" && len(args) > 0 && args[0] == "image" && args[1] == "inspect" {
			return []byte(`{"config":{"digest":"sha256:abc","Labels":{"io.ai-sandboxes.shared-state.id":"demo","io.ai-sandboxes.shared-state.quota":"2G"}}}`), nil
		}
		if name == "docker" && len(args) > 0 && args[0] == "image" {
			return []byte(`[{}]`), nil
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
	if s := checkStatus(checks, "shared state ai-sandboxes-claude:local"); s != statusFail {
		t.Errorf("shared state claude = %s, want fail", s)
	}
}

func installWrapper(t *testing.T, home, agent, body string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "fish", "functions")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, agent+".fish"), []byte("function "+agent+"\n  "+body+"\nend\n"), 0o644)
}
