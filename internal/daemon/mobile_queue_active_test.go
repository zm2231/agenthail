package daemon

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func getMobileQueue(t *testing.T, handler http.Handler, query string) mobileQueuePage {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/queue"+query, nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("%s status=%d body=%s", query, response.Code, response.Body.String())
	}
	var page mobileQueuePage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	return page
}

func TestMobileQueueListsOnlyActiveItems(t *testing.T) {
	d, registry, _, _, _ := daemonFixture(t)
	if err := registry.RegisterSession(surface.Session{ID: "history", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop}); err != nil {
		t.Fatal(err)
	}
	deadID := queueDeadForMobileTest(t, registry, "dead message")
	inflightID := queueInflightForMobileTest(t, registry, "inflight message")
	pendingID, err := registry.QueueMessageWithKey("to", "pending message", "active-pending")
	if err != nil {
		t.Fatal(err)
	}
	delivered := queueDeliveredForMobileTest(t, registry, "history", "delivered")
	handler := dashboardRouter(d)

	first := getMobileQueue(t, handler, "")
	seen := map[int64]dashboardQueue{}
	for _, item := range first.Items {
		if _, exists := seen[item.ID]; exists {
			t.Fatalf("queue item %d returned more than once", item.ID)
		}
		seen[item.ID] = item
		if item.ID == delivered {
			t.Fatalf("delivered row %d is listed as active", item.ID)
		}
		if item.ID == deadID && (item.Historical || item.Evidence != surface.EvidenceUnknown) {
			t.Fatalf("active unknown row=%+v", item)
		}
	}
	for _, id := range []int64{deadID, inflightID, pendingID} {
		if _, found := seen[id]; !found {
			t.Fatalf("active item %d was omitted", id)
		}
	}

	scoped := getMobileQueue(t, handler, "?sessionId=to")
	if len(scoped.Items) != 1 || scoped.Items[0].ID != pendingID {
		t.Fatalf("session page=%+v", scoped)
	}

	unknownHost := httptest.NewRequest(http.MethodGet, "/api/v1/queue?sessionId=hpeer::to", nil)
	unknownHost.Header.Set("Authorization", "Bearer secret")
	refused := httptest.NewRecorder()
	handler.ServeHTTP(refused, unknownHost)
	if refused.Code != http.StatusNotFound {
		t.Fatalf("a session on an unknown host status=%d", refused.Code)
	}

	single := getMobileQueue(t, handler, "?id="+strconv.FormatInt(pendingID, 10))
	if len(single.Items) != 1 || single.Items[0].ID != pendingID {
		t.Fatalf("item page=%+v", single)
	}
	if finished := getMobileQueue(t, handler, "?id="+strconv.FormatInt(delivered, 10)); len(finished.Items) != 1 || finished.Items[0].ID != delivered {
		t.Fatalf("finished item page=%+v", finished)
	}
	if missing := getMobileQueue(t, handler, "?id=999999"); len(missing.Items) != 0 {
		t.Fatalf("missing item page=%+v", missing)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/queue?id=-1", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("id=-1 status=%d", response.Code)
	}
}

func queueDeliveredForMobileTest(t *testing.T, registry *registry.Registry, sessionID, message string) int64 {
	t.Helper()
	if err := registry.QueueMessage(sessionID, message); err != nil {
		t.Fatal(err)
	}
	item, err := registry.ClaimNextMessage(sessionID, time.Now())
	if err != nil || item == nil {
		t.Fatalf("claim delivered item=%+v err=%v", item, err)
	}
	if err := registry.AckMessage(item.ID); err != nil {
		t.Fatal(err)
	}
	return item.ID
}

func queueDeadForMobileTest(t *testing.T, registry *registry.Registry, message string) int64 {
	t.Helper()
	id, err := registry.QueueMessageWithKey("from", message, "active-dead")
	if err != nil {
		t.Fatal(err)
	}
	item, err := registry.ClaimNextMessage("from", time.Now())
	if err != nil || item == nil {
		t.Fatalf("claim dead item=%+v err=%v", item, err)
	}
	if err := registry.DeadLetterUnknown(item.ID, errors.New("connection closed")); err != nil {
		t.Fatal(err)
	}
	return id
}

func queueInflightForMobileTest(t *testing.T, registry *registry.Registry, message string) int64 {
	t.Helper()
	id, err := registry.QueueMessageWithKey("from", message, "active-inflight")
	if err != nil {
		t.Fatal(err)
	}
	item, err := registry.ClaimNextMessage("from", time.Now())
	if err != nil || item == nil {
		t.Fatalf("claim inflight item=%+v err=%v", item, err)
	}
	return id
}
