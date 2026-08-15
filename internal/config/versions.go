package config

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Versions holds the launcher-relevant pins read from versions.env. The rest
// of that file drives the Docker image build and is out of scope here.
type Versions struct{}

// versionsLineRE matches KEY=VALUE where KEY is a shell-safe identifier. A
// bare `strings.Contains(line, "=")` accepted things like `foo bar=baz` or
// `=value` as "valid", which is not what versions.env is; enforce the real
// shell-assignment shape so a typo fails loudly.
var versionsLineRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=.*$`)

// ParseVersions validates the versions.env syntax. It no longer extracts
// agent resource quotas because every agent now carries explicit typed values
// in AgentConfig.
func ParseVersions(data []byte) (*Versions, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !versionsLineRE.MatchString(line) {
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
