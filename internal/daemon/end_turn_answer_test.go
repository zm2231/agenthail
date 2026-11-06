package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

var twoAsks = map[string]any{"headline": "Ready to ship", "needs": "you", "asks": []any{
	map[string]any{"question": "Deploy it now?", "options": []any{map[string]any{"label": "Deploy"}, map[string]any{"label": "Wait"}}, "suggested": 0},
	map[string]any{"question": "Which region first?", "options": []any{map[string]any{"label": "us"}, map[string]any{"label": "eu"}}, "links": []any{map[string]any{"title": "Runbook", "url": "https://example.com/runbook"}}},
}}

type answerFixture struct {
	endTurnFixture
	handler http.Handler
}

func newAnswerFixture(t *testing.T, end map[string]any) answerFixture {
	t.Helper()
	f := answerFixture{endTurnFixture: newEndTurnFixture(t)}
	f.handler = f.d.dashboardHandler(&dashboardServer{token: dashboardTestToken})
	f.turn(t, f.from, "turn-1", func() {
		if _, refused := callMCP(t, f.person, "agenthail_end_turn", end); refused {
			t.Fatal("end turn refused")
		}
	})
	return f
}

func (f answerFixture) requests(t *testing.T) (int, []openRequestView) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/requests?sessionId="+f.from.ID, nil)
	request.Header.Set("Authorization", "Bearer "+dashboardTestToken)
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	var body struct {
		Requests []openRequestView `json:"requests"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	return response.Code, body.Requests
}

func (f answerFixture) respond(requestID, key string, decision map[string]any) *httptest.ResponseRecorder {
	encoded, _ := json.Marshal(map[string]any{"action": "respond", "sessionId": f.from.ID, "requestId": requestID, "decision": decision})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/actions", bytes.NewReader(encoded))
	request.Header.Set("Authorization", "Bearer "+dashboardTestToken)
	request.Header.Set("Idempotency-Key", key)
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	return response
}

func (f answerFixture) ask(t *testing.T) openRequestView {
	t.Helper()
	code, requests := f.requests(t)
	if code != http.StatusOK || len(requests) != 1 || requests[0].Kind != surface.WaitingAsk {
		t.Fatalf("requests code=%d %+v", code, requests)
	}
	return requests[0]
}

func TestOneReplyAnswersEveryAskOfASessionWithNoLiveRequestsOnce(t *testing.T) {
	f := newAnswerFixture(t, twoAsks)
	ask := f.ask(t)
	if !strings.HasPrefix(ask.ID, "turn-end-") || ask.Prompt != "Ready to ship" || len(ask.Questions) != 2 || ask.Questions[0].Suggested == nil || *ask.Questions[0].Suggested != 0 || len(ask.Questions[1].Links) != 1 || ask.Actions != nil {
		t.Fatalf("ask %+v", ask)
	}
	if row := viewRow(t, f.d, f.from.ID); row.EndTurn == nil || row.EndTurn.RequestID != ask.ID {
		t.Fatalf("the row does not name its ask: %+v", row.EndTurn)
	}

	decision := map[string]any{"behavior": "allow", "answers": map[string]any{"Deploy it now?": "Deploy", "Which region first?": "eu, then us"}, "message": "Ship after lunch."}
	first := f.respond(ask.ID, "answer-1", decision)
	if first.Code != http.StatusOK {
		t.Fatalf("answer: %d %s", first.Code, first.Body)
	}
	want := "1. Deploy it now? Deploy\n2. Which region first? eu, then us\n\nShip after lunch."
	if len(f.fake.sent) != 1 || f.fake.sent[0] != want {
		t.Fatalf("sent %q, want one message %q", f.fake.sent, want)
	}
	if retry := f.respond(ask.ID, "answer-1", decision); retry.Code != http.StatusOK || retry.Body.String() != first.Body.String() {
		t.Fatalf("a retry with the same key: %d %s", retry.Code, retry.Body)
	}
	if again := f.respond(ask.ID, "answer-2", decision); again.Code != http.StatusConflict || !strings.Contains(again.Body.String(), "stale") {
		t.Fatalf("a second answer: %d %s", again.Code, again.Body)
	}
	if len(f.fake.sent) != 1 {
		t.Fatalf("the answer was sent %d times", len(f.fake.sent))
	}
	row := viewRow(t, f.d, f.from.ID)
	if row.Attention != nil || presentationNeedsYou(&row) || row.EndTurn == nil || row.EndTurn.AnsweredAt.IsZero() {
		t.Fatalf("after the answer: attention=%+v endTurn=%+v", row.Attention, row.EndTurn)
	}
}

func TestTwoDevicesAnsweringAtOnceSendOneAnswer(t *testing.T) {
	f := newAnswerFixture(t, twoAsks)
	ask := f.ask(t)
	codes := make([]int, 2)
	var wait sync.WaitGroup
	for i := range codes {
		wait.Add(1)
		go func() {
			defer wait.Done()
			codes[i] = f.respond(ask.ID, "device-"+string(rune('a'+i)), map[string]any{"message": "go ahead"}).Code
		}()
	}
	wait.Wait()
	if !(codes[0] == http.StatusOK && codes[1] == http.StatusConflict || codes[0] == http.StatusConflict && codes[1] == http.StatusOK) || len(f.fake.sent) != 1 {
		t.Fatalf("codes=%v sent=%q", codes, f.fake.sent)
	}
}

func TestANotificationButtonAnswersASingleAsk(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var mu sync.Mutex
	var pushed []RequestPush
	f := answerFixture{endTurnFixture: newEndTurnFixture(t)}
	f.d.notifyDesktop = func(_, message, _, _, requestID string, actions []surface.RequestAction) error {
		mu.Lock()
		defer mu.Unlock()
		pushed = append(pushed, RequestPush{message, requestID, actions})
		return nil
	}
	f.handler = f.d.dashboardHandler(&dashboardServer{token: dashboardTestToken})
	f.turn(t, f.from, "turn-1", func() {
		if _, refused := callMCP(t, f.person, "agenthail_end_turn", deployAsk); refused {
			t.Fatal("end turn refused")
		}
	})
	ask := f.ask(t)
	testkit.Eventually(t, "the needs-you push", 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(pushed) == 1
	})
	if push := pushed[0]; push.requestID != ask.ID || len(push.actions) != 3 || push.actions[1].ID != "o1" || push.actions[2].ID != "reply" || !strings.HasSuffix(push.message, "needs you: Deploy the fix?") {
		t.Fatalf("push %+v, ask %s", push, ask.ID)
	}
	if response := f.respond(ask.ID, "button", map[string]any{"choice": "o1"}); response.Code != http.StatusOK {
		t.Fatalf("button: %d %s", response.Code, response.Body)
	}
	if len(f.fake.sent) != 1 || f.fake.sent[0] != "1. Deploy it now? Wait" {
		t.Fatalf("sent %q", f.fake.sent)
	}
}

type RequestPush struct {
	message, requestID string
	actions            []surface.RequestAction
}

func TestAnAnswerThatProvablyFailedCanBeGivenAgainAndOneThatMayHaveArrivedCannot(t *testing.T) {
	f := newAnswerFixture(t, deployAsk)
	ask := f.ask(t)
	f.fake.sendErr = surface.DeliveryTerminal(errors.New("the session is gone"), surface.DeliveryTargetMissing)
	if response := f.respond(ask.ID, "first", map[string]any{"choice": "o0"}); response.Code != http.StatusBadGateway {
		t.Fatalf("a rejected delivery: %d %s", response.Code, response.Body)
	}
	if still := f.ask(t); still.ID != ask.ID {
		t.Fatalf("the ask was not released: %+v", still)
	}

	f.fake.sendErr = errors.New("connection reset after the write")
	if response := f.respond(ask.ID, "second", map[string]any{"choice": "o0"}); response.Code != http.StatusBadGateway {
		t.Fatalf("a delivery of unknown outcome: %d %s", response.Code, response.Body)
	}
	f.fake.sendErr = nil
	if response := f.respond(ask.ID, "third", map[string]any{"choice": "o0"}); response.Code != http.StatusConflict {
		t.Fatalf("an answer that may have arrived was given again: %d %s", response.Code, response.Body)
	}
}
