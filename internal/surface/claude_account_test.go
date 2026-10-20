package surface

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeClaudeAccountFixture(t *testing.T, path string, config map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func oauthAccount(uuid, email string) map[string]any {
	return map[string]any{"accountUuid": uuid, "emailAddress": email, "organizationName": "Org " + uuid, "organizationType": "claude_max", "accessToken": "must-not-surface"}
}

func claudeAccountHome(t *testing.T, roots ...string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	configured := []string{}
	for _, root := range roots {
		path := filepath.Join(home, root)
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		configured = append(configured, path)
	}
	writeClaudeAccountFixture(t, AgenthailConfigPath(home), map[string]any{"claudeConfigDirs": configured})
	return home
}

func TestClaudeAccountsReadEachRootsGlobalConfig(t *testing.T) {
	home := claudeAccountHome(t, ".work/.claude", "spare")
	resets := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	writeClaudeAccountFixture(t, filepath.Join(home, ".claude.json"), map[string]any{"oauthAccount": oauthAccount("a-1", "me@example.com")})
	writeClaudeAccountFixture(t, filepath.Join(home, ".claude", ".claude.json"), map[string]any{"oauthAccount": oauthAccount("wrong", "wrong@example.com")})
	writeClaudeAccountFixture(t, filepath.Join(home, ".work", ".claude", ".claude.json"), map[string]any{
		"oauthAccount": oauthAccount("b-2", "work@example.com"),
		"cachedUsageUtilization": map[string]any{"fetchedAtMs": 1792364047870, "accountUuid": "b-2", "utilization": map[string]any{"limits": []any{
			map[string]any{"kind": "session", "group": "session", "percent": 100, "severity": "critical", "resets_at": resets.Format(time.RFC3339Nano)},
			map[string]any{"kind": "weekly_scoped", "percent": 35, "scope": map[string]any{"model": map[string]any{"display_name": "Fable"}}},
		}}},
	})

	accounts, err := ClaudeAccounts(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 3 {
		t.Fatalf("accounts=%+v", accounts)
	}
	primary, work, spare := accounts[0], accounts[1], accounts[2]
	if !primary.Primary || primary.Name != "default" || primary.Email != "me@example.com" || primary.Plan != "max" || primary.Root != filepath.Join(home, ".claude") {
		t.Fatalf("primary=%+v", primary)
	}
	if work.Name != "work" || work.Email != "work@example.com" || work.Usage == nil || len(work.Usage.Limits) != 2 || work.Usage.Limits[1].Scope != "Fable" {
		t.Fatalf("work=%+v", work)
	}
	if until, out := work.Usage.Exhausted(time.Now()); !out || !until.Equal(resets) {
		t.Fatalf("exhausted=%v until=%v", out, until)
	}
	if _, out := work.Usage.Exhausted(resets.Add(time.Minute)); out {
		t.Fatal("a limit that has reset still reads as exhausted")
	}
	if spare.SignedIn || spare.Name != "spare" || spare.Label != "spare (signed out)" {
		t.Fatalf("spare=%+v", spare)
	}
	encoded, _ := json.Marshal(accounts)
	if strings.Contains(string(encoded), "must-not-surface") {
		t.Fatalf("a credential field surfaced: %s", encoded)
	}
}

func TestClaudeAccountsUseTheEnvironmentRootsOwnConfig(t *testing.T) {
	home := claudeAccountHome(t)
	root := filepath.Join(home, "env-root")
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	writeClaudeAccountFixture(t, filepath.Join(home, ".claude.json"), map[string]any{"oauthAccount": oauthAccount("wrong", "wrong@example.com")})
	writeClaudeAccountFixture(t, filepath.Join(root, ".claude.json"), map[string]any{"oauthAccount": oauthAccount("e-1", "env@example.com")})
	accounts, err := ClaudeAccounts(home)
	if err != nil || len(accounts) != 1 || accounts[0].Email != "env@example.com" {
		t.Fatalf("accounts=%+v err=%v", accounts, err)
	}
}

func TestClaudeAccountsFlagRootsSignedIntoOneAccount(t *testing.T) {
	home := claudeAccountHome(t, "second")
	writeClaudeAccountFixture(t, filepath.Join(home, ".claude.json"), map[string]any{"oauthAccount": oauthAccount("same", "me@example.com")})
	writeClaudeAccountFixture(t, filepath.Join(home, "second", ".claude.json"), map[string]any{"oauthAccount": oauthAccount("same", "me@example.com")})
	accounts, err := ClaudeAccounts(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts[0].SameAccountAs) != 1 || accounts[0].SameAccountAs[0] != accounts[1].Root || len(accounts[1].SameAccountAs) != 1 {
		t.Fatalf("accounts=%+v", accounts)
	}
	if _, err := ResolveClaudeAccountRoot(home, "me@example.com"); err == nil || !strings.Contains(err.Error(), "more than one") {
		t.Fatalf("an email two roots share resolved: %v", err)
	}
}

func TestResolveClaudeAccountRoot(t *testing.T) {
	home := claudeAccountHome(t, ".gentle-claude/.claude")
	gentle := filepath.Join(home, ".gentle-claude", ".claude")
	writeClaudeAccountFixture(t, filepath.Join(gentle, ".claude.json"), map[string]any{"oauthAccount": oauthAccount("g", "Other@Example.com")})
	for choice, want := range map[string]string{
		"":                         filepath.Join(home, ".claude"),
		"default":                  filepath.Join(home, ".claude"),
		"gentle-claude":            gentle,
		"other@example.com":        gentle,
		gentle:                     gentle,
		"~/.gentle-claude/.claude": gentle,
	} {
		if got, err := ResolveClaudeAccountRoot(home, choice); err != nil || got != want {
			t.Fatalf("choice %q: got %q err=%v", choice, got, err)
		}
	}
	for _, choice := range []string{"nobody@example.com", filepath.Join(home, "unconfigured")} {
		if _, err := ResolveClaudeAccountRoot(home, choice); err == nil {
			t.Fatalf("choice %q resolved", choice)
		}
	}
}

func TestClaudeAccountCacheRereadsAChangedConfig(t *testing.T) {
	home := claudeAccountHome(t)
	path := filepath.Join(home, ".claude.json")
	writeClaudeAccountFixture(t, path, map[string]any{"oauthAccount": oauthAccount("one", "one@example.com")})
	if accounts, _ := ClaudeAccounts(home); accounts[0].Email != "one@example.com" {
		t.Fatalf("accounts=%+v", accounts)
	}
	writeClaudeAccountFixture(t, path, map[string]any{"oauthAccount": oauthAccount("two", "second@example.com")})
	if accounts, _ := ClaudeAccounts(home); accounts[0].Email != "second@example.com" {
		t.Fatalf("a changed config was not re-read: %+v", accounts)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if accounts, _ := ClaudeAccounts(home); accounts[0].SignedIn {
		t.Fatalf("a removed config still reads as signed in: %+v", accounts)
	}
}

func TestTerminalClaudeLaunchRunsUnderTheChosenAccount(t *testing.T) {
	home := claudeAccountHome(t, "spare")
	claude := filepath.Join(home, "claude")
	if err := os.WriteFile(claude, []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_CLAUDE_BIN", claude)
	request := LaunchRequest{Surface: KindClaude, Options: SessionStartOptions{Cwd: home, Message: "hello", Account: "spare"}}
	if err := validateLaunchRequest(request, []SurfaceKind{KindClaude, KindCodex}); err != nil {
		t.Fatal(err)
	}
	argv, err := agentArgv(request)
	if err != nil || len(argv) < 3 || argv[0] != "env" || argv[1] != "CLAUDE_CONFIG_DIR="+filepath.Join(home, "spare") || argv[2] != claude {
		t.Fatalf("argv=%q err=%v", argv, err)
	}
	request.Options.Account = "default"
	if argv, err := agentArgv(request); err != nil || argv[0] != claude {
		t.Fatalf("primary argv=%q err=%v", argv, err)
	}
	request.Surface = KindCodex
	if err := validateLaunchRequest(request, []SurfaceKind{KindClaude, KindCodex}); err == nil {
		t.Fatal("a Codex launch accepted a Claude account")
	}
}
