package surface

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeClaudeGlobalConfigFixture(t *testing.T, path string, config map[string]any) {
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
	return map[string]any{"accountUuid": uuid, "emailAddress": email, "organizationUuid": "org-" + uuid, "organizationName": "Org " + uuid, "organizationType": "claude_max", "accessToken": "must-not-surface"}
}

func claudeRootsHome(t *testing.T, roots ...string) string {
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
	writeClaudeGlobalConfigFixture(t, AgenthailConfigPath(home), map[string]any{"claudeConfigDirs": configured})
	return home
}

func signedInAs(root ClaudeConfigRoot) string {
	if root.SignedInAs == nil {
		return ""
	}
	return root.SignedInAs.Email
}

func TestDescribedRootsReadEachRootsCurrentLogin(t *testing.T) {
	home := claudeRootsHome(t, ".work/.claude", "spare")
	writeClaudeGlobalConfigFixture(t, filepath.Join(home, ".claude.json"), map[string]any{"oauthAccount": oauthAccount("a-1", "me@example.com")})
	writeClaudeGlobalConfigFixture(t, filepath.Join(home, ".claude", ".claude.json"), map[string]any{"oauthAccount": oauthAccount("wrong", "wrong@example.com")})
	writeClaudeGlobalConfigFixture(t, filepath.Join(home, ".work", ".claude", ".claude.json"), map[string]any{"oauthAccount": oauthAccount("b-2", "work@example.com")})

	roots, err := DescribeClaudeConfigRoots(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 3 {
		t.Fatalf("roots=%+v", roots)
	}
	primary, work, spare := roots[0], roots[1], roots[2]
	if !primary.Primary || primary.Name != "default" || signedInAs(primary) != "me@example.com" || primary.SignedInAs.Plan != "max" || primary.Root != filepath.Join(home, ".claude") {
		t.Fatalf("primary=%+v", primary)
	}
	if work.Name != "work" || signedInAs(work) != "work@example.com" || work.SignedInAs.AccountID != "b-2" || work.SignedInAs.OrganizationID != "org-b-2" {
		t.Fatalf("work=%+v login=%+v", work, work.SignedInAs)
	}
	if spare.SignedInAs != nil || spare.Name != "spare" {
		t.Fatalf("spare=%+v", spare)
	}
	encoded, _ := json.Marshal(roots)
	if strings.Contains(string(encoded), "must-not-surface") || strings.Contains(string(encoded), `"b-2"`) {
		t.Fatalf("a credential or account id surfaced: %s", encoded)
	}
}

func TestDescribedRootsIgnoreClaudesCachedUsage(t *testing.T) {
	home := claudeRootsHome(t)
	writeClaudeGlobalConfigFixture(t, filepath.Join(home, ".claude.json"), map[string]any{
		"oauthAccount":           oauthAccount("now", "now@example.com"),
		"cachedUsageUtilization": map[string]any{"fetchedAtMs": 1792364047870, "accountUuid": "now", "utilization": map[string]any{"limits": []any{map[string]any{"kind": "session", "percent": 100}}}},
	})
	roots, err := DescribeClaudeConfigRoots(home)
	if err != nil || roots[0].Usage != nil || signedInAs(roots[0]) != "now@example.com" {
		t.Fatalf("Claude's own usage cache is refreshed only by its usage view, so it must not be reported: roots=%+v err=%v", roots, err)
	}
}

func TestClaudeUsageExhaustedUntilTheLatestLimitResets(t *testing.T) {
	now := time.Now()
	resets := now.Add(2 * time.Hour)
	usage := &ClaudeUsage{Limits: []ClaudeUsageLimit{{Kind: "session", Percent: 100, ResetsAt: now.Add(time.Hour)}, {Kind: "weekly_all", Percent: 100, ResetsAt: resets}, {Kind: "weekly_scoped", Percent: 35}}}
	if until, out := usage.Exhausted(now); !out || !until.Equal(resets) {
		t.Fatalf("exhausted=%v until=%v", out, until)
	}
	if _, out := usage.Exhausted(resets.Add(time.Minute)); out {
		t.Fatal("a limit that has reset still reads as exhausted")
	}
}

func TestDescribedRootsUseTheEnvironmentRootsOwnConfig(t *testing.T) {
	home := claudeRootsHome(t)
	root := filepath.Join(home, "env-root")
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	writeClaudeGlobalConfigFixture(t, filepath.Join(home, ".claude.json"), map[string]any{"oauthAccount": oauthAccount("wrong", "wrong@example.com")})
	writeClaudeGlobalConfigFixture(t, filepath.Join(root, ".claude.json"), map[string]any{"oauthAccount": oauthAccount("e-1", "env@example.com")})
	roots, err := DescribeClaudeConfigRoots(home)
	if err != nil || len(roots) != 1 || signedInAs(roots[0]) != "env@example.com" {
		t.Fatalf("roots=%+v err=%v", roots, err)
	}
}

func TestResolveClaudeConfigRoot(t *testing.T) {
	home := claudeRootsHome(t, ".gentle-claude/.claude")
	gentle := filepath.Join(home, ".gentle-claude", ".claude")
	writeClaudeGlobalConfigFixture(t, filepath.Join(gentle, ".claude.json"), map[string]any{"oauthAccount": oauthAccount("g", "other@example.com")})
	for choice, want := range map[string]string{
		"":                         filepath.Join(home, ".claude"),
		"default":                  filepath.Join(home, ".claude"),
		"gentle-claude":            gentle,
		gentle:                     gentle,
		"~/.gentle-claude/.claude": gentle,
	} {
		if got, err := ResolveClaudeConfigRoot(home, choice); err != nil || got != want {
			t.Fatalf("choice %q: got %q err=%v", choice, got, err)
		}
	}
	for _, choice := range []string{"other@example.com", "nobody", filepath.Join(home, "unconfigured")} {
		if _, err := ResolveClaudeConfigRoot(home, choice); err == nil {
			t.Fatalf("choice %q resolved", choice)
		}
	}
}

func TestDescribedRootsRereadAChangedConfig(t *testing.T) {
	home := claudeRootsHome(t)
	path := filepath.Join(home, ".claude.json")
	writeClaudeGlobalConfigFixture(t, path, map[string]any{"oauthAccount": oauthAccount("one", "one@example.com")})
	if roots, _ := DescribeClaudeConfigRoots(home); signedInAs(roots[0]) != "one@example.com" {
		t.Fatalf("roots=%+v", roots)
	}
	writeClaudeGlobalConfigFixture(t, path, map[string]any{"oauthAccount": oauthAccount("two", "second@example.com")})
	if roots, _ := DescribeClaudeConfigRoots(home); signedInAs(roots[0]) != "second@example.com" {
		t.Fatalf("a changed config was not re-read: %+v", roots)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if roots, _ := DescribeClaudeConfigRoots(home); roots[0].SignedInAs != nil {
		t.Fatalf("a removed config still reads as signed in: %+v", roots)
	}
}

func TestTerminalClaudeLaunchRunsUnderTheChosenRoot(t *testing.T) {
	home := claudeRootsHome(t, "spare")
	claude := filepath.Join(home, "claude")
	if err := os.WriteFile(claude, []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_CLAUDE_BIN", claude)
	request := LaunchRequest{Surface: KindClaude, Options: SessionStartOptions{Cwd: home, Message: "hello", ConfigRoot: "spare"}}
	if err := validateLaunchRequest(request, []SurfaceKind{KindClaude, KindCodex}); err != nil {
		t.Fatal(err)
	}
	argv, err := agentArgv(request)
	if err != nil || len(argv) < 3 || argv[0] != "env" || argv[1] != "CLAUDE_CONFIG_DIR="+filepath.Join(home, "spare") || argv[2] != claude {
		t.Fatalf("argv=%q err=%v", argv, err)
	}
	request.Options.ConfigRoot = "default"
	if argv, err := agentArgv(request); err != nil || argv[0] != claude {
		t.Fatalf("primary argv=%q err=%v", argv, err)
	}
	request.Surface = KindCodex
	if err := validateLaunchRequest(request, []SurfaceKind{KindClaude, KindCodex}); err == nil {
		t.Fatal("a Codex launch accepted a Claude config root")
	}
}

func TestClaudeRootsWithOneFolderNameGetDistinctNames(t *testing.T) {
	home := claudeRootsHome(t, "personal/claude", "work/claude", "work/.claude/../x/claude")
	roots, err := DescribeClaudeConfigRoots(home)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, root := range roots {
		if other, taken := names[root.Name]; taken {
			t.Fatalf("%s and %s share the name %q", other, root.Root, root.Name)
		}
		names[root.Name] = root.Root
		if got, err := ResolveClaudeConfigRoot(home, root.Name); err != nil || got != root.Root {
			t.Fatalf("name %q resolved to %q err=%v, want %s", root.Name, got, err, root.Root)
		}
		if !root.Primary {
			if got, err := ResolveClaudeConfigRoot(home, root.Root); err != nil || got != root.Root {
				t.Fatalf("root %q resolved to %q err=%v", root.Root, got, err)
			}
		}
	}
	if roots[1].Name != "personal/claude" || roots[2].Name != "work/claude" {
		t.Fatalf("roots=%+v", roots)
	}
}

func TestASessionsConfigRootIsTheRootHoldingItsTranscript(t *testing.T) {
	home := claudeRootsHome(t, "spare")
	writeClaudeGlobalConfigFixture(t, filepath.Join(home, "spare", ".claude.json"), map[string]any{"oauthAccount": oauthAccount("s", "spare@example.com")})
	roots, err := DescribeClaudeConfigRoots(home)
	if err != nil {
		t.Fatal(err)
	}
	view := ClaudeSessionConfigRoot(roots, filepath.Join(home, "spare", "projects", "-p", "c.jsonl"))
	if view == nil || view.Name != "spare" || view.Root != filepath.Join(home, "spare") || view.SignedInAs != "spare@example.com" {
		t.Fatalf("view=%+v", view)
	}
	if ClaudeSessionConfigRoot(roots, filepath.Join(home, "elsewhere", "projects", "-p", "c.jsonl")) != nil || ClaudeSessionConfigRoot(roots, "") != nil {
		t.Fatal("a transcript outside every root got a config root")
	}
}
