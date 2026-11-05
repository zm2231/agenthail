package daemon

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/claudepick"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
	"github.com/zm2231/agenthail/internal/testkit"
)

func limitMoveNode(t *testing.T, workPercent string) (*moveNode, string, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	claudetest.New(t)
	allowLoopbackHosts(t)
	host := claudehosttest.New(t, "")
	host.Start(t)
	t.Setenv("HOME", host.Home)
	second := filepath.Join(host.Home, ".work", ".claude")
	pool := filepath.Join(host.Home, ".pool", ".claude")
	writeUsageFixture(t, map[string]string{
		filepath.Join(host.Home, ".agenthail", "config.json"): `{"claudeConfigDirs":["` + second + `",{"path":"` + pool + `","proxy":"shared"}],"proxies":[{"name":"shared","url":"http://127.0.0.1:9"}]}`,
		filepath.Join(host.Home, ".claude.json"):              `{"oauthAccount":{"accountUuid":"acct-a","emailAddress":"me@example.com","organizationUuid":"org-a"}}`,
		filepath.Join(second, ".claude.json"):                 `{"oauthAccount":{"accountUuid":"acct-b","emailAddress":"work@example.com","organizationUuid":"org-b"}}`,
		filepath.Join(pool, ".claude.json"):                   `{}`,
	})
	resets := time.Now().Add(3 * time.Hour).UTC().Format(time.RFC3339)
	percent := map[string]string{"org-a": "100", "org-b": workPercent}
	profiles := []signin.Source{{ProfileID: "sp_a", Label: "a", Browser: signin.Chrome, Dir: "/a"}, {ProfileID: "sp_b", Label: "b", Browser: signin.Chrome, Dir: "/b"}}
	claude := surfaces.NewClaudeWithRequest(candidates(profiles), host.Home, func(_ context.Context, _ string, url string, _ map[string]string, _ string, cookies surfaces.CookieSource, _ time.Duration) (int, string, error) {
		if strings.HasSuffix(url, "/api/account") {
			return 200, `{"uuid":"acct` + strings.ReplaceAll(cookies.Signin.Dir, "/", "-") + `"}`, nil
		}
		for org, value := range percent {
			if strings.HasSuffix(url, "/api/organizations/"+org+"/usage") {
				return 200, `{"limits":[{"kind":"session","percent":` + value + `,"resets_at":"` + resets + `"}]}`, nil
			}
		}
		return 404, "{}", nil
	})
	reg, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reg.Close() })
	d := newTestDaemon(t, reg, []surface.Surface{claude})
	node := &federationNode{daemon: d, reg: reg, gate: &actionGate{}}
	node.handler = d.dashboardHandler(&dashboardServer{token: dashboardTestToken})
	return &moveNode{federationNode: node, host: host, claude: claude}, second, pool
}

func readClaudeUsage(t *testing.T, d *Daemon) {
	t.Helper()
	roots := d.currentClaudeConfigRoots()
	d.claudeCachedUsageSets(roots, time.Now())
	testkit.Eventually(t, "the usage readings", 10*time.Second, func() bool {
		for _, root := range roots {
			if root.Proxy == "" && d.claudeUsage.entry(root.Root).last.Load() == nil {
				return false
			}
		}
		return true
	})
}

func stopAtLimit(t *testing.T, node *moveNode, session *surface.Session, account, turn string, refusedAt time.Time) {
	t.Helper()
	if err := node.reg.RecordLimitStop(*session, account, surface.LimitStop{TurnID: turn, RefusedAt: refusedAt, ResetsAt: refusedAt.Add(3 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	node.daemon.limitResumes.moves.forget()
}

func limitStopState(t *testing.T, node *moveNode, sessionID string) registry.LimitResumeState {
	t.Helper()
	stop, found, err := node.reg.LimitResumeFor(sessionID)
	if err != nil || !found {
		t.Fatalf("stop found=%v err=%v", found, err)
	}
	return stop.State
}

func TestAStoppedSessionIsOfferedTheAccountWithRoomAndMovesThere(t *testing.T) {
	node, second, _ := limitMoveNode(t, "10")
	session := startMovable(t, node)
	primary := filepath.Join(node.host.Home, ".claude")
	stopAtLimit(t, node, session, primary, "u1", time.Now().Add(-time.Minute))
	readClaudeUsage(t, node.daemon)
	offer := node.daemon.limitMoveOffer(session.ID, time.Now())
	if offer == nil || offer.Root != "work" || offer.Account != "work@example.com" || offer.Reason != "most headroom, session 10%" {
		t.Fatalf("offer=%+v", offer)
	}
	if response := postAction(t, node.handler, map[string]any{"action": "limit-move", "sessionId": session.ID}); response.Code != http.StatusOK {
		t.Fatalf("limit-move status=%d body=%s", response.Code, response.Body.String())
	}
	if transcriptUnder(primary, session.ID) != "" || transcriptUnder(second, session.ID) == "" {
		t.Fatal("the session did not move to the offered account")
	}
	if state := limitStopState(t, node, session.ID); state != registry.LimitResumeReleased {
		t.Fatalf("a moved session's stop is %s", state)
	}
}

func TestAModelScopedWeekNeverHidesAFullWeekFromTheOffer(t *testing.T) {
	now := time.Now()
	meter := func(kind string, percent float64) usageMeter {
		return usageMeter{Provider: "claude", Kind: kind, Percent: percent, ResetsAt: now.Add(time.Hour)}
	}
	sets := []usageSet{
		{Provider: "claude", Name: "default", Root: "/home/dev/.claude", Primary: true, Login: surface.ClaudeLoginClaudeAI, Meters: []usageMeter{meter("session", 100)}},
		{Provider: "claude", Name: "team", Root: "/home/dev/.team/.claude", Login: surface.ClaudeLoginClaudeAI, Meters: []usageMeter{meter("session", 18), meter("weekly_all", 92), meter("weekly_scoped", 40)}},
	}
	candidates := limitMoveCandidates(sets, now)
	if pick, ok := claudepick.MoveTarget(candidates, "/home/dev/.claude"); ok {
		t.Fatalf("a root whose week is 92%% was offered by its scoped 40%%: %+v", pick)
	}
	sets[1].Meters[1].Percent = 40
	if pick, ok := claudepick.MoveTarget(limitMoveCandidates(sets, now), "/home/dev/.claude"); !ok || pick.Reason != "most headroom, session 18%, week 40%" {
		t.Fatalf("pick=%+v ok=%v", pick, ok)
	}
}

func TestNoMoveIsOfferedWhenNoOtherAccountHasRoom(t *testing.T) {
	node, _, _ := limitMoveNode(t, "95")
	session := startMovable(t, node)
	primary := filepath.Join(node.host.Home, ".claude")
	stopAtLimit(t, node, session, primary, "u1", time.Now().Add(-time.Minute))
	readClaudeUsage(t, node.daemon)
	if offer := node.daemon.limitMoveOffer(session.ID, time.Now()); offer != nil {
		t.Fatalf("an account over its ceiling was offered: %+v", offer)
	}
	response := postAction(t, node.handler, map[string]any{"action": "limit-move", "sessionId": session.ID})
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "no other Claude account has room") {
		t.Fatalf("limit-move status=%d body=%s", response.Code, response.Body.String())
	}
	if transcriptUnder(primary, session.ID) == "" {
		t.Fatal("a refused move took the transcript away")
	}
}

func TestALimitMoveRefusesAProxyRootThatAMoveByHandTakes(t *testing.T) {
	node, _, pool := limitMoveNode(t, "95")
	session := startMovable(t, node)
	primary := filepath.Join(node.host.Home, ".claude")
	stopAtLimit(t, node, session, primary, "u1", time.Now().Add(-time.Minute))
	response := postAction(t, node.handler, map[string]any{"action": "limit-move", "sessionId": session.ID, "configRoot": "pool"})
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "shared proxy") {
		t.Fatalf("limit-move status=%d body=%s", response.Code, response.Body.String())
	}
	if transcriptUnder(primary, session.ID) == "" {
		t.Fatal("a refused limit move took the transcript away")
	}
	if _, err := node.daemon.moveSessionToRoot(context.Background(), sessionMoveRequest{SessionID: session.ID, Root: "pool"}); err != nil {
		t.Fatal(err)
	}
	if transcriptUnder(primary, session.ID) != "" || transcriptUnder(pool, session.ID) == "" {
		t.Fatal("the session did not move to the proxy root")
	}
	spawns := node.host.Spawns(t, session.ID)
	if last := spawns[len(spawns)-1]; last.ConfigDir == nil || *last.ConfigDir != pool {
		t.Fatalf("the session resumed in %v, want %s", last.ConfigDir, pool)
	}
}

func TestACanceledStopIsNotMovedOrReleasedByAMove(t *testing.T) {
	node, second, _ := limitMoveNode(t, "10")
	session := startMovable(t, node)
	primary := filepath.Join(node.host.Home, ".claude")
	stopAtLimit(t, node, session, primary, "u1", time.Now().Add(-time.Minute))
	readClaudeUsage(t, node.daemon)
	if response := postAction(t, node.handler, map[string]any{"action": "limit-resume-cancel", "sessionId": session.ID}); response.Code != http.StatusOK {
		t.Fatalf("cancel status=%d body=%s", response.Code, response.Body.String())
	}
	response := postAction(t, node.handler, map[string]any{"action": "limit-move", "sessionId": session.ID, "configRoot": "work"})
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "not waiting") {
		t.Fatalf("limit-move status=%d body=%s", response.Code, response.Body.String())
	}
	if transcriptUnder(primary, session.ID) == "" {
		t.Fatal("a canceled stop was moved")
	}
	if _, err := node.daemon.moveSessionToRoot(context.Background(), sessionMoveRequest{SessionID: session.ID, Root: "work"}); err != nil {
		t.Fatal(err)
	}
	if transcriptUnder(second, session.ID) == "" || limitStopState(t, node, session.ID) != registry.LimitResumeCanceled {
		t.Fatal("a move by hand released a canceled stop")
	}
}

func TestAStopCannotBeCanceledOrResumedWhileItsSessionMoves(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newLimitFixture(t, "worker")
	refusedAt := time.Now().Add(-time.Minute).Truncate(time.Second)
	f.refused("worker", "/roots/work", "u1", refusedAt, refusedAt.Add(time.Hour))
	f.pass(refusedAt.Add(time.Second))
	handler := dashboardRouter(f.d)
	if claimed, _ := f.d.claimLimitMove("worker", "u0"); claimed {
		t.Fatal("a move claimed a turn the stop is not on")
	}
	if claimed, _ := f.d.claimLimitMove("worker", "u1"); !claimed {
		t.Fatal("a waiting stop was not claimed for its move")
	}
	if _, moving := f.d.claimLimitMove("worker", ""); !moving {
		t.Fatal("a second move claimed a session already moving")
	}
	for _, action := range []string{"limit-resume-cancel", "limit-resume"} {
		if response := serveDashboardRequest(handler, http.MethodPost, "/api/v1/actions", `{"action":"`+action+`","sessionId":"worker"}`); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "moving") {
			t.Fatalf("%s during a move: status=%d body=%s", action, response.Code, response.Body.String())
		}
	}
	if response := serveDashboardRequest(handler, http.MethodPost, "/api/v1/actions", `{"action":"limit-move","sessionId":"worker","configRoot":"work"}`); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "move_busy") {
		t.Fatalf("limit-move during a move: status=%d body=%s", response.Code, response.Body.String())
	}
	if state := f.state("worker"); state != registry.LimitResumeWaiting {
		t.Fatalf("a stop changed while its session moved: %s", state)
	}
	f.d.unclaimLimitMove("worker")
	if response := serveDashboardRequest(handler, http.MethodPost, "/api/v1/actions", `{"action":"limit-resume-cancel","sessionId":"worker"}`); response.Code != http.StatusOK {
		t.Fatalf("cancel after the move: status=%d body=%s", response.Code, response.Body.String())
	}
	if claimed, _ := f.d.claimLimitMove("worker", ""); claimed {
		t.Fatal("a canceled stop was claimed for a move")
	}
}
