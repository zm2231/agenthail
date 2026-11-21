package daemon

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/testkit"
)

func TestAMessageHeldInSeveralTerminalsFollowsTheTerminalsThatClose(t *testing.T) {
	s, h, _ := holdInTerminals(t, 3)
	h.execStore(`UPDATE message_queue SET available_at_ms=? WHERE id=?`, time.Now().Add(time.Hour).UnixMilli(), s.queued)

	s.holders[2].Exit()
	again := s.awaitAskWithin(t, 30*time.Second, s.holders[:2]...)
	if again.Title != "Message held: open in 2 terminals" {
		t.Fatalf("ask=%+v, want it to name the two terminals left", again)
	}

	s.holders[1].Exit()
	h.awaitQueueItem(s.queued, "the message delivered to the one terminal left", func(item dashboardQueue) bool {
		return item.Historical && item.Status == "delivered"
	})
	if got := s.received(t); !slices.Equal(got, []int{1, 0, 0}) || !strings.Contains(s.holders[0].Received()[0], "ship the notes") {
		t.Fatalf("received=%v, want only the terminal left", got)
	}
	testkit.Eventually(t, "the ask withdrawn", 30*time.Second, func() bool {
		for _, ask := range openDeliveryAsks(t, s.store) {
			if strings.HasPrefix(ask.Ref, fmt.Sprintf("queue:%d:", s.queued)) {
				return false
			}
		}
		return true
	})
}
