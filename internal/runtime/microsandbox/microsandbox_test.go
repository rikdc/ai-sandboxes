package microsandbox

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rikdc/ai-sandboxes/internal/plan"
)

func TestParseImageMetadata(t *testing.T) {
	data := []byte(`{
	  "config": {
	    "digest": "sha256:abc123",
	    "Labels": {
	      "io.ai-sandboxes.shared-state.id": "demo",
	      "io.ai-sandboxes.shared-state.quota": "2G",
	      "other": "value"
	    }
	  }
	}`)
	meta, err := ParseImageMetadata(data)
	if err != nil {
		t.Fatal(err)
	}
	if meta.ConfigDigest != "sha256:abc123" {
		t.Errorf("digest = %q", meta.ConfigDigest)
	}
	want := map[string]string{
		"io.ai-sandboxes.shared-state.id":    "demo",
		"io.ai-sandboxes.shared-state.quota": "2G",
		"other":                              "value",
	}
	if !reflect.DeepEqual(meta.Labels, want) {
		t.Errorf("labels = %v, want %v", meta.Labels, want)
	}
}

func TestParseImageMetadataNoLabels(t *testing.T) {
	meta, err := ParseImageMetadata([]byte(`{"config": {"digest": "sha256:x"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Labels) != 0 {
		t.Errorf("expected no labels, got %v", meta.Labels)
	}
	// Missing config entirely is tolerated.
	meta, err = ParseImageMetadata([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Labels) != 0 {
		t.Errorf("expected no labels, got %v", meta.Labels)
	}
}

func TestParseImageMetadataInvalidJSON(t *testing.T) {
	if _, err := ParseImageMetadata([]byte(`not json`)); err == nil {
		t.Error("invalid JSON should error")
	}
}

// TestClientCommands drives a real msb CLI invocation shape through a stub
// binary, mirroring how the fish tests stubbed `msb` on PATH. This asserts the
// exact subcommands the adapter issues without needing Microsandbox.
func TestClientCommands(t *testing.T) {
	stub := filepath.Join(t.TempDir(), "msb")
	recordFile := filepath.Join(t.TempDir(), "record")
	cannedImages := "ai-sandboxes-claude:local\nai-sandboxes-codex:local\n"
	cannedVolumes := "claude-home-hardened\n"
	cannedInspect := `{"config":{"digest":"sha256:abc","Labels":{"io.ai-sandboxes.shared-state.id":"demo","io.ai-sandboxes.shared-state.quota":"2G"}}}`

	stubScript := `#!/bin/sh
printf '%s\n' "$*" > "$MSB_STUB_RECORD"
case "$1 $2" in
  "image list") printf '%s' '` + cannedImages + `' ;;
  "volume list") printf '%s' '` + cannedVolumes + `' ;;
  "image inspect") printf '%s' '` + cannedInspect + `' ;;
esac
exit 0
`
	if err := os.WriteFile(stub, []byte(stubScript), 0o755); err != nil {
		t.Fatal(err)
	}

	c := &Client{Msb: stub, Env: []string{"MSB_STUB_RECORD=" + recordFile}}

	present, err := c.ImagePresent("ai-sandboxes-claude:local")
	if err != nil || !present {
		t.Fatalf("ImagePresent(claude) = %v, %v", present, err)
	}
	if present, err = c.ImagePresent("missing:local"); err != nil || present {
		t.Fatalf("ImagePresent(missing) = %v, %v", present, err)
	}

	meta, err := c.ImageMetadata("ai-sandboxes-claude:local")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Labels["io.ai-sandboxes.shared-state.id"] != "demo" {
		t.Errorf("labels = %v", meta.Labels)
	}
	if meta.ConfigDigest != "sha256:abc" {
		t.Errorf("digest = %q", meta.ConfigDigest)
	}

	if ok, err := c.VolumePresent("claude-home-hardened"); err != nil || !ok {
		t.Fatalf("VolumePresent = %v, %v", ok, err)
	}
	if ok, err := c.VolumePresent("codex-home"); err != nil || ok {
		t.Fatalf("VolumePresent(codex-home) = %v, %v", ok, err)
	}

	if err := c.VolumeCreate("codex-home"); err != nil {
		t.Fatal(err)
	}

	rec, err := os.ReadFile(recordFile)
	if err != nil {
		t.Fatal(err)
	}
	last := strings.TrimSpace(string(rec))
	if !strings.Contains(last, "volume create codex-home") {
		t.Errorf("volume create argv = %q", last)
	}
}

func TestMatchDigests(t *testing.T) {
	cases := []struct {
		docker  string
		msb     string
		wantErr bool
	}{
		{"sha256:abc", "sha256:abc", false},
		{"sha256:abc", "abc", false},
		{"ABC", "sha256:abc", false},
		{"sha256:abc", "sha256:different", true},
		{"", "sha256:abc", true},
		{"sha256:abc", "", true},
	}
	for _, c := range cases {
		err := MatchDigests(c.docker, c.msb)
		if c.wantErr {
			if err == nil {
				t.Errorf("MatchDigests(%q, %q) should error", c.docker, c.msb)
			}
			continue
		}
		if err != nil {
			t.Errorf("MatchDigests(%q, %q) unexpected error: %v", c.docker, c.msb, err)
		}
	}
}

func TestInitSharedStateArgv(t *testing.T) {
	stub := filepath.Join(t.TempDir(), "msb")
	recordFile := filepath.Join(t.TempDir(), "record")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$MSB_STUB_RECORD\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := &Client{Msb: stub, Env: []string{"MSB_STUB_RECORD=" + recordFile}}
	shared := &plan.SharedState{Volume: "agent-state-demo-v1", Mount: "agent-state-demo-v1:/var/lib/agent-state:kind=dir,quota=2G"}
	if err := c.InitSharedState("ai-sandboxes-claude:local", shared); err != nil {
		t.Fatal(err)
	}
	rec, err := os.ReadFile(recordFile)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(string(rec))
	want := "run --pull never --no-tty --no-net --security restricted --user root --mount-named agent-state-demo-v1:/var/lib/agent-state:kind=dir,quota=2G ai-sandboxes-claude:local -- install -d -o node -g node -m 0700 /var/lib/agent-state"
	if got != want {
		t.Errorf("init argv\n got: %s\nwant: %s", got, want)
	}
}
