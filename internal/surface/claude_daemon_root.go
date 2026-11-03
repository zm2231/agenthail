package surface

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func daemonClaudeRootPath(home string) string {
	return filepath.Join(home, ".agenthail", "daemon-claude-root")
}

// RecordDaemonClaudeRoot saves the daemon's primary root so every process reads it, whatever its own CLAUDE_CONFIG_DIR.
func RecordDaemonClaudeRoot(home string) error {
	root := filepath.Clean(ClaudeConfigDir(home))
	if recorded, err := recordedDaemonClaudeRoot(home); err == nil && recorded == root {
		return nil
	}
	path := daemonClaudeRootPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	staged := path + ".tmp"
	if err := os.WriteFile(staged, []byte(root+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(staged, path)
}

func recordedDaemonClaudeRoot(home string) (string, error) {
	path := daemonClaudeRootPath(home)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read the daemon's Claude config root %s: %w", path, err)
	}
	root := strings.TrimSpace(string(data))
	if root == "" || !filepath.IsAbs(root) {
		return "", fmt.Errorf("the daemon's Claude config root in %s is not an absolute path: %q", path, root)
	}
	return filepath.Clean(root), nil
}
