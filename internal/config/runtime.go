package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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

// strictSharedState mirrors SharedState but decodes with DisallowUnknownFields.
// Kept private because the public type stays tag-clean for consumers that want
// to encode a SharedState back out.
type strictSharedState struct {
	ID    string `json:"id"`
	Quota string `json:"quota"`
}

type strictRuntime struct {
	SharedState *strictSharedState `json:"shared_state"`
}

// ParseRuntime validates the exact runtime.json shape: a non-null top-level
// object with only a shared_state key that is either null or {id, quota} with
// valid values. Unknown fields at any nesting level are rejected, as are
// trailing tokens after the document.
func ParseRuntime(data []byte) (*Runtime, error) {
	// json.Unmarshal accepts a literal `null` for pointer/struct targets,
	// which would silently produce an empty Runtime. Reject it upfront so the
	// contract ("a top-level object") is enforced.
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("invalid runtime document: empty")
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return nil, fmt.Errorf("invalid runtime document: top-level null is not allowed")
	}
	if trimmed[0] != '{' {
		return nil, fmt.Errorf("invalid runtime document: top-level value must be an object")
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var doc strictRuntime
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("invalid runtime document: %w", err)
	}
	// Reject trailing tokens: `{"shared_state":null} garbage` or two
	// concatenated JSON objects should not parse as valid runtime.
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("invalid runtime document: unexpected trailing content")
		}
		return nil, fmt.Errorf("invalid runtime document: %w", err)
	}

	rt := &Runtime{}
	if doc.SharedState == nil {
		return rt, nil
	}
	if err := ValidateSharedState(doc.SharedState.ID, doc.SharedState.Quota); err != nil {
		return nil, fmt.Errorf("invalid runtime document: %w", err)
	}
	rt.SharedState = &SharedState{ID: doc.SharedState.ID, Quota: doc.SharedState.Quota}
	return rt, nil
}

// LoadRuntime reads and validates config/runtime.json.
func LoadRuntime(path string) (*Runtime, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseRuntime(data)
}
