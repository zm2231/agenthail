package surface

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ClaudeConfigRoot is one configured Claude config directory. SignedInAs is
// the login the directory holds now: Claude Code processes started from it
// later use that login, while running ones keep the login they started with,
// so it says nothing about which account an existing session spends.
type ClaudeConfigRoot struct {
	Name       string       `json:"name"`
	Root       string       `json:"root"`
	Primary    bool         `json:"primary"`
	SignedInAs *ClaudeLogin `json:"signedInAs,omitempty"`
	Usage      *ClaudeUsage `json:"usage,omitempty"`
	Error      string       `json:"error,omitempty"`
}

type ClaudeLogin struct {
	Email        string `json:"email,omitempty"`
	Organization string `json:"organization,omitempty"`
	Plan         string `json:"plan,omitempty"`
	AccountID    string `json:"-"`
}

// ClaudeUsage is the usage Claude Code last cached for a root's current
// login. Claude Code refreshes it only when its own usage view runs, so
// FetchedAt can be days old.
type ClaudeUsage struct {
	FetchedAt time.Time          `json:"fetchedAt"`
	Limits    []ClaudeUsageLimit `json:"limits,omitempty"`
}

type ClaudeUsageLimit struct {
	Kind     string    `json:"kind"`
	Group    string    `json:"group,omitempty"`
	Scope    string    `json:"scope,omitempty"`
	Percent  float64   `json:"percent"`
	Severity string    `json:"severity,omitempty"`
	ResetsAt time.Time `json:"resetsAt,omitzero"`
}

func (u *ClaudeUsage) Exhausted(now time.Time) (time.Time, bool) {
	if u == nil {
		return time.Time{}, false
	}
	var until time.Time
	for _, limit := range u.Limits {
		if limit.Percent >= 100 && limit.ResetsAt.After(now) && limit.ResetsAt.After(until) {
			until = limit.ResetsAt
		}
	}
	return until, !until.IsZero()
}

// ClaudeGlobalConfigPath is the global config Claude Code reads for root:
// <root>/.claude.json when the launch names the root, and the home
// directory's .claude.json for the primary root launched with
// CLAUDE_CONFIG_DIR unset.
func ClaudeGlobalConfigPath(home, root string) string {
	if dir := ClaudeLaunchConfigDir(home, root); dir != "" {
		return filepath.Join(dir, ".claude.json")
	}
	if configured := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); configured != "" {
		return filepath.Join(configured, ".claude.json")
	}
	return filepath.Join(home, ".claude.json")
}

// DescribeClaudeConfigRoots lists the configured roots, primary first, each
// with a name unique among them and the login it currently holds.
func DescribeClaudeConfigRoots(home string) ([]ClaudeConfigRoot, error) {
	paths, err := ClaudeConfigRoots(home)
	if err != nil {
		return nil, err
	}
	roots := make([]ClaudeConfigRoot, 0, len(paths))
	for i, path := range paths {
		root := claudeGlobalConfigs.read(ClaudeGlobalConfigPath(home, path))
		root.Root = path
		root.Primary = i == 0
		root.Name = claudeRootName(path, i == 0)
		roots = append(roots, root)
	}
	uniqueClaudeRootNames(roots)
	return roots, nil
}

func ClaudeConfigRootFor(roots []ClaudeConfigRoot, path string) (ClaudeConfigRoot, bool) {
	if path == "" {
		return ClaudeConfigRoot{}, false
	}
	identity := claudeRootIdentity(filepath.Clean(path))
	for _, root := range roots {
		if claudeRootIdentity(root.Root) == identity {
			return root, true
		}
	}
	return ClaudeConfigRoot{}, false
}

// ResolveClaudeConfigRoot accepts a configured root's path or name; "" is
// the primary root.
func ResolveClaudeConfigRoot(home, choice string) (string, error) {
	roots, err := DescribeClaudeConfigRoots(home)
	if err != nil {
		return "", err
	}
	choice = strings.TrimSpace(choice)
	if choice == "" {
		return roots[0].Root, nil
	}
	if filepath.IsAbs(choice) || strings.HasPrefix(choice, "~") {
		path := choice
		if strings.HasPrefix(path, "~/") || path == "~" {
			path = filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
		if root, ok := ClaudeConfigRootFor(roots, path); ok {
			return root.Root, nil
		}
		return "", fmt.Errorf("Claude config directory %s is not configured; add it to claudeConfigDirs in %s", path, AgenthailConfigPath(home))
	}
	for _, root := range roots {
		if root.Name == choice {
			return root.Root, nil
		}
	}
	return "", fmt.Errorf("no configured Claude config directory is named %q; choose one of %s", choice, claudeConfigRootChoices(roots))
}

// claudeRootName is "default" for the primary root and otherwise the root's
// directory name, or its parent's when the root is a .claude directory, so
// ~/.gentle-claude/.claude is "gentle-claude".
func claudeRootName(root string, primary bool) string {
	if primary {
		return "default"
	}
	name := filepath.Base(root)
	if name == ".claude" {
		name = filepath.Base(filepath.Dir(root))
	}
	return strings.TrimLeft(name, ".")
}

// uniqueClaudeRootNames prefixes a name two roots share with their parent
// folders until each is distinct, so /a/work/.claude and /b/work/.claude are
// a/work and b/work.
func uniqueClaudeRootNames(roots []ClaudeConfigRoot) {
	for depth := 1; depth < 64; depth++ {
		count := map[string]int{}
		for _, root := range roots {
			count[root.Name]++
		}
		changed := false
		for i := range roots {
			if count[roots[i].Name] > 1 && !roots[i].Primary {
				roots[i].Name = claudeRootNameAt(roots[i].Root, depth)
				changed = true
			}
		}
		if !changed {
			return
		}
	}
}

func claudeRootNameAt(root string, depth int) string {
	dir := root
	if filepath.Base(dir) == ".claude" {
		dir = filepath.Dir(dir)
	}
	parts := []string{strings.TrimLeft(filepath.Base(dir), ".")}
	for i := 0; i < depth && filepath.Dir(dir) != dir; i++ {
		dir = filepath.Dir(dir)
		parts = append([]string{strings.TrimLeft(filepath.Base(dir), ".")}, parts...)
	}
	return strings.Join(parts, "/")
}

func claudeConfigRootChoices(roots []ClaudeConfigRoot) string {
	var choices []string
	for _, root := range roots {
		choices = append(choices, root.Name+" ("+root.Root+")")
	}
	return strings.Join(choices, ", ")
}

const maxClaudeGlobalConfigBytes = 16 << 20

type claudeGlobalConfigCache struct {
	mu      sync.Mutex
	entries map[string]claudeGlobalConfigEntry
}

type claudeGlobalConfigEntry struct {
	modified time.Time
	size     int64
	root     ClaudeConfigRoot
}

var claudeGlobalConfigs = &claudeGlobalConfigCache{entries: map[string]claudeGlobalConfigEntry{}}

func (c *claudeGlobalConfigCache) read(path string) ClaudeConfigRoot {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return ClaudeConfigRoot{}
	}
	if err != nil {
		return ClaudeConfigRoot{Error: err.Error()}
	}
	c.mu.Lock()
	entry, ok := c.entries[path]
	c.mu.Unlock()
	if ok && entry.modified.Equal(info.ModTime()) && entry.size == info.Size() {
		return entry.root
	}
	root := parseClaudeGlobalConfig(path)
	c.mu.Lock()
	c.entries[path] = claudeGlobalConfigEntry{modified: info.ModTime(), size: info.Size(), root: root}
	c.mu.Unlock()
	return root
}

type claudeGlobalConfig struct {
	OAuthAccount *struct {
		AccountUUID      string `json:"accountUuid"`
		EmailAddress     string `json:"emailAddress"`
		OrganizationName string `json:"organizationName"`
		OrganizationType string `json:"organizationType"`
	} `json:"oauthAccount"`
	CachedUsage *struct {
		FetchedAtMs float64 `json:"fetchedAtMs"`
		AccountUUID string  `json:"accountUuid"`
		Utilization struct {
			Limits []struct {
				Kind     string  `json:"kind"`
				Group    string  `json:"group"`
				Percent  float64 `json:"percent"`
				Severity string  `json:"severity"`
				ResetsAt string  `json:"resets_at"`
				Scope    *struct {
					Model *struct {
						DisplayName string `json:"display_name"`
					} `json:"model"`
				} `json:"scope"`
			} `json:"limits"`
		} `json:"utilization"`
	} `json:"cachedUsageUtilization"`
}

// parseClaudeGlobalConfig reads the login a root holds and the usage cached
// for that same login; usage cached for an earlier login is dropped.
func parseClaudeGlobalConfig(path string) ClaudeConfigRoot {
	file, err := os.Open(path)
	if err != nil {
		return ClaudeConfigRoot{Error: err.Error()}
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxClaudeGlobalConfigBytes+1))
	if err != nil {
		return ClaudeConfigRoot{Error: err.Error()}
	}
	if len(data) > maxClaudeGlobalConfigBytes {
		return ClaudeConfigRoot{Error: fmt.Sprintf("%s exceeds %d bytes", path, maxClaudeGlobalConfigBytes)}
	}
	var config claudeGlobalConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return ClaudeConfigRoot{Error: fmt.Sprintf("read %s: %v", path, err)}
	}
	if config.OAuthAccount == nil || config.OAuthAccount.AccountUUID == "" {
		return ClaudeConfigRoot{}
	}
	login := &ClaudeLogin{
		Email:        config.OAuthAccount.EmailAddress,
		Organization: config.OAuthAccount.OrganizationName,
		Plan:         strings.TrimPrefix(config.OAuthAccount.OrganizationType, "claude_"),
		AccountID:    config.OAuthAccount.AccountUUID,
	}
	root := ClaudeConfigRoot{SignedInAs: login}
	if usage := config.CachedUsage; usage != nil && usage.AccountUUID == login.AccountID && usage.FetchedAtMs > 0 {
		root.Usage = &ClaudeUsage{FetchedAt: time.UnixMilli(int64(usage.FetchedAtMs)).UTC()}
		for _, limit := range usage.Utilization.Limits {
			out := ClaudeUsageLimit{Kind: limit.Kind, Group: limit.Group, Percent: limit.Percent, Severity: limit.Severity}
			if limit.Scope != nil && limit.Scope.Model != nil {
				out.Scope = limit.Scope.Model.DisplayName
			}
			if at, err := time.Parse(time.RFC3339Nano, limit.ResetsAt); err == nil {
				out.ResetsAt = at.UTC()
			}
			root.Usage.Limits = append(root.Usage.Limits, out)
		}
	}
	return root
}

// SessionConfigRoot is the config root holding a Claude session's
// transcript, with the login that root holds now.
type SessionConfigRoot struct {
	Name       string `json:"name"`
	Root       string `json:"root"`
	SignedInAs string `json:"signedInAs,omitempty"`
}

func ClaudeSessionConfigRoot(roots []ClaudeConfigRoot, transcript string) *SessionConfigRoot {
	if transcript == "" {
		return nil
	}
	root, ok := ClaudeConfigRootFor(roots, filepath.Dir(filepath.Dir(filepath.Dir(transcript))))
	if !ok {
		return nil
	}
	view := &SessionConfigRoot{Name: root.Name, Root: root.Root}
	if root.SignedInAs != nil {
		view.SignedInAs = root.SignedInAs.Email
	}
	return view
}
