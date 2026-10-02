package claudeconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Roots keeps the native home discoverable alongside explicitly configured roots.
func Roots(home string) ([]string, error) {
	paths := []string{filepath.Join(home, ".claude")}
	if root := os.Getenv("CLAUDE_CONFIG_DIR"); root != "" {
		paths = append(paths, root)
	}
	paths = append(paths, filepath.SplitList(os.Getenv("AGENTHAIL_CLAUDE_CONFIG_DIRS"))...)
	return Normalize(paths)
}

func Normalize(paths []string) ([]string, error) {
	var roots []string
	seen := map[string]bool{}
	for _, path := range paths {
		if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
			return nil, fmt.Errorf("Claude config directories must be nonempty absolute paths")
		}
		root := filepath.Clean(path)
		resolved, err := filepath.EvalSymlinks(root)
		if err == nil {
			root = resolved
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("resolve Claude config directory %q: %w", path, err)
		}
		if info, err := os.Stat(root); err == nil && !info.IsDir() {
			return nil, fmt.Errorf("Claude config directory %q is not a directory", root)
		} else if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("read Claude config directory %q: %w", root, err)
		}
		if !seen[root] {
			roots = append(roots, root)
			seen[root] = true
		}
	}
	return roots, nil
}
