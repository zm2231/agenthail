package surface

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ClaudeConfigDir is the primary Claude config root: the one Claude Code
// itself uses from this environment, and where Agenthail's own peer workers
// write their records.
func ClaudeConfigDir(home string) string {
	if configured := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); configured != "" {
		return configured
	}
	return filepath.Join(home, ".claude")
}

// ClaudeLaunchConfigDir is the CLAUDE_CONFIG_DIR a Claude launched against
// root is given, or "" to leave the launch with the environment's own. The
// primary root is never named explicitly: Claude Code on macOS keys its
// keychain credentials on whether CLAUDE_CONFIG_DIR is set, so naming the
// default directory reads a different credential than an unset variable.
func ClaudeLaunchConfigDir(home, root string) string {
	if root == "" || claudeRootIdentity(filepath.Clean(root)) == claudeRootIdentity(filepath.Clean(ClaudeConfigDir(home))) {
		return ""
	}
	return root
}

func AgenthailConfigPath(home string) string {
	return filepath.Join(home, ".agenthail", "config.json")
}

const maxAgenthailConfigBytes = 64 * 1024

// ClaudeConfigRoots returns every Claude config root Agenthail reads: the
// primary root first, then each claudeConfigDirs entry from the Agenthail
// config. A root named twice, including through a symlink, appears once. A
// config that cannot be read as that list is an error, never an empty list.
func ClaudeConfigRoots(home string) ([]string, error) {
	configured, err := configuredClaudeRoots(home)
	if err != nil {
		return nil, err
	}
	roots := []string{filepath.Clean(ClaudeConfigDir(home))}
	seen := map[string]bool{claudeRootIdentity(roots[0]): true}
	for _, root := range configured {
		if strings.TrimSpace(root) == "" || !filepath.IsAbs(root) {
			return nil, fmt.Errorf("Agenthail config %s: claudeConfigDirs entries must be absolute paths, got %q", AgenthailConfigPath(home), root)
		}
		root = filepath.Clean(root)
		if info, err := os.Stat(root); err == nil && !info.IsDir() {
			return nil, fmt.Errorf("Agenthail config %s: Claude config directory %s is not a directory", AgenthailConfigPath(home), root)
		} else if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("Agenthail config %s: %w", AgenthailConfigPath(home), err)
		}
		identity := claudeRootIdentity(root)
		if seen[identity] {
			continue
		}
		seen[identity] = true
		roots = append(roots, root)
	}
	return roots, nil
}

func configuredClaudeRoots(home string) ([]string, error) {
	path := AgenthailConfigPath(home)
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Agenthail config %s: %w", path, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxAgenthailConfigBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read Agenthail config %s: %w", path, err)
	}
	if len(data) > maxAgenthailConfigBytes {
		return nil, fmt.Errorf("Agenthail config %s exceeds %d bytes", path, maxAgenthailConfigBytes)
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(data, &config); err != nil || config == nil {
		return nil, fmt.Errorf("Agenthail config %s must be a JSON object", path)
	}
	raw, ok := config["claudeConfigDirs"]
	if !ok {
		return nil, nil
	}
	var roots []string
	if err := json.Unmarshal(raw, &roots); err != nil || roots == nil {
		return nil, fmt.Errorf("Agenthail config %s: claudeConfigDirs must be a list of absolute paths", path)
	}
	return roots, nil
}

func claudeRootIdentity(root string) string {
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		return resolved
	}
	return root
}

func ClaudeSessionRecordPaths(roots []string, pid string) []string {
	var paths []string
	for _, root := range roots {
		path := filepath.Join(root, "sessions", pid+".json")
		if _, err := os.Lstat(path); err == nil {
			paths = append(paths, path)
		}
	}
	return paths
}
