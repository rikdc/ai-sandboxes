package config

import (
	"strings"
	"testing"
)

func TestParseRuntimeNull(t *testing.T) {
	rt, err := ParseRuntime([]byte(`{"shared_state": null}`))
	if err != nil {
		t.Fatal(err)
	}
	if rt.SharedState != nil {
		t.Errorf("shared_state should be nil, got %+v", rt.SharedState)
	}
}

func TestParseRuntimeSharedState(t *testing.T) {
	rt, err := ParseRuntime([]byte(`{"shared_state": {"id": "demo", "quota": "2G"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if rt.SharedState == nil || rt.SharedState.ID != "demo" || rt.SharedState.Quota != "2G" {
		t.Errorf("unexpected shared_state: %+v", rt.SharedState)
	}
}

func TestParseRuntimeRejectsInvalid(t *testing.T) {
	cases := []string{
		`{"shared_state": {"id": "demo"}}`,
		`{"shared_state": {"id": "demo", "quota": "2"}}`,
		`{"shared_state": {"id": "BAD id", "quota": "2G"}}`,
		`{"shared_state": "not-an-object"}`,
		`{"extra": 1, "shared_state": null}`,
		`not json`,
	}
	for _, c := range cases {
		if _, err := ParseRuntime([]byte(c)); err == nil {
			t.Errorf("ParseRuntime(%q) should fail", c)
		}
	}
}

func TestParseVersions(t *testing.T) {
	data := strings.Join([]string{
		"NODE_IMAGE=node:22-trixie@sha256:abc",
		"WORKSPACE_QUOTA=20G",
		"CODEX_VERSION=0.147.0",
	}, "\n")
	v, err := ParseVersions([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if v.WorkspaceQuota != "20G" {
		t.Errorf("WorkspaceQuota = %q, want 20G", v.WorkspaceQuota)
	}
}

func TestParseVersionsRejectsInvalidQuota(t *testing.T) {
	if _, err := ParseVersions([]byte("WORKSPACE_QUOTA=20GB\n")); err == nil {
		t.Error("invalid quota should be rejected")
	}
	if _, err := ParseVersions([]byte("WORKSPACE_QUOTA=0G\n")); err == nil {
		t.Error("zero quota should be rejected")
	}
}

func TestAgentConfig(t *testing.T) {
	cfg, err := AgentConfig("claude")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Image != "ai-sandboxes-claude:local" || cfg.Security != "restricted" || cfg.CPUs != 4 {
		t.Errorf("unexpected claude policy: %+v", cfg)
	}
	cfg, err = AgentConfig("codex")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Net != "" || cfg.CreateHomeVolume != true || cfg.RootDiskFromVersions != true {
		t.Errorf("unexpected codex policy: %+v", cfg)
	}
	if cfg.Security != "restricted" {
		t.Errorf("codex security = %q, want restricted", cfg.Security)
	}
	if len(cfg.BaseNetRules) == 0 {
		t.Errorf("codex should have deny-by-default base net rules: %+v", cfg.BaseNetRules)
	}
	if _, err := AgentConfig("nope"); err == nil {
		t.Error("unknown agent should error")
	}
}
