package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestAccountsListsEachClaudeRootsAccount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	second := filepath.Join(home, ".work", ".claude")
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(home, ".agenthail", "config.json"), `{"claudeConfigDirs":["`+second+`"]}`)
	write(filepath.Join(home, ".claude.json"), `{"oauthAccount":{"accountUuid":"a","emailAddress":"me@example.com","organizationType":"claude_max"}}`)
	write(filepath.Join(second, ".claude.json"), `{"oauthAccount":{"accountUuid":"a","emailAddress":"me@example.com"}}`)

	app, _ := cliFixture(t, &cliSurface{kind: "claude"})
	output, err := captureStdout(t, func() error { return app.Run([]string{"accounts", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Claude []surface.ClaudeAccount `json:"claude"`
	}
	if err := json.Unmarshal([]byte(output), &report); err != nil || len(report.Claude) != 2 {
		t.Fatalf("output=%s err=%v", output, err)
	}
	if report.Claude[1].Name != "work" || report.Claude[1].Email != "me@example.com" || len(report.Claude[1].SameAccountAs) != 1 {
		t.Fatalf("accounts=%+v", report.Claude)
	}
	text, err := captureStdout(t, func() error { return app.Run([]string{"accounts"}) })
	if err != nil || !strings.Contains(text, "default (primary)") || !strings.Contains(text, "same account as "+filepath.Join(home, ".claude")) {
		t.Fatalf("text=%s err=%v", text, err)
	}
}

func TestDescribeClaudeAccountNamesAnExhaustedLimit(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	account := surface.ClaudeAccount{SignedIn: true, Email: "me@example.com", Plan: "max", Usage: &surface.ClaudeUsage{FetchedAt: now, Limits: []surface.ClaudeUsageLimit{{Kind: "session", Percent: 100, ResetsAt: now.Add(time.Hour)}}}}
	got := describeClaudeAccount(account, now)
	if !strings.Contains(got, "me@example.com (max)") || !strings.Contains(got, "out of usage until") || !strings.Contains(got, "session 100%") {
		t.Fatalf("got %q", got)
	}
	if got := describeClaudeAccount(surface.ClaudeAccount{}, now); got != "signed out" {
		t.Fatalf("got %q", got)
	}
}
