package plan

import (
	"reflect"
	"testing"
)

// These golden tests are the parity contract with the previous Fish
// launchers: they assert the exact resolved plan and the exact `msb run` argv
// for representative Claude and Codex invocations, without any Docker or
// Microsandbox dependency. Run them with `go test ./internal/plan`.

func TestClaudeMsbArgvGolden(t *testing.T) {
	p, err := Resolve(mustConfig(t, "claude"), resolveInput("claude", nil))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"run", "--tty", "--pull", "never", "--user", "node",
		"--cpus", "4", "--memory", "8G", "--root-disk", "10G", "--security", "restricted",
		"--no-net", "--net-rule", "allow@host:udp:53", "--net-rule", "allow@host:tcp:53",
		"--net-rule", "allow@api.anthropic.com:tcp:443", "--net-rule", "allow@github.com:tcp:443",
		"--mount-dir", "/Users/me/dev/my-project:/workspace/my-project-e85645dcb849:rw,quota=10G",
		"--mount-named", "claude-home-hardened:/home/node:kind=dir,quota=4G",
		"--workdir", "/workspace/my-project-e85645dcb849",
		"ai-sandboxes-claude:local",
		"--", "env",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"ENABLE_CLAUDEAI_MCP_SERVERS=false",
		"claude",
	}
	if !reflect.DeepEqual(p.MsbArgv(), want) {
		t.Errorf("claude argv mismatch\n got: %#v\nwant: %#v", p.MsbArgv(), want)
	}
}

func TestClaudeMsbArgvGoldenWithSharedState(t *testing.T) {
	shared, err := SharedStateFromLabels(map[string]string{
		"io.ai-sandboxes.shared-state.id":    "demo-profile",
		"io.ai-sandboxes.shared-state.quota": "2G",
	})
	if err != nil {
		t.Fatal(err)
	}
	p, err := Resolve(mustConfig(t, "claude"), resolveInput("claude", shared))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"run", "--tty", "--pull", "never", "--user", "node",
		"--cpus", "4", "--memory", "8G", "--root-disk", "10G", "--security", "restricted",
		"--no-net", "--net-rule", "allow@host:udp:53", "--net-rule", "allow@host:tcp:53",
		"--net-rule", "allow@api.anthropic.com:tcp:443", "--net-rule", "allow@github.com:tcp:443",
		"--mount-dir", "/Users/me/dev/my-project:/workspace/my-project-e85645dcb849:rw,quota=10G",
		"--mount-named", "claude-home-hardened:/home/node:kind=dir,quota=4G",
		"--mount-named", "agent-state-demo-profile-v1:/var/lib/agent-state:kind=dir,quota=2G",
		"--workdir", "/workspace/my-project-e85645dcb849",
		"ai-sandboxes-claude:local",
		"--", "env",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"ENABLE_CLAUDEAI_MCP_SERVERS=false",
		"claude",
	}
	if !reflect.DeepEqual(p.MsbArgv(), want) {
		t.Errorf("claude+shared-state argv mismatch\n got: %#v\nwant: %#v", p.MsbArgv(), want)
	}
}

func TestClaudeMsbArgvPublicEgress(t *testing.T) {
	in := resolveInput("claude", nil)
	in.Network = Network{Public: true}
	p, err := Resolve(mustConfig(t, "claude"), in)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"run", "--tty", "--pull", "never", "--user", "node",
		"--cpus", "4", "--memory", "8G", "--root-disk", "10G", "--security", "restricted",
		"--net", "public",
		"--mount-dir", "/Users/me/dev/my-project:/workspace/my-project-e85645dcb849:rw,quota=10G",
		"--mount-named", "claude-home-hardened:/home/node:kind=dir,quota=4G",
		"--workdir", "/workspace/my-project-e85645dcb849",
		"ai-sandboxes-claude:local",
		"--", "env",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"ENABLE_CLAUDEAI_MCP_SERVERS=false",
		"claude",
	}
	if !reflect.DeepEqual(p.MsbArgv(), want) {
		t.Errorf("claude public egress argv mismatch\n got: %#v\nwant: %#v", p.MsbArgv(), want)
	}
}

func TestClaudeForwardsAgentArgsVerbatim(t *testing.T) {
	in := resolveInput("claude", nil)
	in.AgentArgs = []string{"--dangerously-skip-permissions", "-p", "a prompt with spaces"}
	p, err := Resolve(mustConfig(t, "claude"), in)
	if err != nil {
		t.Fatal(err)
	}
	argv := p.MsbArgv()
	tail := argv[len(argv)-3:]
	want := []string{"claude", "--dangerously-skip-permissions", "-p", "a prompt with spaces"}
	if !reflect.DeepEqual(argv[len(argv)-len(want):], want) {
		t.Errorf("agent args not forwarded verbatim: got %#v", tail)
	}
}

func TestClaudeSessionMsbArgvGolden(t *testing.T) {
	shared, err := SharedStateFromLabels(map[string]string{
		"io.ai-sandboxes.shared-state.id":    "demo-profile",
		"io.ai-sandboxes.shared-state.quota": "2G",
	})
	if err != nil {
		t.Fatal(err)
	}
	in := resolveInput("claude", shared)
	in.ImageOverride = "ai-sandboxes-claude-session:sha-deadbeef"
	in.AgentArgs = []string{"--model", "sonnet"}
	p, err := Resolve(mustConfig(t, "claude"), in)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"run", "--tty", "--pull", "never", "--user", "node",
		"--cpus", "4", "--memory", "8G", "--root-disk", "10G", "--security", "restricted",
		"--no-net", "--net-rule", "allow@host:udp:53", "--net-rule", "allow@host:tcp:53",
		"--net-rule", "allow@api.anthropic.com:tcp:443", "--net-rule", "allow@github.com:tcp:443",
		"--mount-dir", "/Users/me/dev/my-project:/workspace/my-project-e85645dcb849:rw,quota=10G",
		"--mount-named", "claude-home-hardened:/home/node:kind=dir,quota=4G",
		"--mount-named", "agent-state-demo-profile-v1:/var/lib/agent-state:kind=dir,quota=2G",
		"--workdir", "/workspace/my-project-e85645dcb849",
		"ai-sandboxes-claude-session:sha-deadbeef",
		"--", "env",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"ENABLE_CLAUDEAI_MCP_SERVERS=false",
		"claude", "--model", "sonnet",
	}
	if !reflect.DeepEqual(p.MsbArgv(), want) {
		t.Errorf("claude-session argv mismatch\n got: %#v\nwant: %#v", p.MsbArgv(), want)
	}
}

func TestCodexMsbArgvGolden(t *testing.T) {
	p, err := Resolve(mustConfig(t, "codex"), resolveInput("codex", nil))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"run", "--tty", "--pull", "never", "--user", "node",
		"--root-disk", "20G", "--security", "restricted",
		"--no-net", "--net-rule", "allow@host:udp:53", "--net-rule", "allow@host:tcp:53",
		"--net-rule", "allow@api.openai.com:tcp:443", "--net-rule", "allow@github.com:tcp:443",
		"--mount-dir", "/Users/me/dev/my-project:/workspace/my-project-2d3837f6cd02:rw",
		"--mount-named", "codex-home:/home/node:rw",
		"--workdir", "/workspace/my-project-2d3837f6cd02",
		"ai-sandboxes-codex:local",
		"--", "codex",
	}
	if !reflect.DeepEqual(p.MsbArgv(), want) {
		t.Errorf("codex argv mismatch\n got: %#v\nwant: %#v", p.MsbArgv(), want)
	}
}

func TestCodexMsbArgvWithSharedStateAndArgs(t *testing.T) {
	shared, err := SharedStateFromLabels(map[string]string{
		"io.ai-sandboxes.shared-state.id":    "work",
		"io.ai-sandboxes.shared-state.quota": "4G",
	})
	if err != nil {
		t.Fatal(err)
	}
	in := resolveInput("codex", shared)
	in.AgentArgs = []string{"exec", "echo", "hi"}
	p, err := Resolve(mustConfig(t, "codex"), in)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"run", "--tty", "--pull", "never", "--user", "node",
		"--root-disk", "20G", "--security", "restricted",
		"--no-net", "--net-rule", "allow@host:udp:53", "--net-rule", "allow@host:tcp:53",
		"--net-rule", "allow@api.openai.com:tcp:443", "--net-rule", "allow@github.com:tcp:443",
		"--mount-dir", "/Users/me/dev/my-project:/workspace/my-project-2d3837f6cd02:rw",
		"--mount-named", "codex-home:/home/node:rw",
		"--mount-named", "agent-state-work-v1:/var/lib/agent-state:kind=dir,quota=4G",
		"--workdir", "/workspace/my-project-2d3837f6cd02",
		"ai-sandboxes-codex:local",
		"--", "codex", "exec", "echo", "hi",
	}
	if !reflect.DeepEqual(p.MsbArgv(), want) {
		t.Errorf("codex+shared-state argv mismatch\n got: %#v\nwant: %#v", p.MsbArgv(), want)
	}
}
