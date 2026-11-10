package daemon

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
)

func questionDaemon(t *testing.T) (*Daemon, *registry.Registry, http.Handler, *daemonSurface, *desktopRecorder, ledgerProject) {
	t.Helper()
	r, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	fake := &daemonSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{}, observations: map[string]*surface.TurnObservation{}, accepted: true}
	d := newTestDaemon(t, r, []surface.Surface{fake})
	desktop := &desktopRecorder{}
	d.notifyDesktop = desktop.notify
	p := addLedgerProject(t, r, "proj-q", "repo:github.com/acme/widget")
	s := p.session(t, r, "s1")
	fake.sessions[s.ID] = s
	return d, r, d.dashboardHandler(&dashboardServer{token: "test-token"}), fake, desktop, p
}

func ask(t *testing.T, handler http.Handler, p ledgerProject, req ledger.QuestionRequest) ledger.Question {
	t.Helper()
	req.ProjectKey, req.SessionID = p.key, "s1"
	if req.Recommendation == "" {
		req.Recommendation = "full-text"
	}
	req.DeadlineMs = time.Now().Add(time.Hour).UnixMilli()
	var q ledger.Question
	if code, body := ledgerWrite(t, handler, "/api/v1/ledger/question", req, &q); code != http.StatusOK {
		t.Fatalf("question status=%d body=%s", code, body)
	}
	return q
}

func daemonEvents(t *testing.T, r *registry.Registry, prefix string) []registry.DaemonEvent {
	t.Helper()
	recent, err := r.RecentDaemonEvents(200)
	if err != nil {
		t.Fatal(err)
	}
	var out []registry.DaemonEvent
	for _, e := range recent {
		if strings.HasPrefix(e.Type, prefix) {
			out = append(out, e)
		}
	}
	return out
}

func TestLedgerAPIAsksAndRefusesQuestions(t *testing.T) {
	_, _, handler, _, _, p := questionDaemon(t)
	ledgerWrite(t, handler, "/api/v1/ledger/goal", ledgerGoalRequest{ProjectKey: p.key, SessionID: "s1", Name: "Search"}, nil)
	if q := ask(t, handler, p, ledger.QuestionRequest{To: "widget-G1", Text: "which index?", DefaultAnswer: "full-text", Kind: ledger.QuestionScope}); q.ID != "widget-Q1" {
		t.Fatalf("question = %+v", q)
	}
	code, body := ledgerWrite(t, handler, "/api/v1/ledger/question", ledger.QuestionRequest{ProjectKey: p.key, SessionID: "s1", To: "widget-G1", Text: "deploy it?", Recommendation: "yes", DefaultAnswer: "yes", Kind: ledger.QuestionScope, DeadlineMs: time.Now().Add(time.Hour).UnixMilli()}, nil)
	if code != http.StatusConflict || !strings.Contains(body, "outward") {
		t.Fatalf("status=%d body=%s", code, body)
	}
}

func TestAQuestionAsksThePersonOnceAndOnlyItsAppliedDefaultTellsTheOwner(t *testing.T) {
	d, r, handler, _, desktop, p := questionDaemon(t)
	ledgerWrite(t, handler, "/api/v1/ledger/goal", ledgerGoalRequest{ProjectKey: p.key, SessionID: "s1", Name: "Search"}, nil)
	ask(t, handler, p, ledger.QuestionRequest{To: "widget-G1", Text: "which index?", DefaultAnswer: "full-text and fuzzy", KeepsBreadth: true, Kind: ledger.QuestionScope})
	ask(t, handler, p, ledger.QuestionRequest{To: "widget-G1", Text: "how broad?", DefaultAnswer: "a smaller first version", Kind: ledger.QuestionScope})
	ask(t, handler, p, ledger.QuestionRequest{To: ledger.ProjectTarget, Text: "publish the beta?", Kind: ledger.QuestionOutward})
	d.background.wait()
	asks := openLedgerAsks(t, r)
	if len(asks) != 3 {
		t.Fatalf("asks=%+v, want one per question", asks)
	}
	for _, ask := range asks {
		if ask.RoutedTo != registry.AskToPerson || ask.SessionID != "s1" || ask.Deadline.IsZero() {
			t.Fatalf("ask=%+v, want it with the person, on the asking session, with its deadline", ask)
		}
	}
	if alerts := desktop.matching(""); len(alerts) != 3 {
		t.Fatalf("the person was alerted %d times, want once per question: %+v", len(alerts), alerts)
	}

	d.sweepLedgerQuestions(time.Now())
	d.syncAllLedgerAsks()
	d.background.wait()
	if len(openLedgerAsks(t, r)) != 3 {
		t.Fatal("questions expired before their deadline")
	}
	later := time.Now().Add(2 * time.Hour)
	d.sweepLedgerQuestions(later)
	d.sweepLedgerQuestions(later.Add(time.Minute))
	d.background.wait()
	d.syncAllLedgerAsks()
	d.background.wait()

	open := openLedgerAsks(t, r)
	if len(open) != 2 {
		t.Fatalf("open=%+v, want the cut and the outward question still with the person", open)
	}
	if got := noticesTo(t, r, "s1", "its default applied: full-text and fuzzy"); got != 1 {
		t.Fatalf("owner told of the applied default %d times, want once", got)
	}
	if got := noticesTo(t, r, "s1", "[intent ledger]"); got != 1 {
		t.Fatalf("owner got %d ledger notices, want only the applied default", got)
	}
	if events := daemonEvents(t, r, "ledger.question."); len(events) != 0 {
		t.Fatalf("events=%+v, a question's expiry is its ask's state", events)
	}
	if alerts := desktop.matching(""); len(alerts) != 3 {
		t.Fatalf("the person was alerted %d times, want no new alert at the deadline: %+v", len(alerts), alerts)
	}
	board, err := p.store.Board(p.key)
	if err != nil {
		t.Fatal(err)
	}
	if len(board.Decisions()) != 1 {
		t.Fatalf("decisions = %+v; only the breadth-keeping default may apply", board.Decisions())
	}
}
