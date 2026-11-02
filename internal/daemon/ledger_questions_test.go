package daemon

import (
	"context"
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

func TestExpiredQuestionsDefaultCutOrEscalateAndTellTheOwner(t *testing.T) {
	d, r, handler, fake, desktop, p := questionDaemon(t)
	ledgerWrite(t, handler, "/api/v1/ledger/goal", ledgerGoalRequest{ProjectKey: p.key, SessionID: "s1", Name: "Search"}, nil)
	ask(t, handler, p, ledger.QuestionRequest{To: "widget-G1", Text: "which index?", DefaultAnswer: "full-text and fuzzy", KeepsBreadth: true, Kind: ledger.QuestionScope})
	ask(t, handler, p, ledger.QuestionRequest{To: "widget-G1", Text: "how broad?", DefaultAnswer: "a smaller first version", Kind: ledger.QuestionScope})
	ask(t, handler, p, ledger.QuestionRequest{To: ledger.ProjectTarget, Text: "publish the beta?", Kind: ledger.QuestionOutward})

	d.sweepLedgerQuestions(context.Background(), time.Now())
	if len(daemonEvents(t, r, "ledger.question.")) != 0 || len(fake.sent) != 0 {
		t.Fatal("questions expired before their deadline")
	}
	later := time.Now().Add(2 * time.Hour)
	d.sweepLedgerQuestions(context.Background(), later)
	d.sweepLedgerQuestions(context.Background(), later.Add(time.Minute))

	var outcomes []string
	for _, e := range daemonEvents(t, r, "ledger.question.") {
		outcomes = append(outcomes, e.Type)
	}
	if len(outcomes) != 3 || !strings.Contains(strings.Join(outcomes, " "), "ledger.question.defaulted") || !strings.Contains(strings.Join(outcomes, " "), "ledger.question.cut") || !strings.Contains(strings.Join(outcomes, " "), "ledger.question.escalated") {
		t.Fatalf("want one defaulted, one cut, one escalated event, got %v", outcomes)
	}
	if len(fake.sent) != 3 {
		t.Fatalf("owner told %d times, want 3: %q", len(fake.sent), fake.sent)
	}
	all := strings.Join(fake.sent, "\n")
	for _, want := range []string{"default applied as widget-D1", "widget-X1 is a pending cut", "outward act, so nothing was done"} {
		if !strings.Contains(all, want) {
			t.Fatalf("owner notices lack %q:\n%s", want, all)
		}
	}
	if alerts := desktop.matching(""); len(alerts) != 2 {
		t.Fatalf("the person was alerted %d times, want 2 (cut and outward): %+v", len(alerts), alerts)
	}
	board, err := p.store.Board(p.key)
	if err != nil {
		t.Fatal(err)
	}
	if len(board.Decisions()) != 1 {
		t.Fatalf("decisions = %+v; only the breadth-keeping default may apply", board.Decisions())
	}
}
