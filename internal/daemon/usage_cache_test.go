package daemon

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

func writeUsageFixture(t *testing.T, files map[string]string) {
	t.Helper()
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUsageReadsClaudeAIOncePerMinuteAcrossPolls(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	writeUsageFixture(t, map[string]string{
		filepath.Join(home, ".claude.json"): `{"oauthAccount":{"accountUuid":"acct-work","emailAddress":"work@example.com","organizationUuid":"org-work"}}`,
	})
	var reads atomic.Int32
	resets := time.Now().Add(3 * time.Hour).UTC().Format(time.RFC3339)
	profiles := []signin.Source{{ProfileID: "sp_work", Label: "work", Browser: signin.Chrome, Dir: "/work"}}
	d.Surfaces = []surface.Surface{surfaces.NewClaudeWithRequest(candidates(profiles), home, func(_ context.Context, _ string, url string, _ map[string]string, _ string, _ surfaces.CookieSource, _ time.Duration) (int, string, error) {
		switch {
		case strings.HasSuffix(url, "/api/account"):
			return 200, `{"uuid":"acct-work"}`, nil
		case strings.HasSuffix(url, "/usage"):
			reads.Add(1)
			return 200, `{"limits":[{"kind":"session","percent":12,"resets_at":"` + resets + `"}]}`, nil
		}
		return 404, "{}", nil
	})}
	api := signinAPIClient{t: t, handler: d.dashboardHandler(&dashboardServer{token: "secret"}), token: "secret"}
	for range 3 {
		var body usageBody
		if code := api.do(http.MethodGet, "/api/v1/usage", "", &body); code != http.StatusOK {
			t.Fatalf("status=%d", code)
		}
		set := body.Sets[0]
		if set.Name != "default" || set.AsOf.IsZero() || len(set.Meters) != 1 {
			t.Fatalf("a set names its root and when its usage was read: %+v", set)
		}
	}
	if got := reads.Load(); got != 1 {
		t.Fatalf("three polls inside a minute read claude.ai %d times, want 1", got)
	}
}

func TestUsageKeepsTheLastReadingWhenClaudeAIFails(t *testing.T) {
	reader := &scriptedRootReader{answers: []scriptedRootAnswer{
		{usage: &surface.Usage{FetchedAt: time.Now().Add(-time.Minute), Limits: []surface.UsageWindow{{Kind: "session", Percent: 40}}}},
		{problem: "claude.ai answered 503"},
	}}
	root := surface.ClaudeConfigRoot{Name: "default", Root: "/r", Primary: true, SignedInAs: &surface.ClaudeLogin{Email: "a@example.com", AccountID: "a"}}
	d := &Daemon{}
	now := time.Now()
	first := d.claudeUsageSet(context.Background(), reader, root, now)
	second := d.claudeUsageSet(context.Background(), reader, root, now.Add(2*claudeUsageTTL))
	if len(first.Meters) != 1 || first.Problem != "" {
		t.Fatalf("first read: %+v", first)
	}
	if len(second.Meters) != 1 || second.Problem != "claude.ai answered 503" || !second.AsOf.Equal(first.AsOf) {
		t.Fatalf("a failed read keeps the last meters, their time and says why: %+v", second)
	}
	other := root
	other.SignedInAs = &surface.ClaudeLogin{Email: "b@example.com", AccountID: "b"}
	reader.answers = append(reader.answers, scriptedRootAnswer{problem: "claude.ai answered 503"})
	if third := d.claudeUsageSet(context.Background(), reader, other, now.Add(2*claudeUsageTTL)); len(third.Meters) != 0 {
		t.Fatalf("another login never shows the previous login's usage: %+v", third)
	}
}

func TestUsageNamesARootWithoutAClaudeAILogin(t *testing.T) {
	d := &Daemon{}
	reader := &scriptedRootReader{}
	set := d.claudeUsageSet(context.Background(), reader, surface.ClaudeConfigRoot{Name: "keys", Root: "/srv/claude"}, time.Now())
	if set.Problem != claudeNoLoginProblem || set.Name != "keys" || set.Meters != nil || reader.calls != 0 {
		t.Fatalf("an API-key root says why it has no usage without asking claude.ai: %+v calls=%d", set, reader.calls)
	}
}

type scriptedRootAnswer struct {
	usage   *surface.Usage
	problem string
}

type scriptedRootReader struct {
	answers []scriptedRootAnswer
	calls   int
}

func (r *scriptedRootReader) ClaudeConfigRoots(context.Context) ([]surface.ClaudeConfigRoot, error) {
	return nil, nil
}

func (r *scriptedRootReader) ClaudeConfigRoot(_ context.Context, path string) (surface.ClaudeConfigRoot, error) {
	answer := r.answers[r.calls]
	r.calls++
	return surface.ClaudeConfigRoot{Root: path, Usage: answer.usage, UsageProblem: answer.problem}, nil
}
