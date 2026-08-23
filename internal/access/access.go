// Package access loads and validates host-owned SSH access profiles for
// `ai-sandbox run --access <name>`. A profile pins one exact SSH destination
// (host, port, user) with pinned server host keys, reachable from the guest
// as `ssh <name>`; its key material lives in one dedicated per-profile
// directory under the user configuration tree, mounted read-only into the
// guest at /run/ai-sandbox/ssh. The package is deliberately strict: profiles
// are JSON with unknown fields rejected, and the key directory whitelist
// structurally refuses ~/.ssh, workspaces, and every other location on the
// host.
package access

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/rikdc/ai-sandboxes/internal/config"
)

// SchemaVersion is the only supported access-profile schema version.
const SchemaVersion = 1

// GuestDir is where the control plane mounts the profile's key directory
// inside the guest.
const GuestDir = "/run/ai-sandbox/ssh"

// ConfigIncludePath is the system-wide include location the generated
// ssh_config is mounted at — the single delivery mechanism. Debian's stock
// /etc/ssh/ssh_config sources /etc/ssh/ssh_config.d/*.conf, so plain
// `ssh <name>` resolves inside the guest without -F.
const ConfigIncludePath = "/etc/ssh/ssh_config.d/99-ai-sandbox-access.conf"

// ConfigIncludeMount returns the read-only --mount-file value that exposes
// <keyDir>/config at ConfigIncludePath inside the guest.
func ConfigIncludeMount(keyDir string) string {
	return filepath.Join(keyDir, "config") + ":" + ConfigIncludePath + ":ro"
}

// PrivateKeyFile and PublicKeyFile are the file names required inside every
// key directory.
const (
	PrivateKeyFile = "id_ed25519"
	PublicKeyFile  = "id_ed25519.pub"
)

// Profile mirrors ~/.config/ai-sandboxes/access/<name>.json. It pins exactly
// one SSH destination: one host, one account, one port. The profile name is
// the guest-side ssh alias (`ssh <name>`); multiple destinations are expressed
// as multiple access profiles.
type Profile struct {
	SchemaVersion int `json:"schema_version"`
	// Host is the exact destination hostname the guest may reach.
	Host string `json:"host"`
	// Port is the exact TCP port of the destination.
	Port int `json:"port"`
	// User is the remote account ssh authenticates as.
	User string `json:"user"`
	// HostKeys are pinned known_hosts lines for Host. At least one is
	// required, so a guest can never be talked into trusting an unverified
	// server key. Each line is either the full known_hosts form
	// ("<selector> <algo> <key>") or the bare ssh-keyscan tail
	// ("<algo> <key>"); in the bare form the destination's selector is
	// prefixed when the known_hosts file is rendered. Lines are validated
	// strictly: no wildcards, negations, comma-separated patterns, or
	// @cert-authority/@revoked markers.
	HostKeys []string `json:"host_keys"`
}

// nameRE is the same slug pattern config.SharedStateIDRE exports for
// shared-state ids and internal/plan reuses for its own id validation; access
// profile names follow the identical contract (they become both the key
// directory name and the guest ssh alias), so it is reused here rather than
// redefined.
var (
	nameRE = config.SharedStateIDRE()
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
	if !hostRE.MatchString(p.Host) || strings.Contains(p.Host, "*") {
		return fmt.Errorf("invalid host %q", p.Host)
	}
	if p.Port < 1 || p.Port > 65535 {
		return fmt.Errorf("port %d out of range", p.Port)
	}
	if !userRE.MatchString(p.User) {
		return fmt.Errorf("invalid user %q", p.User)
	}
	if len(p.HostKeys) == 0 {
		return fmt.Errorf("at least one pinned host key is required")
	}
	selector := KnownHostsSelector(p.Host, p.Port)
	for j, k := range p.HostKeys {
		if err := validateHostKey(selector, k); err != nil {
			return fmt.Errorf("host key %d: %w", j+1, err)
		}
	}
	return nil
}

// KnownHostsSelector renders the known_hosts host-pattern field for a
// destination the way ssh itself matches it: "host" for port 22 and
// "[host]:port" for any other port. The selector is derived exclusively from
// the profile's Host and Port so a rendered entry can never match a different
// endpoint.
func KnownHostsSelector(host string, port int) string {
	if port == 22 {
		return host
	}
	return fmt.Sprintf("[%s]:%d", host, port)
}

// hostKeyAlgoRE matches the algorithm field of a pinned known_hosts entry:
// the standard SSH public key algorithm names.
var hostKeyAlgoRE = regexp.MustCompile(
	`^(ssh-(ed25519|rsa|dss)|ecdsa-sha2-nistp(256|384|521)|sk-(ssh-ed25519|ecdsa-sha2-nistp256)@openssh\.com)$`)

// keyBase64RE constrains the key body to standard base64 characters before it
// is decoded; anything else (spaces, colons, option suffixes) is malformed.
var keyBase64RE = regexp.MustCompile(`^[A-Za-z0-9+/]+={0,2}$`)

// validateHostKey strictly validates one pinned known_hosts entry against the
// destination's selector. Accepted forms are exactly "<selector> <algo>
// <key>" and the bare "<algo> <key>". Everything else — wildcards,
// negations, comma-separated patterns, @cert-authority/@revoked markers,
// unknown algorithms, non-base64 keys — is rejected rather than passed
// through to the guest's known_hosts file.
func validateHostKey(selector, line string) error {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return fmt.Errorf("is empty or a comment")
	}
	fields := strings.Fields(line)
	var algo, key string
	switch len(fields) {
	case 2:
		algo, key = fields[0], fields[1]
	case 3:
		pattern := fields[0]
		if strings.ContainsAny(pattern, "*?!") || strings.HasPrefix(pattern, "@") || strings.Contains(pattern, ",") {
			return fmt.Errorf("unsupported host pattern %q", pattern)
		}
		if pattern != selector {
			return fmt.Errorf("host pattern %q does not match the destination selector %q", pattern, selector)
		}
		algo, key = fields[1], fields[2]
	default:
		return fmt.Errorf("not a known_hosts line (want %q <algo> <key> or bare <algo> <key>)", selector)
	}
	if !hostKeyAlgoRE.MatchString(algo) {
		return fmt.Errorf("unsupported key algorithm %q", algo)
	}
	if !keyBase64RE.MatchString(key) {
		return fmt.Errorf("malformed key material")
	}
	if _, err := base64.StdEncoding.DecodeString(key); err != nil {
		return fmt.Errorf("malformed key material: %w", err)
	}
	return nil
}

// NetRule is the msb --net-rule value allowing exactly this destination.
func (p *Profile) NetRule() string {
	return fmt.Sprintf("allow@%s:tcp:%d", p.Host, p.Port)
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

// RenderConfig renders the hardened ssh_config served to the guest. name is
// the profile name and becomes the ssh alias, so `ssh <name>` locks to the
// pinned host with the single mounted identity; agent and forwarding are
// disabled outright.
func RenderConfig(name string, p *Profile) string {
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
	fmt.Fprintf(&b, "Host %s\n"+
		"    HostName %s\n"+
		"    Port %d\n"+
		"    User %s\n\n", name, p.Host, p.Port, p.User)
	return b.String()
}

// RenderKnownHosts renders the pinned known_hosts file contents. Every entry
// is rendered with the destination's selector ("host" for port 22,
// "[host]:port" otherwise), because known_hosts matches entries by the exact
// host and port used to connect and a bare key line would silently never
// match anything. Profiles are validated before rendering, so each line is
// normalized to "<selector> <algo> <key>".
func RenderKnownHosts(p *Profile) string {
	var b strings.Builder
	b.WriteString("# Generated by ai-sandbox from an access profile; edits are overwritten.\n")
	selector := KnownHostsSelector(p.Host, p.Port)
	for _, k := range p.HostKeys {
		fields := strings.Fields(strings.TrimSpace(k))
		if len(fields) < 2 {
			continue // unreachable for validated profiles; skip defensively
		}
		fmt.Fprintf(&b, "%s %s %s\n", selector, fields[len(fields)-2], fields[len(fields)-1])
	}
	return b.String()
}

// Materialize writes config and known_hosts into the key directory. Both are
// derived state, rewritten idempotently before every run; the profile stays
// the single source of truth. The files contain no secrets.
//
// Each file is written to a unique temporary file in the same directory,
// forced to mode 0600, then atomically renamed over the target, so concurrent
// launches never observe a partially written file and a crash never leaves one
// behind. A pre-existing target that is not a regular file (a symlink or
// device) is refused instead of being replaced through.
func Materialize(dir, name string, p *Profile) error {
	names := []string{"config", "known_hosts"}
	files := map[string]string{
		"config":      RenderConfig(name, p),
		"known_hosts": RenderKnownHosts(p),
	}
	// Deterministic order so two racing materializations converge on the same
	// final content regardless of map iteration order.
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(dir, name)
		if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to write %s: not a regular file", path)
		}
		tmp, err := os.CreateTemp(dir, "."+name+".tmp-")
		if err != nil {
			return fmt.Errorf("could not create temporary file for %s: %w", path, err)
		}
		tmpName := tmp.Name()
		if _, err := tmp.WriteString(files[name]); err != nil {
			tmp.Close()
			os.Remove(tmpName)
			return fmt.Errorf("could not write %s: %w", path, err)
		}
		if err := tmp.Chmod(0o600); err != nil {
			tmp.Close()
			os.Remove(tmpName)
			return fmt.Errorf("could not set mode 0600 on %s: %w", path, err)
		}
		if err := tmp.Close(); err != nil {
			os.Remove(tmpName)
			return fmt.Errorf("could not close %s: %w", path, err)
		}
		if err := os.Rename(tmpName, path); err != nil {
			os.Remove(tmpName)
			return fmt.Errorf("could not finalize %s: %w", path, err)
		}
	}
	return nil
}
