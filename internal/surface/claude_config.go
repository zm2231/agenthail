package surface

import (
	"os"
	"path/filepath"
	"strings"
)

func ClaudeConfigDir(home string) string {
	if configured := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); configured != "" {
		return configured
	}
	return filepath.Join(home, ".claude")
}
