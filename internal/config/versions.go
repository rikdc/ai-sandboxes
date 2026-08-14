package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// Versions holds the launcher-relevant pins read from versions.env. The rest
// of that file drives the Docker image build and is out of scope here.
type Versions struct {
	WorkspaceQuota string
}

// ParseVersions extracts and validates WORKSPACE_QUOTA from versions.env.
func ParseVersions(data []byte) (*Versions, error) {
	v := &Versions{}
	found := false
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if key != "WORKSPACE_QUOTA" {
			continue
		}
		if !sharedStateQuotaRE.MatchString(value) {
			return nil, fmt.Errorf("WORKSPACE_QUOTA must be a positive K, M, G, or T size: %q", value)
		}
		v.WorkspaceQuota = value
		found = true
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("WORKSPACE_QUOTA is not set")
	}
	return v, nil
}

// LoadVersions reads versions.env from the given checkout path.
func LoadVersions(path string) (*Versions, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseVersions(data)
}