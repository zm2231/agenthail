package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
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
	mu       sync.Mutex
	sent     []string
	steered  []string
	observed int
}

func (c *recordingClaude) Observe(ctx context.Context, session *surface.Session) (*surface.TurnObservation, error) {
	observation, err := c.Claude.Observe(ctx, session)
	c.mu.Lock()
	c.observed++
	c.mu.Unlock()
	return observation, err
}

// awaitObservations returns once the daemon has read the transcript n more
// times, so an absence checked afterwards covers that many passes.
func (c *recordingClaude) observations() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.observed
}

func (c *recordingClaude) awaitObservations(t *testing.T, n int) {
	t.Helper()
	c.mu.Lock()
	target := c.observed + n
	c.mu.Unlock()
	eventually(t, fmt.Sprintf("%d more observations", n), func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.observed >= target
	})
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
	// stall, while set, holds every response open after the push is
	// recorded, as a relay would look to a daemon stopped mid-delivery.
	stall chan struct{}
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
		stall := recorder.stall
		recorder.mu.Unlock()
		if stall != nil {
			<-stall
		}
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

func (p *pushRecorder) all(eventType string) []map[string]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]map[string]string(nil), p.events[eventType]...)
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
	return runDaemonWith(t, store, func(*Daemon) {}, adapters...)
}

// runDaemonWith is runDaemon with setup applied to the daemon before it runs.
func runDaemonWith(t *testing.T, store *registry.Registry, setup func(*Daemon), adapters ...surface.Surface) (*runningDaemon, func()) {
	t.Helper()
	d := New(store, adapters)
	d.pollInterval = 10 * time.Millisecond
	setup(d)
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
	return &runningDaemon{t: t, server: server, store: store, daemon: d}, stop
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

type claudeQuestion struct {
	store      *registry.Registry
	claude     *recordingClaude
	pushes     *pushRecorder
	session    surface.Session
	transcript string
	process    *claudetest.Fixture
}

// questionAsked is when the synthetic session started waiting on its user.
var questionAsked = time.Date(2026, 10, 18, 8, 14, 41, 0, time.UTC)

// questionID is the id Agenthail gives that question.
var questionID = fmt.Sprintf("conversation-question:question:%d", questionAsked.UnixMilli())

// answer records what Claude writes once the user answers: the process is
// working again and the resolved tool call reaches the transcript.
func (q claudeQuestion) answer(t *testing.T) {
	t.Helper()
	q.process.RecordStatus("conversation-question", q.process.Home, "session_question", "busy", "", questionAsked.Add(time.Hour))
	appendClaudeRecords(t, q.transcript,
		`{"type":"assistant","uuid":"ask","timestamp":"2026-10-18T08:14:41Z","message":{"id":"msg-ask","stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_question","name":"AskUserQuestion","input":{"questions":[{"question":"Which branch?"}]}}]}}`,
		`{"type":"user","uuid":"answer","timestamp":"2026-10-18T20:18:07Z","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_question","content":"main"}]}}`)
}

// newClaudeQuestion is a Claude session whose turn has asked the user an
// unanswered AskUserQuestion.
func newClaudeQuestion(t *testing.T) claudeQuestion {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	process := claudetest.New(t)
	process.Listen()
	transcript := process.Transcript("conversation-question", process.Home)
	process.RecordStatus("conversation-question", process.Home, "session_question", "waiting", "input needed", questionAsked)
	appendClaudeRecords(t, transcript,
		`{"type":"user","uuid":"prompt","timestamp":"2026-10-18T08:14:31Z","message":{"content":"Plan the release"}}`,
		`{"type":"attachment","timestamp":"2026-10-18T08:14:42Z"}`)
	store, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	session := surface.Session{ID: "conversation-question", Bridge: "session_question", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: process.PID, Transport: "uds", Status: surface.StatusBusy, HasLocal: true, Transcript: transcript}
	if err := store.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	pushes := newPushRecorder(t, store)
	claude := &recordingClaude{Claude: surfaces.NewClaude(nil, process.Home)}

	return claudeQuestion{store: store, claude: claude, pushes: pushes, session: session, transcript: transcript, process: process}
}

func TestClaudeQuestionHoldsMessagesNotifiesOnceAndDeliversAfterTheAnswer(t *testing.T) {
	q := newClaudeQuestion(t)
	store, claude, pushes, session, transcript := q.store, q.claude, q.pushes, q.session, q.transcript
	h, stop := runDaemon(t, store, claude)
	eventually(t, "the session to show it is waiting on the user", func() bool {
		waiting := h.sessionWaiting(session.ID)
		return waiting != nil && waiting.ID == questionID && waiting.Kind == surface.WaitingQuestion
	})
	queued := h.receipt(map[string]any{"action": "send", "sessionId": session.ID, "message": "after you answer", "busyDelivery": "queue"})
	steer := h.receipt(map[string]any{"action": "steer", "sessionId": session.ID, "message": "steer after the answer"})
	for _, receipt := range []map[string]any{queued, steer} {
		if receipt["evidence"] != string(surface.EvidenceQueued) || receipt["waitingOn"] == nil {
			t.Fatalf("a message to a session waiting on its user was not held: %v", receipt)
		}
	}
	queuedID, steerID := queueID(t, queued), queueID(t, steer)
	claude.awaitObservations(t, 3)
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
	claude.awaitObservations(t, 3)
	if _, announced, err := store.WaitingState(session.ID); err != nil || !announced {
		t.Fatalf("announced=%v err=%v", announced, err)
	}
	if got := pushes.count("session.waiting"); got != 1 {
		t.Fatalf("one question notified %d times", got)
	}
	if push := pushes.first("session.waiting"); push["sessionId"] != session.ID || push["turnId"] != questionID || push["message"] != "Claude Code agent is waiting on you" {
		t.Fatalf("push=%v", push)
	}

	q.answer(t)
	eventually(t, "the held steer to reach the answered turn", func() bool {
		_, steered := claude.delivered()
		return len(steered) == 1
	})
	eventually(t, "the answered question to clear", func() bool { return h.sessionWaiting(session.ID) == nil })
	if item := h.queueItem(queuedID); item.Status != "pending" || item.HeldBy != registry.QueueHeldBusy {
		t.Fatalf("queued message left before the turn ended: %+v", item)
	}

	appendClaudeRecords(t, transcript, `{"type":"assistant","uuid":"done","timestamp":"2026-10-18T20:18:30Z","message":{"id":"msg-done","stop_reason":"end_turn","content":[{"type":"text","text":"Released from main."}]}}`,
		`{"type":"system","subtype":"turn_duration","timestamp":"2026-10-18T20:18:31Z"}`)
	q.process.RecordStatus("conversation-question", q.process.Home, "session_question", "idle", "", questionAsked.Add(2*time.Hour))
	eventually(t, "the queued message after the turn ends", func() bool {
		sent, _ := claude.delivered()
		return len(sent) == 1
	})
	eventually(t, "both held messages to finish", func() bool {
		for _, id := range []int64{queuedID, steerID} {
			if item, err := store.QueueItem(id, nil); err != nil || item.Status != "delivered" {
				return false
			}
		}
		return true
	})
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
	awaitThreadReads(t, lifecycle, 3)
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
	awaitThreadReads(t, lifecycle, 3)
	if thread, _ := lifecycle.Daemon().Thread(id); len(thread.Turns) != 1 || thread.Turns[0].User != "Apply the migration\nalso update the changelog" {
		t.Fatalf("thread=%+v", thread.Turns)
	}
	if got := pushes.count("session.waiting"); got != 1 {
		t.Fatalf("one approval notified %d times", got)
	}
}

// awaitThreadReads returns once the daemon has read Codex threads n more
// times.
func awaitThreadReads(t *testing.T, lifecycle *codextest.Lifecycle, n int) {
	t.Helper()
	target := len(lifecycle.Daemon().Calls("thread/read")) + n
	eventually(t, fmt.Sprintf("%d more thread reads", n), func() bool { return len(lifecycle.Daemon().Calls("thread/read")) >= target })
}

func TestQuestionRecordedButNotAnnouncedIsAnnouncedAfterRestart(t *testing.T) {
	q := newClaudeQuestion(t)
	if record, err := q.store.RecordWaiting(q.session.ID, &surface.Waiting{Kind: surface.WaitingQuestion, ID: questionID, Since: questionAsked}, questionAsked); err != nil || !record.Announce {
		t.Fatalf("record=%+v err=%v", record, err)
	}
	if _, err := q.store.QueueMessageWithOptions(q.session.ID, "after you answer", "", surface.SendOptions{}); err != nil {
		t.Fatal(err)
	}
	runDaemon(t, q.store, q.claude)
	eventually(t, "the pending announcement", func() bool { return q.pushes.count("session.waiting") == 1 })
	eventually(t, "the announcement to be recorded", func() bool {
		_, announced, err := q.store.WaitingState(q.session.ID)
		return err == nil && announced
	})
	q.claude.awaitObservations(t, 3)
	if got := q.pushes.count("session.waiting"); got != 1 {
		t.Fatalf("one question notified %d times", got)
	}
}

func TestQuestionAnnouncedAgainAfterAStopMidDeliveryCollapsesIntoOne(t *testing.T) {
	q := newClaudeQuestion(t)
	stall := make(chan struct{})
	q.pushes.mu.Lock()
	q.pushes.stall = stall
	q.pushes.mu.Unlock()
	t.Cleanup(func() { close(stall) })
	_, stop := runDaemon(t, q.store, q.claude)
	eventually(t, "the first announcement", func() bool { return q.pushes.count("session.waiting") == 1 })
	stop()
	if _, announced, err := q.store.WaitingState(q.session.ID); err != nil || announced {
		t.Fatalf("announced=%v err=%v before the delivery finished", announced, err)
	}
	q.pushes.mu.Lock()
	q.pushes.stall = nil
	q.pushes.mu.Unlock()
	runDaemon(t, q.store, q.claude)
	eventually(t, "the repeated announcement", func() bool { return q.pushes.count("session.waiting") == 2 })
	key := waitingNotificationKey(q.session.ID, questionID)
	for _, push := range q.pushes.all("session.waiting") {
		if push["collapseId"] != key || push["turnId"] != questionID {
			t.Fatalf("push=%v, want collapse id %s so the device shows one notification", push, key)
		}
	}
	if other := waitingNotificationKey(q.session.ID, questionID+"-next"); other == key || len(key) > 64 {
		t.Fatalf("key=%q next question=%q", key, other)
	}
}

func TestCatalogRowSelectedBeforeTheAnswerCannotReviveTheQuestion(t *testing.T) {
	q := newClaudeQuestion(t)
	var armed atomic.Bool
	selected, release, contended := make(chan *surface.Waiting, 1), make(chan struct{}), make(chan struct{}, 1)
	h, _ := runDaemonWith(t, q.store, func(d *Daemon) {
		d.waitingContended = func(id string) {
			if id == q.session.ID {
				select {
				case contended <- struct{}{}:
				default:
				}
			}
		}
		d.catalogSelected = func(id string) {
			if id == q.session.ID && armed.CompareAndSwap(true, false) {
				waiting, _, _ := q.store.WaitingState(id)
				selected <- waiting
				<-release
			}
		}
	}, q.claude)
	eventually(t, "the session to show it is waiting on the user", func() bool { return h.sessionWaiting(q.session.ID) != nil })
	eventually(t, "the question to be announced", func() bool {
		_, announced, err := q.store.WaitingState(q.session.ID)
		return err == nil && announced
	})
	armed.Store(true)
	h.daemon.catalogNudge <- struct{}{}
	select {
	case waiting := <-selected:
		if waiting == nil || waiting.ID != questionID {
			close(release)
			t.Fatalf("the paused publication selected %+v", waiting)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no catalog publication reached the session")
	}
	answered := q.session
	observed := make(chan error, 1)
	go func() {
		observation, err := h.daemon.observeTurn(context.Background(), q.claude, &answered)
		if err == nil && observation.WaitingOn != nil {
			err = fmt.Errorf("observation after the answer still waits: %+v", observation.WaitingOn)
		}
		observed <- err
	}()
	// The observation must wait for the paused row to commit, not read the
	// turn while a row carrying the question is still to be written.
	select {
	case <-contended:
	case <-observed:
		close(release)
		t.Fatal("the turn was observed while a catalog row with the question was still to be written")
	}
	q.answer(t)
	close(release)
	if err := <-observed; err != nil {
		t.Fatal(err)
	}
	cleared := func() bool {
		recorded, _, err := q.store.WaitingState(q.session.ID)
		if err != nil || recorded != nil || h.sessionWaiting(q.session.ID) != nil {
			return false
		}
		snapshot, err := q.store.CatalogSnapshot()
		if err != nil {
			return false
		}
		for _, row := range snapshot.Sessions {
			if row.Session.ID == q.session.ID {
				return row.Session.WaitingOn == nil
			}
		}
		return false
	}
	eventually(t, "the answer to clear the record, the catalog and the API", cleared)
	armed.Store(true)
	h.daemon.catalogNudge <- struct{}{}
	select {
	case waiting := <-selected:
		if waiting != nil {
			t.Fatalf("a later publication selected the answered question: %+v", waiting)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no later catalog publication reached the session")
	}
	eventually(t, "the later publication to keep the session clear", cleared)
	if got := q.pushes.count("session.waiting"); got != 1 {
		t.Fatalf("one question notified %d times", got)
	}
}

func TestQueuedSteerToAClaudeSessionWithoutABridgeWaitsForTheTurn(t *testing.T) {
	store, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	adapters := surface.Adapters{surfaces.NewClaude(nil, t.TempDir())}
	for _, test := range []struct {
		bridge string
		want   registry.QueueHold
	}{{"", registry.QueueHeldBusy}, {"session_steerable", ""}} {
		id := "conversation-bridge-" + test.bridge
		if err := store.RegisterSession(surface.Session{ID: id, Bridge: test.bridge, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Transport: "uds", Status: surface.StatusBusy}); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveRuntimeState(id, surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "turn-1"}); err != nil {
			t.Fatal(err)
		}
		queueID, err := store.QueueMessageWithOptions(id, "into the turn", "", surface.SendOptions{BusyDelivery: "steer"})
		if err != nil {
			t.Fatal(err)
		}
		if item, err := store.QueueItem(queueID, adapters); err != nil || item.HeldBy != test.want {
			t.Fatalf("bridge %q: item=%+v err=%v, want held by %q", test.bridge, item, err, test.want)
		}
	}
}
