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

// ErrNoSandbox is returned when no running sandbox matches the requested
// agent + workspace hash.
var ErrNoSandbox = errors.New("no running sandbox for this agent and workspace")

// ErrMultipleSandboxes is returned when more than one running sandbox matches
// the requested agent + workspace hash — the caller must resolve the ambiguity
// by stopping the extras before an attach operation can proceed.
var ErrMultipleSandboxes = errors.New("multiple running sandboxes for this agent and workspace")

// FindSandbox returns the single running sandbox for the given agent + workspace
// hash, matched against the ai-sandbox.agent and ai-sandbox.workspace labels set
// by `plan.Resolve`.
func (c *Client) FindSandbox(agent, workspaceHash string) (*Sandbox, error) {
	out, err := c.runCapture(
		"list", "--running", "--format", "json",
		"--label", "ai-sandbox.agent="+agent,
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
		return nil, ErrNoSandbox
	case 1:
		return &sandboxes[0], nil
	default:
		return nil, ErrMultipleSandboxes
	}
}
