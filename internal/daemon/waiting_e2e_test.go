package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

// recordingClaude is the real Claude surface reading a synthetic transcript,
// with the peer socket replaced by a recorder.
type recordingClaude struct {
	*surfaces.Claude
	mu      sync.Mutex
	sent    []string
	steered []string
}

func (c *recordingClaude) Send(_ context.Context, _ *surface.Session, message string) (*surface.SendResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, message)
	return &surface.SendResult{UUID: "peer", Accepted: true}, nil
}

func (c *recordingClaude) Steer(_ context.Context, _ *surface.Session, message string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.steered = append(c.steered, message)
	return nil
}

func (c *recordingClaude) delivered() (sent, steered []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.sent...), append([]string(nil), c.steered...)
}

// pushRecorder stands in for the push relay and counts notifications by event
// type.
type pushRecorder struct {
	mu     sync.Mutex
	events map[string][]map[string]string
}

func newPushRecorder(t *testing.T, store *registry.Registry) *pushRecorder {
	t.Helper()
	recorder := &pushRecorder{events: map[string][]map[string]string{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		recorder.mu.Lock()
		recorder.events[payload["eventType"]] = append(recorder.events[payload["eventType"]], payload)
		recorder.mu.Unlock()
	}))
	t.Cleanup(server.Close)
	t.Setenv("AGENTHAIL_PUSH_RELAY_URL", server.URL)
	pairing, err := store.CreateDevicePairing("Synthetic phone", nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	device, _, err := store.CompleteDevicePairing(pairing.Secret, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveDevicePushTarget(device.ID, "installation", "credential"); err != nil {
		t.Fatal(err)
	}
	return recorder
}

func (p *pushRecorder) count(eventType string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.events[eventType])
}

func (p *pushRecorder) first(eventType string) map[string]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.events[eventType][0]
}

// runDaemon runs the daemon loop and API over store until the returned stop
// is called or the test ends.
func runDaemon(t *testing.T, store *registry.Registry, adapters ...surface.Surface) (*runningDaemon, func()) {
	t.Helper()
	d := New(store, adapters)
	d.pollInterval = 10 * time.Millisecond
	server := httptest.NewServer(d.dashboardHandler(&dashboardServer{token: e2eToken}))
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = d.Run(ctx)
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			<-stopped
			server.Close()
		})
	}
	t.Cleanup(stop)
	return &runningDaemon{t: t, server: server, store: store}, stop
}

func (h *runningDaemon) sessionWaiting(id string) *surface.Waiting {
	h.t.Helper()
	status, payload := h.request(http.MethodGet, "/api/v1/snapshot", nil)
	if status != http.StatusOK {
		h.t.Fatalf("snapshot status=%d body=%s", status, payload)
	}
	var decoded struct {
		Sessions []dashboardSession `json:"sessions"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		h.t.Fatal(err)
	}
	for _, session := range decoded.Sessions {
		if session.ID == id {
			return session.WaitingOn
		}
	}
	return nil
}

func appendClaudeRecords(t *testing.T, path string, records ...string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	for _, record := range records {
		if _, err := file.WriteString(record + "\n"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestClaudeQuestionHoldsMessagesNotifiesOnceAndDeliversAfterTheAnswer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	process := claudetest.New(t)
	process.Listen()
	transcript := process.Transcript("conversation-question", process.Home)
	process.Record("conversation-question", process.Home, "session_question")
	appendClaudeRecords(t, transcript,
		`{"type":"user","uuid":"prompt","timestamp":"2026-10-18T08:14:31Z","message":{"content":"Plan the release"}}`,
		`{"type":"assistant","uuid":"ask","timestamp":"2026-10-18T08:14:41Z","message":{"id":"msg-ask","stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_question","name":"AskUserQuestion","input":{"questions":[{"question":"Which branch?"}]}}]}}`,
		`{"type":"attachment","timestamp":"2026-10-18T08:14:42Z"}`)
	store, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	session := surface.Session{ID: "session_question", Surface: surface.KindClaude, PID: process.PID, Transport: "uds", Status: surface.StatusBusy, HasLocal: true, Transcript: transcript}
	if err := store.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	pushes := newPushRecorder(t, store)
	claude := &recordingClaude{Claude: surfaces.NewClaude("Default", process.Home)}

	h, stop := runDaemon(t, store, claude)
	eventually(t, "the session to show it is waiting on the user", func() bool {
		waiting := h.sessionWaiting(session.ID)
		return waiting != nil && waiting.ID == "toolu_question" && waiting.Kind == surface.WaitingQuestion
	})
	queued := h.receipt(map[string]any{"action": "send", "sessionId": session.ID, "message": "after you answer", "busyDelivery": "queue"})
	steer := h.receipt(map[string]any{"action": "steer", "sessionId": session.ID, "message": "steer after the answer"})
	for _, receipt := range []map[string]any{queued, steer} {
		if receipt["evidence"] != string(surface.EvidenceQueued) || receipt["waitingOn"] == nil {
			t.Fatalf("a message to a session waiting on its user was not held: %v", receipt)
		}
	}
	queuedID, steerID := queueID(t, queued), queueID(t, steer)
	settle()
	for _, id := range []int64{queuedID, steerID} {
		if item := h.queueItem(id); item.Status != "pending" || item.HeldBy != registry.QueueHeldWaiting {
			t.Fatalf("item=%+v", item)
		}
	}
	if sent, steered := claude.delivered(); len(sent) != 0 || len(steered) != 0 {
		t.Fatalf("delivered into an unanswered question: sent=%q steered=%q", sent, steered)
	}
	eventually(t, "the waiting notification", func() bool { return pushes.count("session.waiting") == 1 })

	stop()
	h, _ = runDaemon(t, store, claude)
	eventually(t, "the restarted daemon to see the question", func() bool { return h.sessionWaiting(session.ID) != nil })
	settle()
	if got := pushes.count("session.waiting"); got != 1 {
		t.Fatalf("one question notified %d times", got)
	}
	if push := pushes.first("session.waiting"); push["sessionId"] != session.ID || push["turnId"] != "toolu_question" || push["message"] != "Claude Code agent is waiting on you" {
		t.Fatalf("push=%v", push)
	}

	appendClaudeRecords(t, transcript, `{"type":"user","uuid":"answer","timestamp":"2026-10-18T20:18:07Z","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_question","content":"main"}]}}`)
	eventually(t, "the held steer to reach the answered turn", func() bool {
		_, steered := claude.delivered()
		return len(steered) == 1
	})
	eventually(t, "the answered question to clear", func() bool { return h.sessionWaiting(session.ID) == nil })
	if item := h.queueItem(queuedID); item.Status != "pending" || item.HeldBy != registry.QueueHeldBusy {
		t.Fatalf("queued message left before the turn ended: %+v", item)
	}

	appendClaudeRecords(t, transcript, `{"type":"assistant","uuid":"done","timestamp":"2026-10-18T20:18:30Z","message":{"id":"msg-done","stop_reason":"end_turn","content":[{"type":"text","text":"Released from main."}]}}`)
	eventually(t, "the queued message after the turn ends", func() bool {
		sent, _ := claude.delivered()
		return len(sent) == 1
	})
	settle()
	sent, steered := claude.delivered()
	if len(sent) != 1 || sent[0] != "after you answer" || len(steered) != 1 || steered[0] != "steer after the answer" {
		t.Fatalf("sent=%q steered=%q", sent, steered)
	}
	if got := pushes.count("session.waiting"); got != 1 {
		t.Fatalf("one question notified %d times", got)
	}
}

func TestCodexPendingApprovalShowsWaitingAndHoldsSteerUntilApproved(t *testing.T) {
	home := codextest.Home(t)
	lifecycle := codextest.InstallLifecycle(t, home, func(string, map[string]any) map[string]any { return nil })
	store, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	pushes := newPushRecorder(t, store)
	h, stop := runDaemon(t, store, surfaces.NewCodex(unreachableDesktop))
	status, payload := h.request(http.MethodPost, "/api/v1/actions", map[string]any{"action": "session-create", "surface": "codex", "message": "Apply the migration", "cwd": t.TempDir()})
	var created struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if err := json.Unmarshal(payload, &created); status != http.StatusCreated || err != nil || created.Session.ID == "" {
		t.Fatalf("create status=%d body=%s err=%v", status, payload, err)
	}
	id := created.Session.ID
	lifecycle.Daemon().SetActiveFlags(id, "waitingOnApproval")
	stop()
	h, _ = runDaemon(t, store, surfaces.NewCodex(unreachableDesktop))
	eventually(t, "the thread to show it is waiting on an approval", func() bool {
		waiting := h.sessionWaiting(id)
		return waiting != nil && waiting.Kind == surface.WaitingApproval
	})
	receipt := h.receipt(map[string]any{"action": "send", "sessionId": id, "message": "also update the changelog", "busyDelivery": "steer"})
	if receipt["evidence"] != string(surface.EvidenceQueued) || receipt["waitingOn"] == nil {
		t.Fatalf("a steer into a pending approval was not held: %v", receipt)
	}
	held := queueID(t, receipt)
	settle()
	if item := h.queueItem(held); item.Status != "pending" || item.HeldBy != registry.QueueHeldWaiting {
		t.Fatalf("item=%+v", item)
	}
	if thread, _ := lifecycle.Daemon().Thread(id); thread.Turns[0].User != "Apply the migration" {
		t.Fatalf("steered into a pending approval: %+v", thread.Turns)
	}
	eventually(t, "the waiting notification", func() bool { return pushes.count("session.waiting") == 1 })

	lifecycle.Daemon().SetActiveFlags(id)
	eventually(t, "the held steer after approval", func() bool {
		thread, _ := lifecycle.Daemon().Thread(id)
		return thread.Turns[0].User == "Apply the migration\nalso update the changelog"
	})
	settle()
	if thread, _ := lifecycle.Daemon().Thread(id); len(thread.Turns) != 1 || thread.Turns[0].User != "Apply the migration\nalso update the changelog" {
		t.Fatalf("thread=%+v", thread.Turns)
	}
	if got := pushes.count("session.waiting"); got != 1 {
		t.Fatalf("one approval notified %d times", got)
	}
}
