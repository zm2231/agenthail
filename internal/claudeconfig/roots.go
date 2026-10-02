package claudeconfig

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Roots keeps the native home discoverable alongside explicitly configured roots.
func Roots(home string) ([]string, error) {
	paths := []string{filepath.Join(home, ".claude")}
	saved, err := savedRoots(home)
	if err != nil {
		return nil, err
	}
	paths = append(paths, saved...)
	if root := os.Getenv("CLAUDE_CONFIG_DIR"); root != "" {
		paths = append(paths, root)
	}
	paths = append(paths, filepath.SplitList(os.Getenv("AGENTHAIL_CLAUDE_CONFIG_DIRS"))...)
	return Normalize(paths)
}

func savedRoots(home string) ([]string, error) {
	path := filepath.Join(home, ".agenthail", "config.json")
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Agenthail config %q: %w", path, err)
	}
	defer file.Close()
	const limit = 64 * 1024
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read Agenthail config %q: %w", path, err)
	}
	if len(data) > limit {
		return nil, fmt.Errorf("Agenthail config %q exceeds %d bytes", path, limit)
	}
	var config *struct {
		ClaudeConfigDirs []string `json:"claudeConfigDirs"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parse Agenthail config %q: %w", path, err)
	}
	if config == nil {
		return nil, fmt.Errorf("Agenthail config %q must be a JSON object", path)
	}
	return config.ClaudeConfigDirs, nil
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
