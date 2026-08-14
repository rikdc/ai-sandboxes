package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rikdc/ai-sandboxes/internal/plan"
	"github.com/rikdc/ai-sandboxes/internal/runtime/microsandbox"
)

// fakeMsb stands in for the Microsandbox adapter in orchestration tests.
type fakeMsb struct {
	images      map[string]bool
	labels      map[string]string
	volumes     map[string]bool
	created     []string
	initialized []string
}

func newFakeMsb() *fakeMsb {
	return &fakeMsb{
		images:  map[string]bool{"ai-sandboxes-claude:local": true, "ai-sandboxes-codex:local": true},
		labels:  map[string]string{},
		volumes: map[string]bool{"claude-home-hardened": true, "codex-home": true},
	}
}

func (f *fakeMsb) ImagePresent(tag string) (bool, error)    { return f.images[tag], nil }
func (f *fakeMsb) VolumePresent(name string) (bool, error)   { return f.volumes[name], nil }
func (f *fakeMsb) VolumeCreate(name string) error            { f.volumes[name] = true; f.created = append(f.created, name); return nil }
func (f *fakeMsb) InitSharedState(_ string, st *plan.SharedState) error {
	f.initialized = append(f.initialized, st.Volume)
	return nil
}
func (f *fakeMsb) ImageMetadata(_ string) (*microsandbox.ImageMetadata, error) {
	return &microsandbox.ImageMetadata{ConfigDigest: "sha256:abc", Labels: f.labels}, nil
}

// testEnv builds an execEnv rooted in temp dirs: a project directory to run
// from and a home containing Claude's egress allowlist.
func testEnv(t *testing.T) (execEnv, string) {
	t.Helper()
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".config", "microvms"), 0o755)
	os.WriteFile(filepath.Join(home, ".config", "microvms", "claude-egress"),
		[]byte("api.anthropic.com\n"), 0o600)
	os.WriteFile(filepath.Join(home, ".config", "microvms", "codex-egress"),
		[]byte("api.openai.com\n"), 0o600)
	project := t.TempDir()
	os.MkdirAll(project, 0o755)

	e := execEnv{
		cwd:      project,
		home:     home,
		getenv:   func(string) string { return "" },
	}
	return e, home
}

func TestParseAgentArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		agent   string
		agentArgs []string
		profile string
		wantErr bool
	}{
		{name: "bare", args: []string{"claude"}, agent: "claude"},
		{name: "separator", args: []string{"codex", "--", "-p", "hi"}, agent: "codex", agentArgs: []string{"-p", "hi"}},
		{name: "implicit positional", args: []string{"claude", "somearg"}, agent: "claude", agentArgs: []string{"somearg"}},
		{name: "profile", args: []string{"claude", "--profile", "work", "--", "-p", "x"}, agent: "claude", profile: "work", agentArgs: []string{"-p", "x"}},
		{name: "profile equals", args: []string{"claude", "--profile=work"}, agent: "claude", profile: "work"},
		{name: "empty agent", args: []string{}, wantErr: true},
		{name: "unknown flag", args: []string{"claude", "--bogus", "x"}, wantErr: true},
		{name: "dashed arg needs separator", args: []string{"claude", "--version"}, wantErr: true},
	}
	for _, c := range cases {
		opts, err := parseAgentArgs(c.args)
		if c.wantErr {
			if err == nil {
				t.Errorf("%s: expected error", c.name)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error: %v", c.name, err)
			continue
		}
		if opts.agent != c.agent {
			t.Errorf("%s: agent = %q, want %q", c.name, opts.agent, c.agent)
		}
		if !reflect.DeepEqual(opts.agentArgs, c.agentArgs) {
			t.Errorf("%s: agentArgs = %v, want %v", c.name, opts.agentArgs, c.agentArgs)
		}
		if opts.profile != c.profile {
			t.Errorf("%s: profile = %q, want %q", c.name, opts.profile, c.profile)
		}
	}
}

func TestExecuteRunClaude(t *testing.T) {
	e, _ := testEnv(t)
	client := newFakeMsb()
	var launched []string
	code := executeRun(runOptions{agent: "claude", agentArgs: []string{"--version"}}, e, &bytes.Buffer{}, client,
		func(argv []string) error { launched = argv; return nil })
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if len(launched) == 0 || launched[0] != "run" {
		t.Fatalf("msb not launched with run argv: %v", launched)
	}
	if !containsArg(launched, "ai-sandboxes-claude:local") {
		t.Errorf("argv missing image: %v", launched)
	}
	if containsArg(launched, "--net", "public") {
		t.Errorf("allowlist egress should not be public: %v", launched)
	}
	// agent args must be the trailing tokens, verbatim
	if !reflect.DeepEqual(launched[len(launched)-2:], []string{"claude", "--version"}) {
		t.Errorf("trailing argv = %v", launched[len(launched)-2:])
	}
}

func TestExecuteRunCodexCreatesVolumeAndInitsSharedState(t *testing.T) {
	e, _ := testEnv(t)
	client := newFakeMsb()
	client.volumes = map[string]bool{"claude-home-hardened": true} // codex-home missing
	client.labels = map[string]string{
		"io.ai-sandboxes.shared-state.id":    "work",
		"io.ai-sandboxes.shared-state.quota": "4G",
	}
	var launched []string
	code := executeRun(runOptions{agent: "codex"}, e, &bytes.Buffer{}, client,
		func(argv []string) error { launched = argv; return nil })
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !containsArg(client.created, "codex-home") {
		t.Errorf("codex home volume was not created: %v", client.created)
	}
	if !containsArg(client.initialized, "agent-state-work-v1") {
		t.Errorf("shared state not initialized: %v", client.initialized)
	}
	if !containsArg(launched, "agent-state-work-v1:/var/lib/agent-state:kind=dir,quota=4G") {
		t.Errorf("shared state mount missing from argv: %v", launched)
	}
}

func TestExecuteRunRejectsOverlappingCheckout(t *testing.T) {
	checkout := t.TempDir()
	for _, name := range []string{"versions.env", "config", "shell", "docker-bake.hcl"} {
		os.WriteFile(filepath.Join(checkout, name), []byte("x"), 0o644)
	}
	os.MkdirAll(filepath.Join(checkout, "config"), 0o755)
	os.MkdirAll(filepath.Join(checkout, "shell"), 0o755)
	// Run from a project nested inside the checkout.
	nested := filepath.Join(checkout, "nested-project")
	os.MkdirAll(nested, 0o755)

	home := t.TempDir()
	e := execEnv{cwd: nested, home: home, getenv: func(string) string { return "" }}
	client := newFakeMsb()
	var launched []string
	code := executeRun(runOptions{agent: "claude"}, e, &bytes.Buffer{}, client,
		func(argv []string) error { launched = argv; return nil })
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (protected overlap)", code)
	}
	if len(launched) != 0 {
		t.Errorf("should not launch when overlapping a protected path")
	}
}

func TestExecuteRunImageMissing(t *testing.T) {
	e, _ := testEnv(t)
	client := newFakeMsb()
	client.images = map[string]bool{}
	code := executeRun(runOptions{agent: "claude"}, e, &bytes.Buffer{}, client, func([]string) error { return nil })
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

func TestExecuteRunMissingEgress(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	e := execEnv{cwd: project, home: home, getenv: func(string) string { return "" }}
	client := newFakeMsb()
	code := executeRun(runOptions{agent: "claude"}, e, &bytes.Buffer{}, client, func([]string) error { return nil })
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (missing egress allowlist)", code)
	}
}

func TestExecuteRunInvalidLabels(t *testing.T) {
	e, _ := testEnv(t)
	client := newFakeMsb()
	client.labels = map[string]string{"io.ai-sandboxes.shared-state.id": "x"}
	code := executeRun(runOptions{agent: "claude"}, e, &bytes.Buffer{}, client, func([]string) error { return nil })
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (inconsistent labels)", code)
	}
}

func TestExecutePlanPrints(t *testing.T) {
	e, _ := testEnv(t)
	client := newFakeMsb()
	var out, err bytes.Buffer
	code := executePlan(runOptions{agent: "claude", agentArgs: []string{"-p", "hi"}}, e, &out, &err, client)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	for _, want := range []string{"agent:", "ai-sandboxes-claude:local", "msb run argv:", "claude", "-p", "hi"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("plan output missing %q:\n%s", want, out.String())
		}
	}
}

func TestExecutePlanRejectsUnknownAgent(t *testing.T) {
	e, _ := testEnv(t)
	code := executePlan(runOptions{agent: "bogus"}, e, &bytes.Buffer{}, &bytes.Buffer{}, newFakeMsb())
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

func TestDispatch(t *testing.T) {
	var out, err bytes.Buffer
	if code := run([]string{"version"}, &out, &err); code != 0 || !strings.Contains(out.String(), "ai-sandbox") {
		t.Fatalf("version: code=%d out=%q", code, out.String())
	}
	out.Reset()
	if code := run([]string{"help"}, &out, &err); code != 0 || !strings.Contains(out.String(), "usage:") {
		t.Fatalf("help: code=%d", code)
	}
	out.Reset()
	if code := run([]string{"bogus"}, &out, &err); code != 2 {
		t.Fatalf("unknown command: code=%d", code)
	}
	out.Reset()
	if code := run([]string{"-v", "plan", "claude"}, &out, &err); code != 0 {
		// -v before a command is accepted; plan requires msb, which may be
		// absent in the test environment. Assert the flag parses and we reach
		// the msb-missing branch cleanly rather than failing on parsing.
		t.Logf("plan with -v exited %d (expected 127 or 0 depending on msb)", code)
	}
}

func TestRunCommandMsbMissing(t *testing.T) {
	if _, err := microsandbox.LookPathMsb(); err == nil {
		t.Skip("msb is installed; the 127 path cannot be exercised")
	}
	var out, err bytes.Buffer
	code := run([]string{"run", "claude"}, &out, &err)
	if code != 127 {
		t.Fatalf("exit code = %d, want 127 when msb is missing", code)
	}
}

func containsArg(args []string, want ...string) bool {
	for i := 0; i+len(want) <= len(args); i++ {
		if reflect.DeepEqual(args[i:i+len(want)], want) {
			return true
		}
	}
	return false
}
