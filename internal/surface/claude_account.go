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

type ClaudeAccount struct {
	Name          string       `json:"name"`
	Root          string       `json:"root"`
	Label         string       `json:"label"`
	Primary       bool         `json:"primary"`
	SignedIn      bool         `json:"signedIn"`
	Email         string       `json:"email,omitempty"`
	Organization  string       `json:"organization,omitempty"`
	Plan          string       `json:"plan,omitempty"`
	AccountID     string       `json:"-"`
	SameAccountAs []string     `json:"sameAccountAs,omitempty"`
	Usage         *ClaudeUsage `json:"usage,omitempty"`
	Error         string       `json:"error,omitempty"`
}

// Claude Code refreshes its cached usage only when its own usage view runs,
// so FetchedAt can be days old.
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

// Roots signed into the same account name each other in SameAccountAs: moving a
// session between them does not change whose usage it spends.
func ClaudeAccounts(home string) ([]ClaudeAccount, error) {
	roots, err := ClaudeConfigRoots(home)
	if err != nil {
		return nil, err
	}
	accounts := make([]ClaudeAccount, 0, len(roots))
	for i, root := range roots {
		account := claudeAccounts.read(ClaudeGlobalConfigPath(home, root))
		account.Root = root
		account.Primary = i == 0
		account.Name = claudeRootName(root, i == 0)
		accounts = append(accounts, account)
	}
	for i := range accounts {
		accounts[i].Label = ClaudeAccountLabel(accounts, accounts[i])
		for j := range accounts {
			if i != j && accounts[i].AccountID != "" && accounts[i].AccountID == accounts[j].AccountID {
				accounts[i].SameAccountAs = append(accounts[i].SameAccountAs, accounts[j].Root)
			}
		}
	}
	return accounts, nil
}

func ClaudeAccountForRoot(accounts []ClaudeAccount, root string) (ClaudeAccount, bool) {
	if root == "" {
		return ClaudeAccount{}, false
	}
	identity := claudeRootIdentity(filepath.Clean(root))
	for _, account := range accounts {
		if claudeRootIdentity(account.Root) == identity {
			return account, true
		}
	}
	return ClaudeAccount{}, false
}

// ResolveClaudeAccountRoot accepts a root path, its name or the email of
// exactly one root; "" is the primary root.
func ResolveClaudeAccountRoot(home, choice string) (string, error) {
	accounts, err := ClaudeAccounts(home)
	if err != nil {
		return "", err
	}
	choice = strings.TrimSpace(choice)
	if choice == "" {
		return accounts[0].Root, nil
	}
	if filepath.IsAbs(choice) || strings.HasPrefix(choice, "~") {
		path := choice
		if strings.HasPrefix(path, "~/") || path == "~" {
			path = filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
		if account, ok := ClaudeAccountForRoot(accounts, path); ok {
			return account.Root, nil
		}
		return "", fmt.Errorf("Claude config directory %s is not configured; add it to claudeConfigDirs in %s", path, AgenthailConfigPath(home))
	}
	var matches []ClaudeAccount
	for _, account := range accounts {
		if account.Name == choice || strings.EqualFold(account.Email, choice) {
			matches = append(matches, account)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0].Root, nil
	case 0:
		return "", fmt.Errorf("no configured Claude account matches %q; choose one of %s", choice, claudeAccountChoices(accounts))
	default:
		var roots []string
		for _, account := range matches {
			roots = append(roots, account.Root)
		}
		return "", fmt.Errorf("%q matches more than one Claude config directory (%s); name the directory", choice, strings.Join(roots, ", "))
	}
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

func claudeAccountChoices(accounts []ClaudeAccount) string {
	var choices []string
	for _, account := range accounts {
		choices = append(choices, account.Name+" ("+account.Root+", "+account.Label+")")
	}
	return strings.Join(choices, ", ")
}

const maxClaudeGlobalConfigBytes = 16 << 20

type claudeAccountCache struct {
	mu      sync.Mutex
	entries map[string]claudeAccountEntry
}

type claudeAccountEntry struct {
	modified time.Time
	size     int64
	account  ClaudeAccount
}

var claudeAccounts = &claudeAccountCache{entries: map[string]claudeAccountEntry{}}

func (c *claudeAccountCache) read(path string) ClaudeAccount {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return ClaudeAccount{}
	}
	if err != nil {
		return ClaudeAccount{Error: err.Error()}
	}
	c.mu.Lock()
	entry, ok := c.entries[path]
	c.mu.Unlock()
	if ok && entry.modified.Equal(info.ModTime()) && entry.size == info.Size() {
		return entry.account
	}
	account := parseClaudeGlobalConfig(path)
	c.mu.Lock()
	c.entries[path] = claudeAccountEntry{modified: info.ModTime(), size: info.Size(), account: account}
	c.mu.Unlock()
	return account
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

func parseClaudeGlobalConfig(path string) ClaudeAccount {
	file, err := os.Open(path)
	if err != nil {
		return ClaudeAccount{Error: err.Error()}
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxClaudeGlobalConfigBytes+1))
	if err != nil {
		return ClaudeAccount{Error: err.Error()}
	}
	if len(data) > maxClaudeGlobalConfigBytes {
		return ClaudeAccount{Error: fmt.Sprintf("%s exceeds %d bytes", path, maxClaudeGlobalConfigBytes)}
	}
	var config claudeGlobalConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return ClaudeAccount{Error: fmt.Sprintf("read %s: %v", path, err)}
	}
	if config.OAuthAccount == nil || config.OAuthAccount.AccountUUID == "" {
		return ClaudeAccount{}
	}
	account := ClaudeAccount{
		SignedIn:     true,
		Email:        config.OAuthAccount.EmailAddress,
		Organization: config.OAuthAccount.OrganizationName,
		Plan:         strings.TrimPrefix(config.OAuthAccount.OrganizationType, "claude_"),
		AccountID:    config.OAuthAccount.AccountUUID,
	}
	if usage := config.CachedUsage; usage != nil && usage.AccountUUID == account.AccountID && usage.FetchedAtMs > 0 {
		account.Usage = &ClaudeUsage{FetchedAt: time.UnixMilli(int64(usage.FetchedAtMs)).UTC()}
		for _, limit := range usage.Utilization.Limits {
			out := ClaudeUsageLimit{Kind: limit.Kind, Group: limit.Group, Percent: limit.Percent, Severity: limit.Severity}
			if limit.Scope != nil && limit.Scope.Model != nil {
				out.Scope = limit.Scope.Model.DisplayName
			}
			if at, err := time.Parse(time.RFC3339Nano, limit.ResetsAt); err == nil {
				out.ResetsAt = at.UTC()
			}
			account.Usage.Limits = append(account.Usage.Limits, out)
		}
	}
	return account
}

// SessionAccount is the account a Claude session spends: the one signed into
// the config root that holds its transcript.
type SessionAccount struct {
	Name  string `json:"name"`
	Root  string `json:"root"`
	Email string `json:"email,omitempty"`
	Label string `json:"label"`
}

func ClaudeSessionAccount(accounts []ClaudeAccount, transcript string) *SessionAccount {
	if transcript == "" {
		return nil
	}
	account, ok := ClaudeAccountForRoot(accounts, filepath.Dir(filepath.Dir(filepath.Dir(transcript))))
	if !ok {
		return nil
	}
	return &SessionAccount{Name: account.Name, Root: account.Root, Email: account.Email, Label: account.Label}
}

// ClaudeAccountLabel is the email when it tells the roots apart, and the
// root's name beside it when another root shares the email.
func ClaudeAccountLabel(accounts []ClaudeAccount, account ClaudeAccount) string {
	if !account.SignedIn {
		return account.Name + " (signed out)"
	}
	if account.Email == "" {
		return account.Name
	}
	for _, other := range accounts {
		if other.Root != account.Root && strings.EqualFold(other.Email, account.Email) {
			return account.Name + " · " + account.Email
		}
	}
	return account.Email
}
