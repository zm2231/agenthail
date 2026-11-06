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

func postActionAs(t *testing.T, handler http.Handler, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/actions", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func snapshotProblems(t *testing.T, handler http.Handler) []map[string]any {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/snapshot?fresh=1", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var snapshot struct {
		DeliveryProblems []map[string]any `json:"deliveryProblems"`
	}
	if response.Code != http.StatusOK {
		t.Fatalf("snapshot status=%d body=%s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot.DeliveryProblems
}

func TestDeleteRemovesAQueuedMessageThatNeverReachedItsSession(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	queueID, deliveryID, err := r.QueueDeliveryWithIntent("to", "never arrives", "", surface.SendOptions{SourceSessionID: surface.PersonSessionID})
	if err != nil {
		t.Fatal(err)
	}
	item, err := r.ClaimNextMessage("to", time.Now())
	if err != nil || item == nil || item.ID != queueID {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	if _, err := r.DeadLetterMessage(item.ID, errors.New("delivery rejected [target missing]: gone")); err != nil {
		t.Fatal(err)
	}
	handler := dashboardRouter(d)
	problems := snapshotProblems(t, handler)
	if len(problems) != 1 || problems[0]["deliveryId"] != float64(deliveryID) || problems[0]["deletable"] != true {
		t.Fatalf("problems=%v", problems)
	}
	before, err := r.CatalogEventsAfter(0, 1000)
	if err != nil {
		t.Fatal(err)
	}

	response := postActionAs(t, handler, "secret", fmt.Sprintf(`{"action":"delivery-delete","deliveryId":%d}`, deliveryID))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"deleted":true`) {
		t.Fatalf("delete status=%d body=%s", response.Code, response.Body.String())
	}
	if problems := snapshotProblems(t, handler); len(problems) != 0 {
		t.Fatalf("problems after delete=%v", problems)
	}
	row, err := r.QueueItem(queueID, d.adapters())
	if err != nil || row.Status != "canceled" {
		t.Fatalf("queue row=%+v err=%v", row, err)
	}
	after, err := r.CatalogEventsAfter(before.LatestSeq, 1000)
	if err != nil {
		t.Fatal(err)
	}
	dismissed := false
	for _, event := range after.Events {
		if event.Type == "delivery.dismissed" && event.EntityID == "to" && strings.Contains(string(event.Payload), fmt.Sprintf(`"deliveryId":%d`, deliveryID)) {
			dismissed = true
		}
	}
	if !dismissed {
		t.Fatalf("no delivery.dismissed event in %+v", after.Events)
	}

	repeat := postActionAs(t, handler, "secret", fmt.Sprintf(`{"action":"delivery-delete","deliveryId":%d}`, deliveryID))
	if repeat.Code != http.StatusOK || !strings.Contains(repeat.Body.String(), `"deleted":false`) {
		t.Fatalf("repeat status=%d body=%s", repeat.Code, repeat.Body.String())
	}
	if err := r.RetryMessage(queueID); err == nil {
		t.Fatal("a deleted message could be retried")
	}
}

func TestDeleteRefusesAMessageThatMayHaveReachedItsSession(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	handler := dashboardRouter(d)
	for _, from := range []registry.DeliveryIntentStatus{registry.DeliveryIntentSent, registry.DeliveryIntentUnknown} {
		intent, err := r.RecordDeliveryIntent(registry.DeliveryIntentInput{SenderSessionID: "from", TargetSessionID: "to", Message: "maybe arrived " + string(from), Status: from, Evidence: surface.EvidenceSubmitted})
		if err != nil {
			t.Fatal(err)
		}
		if changed, err := r.FailDeliveryIntent(intent.ID, "lost"); err != nil || !changed {
			t.Fatalf("%s: changed=%v err=%v", from, changed, err)
		}
		response := postActionAs(t, handler, "secret", fmt.Sprintf(`{"action":"delivery-delete","deliveryId":%d}`, intent.ID))
		assertAPIV1Error(t, response, http.StatusConflict, "conflict")
	}
	held, err := r.RecordDeliveryIntent(registry.DeliveryIntentInput{SenderSessionID: "from", TargetSessionID: "to", Message: "parked", Status: registry.DeliveryIntentSent, Evidence: surface.EvidenceTransportAccepted})
	if err != nil {
		t.Fatal(err)
	}
	peer := registry.PeerMessage{MsgID: held.UUID, TargetSessionID: "to", SenderSessionID: "from"}
	if changed, err := r.HoldPeerMessage(peer, "parked by inbound policy"); err != nil || !changed {
		t.Fatalf("hold changed=%v err=%v", changed, err)
	}
	if changed, err := r.DropPeerMessage(peer, "dropped"); err != nil || !changed {
		t.Fatalf("drop changed=%v err=%v", changed, err)
	}
	assertAPIV1Error(t, postActionAs(t, handler, "secret", fmt.Sprintf(`{"action":"delivery-delete","deliveryId":%d}`, held.ID)), http.StatusConflict, "conflict")
	pending, err := r.RecordDeliveryIntent(registry.DeliveryIntentInput{SenderSessionID: "from", TargetSessionID: "to", Message: "possibly delivered", Status: registry.DeliveryIntentSubmitted, Evidence: surface.EvidenceSubmitted})
	if err != nil {
		t.Fatal(err)
	}
	assertAPIV1Error(t, postActionAs(t, handler, "secret", fmt.Sprintf(`{"action":"delivery-delete","deliveryId":%d}`, pending.ID)), http.StatusConflict, "conflict")
	for _, problem := range snapshotProblems(t, handler) {
		if problem["deletable"] != nil {
			t.Fatalf("problem offered for delete: %v", problem)
		}
	}
	assertAPIV1Error(t, postActionAs(t, handler, "secret", `{"action":"delivery-delete","deliveryId":999999}`), http.StatusNotFound, "not_found")
	assertAPIV1Error(t, postActionAs(t, handler, "secret", `{"action":"delivery-delete"}`), http.StatusBadRequest, "invalid_request")
}

func TestAnAgentCannotDeleteAFailedMessage(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	intent, err := r.RecordDeliveryIntent(registry.DeliveryIntentInput{SenderSessionID: "from", TargetSessionID: "to", Message: "refused", Status: registry.DeliveryIntentSubmitted, Evidence: surface.EvidenceSubmitted})
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := r.FailDeliveryIntent(intent.ID, "refused"); err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	reply := d.performAction(t.Context(), sessionPrincipal("from"), actionFormatAPIv1, []byte(fmt.Sprintf(`{"action":"delivery-delete","deliveryId":%d}`, intent.ID)), "agent-delete")
	if reply.Status != http.StatusForbidden || !strings.Contains(string(reply.Body), `"forbidden"`) {
		t.Fatalf("status=%d body=%s", reply.Status, reply.Body)
	}
	if deletable, err := r.DeliveryDeletable(intent.ID); err != nil || !deletable {
		t.Fatalf("deletable=%v err=%v", deletable, err)
	}
}
