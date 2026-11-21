package daemon

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/browsercookie"
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
	d.Surfaces = []surface.Surface{surfaces.NewClaudeWithRequest(candidates(profiles), home, func(_ context.Context, _ string, url string, _ map[string]string, _ string, _ browsercookie.Source, _ time.Duration) (int, string, error) {
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
	good := scriptedRootAnswer{usage: &surface.Usage{FetchedAt: time.Now().Add(-time.Minute), Limits: []surface.UsageWindow{{Kind: "session", Percent: 40}}}}
	failed := scriptedRootAnswer{problem: "claude.ai could not be asked which profile holds this login"}
	reader := &scriptedRootReader{answers: []scriptedRootAnswer{good, failed, failed, failed, good}}
	root := surface.ClaudeConfigRoot{Name: "default", Root: "/r", Primary: true, SignedInAs: &surface.ClaudeLogin{Email: "a@example.com", AccountID: "a"}}
	d := &Daemon{}
	now := time.Now()
	poll := func(n int) usageSet {
		return d.claudeUsageSet(context.Background(), reader, root, now.Add(time.Duration(n)*claudeUsageTTL))
	}
	first := poll(0)
	if len(first.Meters) != 1 || first.Problem != "" {
		t.Fatalf("first read: %+v", first)
	}
	for n := 1; n < claudeUsageFailuresHeld; n++ {
		if held := poll(n); len(held.Meters) != 1 || held.Problem != "" || !held.AsOf.Equal(first.AsOf) {
			t.Fatalf("failed refresh %d keeps the last meters and their time, without an error: %+v", n, held)
		}
	}
	shown := poll(claudeUsageFailuresHeld)
	if len(shown.Meters) != 1 || shown.Problem != failed.problem || !shown.AsOf.Equal(first.AsOf) {
		t.Fatalf("a failure lasting %d refreshes says why and keeps the last meters: %+v", claudeUsageFailuresHeld, shown)
	}
	if again := poll(claudeUsageFailuresHeld + 1); again.Problem != "" || len(again.Meters) != 1 {
		t.Fatalf("a good read clears the error: %+v", again)
	}
	other := root
	other.SignedInAs = &surface.ClaudeLogin{Email: "b@example.com", AccountID: "b"}
	reader.answers = append(reader.answers, failed)
	if third := d.claudeUsageSet(context.Background(), reader, other, now.Add(10*claudeUsageTTL)); len(third.Meters) != 0 || third.Problem != failed.problem {
		t.Fatalf("a login with no reading yet shows its error at once and never the previous login's usage: %+v", third)
	}
}

func TestUsageNamesHowARootWithoutAClaudeAILoginSignsIn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "")
	keys, out, cloud, token := filepath.Join(home, ".keys"), filepath.Join(home, ".out"), filepath.Join(home, ".cloud"), filepath.Join(home, ".token")
	writeUsageFixture(t, map[string]string{
		filepath.Join(keys, "settings.json"):  `{"apiKeyHelper":"print-key"}`,
		filepath.Join(out, "settings.json"):   `{}`,
		filepath.Join(cloud, "settings.json"): `{"env":{"CLAUDE_CODE_USE_BEDROCK":"1"}}`,
		filepath.Join(token, "settings.json"): `{"env":{"CLAUDE_CODE_OAUTH_TOKEN":"sk-ant-oat-x"}}`,
	})
	d := &Daemon{}
	reader := &scriptedRootReader{}
	for _, c := range []struct {
		root   string
		login  string
		source string
		prefix string
	}{
		{keys, surface.ClaudeLoginAPIKey, surface.ClaudeSourceAPIKey, "Signed in with an API key"},
		{out, surface.ClaudeLoginSignedOut, "", "Not signed in"},
		{cloud, surface.ClaudeLoginAPIKey, surface.ClaudeSourceBedrock, "Signed in with Amazon Bedrock"},
		{token, surface.ClaudeLoginAPIKey, surface.ClaudeSourceOAuthToken, "Signed in with an OAuth token"},
	} {
		set := d.claudeUsageSet(context.Background(), reader, surface.ClaudeConfigRoot{Name: "keys", Root: c.root}, time.Now())
		if set.Login != c.login || set.LoginSource != c.source || !strings.HasPrefix(set.Problem, c.prefix) || set.Detail == "" || set.Account != "" || set.Meters != nil || strings.Contains(set.Problem+set.Detail, "sk-ant") {
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
		_, _, _, err := cache.read(first, reader, root, now)
		firstDone <- err
	}()
	<-reader.started
	secondDone := make(chan *surface.Usage, 1)
	go func() {
		_, usage, _, _ := cache.read(context.Background(), reader, root, now)
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

func TestABurstOfStartsRefreshesAStaleReadingOnce(t *testing.T) {
	var cache claudeUsageCache
	reader := &blockingRootReader{started: make(chan struct{}), release: make(chan struct{})}
	root := surface.ClaudeConfigRoot{Root: "/r", SignedInAs: &surface.ClaudeLogin{AccountID: "a"}}
	before := runtime.NumGoroutine()
	for range 100 {
		if usage := cache.cached(reader, root, time.Now()); usage != nil {
			t.Fatalf("a cold cache has no reading yet: %+v", usage)
		}
	}
	<-reader.started
	if grew := runtime.NumGoroutine() - before; grew > 3 {
		t.Fatalf("100 starts queued %d refreshes behind one claude.ai read", grew)
	}
	close(reader.release)
	deadline := time.Now().Add(5 * time.Second)
	for cache.entry(root.Root).refreshing.Load() {
		if time.Now().After(deadline) {
			t.Fatal("the refresh did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if usage := cache.cached(reader, root, time.Now()); usage == nil || reader.calls.Load() != 1 {
		t.Fatalf("the next start reads the refreshed snapshot: usage=%+v reads=%d", usage, reader.calls.Load())
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
