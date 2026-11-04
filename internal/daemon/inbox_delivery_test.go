package daemon

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

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
	status, payload := h.request(http.MethodGet, "/api/v1/queue?sessionId=target", nil)
	if status != http.StatusOK {
		t.Fatalf("inbox status=%d body=%s", status, payload)
	}
	var page mobileQueuePage
	if err := json.Unmarshal(payload, &page); err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		if item.SessionID == "target" && item.SourceSessionID == "sender" && item.Message == "build finished, two tests failing" && item.Historical {
			return
		}
	}
	t.Fatalf("the inbox of the session that received the message does not list it: %s", payload)
}
