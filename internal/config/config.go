// Package config holds the typed runtime policy for each agent and the
// external configuration files the launcher consumes (versions.env and
// config/runtime.json). The per-agent policy is deliberate Go configuration,
// not something reconstructed by a shell launcher: Claude and Codex both
// resolve through this one model, and the values here mirror what the Fish
// integration used to assemble from scattered call sites.
package config

import "fmt"

// Agent describes one agent's complete Microsandbox runtime policy.
type Agent struct {
	// Name is the agent identifier used on the command line ("claude" or
	// "codex") and, inside the guest, as the primary command name.
	Name string

	// Image is the OCI image tag to launch in Microsandbox.
	Image string

	// User is the unprivileged guest user the VM runs as.
	User string

	// TTY and PullNever map directly to msb run flags.
	TTY       bool
	PullNever bool

	// Resources: every agent has explicit values so names describe what
	// they control and no field is silently ignored.
	CPUs           int
	Memory         string
	RootDiskQuota  string
	WorkspaceQuota string
	HomeQuota      string

	// Security is the msb --security value, or "" when not set.
	Security string

	// Net is the fixed network mode for agents that always use one
	// ("public" for codex). Claude's network is resolved dynamically from
	// the egress allowlist, so its Net is "".
	Net string

	// BaseNetRules are the msb --net-rule values added before the
	// allowlist-derived rules in allowlist mode (the gateway DNS rules).
	BaseNetRules []string

	// HomeVolume and HomePath describe the persistent home named volume and
	// where it is mounted.
	HomeVolume string
	HomePath   string

	// HomeMountOpts is the base --mount-named option string for the home
	// volume before the quota is appended (for example "kind=dir" or
	// "rw"). The full mount suffix becomes HomeMountOpts + ",quota=" +
	// HomeQuota when HomeQuota is non-empty.
	HomeMountOpts string

	// WorkspaceMountOpts is the base --mount-dir option string for the
	// workspace before the quota is appended (for example "rw"). The full
	// mount suffix becomes WorkspaceMountOpts + ",quota=" + WorkspaceQuota
	// when WorkspaceQuota is non-empty.
	WorkspaceMountOpts string

	// WorkspaceHash selects how the stable workspace identity in the guest
	// path is derived: "git-blob" (git hash-object) or "sha256".
	WorkspaceHash string

	// Environment is appended via a guest `env` command before the primary
	// command when non-empty.
	Environment []string

	// Command is the guest command invoked after any `env` prefix.
	Command []string

	// CreateHomeVolume creates the home named volume when missing.
	CreateHomeVolume bool
}

// Agents is the single policy source for every supported agent.
var agents = map[string]Agent{
	"claude": {
		Name:               "claude",
		Image:              "ai-sandboxes-claude:local",
		User:               "node",
		TTY:                true,
		PullNever:          true,
		CPUs:               4,
		Memory:             "8G",
		RootDiskQuota:      "10G",
		WorkspaceQuota:     "10G",
		HomeQuota:          "4G",
		Security:           "restricted",
		BaseNetRules:       []string{"allow@host:udp:53", "allow@host:tcp:53"},
		HomeVolume:         "claude-home-hardened",
		HomePath:           "/home/node",
		HomeMountOpts:      "kind=dir",
		WorkspaceMountOpts: "rw",
		WorkspaceHash:      "git-blob",
		Environment: []string{
			"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
			"ENABLE_CLAUDEAI_MCP_SERVERS=false",
		},
		Command: []string{"claude"},
	},
	"codex": {
		Name:               "codex",
		Image:              "ai-sandboxes-codex:local",
		User:               "node",
		TTY:                true,
		PullNever:          true,
		CPUs:               4,
		Memory:             "8G",
		RootDiskQuota:      "20G",
		WorkspaceQuota:     "20G",
		HomeQuota:          "4G",
		Security:           "restricted",
		// Codex is deny-by-default: its network is resolved from
		// ~/.config/microvms/codex-egress, matching claude's model, with
		// CODEX_MSB_PUBLIC_EGRESS=1 as the escape hatch.
		BaseNetRules:       []string{"allow@host:udp:53", "allow@host:tcp:53"},
		HomeVolume:         "codex-home",
		HomePath:           "/home/node",
		HomeMountOpts:      "kind=dir",
		WorkspaceMountOpts: "rw",
		WorkspaceHash:      "sha256",
		Command:            []string{"codex"},
		CreateHomeVolume:   true,
	},
}

// AgentConfig returns a copy of the runtime policy for name, or an error when
// the agent is unknown.
func AgentConfig(name string) (Agent, error) {
	a, ok := agents[name]
	if !ok {
		return Agent{}, fmt.Errorf("unknown agent %q (supported: claude, codex)", name)
	}
	return a, nil
}

// Names returns the supported agent names in a stable order.
func Names() []string {
	return []string{"claude", "codex"}
}
