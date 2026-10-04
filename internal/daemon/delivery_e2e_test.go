package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const e2eToken = "e2e-token"

type scriptedSession struct {
	status    surface.SessionStatus
	completed string
	reply     string
}

type deliveredMessage struct {
	sessionID string
	text      string
	model     string
	source    string
	steered   bool
}

type scriptedAgent struct {
	mu         sync.Mutex
	sessions   map[string]*scriptedSession
	sendErr    map[string]error
	observeErr map[string]error
	attempts   map[string]int
	delivered  []deliveredMessage
	turn       int
}

func newScriptedAgent(ids ...string) *scriptedAgent {
	agent := &scriptedAgent{sessions: map[string]*scriptedSession{}, sendErr: map[string]error{}, observeErr: map[string]error{}, attempts: map[string]int{}}
	for _, id := range ids {
		agent.sessions[id] = &scriptedSession{status: surface.StatusIdle}
	}
	return agent
}

func (a *scriptedAgent) session(id string) surface.Session {
	return surface.Session{ID: id, Surface: surface.KindCodex, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop"}
}

func (a *scriptedAgent) setStatus(id string, status surface.SessionStatus) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessions[id].status = status
}

func (a *scriptedAgent) complete(id, turnID, reply string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessions[id].status = surface.StatusIdle
	a.sessions[id].completed = turnID
	a.sessions[id].reply = reply
}

func (a *scriptedAgent) failSends(id string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err == nil {
		delete(a.sendErr, id)
		return
	}
	a.sendErr[id] = err
}

func (a *scriptedAgent) failObservation(id string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err == nil {
		delete(a.observeErr, id)
		return
	}
	a.observeErr[id] = err
}

func (a *scriptedAgent) attemptsFor(id string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.attempts[id]
}

func (a *scriptedAgent) deliveredTo(id string) []deliveredMessage {
	a.mu.Lock()
	defer a.mu.Unlock()
	var result []deliveredMessage
	for _, message := range a.delivered {
		if message.sessionID == id {
			result = append(result, message)
		}
	}
	return result
}

func (a *scriptedAgent) Name() surface.SurfaceKind { return surface.KindCodex }

func (a *scriptedAgent) List(context.Context) ([]surface.Session, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := make([]surface.Session, 0, len(a.sessions))
	for id := range a.sessions {
		result = append(result, a.session(id))
	}
	return result, nil
}

func (a *scriptedAgent) Resolve(_ context.Context, id string) (*surface.Session, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.sessions[id]; !ok {
		return nil, fmt.Errorf("session %s not found", id)
	}
	session := a.session(id)
	return &session, nil
}

func (a *scriptedAgent) Observe(_ context.Context, session *surface.Session) (*surface.TurnObservation, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.observeErr[session.ID]; err != nil {
		return nil, err
	}
	state, ok := a.sessions[session.ID]
	if !ok {
		return nil, fmt.Errorf("session %s not found", session.ID)
	}
	observation := &surface.TurnObservation{Status: state.status, CompletedTurnID: state.completed}
	if state.status == surface.StatusBusy {
		observation.ActiveTurnID = "active-" + session.ID
	}
	if state.completed != "" {
		observation.Reply = &surface.ReplyResult{Text: state.reply, Done: true}
	}
	return observation, nil
}

func (a *scriptedAgent) accept(sessionID, message string, options surface.SendOptions, steered bool) (*surface.SendResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.attempts[sessionID]++
	if err := a.sendErr[sessionID]; errors.Is(err, errEmptyResult) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	state, ok := a.sessions[sessionID]
	if !ok {
		return nil, surface.DeliveryTerminal(fmt.Errorf("session %s not found", sessionID), surface.DeliveryTargetMissing)
	}
	if state.status != surface.StatusIdle && !steered {
		return &surface.SendResult{Accepted: false}, nil
	}
	a.turn++
	a.delivered = append(a.delivered, deliveredMessage{sessionID: sessionID, text: message, model: options.Model, source: options.SourceSessionID, steered: steered})
	return &surface.SendResult{UUID: fmt.Sprintf("turn-%d", a.turn), Accepted: true}, nil
}

func (a *scriptedAgent) Send(ctx context.Context, session *surface.Session, message string) (*surface.SendResult, error) {
	return a.accept(session.ID, message, surface.SendOptions{SourceSessionID: surface.SourceSessionID(ctx)}, false)
}

func (a *scriptedAgent) SendWithOptions(ctx context.Context, session *surface.Session, message string, options surface.SendOptions) (*surface.SendResult, error) {
	options.SourceSessionID = surface.SourceSessionID(ctx)
	return a.accept(session.ID, message, options, false)
}

func (a *scriptedAgent) Steer(ctx context.Context, session *surface.Session, message string) error {
	_, err := a.accept(session.ID, message, surface.SendOptions{SourceSessionID: surface.SourceSessionID(ctx)}, true)
	return err
}

func (a *scriptedAgent) Reply(context.Context, *surface.Session, int) (*surface.ReplyResult, error) {
	return nil, errors.New("not scripted")
}
func (a *scriptedAgent) Tail(context.Context, *surface.Session, int) ([]surface.Exchange, error) {
	return nil, nil
}
func (a *scriptedAgent) Stream(context.Context, *surface.Session, string, func(surface.StreamEvent), time.Duration) error {
	return errors.New("not scripted")
}
func (a *scriptedAgent) GoalSet(context.Context, *surface.Session, string) error { return nil }
func (a *scriptedAgent) GoalClear(context.Context, *surface.Session) error       { return nil }
func (a *scriptedAgent) GoalGet(context.Context, *surface.Session) (*surface.GoalState, error) {
	return nil, nil
}
func (a *scriptedAgent) Compact(context.Context, *surface.Session) error { return nil }
func (a *scriptedAgent) Model(context.Context, *surface.Session, string) (string, error) {
	return "", nil
}
func (a *scriptedAgent) Interrupt(context.Context, *surface.Session) error { return nil }
func (a *scriptedAgent) Capabilities() surface.Capabilities {
	return surface.Capabilities{Send: true, Reply: true, Steer: true, Model: true}
}

type runningDaemon struct {
	t      *testing.T
	agent  *scriptedAgent
	server *httptest.Server
}

func startRunningDaemon(t *testing.T, agent *scriptedAgent) *runningDaemon {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	store, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	for id := range agent.sessions {
		if err := store.RegisterSession(agent.session(id)); err != nil {
			t.Fatal(err)
		}
	}
	d := New(store, []surface.Surface{agent})
	d.pollInterval = 10 * time.Millisecond
	server := httptest.NewServer(d.dashboardHandler(&dashboardServer{token: e2eToken}))
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = d.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-stopped
		server.Close()
		store.Close()
	})
	return &runningDaemon{t: t, agent: agent, server: server}
}

func (h *runningDaemon) request(method, path string, body any) (int, []byte) {
	h.t.Helper()
	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			h.t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}
	request, err := http.NewRequest(method, h.server.URL+path, reader)
	if err != nil {
		h.t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+e2eToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		h.t.Fatal(err)
	}
	defer response.Body.Close()
	var buffer bytes.Buffer
	if _, err := buffer.ReadFrom(response.Body); err != nil {
		h.t.Fatal(err)
	}
	return response.StatusCode, buffer.Bytes()
}

func (h *runningDaemon) action(body map[string]any) map[string]any {
	h.t.Helper()
	status, payload := h.request(http.MethodPost, "/api/v1/actions", body)
	if status != http.StatusOK {
		h.t.Fatalf("action %v status=%d body=%s", body["action"], status, payload)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		h.t.Fatalf("action %v body=%s: %v", body["action"], payload, err)
	}
	return decoded
}

func (h *runningDaemon) receipt(body map[string]any) map[string]any {
	h.t.Helper()
	response := h.action(body)
	result, ok := response["result"].(map[string]any)
	if !ok {
		h.t.Fatalf("action %v returned no receipt: %v", body["action"], response)
	}
	return result
}

func (h *runningDaemon) queue() []dashboardQueue {
	h.t.Helper()
	status, payload := h.request(http.MethodGet, "/api/v1/queue", nil)
	if status != http.StatusOK {
		h.t.Fatalf("queue status=%d body=%s", status, payload)
	}
	var decoded struct {
		Items []dashboardQueue `json:"items"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		h.t.Fatal(err)
	}
	return decoded.Items
}

func (h *runningDaemon) queueItem(id int64) dashboardQueue {
	h.t.Helper()
	for _, item := range h.queue() {
		if item.ID == id {
			return item
		}
	}
	h.t.Fatalf("queue item %d not listed", id)
	return dashboardQueue{}
}

func (h *runningDaemon) openCatalogEvents() *bufio.Reader {
	h.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.server.URL+"/api/v1/catalog-events?after=0", nil)
	if err != nil {
		cancel()
		h.t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+e2eToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		cancel()
		h.t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		cancel()
		h.t.Fatalf("catalog-events status=%d", response.StatusCode)
	}
	h.t.Cleanup(func() {
		cancel()
		response.Body.Close()
	})
	return bufio.NewReader(response.Body)
}

func waitForCatalogEvent(t *testing.T, reader *bufio.Reader, eventType string) map[string]any {
	t.Helper()
	found := make(chan map[string]any, 1)
	go func() {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var event map[string]any
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) != nil {
				return
			}
			if event["type"] == eventType {
				found <- event
				return
			}
		}
	}()
	select {
	case event := <-found:
		return event
	case <-time.After(5 * time.Second):
		t.Fatalf("no %s event on /api/v1/catalog-events", eventType)
		return nil
	}
}

func eventually(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func settle() { time.Sleep(150 * time.Millisecond) }

func queueID(t *testing.T, receipt map[string]any) int64 {
	t.Helper()
	value, ok := receipt["queueId"].(float64)
	if !ok || value == 0 {
		t.Fatalf("receipt has no queueId: %v", receipt)
	}
	return int64(value)
}

func TestSendToIdleSessionDeliversOnce(t *testing.T) {
	agent := newScriptedAgent("target")
	h := startRunningDaemon(t, agent)
	receipt := h.receipt(map[string]any{"action": "send", "sessionId": "target", "message": "run the build"})
	if receipt["evidence"] != string(surface.EvidenceDelivered) {
		t.Fatalf("receipt=%v", receipt)
	}
	settle()
	if got := agent.deliveredTo("target"); len(got) != 1 || got[0].text != "run the build" {
		t.Fatalf("delivered=%+v", got)
	}
}

func TestBusySessionQueuesThenRunLoopDeliversOnceWithOptions(t *testing.T) {
	agent := newScriptedAgent("sender", "target")
	agent.setStatus("target", surface.StatusBusy)
	h := startRunningDaemon(t, agent)
	receipt := h.receipt(map[string]any{"action": "send", "sessionId": "target", "sourceSessionId": "sender", "model": "gpt-test", "message": "after this turn"})
	if receipt["evidence"] != string(surface.EvidenceQueued) {
		t.Fatalf("receipt=%v", receipt)
	}
	id := queueID(t, receipt)
	settle()
	if item := h.queueItem(id); item.Historical || len(agent.deliveredTo("target")) != 0 {
		t.Fatalf("busy target received the queued message early: item=%+v", item)
	}
	agent.setStatus("target", surface.StatusIdle)
	eventually(t, "queued delivery", func() bool { return len(agent.deliveredTo("target")) > 0 })
	settle()
	got := agent.deliveredTo("target")
	if len(got) != 1 || got[0].text != "after this turn" || got[0].model != "gpt-test" || got[0].source != "sender" {
		t.Fatalf("delivered=%+v", got)
	}
	if item := h.queueItem(id); !item.Historical || item.Evidence != surface.EvidenceDelivered {
		t.Fatalf("queue item=%+v", item)
	}
}

func TestUnknownQueuedOutcomeIsDeadLetteredAndNeverResent(t *testing.T) {
	for name, sendErr := range map[string]error{
		"outcome unknown": surface.DeliveryOutcomeUnknown(errors.New("connection dropped after write")),
		"empty result":    nil,
	} {
		t.Run(name, func(t *testing.T) {
			agent := newScriptedAgent("target")
			agent.setStatus("target", surface.StatusBusy)
			h := startRunningDaemon(t, agent)
			id := queueID(t, h.receipt(map[string]any{"action": "send", "sessionId": "target", "message": "maybe delivered"}))
			if sendErr != nil {
				agent.failSends("target", sendErr)
			} else {
				agent.failSends("target", errEmptyResult)
			}
			agent.setStatus("target", surface.StatusIdle)
			eventually(t, "dead-lettered queue item", func() bool { return h.queueItem(id).Status == "dead" })
			settle()
			if item := h.queueItem(id); item.Evidence != surface.EvidenceUnknown {
				t.Fatalf("queue item=%+v", item)
			}
			if attempts := agent.attemptsFor("target"); attempts != 2 {
				t.Fatalf("attempts=%d, want the initial busy attempt plus exactly one queued attempt", attempts)
			}
		})
	}
}

var errEmptyResult = errors.New("empty result")

func TestTerminalQueuedFailureNotifiesSenderOnceAndPublishesProblem(t *testing.T) {
	agent := newScriptedAgent("sender", "target")
	agent.setStatus("target", surface.StatusBusy)
	h := startRunningDaemon(t, agent)
	events := h.openCatalogEvents()
	id := queueID(t, h.receipt(map[string]any{"action": "send", "sessionId": "target", "sourceSessionId": "sender", "message": "rejected later"}))
	agent.failSends("target", surface.DeliveryTerminal(errors.New("target rejected input"), surface.DeliveryInvalidRequest))
	agent.setStatus("target", surface.StatusIdle)
	problem := waitForCatalogEvent(t, events, "delivery.problem")
	if problem == nil {
		t.Fatal("missing delivery.problem")
	}
	eventually(t, "sender notice", func() bool { return len(agent.deliveredTo("sender")) > 0 })
	settle()
	if item := h.queueItem(id); item.Status != "dead" || item.Evidence != surface.EvidenceFailed {
		t.Fatalf("queue item=%+v", item)
	}
	if notices := agent.deliveredTo("sender"); len(notices) != 1 {
		t.Fatalf("sender notices=%+v", notices)
	}
	if attempts := agent.attemptsFor("target"); attempts != 2 {
		t.Fatalf("attempts=%d", attempts)
	}
}

func TestUnavailableTargetKeepsQueuedMessageAndDeliversOnceAfterRecovery(t *testing.T) {
	agent := newScriptedAgent("target")
	agent.setStatus("target", surface.StatusBusy)
	h := startRunningDaemon(t, agent)
	id := queueID(t, h.receipt(map[string]any{"action": "send", "sessionId": "target", "message": "survive the outage"}))
	agent.failSends("target", surface.DeliveryUnavailable(errors.New("bridge restarting")))
	agent.setStatus("target", surface.StatusIdle)
	eventually(t, "deferred attempt", func() bool { return agent.attemptsFor("target") >= 2 })
	settle()
	if item := h.queueItem(id); item.Historical || item.Status == "dead" {
		t.Fatalf("unavailable delivery lost the message: %+v", item)
	}
	agent.failSends("target", nil)
	eventually(t, "recovered delivery", func() bool { return len(agent.deliveredTo("target")) > 0 })
	settle()
	if got := agent.deliveredTo("target"); len(got) != 1 {
		t.Fatalf("delivered=%+v", got)
	}
	if item := h.queueItem(id); !item.Historical || item.Evidence != surface.EvidenceDelivered {
		t.Fatalf("queue item=%+v", item)
	}
}

func TestQueuedMessageWaitsThroughObservationOutageAndDeliversOnce(t *testing.T) {
	agent := newScriptedAgent("target")
	agent.setStatus("target", surface.StatusBusy)
	h := startRunningDaemon(t, agent)
	id := queueID(t, h.receipt(map[string]any{"action": "send", "sessionId": "target", "message": "deliver after bridge recovery"}))
	agent.failObservation("target", errors.New("Codex Desktop bridge was replaced; rebinding"))
	agent.setStatus("target", surface.StatusIdle)
	settle()
	if item := h.queueItem(id); item.Historical || item.Status != "pending" || item.Attempts != 0 || len(agent.deliveredTo("target")) != 0 {
		t.Fatalf("observation outage touched the queued message: item=%+v", item)
	}
	agent.failObservation("target", nil)
	eventually(t, "delivery after recovery", func() bool { return h.queueItem(id).Historical })
	settle()
	if got := agent.deliveredTo("target"); len(got) != 1 {
		t.Fatalf("delivered=%+v", got)
	}
	if item := h.queueItem(id); item.Attempts != 1 || item.Evidence != surface.EvidenceDelivered {
		t.Fatalf("queue item=%+v", item)
	}
}

func TestQueuedMessageWaitsWhileTargetStatusIsUnknown(t *testing.T) {
	agent := newScriptedAgent("target")
	agent.setStatus("target", surface.StatusBusy)
	h := startRunningDaemon(t, agent)
	id := queueID(t, h.receipt(map[string]any{"action": "send", "sessionId": "target", "message": "hold until known"}))
	agent.setStatus("target", surface.StatusUnknown)
	settle()
	if got := agent.deliveredTo("target"); len(got) != 0 {
		t.Fatalf("delivered while status unknown: %+v", got)
	}
	agent.setStatus("target", surface.StatusIdle)
	eventually(t, "delivery once idle", func() bool { return h.queueItem(id).Historical })
	if got := agent.deliveredTo("target"); len(got) != 1 {
		t.Fatalf("delivered=%+v", got)
	}
}

func TestSteerSendToBusySessionSteersActiveTurnOnce(t *testing.T) {
	agent := newScriptedAgent("target")
	agent.setStatus("target", surface.StatusBusy)
	h := startRunningDaemon(t, agent)
	receipt := h.receipt(map[string]any{"action": "send", "sessionId": "target", "busyDelivery": "steer", "message": "also check the docs"})
	if receipt["evidence"] != string(surface.EvidenceDelivered) {
		t.Fatalf("receipt=%v", receipt)
	}
	agent.setStatus("target", surface.StatusIdle)
	settle()
	got := agent.deliveredTo("target")
	if len(got) != 1 || !got[0].steered || got[0].text != "also check the docs" {
		t.Fatalf("delivered=%+v", got)
	}
}

func TestRelayForwardsOnlyNewCompletionsOnce(t *testing.T) {
	agent := newScriptedAgent("worker", "reviewer")
	agent.complete("worker", "turn-old", "finished before the route existed")
	h := startRunningDaemon(t, agent)
	settle()
	h.action(map[string]any{"action": "relay-add", "fromId": "worker", "toId": "reviewer"})
	settle()
	if got := agent.deliveredTo("reviewer"); len(got) != 0 {
		t.Fatalf("baseline completion was relayed: %+v", got)
	}
	agent.complete("worker", "turn-new", "the fix is ready for review")
	eventually(t, "relayed completion", func() bool { return len(agent.deliveredTo("reviewer")) > 0 })
	settle()
	got := agent.deliveredTo("reviewer")
	if len(got) != 1 || !strings.Contains(got[0].text, "the fix is ready for review") {
		t.Fatalf("relayed=%+v", got)
	}
}

func TestOneShotRelayFiresForTheFirstCompletionOnly(t *testing.T) {
	agent := newScriptedAgent("worker", "reviewer")
	h := startRunningDaemon(t, agent)
	settle()
	h.action(map[string]any{"action": "relay-add", "fromId": "worker", "toId": "reviewer", "once": true})
	settle()
	agent.complete("worker", "turn-1", "first result")
	eventually(t, "first relay", func() bool { return len(agent.deliveredTo("reviewer")) > 0 })
	agent.complete("worker", "turn-2", "second result")
	settle()
	got := agent.deliveredTo("reviewer")
	if len(got) != 1 || !strings.Contains(got[0].text, "first result") {
		t.Fatalf("relayed=%+v", got)
	}
}
