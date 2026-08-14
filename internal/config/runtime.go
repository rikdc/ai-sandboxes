package config

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
)

// SharedState is a validated shared-state request: a named persistent volume
// mounted at /var/lib/agent-state in every image that opts in to the same id.
type SharedState struct {
	ID    string `json:"id"`
	Quota string `json:"quota"`
}

// Runtime mirrors config/runtime.json.
type Runtime struct {
	SharedState *SharedState `json:"shared_state"`
}

var (
	sharedStateIDRE    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	sharedStateQuotaRE = regexp.MustCompile(`^[1-9][0-9]*[KMGT]$`)
)

// ValidateSharedState checks the id and quota patterns used by both the
// checked-in runtime.json and OCI image labels.
func ValidateSharedState(id, quota string) error {
	if !sharedStateIDRE.MatchString(id) {
		return fmt.Errorf("invalid shared-state id %q", id)
	}
	if !sharedStateQuotaRE.MatchString(quota) {
		return fmt.Errorf("invalid shared-state quota %q", quota)
	}
	return nil
}

// SharedStateIDRE and SharedStateQuotaRE are exported for doctor and plan to
// reuse without duplicating the pattern.
func SharedStateIDRE() *regexp.Regexp { return sharedStateIDRE }
func SharedStateQuotaRE() *regexp.Regexp { return sharedStateQuotaRE }

// ParseRuntime validates the exact runtime.json shape: a top-level object with
// only a shared_state key that is either null or {id, quota} with valid values.
func ParseRuntime(data []byte) (*Runtime, error) {
	var doc struct {
		Runtime
		Extra map[string]json.RawMessage `json:"-"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("invalid runtime document: %w", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("invalid runtime document: %w", err)
	}
	for key := range raw {
		if key != "shared_state" {
			return nil, fmt.Errorf("invalid runtime document: unexpected key %q", key)
		}
	}
	if doc.SharedState == nil {
		return &Runtime{}, nil
	}
	if err := ValidateSharedState(doc.SharedState.ID, doc.SharedState.Quota); err != nil {
		return nil, fmt.Errorf("invalid runtime document: %w", err)
	}
	return &doc.Runtime, nil
}

// LoadRuntime reads and validates config/runtime.json.
func LoadRuntime(path string) (*Runtime, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseRuntime(data)
}
