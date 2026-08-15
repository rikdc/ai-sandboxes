package runtimepolicy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rikdc/ai-sandboxes/internal/plan"
)

func TestResolveRejectsRelativeOverride(t *testing.T) {
	if _, err := Resolve("", "runtime.json"); err == nil || !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("Resolve relative override error = %v, want absolute-path error", err)
	}
}

func TestResolveUsesAbsoluteOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.json")
	if err := os.WriteFile(path, []byte(`{"shared_state":{"id":"override","quota":"2G"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve("", path)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != "override" || got.Quota != "2G" {
		t.Fatalf("Resolve = %+v, want override:2G", got)
	}
}

func TestReconcileBaseImageRequiresExactSharedStateContract(t *testing.T) {
	desired, err := plan.ParseSharedStateRequest("work", "4G")
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{
		"io.ai-sandboxes.shared-state.id":    "client",
		"io.ai-sandboxes.shared-state.quota": "8G",
	}
	err = ReconcileBaseImage("sha256:abc", "sha256:abc", labels, desired)
	if err == nil || !strings.Contains(err.Error(), "rebuild") {
		t.Fatalf("ReconcileBaseImage error = %v, want contract mismatch", err)
	}
}

func TestReconcileBaseImageVerifiesDigestWhenPolicyIsNone(t *testing.T) {
	err := ReconcileBaseImage("sha256:docker", "sha256:msb", map[string]string{}, nil)
	if err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("ReconcileBaseImage error = %v, want digest mismatch", err)
	}
}
