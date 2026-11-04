package daemon

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func (h *runningDaemon) inbox(sessionID string) []inboxItem {
	h.t.Helper()
	status, payload := h.request(http.MethodGet, "/api/v1/inbox?sessionId="+sessionID, nil)
	if status != http.StatusOK {
		h.t.Fatalf("inbox status=%d body=%s", status, payload)
	}
	var page inboxPage
	if err := json.Unmarshal(payload, &page); err != nil {
		h.t.Fatal(err)
	}
	return page.Items
}

func inboxMessage(items []inboxItem, message string) (inboxItem, bool) {
	for _, item := range items {
		if item.Message == message {
			return item, true
		}
	}
	return inboxItem{}, false
}

func TestInboxListsACrossSessionMessageDeliveredImmediately(t *testing.T) {
	agent := newScriptedAgent("sender", "target")
	h := startRunningDaemon(t, agent)
	receipt := h.receipt(map[string]any{"action": "send", "sessionId": "target", "sourceSessionId": "sender", "message": "build finished, two tests failing"})
	if receipt["evidence"] != string(surface.EvidenceDelivered) {
		t.Fatalf("receipt=%v", receipt)
	}
	settle()
	if got := agent.deliveredTo("target"); len(got) != 1 {
		t.Fatalf("delivered=%+v", got)
	}
	item, ok := inboxMessage(h.inbox("target"), "build finished, two tests failing")
	if !ok || item.SourceSessionID != "sender" || item.Pending || item.Evidence != surface.EvidenceDelivered {
		t.Fatalf("the inbox of the session that received the message does not list it as delivered: %+v ok=%v", item, ok)
	}
}

func TestInboxShowsAHeldMessageAsPendingUntilItIsDelivered(t *testing.T) {
	agent := newScriptedAgent("sender", "target")
	agent.setStatus("target", surface.StatusBusy)
	h := startRunningDaemon(t, agent)
	h.receipt(map[string]any{"action": "send", "sessionId": "target", "sourceSessionId": "sender", "model": "gpt-test", "message": "after this turn"})
	settle()
	if item, ok := inboxMessage(h.inbox("target"), "after this turn"); !ok || !item.Pending || item.QueueID == 0 || item.QueueStatus != "pending" {
		t.Fatalf("held message: %+v ok=%v", item, ok)
	}
	agent.setStatus("target", surface.StatusIdle)
	testkit.Eventually(t, "the held message delivered", 5*time.Second, func() bool {
		item, ok := inboxMessage(h.inbox("target"), "after this turn")
		return ok && !item.Pending && item.QueueStatus == "delivered"
	})
}

func TestInboxShowsACanceledMessageAsCanceled(t *testing.T) {
	agent := newScriptedAgent("sender", "target")
	agent.setStatus("target", surface.StatusBusy)
	h := startRunningDaemon(t, agent)
	id := queueID(t, h.receipt(map[string]any{"action": "send", "sessionId": "target", "sourceSessionId": "sender", "model": "gpt-test", "message": "never mind"}))
	h.action(map[string]any{"action": "queue-cancel", "queueId": id})
	item, ok := inboxMessage(h.inbox("target"), "never mind")
	if !ok || item.Pending || item.QueueStatus != "canceled" || item.Evidence != surface.EvidenceCanceled {
		t.Fatalf("canceled message: %+v ok=%v", item, ok)
	}
}
