package microsandbox

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Sandbox is the subset of `msb list --format json` output the discovery
// path consumes. Labels are not surfaced by `msb list`; the label filter is
// applied server-side by `--label KEY=VALUE`, so callers do not read labels
// back off the result.
type Sandbox struct {
	Name   string `json:"name"`
	Image  string `json:"image"`
	Status string `json:"status"`
}

// ErrNoCodexSandbox is returned when no running codex sandbox matches the
// requested workspace hash.
var ErrNoCodexSandbox = errors.New("no running codex sandbox for this workspace")

// ErrMultipleCodexSandboxes is returned when more than one running codex
// sandbox matches the requested workspace hash — the caller must resolve the
// ambiguity by stopping the extras before `codex login` can attach.
var ErrMultipleCodexSandboxes = errors.New("multiple running codex sandboxes for this workspace")

// FindCodexSandbox returns the single running codex sandbox for the given
// workspace hash, matched against the ai-sandbox.agent and
// ai-sandbox.workspace labels set by `plan.Resolve` for codex agents.
func (c *Client) FindCodexSandbox(workspaceHash string) (*Sandbox, error) {
	out, err := c.runCapture(
		"list", "--running", "--format", "json",
		"--label", "ai-sandbox.agent=codex",
		"--label", "ai-sandbox.workspace="+workspaceHash,
	)
	if err != nil {
		return nil, fmt.Errorf("msb list: %w", err)
	}
	var sandboxes []Sandbox
	if err := json.Unmarshal(out, &sandboxes); err != nil {
		return nil, fmt.Errorf("parse msb list output: %w", err)
	}
	switch len(sandboxes) {
	case 0:
		return nil, ErrNoCodexSandbox
	case 1:
		return &sandboxes[0], nil
	default:
		return nil, ErrMultipleCodexSandboxes
	}
}
