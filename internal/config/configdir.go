package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// UserConfigDir returns the user-owned ai-sandboxes configuration directory:
// $AI_SANDBOX_CONFIG_DIR when set, otherwise $XDG_CONFIG_HOME/ai-sandboxes
// (defaulting XDG_CONFIG_HOME to $HOME/.config). It mirrors the shell-side
// resolution in scripts/lib/config-dir.sh so both agree on one location. The
// directory is created with mode 0700 when missing.
func UserConfigDir() (string, error) {
	dir := os.Getenv("AI_SANDBOX_CONFIG_DIR")
	if dir == "" {
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("cannot determine home directory for the ai-sandboxes configuration; set AI_SANDBOX_CONFIG_DIR explicitly: %w", err)
			}
			base = filepath.Join(home, ".config")
		}
		dir = filepath.Join(base, "ai-sandboxes")
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("ai-sandboxes configuration directory must be an absolute path: %q", dir)
	}
	for _, r := range dir {
		if r == '\n' {
			return "", fmt.Errorf("ai-sandboxes configuration directory contains a newline: %q", dir)
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("could not create configuration directory %s: %w", dir, err)
	}
	return dir, nil
}

// RuntimeConfigPath resolves the effective runtime configuration path without
// consulting the AI_SANDBOX_RUNTIME_CONFIG override (callers handle that
// explicit override themselves): the user configuration directory's
// runtime.json. It does not require the file to exist.
func RuntimeConfigPath() (string, error) {
	dir, err := UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "runtime.json"), nil
}
