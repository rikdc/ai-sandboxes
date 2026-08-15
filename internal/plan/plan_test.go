package plan

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"testing"

	"github.com/rikdc/ai-sandboxes/internal/config"
)

// resolveInput builds a Resolve input with a representative allowlist network
// for claude or a public network for codex, over values that mirror a project
// at /Users/me/dev/my-project.
func resolveInput(agent string, shared *SharedState) Input {
	in := Input{
		Agent:       agent,
		Workspace:   "/Users/me/dev/my-project",
		SharedState: shared,
	}
	if agent == "codex" {
		in.Network = Network{NoNet: true, Rules: []string{
			"allow@host:udp:53",
			"allow@host:tcp:53",
			"allow@api.openai.com:tcp:443",
			"allow@github.com:tcp:443",
		}}
	} else {
		in.Network = Network{NoNet: true, Rules: []string{
			"allow@host:udp:53",
			"allow@host:tcp:53",
			"allow@api.anthropic.com:tcp:443",
			"allow@github.com:tcp:443",
		}}
	}
	return in
}

func TestGuestWorkspaceMatchesLaunchers(t *testing.T) {
	cases := []struct {
		method, workspace, want string
	}{
		{"git-blob", "/Users/me/dev/my-project", "my-project-e85645dcb849"},
		{"sha256", "/Users/me/dev/my-project", "my-project-2d3837f6cd02"},
		{"git-blob", "/Users/a b/My_Project.v2", "My_Project.v2-880ab1c7a5df"},
	}
	for _, c := range cases {
		got, err := GuestWorkspace(c.method, c.workspace)
		if err != nil {
			t.Fatalf("GuestWorkspace(%q, %q): %v", c.method, c.workspace, err)
		}
		if got != "/workspace/"+c.want {
			t.Errorf("GuestWorkspace(%q, %q) = %q, want /workspace/%s", c.method, c.workspace, got, c.want)
		}
	}
}

// WorkspaceHash asserts the exact derived identities used by the previous fish
// launchers (git hash-object blob for claude, sha256 first-12 for codex).
func TestWorkspaceHash(t *testing.T) {
	got, err := WorkspaceHash("git-blob", "/Users/me/dev/my-project")
	if err != nil {
		t.Fatal(err)
	}
	if got != "e85645dcb849" {
		t.Errorf("git-blob hash = %q, want e85645dcb849", got)
	}
	got, err = WorkspaceHash("sha256", "/Users/me/dev/my-project")
	if err != nil {
		t.Fatal(err)
	}
	if got != "2d3837f6cd02" {
		t.Errorf("sha256 hash = %q, want 2d3837f6cd02", got)
	}
}

func TestWorkspaceHashMatchesShell(t *testing.T) {
	// Cross-check the git-blob derivation against the real `git hash-object`
	// the claude launcher used, skipping when git is unavailable.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	content := []byte("/Users/me/dev/my-project")
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(content))
	_, _ = h.Write(content)
	want := hex.EncodeToString(h.Sum(nil))[:12]
	if want != "e85645dcb849" {
		t.Fatalf("fixture hash drifted: %s", want)
	}
	cmd := exec.Command("git", "hash-object", "--stdin")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.Write(content); err != nil {
		t.Fatal(err)
	}
	_ = stdin.Close()
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out[:12]); got != want {
		t.Errorf("WorkspaceHash git-blob = %q, git hash-object = %q", want, got)
	}
}

func TestResolveClaudePlan(t *testing.T) {
	p, err := Resolve(mustConfig(t, "claude"), resolveInput("claude", nil))
	if err != nil {
		t.Fatal(err)
	}
	if p.Image != "ai-sandboxes-claude:local" {
		t.Errorf("image = %q", p.Image)
	}
	if p.WorkspaceGuest != "/workspace/my-project-e85645dcb849" {
		t.Errorf("guest = %q", p.WorkspaceGuest)
	}
	wantMount := "/Users/me/dev/my-project:/workspace/my-project-e85645dcb849:rw,quota=10G"
	if p.WorkspaceMount != wantMount {
		t.Errorf("workspace mount = %q, want %q", p.WorkspaceMount, wantMount)
	}
	if p.HomeMount != "claude-home-hardened:/home/node:kind=dir,quota=4G" {
		t.Errorf("home mount = %q", p.HomeMount)
	}
	if p.SharedState != nil {
		t.Errorf("shared state unexpectedly set: %+v", p.SharedState)
	}
	if p.Resources.CPUs != 4 || p.Resources.Memory != "8G" || p.Resources.RootDisk != "10G" {
		t.Errorf("resources = %+v", p.Resources)
	}
	if p.Security != "restricted" {
		t.Errorf("security = %q", p.Security)
	}
	if !reflect.DeepEqual(p.Environment, []string{"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "ENABLE_CLAUDEAI_MCP_SERVERS=false"}) {
		t.Errorf("environment = %v", p.Environment)
	}
}

func TestResolveCodexPlan(t *testing.T) {
	p, err := Resolve(mustConfig(t, "codex"), resolveInput("codex", nil))
	if err != nil {
		t.Fatal(err)
	}
	if p.WorkspaceGuest != "/workspace/my-project-2d3837f6cd02" {
		t.Errorf("guest = %q", p.WorkspaceGuest)
	}
	if p.WorkspaceMount != "/Users/me/dev/my-project:/workspace/my-project-2d3837f6cd02:rw,quota=20G" {
		t.Errorf("workspace mount = %q", p.WorkspaceMount)
	}
	if p.HomeMount != "codex-home:/home/node:kind=dir,quota=4G" {
		t.Errorf("home mount = %q", p.HomeMount)
	}
	if p.Network.Public || !p.Network.NoNet || len(p.Network.Rules) == 0 {
		t.Errorf("codex network should be deny-by-default allowlist: %+v", p.Network)
	}
	if p.Resources.RootDisk != "20G" {
		t.Errorf("codex root-disk = %q, want 20G", p.Resources.RootDisk)
	}
	if p.Resources.CPUs != 4 || p.Resources.Memory != "8G" {
		t.Errorf("codex resources = %+v", p.Resources)
	}
	if p.Security != "restricted" {
		t.Errorf("codex security = %q, want restricted", p.Security)
	}
	wantLabels := []string{
		"ai-sandbox.agent=codex",
		"ai-sandbox.workspace=2d3837f6cd02",
	}
	if !reflect.DeepEqual(p.Labels, wantLabels) {
		t.Errorf("codex labels = %v, want %v", p.Labels, wantLabels)
	}
}

func TestResolveClaudePlanHasNoLabels(t *testing.T) {
	p, err := Resolve(mustConfig(t, "claude"), resolveInput("claude", nil))
	if err != nil {
		t.Fatal(err)
	}
	if p.Labels != nil {
		t.Errorf("claude labels = %v, want nil (labels are codex-only for now)", p.Labels)
	}
}

func TestResolveSessionPlan(t *testing.T) {
	shared, err := ParseSharedStateRequest("demo-profile", "2G")
	if err != nil {
		t.Fatal(err)
	}
	in := resolveInput("claude", shared)
	in.ImageOverride = "ai-sandboxes-claude-session:sha-deadbeef"
	p, err := Resolve(mustConfig(t, "claude"), in)
	if err != nil {
		t.Fatal(err)
	}
	if p.Image != "ai-sandboxes-claude-session:sha-deadbeef" {
		t.Errorf("session image = %q", p.Image)
	}
	if p.Security != "restricted" {
		t.Errorf("session security = %q, want restricted", p.Security)
	}
	if p.SharedState == nil || p.SharedState.Mount != "agent-state-demo-profile-v1:/var/lib/agent-state:kind=dir,quota=2G" {
		t.Errorf("session shared state = %+v", p.SharedState)
	}
	if p.WorkspaceGuest != "/workspace/my-project-e85645dcb849" {
		t.Errorf("session guest workspace = %q", p.WorkspaceGuest)
	}
	if p.HomeMount != "claude-home-hardened:/home/node:kind=dir,quota=4G" {
		t.Errorf("session home mount = %q", p.HomeMount)
	}
}

func TestResolveSharedState(t *testing.T) {
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
	if p.SharedState == nil || p.SharedState.Mount != "agent-state-demo-profile-v1:/var/lib/agent-state:kind=dir,quota=2G" {
		t.Errorf("shared state = %+v", p.SharedState)
	}
}

func TestSharedStateFromLabels(t *testing.T) {
	cases := []struct {
		name   string
		labels map[string]string
		want   *SharedState
		wantErr bool
	}{
		{name: "absent", labels: nil, want: nil},
		{name: "empty values", labels: map[string]string{"io.ai-sandboxes.shared-state.id": "", "io.ai-sandboxes.shared-state.quota": ""}, want: nil},
		{name: "partial", labels: map[string]string{"io.ai-sandboxes.shared-state.id": "x"}, wantErr: true},
		{name: "invalid id", labels: map[string]string{"io.ai-sandboxes.shared-state.id": "BAD id", "io.ai-sandboxes.shared-state.quota": "2G"}, wantErr: true},
		{name: "invalid quota", labels: map[string]string{"io.ai-sandboxes.shared-state.id": "ok", "io.ai-sandboxes.shared-state.quota": "2"}, wantErr: true},
	}
	for _, c := range cases {
		got, err := SharedStateFromLabels(c.labels)
		if c.wantErr {
			if err == nil {
				t.Errorf("%s: expected error, got %+v", c.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.name, err)
			continue
		}
		if (got == nil) != (c.want == nil) {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestResolveNetwork(t *testing.T) {
	dir := t.TempDir()
	egress := dir + "/claude-egress"
	writeFile(t, egress, "# comment\napi.anthropic.com\n*.githubusercontent.com\n\n")

	net, err := ResolveNetwork(false, egress, []string{"allow@host:udp:53", "allow@host:tcp:53"})
	if err != nil {
		t.Fatal(err)
	}
	want := Network{NoNet: true, Rules: []string{
		"allow@host:udp:53",
		"allow@host:tcp:53",
		"allow@api.anthropic.com:tcp:443",
		"allow@*.githubusercontent.com:tcp:443",
	}}
	if !reflect.DeepEqual(net, want) {
		t.Errorf("network = %+v, want %+v", net, want)
	}

	net, err = ResolveNetwork(true, egress, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !net.Public || len(net.Rules) != 0 {
		t.Errorf("public network = %+v", net)
	}

	if _, err := ResolveNetwork(false, dir+"/missing", nil); err == nil {
		t.Error("expected an error for a missing egress file")
	}

	invalid := dir + "/invalid"
	writeFile(t, invalid, "bad host name\n")
	if _, err := ResolveNetwork(false, invalid, nil); err == nil {
		t.Error("expected an error for an invalid hostname")
	}
}

func TestValidateWorkspace(t *testing.T) {
	if err := ValidateWorkspace("", t.TempDir()); err == nil {
		t.Error("empty workspace should be rejected")
	}
	if err := ValidateWorkspace("/", t.TempDir()); err == nil {
		t.Error("/ should be rejected")
	}
	home := t.TempDir()
	if err := ValidateWorkspace(home, home); err == nil {
		t.Error("home directory workspace should be rejected")
	}
	if err := ValidateWorkspace(home+"/sub", home); err != nil {
		t.Errorf("a subdirectory of home is valid: %v", err)
	}
}

func TestRefuseOverlap(t *testing.T) {
	base := t.TempDir()
	children := func(name string) string {
		return base + "/" + name
	}
	checkout := children("checkout")
	mkdirAll(t, checkout)

	inside := children("checkout/sub")
	mkdirAll(t, inside)
	containing := children("proj")
	mkdirAll(t, containing)
	nested := children("proj/nested")
	mkdirAll(t, nested)

	ok1 := children("proj2")
	mkdirAll(t, ok1)
	ok2 := children("proj3")
	mkdirAll(t, ok2)
	mkdirAll(t, children("between"))

	roots := []string{checkout, ok2}

	cases := []struct {
		workspace string
		wantErr   bool
	}{
		{workspace: checkout, wantErr: true},            // equal to protected root
		{workspace: inside, wantErr: true},              // inside a protected root
		{workspace: containing, wantErr: false},         // contains nothing protected
		{workspace: nested, wantErr: false},             // unrelated
		{workspace: ok1, wantErr: false},                // no overlap
		{workspace: ok2, wantErr: true},                 // equal to the second root
	}
	for _, c := range cases {
		err := RefuseOverlap(c.workspace, roots)
		if c.wantErr && err == nil {
			t.Errorf("RefuseOverlap(%q) expected an error", c.workspace)
		}
		if !c.wantErr && err != nil {
			t.Errorf("RefuseOverlap(%q) unexpected error: %v", c.workspace, err)
		}
	}

	// Worktree/symlinked overlap: resolve a symlink that points inside a
	// protected root.
	link := children("link-to-checkout")
	if err := osSymlink(checkout, link); err != nil {
		t.Fatal(err)
	}
	if err := RefuseOverlap(link, []string{checkout}); err == nil {
		t.Error("a symlink resolving inside a protected root should be rejected")
	}

	// An unresolvable protected root fails closed.
	if err := RefuseOverlap(ok1, []string{children("missing-root")}); err == nil {
		t.Error("an unresolvable protected root must be rejected, not skipped")
	}
}

func TestParseSharedStateRequest(t *testing.T) {
	st, err := ParseSharedStateRequest("work", "4G")
	if err != nil {
		t.Fatal(err)
	}
	if st.Mount != "agent-state-work-v1:/var/lib/agent-state:kind=dir,quota=4G" {
		t.Errorf("mount = %q", st.Mount)
	}
	if _, err := ParseSharedStateRequest("bad id", "4G"); err == nil {
		t.Error("invalid id should be rejected")
	}
	none, err := ParseSharedStateRequest("", "")
	if err != nil || none != nil {
		t.Errorf("empty request should yield nil, got %+v err %v", none, err)
	}
}

func mustConfig(t *testing.T, name string) config.Agent {
	t.Helper()
	cfg, err := config.AgentConfig(name)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func osSymlink(oldname, newname string) error { return os.Symlink(oldname, newname) }