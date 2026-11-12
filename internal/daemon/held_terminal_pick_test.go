package daemon

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
	"github.com/zm2231/agenthail/internal/testkit"
)

const heldConversation = "conversation-held"

type heldInTerminals struct {
	fixture *claudetest.Fixture
	store   *registry.Registry
	cwd     string
	holders []*claudetest.Holder
	queued  int64
	closing atomic.Pointer[claudetest.Holder]
}

// closingOnSend closes a picked terminal as the daemon sends to it, after the
// daemon checked that it still holds the conversation.
type closingOnSend struct {
	nativeSenderClaude
	held *heldInTerminals
}

func (c closingOnSend) Send(ctx context.Context, session *surface.Session, message string) (*surface.SendResult, error) {
	if closing := c.held.closing.Load(); closing != nil && session.HolderPID == closing.PID {
		closing.Exit()
	}
	return c.nativeSenderClaude.Send(ctx, session, message)
}

func (s *heldInTerminals) claude() surface.Surface {
	return closingOnSend{nativeSenderClaude: nativeSenderClaude{Claude: surfaces.NewClaude(nil, s.fixture.Home), source: "sender-native"}, held: s}
}

func (s *heldInTerminals) run(t *testing.T) (*runningDaemon, func()) {
	t.Helper()
	return runDaemon(t, s.store, s.claude())
}

// holdInTerminals queues a message from an agent session to a conversation
// open in count terminals, and waits for it to be held with an ask naming
// every terminal.
func holdInTerminals(t *testing.T, count int) (*heldInTerminals, *runningDaemon, func()) {
	t.Helper()
	fixture := claudetest.New(t)
	s := &heldInTerminals{fixture: fixture, cwd: filepath.Join(t.TempDir(), "release-notes")}
	fixture.Transcript(heldConversation, s.cwd)
	for range count {
		s.holders = append(s.holders, fixture.Holder(heldConversation, s.cwd))
	}
	recordNativeSender(t, fixture.Root, "sender-native")
	store, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	s.store = store
	h, stop := s.run(t)
	testkit.Eventually(t, "the conversation to list every terminal", 5*time.Second, func() bool {
		return len(snapshotHolders(t, h, heldConversation)) == count
	})
	s.queued, err = store.QueueMessageWithOptions(heldConversation, "ship the notes", "", surface.SendOptions{SourceSessionID: "sender-native", BusyDelivery: "queue"})
	if err != nil {
		t.Fatal(err)
	}
	s.awaitAsk(t, s.holders...)
	return s, h, stop
}

// awaitAsk waits for the one open ask about the held message and checks it
// offers exactly the given terminals, then Cancel and Dismiss.
func (s *heldInTerminals) awaitAsk(t *testing.T, holders ...*claudetest.Holder) registry.Ask {
	t.Helper()
	want := []string{}
	for _, holder := range holders {
		want = append(want, registry.TerminalOption(holder.PID))
	}
	want = append(want, registry.DeliveryCancel, registry.DeliveryDismiss)
	var found []registry.Ask
	testkit.Eventually(t, "an ask to pick one of the terminals", 5*time.Second, func() bool {
		found = nil
		for _, ask := range openDeliveryAsks(t, s.store) {
			if strings.HasPrefix(ask.Ref, fmt.Sprintf("queue:%d:shared:", s.queued)) {
				found = append(found, ask)
			}
		}
		return len(found) == 1 && slices.Equal(optionIDs(found[0]), want)
	})
	return found[0]
}

func (s *heldInTerminals) received(t *testing.T) []int {
	t.Helper()
	counts := make([]int, len(s.holders))
	for i, holder := range s.holders {
		counts[i] = len(holder.Received())
	}
	return counts
}

func (s *heldInTerminals) answerStopped(t *testing.T, h *runningDaemon, ask registry.Ask, choice string) {
	t.Helper()
	reply := h.daemon.performAction(t.Context(), principal{ID: localDashboardPrincipal, Kind: principalPerson}, actionFormatAPIv1, []byte(answerBody(ask, choice)), "")
	if reply.Status != http.StatusOK {
		t.Fatalf("answer %s: status=%d body=%s", choice, reply.Status, reply.Body)
	}
}

func (s *heldInTerminals) queueRow(t *testing.T) *registry.QueueRow {
	t.Helper()
	row, err := s.store.QueueItem(s.queued, nil)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func TestAMessageHeldInTwoTerminalsAsksWhichOneAndStaysHeldUntilAnswered(t *testing.T) {
	s, h, _ := holdInTerminals(t, 2)
	ask := s.awaitAsk(t, s.holders...)
	if ask.SessionID != "sender-native" || ask.Title != "Message held: open in 2 terminals" {
		t.Fatalf("ask=%+v, want the sender asked first", ask)
	}
	question := ask.Questions[0]
	if question.Recommended != registry.DeliveryDismiss {
		t.Fatalf("recommended=%q, want no terminal picked for the person", question.Recommended)
	}
	for i, holder := range s.holders {
		label := question.Options[i].Label
		if !strings.Contains(label, "release-notes") || !strings.HasSuffix(label, fmt.Sprintf("pid %d", holder.PID)) {
			t.Fatalf("label %q does not name the folder and end with pid %d", label, holder.PID)
		}
	}

	for range 3 {
		h.execStore(`UPDATE message_queue SET available_at_ms=0 WHERE id=?`, s.queued)
		h.awaitQueueItem(s.queued, "the message tried again and held", func(item dashboardQueue) bool {
			return item.Attempts == 0 && item.Status == "pending" && strings.Contains(item.LastError, "open in 2 terminals")
		})
	}
	if got := s.received(t); !slices.Equal(got, []int{0, 0}) {
		t.Fatalf("an unanswered held message reached a terminal: %v", got)
	}
	if again := s.awaitAsk(t, s.holders...); again.ID != ask.ID {
		t.Fatalf("retrying a held message raised ask %s after %s", again.ID, ask.ID)
	}
}

func TestATerminalPickedForAHeldMessageSurvivesADaemonRestart(t *testing.T) {
	s, h, stop := holdInTerminals(t, 2)
	ask := s.awaitAsk(t, s.holders...)
	stop()
	picked := s.holders[1]
	s.answerStopped(t, h, ask, registry.TerminalOption(picked.PID))
	if got := s.received(t); !slices.Equal(got, []int{0, 0}) {
		t.Fatalf("a stopped daemon delivered: %v", got)
	}

	restarted, _ := s.run(t)
	restarted.awaitQueueItem(s.queued, "the held message delivered to the picked terminal", func(item dashboardQueue) bool {
		return item.Historical && item.Status == "delivered"
	})
	if got := s.received(t); !slices.Equal(got, []int{0, 1}) || !strings.Contains(picked.Received()[0], "ship the notes") {
		t.Fatalf("received=%v picked=%q, want only the picked terminal", got, picked.Received())
	}
	if open := openDeliveryAsks(t, s.store); len(open) != 0 {
		t.Fatalf("asks %+v left open after the pick", open)
	}
}

func TestATerminalPickNeverReachesALaterProcessThatReusedItsPid(t *testing.T) {
	s, h, stop := holdInTerminals(t, 2)
	ask := s.awaitAsk(t, s.holders...)
	stop()
	s.answerStopped(t, h, ask, registry.TerminalOption(s.holders[1].PID))
	h.execStore(`UPDATE message_queue SET holder_start='an earlier process' WHERE id=?`, s.queued)

	restarted, _ := s.run(t)
	restarted.awaitQueueItem(s.queued, "the message held again", func(item dashboardQueue) bool {
		return item.Status == "pending" && strings.Contains(item.LastError, "open in 2 terminals")
	})
	again := s.awaitAsk(t, s.holders...)
	if again.ID == ask.ID {
		t.Fatalf("the answered ask %s was reused", ask.ID)
	}
	if got := s.received(t); !slices.Equal(got, []int{0, 0}) {
		t.Fatalf("a pick reached the process now under its pid: %v", got)
	}
	if row := s.queueRow(t); row.Status != "pending" {
		t.Fatalf("row=%+v", row)
	}
}

func TestAPickedTerminalThatClosedHoldsTheMessageAgainAmongTheOthers(t *testing.T) {
	s, h, _ := holdInTerminals(t, 3)
	ask := s.awaitAsk(t, s.holders...)
	h.execStore(`UPDATE message_queue SET available_at_ms=? WHERE id=?`, time.Now().Add(time.Hour).UnixMilli(), s.queued)
	gone := s.holders[0]
	gone.Exit()
	status, payload := h.request(http.MethodPost, "/api/v1/actions", map[string]any{"action": "respond", "sessionId": ask.SessionID, "requestId": ask.ID, "decision": map[string]any{"choices": []string{registry.TerminalOption(gone.PID)}}})
	if status != http.StatusOK {
		t.Fatalf("pick status=%d body=%s", status, payload)
	}

	h.awaitQueueItem(s.queued, "the message held among the terminals left", func(item dashboardQueue) bool {
		return item.Status == "pending" && strings.Contains(item.LastError, "open in 2 terminals")
	})
	again := s.awaitAsk(t, s.holders[1:]...)
	if again.ID == ask.ID || again.Title != "Message held: open in 2 terminals" {
		t.Fatalf("ask=%+v, want a new ask naming the two terminals left", again)
	}
	if got := s.received(t); !slices.Equal(got, []int{0, 0, 0}) {
		t.Fatalf("a message whose terminal closed reached another: %v", got)
	}
}

func TestATerminalThatClosesAsThePickedMessageIsSentHoldsItAgainAmongTheOthers(t *testing.T) {
	s, h, _ := holdInTerminals(t, 3)
	ask := s.awaitAsk(t, s.holders...)
	s.closing.Store(s.holders[0])
	status, payload := h.request(http.MethodPost, "/api/v1/actions", map[string]any{"action": "respond", "sessionId": ask.SessionID, "requestId": ask.ID, "decision": map[string]any{"choices": []string{registry.TerminalOption(s.holders[0].PID)}}})
	if status != http.StatusOK {
		t.Fatalf("pick status=%d body=%s", status, payload)
	}

	again := s.awaitAsk(t, s.holders[1:]...)
	if again.ID == ask.ID || again.Title != "Message held: open in 2 terminals" {
		t.Fatalf("ask=%+v, want a new ask naming the two terminals left", again)
	}
	if row := s.queueRow(t); row.Status != "pending" {
		t.Fatalf("row status=%q, want it held", row.Status)
	}
	if got := s.received(t); !slices.Equal(got, []int{0, 0, 0}) {
		t.Fatalf("a message whose terminal closed reached another: %v", got)
	}
}

func TestCancellingAHeldMessageDropsItAndTellsTheSender(t *testing.T) {
	s, h, _ := holdInTerminals(t, 2)
	ask := s.awaitAsk(t, s.holders...)
	status, payload := h.request(http.MethodPost, "/api/v1/actions", map[string]any{"action": "respond", "sessionId": ask.SessionID, "requestId": ask.ID, "decision": map[string]any{"choices": []string{registry.DeliveryCancel}}})
	if status != http.StatusOK {
		t.Fatalf("cancel status=%d body=%s", status, payload)
	}
	if row := s.queueRow(t); row.Status != "canceled" {
		t.Fatalf("row status=%q, want canceled", row.Status)
	}
	testkit.Eventually(t, "the sender told its message was canceled", 5*time.Second, func() bool {
		rows, err := s.store.ListQueue(true, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.SessionID == "sender-native" && strings.Contains(row.Message, "was canceled before it was delivered") && strings.Contains(row.Message, "ship the notes") {
				return true
			}
		}
		return false
	})
	if got := s.received(t); !slices.Equal(got, []int{0, 0}) {
		t.Fatalf("a canceled message reached a terminal: %v", got)
	}
}
