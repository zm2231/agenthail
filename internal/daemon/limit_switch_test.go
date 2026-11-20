package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/proxyacct/proxyaccttest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

// sharedProxy serves one Claude account whose session, week and Fable week
// stand at the given utilizations, each under a 60% ceiling.
func sharedProxy(t *testing.T, session, week, fableWeek float64) *proxyaccttest.Proxy {
	t.Helper()
	proxy := proxyaccttest.New(t)
	now := time.Now().UTC()
	proxy.Files.Store(fmt.Sprintf(`{"files":[{"name":"claude-pool.json","provider":"claude","email":"pool@example.com","status":"active","quota_ceiling":{"5h":60,"7d":60,"7d_oi":60},
 "quota":{"observed_at":%q,"signals":{"Anthropic-Ratelimit-Unified-5h-Utilization":"%.2f","Anthropic-Ratelimit-Unified-5h-Reset":"%d","Anthropic-Ratelimit-Unified-7d-Utilization":"%.2f","Anthropic-Ratelimit-Unified-7d-Reset":"%d","Anthropic-Ratelimit-Unified-7d_oi-Utilization":"%.2f","Anthropic-Ratelimit-Unified-7d_oi-Reset":"%d"}}}]}`,
		now.Format(time.RFC3339), session, now.Add(3*time.Hour).Unix(), week, now.Add(50*time.Hour).Unix(), fableWeek, now.Add(50*time.Hour).Unix()))
	return proxy
}

func proxyEntry(t *testing.T, proxy *proxyaccttest.Proxy) func(string) string {
	return namedProxyEntries(t, map[string]*proxyaccttest.Proxy{"shared": proxy})
}

func namedProxyEntries(t *testing.T, proxies map[string]*proxyaccttest.Proxy) func(string) string {
	return func(home string) string {
		key := proxyaccttest.KeyFile(t, home, proxyaccttest.Key, 0o600)
		var entries []string
		for name, proxy := range proxies {
			entries = append(entries, `{"name":"`+name+`","url":"`+proxy.Server.URL+`","management":{"file":"`+key+`"}}`)
		}
		return strings.Join(entries, ",")
	}
}

// resumeRoot runs the resume pass for one root's recorded stops; the full
// pass would first read the fake transcripts, which hold no refusal.
func resumeRoot(t *testing.T, d *Daemon, root string, now time.Time) {
	t.Helper()
	stops, err := d.Registry.LimitResumes()
	if err != nil {
		t.Fatal(err)
	}
	d.resumeAccount(root, stops, true, true, now)
}

func awaitProxyReading(t *testing.T, d *Daemon) {
	t.Helper()
	home, _ := os.UserHomeDir()
	configs, err := surface.ConfiguredProxies(home)
	if err != nil || len(configs) == 0 {
		t.Fatalf("configs=%+v err=%v", configs, err)
	}
	for _, config := range configs {
		testkit.Eventually(t, "the "+config.Name+" proxy's accounts", 10*time.Second, func() bool {
			reading := d.proxyUsage.cached(config, time.Now())
			return reading != nil && reading.err == nil
		})
	}
}

func TestAStoppedSessionMovesToAProxyWithRoomOnceRatherThanToAnotherAccount(t *testing.T) {
	proxy := sharedProxy(t, 0.12, 0.30, 0.10)
	node, second, pool := limitMoveNodeWith(t, "10", proxyEntry(t, proxy))
	session := startMovable(t, node)
	primary := filepath.Join(node.host.Home, ".claude")
	stopAtLimit(t, node, session, primary, "u1", time.Now().Add(-time.Minute))
	awaitProxyReading(t, node.daemon)
	readClaudeUsage(t, node.daemon)
	if offer := node.daemon.limitMoveOffer(session.ID, time.Now()); offer == nil || offer.Root != "pool" {
		t.Fatalf("offer=%+v, want the proxy root", offer)
	}

	resumeRoot(t, node.daemon, primary, time.Now())
	testkit.Eventually(t, "the move to the proxy root", 30*time.Second, func() bool {
		return transcriptUnder(pool, session.ID) != "" && limitStopState(t, node, session.ID) == registry.LimitResumeReleased
	})
	if transcriptUnder(primary, session.ID) != "" || transcriptUnder(second, session.ID) != "" {
		t.Fatal("the session is left on, or went to, a directly signed-in root")
	}
	spawns := len(node.host.Spawns(t, session.ID))
	resumeRoot(t, node.daemon, primary, time.Now().Add(time.Minute))
	if again := len(node.host.Spawns(t, session.ID)); again != spawns {
		t.Fatalf("a released stop was acted on again: %d spawns, then %d", spawns, again)
	}
	if writes := proxy.Writes(); len(writes) != 0 {
		t.Fatalf("the switch wrote to the proxy: %v", writes)
	}
}

func TestAStoppedSessionWaitsWhenTheProxyIsFullEvenIfAnotherAccountHasRoom(t *testing.T) {
	proxy := sharedProxy(t, 0.70, 0.30, 0.10)
	node, second, _ := limitMoveNodeWith(t, "10", proxyEntry(t, proxy))
	session := startMovable(t, node)
	primary := filepath.Join(node.host.Home, ".claude")
	stopAtLimit(t, node, session, primary, "u1", time.Now().Add(-time.Minute))
	awaitProxyReading(t, node.daemon)
	readClaudeUsage(t, node.daemon)

	resumeRoot(t, node.daemon, primary, time.Now())
	if claimed, moving := node.daemon.claimLimitMove(session.ID, "u1"); !claimed || moving {
		t.Fatalf("a move started with the proxy full: claimed=%v moving=%v", claimed, moving)
	}
	node.daemon.unclaimLimitMove(session.ID)
	if transcriptUnder(primary, session.ID) == "" || transcriptUnder(second, session.ID) != "" || limitStopState(t, node, session.ID) != registry.LimitResumeWaiting {
		t.Fatal("the session left its root with the proxy full")
	}
	if writes := proxy.Writes(); len(writes) != 0 {
		t.Fatalf("the switch wrote to the proxy: %v", writes)
	}
}

func TestAStopOnAProxyRootIsOfferedTheNextProxyWithRoomBeforeASignedInRoot(t *testing.T) {
	shared, spare := sharedProxy(t, 0.12, 0.30, 0.10), sharedProxy(t, 0.12, 0.30, 0.10)
	node, _, pool := limitMoveNodeWith(t, "10", namedProxyEntries(t, map[string]*proxyaccttest.Proxy{"shared": shared, "spare": spare}), func(home string) string {
		root := filepath.Join(home, ".spare", ".claude")
		writeUsageFixture(t, map[string]string{filepath.Join(root, ".claude.json"): `{}`})
		return `{"path":"` + root + `","proxy":"spare"}`
	})
	session := startMovable(t, node)
	stopAtLimit(t, node, session, pool, "u1", time.Now().Add(-time.Minute))
	awaitProxyReading(t, node.daemon)
	readClaudeUsage(t, node.daemon)
	if offer := node.daemon.limitMoveOffer(session.ID, time.Now()); offer == nil || offer.Root != "spare" {
		t.Fatalf("offer=%+v, want the spare proxy's root, not the stop's own proxy or a signed-in root", offer)
	}
}

func TestAStoppedSessionMovesToAFullProxyOnceItFrees(t *testing.T) {
	proxy := sharedProxy(t, 0.70, 0.30, 0.10)
	frees := time.Now().Add(3 * time.Hour)
	node, second, pool := limitMoveNodeWith(t, "100", proxyEntry(t, proxy))
	session := startMovable(t, node)
	primary := filepath.Join(node.host.Home, ".claude")
	refusedAt := time.Now().Add(-time.Minute).Truncate(time.Second)
	if err := node.reg.RecordLimitStop(*session, primary, surface.LimitStop{TurnID: "u1", RefusedAt: refusedAt, ResetsAt: refusedAt.Add(30 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	awaitProxyReading(t, node.daemon)

	resumeRoot(t, node.daemon, primary, time.Now())
	if row := node.daemon.limitResumeRow(session.ID); row == nil || row.ResetsAt.Sub(frees).Abs() > time.Minute {
		t.Fatalf("row=%+v, want it to resume about %s, when the proxy frees", row, frees)
	}
	if transcriptUnder(pool, session.ID) != "" {
		t.Fatal("the session moved while the proxy was full")
	}
	resumeRoot(t, node.daemon, primary, frees.Add(time.Minute))
	testkit.Eventually(t, "the move once the proxy frees", 30*time.Second, func() bool {
		return transcriptUnder(pool, session.ID) != "" && limitStopState(t, node, session.ID) == registry.LimitResumeReleased
	})
	if transcriptUnder(primary, session.ID) != "" || transcriptUnder(second, session.ID) != "" {
		t.Fatal("the session is left on, or went to, a directly signed-in root")
	}
	if writes := proxy.Writes(); len(writes) != 0 {
		t.Fatalf("the switch wrote to the proxy: %v", writes)
	}
}

// switchFixture is a limit fixture on a host whose roots are the primary,
// signed in directly, and pool, which runs through the shared proxy.
func switchFixture(t *testing.T, proxy *proxyaccttest.Proxy, ids ...string) (*limitFixture, string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	primary := filepath.Join(home, ".claude")
	pool := filepath.Join(home, ".pool", ".claude")
	writeUsageFixture(t, map[string]string{
		filepath.Join(home, ".agenthail", "config.json"): `{"claudeConfigDirs":[{"path":"` + pool + `","proxy":"shared"}],"proxies":[` + proxyEntry(t, proxy)(home) + `]}`,
		filepath.Join(home, ".claude.json"):              `{"oauthAccount":{"accountUuid":"acct-a","emailAddress":"me@example.com","organizationUuid":"org-a"}}`,
		filepath.Join(pool, ".claude.json"):              `{}`,
	})
	f := newLimitFixture(t, ids...)
	awaitProxyReading(t, f.d)
	return f, primary, pool
}

func TestAHostedSessionOnAProxyRootIsRestartedOnceWhenTheProxyHasRoom(t *testing.T) {
	proxy := sharedProxy(t, 0.12, 0.30, 0.10)
	f, _, pool := switchFixture(t, proxy, "stopped")
	f.hosted("stopped")
	refusedAt := time.Now().Add(-time.Minute).Truncate(time.Second)
	f.refused("stopped", pool, "u1", refusedAt, refusedAt.Add(30*time.Hour))
	f.pass(refusedAt.Add(time.Second))
	if len(f.host.stops) != 1 || f.host.stops[0] != "stopped" || f.state("stopped") != registry.LimitResumeReleased {
		t.Fatalf("stops=%v state=%q, want one restart", f.host.stops, f.state("stopped"))
	}
	if writes := proxy.Writes(); len(writes) != 0 {
		t.Fatalf("the restart wrote to the proxy: %v", writes)
	}
}

func TestOnlyAHostedSessionThatIsNoCoordinatorMovesAndOnlyWhileItsResetIsFar(t *testing.T) {
	proxy := sharedProxy(t, 0.12, 0.30, 0.70)
	f, primary, _ := switchFixture(t, proxy, "worker", "terminal", "lead", "fable", "soon")
	for _, id := range []string{"worker", "lead", "fable", "soon"} {
		f.hosted(id)
	}
	fable := f.fake.sessions["fable"]
	fable.ConfiguredModel = "claude-fable-5-2"
	if err := f.r.RecordObservedSession(fable); err != nil {
		t.Fatal(err)
	}
	if err := f.r.SetAlias("widget-lead", "lead"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.SetCoordinator(registry.CoordinatorSpec{ProjectID: "widget", Name: "widget-lead", Surface: "claude", ProjectRoot: f.dir, Cwd: f.dir}, registry.CoordinatorChange{SessionID: "lead", ChangedBy: "fixture"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	stop := func(id string, resets time.Duration) registry.LimitResume {
		return registry.LimitResume{SessionID: id, TurnID: "u1", AccountKey: primary, RefusedAt: now.Add(-time.Minute), ResetsAt: now.Add(resets), State: registry.LimitResumeWaiting}
	}
	for _, test := range []struct {
		name  string
		stop  registry.LimitResume
		moves bool
		want  bool
	}{
		{"a hosted session", stop("worker", time.Hour), true, true},
		{"with moves off", stop("worker", time.Hour), false, false},
		{"a terminal session", stop("terminal", time.Hour), true, false},
		{"a coordinator", stop("lead", time.Hour), true, false},
		{"a Fable session over the proxy's Fable week", stop("fable", time.Hour), true, false},
		{"a stop whose reset is close", stop("soon", 10*time.Minute), true, false},
	} {
		release, ok := f.d.limitSwitch(f.d.readProxyView(now), test.stop, test.moves, now)
		if moved := ok && release.moveTo == "pool"; moved != test.want || ok && !moved {
			t.Errorf("%s: release=%+v ok=%v, want a move to pool %v and nothing else", test.name, release, ok, test.want)
		}
	}
	if writes := proxy.Writes(); len(writes) != 0 {
		t.Fatalf("the switch wrote to the proxy: %v", writes)
	}
}

func TestAStopResumesWhenTheFullProxyItWouldSwitchThroughFreesAnAccount(t *testing.T) {
	proxy := sharedProxy(t, 0.70, 0.30, 0.10)
	frees := time.Now().Add(3 * time.Hour)
	f, primary, pool := switchFixture(t, proxy, "on-proxy", "direct", "lead", "terminal", "no-reset")
	for _, id := range []string{"on-proxy", "direct", "lead", "no-reset"} {
		f.hosted(id)
	}
	if err := f.r.SetAlias("widget-lead", "lead"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.SetCoordinator(registry.CoordinatorSpec{ProjectID: "widget", Name: "widget-lead", Surface: "claude", ProjectRoot: f.dir, Cwd: f.dir}, registry.CoordinatorChange{SessionID: "lead", ChangedBy: "fixture"}); err != nil {
		t.Fatal(err)
	}
	refusedAt := time.Now().Add(-time.Minute).Truncate(time.Second)
	resets := refusedAt.Add(30 * time.Hour)
	f.refused("on-proxy", pool, "u1", refusedAt, resets)
	f.refused("no-reset", pool, "u1", refusedAt, time.Time{})
	for _, id := range []string{"direct", "lead", "terminal"} {
		f.refused(id, primary, "u1", refusedAt, resets)
	}
	f.pass(refusedAt.Add(time.Second))
	for id, want := range map[string]time.Time{"on-proxy": frees, "direct": frees, "lead": resets, "terminal": resets, "no-reset": {}} {
		row := f.d.limitResumeRow(id)
		if row == nil || row.ResetsAt.Sub(want).Abs() > time.Minute || want.IsZero() != row.ResetsAt.IsZero() {
			t.Errorf("%s resumes at %+v, want about %s", id, row, want)
		}
	}
	if len(f.host.stops) != 0 {
		t.Fatalf("restarted %v while the proxy was full", f.host.stops)
	}

	then := frees.Add(time.Minute)
	view := f.d.readProxyView(then)
	for id, want := range map[string]string{"direct": "pool", "lead": "", "terminal": ""} {
		stop, _, err := f.r.LimitResumeFor(id)
		if err != nil {
			t.Fatal(err)
		}
		if release, ok := f.d.limitSwitch(view, stop, true, then); ok != (want != "") || release.moveTo != want {
			t.Errorf("%s once the proxy frees: release=%+v ok=%v, want a move to %q", id, release, ok, want)
		}
	}
	f.pass(then)
	f.pass(then.Add(time.Minute))
	if !slices.Contains(f.host.stops, "on-proxy") || f.state("on-proxy") != registry.LimitResumeReleased {
		t.Fatalf("stops=%v state=%q, want the stop on the proxy root restarted once the proxy frees", f.host.stops, f.state("on-proxy"))
	}
	if writes := proxy.Writes(); len(writes) != 0 {
		t.Fatalf("reading when the proxy frees wrote to it: %v", writes)
	}
}
