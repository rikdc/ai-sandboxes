// Package session resolves a claude-session profile into a loaded, verified
// session image plus its shared-state request. The host-side image build
// (scripts/session/resolve-image.sh) and transport (load-image.sh) stay in
// Bash; this package owns the policy resolution that used to live in the Fish
// claude-session launcher: profile naming and canonicalization, descriptor
// decoding, and the msb-vs-docker identity cross-check.
package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rikdc/ai-sandboxes/internal/runtime/microsandbox"
)

// SharedState is the validated shared-state request carried by a session image
// descriptor.
type SharedState struct {
	ID    string `json:"id"`
	Quota string `json:"quota"`
}

// Descriptor is the JSON resolve-image.sh prints on stdout: the session image
// tag to launch and, when the profile requested one, its shared-state pair.
type Descriptor struct {
	Image       string       `json:"image"`
	SharedState *SharedState `json:"shared_state"`
}

// ResolveProfilePath maps a --profile value to an absolute profile path. A
// value containing '/' is a literal path; anything else is a bare name
// resolved against ~/.config/ai-sandboxes/profiles/<name>.json. The result is
// canonicalized and must be an existing regular file.
func ResolveProfilePath(home, value string) (string, error) {
	p := value
	if !strings.Contains(value, "/") {
		p = filepath.Join(home, ".config", "ai-sandboxes", "profiles", value+".json")
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", fmt.Errorf("profile not found: %s", p)
	}
	if fi, err := os.Stat(resolved); err != nil || fi.IsDir() {
		return "", fmt.Errorf("profile not found: %s", p)
	}
	return resolved, nil
}

// ParseDescriptor decodes resolve-image.sh's stdout, enforcing the minimal
// contract the launcher relies on: a non-empty image and a shared_state that
// is either absent or a complete id/quota pair.
func ParseDescriptor(data []byte) (*Descriptor, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, fmt.Errorf("session image resolver produced no output")
	}
	var d Descriptor
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("session image resolver produced invalid JSON: %w", err)
	}
	if d.Image == "" {
		return nil, fmt.Errorf("session image resolver produced a descriptor with no image")
	}
	if d.SharedState != nil && (d.SharedState.ID == "" || d.SharedState.Quota == "") {
		return nil, fmt.Errorf("session image resolver produced a descriptor with an invalid shared_state")
	}
	return &d, nil
}

// Resolver drives the host-side session-image steps. Checkout is the
// ai-sandboxes checkout providing scripts/session/{resolve-image,load-image}.sh;
// Home is the literal $HOME used to resolve bare profile names. Run executes
// host programs and returns their combined output.
type Resolver struct {
	Checkout string
	Home     string
	Run      func(name string, args ...string) ([]byte, error)
}

// Resolve builds (or cache-loads) the session image named by profileValue,
// loads it into msb when load is true, and verifies the msb-side image is the
// same content Docker holds. It returns the descriptor (image + shared state).
func (r *Resolver) Resolve(profileValue string, load bool) (*Descriptor, error) {
	if r.Home == "" {
		return nil, fmt.Errorf("cannot resolve a session profile without HOME")
	}
	if r.Run == nil {
		return nil, fmt.Errorf("internal error: session resolver has no command runner")
	}
	profile, err := ResolveProfilePath(r.Home, profileValue)
	if err != nil {
		return nil, err
	}
	if r.Checkout == "" {
		return nil, fmt.Errorf("claude-session needs the ai-sandboxes checkout for its build scripts; run ai-sandbox from the checkout or set AI_SANDBOXES_ROOT")
	}
	resolveCmd := filepath.Join(r.Checkout, "scripts", "session", "resolve-image.sh")
	if _, err := os.Stat(resolveCmd); err != nil {
		return nil, fmt.Errorf("session image resolver not found at %s; claude-session requires the ai-sandboxes checkout", resolveCmd)
	}
	out, err := r.Run(resolveCmd, profile)
	if err != nil {
		return nil, fmt.Errorf("session image resolution failed: %w", runError(err, out))
	}
	d, err := ParseDescriptor(out)
	if err != nil {
		return nil, err
	}
	if !load {
		return d, nil
	}
	loadCmd := filepath.Join(r.Checkout, "scripts", "session", "load-image.sh")
	if _, err := os.Stat(loadCmd); err != nil {
		return nil, fmt.Errorf("session image loader not found at %s; claude-session requires the ai-sandboxes checkout", loadCmd)
	}
	if out, err = r.Run(loadCmd, d.Image); err != nil {
		return nil, fmt.Errorf("session image load failed: %w", runError(err, out))
	}
	if err := r.verifyLoaded(d.Image); err != nil {
		return nil, err
	}
	return d, nil
}

// verifyLoaded cross-checks the msb-side image against Docker's. msb load
// does not verify an image's identity and drops OCI labels, so a tag with the
// same name but different content must not be trusted.
func (r *Resolver) verifyLoaded(tag string) error {
	dockerOut, err := r.Run("docker", "image", "inspect", "--format", "{{.Id}}", tag)
	if err != nil {
		return fmt.Errorf("cannot inspect Docker image %s: %w", tag, runError(err, dockerOut))
	}
	msbOut, err := r.Run("msb", "image", "inspect", "--format", "json", tag)
	if err != nil {
		return fmt.Errorf("cannot inspect msb image %s: %w", tag, runError(err, msbOut))
	}
	meta, err := microsandbox.ParseImageMetadata(msbOut)
	if err != nil {
		return fmt.Errorf("cannot parse msb image metadata for %s: %w", tag, err)
	}
	if strings.TrimSpace(string(dockerOut)) != meta.ConfigDigest {
		return fmt.Errorf("msb image %s does not match Docker image %s; remove it (msb image remove %s) before retrying",
			tag, tag, tag)
	}
	return nil
}

func runError(err error, out []byte) error {
	if txt := strings.TrimSpace(string(out)); txt != "" {
		return fmt.Errorf("%w: %s", err, txt)
	}
	return err
}
