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
// the login the directory holds now. Every Claude Code process of the root
// uses it from its next request on, including processes that started under an
// earlier login, and a login change drops their Remote Control bridges.
// Signin is the browser sign-in profile signed into that login on claude.ai,
// and Usage its limits as claude.ai reports them; both are filled only by the
// process that reads browser cookies.
type ClaudeConfigRoot struct {
	Name          string       `json:"name"`
	Root          string       `json:"root"`
	Primary       bool         `json:"primary"`
	SignedInAs    *ClaudeLogin `json:"signedInAs,omitempty"`
	Signin        string       `json:"signin,omitempty"`
	SigninProblem string       `json:"signinProblem,omitempty"`
	Usage         *Usage       `json:"usage,omitempty"`
	UsageProblem  string       `json:"usageProblem,omitempty"`
	Error         string       `json:"error,omitempty"`
}

type ClaudeLogin struct {
	Email          string `json:"email,omitempty"`
	Organization   string `json:"organization,omitempty"`
	Plan           string `json:"plan,omitempty"`
	AccountID      string `json:"-"`
	OrganizationID string `json:"-"`
}

// Usage is an account's limits as its provider last reported them, at
// FetchedAt: claude.ai for a Claude login, the newest rollout for Codex.
type Usage struct {
	FetchedAt time.Time     `json:"fetchedAt"`
	Limits    []UsageWindow `json:"limits,omitempty"`
}

type UsageWindow struct {
	Kind     string    `json:"kind"`
	Group    string    `json:"group,omitempty"`
	Scope    string    `json:"scope,omitempty"`
	Percent  float64   `json:"percent"`
	Severity string    `json:"severity,omitempty"`
	ResetsAt time.Time `json:"resetsAt,omitzero"`
}

func (u *Usage) Exhausted(now time.Time) (time.Time, bool) {
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
// ~/.work/.claude is "work".
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
	read     time.Time
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
	if ok && UnchangedSince(entry.read, entry.modified, info.ModTime()) && entry.size == info.Size() {
		return entry.root
	}
	read := time.Now()
	root := parseClaudeGlobalConfig(path)
	c.mu.Lock()
	c.entries[path] = claudeGlobalConfigEntry{modified: info.ModTime(), read: read, size: info.Size(), root: root}
	c.mu.Unlock()
	return root
}

type claudeGlobalConfig struct {
	OAuthAccount *struct {
		AccountUUID      string `json:"accountUuid"`
		EmailAddress     string `json:"emailAddress"`
		OrganizationUUID string `json:"organizationUuid"`
		OrganizationName string `json:"organizationName"`
		OrganizationType string `json:"organizationType"`
	} `json:"oauthAccount"`
}

// parseClaudeGlobalConfig reads the login a root holds. Claude Code's own
// usage cache is not read: it is refreshed only when Claude's usage view runs
// and is dropped when the root signs into another account.
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
	return ClaudeConfigRoot{SignedInAs: &ClaudeLogin{
		Email:          config.OAuthAccount.EmailAddress,
		Organization:   config.OAuthAccount.OrganizationName,
		Plan:           strings.TrimPrefix(config.OAuthAccount.OrganizationType, "claude_"),
		AccountID:      config.OAuthAccount.AccountUUID,
		OrganizationID: config.OAuthAccount.OrganizationUUID,
	}}
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
