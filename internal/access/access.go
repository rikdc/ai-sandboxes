// Package access loads and validates host-owned SSH access profiles for
// `ai-sandbox run --access <name>`. A profile names exact SSH destinations
// (host, port, user) with pinned server host keys; its key material lives in
// one dedicated per-profile directory under the user configuration tree,
// mounted read-only into the guest at /run/ai-sandbox/ssh. The package is
// deliberately strict: profiles are JSON with unknown fields rejected, and
// the key directory whitelist structurally refuses ~/.ssh, workspaces, and
// every other location on the host.
package access

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SchemaVersion is the only supported access-profile schema version.
const SchemaVersion = 1

// GuestDir is where the control plane mounts the profile's key directory
// inside the guest.
const GuestDir = "/run/ai-sandbox/ssh"

// SSHConfigEnvVar is the guest environment variable pointing ssh(1) at the
// generated hardened config, so plain `ssh <alias>` uses it by default.
const SSHConfigEnvVar = "AI_SANDBOX_SSH_CONFIG"

// PrivateKeyFile and PublicKeyFile are the file names required inside every
// key directory.
const (
	PrivateKeyFile = "id_ed25519"
	PublicKeyFile  = "id_ed25519.pub"
)

// Destination is one exact SSH endpoint the guest may reach.
type Destination struct {
	Alias string `json:"alias"`
	Host  string `json:"host"`
	Port  int    `json:"port"`
	User  string `json:"user"`
	// HostKeys are pinned known_hosts lines for Host. At least one is
	// required so a guest can never be talked into trusting an unverified
	// server key.
	HostKeys []string `json:"host_keys"`
}

// Profile mirrors ~/.config/ai-sandboxes/access/<name>.json.
type Profile struct {
	SchemaVersion int           `json:"schema_version"`
	Destinations  []Destination `json:"destinations"`
}

var (
	nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	hostRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*$`)
	userRE = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
)

// ProfilePath returns the profile file path for name under configDir.
func ProfilePath(configDir, name string) string {
	return filepath.Join(configDir, "access", name+".json")
}

// KeyRoot returns the directory that contains every access key directory.
// It is the whitelist root: nothing outside it may ever be mounted as a
// credential source.
func KeyRoot(configDir string) string {
	return filepath.Join(configDir, "access", "keys")
}

// KeyDir returns the canonical (unresolved) key directory path for name.
func KeyDir(configDir, name string) string {
	return filepath.Join(KeyRoot(configDir), name)
}

// Load reads, parses strictly, and validates the named access profile.
func Load(configDir, name string) (*Profile, error) {
	if !nameRE.MatchString(name) {
		return nil, fmt.Errorf("invalid access profile name %q", name)
	}
	path := ProfilePath(configDir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("could not read access profile: %w", err)
	}
	p, err := parse(data)
	if err != nil {
		return nil, fmt.Errorf("invalid access profile %s: %w", path, err)
	}
	return p, nil
}

func parse(data []byte) (*Profile, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("empty document")
	}
	if trimmed[0] != '{' {
		return nil, fmt.Errorf("top-level value must be an object")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var doc Profile
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("unexpected trailing content")
		}
		return nil, err
	}
	if err := doc.Validate(); err != nil {
		return nil, err
	}
	return &doc, nil
}

// Validate enforces the full profile contract.
func (p *Profile) Validate() error {
	if p.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema_version %d (want %d)", p.SchemaVersion, SchemaVersion)
	}
	if len(p.Destinations) == 0 {
		return fmt.Errorf("at least one destination is required")
	}
	seen := map[string]bool{}
	for i, d := range p.Destinations {
		if !nameRE.MatchString(d.Alias) {
			return fmt.Errorf("destination %d: invalid alias %q", i+1, d.Alias)
		}
		if seen[d.Alias] {
			return fmt.Errorf("destination %d: duplicate alias %q", i+1, d.Alias)
		}
		seen[d.Alias] = true
		if !hostRE.MatchString(d.Host) || strings.Contains(d.Host, "*") {
			return fmt.Errorf("destination %s: invalid host %q", d.Alias, d.Host)
		}
		if d.Port < 1 || d.Port > 65535 {
			return fmt.Errorf("destination %s: port %d out of range", d.Alias, d.Port)
		}
		if !userRE.MatchString(d.User) {
			return fmt.Errorf("destination %s: invalid user %q", d.Alias, d.User)
		}
		if len(d.HostKeys) == 0 {
			return fmt.Errorf("destination %s: at least one pinned host key is required", d.Alias)
		}
		for j, k := range d.HostKeys {
			line := strings.TrimSpace(k)
			if line == "" || strings.HasPrefix(line, "#") {
				return fmt.Errorf("destination %s: host key %d is empty or a comment", d.Alias, j+1)
			}
			fields := strings.Fields(line)
			if len(fields) < 2 {
				return fmt.Errorf("destination %s: host key %d is not a known_hosts line", d.Alias, j+1)
			}
		}
	}
	return nil
}

// NetRule is the msb --net-rule value allowing exactly this destination.
func (d Destination) NetRule() string {
	return fmt.Sprintf("allow@%s:tcp:%d", d.Host, d.Port)
}

// NetRules returns the network allow rules for every destination.
func (p *Profile) NetRules() []string {
	rules := make([]string, 0, len(p.Destinations))
	for _, d := range p.Destinations {
		rules = append(rules, d.NetRule())
	}
	return rules
}

// ResolveKeyDir canonicalizes the profile's key directory and fails closed if
// symlinks carry it outside KeyRoot. This is what makes ~/.ssh unusable as a
// key source even when a symlink points there.
func ResolveKeyDir(configDir, name string) (string, error) {
	if !nameRE.MatchString(name) {
		return "", fmt.Errorf("invalid access profile name %q", name)
	}
	dir := KeyDir(configDir, name)
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("cannot resolve access key directory %s: %w", dir, err)
	}
	root := KeyRoot(configDir)
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("cannot resolve access key root %s: %w", root, err)
	}
	expected := filepath.Join(resolvedRoot, name)
	if resolved != expected {
		return "", fmt.Errorf(
			"refusing to run: access key directory %s resolves to %s, outside %s; only directories inside the ai-sandboxes access key root may hold credentials",
			dir, resolved, resolvedRoot)
	}
	return resolved, nil
}

// ValidateKeyDir checks the key directory contract: directory mode 0700, a
// mode-0600 private key, and the matching public key present.
func ValidateKeyDir(dir string) error {
	info, err := os.Stat(dir)
	switch {
	case err != nil:
		return fmt.Errorf("cannot stat access key directory %s: %w", dir, err)
	case !info.IsDir():
		return fmt.Errorf("access key directory %s is not a directory", dir)
	case info.Mode().Perm() != 0o700:
		return fmt.Errorf("access key directory %s must have mode 0700 (got %04o)", dir, info.Mode().Perm())
	}
	keyPath := filepath.Join(dir, PrivateKeyFile)
	keyInfo, err := os.Lstat(keyPath)
	switch {
	case err != nil:
		return fmt.Errorf("access key directory %s is missing %s: %w", dir, PrivateKeyFile, err)
	case !keyInfo.Mode().IsRegular():
		return fmt.Errorf("%s must be a regular file", keyPath)
	case keyInfo.Mode().Perm() != 0o600:
		return fmt.Errorf("%s must have mode 0600 (got %04o)", keyPath, keyInfo.Mode().Perm())
	}
	pubPath := filepath.Join(dir, PublicKeyFile)
	pubInfo, err := os.Lstat(pubPath)
	switch {
	case err != nil:
		return fmt.Errorf("access key directory %s is missing %s: %w", dir, PublicKeyFile, err)
	case !pubInfo.Mode().IsRegular():
		return fmt.Errorf("%s must be a regular file", pubPath)
	}
	return nil
}

// RenderConfig renders the hardened ssh_config served to the guest. Every
// destination gets a Host block locked to the pinned known_hosts file and the
// single mounted identity; agent and forwarding are disabled outright.
func RenderConfig(p *Profile) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Generated by ai-sandbox from an access profile; edits are overwritten.\n")
	fmt.Fprintf(&b, "Host *\n"+
		"    IdentitiesOnly yes\n"+
		"    IdentityFile %s/%s\n"+
		"    UserKnownHostsFile %s/known_hosts\n"+
		"    StrictHostKeyChecking yes\n"+
		"    ForwardAgent no\n"+
		"    ClearAllForwardings yes\n"+
		"    PasswordAuthentication no\n\n", GuestDir, PrivateKeyFile, GuestDir)
	for _, d := range p.Destinations {
		fmt.Fprintf(&b, "Host %s\n"+
			"    HostName %s\n"+
			"    Port %d\n"+
			"    User %s\n\n", d.Alias, d.Host, d.Port, d.User)
	}
	return b.String()
}

// RenderKnownHosts renders the pinned known_hosts file contents.
func RenderKnownHosts(p *Profile) string {
	var b strings.Builder
	b.WriteString("# Generated by ai-sandbox from an access profile; edits are overwritten.\n")
	for _, d := range p.Destinations {
		for _, k := range d.HostKeys {
			b.WriteString(strings.TrimSpace(k))
			b.WriteString("\n")
		}
	}
	return b.String()
}

// Materialize writes config and known_hosts into the key directory. Both are
// derived state, rewritten idempotently before every run; the profile stays
// the single source of truth. The files contain no secrets.
func Materialize(dir string, p *Profile) error {
	files := map[string]string{
		"config":      RenderConfig(p),
		"known_hosts": RenderKnownHosts(p),
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return fmt.Errorf("could not write %s: %w", path, err)
		}
	}
	return nil
}
