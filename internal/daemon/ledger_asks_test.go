package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func openLedgerAsks(t *testing.T, r *registry.Registry) []registry.Ask {
	t.Helper()
	open, err := r.OpenAsks()
	if err != nil {
		t.Fatal(err)
	}
	var asks []registry.Ask
	for _, ask := range open {
		if ask.Origin == registry.AskOriginLedger {
			asks = append(asks, ask)
		}
	}
	return asks
}

func ledgerAskFor(t *testing.T, r *registry.Registry, p ledgerProject, questionID string) registry.Ask {
	t.Helper()
	for _, ask := range openLedgerAsks(t, r) {
		if ask.Ref == registry.LedgerAskRef(p.key, questionID) {
			return ask
		}
	}
	t.Fatalf("no open ask for %s in %+v", questionID, openLedgerAsks(t, r))
	return registry.Ask{}
}

func boardQuestion(t *testing.T, p ledgerProject, id string) ledger.Question {
	t.Helper()
	board, err := p.store.Board(p.key)
	if err != nil {
		t.Fatal(err)
	}
	q, found := ledgerQuestion(board, id)
	if !found {
		t.Fatalf("no question %s", id)
	}
	return q
}

func respondTo(ask registry.Ask, decision string) string {
	return fmt.Sprintf(`{"action":"respond","sessionId":%q,"requestId":%q,"decision":%s}`, ask.SessionID, ask.ID, decision)
}

func TestThePersonAnswersALedgerQuestionThroughItsAsk(t *testing.T) {
	d, r, handler, _, _, p := questionDaemon(t)
	ledgerWrite(t, handler, "/api/v1/ledger/goal", ledgerGoalRequest{ProjectKey: p.key, SessionID: "s1", Name: "Search"}, nil)
	recommendation := "index the bodies too, so a search finds words that appear only inside an article body"
	picked := ask(t, handler, p, ledger.QuestionRequest{To: "widget-G1", Text: "which index?", Recommendation: recommendation, DefaultAnswer: "titles only", Kind: ledger.QuestionScope})
	typed := ask(t, handler, p, ledger.QuestionRequest{To: "widget-G1", Text: "how broad?", DefaultAnswer: "a smaller first version", Kind: ledger.QuestionScope})
	d.background.wait()

	first := ledgerAskFor(t, r, p, picked.ID)
	options := first.Questions[0].Options
	if len(options) != 2 || options[0].ID != registry.LedgerRecommended || options[1].ID != registry.LedgerDefault {
		t.Fatalf("options=%+v, want the recommendation then the default", options)
	}
	assertAPIV1Error(t, postActionAs(t, handler, "test-token", respondTo(first, `{"choices":["deny"]}`)), 400, "invalid_request")
	if response := postActionAs(t, handler, "test-token", respondTo(first, `{"choices":["recommended"]}`)); response.Code != 200 {
		t.Fatalf("answer status=%d body=%s", response.Code, response.Body.String())
	}
	if q := boardQuestion(t, p, picked.ID); q.State != ledger.QuestionAnswered || q.Answer != recommendation {
		t.Fatalf("question=%+v, want it answered with the whole recommendation", q)
	}
	if response := postActionAs(t, handler, "test-token", respondTo(first, `{"choices":["recommended"]}`)); response.Code != 409 {
		t.Fatalf("repeat status=%d body=%s, want the answered ask stale", response.Code, response.Body.String())
	}

	second := ledgerAskFor(t, r, p, typed.ID)
	if response := postActionAs(t, handler, "test-token", respondTo(second, `{"answers":{"how broad?":"the whole thing"},"message":"ship it behind a flag"}`)); response.Code != 200 {
		t.Fatalf("typed answer status=%d body=%s", response.Code, response.Body.String())
	}
	q := boardQuestion(t, p, typed.ID)
	if q.State != ledger.QuestionAnswered || q.Answer != "the whole thing\n\nship it behind a flag" {
		t.Fatalf("question=%+v, want the typed answer and the note", q)
	}
	board, err := p.store.Board(p.key)
	if err != nil {
		t.Fatal(err)
	}
	if u, found := board.Utterance(q.AnsweredBy); !found || u.CapturedBy != ledger.CapturedByAsk || u.SpeakerID != surface.PersonSessionID {
		t.Fatalf("utterance=%+v, want the person's words captured by the ask", u)
	}
	d.background.wait()
	if len(openLedgerAsks(t, r)) != 0 {
		t.Fatalf("asks=%+v after both answers", openLedgerAsks(t, r))
	}
	for _, id := range []string{picked.ID, typed.ID} {
		if got := noticesTo(t, r, "s1", "The person answered "+id); got != 1 {
			t.Fatalf("owner told of the answer to %s %d times, want once", id, got)
		}
	}
}

func TestAQuestionAnsweredInTheLedgerWithdrawsItsAsk(t *testing.T) {
	d, r, handler, _, desktop, p := questionDaemon(t)
	ledgerWrite(t, handler, "/api/v1/ledger/goal", ledgerGoalRequest{ProjectKey: p.key, SessionID: "s1", Name: "Search"}, nil)
	q := ask(t, handler, p, ledger.QuestionRequest{To: "widget-G1", Text: "which index?", DefaultAnswer: "titles only", Kind: ledger.QuestionScope})
	d.background.wait()
	asked := ledgerAskFor(t, r, p, q.ID)
	if _, _, err := d.ledgerStore().RecordUtterance(ledger.Utterance{ProjectKey: p.key, Ref: "claude-prompt:typed", SpeakerID: surface.PersonSessionID, TargetID: "s1", Text: "bodies too", CapturedBy: ledger.CapturedByHook}); err != nil {
		t.Fatal(err)
	}
	if code, body := ledgerWrite(t, handler, "/api/v1/ledger/attach", ledger.AttachRequest{ProjectKey: p.key, SessionID: "s1", UtteranceID: "claude-prompt:typed", As: ledger.AsAnswers, To: q.ID}, nil); code != 200 {
		t.Fatalf("attach status=%d body=%s", code, body)
	}
	d.background.wait()
	closed, found, err := r.Ask(asked.ID)
	if err != nil || !found || closed.State != registry.AskWithdrawn {
		t.Fatalf("ask=%+v found=%v err=%v, want it withdrawn", closed, found, err)
	}
	if alerts, notices := desktop.matching(""), noticesTo(t, r, "s1", "[intent ledger]"); len(alerts) != 1 || notices != 0 {
		t.Fatalf("alerts=%+v notices=%d, want only the first alert and no notice to the owner", alerts, notices)
	}
}

func TestOnlyThePersonAnswersALedgerQuestionThatItsRequestsList(t *testing.T) {
	d, r, handler, _, _, p := questionDaemon(t)
	ledgerWrite(t, handler, "/api/v1/ledger/goal", ledgerGoalRequest{ProjectKey: p.key, SessionID: "s1", Name: "Search"}, nil)
	q := ask(t, handler, p, ledger.QuestionRequest{To: "widget-G1", Text: "which index?", DefaultAnswer: "titles only", Kind: ledger.QuestionScope})
	d.background.wait()
	asked := ledgerAskFor(t, r, p, q.ID)
	listed := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/requests?sessionId=s1", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	handler.ServeHTTP(listed, request)
	if body := listed.Body.String(); listed.Code != http.StatusOK || !strings.Contains(body, asked.ID) || !strings.Contains(body, `"id":"recommended"`) || !strings.Contains(body, `"id":"default"`) || !strings.Contains(body, `"deadline":`) || !strings.Contains(body, `"suggested":0`) || strings.Contains(body, `"optionsOnly":true`) {
		t.Fatalf("requests status=%d body=%s, want the ledger ask with its option ids, its deadline, and free text allowed", listed.Code, body)
	}
	row, found, err := r.RowAsk("s1")
	if err != nil || !found || row.ID != asked.ID || !askRow(&row).asksPerson() || askRow(&row).Deadline.IsZero() {
		t.Fatalf("row ask=%+v found=%v err=%v, want the ledger ask with its deadline on the session's row", row, found, err)
	}
	agent := sessionPrincipal("s1")
	agent.ForPerson = true
	refused := d.performAction(t.Context(), agent, actionFormatAPIv1, []byte(respondTo(asked, `{"choices":["recommended"]}`)), "agent")
	if refused.Status != http.StatusForbidden {
		t.Fatalf("an agent acting for the person answered: status=%d body=%s", refused.Status, refused.Body)
	}
	if got := boardQuestion(t, p, q.ID); got.State != ledger.QuestionOpen || len(openLedgerAsks(t, r)) != 1 {
		t.Fatalf("question=%+v, want it and its ask still open", got)
	}
}

func TestALedgerAskAnsweredForAProjectKeptOnAnotherHostLandsOnce(t *testing.T) {
	mac, owner, _ := ledgerOnTwoHosts(t)
	mustLedger(t, mac, "/api/v1/ledger/owner", ledgerOwnerRequest{ProjectKey: widgetKey, Host: "vps"}, nil)
	mustLedger(t, mac, "/api/v1/ledger/goal", ledgerGoalRequest{ProjectKey: widgetKey, SessionID: "local-a", Name: "Search"}, nil)
	var q ledger.Question
	mustLedger(t, mac, "/api/v1/ledger/question", ledger.QuestionRequest{ProjectKey: widgetKey, SessionID: "local-a", To: "widget-G1", Text: "which index?", Recommendation: "bodies too", DefaultAnswer: "titles only", Kind: ledger.QuestionScope, DeadlineMs: time.Now().Add(time.Hour).UnixMilli()}, &q)
	var asked registry.Ask
	testkit.Eventually(t, "the mac holds the ask of its session's question", 10*time.Second, func() bool {
		mac.daemon.syncLedgerAsks(widgetKey)
		open := openLedgerAsks(t, mac.reg)
		if len(open) == 1 {
			asked = open[0]
		}
		return len(open) == 1
	})
	owner.daemon.syncLedgerAsks(widgetKey)
	owner.daemon.background.wait()
	if open := openLedgerAsks(t, owner.reg); len(open) != 0 {
		t.Fatalf("the owner raised %+v for a session it does not run", open)
	}

	owner.gate.ledgerDrops.Store(true)
	owner.gate.drops.Store(100)
	lost := mac.request(t, http.MethodPost, "/api/v1/actions", json.RawMessage(respondTo(asked, `{"choices":["recommended"]}`)))
	owner.gate.drops.Store(0)
	if lost.Code != http.StatusGatewayTimeout {
		t.Fatalf("lost answer status=%d body=%s, want the write reported unconfirmed", lost.Code, lost.Body.String())
	}
	for _, decision := range []string{`{"choices":["recommended"]}`, `{"choices":["default"]}`} {
		if retry := mac.request(t, http.MethodPost, "/api/v1/actions", json.RawMessage(respondTo(asked, decision))); retry.Code == http.StatusOK {
			t.Fatalf("retry %s answered again: %s", decision, retry.Body.String())
		}
	}
	sameLedgerOnBothHosts(t, widgetKey, mac, owner)
	if n := countEvents(t, owner.reg, ledger.EventQuestionRes, ""); n != 1 {
		t.Fatalf("the owner recorded %d answers, want the lost one once", n)
	}
	board, err := ledger.NewStore(owner.reg.WriteDB(), owner.reg.ReadDB()).Board(widgetKey)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := ledgerQuestion(board, q.ID); got.State != ledger.QuestionAnswered || got.Answer != "bodies too" {
		t.Fatalf("question=%+v, want the first answer", got)
	}
	testkit.Eventually(t, "the mac closes the answered question's ask", 10*time.Second, func() bool {
		mac.daemon.syncLedgerAsks(widgetKey)
		return len(openLedgerAsks(t, mac.reg)) == 0
	})
}
