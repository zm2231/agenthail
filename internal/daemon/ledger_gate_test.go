package daemon

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
)

type gateFixture struct {
	d       *Daemon
	r       *registry.Registry
	fake    *daemonSurface
	project ledgerProject
	session surface.Session
}

func newGateFixture(t *testing.T, via registry.OriginVia, runtime surface.RuntimeKind) gateFixture {
	t.Helper()
	return gateFixtureIn(t, via, runtime, true)
}

func gateFixtureIn(t *testing.T, via registry.OriginVia, runtime surface.RuntimeKind, enabled bool) gateFixture {
	t.Helper()
	r, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	fake := &daemonSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{}, observations: map[string]*surface.TurnObservation{}, accepted: true}
	d := newTestDaemon(t, r, []surface.Surface{fake})
	p := addLedgerProject(t, r, "proj-g2", "repo:github.com/acme/widget")
	if enabled {
		if err := p.store.Enable(p.key, ledger.AttachEachPrompt); err != nil {
			t.Fatal(err)
		}
	}
	s := surface.Session{ID: "worker", Surface: surface.KindClaude, RuntimeKind: runtime, Cwd: p.dir, Status: surface.StatusIdle}
	if err := r.RegisterSession(s); err != nil {
		t.Fatal(err)
	}
	fake.sessions[s.ID] = s
	origin := registry.SessionOrigin{SessionID: s.ID, Via: via}
	if via == registry.OriginAgent {
		origin.CreatorID = "lead"
	}
	if err := r.RecordSessionOrigin(origin); err != nil {
		t.Fatal(err)
	}
	return gateFixture{d: d, r: r, fake: fake, project: p, session: s}
}

func (f gateFixture) ownClause(t *testing.T, at time.Time) {
	t.Helper()
	u := ledger.Utterance{ProjectKey: f.project.key, Ref: "claude-prompt:1", SpeakerID: surface.PersonSessionID, TargetID: f.session.ID, Text: "build search", At: at, CapturedBy: ledger.CapturedByHook}
	if _, _, err := f.project.store.RecordUtterance(u); err != nil {
		t.Fatal(err)
	}
	g, _, err := f.project.store.EnsureGoal(f.project.key, "Search", f.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.project.store.Attach(ledger.AttachRequest{ProjectKey: f.project.key, SessionID: f.session.ID, UtteranceID: u.Ref, As: ledger.AsNewAsk, To: g.ID, Clauses: []string{"build search"}}); err != nil {
		t.Fatal(err)
	}
}

func (f gateFixture) completeTurn(t *testing.T, turn string) {
	t.Helper()
	f.fake.observations[f.session.ID] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: turn}
	f.d.observeSession(t.Context(), f.fake, &f.session)
	f.fake.observations[f.session.ID] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: turn, Reply: &surface.ReplyResult{Text: "done", Done: true}}
	f.d.observeSession(t.Context(), f.fake, &f.session)
}

func TestG2QueuesTheOpenWorkIntoAManagedSessionAtTurnEnd(t *testing.T) {
	f := newGateFixture(t, registry.OriginAgent, surface.RuntimeClaudeHost)
	f.ownClause(t, time.Now().Add(-time.Hour))
	f.completeTurn(t, "turn-1")
	all := strings.Join(f.fake.sent, "\n")
	if !strings.Contains(all, "You ended your turn with open work") || !strings.Contains(all, "    widget-C1: build search") {
		t.Fatalf("no G2 notice delivered: %q", f.fake.sent)
	}
	if gates := daemonEvents(t, f.r, "ledger.gate"); len(gates) != 1 || !strings.Contains(string(gates[0].Payload), `"result":"blocked"`) {
		t.Fatalf("gate events = %+v", gates)
	}
}

func TestG2HoldsOnlyOwnedClausesInEachProjectTheSessionWorksIn(t *testing.T) {
	f := newGateFixture(t, registry.OriginAgent, surface.RuntimeClaudeHost)
	f.ownClause(t, time.Now().Add(-time.Hour))
	other := addLedgerProject(t, f.r, "proj-other", "repo:github.com/acme/infra")
	if err := other.store.GrantSession(other.key, f.session.ID); err != nil {
		t.Fatal(err)
	}
	u := ledger.Utterance{ProjectKey: other.key, Ref: "claude-prompt:9", SpeakerID: surface.PersonSessionID, TargetID: f.session.ID, Text: "rotate keys", At: time.Now().Add(-time.Hour), CapturedBy: ledger.CapturedByHook}
	if _, _, err := other.store.RecordUtterance(u); err != nil {
		t.Fatal(err)
	}
	f.completeTurn(t, "turn-1")
	if len(f.fake.sent) != 1 || f.r.QueueCount(f.session.ID) != 0 {
		t.Fatalf("want one notice for the turn, got %d sent and %d queued", len(f.fake.sent), f.r.QueueCount(f.session.ID))
	}
	if notice := f.fake.sent[0]; !strings.Contains(notice, "in repo:github.com/acme/widget:\n    widget-C1: build search") || strings.Contains(notice, "acme/infra") || strings.Contains(notice, "claude-prompt:9") {
		t.Fatalf("notice should name the owned clause and not the granted project's provisional words: %q", notice)
	}
	f.completeTurn(t, "turn-1")
	if gates := daemonEvents(t, f.r, "ledger.gate"); len(gates) != 1 {
		t.Fatalf("gate events = %d, want one for the project with an owned clause", len(gates))
	}
}

func TestG2CountsEveryManagedTurnItChecks(t *testing.T) {
	f := newGateFixture(t, registry.OriginAgent, surface.RuntimeClaudeHost)
	f.completeTurn(t, "turn-1")
	if got := f.d.g2.project(f.project.key); got.Checked != 1 || got.Gated != 0 {
		t.Fatalf("after a turn with nothing to gate: %+v", got)
	}
	f.ownClause(t, time.Now().Add(-time.Hour))
	f.completeTurn(t, "turn-2")
	f.completeTurn(t, "turn-2")
	got := f.d.g2.project(f.project.key)
	if got.Checked != 2 || got.Gated != 1 {
		t.Fatalf("after a gated turn and its repeat: %+v", got)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ledger/g2?projectKey="+url.QueryEscape(f.project.key), nil)
	rec := httptest.NewRecorder()
	f.d.apiLedgerG2Handler(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"checked":2`) || !strings.Contains(rec.Body.String(), `"gated":1`) {
		t.Fatalf("g2 endpoint = %d %s", rec.Code, rec.Body.String())
	}
}

func TestG2CountsArePublishedAtMostHourly(t *testing.T) {
	start := time.Now()
	c := newG2Counter(start)
	if _, publish := c.record("p", false, start.Add(time.Minute)); publish {
		t.Fatal("published within the first hour")
	}
	snapshot, publish := c.record("p", true, start.Add(time.Hour))
	if !publish || len(snapshot) != 1 || snapshot[0].Checked != 2 || snapshot[0].Gated != 1 {
		t.Fatalf("hourly snapshot = %v %+v", publish, snapshot)
	}
	if _, publish := c.record("p", false, start.Add(90*time.Minute)); publish {
		t.Fatal("published twice within an hour")
	}
}

func TestG2NeverQueuesIntoThePersonsOwnTerminal(t *testing.T) {
	f := newGateFixture(t, registry.OriginPerson, surface.RuntimeClaudeCLI)
	f.ownClause(t, time.Now().Add(-time.Hour))
	f.completeTurn(t, "turn-1")
	if len(f.fake.sent) != 0 || len(daemonEvents(t, f.r, "ledger.gate")) != 0 {
		t.Fatalf("G2 reached a person's terminal: sent %q", f.fake.sent)
	}
}

func TestG2DefersWhenThePersonJustSpokeToTheSession(t *testing.T) {
	f := newGateFixture(t, registry.OriginCoordinator, surface.RuntimeClaudeHost)
	f.ownClause(t, time.Now())
	f.completeTurn(t, "turn-1")
	if len(f.fake.sent) != 0 {
		t.Fatalf("G2 interleaved with the person's conversation: %q", f.fake.sent)
	}
	if gates := daemonEvents(t, f.r, "ledger.gate"); len(gates) != 1 || !strings.Contains(string(gates[0].Payload), ledger.GateDeferredPerson) {
		t.Fatalf("gate events = %+v", gates)
	}
}

func TestALeadInAProjectNobodyEnabledIsAskedToAttachButNotHeld(t *testing.T) {
	f := gateFixtureIn(t, registry.OriginCoordinator, surface.RuntimeClaudeHost, false)
	makeCoordinator(t, f.r, f.project, f.session.ID)
	if got := ledgerPost(t, f.d.dashboardHandler(&dashboardServer{token: "test-token"}), ledgerCaptureRequest{SessionID: f.session.ID, PromptID: "p-0", Prompt: "and the docs"}); !got.Captured || !got.Attach {
		t.Fatalf("the lead's words in a project nobody enabled: %+v", got)
	}
	u := ledger.Utterance{ProjectKey: f.project.key, Ref: "claude-prompt:1", SpeakerID: surface.PersonSessionID, TargetID: f.session.ID, Text: "build search", At: time.Now().Add(-time.Hour), CapturedBy: ledger.CapturedByHook}
	if _, _, err := f.project.store.RecordUtterance(u); err != nil {
		t.Fatal(err)
	}
	f.completeTurn(t, "turn-1")
	if len(f.fake.sent) != 0 || f.r.QueueCount(f.session.ID) != 0 {
		t.Fatalf("a lead in a project nobody enabled was held at the end of its turn: %q", f.fake.sent)
	}
}
