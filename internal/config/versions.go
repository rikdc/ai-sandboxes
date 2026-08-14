package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// Versions holds the launcher-relevant pins read from versions.env. The rest
// of that file drives the Docker image build and is out of scope here.
type Versions struct{}

// ParseVersions validates the versions.env syntax. It no longer extracts
// agent resource quotas because every agent now carries explicit typed values
// in AgentConfig.
func ParseVersions(data []byte) (*Versions, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.Contains(line, "=") {
			return nil, fmt.Errorf("invalid versions.env line: %q", line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return &Versions{}, nil
}

// LoadVersions reads versions.env from the given checkout path.
func LoadVersions(path string) (*Versions, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseVersions(data)
}