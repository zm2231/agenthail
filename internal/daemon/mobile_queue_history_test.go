package daemon

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestMobileQueueRetainsActiveItemsAndBoundsTerminalHistory(t *testing.T) {
	d, registry, _, _, _ := daemonFixture(t)
	for _, sessionID := range []string{"history"} {
		if err := registry.RegisterSession(surface.Session{ID: sessionID, Surface: surface.KindCodex}); err != nil {
			t.Fatal(err)
		}
	}
	deadID := queueDeadForMobileTest(t, registry, "dead message")
	inflightID := queueInflightForMobileTest(t, registry, "inflight message")
	pendingID, err := registry.QueueMessageWithKey("to", "pending message", "active-pending")
	if err != nil {
		t.Fatal(err)
	}

	oldestDelivered := queueDeliveredForMobileTest(t, registry, "history", "oldest delivered")
	terminalIDs := map[int64]struct{}{}
	for i := 0; i < 98; i++ {
		id, err := registry.QueueMessageWithKey("history", "terminal message", "terminal-"+string(rune('a'+i)))
		if err != nil {
			t.Fatal(err)
		}
		if err := registry.CancelMessage(id); err != nil {
			t.Fatal(err)
		}
		terminalIDs[id] = struct{}{}
	}
	retainedDelivered := queueDeliveredForMobileTest(t, registry, "history", "retained delivered")
	terminalIDs[retainedDelivered] = struct{}{}
	expiredID, err := registry.QueueMessageWithKey("history", "expired message", "terminal-expired")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ClaimNextMessage("history", time.Now().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := registry.RetryMessage(pendingID); err != nil {
		t.Fatal(err)
	}
	terminalIDs[expiredID] = struct{}{}

	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/queue", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}

	var page struct {
		Items []dashboardQueue `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 103 {
		t.Fatalf("items=%d, want 103", len(page.Items))
	}

	seen := map[int64]dashboardQueue{}
	terminalSeen := 0
	expiredSeen := 0
	for _, item := range page.Items {
		if _, exists := seen[item.ID]; exists {
			t.Fatalf("queue item %d returned more than once", item.ID)
		}
		seen[item.ID] = item
		if item.ID == deadID && (item.Historical || item.Evidence != surface.EvidenceUnknown) {
			t.Fatalf("active unknown row=%+v", item)
		}
		if _, terminal := terminalIDs[item.ID]; terminal {
			terminalSeen++
			if item.Status == "expired" {
				expiredSeen++
			}
		}
	}
	if _, found := seen[oldestDelivered]; found {
		t.Fatalf("oldest terminal item %d was retained", oldestDelivered)
	}
	if terminalSeen != 100 || expiredSeen != 1 {
		t.Fatalf("terminal=%d expired=%d, want terminal=100 expired=1", terminalSeen, expiredSeen)
	}
	for _, id := range []int64{deadID, inflightID, pendingID} {
		if _, found := seen[id]; !found {
			t.Fatalf("active item %d was omitted", id)
		}
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
