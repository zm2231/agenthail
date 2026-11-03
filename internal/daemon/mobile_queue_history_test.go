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

func TestMobileQueueRetainsActiveItemsAndPagesTerminalHistory(t *testing.T) {
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
	terminal := map[int64]bool{}
	for i := 0; i < 60; i++ {
		terminal[queueDeliveredForMobileTest(t, registry, "history", "delivered "+strconv.Itoa(i))] = true
	}
	handler := dashboardRouter(d)

	first := getMobileQueue(t, handler, "")
	seen := map[int64]dashboardQueue{}
	terminalSeen := 0
	for _, item := range first.Items {
		if _, exists := seen[item.ID]; exists {
			t.Fatalf("queue item %d returned more than once", item.ID)
		}
		seen[item.ID] = item
		if terminal[item.ID] {
			terminalSeen++
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
	if terminalSeen != registryQueuePageLimit() || first.NextBefore == 0 {
		t.Fatalf("first page terminal=%d next=%d", terminalSeen, first.NextBefore)
	}

	older := getMobileQueue(t, handler, "?before="+strconv.FormatInt(first.NextBefore, 10))
	for _, item := range older.Items {
		if _, exists := seen[item.ID]; exists || !terminal[item.ID] {
			t.Fatalf("older page returned %d again or a non-terminal row", item.ID)
		}
		terminalSeen++
	}
	if terminalSeen != 60 || older.NextBefore != 0 {
		t.Fatalf("terminal across pages=%d next=%d", terminalSeen, older.NextBefore)
	}

	scoped := getMobileQueue(t, handler, "?sessionId=to")
	if len(scoped.Items) != 1 || scoped.Items[0].ID != pendingID || scoped.NextBefore != 0 {
		t.Fatalf("session page=%+v", scoped)
	}

	if qualified := getMobileQueue(t, handler, "?sessionId=hpeer::to"); len(qualified.Items) != 1 || qualified.Items[0].ID != pendingID {
		t.Fatalf("qualified session page=%+v", qualified)
	}

	single := getMobileQueue(t, handler, "?id="+strconv.FormatInt(pendingID, 10))
	if len(single.Items) != 1 || single.Items[0].ID != pendingID {
		t.Fatalf("item page=%+v", single)
	}
	if missing := getMobileQueue(t, handler, "?id=999999"); len(missing.Items) != 0 {
		t.Fatalf("missing item page=%+v", missing)
	}
	for _, query := range []string{"?before=x", "?limit=0", "?id=-1"} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/queue"+query, nil)
		request.Header.Set("Authorization", "Bearer secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d", query, response.Code)
		}
	}
}

func registryQueuePageLimit() int { return registry.QueueHistoryPageLimit }

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
