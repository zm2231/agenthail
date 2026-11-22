package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/proxyacct/proxyaccttest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

type limitSurface struct {
	*idleSurface
	usage map[string]*surface.Usage
}

func (s *limitSurface) ClaudeConfigRoots(context.Context) ([]surface.ClaudeConfigRoot, error) {
	return nil, nil
}

func (s *limitSurface) ClaudeConfigRoot(_ context.Context, path string) (surface.ClaudeConfigRoot, error) {
	return surface.ClaudeConfigRoot{Root: path, Usage: s.usage[path]}, nil
}

type preLimitFixture struct {
	t     *testing.T
	home  string
	now   time.Time
	reg   *registry.Registry
	fake  *limitSurface
	d     *Daemon
	roots map[string]string
}

func newPreLimitFixture(t *testing.T, names []string, proxied, proxy string) *preLimitFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("ANTHROPIC_BASE_URL", "")
	f := &preLimitFixture{t: t, home: home, now: time.Now(), roots: map[string]string{}}
	files := map[string]string{}
	var dirs []string
	for i, name := range names {
		root := filepath.Join(home, "."+name, ".claude")
		f.roots[name] = root
		if name == proxied {
			dirs = append(dirs, fmt.Sprintf(`{"path":%q,"proxy":"shared"}`, root))
			files[filepath.Join(root, ".claude.json")] = `{}`
			continue
		}
		dirs = append(dirs, fmt.Sprintf("%q", root))
		files[filepath.Join(root, ".claude.json")] = fmt.Sprintf(`{"oauthAccount":{"accountUuid":"acct-%d","emailAddress":"%s@example.com","organizationUuid":"org-%d"}}`, i, name, i)
	}
	config := `{"claudeConfigDirs":[` + joinComma(dirs) + `]`
	if proxy != "" {
		config += `,"proxies":[{"name":"shared","url":"` + proxy + `","management":{"file":"` + proxyaccttest.KeyFile(t, home, proxyaccttest.Key, 0o600) + `"}}]`
	}
	files[filepath.Join(home, ".agenthail", "config.json")] = config + `}`
	files[filepath.Join(home, ".claude.json")] = `{}`
	writeUsageFixture(t, files)
	reg, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reg.Close() })
	f.reg = reg
	f.fake = &limitSurface{idleSurface: &idleSurface{daemonSurface: &daemonSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{}, observations: map[string]*surface.TurnObservation{}, accepted: true, caps: surface.Capabilities{Send: true, Compact: true}}, states: map[string]surface.IdleCompactionState{}}, usage: map[string]*surface.Usage{}}
	f.d = newTestDaemon(t, reg, []surface.Surface{f.fake})
	return f
}

func joinComma(items []string) string {
	out := ""
	for i, item := range items {
		if i > 0 {
			out += ","
		}
		out += item
	}
	return out
}

func (f *preLimitFixture) limit(name, kind string, percent float64, resetsIn time.Duration) {
	f.fake.usage[f.roots[name]] = &surface.Usage{FetchedAt: f.now, Limits: []surface.UsageWindow{{Kind: kind, Percent: percent, ResetsAt: f.now.Add(resetsIn)}}}
}

func (f *preLimitFixture) readUsage() {
	f.t.Helper()
	for _, root := range f.d.currentClaudeConfigRoots() {
		if root.Proxy == "" {
			f.d.claudeUsage.read(context.Background(), f.fake, root, f.now)
		}
	}
}

func (f *preLimitFixture) session(id, name string, tokens int64, idle time.Duration) {
	f.t.Helper()
	transcript := filepath.Join(f.roots[name], "projects", "work", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
		f.t.Fatal(err)
	}
	session := surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Status: surface.StatusIdle, Transcript: transcript}
	if err := f.reg.RecordObservedSession(session); err != nil {
		f.t.Fatal(err)
	}
	raw, _ := json.Marshal(session)
	if _, _, err := f.reg.RecordCatalogSession(registry.CatalogSessionState{Session: session, Project: json.RawMessage(`null`), Checkout: json.RawMessage(`{}`), ObservedAt: f.now, ProjectionFingerprint: string(raw)}, registry.CatalogEvent{DedupeKey: id, Type: "session.upserted", EntityID: id, Payload: json.RawMessage(`{}`)}); err != nil {
		f.t.Fatal(err)
	}
	f.fake.sessions[id] = session
	f.fake.states[id] = surface.IdleCompactionState{LastRequestAt: f.now.Add(-idle), OneHourCache: true, ContextTokens: tokens}
}

func (f *preLimitFixture) compacted() map[string]int {
	f.t.Helper()
	counts := map[string]int{}
	for id := range f.fake.sessions {
		history, err := f.reg.ListHistory(50, id)
		if err != nil {
			f.t.Fatal(err)
		}
		for _, entry := range history {
			if entry.Kind == "queued" && entry.Message == "idle compact" {
				counts[id]++
			}
		}
	}
	return counts
}

func (f *preLimitFixture) expect(want map[string]int) {
	f.t.Helper()
	got := f.compacted()
	for id := range f.fake.sessions {
		if got[id] != want[id] {
			f.t.Errorf("%s: %d compactions queued, want %d (all: %v)", id, got[id], want[id], got)
		}
	}
}

func TestARootNearItsLimitCompactsItsLargestWarmSessionsOneAtATime(t *testing.T) {
	f := newPreLimitFixture(t, []string{"near", "weekly", "soon", "past", "under", "off", "later"}, "", "")
	for name, percent := range map[string]int{"off": 0, "later": 95} {
		if _, err := surface.WriteClaudeCompactSettings(f.home, f.roots[name], surface.ClaudeCompactChange{AgenthailPreLimitPercent: &percent}); err != nil {
			t.Fatal(err)
		}
	}
	f.limit("near", "session", 93, 3*time.Hour)
	f.limit("weekly", "weekly", 91, 30*time.Hour)
	f.limit("soon", "session", 93, 20*time.Minute)
	f.limit("past", "session", 98.5, 3*time.Hour)
	f.limit("under", "session", 85, 3*time.Hour)
	f.limit("off", "session", 93, 3*time.Hour)
	f.limit("later", "session", 93, 3*time.Hour)
	f.readUsage()
	f.session("big", "near", 300_000, 10*time.Minute)
	f.session("bigger", "near", 400_000, 20*time.Minute)
	f.session("small", "near", 150_000, 10*time.Minute)
	f.session("week", "weekly", 300_000, 10*time.Minute)
	f.session("resets-first", "soon", 300_000, 10*time.Minute)
	f.session("too-near", "past", 300_000, 10*time.Minute)
	f.session("room-left", "under", 300_000, 10*time.Minute)
	f.session("turned-off", "off", 300_000, 10*time.Minute)
	f.session("set-higher", "later", 300_000, 10*time.Minute)

	read := map[string]idleTranscript{}
	f.d.compactIdle(context.Background(), f.now, read)
	f.d.compactIdle(context.Background(), f.now.Add(time.Minute), read)
	f.expect(map[string]int{"bigger": 1, "week": 1})

	compacting := f.fake.sessions["bigger"]
	f.d.drainMessageQueue(context.Background(), f.fake, &compacting)
	testkit.Eventually(t, "the first compact to run", 5*time.Second, func() bool { return f.reg.QueueCount("bigger") == 0 })
	state := f.fake.states["bigger"]
	state.CompactedSince = true
	f.fake.states["bigger"] = state
	f.fake.sessions["bigger"] = compacting
	f.d.compactIdle(context.Background(), f.now.Add(2*time.Minute), map[string]idleTranscript{})
	f.expect(map[string]int{"bigger": 1, "big": 1, "week": 1})
}

func TestARootThroughAProxyCompactsByTheProxysFillNotOneAccounts(t *testing.T) {
	proxy := proxyaccttest.New(t)
	now := time.Now().UTC()
	reset := now.Add(3 * time.Hour).Unix()
	account := func(name string, used float64) string {
		return fmt.Sprintf(`{"name":"%s.json","provider":"claude","email":"%s@example.com","status":"active","quota_ceiling":{"5h":60,"7d":60},
  "quota":{"observed_at":%q,"signals":{"Anthropic-Ratelimit-Unified-5h-Utilization":"%.4f","Anthropic-Ratelimit-Unified-5h-Reset":"%d","Anthropic-Ratelimit-Unified-7d-Utilization":"0.10","Anthropic-Ratelimit-Unified-7d-Reset":"%d"}}}`,
			name, name, now.Add(-time.Minute).Format(time.RFC3339), used, reset, now.Add(50*time.Hour).Unix())
	}
	proxy.Files.Store(`{"files":[` + account("claude-full", 0.59) + `,` + account("claude-room", 0.558) + `]}`)
	f := newPreLimitFixture(t, []string{"pool"}, "pool", proxy.Server.URL)
	f.session("pooled", "pool", 300_000, 10*time.Minute)
	testkit.Eventually(t, "the proxy reading", 20*time.Second, func() bool {
		return f.d.readProxyView(time.Now()).readings["shared"] != nil
	})

	f.d.compactIdle(context.Background(), f.now, map[string]idleTranscript{})
	f.expect(map[string]int{"pooled": 1})
}

func TestASessionReachingItsIdleCompactionWaitsForAPreLimitCompactUnderItsRoot(t *testing.T) {
	f := newPreLimitFixture(t, []string{"near"}, "", "")
	f.limit("near", "session", 93, 3*time.Hour)
	f.readUsage()
	f.session("first", "near", 400_000, 20*time.Minute)
	f.session("second", "near", 300_000, 45*time.Minute)

	f.d.compactIdle(context.Background(), f.now, map[string]idleTranscript{})
	f.d.compactIdle(context.Background(), f.now.Add(6*time.Minute), map[string]idleTranscript{})
	f.expect(map[string]int{"first": 1})

	compacting := f.fake.sessions["first"]
	f.d.drainMessageQueue(context.Background(), f.fake, &compacting)
	testkit.Eventually(t, "the first compact to run", 5*time.Second, func() bool { return f.reg.QueueCount("first") == 0 })
	state := f.fake.states["first"]
	state.CompactedSince = true
	f.fake.states["first"] = state
	f.fake.sessions["first"] = compacting
	f.d.compactIdle(context.Background(), f.now.Add(7*time.Minute), map[string]idleTranscript{})
	f.expect(map[string]int{"first": 1, "second": 1})
}
