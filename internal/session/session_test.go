package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveProfilePathName(t *testing.T) {
	home := t.TempDir()
	profiles := filepath.Join(home, ".config", "ai-sandboxes", "profiles")
	os.MkdirAll(profiles, 0o755)
	os.WriteFile(filepath.Join(profiles, "work.json"), []byte(`{"schema_version":1}`), 0o644)

	got, err := ResolveProfilePath(home, "work")
	if err != nil {
		t.Fatal(err)
	}
	if want := canonical(filepath.Join(profiles, "work.json")); got != want {
		t.Errorf("ResolveProfilePath(name) = %q, want %q", got, want)
	}
}

func TestResolveProfilePathLiteral(t *testing.T) {
	home := t.TempDir()
	prof := filepath.Join(t.TempDir(), "team", "session.json")
	os.MkdirAll(filepath.Dir(prof), 0o755)
	os.WriteFile(prof, []byte(`{"schema_version":1}`), 0o644)

	got, err := ResolveProfilePath(home, prof)
	if err != nil {
		t.Fatal(err)
	}
	if want := canonical(prof); got != want {
		t.Errorf("ResolveProfilePath(literal) = %q, want %q", got, want)
	}
}

func TestResolveProfilePathMissing(t *testing.T) {
	home := t.TempDir()
	if _, err := ResolveProfilePath(home, "work"); err == nil {
		t.Error("missing bare name should fail")
	}
	if _, err := ResolveProfilePath(home, "/no/such/profile.json"); err == nil {
		t.Error("missing literal path should fail")
	}
}

func TestResolveProfilePathDirectory(t *testing.T) {
	home := t.TempDir()
	_, err := ResolveProfilePath(home, t.TempDir())
	if err == nil {
		t.Fatal("a directory should not be accepted as a profile")
	}
	if !strings.Contains(err.Error(), "directory") {
		t.Errorf("directory error should say so: %v", err)
	}
}

func TestParseDescriptor(t *testing.T) {
	d, err := ParseDescriptor([]byte(`{"image":"ai-sandboxes-claude-session:sha-abc","shared_state":{"id":"demo","quota":"2G"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if d.Image != "ai-sandboxes-claude-session:sha-abc" || d.SharedState == nil {
		t.Fatalf("unexpected descriptor: %+v", d)
	}
	if d.SharedState.ID != "demo" || d.SharedState.Quota != "2G" {
		t.Errorf("unexpected shared state: %+v", d.SharedState)
	}

	if d, err := ParseDescriptor([]byte(`{"image":"ai-sandboxes-claude-session:sha-abc","shared_state":null}`)); err != nil {
		t.Fatal(err)
	} else if d.SharedState != nil {
		t.Errorf("shared_state null should decode to nil: %+v", d.SharedState)
	}
}

func TestParseDescriptorRejectsInvalid(t *testing.T) {
	cases := map[string]string{
		"empty output":         "",
		"not json":             "garbage",
		"no image":             `{"shared_state":null}`,
		"partial shared state": `{"image":"i","shared_state":{"id":"demo"}}`,
	}
	for name, in := range cases {
		if _, err := ParseDescriptor([]byte(in)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestResolverResolveLoadsAndVerifies(t *testing.T) {
	checkout := t.TempDir()
	for _, p := range []string{
		"scripts/session/resolve-image.sh",
		"scripts/session/load-image.sh",
	} {
		os.MkdirAll(filepath.Dir(filepath.Join(checkout, p)), 0o755)
		os.WriteFile(filepath.Join(checkout, p), []byte("#!/bin/sh\n"), 0o755)
	}
	home := t.TempDir()
	profiles := filepath.Join(home, ".config", "ai-sandboxes", "profiles")
	os.MkdirAll(profiles, 0o755)
	os.WriteFile(filepath.Join(profiles, "demo.json"), []byte(`{"schema_version":1}`), 0o644)

	r := &Resolver{Checkout: checkout, Home: home, Run: fakeRun(t, map[string]string{
		"resolve-image.sh": `{"image":"ai-sandboxes-claude-session:sha-abc","shared_state":{"id":"demo","quota":"2G"}}`,
		"load-image.sh":    "",
		"docker":           "sha256:deadbeef",
		"msb":              `{"config":{"digest":"sha256:deadbeef"}}`,
	})}

	d, err := r.Resolve(context.Background(), "demo", true)
	if err != nil {
		t.Fatal(err)
	}
	if d.Image != "ai-sandboxes-claude-session:sha-abc" {
		t.Fatalf("image = %q", d.Image)
	}
	if d.SharedState == nil || d.SharedState.ID != "demo" {
		t.Fatalf("shared state = %+v", d.SharedState)
	}
}

func TestResolverResolveSkipsLoadWhenNotLoading(t *testing.T) {
	checkout := t.TempDir()
	p := filepath.Join(checkout, "scripts", "session", "resolve-image.sh")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755)
	home := t.TempDir()
	profiles := filepath.Join(home, ".config", "ai-sandboxes", "profiles")
	os.MkdirAll(profiles, 0o755)
	os.WriteFile(filepath.Join(profiles, "demo.json"), []byte(`{"schema_version":1}`), 0o644)

	called := false
	r := &Resolver{Checkout: checkout, Home: home, Run: func(ctx context.Context, name string, _ ...string) ([]byte, error) {
		// load-image.sh, docker, and msb must not be invoked for a plan.
		base := filepath.Base(name)
		if base != "resolve-image.sh" {
			called = true
		}
		return []byte(`{"image":"ai-sandboxes-claude-session:sha-abc","shared_state":null}`), nil
	}}
	d, err := r.Resolve(context.Background(), "demo", false)
	if err != nil {
		t.Fatal(err)
	}
	if d == nil || d.Image == "" || called {
		t.Fatalf("plan resolution should not load or verify: d=%+v called=%v", d, called)
	}
}

func TestResolverResolveDigestMismatch(t *testing.T) {
	checkout := t.TempDir()
	for _, p := range []string{"scripts/session/resolve-image.sh", "scripts/session/load-image.sh"} {
		os.MkdirAll(filepath.Dir(filepath.Join(checkout, p)), 0o755)
		os.WriteFile(filepath.Join(checkout, p), []byte("#!/bin/sh\n"), 0o755)
	}
	home := t.TempDir()
	profiles := filepath.Join(home, ".config", "ai-sandboxes", "profiles")
	os.MkdirAll(profiles, 0o755)
	os.WriteFile(filepath.Join(profiles, "demo.json"), []byte(`{"schema_version":1}`), 0o644)

	r := &Resolver{Checkout: checkout, Home: home, Run: fakeRun(t, map[string]string{
		"resolve-image.sh": `{"image":"ai-sandboxes-claude-session:sha-abc","shared_state":null}`,
		"load-image.sh":    "",
		"docker":           "sha256:deadbeef",
		"msb":              `{"config":{"digest":"sha256:different"}}`,
	})}

	if _, err := r.Resolve(context.Background(), "demo", true); err == nil {
		t.Fatalf("digest mismatch should fail")
	} else if !strings.Contains(err.Error(), "does not match Docker image") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolverResolveDigestNormalized(t *testing.T) {
	// Digests compare case-insensitively and ignoring an optional sha256:
	// prefix, so a plausible msb-side format change (bare hex, uppercased)
	// cannot false-negative the identity check.
	r := resolverFixture(t, map[string]string{
		"resolve-image.sh": `{"image":"ai-sandboxes-claude-session:sha-abc","shared_state":null}`,
		"load-image.sh":    "",
		"docker":           "SHA256:DEADBEEF\n",
		"msb":              `{"config":{"digest":"deadbeef"}}`,
	})
	if _, err := r.Resolve(context.Background(), "demo", true); err != nil {
		t.Fatalf("normalized-equal digests should match: %v", err)
	}
}

func TestResolverResolveEmptyDigestFailsClosed(t *testing.T) {
	// An empty digest on either side must fail the identity check rather than
	// compare equal to an empty digest on the other side.
	for name, canned := range map[string]map[string]string{
		"docker empty": {
			"resolve-image.sh": `{"image":"ai-sandboxes-claude-session:sha-abc","shared_state":null}`,
			"load-image.sh":    "",
			"docker":           "",
			"msb":              `{"config":{"digest":"sha256:deadbeef"}}`,
		},
		"msb empty": {
			"resolve-image.sh": `{"image":"ai-sandboxes-claude-session:sha-abc","shared_state":null}`,
			"load-image.sh":    "",
			"docker":           "sha256:deadbeef",
			"msb":              `{"config":{"digest":""}}`,
		},
	} {
		if _, err := resolverFixture(t, canned).Resolve(context.Background(), "demo", true); err == nil {
			t.Errorf("%s: an empty digest should fail the identity check", name)
		}
	}
}

func resolverFixture(t *testing.T, canned map[string]string) *Resolver {
	t.Helper()
	checkout := t.TempDir()
	for _, p := range []string{"scripts/session/resolve-image.sh", "scripts/session/load-image.sh"} {
		os.MkdirAll(filepath.Dir(filepath.Join(checkout, p)), 0o755)
		os.WriteFile(filepath.Join(checkout, p), []byte("#!/bin/sh\n"), 0o755)
	}
	home := t.TempDir()
	profiles := filepath.Join(home, ".config", "ai-sandboxes", "profiles")
	os.MkdirAll(profiles, 0o755)
	os.WriteFile(filepath.Join(profiles, "demo.json"), []byte(`{"schema_version":1}`), 0o644)
	return &Resolver{Checkout: checkout, Home: home, Run: fakeRun(t, canned)}
}

func TestResolverResolveScriptFailure(t *testing.T) {
	checkout := t.TempDir()
	p := filepath.Join(checkout, "scripts", "session", "resolve-image.sh")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755)
	home := t.TempDir()
	profiles := filepath.Join(home, ".config", "ai-sandboxes", "profiles")
	os.MkdirAll(profiles, 0o755)
	os.WriteFile(filepath.Join(profiles, "demo.json"), []byte(`{"schema_version":1}`), 0o644)

	bootErr := errors.New("cache miss requires CLAUDE_MSB_BUILD_EGRESS=1")
	r := &Resolver{Checkout: checkout, Home: home, Run: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte("resolve-image: cache miss requires CLAUDE_MSB_BUILD_EGRESS=1"), bootErr
	}}
	_, err := r.Resolve(context.Background(), "demo", false)
	if err == nil {
		t.Fatal("resolver failure should propagate")
	}
	if !strings.Contains(err.Error(), bootErr.Error()) {
		t.Errorf("error should wrap the underlying failure: %v", err)
	}
}

func fakeRun(t *testing.T, canned map[string]string) func(context.Context, string, ...string) ([]byte, error) {
	t.Helper()
	return func(_ context.Context, name string, _ ...string) ([]byte, error) {
		base := filepath.Base(name)
		if base == "docker" || base == "msb" {
			if out, ok := canned[base]; ok {
				return []byte(out), nil
			}
		}
		for k, out := range canned {
			if strings.Contains(name, k) {
				return []byte(out), nil
			}
		}
		return []byte{}, errors.New("unexpected command: " + name)
	}
}
func canonical(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}
