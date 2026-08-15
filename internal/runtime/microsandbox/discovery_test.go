package microsandbox

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeListStub writes a stub `msb` script that returns `payload` for any
// `msb list ...` invocation and records the full argv to `recordFile`.
func writeListStub(t *testing.T, dir, payload string) (stub, recordFile string) {
	t.Helper()
	stub = filepath.Join(dir, "msb")
	recordFile = filepath.Join(dir, "record")
	script := `#!/bin/sh
printf '%s\n' "$*" > "$MSB_STUB_RECORD"
if [ "$1" = "list" ]; then
  cat <<'JSON'
` + payload + `
JSON
fi
exit 0
`
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return stub, recordFile
}

func TestFindCodexSandboxSingleMatch(t *testing.T) {
	payload := `[{"created_at":"2026-08-15T00:51:27Z","image":"ai-sandboxes-codex:local","name":"codex-abc","status":"Running"}]`
	stub, rec := writeListStub(t, t.TempDir(), payload)
	c := &Client{Msb: stub, Env: []string{"MSB_STUB_RECORD=" + rec}}

	sb, err := c.FindCodexSandbox("2d3837f6cd02")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sb.Name != "codex-abc" || sb.Image != "ai-sandboxes-codex:local" || sb.Status != "Running" {
		t.Errorf("sandbox = %+v", sb)
	}

	got, err := os.ReadFile(rec)
	if err != nil {
		t.Fatal(err)
	}
	argv := strings.TrimSpace(string(got))
	for _, want := range []string{
		"list", "--running", "--format", "json",
		"--label", "ai-sandbox.agent=codex",
		"--label", "ai-sandbox.workspace=2d3837f6cd02",
	} {
		if !strings.Contains(argv, want) {
			t.Errorf("argv %q missing %q", argv, want)
		}
	}
}

func TestFindCodexSandboxNoMatch(t *testing.T) {
	stub, rec := writeListStub(t, t.TempDir(), `[]`)
	c := &Client{Msb: stub, Env: []string{"MSB_STUB_RECORD=" + rec}}

	_, err := c.FindCodexSandbox("nope")
	if !errors.Is(err, ErrNoCodexSandbox) {
		t.Errorf("err = %v, want ErrNoCodexSandbox", err)
	}
}

func TestFindCodexSandboxAmbiguous(t *testing.T) {
	payload := `[
	  {"name":"codex-1","image":"ai-sandboxes-codex:local","status":"Running"},
	  {"name":"codex-2","image":"ai-sandboxes-codex:local","status":"Running"}
	]`
	stub, rec := writeListStub(t, t.TempDir(), payload)
	c := &Client{Msb: stub, Env: []string{"MSB_STUB_RECORD=" + rec}}

	_, err := c.FindCodexSandbox("2d3837f6cd02")
	if !errors.Is(err, ErrMultipleCodexSandboxes) {
		t.Errorf("err = %v, want ErrMultipleCodexSandboxes", err)
	}
}

func TestFindCodexSandboxMalformedJSON(t *testing.T) {
	stub, rec := writeListStub(t, t.TempDir(), `not json`)
	c := &Client{Msb: stub, Env: []string{"MSB_STUB_RECORD=" + rec}}

	_, err := c.FindCodexSandbox("2d3837f6cd02")
	if err == nil || !strings.Contains(err.Error(), "parse msb list output") {
		t.Errorf("err = %v, want parse error", err)
	}
}
