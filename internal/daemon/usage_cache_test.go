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

func TestUsageNamesHowARootWithoutAClaudeAILoginSignsIn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	keys, out := filepath.Join(home, ".keys"), filepath.Join(home, ".out")
	writeUsageFixture(t, map[string]string{
		filepath.Join(keys, "settings.json"): `{"apiKeyHelper":"print-key"}`,
		filepath.Join(out, "settings.json"):  `{}`,
	})
	d := &Daemon{}
	reader := &scriptedRootReader{}
	for _, c := range []struct {
		root   string
		login  string
		prefix string
	}{{keys, surface.ClaudeLoginAPIKey, "Signed in with an API key"}, {out, surface.ClaudeLoginSignedOut, "Not signed in"}} {
		set := d.claudeUsageSet(context.Background(), reader, surface.ClaudeConfigRoot{Name: "keys", Root: c.root}, time.Now())
		if set.Login != c.login || !strings.HasPrefix(set.Problem, c.prefix) || set.Detail == "" || set.Account != "" || set.Meters != nil {
			t.Fatalf("a root without a claude.ai login says how it signs in: %+v", set)
		}
	}
	if reader.calls != 0 {
		t.Fatalf("a root without a claude.ai login never asks claude.ai: %d calls", reader.calls)
	}
}

func TestAScopedWeeklyMeterIsLabelledByItsModel(t *testing.T) {
	now := time.Now()
	usage := &surface.Usage{FetchedAt: now, Limits: []surface.UsageWindow{{Kind: "weekly_scoped", Scope: "Fable", Percent: 40}, {Kind: "weekly_scoped", Percent: 10}}}
	meters := usageMeters("claude", usage, now)
	if len(meters) != 2 || meters[0].Label != "Claude week, Fable" || meters[0].Scope != "Fable" || meters[1].Label != "Claude week, one model" {
		t.Fatalf("scoped meters: %+v", meters)
	}
}

func TestACancelledPollDoesNotFailTheReadAnotherPollWaitsOn(t *testing.T) {
	reader := &blockingRootReader{started: make(chan struct{}), release: make(chan struct{})}
	root := surface.ClaudeConfigRoot{Root: "/r", SignedInAs: &surface.ClaudeLogin{AccountID: "a"}}
	var cache claudeUsageCache
	now := time.Now()
	first, cancel := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() {
		_, _, err := cache.read(first, reader, root, now)
		firstDone <- err
	}()
	<-reader.started
	secondDone := make(chan *surface.Usage, 1)
	go func() {
		_, usage, _ := cache.read(context.Background(), reader, root, now)
		secondDone <- usage
	}()
	cancel()
	close(reader.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("the read outlives the poll that started it: %v", err)
	}
	if usage := <-secondDone; usage == nil || len(usage.Limits) != 1 {
		t.Fatalf("the waiting poll gets the finished reading: %+v", usage)
	}
	if got := reader.calls.Load(); got != 1 {
		t.Fatalf("claude.ai read %d times, want 1", got)
	}
}

type blockingRootReader struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (r *blockingRootReader) ClaudeConfigRoots(context.Context) ([]surface.ClaudeConfigRoot, error) {
	return nil, nil
}

func (r *blockingRootReader) ClaudeConfigRoot(ctx context.Context, path string) (surface.ClaudeConfigRoot, error) {
	if r.calls.Add(1) == 1 {
		close(r.started)
	}
	<-r.release
	if err := ctx.Err(); err != nil {
		return surface.ClaudeConfigRoot{}, err
	}
	return surface.ClaudeConfigRoot{Root: path, Usage: &surface.Usage{Limits: []surface.UsageWindow{{Kind: "session", Percent: 30}}}}, nil
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
