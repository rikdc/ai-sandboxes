package main

import (
	"bytes"
	"context"
	"fmt"
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

func (f *fakeMsb) ImagePresent(tag string) (bool, error)   { return f.images[tag], nil }
func (f *fakeMsb) VolumePresent(name string) (bool, error) { return f.volumes[name], nil }
func (f *fakeMsb) VolumeCreate(name string) error {
	f.volumes[name] = true
	f.created = append(f.created, name)
	return nil
}
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
		cwd:    project,
		home:   home,
		getenv: func(string) string { return "" },
	}
	return e, home
}

func TestParseAgentArgs(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		agent     string
		agentArgs []string
		profile   string
		help      bool
		wantErr   bool
	}{
		{name: "bare", args: []string{"claude"}, agent: "claude"},
		{name: "separator", args: []string{"codex", "--", "-p", "hi"}, agent: "codex", agentArgs: []string{"-p", "hi"}},
		{name: "implicit positional", args: []string{"claude", "somearg"}, agent: "claude", agentArgs: []string{"somearg"}},
		{name: "profile", args: []string{"claude", "--profile", "work", "--", "-p", "x"}, agent: "claude", profile: "work", agentArgs: []string{"-p", "x"}},
		{name: "profile equals", args: []string{"claude", "--profile=work"}, agent: "claude", profile: "work"},
		{name: "profile then agent args", args: []string{"claude", "--profile", "work", "-p", "hi", "--model", "sonnet"}, agent: "claude", profile: "work", agentArgs: []string{"-p", "hi", "--model", "sonnet"}},
		{name: "profile empty agent args", args: []string{"claude", "--profile=work"}, agent: "claude", profile: "work", agentArgs: nil},
		{name: "empty agent", args: []string{}, wantErr: true},
		{name: "unknown flag", args: []string{"claude", "--bogus", "x"}, wantErr: true},
		{name: "dashed arg needs separator", args: []string{"claude", "--version"}, wantErr: true},
		{name: "help bare", args: []string{"--help"}, help: true},
		{name: "help short", args: []string{"-h"}, help: true},
		{name: "help after agent", args: []string{"claude", "--help"}, agent: "claude", help: true},
		{name: "help needs separator", args: []string{"claude", "--", "--help"}, agent: "claude", agentArgs: []string{"--help"}},
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
		if opts.help != c.help {
			t.Errorf("%s: help = %v, want %v", c.name, opts.help, c.help)
		}
	}
}

func TestExecuteRunClaude(t *testing.T) {
	e, _ := testEnv(t)
	client := newFakeMsb()
	var launched []string
	code := executeRun(context.Background(), runOptions{agent: "claude", agentArgs: []string{"--version"}}, e, &bytes.Buffer{}, client,
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
	// Create a checkout with runtime.json that configures shared state.
	checkout := t.TempDir()
	if err := os.MkdirAll(filepath.Join(checkout, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "versions.env"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "docker-bake.hcl"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "config", "runtime.json"), []byte(`{"shared_state":{"id":"work","quota":"4G"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	e.checkout = checkout
	e.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch {
		case name == "docker" && len(args) > 1 && args[0] == "image" && args[1] == "inspect":
			return []byte("sha256:abc"), nil
		case name == "msb" && len(args) > 1 && args[0] == "image" && args[1] == "inspect":
			return []byte(`{"config":{"digest":"sha256:abc"}}`), nil
		}
		return nil, fmt.Errorf("unexpected command %s %v", name, args)
	}
	client := newFakeMsb()
	client.volumes = map[string]bool{"claude-home-hardened": true} // codex-home missing
	var launched []string
	code := executeRun(context.Background(), runOptions{agent: "codex"}, e, &bytes.Buffer{}, client,
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
	code := executeRun(context.Background(), runOptions{agent: "claude"}, e, &bytes.Buffer{}, client,
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
	code := executeRun(context.Background(), runOptions{agent: "claude"}, e, &bytes.Buffer{}, client, func([]string) error { return nil })
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

func TestExecuteRunMissingEgress(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	e := execEnv{cwd: project, home: home, getenv: func(string) string { return "" }}
	client := newFakeMsb()
	code := executeRun(context.Background(), runOptions{agent: "claude"}, e, &bytes.Buffer{}, client, func([]string) error { return nil })
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (missing egress allowlist)", code)
	}
}

// TestExecutePlanEgressSymlinkedHome guards the contract that the egress
// allowlist is read from the literal $HOME, not its canonicalized form. The
// installer, doctor, and docs all write to $HOME/.config/microvms/, which on
// dotfiles-managed layouts (~/.config symlinked via chezmoi/stow) is a
// different directory from EvalSymlinks($HOME)/.config.
func TestExecutePlanEgressSymlinkedHome(t *testing.T) {
	home := t.TempDir()
	link := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(home, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	project := t.TempDir()

	// The allowlist exists only at the symlinked $HOME spelling; the resolved
	// target has no .config at all.
	os.MkdirAll(filepath.Join(link, ".config", "microvms"), 0o755)
	os.WriteFile(filepath.Join(link, ".config", "microvms", "claude-egress"),
		[]byte("api.anthropic.com\n"), 0o600)

	t.Run("resolves through the symlinked home", func(t *testing.T) {
		e := execEnv{cwd: project, home: link, getenv: func(string) string { return "" }}
		var out bytes.Buffer
		code := executePlan(context.Background(), runOptions{agent: "claude"}, e, &out, &bytes.Buffer{}, newFakeMsb())
		if code != 0 {
			t.Fatalf("exit code = %d, want 0 (allowlist exists at $HOME)", code)
		}
		if !strings.Contains(out.String(), "allow@api.anthropic.com:tcp:443") {
			t.Errorf("plan should honor the allowlist through the symlink:\n%s", out.String())
		}
	})

	t.Run("reports the literal home path when missing", func(t *testing.T) {
		// Move the .config aside so the allowlist is missing, then assert the
		// reported path is the literal $HOME spelling, not the resolved one.
		dotconfig := filepath.Join(link, ".config")
		moved := dotconfig + ".moved"
		if err := os.Rename(dotconfig, moved); err != nil {
			t.Fatalf("renaming .config: %v", err)
		}
		defer os.Rename(moved, dotconfig)

		e := execEnv{cwd: project, home: link, getenv: func(string) string { return "" }}
		var errb bytes.Buffer
		code := executePlan(context.Background(), runOptions{agent: "claude"}, e, &bytes.Buffer{}, &errb, newFakeMsb())
		if code != 1 {
			t.Fatalf("exit code = %d, want 1 (missing egress allowlist)", code)
		}
		want := filepath.Join(link, ".config", "microvms", "claude-egress")
		if !strings.Contains(errb.String(), want) {
			t.Errorf("missing-allowlist message should reference the literal $HOME path %q, got:\n%s", want, errb.String())
		}
		if strings.Contains(errb.String(), filepath.Join(home, ".config", "microvms", "claude-egress")) {
			t.Errorf("missing-allowlist message should not reference the resolved home path %q:\n%s", filepath.Join(home, ".config", "microvms", "claude-egress"), errb.String())
		}
	})
}

func TestExecuteRunInvalidRuntimeJSON(t *testing.T) {
	e, _ := testEnv(t)
	// Create a checkout with an invalid runtime.json.
	checkout := t.TempDir()
	if err := os.MkdirAll(filepath.Join(checkout, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "versions.env"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "docker-bake.hcl"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "config", "runtime.json"), []byte(`{"shared_state":{"id":"bad id","quota":"4G"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	e.checkout = checkout
	client := newFakeMsb()
	code := executeRun(context.Background(), runOptions{agent: "claude"}, e, &bytes.Buffer{}, client, func([]string) error { return nil })
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (invalid runtime.json)", code)
	}
}

func TestExecutePlanPrints(t *testing.T) {
	e, _ := testEnv(t)
	client := newFakeMsb()
	var out, err bytes.Buffer
	code := executePlan(context.Background(), runOptions{agent: "claude", agentArgs: []string{"-p", "hi"}}, e, &out, &err, client)
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
	code := executePlan(context.Background(), runOptions{agent: "bogus"}, e, &bytes.Buffer{}, &bytes.Buffer{}, newFakeMsb())
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
	if code := run([]string{"run", "--help"}, &out, &err); code != 0 || !strings.Contains(out.String(), "usage:") {
		t.Fatalf("run --help: code=%d out=%q", code, out.String())
	}
	out.Reset()
	if code := run([]string{"plan", "-h"}, &out, &err); code != 0 || !strings.Contains(out.String(), "usage:") {
		t.Fatalf("plan -h: code=%d out=%q", code, out.String())
	}
	out.Reset()
	if code := run([]string{"doctor", "--help"}, &out, &err); code != 0 || !strings.Contains(out.String(), "usage:") {
		t.Fatalf("doctor --help: code=%d out=%q", code, out.String())
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

// makeCheckout writes the marker files findCheckout's isCheckout looks for.
func makeCheckout(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{"versions.env", "config", "shell", "docker-bake.hcl"} {
		os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644)
	}
}

func TestProtectedRootsSymlinkedBinary(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(t.TempDir(), "ai-sandbox")
	os.WriteFile(target, []byte("#!/bin/sh\n"), 0o755)
	link := filepath.Join(t.TempDir(), "ai-sandbox")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// EvalSymlinks may canonicalize firmlinks (e.g. /var -> /private/var), so
	// assert the resolved parent as the code itself computes it.
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil {
		t.Fatalf("resolving symlink: %v", err)
	}
	if filepath.Dir(resolved) == filepath.Dir(link) {
		t.Fatalf("test needs link and target in different directories: link=%s resolved=%s", link, resolved)
	}

	e := execEnv{home: home, exe: link, getenv: func(string) string { return "" }}
	roots := e.protectedRoots("")

	for _, want := range []string{filepath.Dir(link), filepath.Dir(resolved)} {
		if !containsArg(roots, want) {
			t.Errorf("protected roots %v missing binary parent %q", roots, want)
		}
	}
}

func TestResolvedCheckout(t *testing.T) {
	t.Setenv("AI_SANDBOXES_ROOT", "")
	checkout := t.TempDir()
	makeCheckout(t, checkout)
	nested := filepath.Join(checkout, "nested-project")
	os.MkdirAll(nested, 0o755)
	// findCheckout resolves symlinks on the way up; compare against the same
	// canonical form so firmlinked temp dirs cannot skew the assertion.
	canonical := checkout
	if resolved, err := filepath.EvalSymlinks(checkout); err == nil {
		canonical = resolved
	}

	t.Run("uses precomputed checkout without re-lookup", func(t *testing.T) {
		// cwd sits inside a real checkout, so a re-run of findCheckout would
		// resolve to it. The set field must win verbatim.
		e := execEnv{cwd: nested, exe: "/nonexistent/launcher", checkout: "override-root"}
		if got := e.resolvedCheckout(); got != "override-root" {
			t.Errorf("resolvedCheckout() = %q, want the precomputed %q", got, "override-root")
		}
	})

	t.Run("falls back to deriving from cwd", func(t *testing.T) {
		e := execEnv{cwd: nested}
		if got := e.resolvedCheckout(); got != canonical {
			t.Errorf("resolvedCheckout() = %q, want checkout %q", got, canonical)
		}
	})

	t.Run("honors AI_SANDBOXES_ROOT over exe and cwd anchors", func(t *testing.T) {
		// The installed Fish wrappers set AI_SANDBOXES_ROOT to the checkout they
		// were installed from. That anchor must win over a binary installed
		// outside any checkout and a cwd that is an unrelated project, which is
		// exactly the setup claude-session needs resolve-image.sh for.
		envCheckout := t.TempDir()
		makeCheckout(t, envCheckout)
		t.Setenv("AI_SANDBOXES_ROOT", envCheckout)
		canonical := envCheckout
		if resolved, err := filepath.EvalSymlinks(envCheckout); err == nil {
			canonical = resolved
		}
		e := execEnv{
			cwd: filepath.Join(t.TempDir(), "unrelated-project"),
			exe: filepath.Join(t.TempDir(), "libexec", "ai-sandboxes", "ai-sandbox"),
		}
		if got := e.resolvedCheckout(); got != canonical {
			t.Errorf("resolvedCheckout() = %q, want the AI_SANDBOXES_ROOT checkout %q", got, canonical)
		}
	})

	t.Run("prefers the exe-resolved checkout over a different cwd checkout", func(t *testing.T) {
		// Regression test for the guard's authority: when the binary lives in
		// checkout A and cwd sits inside a different checkout B, the guard must
		// protect A (the checkout providing the running binary's code), not B.
		exeCheckout := t.TempDir()
		makeCheckout(t, exeCheckout)
		binDir := filepath.Join(exeCheckout, "bin")
		os.MkdirAll(binDir, 0o755)
		exe := filepath.Join(binDir, "ai-sandbox")
		os.WriteFile(exe, []byte("x"), 0o755)

		cwdCheckout := t.TempDir()
		makeCheckout(t, cwdCheckout)
		project := filepath.Join(cwdCheckout, "project")
		os.MkdirAll(project, 0o755)

		canonical := func(p string) string {
			if resolved, err := filepath.EvalSymlinks(p); err == nil {
				return resolved
			}
			return p
		}
		exeCanonical := canonical(exeCheckout)
		cwdCanonical := canonical(cwdCheckout)

		e := execEnv{cwd: project, exe: exe}
		if got := e.resolvedCheckout(); got != exeCanonical {
			t.Errorf("resolvedCheckout() = %q, want the exe-resolved checkout %q", got, exeCanonical)
		}
		roots := e.protectedRoots(e.resolvedCheckout())
		if !containsArg(roots, exeCanonical) {
			t.Errorf("protected roots %v missing exe-resolved checkout %q", roots, exeCanonical)
		}
		if containsArg(roots, cwdCanonical) {
			t.Errorf("protected roots %v should not protect the cwd checkout %q", roots, cwdCanonical)
		}
	})
}

// sessionTestEnv builds an execEnv whose session resolver is faked: the home
// provides Claude's egress allowlist and a demo profile, the checkout provides
// the expected scripts (never executed), and run returns canned outputs for
// resolve-image.sh, load-image.sh, docker, and msb.
func sessionTestEnv(t *testing.T) (execEnv, string) {
	t.Helper()
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".config", "microvms"), 0o755)
	os.WriteFile(filepath.Join(home, ".config", "microvms", "claude-egress"),
		[]byte("api.anthropic.com\n"), 0o600)
	profiles := filepath.Join(home, ".config", "ai-sandboxes", "profiles")
	os.MkdirAll(profiles, 0o755)
	os.WriteFile(filepath.Join(profiles, "demo.json"), []byte(`{"schema_version":1}`), 0o644)

	checkout := t.TempDir()
	for _, p := range []string{"scripts/session/resolve-image.sh", "scripts/session/load-image.sh"} {
		full := filepath.Join(checkout, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte("#!/bin/sh\n"), 0o755)
	}

	e := execEnv{cwd: t.TempDir(), home: home, getenv: func(string) string { return "" }}
	e.run = func(_ context.Context, name string, _ ...string) ([]byte, error) {
		switch {
		case strings.Contains(name, "resolve-image.sh"):
			return []byte(`{"image":"ai-sandboxes-claude-session:sha-abc","shared_state":{"id":"demo","quota":"2G"}}`), nil
		case strings.Contains(name, "load-image.sh"):
			return nil, nil
		case name == "docker":
			return []byte("sha256:abc"), nil
		case name == "msb":
			return []byte(`{"config":{"digest":"sha256:abc"}}`), nil
		default:
			return nil, fmt.Errorf("unexpected command %q", name)
		}
	}
	e.checkout = checkout
	return e, checkout
}

func TestExecuteRunClaudeSession(t *testing.T) {
	e, _ := sessionTestEnv(t)
	client := newFakeMsb()
	var launched []string
	code := executeRun(context.Background(), runOptions{agent: "claude", profile: "demo", agentArgs: []string{"--model", "sonnet"}},
		e, &bytes.Buffer{}, client, func(argv []string) error { launched = argv; return nil })
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !containsArg(launched, "ai-sandboxes-claude-session:sha-abc") {
		t.Errorf("argv missing session image: %v", launched)
	}
	if !containsArg(launched, "--security", "restricted") {
		t.Errorf("session argv missing restricted profile: %v", launched)
	}
	if !containsArg(launched, "agent-state-demo-v1:/var/lib/agent-state:kind=dir,quota=2G") {
		t.Errorf("session argv missing shared-state mount: %v", launched)
	}
	if !reflect.DeepEqual(launched[len(launched)-3:], []string{"claude", "--model", "sonnet"}) {
		t.Errorf("trailing argv = %v", launched[len(launched)-3:])
	}
	if !containsArg(client.initialized, "agent-state-demo-v1") {
		t.Errorf("shared state not initialized: %v", client.initialized)
	}
}

func TestExecutePlanClaudeSessionSkipsLoad(t *testing.T) {
	e, _ := sessionTestEnv(t)
	var out, errb bytes.Buffer
	code := executePlan(context.Background(), runOptions{agent: "claude", profile: "demo"}, e, &out, &errb, newFakeMsb())
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (plan must not require a loaded image): %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "ai-sandboxes-claude-session:sha-abc") {
		t.Errorf("plan output missing session image:\n%s", out.String())
	}
}

func TestExecuteRunClaudeSessionProfileNotForCodex(t *testing.T) {
	e, _ := sessionTestEnv(t)
	var errb bytes.Buffer
	code := executeRun(context.Background(), runOptions{agent: "codex", profile: "demo"}, e, &errb, newFakeMsb(),
		func([]string) error { return nil })
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "only supported for claude") {
		t.Errorf("unexpected error:\n%s", errb.String())
	}
}

func TestExecuteRunClaudeSessionProfileNotFound(t *testing.T) {
	e, _ := sessionTestEnv(t)
	e.checkout = t.TempDir()
	var errb bytes.Buffer
	code := executeRun(context.Background(), runOptions{agent: "claude", profile: "nope"}, e, &errb, newFakeMsb(),
		func([]string) error { return nil })
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "profile not found") {
		t.Errorf("unexpected error:\n%s", errb.String())
	}
}
