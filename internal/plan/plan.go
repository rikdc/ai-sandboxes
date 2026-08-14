// Package plan holds the typed runtime plan every agent resolves into and the
// pure resolution logic that builds it: workspace canonicalization and
// protection, shared-state parsing, network policy, and the exact guest
// mount/command layout. The package has no Microsandbox or Docker dependency,
// so its security and command-construction behavior is unit-testable in
// isolation.
package plan

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rikdc/ai-sandboxes/internal/config"
)

// SharedState is a validated shared-state mount for one profile id.
type SharedState struct {
	ID     string `json:"id"`
	Quota  string `json:"quota"`
	Volume string `json:"volume"` // agent-state-<id>-v1
	Mount  string `json:"mount"`  // full --mount-named value
}

// Resources is the per-VM resource allocation.
type Resources struct {
	CPUs        int    `json:"cpus,omitempty"`
	Memory      string `json:"memory,omitempty"`
	RootDisk    string `json:"root_disk"`
}

// Network is the resolved network policy: either public, or no-network plus an
// allowlist of msb --net-rule values.
type Network struct {
	Public bool     `json:"public,omitempty"`
	NoNet  bool     `json:"no_net,omitempty"`
	Rules  []string `json:"rules,omitempty"`
}

// RuntimePlan is the complete, deliberate description of what msb will run for
// one agent invocation. It is produced by Resolve and consumed by the
// Microsandbox adapter, which maps it mechanically onto msb run argv.
type RuntimePlan struct {
	AgentName      string       `json:"agent"`
	Image          string       `json:"image"`
	User           string       `json:"user"`
	TTY            bool         `json:"tty"`
	WorkspaceHost  string       `json:"workspace_host"`
	WorkspaceGuest string       `json:"workspace_guest"`
	WorkspaceMount string       `json:"workspace_mount"` // full --mount-dir value
	HomeVolume     string       `json:"home_volume"`
	HomeMount      string       `json:"home_mount"` // full --mount-named value
	SharedState    *SharedState `json:"shared_state,omitempty"`
	Resources      Resources    `json:"resources"`
	Security       string       `json:"security,omitempty"`
	Network        Network      `json:"network"`
	Environment    []string     `json:"environment,omitempty"`
	Command        []string     `json:"command"`
	AgentArgs      []string     `json:"agent_args,omitempty"`
}

// Input to Resolve. Workspace must already be canonical and validated by the
// caller (see FindWorkspace, ValidateWorkspace, RefuseOverlap).
type Input struct {
	Agent       string
	AgentArgs   []string
	Workspace   string
	SharedState *SharedState
	Network     Network
	// ImageOverride replaces the agent's baked base image. It is used for
	// session images, whose tag is resolved from a profile at run time rather
	// than baked into the agent policy.
	ImageOverride string
}

var (
	idRE    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	quotaRE = regexp.MustCompile(`^[1-9][0-9]*[KMGT]$`)
	hostRE  = regexp.MustCompile(`^(\*\.)?[A-Za-z0-9][A-Za-z0-9.-]*$`)
	slugRE  = regexp.MustCompile(`[^A-Za-z0-9._-]`)
)

// Resolve builds the RuntimePlan for an agent from validated inputs. It is
// pure: no subprocesses, no filesystem writes, no network.
func Resolve(cfg config.Agent, in Input) (*RuntimePlan, error) {
	if cfg.Name != in.Agent {
		return nil, fmt.Errorf("internal error: policy agent %q does not match requested agent %q", cfg.Name, in.Agent)
	}
	guest, err := GuestWorkspace(cfg.WorkspaceHash, in.Workspace)
	if err != nil {
		return nil, err
	}
	rootDisk := cfg.RootDiskQuota
	if !quotaRE.MatchString(rootDisk) {
		return nil, fmt.Errorf("invalid root-disk size %q", rootDisk)
	}

	resources := Resources{CPUs: cfg.CPUs, Memory: cfg.Memory, RootDisk: rootDisk}

	image := cfg.Image
	if in.ImageOverride != "" {
		image = in.ImageOverride
	}

	workspaceOpts := cfg.WorkspaceMountOpts
	if cfg.WorkspaceQuota != "" {
		workspaceOpts += ",quota=" + cfg.WorkspaceQuota
	}
	workspaceMount := fmt.Sprintf("%s:%s:%s", in.Workspace, guest, workspaceOpts)

	homeOpts := cfg.HomeMountOpts
	if cfg.HomeQuota != "" {
		homeOpts += ",quota=" + cfg.HomeQuota
	}
	homeMount := fmt.Sprintf("%s:%s:%s", cfg.HomeVolume, cfg.HomePath, homeOpts)

	return &RuntimePlan{
		AgentName:      cfg.Name,
		Image:          image,
		User:           cfg.User,
		TTY:            cfg.TTY,
		WorkspaceHost:  in.Workspace,
		WorkspaceGuest: guest,
		WorkspaceMount: workspaceMount,
		HomeVolume:     cfg.HomeVolume,
		HomeMount:      homeMount,
		SharedState:    in.SharedState,
		Resources:      resources,
		Security:       cfg.Security,
		Network:        in.Network,
		Environment:    cfg.Environment,
		Command:        cfg.Command,
		AgentArgs:      in.AgentArgs,
	}, nil
}

// resourceFlags emits the msb resource/security flags in a fixed order,
// independent of agent name. Anything unset (CPUs == 0, empty Memory or
// Security) is omitted.
func resourceFlags(r Resources, security string) []string {
	var flags []string
	if r.CPUs > 0 {
		flags = append(flags, "--cpus", fmt.Sprintf("%d", r.CPUs))
	}
	if r.Memory != "" {
		flags = append(flags, "--memory", r.Memory)
	}
	flags = append(flags, "--root-disk", r.RootDisk)
	if security != "" {
		flags = append(flags, "--security", security)
	}
	return flags
}

func networkFlags(net Network) []string {
	var flags []string
	if net.Public {
		flags = append(flags, "--net", "public")
		return flags
	}
	if net.NoNet {
		flags = append(flags, "--no-net")
	}
	for _, rule := range net.Rules {
		flags = append(flags, "--net-rule", rule)
	}
	return flags
}

// MsbArgv builds the exact `msb run` argv for the plan: common flags, the
// per-agent ordered flags (resources, security, network), the workspace, home,
// and shared-state mounts, the workdir, the image, and the guest command with
// every forwarded agent argument preserved verbatim. The Microsandbox adapter
// execs this argv without further interpretation.
func (p *RuntimePlan) MsbArgv() []string {
	argv := []string{"run"}
	if p.TTY {
		argv = append(argv, "--tty")
	}
	argv = append(argv, "--pull", "never", "--user", p.User)
	argv = append(argv, resourceFlags(p.Resources, p.Security)...)
	argv = append(argv, networkFlags(p.Network)...)
	argv = append(argv, "--mount-dir", p.WorkspaceMount)
	argv = append(argv, "--mount-named", p.HomeMount)
	if p.SharedState != nil {
		argv = append(argv, "--mount-named", p.SharedState.Mount)
	}
	argv = append(argv, "--workdir", p.WorkspaceGuest)
	argv = append(argv, p.Image)
	argv = append(argv, "--")
	if len(p.Environment) > 0 {
		argv = append(argv, "env")
		argv = append(argv, p.Environment...)
	}
	argv = append(argv, p.Command...)
	argv = append(argv, p.AgentArgs...)
	return argv
}

// GuestWorkspace derives the guest workspace path: /workspace/<slug>-<hash>
// where slug is the sanitized workspace basename and hash is 12 characters
// derived from the canonical host path by the agent's configured method.
func GuestWorkspace(hashMethod, workspace string) (string, error) {
	base := filepath.Base(workspace)
	slug := slugRE.ReplaceAllString(base, "-")
	hash, err := WorkspaceHash(hashMethod, workspace)
	if err != nil {
		return "", err
	}
	return "/workspace/" + slug + "-" + hash, nil
}

// WorkspaceHash computes the 12-character identity used in the guest workspace
// path. "sha256" is the first 12 hex digits of SHA-256 over the path;
// "git-blob" reproduces `printf '%s' PATH | git hash-object --stdin` (a blob
// SHA-1 with the git header), which is what the Claude launcher used.
func WorkspaceHash(method, workspace string) (string, error) {
	switch method {
	case "sha256":
		sum := sha256.Sum256([]byte(workspace))
		return hex.EncodeToString(sum[:])[:12], nil
	case "git-blob":
		content := []byte(workspace)
		h := sha1.New()
		fmt.Fprintf(h, "blob %d\x00", len(content))
		_, _ = h.Write(content)
		return hex.EncodeToString(h.Sum(nil))[:12], nil
	default:
		return "", fmt.Errorf("unknown workspace hash method %q", method)
	}
}

// SharedStateFromLabels turns OCI shared-state labels into a validated
// SharedState mount. Both labels absent, or both present but empty (as empty
// build ARGs leave them), means no shared state. A partial set is an error.
func SharedStateFromLabels(labels map[string]string) (*SharedState, error) {
	id := labels["io.ai-sandboxes.shared-state.id"]
	quota := labels["io.ai-sandboxes.shared-state.quota"]
	if id == "" && quota == "" {
		return nil, nil
	}
	if id == "" || quota == "" {
		return nil, fmt.Errorf("image has inconsistent shared-state labels")
	}
	if !idRE.MatchString(id) {
		return nil, fmt.Errorf("image has an invalid shared-state id %q", id)
	}
	if !quotaRE.MatchString(quota) {
		return nil, fmt.Errorf("image has an invalid shared-state quota %q", quota)
	}
	volume := "agent-state-" + id + "-v1"
	mount := fmt.Sprintf("%s:/var/lib/agent-state:kind=dir,quota=%s", volume, quota)
	return &SharedState{ID: id, Quota: quota, Volume: volume, Mount: mount}, nil
}

// ParseSharedStateRequest validates an explicit id/quota pair (from a session
// profile descriptor) into a SharedState mount, or returns nil when both are
// empty. It exists so the same validation the Fish
// __ai_sandbox_shared_state_request_args performed happens in one place.
func ParseSharedStateRequest(id, quota string) (*SharedState, error) {
	if id == "" && quota == "" {
		return nil, nil
	}
	if !idRE.MatchString(id) {
		return nil, fmt.Errorf("invalid shared-state id %q", id)
	}
	if !quotaRE.MatchString(quota) {
		return nil, fmt.Errorf("invalid shared-state quota %q", quota)
	}
	volume := "agent-state-" + id + "-v1"
	mount := fmt.Sprintf("%s:/var/lib/agent-state:kind=dir,quota=%s", volume, quota)
	return &SharedState{ID: id, Quota: quota, Volume: volume, Mount: mount}, nil
}

// ResolveNetwork computes the network policy for agents that use the egress
// allowlist (Claude). When public is true the public gateway is used and the
// allowlist is never read. Otherwise the file must exist and contain one valid
// hostname per line; baseRules (gateway DNS) are emitted before the
// allowlist-derived HTTPS rules.
func ResolveNetwork(public bool, egressFile string, baseRules []string) (Network, error) {
	if public {
		return Network{Public: true}, nil
	}
	rules := append([]string{}, baseRules...)
	if egressFile == "" {
		return Network{}, fmt.Errorf("missing egress allowlist path")
	}
	content, err := os.ReadFile(egressFile)
	if err != nil {
		return Network{}, fmt.Errorf("missing egress allowlist %s: %w", egressFile, err)
	}
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !hostRE.MatchString(line) {
			return Network{}, fmt.Errorf("invalid hostname in %s: %s", egressFile, line)
		}
		rules = append(rules, "allow@"+line+":tcp:443")
	}
	return Network{NoNet: true, Rules: rules}, nil
}

// FindWorkspace resolves the workspace to mount: the current git repository
// top level when inside one, otherwise the physical current directory, always
// canonicalized (symlinks resolved).
func FindWorkspace(dir string) (string, error) {
	ws := dir
	if top, err := gitTopLevel(dir); err == nil && top != "" {
		ws = top
	}
	resolved, err := filepath.EvalSymlinks(ws)
	if err != nil {
		return "", fmt.Errorf("cannot resolve workspace %s: %w", ws, err)
	}
	return resolved, nil
}

func gitTopLevel(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// ValidateWorkspace rejects workspaces that must never be mounted: the empty
// path, /, or the complete home directory.
func ValidateWorkspace(workspace, home string) error {
	switch {
	case workspace == "":
		return fmt.Errorf("refusing to mount an empty path")
	case workspace == "/":
		return fmt.Errorf("refusing to mount /")
	case home != "" && workspace == home:
		return fmt.Errorf("refusing to mount the complete home directory %s", home)
	}
	return nil
}

// RefuseOverlap rejects a workspace that is, contains, or is contained in any
// protected root (the ai-sandboxes checkout, installed wrapper directories, or
// the binary's own directory). A guest with write access to a mounted
// workspace that overlaps host-trusted launcher code could tamper with it for
// a later invocation. The workspace and every protected root are canonicalized
// at check time and the check fails closed if any cannot be resolved.
func RefuseOverlap(workspace string, roots []string) error {
	if workspace == "" {
		return fmt.Errorf("refusing to run: workspace is empty")
	}
	resolvedWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return fmt.Errorf("refusing to run: could not resolve workspace %s: %w", workspace, err)
	}
	sep := string(filepath.Separator)
	for _, root := range roots {
		resolved, err := filepath.EvalSymlinks(root)
		if err != nil {
			return fmt.Errorf("refusing to run: could not resolve protected path %s: %w", root, err)
		}
		if resolvedWorkspace == resolved ||
			strings.HasPrefix(resolvedWorkspace, resolved+sep) ||
			strings.HasPrefix(resolved, resolvedWorkspace+sep) {
			return fmt.Errorf(
				"refusing to run: the workspace (%s) overlaps a protected ai-sandboxes path (%s); a guest agent with write access to the mounted workspace could tamper with host-trusted launcher code for a later invocation to run with full host access; run the agent from a different project",
				resolvedWorkspace, resolved)
		}
	}
	return nil
}
