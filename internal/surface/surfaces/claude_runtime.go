package surfaces

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func claudeBinary(home string) (string, error) {
	if configured := strings.TrimSpace(os.Getenv("AGENTHAIL_CLAUDE_BIN")); configured != "" {
		path, err := exec.LookPath(configured)
		if err != nil {
			return "", fmt.Errorf("AGENTHAIL_CLAUDE_BIN %q is not executable: %w", configured, err)
		}
		return path, nil
	}
	path, err := exec.LookPath("claude")
	if err == nil {
		return path, nil
	}
	if !errors.Is(err, exec.ErrNotFound) {
		return "", fmt.Errorf("locate Claude executable: %w", err)
	}
	installed := filepath.Join(home, ".local", "bin", "claude")
	if path, err := exec.LookPath(installed); err == nil {
		return path, nil
	}
	return "", fmt.Errorf("Claude executable was not found on PATH or at %s; install Claude Code or set AGENTHAIL_CLAUDE_BIN", installed)
}
