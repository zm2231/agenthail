package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func openDeliveryAsks(t *testing.T, r *registry.Registry) []registry.Ask {
	t.Helper()
	open, err := r.OpenAsks()
	if err != nil {
		t.Fatal(err)
	}
	var asks []registry.Ask
	for _, ask := range open {
		if ask.Origin == registry.AskOriginDelivery {
			asks = append(asks, ask)
		}
	}
	return asks
}

func deliveryAskByRef(t *testing.T, r *registry.Registry, ref string) registry.Ask {
	t.Helper()
	for _, ask := range openDeliveryAsks(t, r) {
		if ask.Ref == ref {
			return ask
		}
	}
	t.Fatalf("no open delivery ask %s in %+v", ref, openDeliveryAsks(t, r))
	return registry.Ask{}
}

func postActionAs(t *testing.T, handler http.Handler, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/actions", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func answerBody(ask registry.Ask, choice string) string {
	return fmt.Sprintf(`{"action":"respond","sessionId":%q,"requestId":%q,"decision":{"choice":%q}}`, ask.SessionID, ask.ID, choice)
}

func optionIDs(ask registry.Ask) []string {
	var ids []string
	for _, option := range ask.Questions[0].Options {
		ids = append(ids, option.ID)
	}
	return ids
}

func noticesTo(t *testing.T, r *registry.Registry, sessionID, text string) int {
	t.Helper()
	rows, err := r.ListQueue(false, nil)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, row := range rows {
		if row.SessionID == sessionID && strings.Contains(row.Message, text) {
			count++
		}
	}
	return count
}

func failedIntent(t *testing.T, r *registry.Registry, from string, status registry.DeliveryIntentStatus, message string) *registry.DeliveryIntent {
	t.Helper()
	intent, err := r.RecordDeliveryIntent(registry.DeliveryIntentInput{SenderSessionID: from, TargetSessionID: "to", Message: message, Status: status, Evidence: surface.EvidenceSubmitted})
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := r.RecordDeliveryFailure(intent.ID, "target rejected input"); err != nil || !changed {
		t.Fatalf("fail changed=%v err=%v", changed, err)
	}
	return intent
}

func TestTheSenderIsAskedFirstAndOnlyItMayAnswerForItsMessage(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	intent := failedIntent(t, r, "from", registry.DeliveryIntentSubmitted, "build the release")
	d.syncDeliveryAsks()
	d.syncDeliveryAsks()
	asks := openDeliveryAsks(t, r)
	if len(asks) != 1 || asks[0].SessionID != "from" || asks[0].RoutedTo != "from" || asks[0].Ref != fmt.Sprintf("intent:%d:0:failed", intent.ID) {
		t.Fatalf("asks=%+v, want one ask routed to the sender", asks)
	}
	if got := noticesTo(t, r, "from", "Your message: Message not delivered"); got != 1 {
		t.Fatalf("sender notices=%d, want one however often the asks sync", got)
	}
	ask := asks[0]
	other := d.performAction(t.Context(), sessionPrincipal("to"), actionFormatAPIv1, []byte(answerBody(ask, "dismiss")), "other-agent")
	if other.Status != http.StatusForbidden {
		t.Fatalf("another agent answered: status=%d body=%s", other.Status, other.Body)
	}
	own := d.performAction(t.Context(), sessionPrincipal("from"), actionFormatAPIv1, []byte(answerBody(ask, "delete")), "sender")
	if own.Status != http.StatusOK || !strings.Contains(string(own.Body), `"choice":"delete"`) {
		t.Fatalf("sender delete: status=%d body=%s", own.Status, own.Body)
	}
	if asks := openDeliveryAsks(t, r); len(asks) != 0 {
		t.Fatalf("asks=%+v after the sender answered", asks)
	}
}

func TestAnUnansweredSenderAskMovesToThePerson(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	failedIntent(t, r, "from", registry.DeliveryIntentSubmitted, "build the release")
	d.syncDeliveryAsks()
	asks := openDeliveryAsks(t, r)
	if len(asks) != 1 || asks[0].RoutedTo != "from" {
		t.Fatalf("asks=%+v", asks)
	}
	d.escalateAsk(asks[0], "")
	moved, found, err := r.Ask(asks[0].ID)
	if err != nil || !found || moved.RoutedTo != registry.AskToPerson || moved.State != registry.AskOpen {
		t.Fatalf("ask=%+v found=%v err=%v, want it open with the person", moved, found, err)
	}
}

func TestDeleteRemovesAQueuedMessageThatNeverReachedItsSession(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	queueID, _, err := r.QueueDeliveryWithIntent("to", "never arrives", "", surface.SendOptions{SourceSessionID: surface.PersonSessionID})
	if err != nil {
		t.Fatal(err)
	}
	item, err := r.ClaimNextMessage("to", time.Now())
	if err != nil || item == nil || item.ID != queueID {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	if err := r.DeadLetterMessage(item.ID, errors.New("delivery rejected [target missing]: gone")); err != nil {
		t.Fatal(err)
	}
	asks := openDeliveryAsks(t, r)
	if len(asks) != 1 || asks[0].SessionID != "to" || asks[0].RoutedTo != registry.AskToPerson || strings.Join(optionIDs(asks[0]), ",") != "retry,delete,dismiss" {
		t.Fatalf("asks=%+v, want the person offered retry, delete or dismiss", asks)
	}
	handler := dashboardRouter(d)
	if response := postActionAs(t, handler, "secret", answerBody(asks[0], "delete")); response.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", response.Code, response.Body.String())
	}
	if asks := openDeliveryAsks(t, r); len(asks) != 0 {
		t.Fatalf("asks after delete=%+v", asks)
	}
	row, err := r.QueueItem(queueID, d.adapters())
	if err != nil || row.Status != "canceled" {
		t.Fatalf("queue row=%+v err=%v", row, err)
	}
	if response := postActionAs(t, handler, "secret", answerBody(asks[0], "delete")); response.Code != http.StatusConflict {
		t.Fatalf("repeat status=%d body=%s", response.Code, response.Body.String())
	}
	if err := r.RetryMessage(queueID); err == nil {
		t.Fatal("a deleted message could be retried")
	}
}

func TestAMessageThatMayHaveReachedItsSessionIsNeverOfferedDelete(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	if err := r.EnsureSender(surface.PersonSessionID); err != nil {
		t.Fatal(err)
	}
	for _, from := range []registry.DeliveryIntentStatus{registry.DeliveryIntentSent, registry.DeliveryIntentUnknown} {
		failedIntent(t, r, surface.PersonSessionID, from, "maybe arrived "+string(from))
	}
	handler := dashboardRouter(d)
	asks := openDeliveryAsks(t, r)
	if len(asks) != 2 {
		t.Fatalf("asks=%+v", asks)
	}
	for _, ask := range asks {
		if strings.Join(optionIDs(ask), ",") != "dismiss" {
			t.Fatalf("ask %s offers %v, want only dismiss", ask.Ref, optionIDs(ask))
		}
		assertAPIV1Error(t, postActionAs(t, handler, "secret", answerBody(ask, "delete")), http.StatusBadRequest, "invalid_request")
	}
}

func TestRetryRequeuesAMessageWhoseOutcomeIsUnknown(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	if err := r.QueueMessage("to", "maybe lost"); err != nil {
		t.Fatal(err)
	}
	item, err := r.ClaimNextMessage("to", time.Now())
	if err != nil || item == nil {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	if err := r.DeadLetterUnknown(item.ID, errors.New("connection closed")); err != nil {
		t.Fatal(err)
	}
	handler := dashboardRouter(d)
	assertAPIV1Error(t, postActionAs(t, handler, "secret", fmt.Sprintf(`{"action":"queue-cancel","queueId":%d}`, item.ID)), http.StatusConflict, "conflict")
	asks := openDeliveryAsks(t, r)
	if len(asks) != 1 || asks[0].Ref != fmt.Sprintf("queue:%d:dead", item.ID) || strings.Join(optionIDs(asks[0]), ",") != "retry,dismiss" {
		t.Fatalf("asks=%+v", asks)
	}
	if response := postActionAs(t, handler, "secret", answerBody(asks[0], "retry")); response.Code != http.StatusOK {
		t.Fatalf("retry status=%d body=%s", response.Code, response.Body.String())
	}
	row, err := r.QueueItem(item.ID, d.adapters())
	if err != nil || row.Status != "pending" {
		t.Fatalf("queue row=%+v err=%v", row, err)
	}
	if asks := openDeliveryAsks(t, r); len(asks) != 0 {
		t.Fatalf("asks after retry=%+v", asks)
	}
}

func TestARejectedSendRepliesWithItsAsk(t *testing.T) {
	d, r, fake, _, _ := daemonFixture(t)
	fake.sendErr = surface.DeliveryTerminal(errors.New("target rejected input"), surface.DeliveryInvalidRequest)
	response := postActionAs(t, dashboardRouter(d), "secret", `{"action":"send","sessionId":"to","message":"hello"}`)
	var reply struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		AskID      string `json:"askId"`
		DeliveryID int64  `json:"deliveryId"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &reply); err != nil || response.Code < http.StatusBadRequest {
		t.Fatalf("status=%d body=%s err=%v", response.Code, response.Body.String(), err)
	}
	asks := openDeliveryAsks(t, r)
	if len(asks) != 1 || reply.AskID != asks[0].ID || reply.DeliveryID != 0 {
		t.Fatalf("reply=%+v asks=%+v, want the reply to name the failed message's ask", reply, asks)
	}
}
