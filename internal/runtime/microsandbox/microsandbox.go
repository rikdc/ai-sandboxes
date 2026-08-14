// Package microsandbox adapts a resolved plan to the Microsandbox CLI. It owns
// the exact msb argv construction and every host-side msb subprocess: image and
// volume inspection, shared-state initialization, and the final exec. The
// command construction is pure (RunArgv) so it is unit-testable without a
// Docker or Microsandbox installation.
package microsandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/rikdc/ai-sandboxes/internal/plan"
)

// ImageMetadata is the subset of `msb image inspect --format json` that the
// launcher consumes: the OCI config digest and the config labels.
type ImageMetadata struct {
	ConfigDigest string
	Labels       map[string]string
}

// Client runs msb commands on the host. Msb is injectable so tests can stand
// in a stub binary; a zero value resolves msb from PATH. Out receives streamed
// output from side-effect commands (volume creation, shared-state init).
type Client struct {
	Msb string
	Env []string
	Out io.Writer
}

// LookPathMsb returns the resolved msb executable, or an error carrying a
// stable message when msb is not installed or not on PATH.
func LookPathMsb() (string, error) {
	path, err := exec.LookPath("msb")
	if err != nil {
		return "", fmt.Errorf("msb is not installed or not on PATH")
	}
	return path, nil
}

func (c *Client) msb() string {
	if c.Msb != "" {
		return c.Msb
	}
	return "msb"
}

func (c *Client) runCapture(args ...string) ([]byte, error) {
	cmd := exec.Command(c.msb(), args...)
	if len(c.Env) > 0 {
		cmd.Env = c.Env
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) runStream(args ...string) error {
	cmd := exec.Command(c.msb(), args...)
	if len(c.Env) > 0 {
		cmd.Env = c.Env
	}
	cmd.Stdout = c.Out
	cmd.Stderr = c.Out
	return cmd.Run()
}

// ImageList returns the msb image tags, one per line.
func (c *Client) ImageList() ([]string, error) {
	out, err := c.runCapture("image", "list", "--quiet")
	if err != nil {
		return nil, fmt.Errorf("msb image list: %w", err)
	}
	return splitLines(out), nil
}

// ImagePresent reports whether the exact tag is loaded in msb.
func (c *Client) ImagePresent(tag string) (bool, error) {
	tags, err := c.ImageList()
	if err != nil {
		return false, err
	}
	for _, t := range tags {
		if t == tag {
			return true, nil
		}
	}
	return false, nil
}

// ImageMetadata inspects an msb image. A missing image surfaces as an error.
func (c *Client) ImageMetadata(tag string) (*ImageMetadata, error) {
	out, err := c.runCapture("image", "inspect", "--format", "json", tag)
	if err != nil {
		return nil, fmt.Errorf("msb image inspect %s: %w", tag, err)
	}
	return ParseImageMetadata(out)
}

// ErrDigestMismatch is returned by MatchDigests when two digests do not
// represent the same image.
var ErrDigestMismatch = errors.New("digest mismatch")

// stripSHA256Prefix removes a case-insensitive "sha256:" prefix without
// allocating a lowercase copy of the whole string.
func stripSHA256Prefix(s string) string {
	if len(s) >= 7 && strings.EqualFold(s[:7], "sha256:") {
		return s[7:]
	}
	return s
}

// MatchDigests compares a Docker image digest and an msb config digest for
// identity. It normalizes both strings: trims whitespace, strips an optional
// sha256: prefix, and compares case-insensitively. It fails closed on empty
// strings.
func MatchDigests(dockerDigest, msbDigest string) error {
	dockerDigest = strings.TrimSpace(dockerDigest)
	msbDigest = strings.TrimSpace(msbDigest)
	if dockerDigest == "" || msbDigest == "" {
		return errors.New("empty digest")
	}
	dockerDigest = stripSHA256Prefix(dockerDigest)
	msbDigest = stripSHA256Prefix(msbDigest)
	if !strings.EqualFold(dockerDigest, msbDigest) {
		return fmt.Errorf("%w: docker %q vs msb %q", ErrDigestMismatch, dockerDigest, msbDigest)
	}
	return nil
}

// ParseImageMetadata decodes `msb image inspect --format json` output into the
// subset the launcher consumes: the OCI config digest and config labels.
func ParseImageMetadata(data []byte) (*ImageMetadata, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, fmt.Errorf("invalid msb image metadata: %w", err)
	}
	meta := &ImageMetadata{Labels: map[string]string{}}
	configRaw, ok := top["config"]
	if !ok {
		return meta, nil
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(configRaw, &config); err != nil {
		return meta, nil
	}
	if d, ok := config["digest"]; ok {
		_ = json.Unmarshal(d, &meta.ConfigDigest)
	}
	if l, ok := config["Labels"]; ok {
		var labels map[string]string
		if err := json.Unmarshal(l, &labels); err == nil {
			meta.Labels = labels
		}
	}
	return meta, nil
}

// VolumeList returns the named volumes, one per line.
func (c *Client) VolumeList() ([]string, error) {
	out, err := c.runCapture("volume", "list", "--quiet")
	if err != nil {
		return nil, fmt.Errorf("msb volume list: %w", err)
	}
	return splitLines(out), nil
}

// VolumePresent reports whether a named volume exists.
func (c *Client) VolumePresent(name string) (bool, error) {
	volumes, err := c.VolumeList()
	if err != nil {
		return false, err
	}
	for _, v := range volumes {
		if v == name {
			return true, nil
		}
	}
	return false, nil
}

// VolumeCreate creates a named volume.
func (c *Client) VolumeCreate(name string) error {
	if err := c.runStream("volume", "create", name); err != nil {
		return fmt.Errorf("msb volume create %s: %w", name, err)
	}
	return nil
}

// InitSharedState boots the image once to install the shared-state directory
// owned by the node user with the requested mode. Microsandbox assigns the
// volume quota on first mount, so this doubles as the volume creation.
func (c *Client) InitSharedState(image string, st *plan.SharedState) error {
	argv := []string{
		"run",
		"--pull", "never",
		"--no-tty",
		"--no-net",
		"--security", "restricted",
		"--user", "root",
		"--mount-named", st.Mount,
		image,
		"--",
		"install", "-d", "-o", "node", "-g", "node", "-m", "0700", "/var/lib/agent-state",
	}
	if err := c.runStream(argv...); err != nil {
		return fmt.Errorf("could not initialize shared state: %w", err)
	}
	return nil
}

// Launch replaces the current process with `msb run` so the agent's exit code
// and signal handling propagate directly to the caller.
func Launch(msbPath string, argv []string) error {
	if msbPath == "" {
		msbPath = "msb"
	}
	argv0 := filepath.Base(msbPath)
	return syscall.Exec(msbPath, append([]string{argv0}, argv...), os.Environ())
}

func splitLines(out []byte) []string {
	var lines []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
