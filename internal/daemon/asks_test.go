package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

type askFixture struct {
	d       *Daemon
	r       *registry.Registry
	fake    *daemonSurface
	desktop *desktopRecorder
	handler http.Handler
	person  *mcp.ClientSession
	worker  *mcp.ClientSession
	token   string
	from    surface.Session
	to      surface.Session
}

func newAskFixture(t *testing.T) askFixture {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	d, r, fake, from, to := daemonFixture(t)
	desktop := &desktopRecorder{}
	d.notifyDesktop = desktop.notify
	if err := r.RecordSessionOrigin(registry.OriginOf(to.ID, from.ID)); err != nil {
		t.Fatal(err)
	}
	server := mcpServer(t, d)
	token := issueMCPToken(t, d, from)
	return askFixture{d: d, r: r, fake: fake, desktop: desktop, handler: d.dashboardHandler(&dashboardServer{token: dashboardTestToken}),
		person: mcpSession(t, server, token), worker: mcpSession(t, server, issueMCPToken(t, d, to)), token: token, from: from, to: to}
}

func (f askFixture) turn(t *testing.T, session surface.Session, id string, during func()) {
	t.Helper()
	f.fake.observations[session.ID] = &surface.TurnObservation{Status: surface.StatusIdle}
	f.d.observeSession(context.Background(), f.fake, &session)
	f.fake.observations[session.ID] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: id}
	f.d.observeSession(context.Background(), f.fake, &session)
	during()
	f.fake.observations[session.ID] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: id, Reply: &surface.ReplyResult{Text: "reply", Done: true}}
	f.d.observeSession(context.Background(), f.fake, &session)
}

func (f askFixture) alert(t *testing.T, suffix string) {
	t.Helper()
	testkit.Eventually(t, "a desktop alert ending "+suffix, 5*time.Second, func() bool { return len(f.desktop.matching(suffix)) == 1 })
}

func (f askFixture) raise(t *testing.T, session *mcp.ClientSession, ask map[string]any) map[string]any {
	t.Helper()
	out, refused := callMCP(t, session, "agenthail_ask", ask)
	if refused {
		t.Fatalf("ask refused: %v", out)
	}
	return out
}

func (f askFixture) requests(t *testing.T, sessionID string) []openRequestView {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/requests?sessionId="+sessionID, nil)
	request.Header.Set("Authorization", "Bearer "+dashboardTestToken)
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	var body struct {
		Requests []openRequestView `json:"requests"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != http.StatusOK {
		t.Fatalf("requests: %d %s", response.Code, response.Body)
	}
	return body.Requests
}

func (f askFixture) respond(sessionID, requestID, key string, decision map[string]any) *httptest.ResponseRecorder {
	encoded, _ := json.Marshal(map[string]any{"action": "respond", "sessionId": sessionID, "requestId": requestID, "decision": decision})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/actions", bytes.NewReader(encoded))
	request.Header.Set("Authorization", "Bearer "+dashboardTestToken)
	request.Header.Set("Idempotency-Key", key)
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	return response
}

func (f askFixture) told(t *testing.T, sessionID string) []string {
	t.Helper()
	queued, err := f.r.ActiveQueue(sessionID, f.d.adapters())
	if err != nil {
		t.Fatal(err)
	}
	told := []string{}
	for _, row := range queued {
		told = append(told, row.Message)
	}
	return told
}

func (f askFixture) ask(t *testing.T, id string) registry.Ask {
	t.Helper()
	ask, found, err := f.r.Ask(id)
	if err != nil || !found {
		t.Fatalf("ask %s found=%v err=%v", id, found, err)
	}
	return ask
}

var deployAsk = map[string]any{"title": "Deploy the fix?", "questions": []any{map[string]any{"question": "Deploy it now?", "options": []any{map[string]any{"label": "Deploy"}, map[string]any{"label": "Wait", "does": "hold until Monday"}}, "recommended": 0}}}

var twoQuestions = map[string]any{"title": "Ready to ship", "questions": []any{
	map[string]any{"question": "Deploy it now?", "options": []any{map[string]any{"label": "Deploy"}, map[string]any{"label": "Wait"}}, "recommended": 0},
	map[string]any{"question": "Which region first?", "options": []any{map[string]any{"label": "us"}, map[string]any{"label": "eu"}}, "links": []any{map[string]any{"title": "Runbook", "url": "https://example.com/runbook"}}},
}}

func viewRow(t *testing.T, d *Daemon, id string) dashboardSession {
	t.Helper()
	d.discoverCatalog(context.Background())
	view, err := d.readSessionView(context.Background(), sessionViewRequest{Scope: "all", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range view.Sessions {
		if row.ID == id {
			return row
		}
	}
	t.Fatalf("%s is not in the session view", id)
	return dashboardSession{}
}

func TestAPersonsSessionThatAsksIsInNeedsYouUntilAnsweredAndItsTurnEndsQuietly(t *testing.T) {
	f := newAskFixture(t)
	var id string
	f.turn(t, f.from, "turn-1", func() {
		out := f.raise(t, f.person, deployAsk)
		if out["routedTo"] != registry.AskToPerson {
			t.Fatalf("ask: %v", out)
		}
		id, _ = out["askId"].(string)
		if _, refused := callMCP(t, f.person, "agenthail_end_turn", map[string]any{"headline": "Waiting on the deploy call"}); refused {
			t.Fatal("end turn refused")
		}
	})
	f.alert(t, " needs you: Deploy the fix?")
	row := viewRow(t, f.d, f.from.ID)
	if row.Ask == nil || row.Ask.ID != id || row.Ask.RoutedTo != registry.AskToPerson || row.Ask.AskedAt.IsZero() || row.Attention == nil || row.Attention.Kind != AttentionAsk || !presentationNeedsYou(&row) {
		t.Fatalf("row ask=%+v attention=%+v", row.Ask, row.Attention)
	}
	if row.TurnCompletion == nil || row.TurnCompletion.Outcome != turnOutcomeNeedsYou || row.TurnCompletion.RequestID != id {
		t.Fatalf("turn completion %+v", row.TurnCompletion)
	}
	f.d.background.wait()
	if done := f.desktop.matching(" done: Waiting on the deploy call"); len(done) != 0 {
		t.Fatalf("a turn that left an ask also announced itself done: %+v", done)
	}

	if response := f.respond(f.from.ID, id, "answer", map[string]any{"choice": "o0"}); response.Code != http.StatusOK {
		t.Fatalf("answer: %d %s", response.Code, response.Body)
	}
	row = viewRow(t, f.d, f.from.ID)
	if row.Ask == nil || row.Ask.AnsweredAt.IsZero() || row.Attention != nil || presentationNeedsYou(&row) {
		t.Fatalf("after the answer: ask=%+v attention=%+v", row.Ask, row.Attention)
	}

	f.turn(t, f.from, "turn-2", func() {
		if _, refused := callMCP(t, f.person, "agenthail_end_turn", map[string]any{"headline": "Deployed", "next": []any{"watch the error rate"}}); refused {
			t.Fatal("the next turn could not end")
		}
	})
	f.alert(t, " done: Deployed")
	if row := viewRow(t, f.d, f.from.ID); row.Ask != nil || row.EndTurn == nil || row.EndTurn.Headline != "Deployed" {
		t.Fatalf("after the next turn: ask=%+v endTurn=%+v", row.Ask, row.EndTurn)
	}
}

func TestACompletionHeldForBackgroundWorkStaysQuietWhileTheAskIsOpen(t *testing.T) {
	f := newAskFixture(t)
	id, _ := f.raise(t, f.person, deployAsk)["askId"].(string)
	f.alert(t, " needs you: Deploy the fix?")
	if err := f.r.BindTurnCompletion(f.from.ID, "turn-1"); err != nil {
		t.Fatal(err)
	}
	if err := f.r.SaveRuntimeState(f.from.ID, surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "turn-1"}); err != nil {
		t.Fatal(err)
	}
	if err := f.r.HoldCompletion(f.from.ID, "turn-1"); err != nil {
		t.Fatal(err)
	}
	f.d.armCompletionNotification(f.from.ID)
	f.d.releaseUnobservedHold(context.Background(), f.fake, &f.from)
	f.d.background.wait()
	if notices, err := f.r.TurnNotices(); err != nil || notices[f.from.ID].Outcome != turnOutcomeNeedsYou || notices[f.from.ID].RequestID != id {
		t.Fatalf("notice %+v err=%v", notices[f.from.ID], err)
	}
	if alerts := f.desktop.matching(" needs you: Deploy the fix?"); len(alerts) != 1 {
		t.Fatalf("the ask was announced %d times", len(alerts))
	}
}

func TestAWorkersAskGoesToWhoStartedItWhichAnswersIt(t *testing.T) {
	f := newAskFixture(t)
	out := f.raise(t, f.worker, deployAsk)
	id, _ := out["askId"].(string)
	if out["routedTo"] != f.from.ID {
		t.Fatalf("the worker's ask went to %v", out["routedTo"])
	}
	told := append(f.fake.sent, f.told(t, f.from.ID)...)
	if len(told) != 1 || !strings.Contains(told[0], "asks: Deploy the fix?") || !strings.Contains(told[0], "Wait (hold until Monday)") || !strings.Contains(told[0], `agenthail_answer {"requestId":"`+id+`"`) {
		t.Fatalf("the spawner was told %q", told)
	}
	if row := viewRow(t, f.d, f.to.ID); row.Attention != nil || row.Ask == nil || row.Ask.RoutedTo != f.from.ID {
		t.Fatalf("a worker's ask put it in front of the person: ask=%+v attention=%+v", row.Ask, row.Attention)
	}
	f.d.background.wait()
	if alerts := f.desktop.matching(" needs you: Deploy the fix?"); len(alerts) != 0 {
		t.Fatal("the person was told a worker needs them")
	}

	if code := refusalCode(t, f.worker, "agenthail_answer", map[string]any{"requestId": id, "message": "myself", "clientRequestId": "self"}); code != "refused" {
		t.Fatalf("the worker answered its own ask: %q", code)
	}
	sent := len(f.fake.sent)
	if out, refused := callMCP(t, f.person, "agenthail_answer", map[string]any{"requestId": id, "answers": map[string]any{"Deploy it now?": "Wait"}, "clientRequestId": "answer"}); refused {
		t.Fatalf("the spawner's answer: %v", out)
	}
	if got := f.fake.sent[sent:]; len(got) != 1 || got[0] != "1. Deploy it now? Wait" {
		t.Fatalf("the worker got %q", got)
	}
	if ask := f.ask(t, id); ask.State != registry.AskAnswered || ask.AnsweredBy != f.from.ID {
		t.Fatalf("ask %+v", ask)
	}
	if code := refusalCode(t, f.person, "agenthail_answer", map[string]any{"requestId": id, "message": "again", "clientRequestId": "again"}); code == "" {
		t.Fatal("an answered ask was answered again")
	}
}

func TestAnAskTheHearerPassesOnSitsOnOrCannotHearReachesThePerson(t *testing.T) {
	f := newAskFixture(t)
	passed, _ := f.raise(t, f.worker, deployAsk)["askId"].(string)
	if out, refused := callMCP(t, f.person, "agenthail_answer", map[string]any{"requestId": passed, "decision": "escalate", "clientRequestId": "pass"}); refused {
		t.Fatalf("escalate: %v", out)
	}
	if ask := f.ask(t, passed); ask.RoutedTo != registry.AskToPerson || len(ask.Route) != 2 || ask.Route[1].Why != registry.AskEscalated {
		t.Fatalf("passed on: %+v", ask)
	}
	f.alert(t, " needs you: Deploy the fix?")
	if requests := f.requests(t, f.to.ID); len(requests) != 1 || requests[0].ID != passed {
		t.Fatalf("the person's requests %+v", requests)
	}

	sat, _ := f.raise(t, f.worker, map[string]any{"title": "Which port?", "questions": []any{map[string]any{"question": "Port?", "options": []any{map[string]any{"label": "8080"}}}}})["askId"].(string)
	f.d.sweepAsks(time.Now().Add(9 * time.Minute))
	if ask := f.ask(t, sat); ask.RoutedTo != f.from.ID {
		t.Fatalf("an ask moved before its window: %+v", ask)
	}
	f.d.sweepAsks(time.Now().Add(10 * time.Minute))
	if ask := f.ask(t, sat); ask.RoutedTo != registry.AskToPerson {
		t.Fatalf("an ask sat on past its window stayed: %+v", ask)
	}
	f.alert(t, " needs you: Which port?")
	if queued := f.told(t, f.from.ID); len(queued) != 0 {
		t.Fatalf("the spawner still has notices of asks that left it: %q", queued)
	}

	unheard, _ := f.raise(t, f.worker, map[string]any{"title": "Which host?", "questions": []any{map[string]any{"question": "Host?", "options": []any{map[string]any{"label": "a"}}}}})["askId"].(string)
	ended := f.from
	ended.Ended = true
	if err := f.r.RegisterSession(ended); err != nil {
		t.Fatal(err)
	}
	f.d.sweepAsks(time.Now())
	if ask := f.ask(t, unheard); ask.RoutedTo != registry.AskToPerson {
		t.Fatalf("an ask held by a session that ended stayed: %+v", ask)
	}
	if out := f.raise(t, f.worker, deployAsk); out["routedTo"] != registry.AskToPerson {
		t.Fatalf("an ask whose spawner has ended went to %v", out["routedTo"])
	}
}

func TestInAProjectWithALeadOnlyAgentStartedSessionsAskTheLead(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newNoticeFixture(t)
	f.setLead(t, "lead")
	for id, via := range map[string]registry.OriginVia{"by-person": registry.OriginPerson, "by-voice": registry.OriginVoice} {
		session := f.worker
		session.ID = id
		if err := f.reg.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
		f.fake.sessions[id] = session
		if err := f.reg.RecordSessionOrigin(registry.SessionOrigin{SessionID: id, Via: via}); err != nil {
			t.Fatal(err)
		}
	}
	server := mcpServer(t, f.d)
	asked := map[string]any{}
	for _, id := range []string{"worker", "by-person", "by-voice"} {
		session, err := f.reg.Session(id)
		if err != nil {
			t.Fatal(err)
		}
		out, refused := callMCP(t, mcpSession(t, server, issueMCPToken(t, f.d, *session)), "agenthail_ask", deployAsk)
		if refused {
			t.Fatalf("%s: %v", id, out)
		}
		asked[id] = out["routedTo"]
	}
	if asked["worker"] != "@widget-lead" || asked["by-person"] != registry.AskToPerson || asked["by-voice"] != registry.AskToPerson {
		t.Fatalf("asked %v: the worker asks its lead, the person's and voice's sessions ask the person", asked)
	}
	for _, id := range []string{"by-person", "by-voice"} {
		if row := viewRow(t, f.d, id); row.Attention == nil || row.Attention.Kind != AttentionAsk {
			t.Fatalf("%s is not in front of the person: %+v", id, row.Attention)
		}
	}
}

func TestAnAskTheAgentCanFixIsRefused(t *testing.T) {
	f := newAskFixture(t)
	question := func(text string, options ...any) map[string]any {
		return map[string]any{"question": text, "options": options}
	}
	deploy := map[string]any{"label": "Deploy"}
	cases := map[string]map[string]any{
		"a blank title":              {"title": "  ", "questions": []any{question("q?", deploy)}},
		"a two-line title":           {"title": "one\ntwo", "questions": []any{question("q?", deploy)}},
		"a long title":               {"title": strings.Repeat("x", 81), "questions": []any{question("q?", deploy)}},
		"no questions":               {"title": "t"},
		"a question without options": {"title": "t", "questions": []any{question("q?")}},
		"recommended out of range":   {"title": "t", "questions": []any{map[string]any{"question": "q?", "options": []any{deploy}, "recommended": 1}}},
		"a link that is not a url":   {"title": "t", "questions": []any{map[string]any{"question": "q?", "options": []any{deploy}, "links": []any{map[string]any{"title": "PR", "url": "not a url"}}}}},
		"one question asked twice":   {"title": "t", "questions": []any{question("q?", deploy), question("q?", deploy)}},
		"five questions":             {"title": "t", "questions": []any{question("1", deploy), question("2", deploy), question("3", deploy), question("4", deploy), question("5", deploy)}},
		"an option label over limit": {"title": "t", "questions": []any{question("q?", map[string]any{"label": strings.Repeat("x", 41)})}},
	}
	for name, args := range cases {
		if code := refusalCode(t, f.person, "agenthail_ask", args); code != "invalid" {
			t.Errorf("%s: refusal %q, want invalid", name, code)
		}
	}
	for name, args := range map[string]map[string]any{"a blank headline": {"headline": " "}, "four next items": {"headline": "h", "next": []any{"a", "b", "c", "d"}}} {
		if code := refusalCode(t, f.person, "agenthail_end_turn", args); code != "invalid" {
			t.Errorf("end turn with %s: refusal %q, want invalid", name, code)
		}
	}
	if asks, err := f.r.OpenAsks(); err != nil || len(asks) != 0 {
		t.Fatalf("a refused ask was stored: %+v err=%v", asks, err)
	}
}

func TestAWithdrawnAskLeavesThePersonsList(t *testing.T) {
	f := newAskFixture(t)
	id, _ := f.raise(t, f.person, deployAsk)["askId"].(string)
	if out, refused := callMCP(t, f.person, "agenthail_ask", map[string]any{"withdraw": id}); refused || out["outcome"] != "withdrawn" {
		t.Fatalf("withdraw: %v", out)
	}
	if ask := f.ask(t, id); ask.State != registry.AskWithdrawn {
		t.Fatalf("ask after the withdrawal %+v", ask)
	}
	if row := viewRow(t, f.d, f.from.ID); row.Attention != nil || presentationNeedsYou(&row) {
		t.Fatalf("a withdrawn ask still needs the person: %+v", row.Attention)
	}
	if code := refusalCode(t, f.person, "agenthail_ask", map[string]any{"withdraw": id}); code != "stale_request" {
		t.Fatalf("a second withdrawal: %q", code)
	}
	if code := refusalCode(t, f.worker, "agenthail_ask", map[string]any{"withdraw": id}); code != "not_found" {
		t.Fatalf("another session withdrew it: %q", code)
	}
}

func TestOneReplyAnswersEveryQuestionOfAnAskOnce(t *testing.T) {
	f := newAskFixture(t)
	f.raise(t, f.person, twoQuestions)
	requests := f.requests(t, f.from.ID)
	if len(requests) != 1 {
		t.Fatalf("requests %+v", requests)
	}
	ask := requests[0]
	if ask.Kind != surface.WaitingAsk || ask.Prompt != "Ready to ship" || len(ask.Questions) != 2 || ask.Questions[0].Suggested == nil || *ask.Questions[0].Suggested != 0 || len(ask.Questions[1].Links) != 1 || ask.Actions != nil {
		t.Fatalf("ask %+v", ask)
	}
	if row := viewRow(t, f.d, f.from.ID); row.Ask == nil || row.Ask.ID != ask.ID || row.Ask.Title != "Ready to ship" {
		t.Fatalf("the row does not name its ask: %+v", row.Ask)
	}

	decision := map[string]any{"behavior": "allow", "answers": map[string]any{"Deploy it now?": "Deploy", "Which region first?": "eu, then us"}, "message": "Ship after lunch."}
	first := f.respond(f.from.ID, ask.ID, "answer-1", decision)
	if first.Code != http.StatusOK {
		t.Fatalf("answer: %d %s", first.Code, first.Body)
	}
	want := "1. Deploy it now? Deploy\n2. Which region first? eu, then us\n\nShip after lunch."
	if len(f.fake.sent) != 1 || f.fake.sent[0] != want {
		t.Fatalf("sent %q, want one message %q", f.fake.sent, want)
	}
	if retry := f.respond(f.from.ID, ask.ID, "answer-1", decision); retry.Code != http.StatusOK || retry.Body.String() != first.Body.String() {
		t.Fatalf("a retry with the same key: %d %s", retry.Code, retry.Body)
	}
	if again := f.respond(f.from.ID, ask.ID, "answer-2", decision); again.Code != http.StatusConflict || !strings.Contains(again.Body.String(), "stale") {
		t.Fatalf("a second answer: %d %s", again.Code, again.Body)
	}
	if len(f.fake.sent) != 1 {
		t.Fatalf("the answer was sent %d times", len(f.fake.sent))
	}
	if stored := f.ask(t, ask.ID); stored.AnsweredBy != registry.AskToPerson || stored.Answer.Answers["Which region first?"] != "eu, then us" || stored.Answer.Note != "Ship after lunch." {
		t.Fatalf("stored answer %+v", stored)
	}
}

func TestTwoDevicesAnsweringAtOnceSendOneAnswer(t *testing.T) {
	f := newAskFixture(t)
	id, _ := f.raise(t, f.person, twoQuestions)["askId"].(string)
	codes := make([]int, 2)
	var wait sync.WaitGroup
	for i := range codes {
		wait.Add(1)
		go func() {
			defer wait.Done()
			codes[i] = f.respond(f.from.ID, id, "device-"+string(rune('a'+i)), map[string]any{"message": "go ahead"}).Code
		}()
	}
	wait.Wait()
	if !(codes[0] == http.StatusOK && codes[1] == http.StatusConflict || codes[0] == http.StatusConflict && codes[1] == http.StatusOK) || len(f.fake.sent) != 1 {
		t.Fatalf("codes=%v sent=%q", codes, f.fake.sent)
	}
}

type requestPush struct {
	message, requestID string
	actions            []surface.RequestAction
}

func TestANotificationButtonAnswersASingleQuestionAsk(t *testing.T) {
	f := newAskFixture(t)
	var mu sync.Mutex
	var pushed []requestPush
	f.d.notifyDesktop = func(_, message, _, _, requestID string, actions []surface.RequestAction) error {
		mu.Lock()
		defer mu.Unlock()
		pushed = append(pushed, requestPush{message, requestID, actions})
		return nil
	}
	id, _ := f.raise(t, f.person, deployAsk)["askId"].(string)
	testkit.Eventually(t, "the needs-you push", 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(pushed) == 1
	})
	if push := pushed[0]; push.requestID != id || len(push.actions) != 3 || push.actions[1].ID != "o1" || push.actions[2].ID != "reply" || !strings.HasSuffix(push.message, "needs you: Deploy the fix?") {
		t.Fatalf("push %+v, ask %s", push, id)
	}
	if response := f.respond(f.from.ID, id, "button", map[string]any{"choice": "o1"}); response.Code != http.StatusOK {
		t.Fatalf("button: %d %s", response.Code, response.Body)
	}
	if len(f.fake.sent) != 1 || f.fake.sent[0] != "1. Deploy it now? Wait" {
		t.Fatalf("sent %q", f.fake.sent)
	}
}

func TestAnAnswerThatProvablyFailedCanBeGivenAgainAndOneThatMayHaveArrivedCannot(t *testing.T) {
	f := newAskFixture(t)
	id, _ := f.raise(t, f.person, deployAsk)["askId"].(string)
	f.fake.sendErr = surface.DeliveryTerminal(errors.New("the session is gone"), surface.DeliveryTargetMissing)
	if response := f.respond(f.from.ID, id, "first", map[string]any{"choice": "o0"}); response.Code != http.StatusBadGateway {
		t.Fatalf("a rejected delivery: %d %s", response.Code, response.Body)
	}
	if still := f.requests(t, f.from.ID); len(still) != 1 || still[0].ID != id {
		t.Fatalf("the ask was not released: %+v", still)
	}

	f.fake.sendErr = errors.New("connection reset after the write")
	if response := f.respond(f.from.ID, id, "second", map[string]any{"choice": "o0"}); response.Code != http.StatusBadGateway {
		t.Fatalf("a delivery of unknown outcome: %d %s", response.Code, response.Body)
	}
	f.fake.sendErr = nil
	if response := f.respond(f.from.ID, id, "third", map[string]any{"choice": "o0"}); response.Code != http.StatusConflict {
		t.Fatalf("an answer that may have arrived was given again: %d %s", response.Code, response.Body)
	}
}

func TestTheAskCommandEndpointServesOnlyThisMachine(t *testing.T) {
	f := newAskFixture(t)
	post := func(header string, body map[string]any) *httptest.ResponseRecorder {
		encoded, _ := json.Marshal(body)
		request := httptest.NewRequest(http.MethodPost, "/api/v1/asks", bytes.NewReader(encoded))
		request.Header.Set("Authorization", header)
		response := httptest.NewRecorder()
		f.handler.ServeHTTP(response, request)
		return response
	}
	raise := map[string]any{"op": "raise", "sessionId": f.to.ID, "title": "Ship?", "questions": []any{map[string]any{"question": "Ship?", "options": []any{map[string]any{"label": "Yes"}}}}}
	if response := post("Bearer "+f.token, raise); response.Code == http.StatusOK {
		t.Fatalf("a session credential raised an ask for another session: %s", response.Body)
	}
	raised := post("Bearer "+dashboardTestToken, raise)
	var view askView
	if err := json.Unmarshal(raised.Body.Bytes(), &view); err != nil || raised.Code != http.StatusOK || view.RoutedTo != f.from.ID {
		t.Fatalf("raise: %d %s", raised.Code, raised.Body)
	}
	if response := post("Bearer "+dashboardTestToken, map[string]any{"op": "answer", "sessionId": f.to.ID, "askId": view.ID, "note": "me"}); response.Code != http.StatusForbidden {
		t.Fatalf("the asker answered itself: %d %s", response.Code, response.Body)
	}
	if response := post("Bearer "+dashboardTestToken, map[string]any{"op": "answer", "sessionId": f.from.ID, "askId": view.ID, "note": "yes"}); response.Code != http.StatusOK {
		t.Fatalf("the hearer's answer: %d %s", response.Code, response.Body)
	}
	if len(f.fake.sent) != 1 || f.fake.sent[0] != "yes" {
		t.Fatalf("sent %q", f.fake.sent)
	}
}

func TestAnAskOnAPairedHostsSessionShowsAndListsHere(t *testing.T) {
	local, remote, record := pairedFederation(t)
	raised := remote.request(t, http.MethodPost, "/api/v1/asks", map[string]any{"op": "raise", "sessionId": "remote-a", "title": "Ready to ship",
		"questions": []any{map[string]any{"question": "Deploy it now?", "options": []any{map[string]any{"label": "Deploy"}, map[string]any{"label": "Wait"}}}}})
	if raised.Code != http.StatusOK {
		t.Fatalf("raise: %d %s", raised.Code, raised.Body)
	}
	remote.daemon.discoverCatalog(context.Background())
	qualified := hosts.Qualify(record.ID, "remote-a")

	testkit.Eventually(t, "the ask mirrored into this host's session list", 10*time.Second, func() bool {
		for _, row := range sessionsOf(local.snapshot(t), record.ID) {
			if row.ID == qualified {
				return row.Ask != nil && row.Ask.Title == "Ready to ship"
			}
		}
		return false
	})
	row := viewRow(t, local.daemon, qualified)
	if row.Ask == nil || row.Ask.RoutedTo != registry.AskToPerson || row.Attention == nil || row.Attention.Kind != AttentionAsk {
		t.Fatalf("session view row ask=%+v attention=%+v", row.Ask, row.Attention)
	}

	response := local.request(t, http.MethodGet, "/api/v1/requests?sessionId="+url.QueryEscape(qualified), nil)
	var body struct {
		Requests []openRequestView `json:"requests"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != http.StatusOK {
		t.Fatalf("requests status=%d body=%s", response.Code, response.Body.String())
	}
	if len(body.Requests) != 1 || body.Requests[0].Kind != surface.WaitingAsk || len(body.Requests[0].Questions) != 1 {
		t.Fatalf("requests through the host = %+v", body.Requests)
	}
}
