// Package runtimepolicy resolves the host-selected runtime policy and checks
// that a loaded base image was built for that policy.
package runtimepolicy

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rikdc/ai-sandboxes/internal/config"
	"github.com/rikdc/ai-sandboxes/internal/plan"
	"github.com/rikdc/ai-sandboxes/internal/runtime/microsandbox"
)

const (
	labelSharedStateID    = "io.ai-sandboxes.shared-state.id"
	labelSharedStateQuota = "io.ai-sandboxes.shared-state.quota"
)

// Resolve returns the requested shared-state policy. An override must be the
// literal "none" or an absolute path, so policy cannot vary with cwd. Without
// an override, runtime.json comes from the user configuration directory
// (AI_SANDBOX_CONFIG_DIR or $XDG_CONFIG_HOME/ai-sandboxes) — never from the
// repository checkout, whose config files are neutral defaults only.
func Resolve(checkout, override string) (*plan.SharedState, error) {
	_ = checkout // The checkout is never a configuration source; kept for signature stability.
	if override == "none" {
		return nil, nil
	}
	var path string
	// Whether the source was explicitly requested. A missing explicit
	// override is a hard error — the whole point of the override is to make
	// the policy source explicit — while a missing default (the user
	// configuration directory's runtime.json) means "never configured".
	explicit := false
	if override != "" {
		if !filepath.IsAbs(override) {
			return nil, fmt.Errorf("AI_SANDBOX_RUNTIME_CONFIG must be an absolute path or \"none\": %q", override)
		}
		path = override
		explicit = true
	} else {
		resolved, err := config.RuntimeConfigPath()
		if err != nil {
			return nil, fmt.Errorf("cannot locate runtime configuration: %w", err)
		}
		path = resolved
	}

	data, err := os.ReadFile(path)
	if err != nil {
		// Only the default source may treat a missing file as "never
		// configured" and adopt the neutral default (no shared state). Any
		// other read failure, or a file that exists but does not parse, fails
		// loudly rather than silently changing policy.
		if !explicit && errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("could not load runtime configuration %s: %w", path, err)
	}
	rt, err := config.ParseRuntime(data)
	if err != nil {
		return nil, fmt.Errorf("could not load runtime configuration %s: %w", path, err)
	}
	if rt.SharedState == nil {
		return nil, nil
	}
	return plan.ParseSharedStateRequest(rt.SharedState.ID, rt.SharedState.Quota)
}

// DockerSharedStateLabels parses the JSON emitted by
// `docker image inspect --format '{{json .Config.Labels}}'`.
func DockerSharedStateLabels(data []byte) (map[string]string, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		return map[string]string{}, nil
	}
	var labels map[string]string
	if err := json.Unmarshal([]byte(trimmed), &labels); err != nil {
		return nil, fmt.Errorf("invalid docker labels JSON: %w", err)
	}
	return labels, nil
}

// ReconcileBaseImage verifies both Docker-to-msb transport identity and the
// image's baked shared-state contract. It must run even when desired is nil.
func ReconcileBaseImage(dockerDigest, msbDigest string, labels map[string]string, desired *plan.SharedState) error {
	if err := microsandbox.MatchDigests(dockerDigest, msbDigest); err != nil {
		return err
	}
	built, err := plan.SharedStateFromLabels(labels)
	if err != nil {
		return err
	}
	if sameSharedState(built, desired) {
		return nil
	}
	builtID, builtQuota := "", ""
	if built != nil {
		builtID, builtQuota = built.ID, built.Quota
	}
	desiredID, desiredQuota := "", ""
	if desired != nil {
		desiredID, desiredQuota = desired.ID, desired.Quota
	}
	return fmt.Errorf("runtime policy requests id=%q quota=%q but image was built with id=%q quota=%q; rebuild with ./scripts/build", desiredID, desiredQuota, builtID, builtQuota)
}

func sameSharedState(a, b *plan.SharedState) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.ID == b.ID && a.Quota == b.Quota
}
