package acphost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/acp/acptest"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestMain(m *testing.M) {
	acptest.RunIfAgent()
	os.Exit(m.Run())
}

type harness struct {
	t      *testing.T
	home   string
	host   *Host
	client Client
	served chan struct{}
}

func fakeAgent(t *testing.T, id string) Agent {
	argv, env := acptest.Command(t.TempDir())
	return Agent{ID: id, Label: "Fake " + id, Command: argv, Env: env, Modes: map[string]string{"ask": "plan", "default": "edits", "bypass": "full"}}
}

func startHost(t *testing.T, agents ...Agent) *harness {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "acph")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	if err := os.MkdirAll(filepath.Join(home, ".agenthail"), 0o700); err != nil {
		t.Fatal(err)
	}
	host := NewHost(home, "test", agents, nil)
	socket := SocketPath(home)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan struct{})
	go func() {
		_ = serveUntilExit(host, listener, socket)
		close(served)
	}()
	t.Cleanup(func() {
		for _, state := range host.held().Sessions {
			host.closeSession(state.SessionID, false, false)
		}
		host.exit()
	})
	return &harness{t: t, home: home, host: host, client: Client{Home: home}, served: served}
}

func (h *harness) create(agent, ceiling, mode string) State {
	h.t.Helper()
	state, err := h.client.Create(context.Background(), CreateParams{SessionID: uuid.NewString(), Agent: agent, Cwd: h.t.TempDir(), Ceiling: ceiling, Mode: mode})
	if err != nil {
		h.t.Fatal(err)
	}
	return state
}

func (h *harness) waitFor(id string, ok func(State) bool) State {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if state, found, err := h.client.State(context.Background(), id); err == nil && found && ok(state) {
			return state
		}
		time.Sleep(20 * time.Millisecond)
	}
	state, _, _ := h.client.State(context.Background(), id)
	h.t.Fatalf("session %s never reached the expected state; last %+v", id, state)
	return State{}
}

func (h *harness) send(id, text string) string {
	h.t.Helper()
	result, err := h.client.Send(context.Background(), SendParams{SessionID: id, Text: text})
	if err != nil {
		h.t.Fatal(err)
	}
	return result.TurnID
}

func (h *harness) items(agent, id string) map[string]surface.TimelineItem {
	h.t.Helper()
	file, err := os.Open(DefaultStore(h.home).TranscriptPath(agent, id))
	if err != nil {
		h.t.Fatal(err)
	}
	defer file.Close()
	items := map[string]surface.TimelineItem{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var line Line
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			h.t.Fatalf("bad transcript line %q: %v", scanner.Text(), err)
		}
		if line.Item != nil {
			items[line.Item.ID] = *line.Item
		}
	}
	return items
}

func idle(state State) bool { return state.Status == StatusIdle }

func TestHostRunsATurnAndRecordsIt(t *testing.T) {
	h := startHost(t, fakeAgent(t, "fake"))
	state := h.create("fake", "", "")
	turn := h.send(state.SessionID, "think plan tool reply:hello-world")
	done := h.waitFor(state.SessionID, idle)
	if done.LastTurnID != turn || done.LastStop != "end_turn" {
		t.Fatalf("state=%+v", done)
	}
	items := h.items("fake", state.SessionID)
	checks := map[string]func(surface.TimelineItem) bool{
		turn + ":user": func(i surface.TimelineItem) bool { return i.Role == "user" && strings.Contains(i.Text, "hello-world") },
		turn + ":r1":   func(i surface.TimelineItem) bool { return i.Kind == "reasoning" && i.Text == "thinking it over" },
		turn + ":plan": func(i surface.TimelineItem) bool {
			return i.Kind == "event" && strings.Contains(i.Text, "[completed] first step")
		},
		turn + ":tool:call-1": func(i surface.TimelineItem) bool {
			return i.Kind == "toolCall" && i.Status == "completed" && i.Title == "Ran echo"
		},
		turn + ":tool:call-1:result": func(i surface.TimelineItem) bool { return i.Kind == "toolResult" && i.Text == "hi" },
		turn + ":a2":                 func(i surface.TimelineItem) bool { return i.Role == "assistant" && i.Text == "hello-world" },
		turn + ":done":               func(i surface.TimelineItem) bool { return i.Kind == "done" && i.Status == "completed" },
	}
	for id, check := range checks {
		if item, ok := items[id]; !ok || !check(item) {
			t.Errorf("item %s = %+v (present %v); all=%v", id, item, ok, keys(items))
		}
	}
}

func keys(items map[string]surface.TimelineItem) []string {
	var out []string
	for id := range items {
		out = append(out, id)
	}
	return out
}

func TestHostPermissionsAnswerOneCallAndNeverWiden(t *testing.T) {
	h := startHost(t, fakeAgent(t, "fake"))
	state := h.create("fake", "", "")
	h.send(state.SessionID, "ask")
	waiting := h.waitFor(state.SessionID, func(s State) bool { return len(s.Requests) == 1 })
	request := waiting.Requests[0]
	if request.Title != "Ran echo" || !strings.Contains(string(request.RawInput), "echo hi") || len(request.Options) != 4 || waiting.Status != StatusBusy {
		t.Fatalf("request=%+v status=%s", request, waiting.Status)
	}
	if _, err := h.client.Respond(context.Background(), RespondParams{SessionID: state.SessionID, RequestID: request.ID, Choice: "always"}); err == nil {
		t.Fatal("an always answer must be refused")
	}
	if result, err := h.client.Respond(context.Background(), RespondParams{SessionID: state.SessionID, RequestID: request.ID, Choice: "allow"}); err != nil || result.Outcome != "allowed" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	done := h.waitFor(state.SessionID, idle)
	if done.Mode != "default" {
		t.Fatalf("allowing one call must not change the mode: %+v", done)
	}
	if _, err := h.client.Respond(context.Background(), RespondParams{SessionID: state.SessionID, RequestID: request.ID, Choice: "allow"}); !isCode(err, CodeStale) {
		t.Fatalf("a second answer must be stale: %v", err)
	}

	h.send(state.SessionID, "noallow")
	waiting = h.waitFor(state.SessionID, func(s State) bool { return len(s.Requests) == 1 })
	if _, err := h.client.Respond(context.Background(), RespondParams{SessionID: state.SessionID, RequestID: waiting.Requests[0].ID, Choice: "allow"}); !isCode(err, CodeInvalid) {
		t.Fatalf("allow without allow_once must be refused: %v", err)
	}
	if _, err := h.client.Respond(context.Background(), RespondParams{SessionID: state.SessionID, RequestID: waiting.Requests[0].ID, Choice: "deny"}); err != nil {
		t.Fatal(err)
	}
	h.waitFor(state.SessionID, idle)
}

func isCode(err error, code string) bool {
	var callErr *CallError
	return errors.As(err, &callErr) && callErr.Code == code
}

func TestHostCancelEndsTheTurnAndStopAnswersCancelled(t *testing.T) {
	h := startHost(t, fakeAgent(t, "fake"))
	state := h.create("fake", "", "")
	turn := h.send(state.SessionID, "slow")
	h.waitFor(state.SessionID, func(s State) bool { return s.Status == StatusBusy })
	if err := h.client.Cancel(context.Background(), state.SessionID); err != nil {
		t.Fatal(err)
	}
	done := h.waitFor(state.SessionID, idle)
	if done.LastStop != "cancelled" || h.items("fake", state.SessionID)[turn+":done"].Status != "interrupted" {
		t.Fatalf("state=%+v", done)
	}
	turn = h.send(state.SessionID, "ask")
	waiting := h.waitFor(state.SessionID, func(s State) bool { return len(s.Requests) == 1 })
	if _, err := h.client.Respond(context.Background(), RespondParams{SessionID: state.SessionID, RequestID: waiting.Requests[0].ID, Choice: "stop"}); err != nil {
		t.Fatal(err)
	}
	if done := h.waitFor(state.SessionID, idle); done.LastStop != "cancelled" {
		t.Fatalf("state=%+v", done)
	}
}

func TestHostRecordsACrashAndLoadsTheSessionAgain(t *testing.T) {
	h := startHost(t, fakeAgent(t, "fake"))
	state := h.create("fake", "", "")
	h.send(state.SessionID, "remember:PINEAPPLE-42")
	h.waitFor(state.SessionID, idle)
	turn := h.send(state.SessionID, "crash")
	h.waitFor(state.SessionID, func(s State) bool { return s.Status == StatusIdle && s.LastTurnID == turn })
	if item := h.items("fake", state.SessionID)[turn+":done"]; item.Status != "failed" {
		t.Fatalf("done=%+v", item)
	}
	recall := h.send(state.SessionID, "recall")
	h.waitFor(state.SessionID, func(s State) bool { return s.Status == StatusIdle && s.LastTurnID == recall })
	if text := h.items("fake", state.SessionID)[recall+":a1"].Text; text != "PINEAPPLE-42" {
		t.Fatalf("recall=%q", text)
	}
	users := 0
	for id := range h.items("fake", state.SessionID) {
		if strings.HasSuffix(id, ":user") {
			users++
		}
	}
	if users != 3 {
		t.Fatalf("the load replay must not be recorded: %d user items", users)
	}
}

func TestHostReopensAfterRestartAndClosesAnOpenTurn(t *testing.T) {
	agent := fakeAgent(t, "fake")
	h := startHost(t, agent)
	state := h.create("fake", "", "")
	turn := h.send(state.SessionID, "slow")
	h.waitFor(state.SessionID, func(s State) bool { return s.Status == StatusBusy })
	s := h.host.heldSession(state.SessionID)
	s.mu.Lock()
	pid := s.state.PID
	s.mu.Unlock()
	h.host.mu.Lock()
	delete(h.host.sessions, state.SessionID)
	h.host.mu.Unlock()
	s.lock.release()
	if pid == 0 {
		t.Fatal("no agent pid recorded")
	}
	next := NewHost(h.home, "test", []Agent{agent}, nil)
	reopened, err := next.open(state.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer next.closeSession(state.SessionID, false, false)
	if reopened.state.TurnID != "" || reopened.state.LastTurnID != turn {
		t.Fatalf("state=%+v", reopened.state)
	}
	if item := h.items("fake", state.SessionID)[turn+":done"]; item.Status != "failed" {
		t.Fatalf("done=%+v", item)
	}
	if second, err := next.store.lock("fake", state.SessionID); err == nil {
		second.release()
		t.Fatal("the reopened session must hold its lock")
	}
}

func TestHostCeilingLimitsModesAndRevertsTheAgentsOwnChange(t *testing.T) {
	h := startHost(t, fakeAgent(t, "fake"))
	if _, err := h.client.Create(context.Background(), CreateParams{SessionID: uuid.NewString(), Agent: "fake", Cwd: t.TempDir(), Ceiling: "plan", Mode: "bypass"}); !isCode(err, "exceeds_ceiling") {
		t.Fatalf("a mode above the ceiling must be refused: %v", err)
	}
	bounded := h.create("fake", "edits", "")
	if bounded.Mode != "default" {
		t.Fatalf("an omitted mode under edits must pick the highest within it: %+v", bounded)
	}
	planned := h.create("fake", "plan", "")
	if planned.Mode != "ask" {
		t.Fatalf("an omitted mode under plan must pick ask: %+v", planned)
	}
	if _, err := h.client.SetOption(context.Background(), OptionParams{SessionID: bounded.SessionID, Category: "mode", Value: "bypass"}); !isCode(err, "exceeds_ceiling") {
		t.Fatalf("raising the mode above the ceiling must be refused: %v", err)
	}
	turn := h.send(bounded.SessionID, "mode:bypass slow")
	done := h.waitFor(bounded.SessionID, func(s State) bool { return s.Status == StatusIdle && s.LastTurnID == turn && s.Mode == "default" })
	if done.LastStop != "cancelled" {
		t.Fatalf("the agent's own rise above the ceiling must be set back and its turn stopped: %+v", done)
	}
	notices := 0
	for _, item := range h.items("fake", bounded.SessionID) {
		if item.Kind == "notice" && strings.Contains(item.Text, "above this session's ceiling") {
			notices++
		}
	}
	if notices != 1 {
		t.Fatalf("notices=%d", notices)
	}
}

func TestHostServesTwoAgentsFromTheirOwnEntries(t *testing.T) {
	h := startHost(t, fakeAgent(t, "fake-a"), fakeAgent(t, "fake-b"))
	a := h.create("fake-a", "", "")
	b := h.create("fake-b", "", "")
	h.send(a.SessionID, "reply:from-a")
	h.send(b.SessionID, "reply:from-b")
	h.waitFor(a.SessionID, idle)
	h.waitFor(b.SessionID, idle)
	for _, s := range []State{a, b} {
		if records, err := DefaultStore(h.home).Records(s.Agent); err != nil || len(records) != 1 || records[0].SessionID != s.SessionID {
			t.Fatalf("records for %s = %+v err=%v", s.Agent, records, err)
		}
	}
}

func TestDrainedHostExitsAfterItsLastSession(t *testing.T) {
	h := startHost(t, fakeAgent(t, "fake"))
	state := h.create("fake", "", "")
	h.send(state.SessionID, "slow")
	h.waitFor(state.SessionID, func(s State) bool { return s.Status == StatusBusy })
	if err := h.client.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.client.Create(context.Background(), CreateParams{SessionID: uuid.NewString(), Agent: "fake", Cwd: t.TempDir()}); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("the main socket must be free once draining: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	select {
	case <-h.served:
		t.Fatal("the host stopped serving while its session was busy")
	default:
	}
	if err := h.client.Cancel(context.Background(), state.SessionID); err != nil {
		t.Fatalf("the draining host must still serve its session: %v", err)
	}
	select {
	case <-h.served:
	case <-time.After(15 * time.Second):
		t.Fatal("the drained host did not exit after its last session")
	}
}

func TestOutcomeLookupFencesAnUnseenKey(t *testing.T) {
	h := startHost(t, fakeAgent(t, "fake"))
	state := h.create("fake", "", "")
	outcome := h.host.lookupOutcome(context.Background(), "late-key")
	if outcome.State != OutcomeFailed {
		t.Fatalf("outcome=%+v", outcome)
	}
	params, _ := json.Marshal(SendParams{SessionID: state.SessionID, Text: "reply:late"})
	if _, err := h.host.dispatch(context.Background(), Call{Method: "send", Key: "late-key", Params: params}); err == nil {
		t.Fatal("a request whose key was fenced must not act")
	}
	if got := h.host.heldSession(state.SessionID).snapshot(); got.LastTurnID != "" || got.Status != StatusIdle {
		t.Fatalf("state=%+v", got)
	}
}

func TestClientForgetsTheSocketOfAHostThatIsGone(t *testing.T) {
	h := startHost(t, fakeAgent(t, "fake"))
	gone := exec.Command("true")
	if err := gone.Run(); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(h.home, ".agenthail", fmt.Sprintf("acp-host.%d.sock", gone.Process.Pid))
	alive := filepath.Join(h.home, ".agenthail", fmt.Sprintf("acp-host.%d.sock", os.Getpid()))
	for _, path := range []string{stale, alive} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.client.Held(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("a socket whose host is gone must be removed: %v", err)
	}
	if _, err := os.Stat(alive); err != nil {
		t.Fatalf("a socket whose process lives must stay: %v", err)
	}
}

func TestHostRefusesACeilingAboveTheAgentsDefaultEvenWhenAskedForIt(t *testing.T) {
	h := startHost(t, fakeAgent(t, "fake"))
	if _, err := h.client.Create(context.Background(), CreateParams{SessionID: uuid.NewString(), Agent: "fake", Cwd: t.TempDir(), Ceiling: "plan", Mode: "default"}); !isCode(err, "exceeds_ceiling") {
		t.Fatalf("the agent's own default above the ceiling must be refused: %v", err)
	}
}

func TestIdleStopNeverEndsATurnThatJustStarted(t *testing.T) {
	previous := idleStop
	idleStop = time.Millisecond
	t.Cleanup(func() { idleStop = previous })
	h := startHost(t, fakeAgent(t, "fake"))
	state := h.create("fake", "", "")
	for i := range 20 {
		turn := h.send(state.SessionID, fmt.Sprintf("reply:turn-%d", i))
		done := h.waitFor(state.SessionID, func(s State) bool { return s.LastTurnID == turn })
		if done.LastStop != "end_turn" {
			t.Fatalf("turn %d ended %q", i, done.LastStop)
		}
	}
}

func TestClosingASessionWaitsForItsAgentToExit(t *testing.T) {
	h := startHost(t, fakeAgent(t, "fake"))
	state := h.create("fake", "", "")
	h.waitFor(state.SessionID, func(s State) bool { return s.Status == StatusIdle })
	pid := h.host.heldSession(state.SessionID).snapshot().PID
	if pid == 0 {
		t.Fatal("no agent pid recorded")
	}
	if !h.host.closeSession(state.SessionID, false, true) {
		t.Fatal("an idle session must close")
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("the agent must be gone before its lock is released: %v", err)
	}
	if lock, err := h.host.store.lock("fake", state.SessionID); err != nil {
		t.Fatalf("the lock must be free once closed: %v", err)
	} else {
		lock.release()
	}
}

func TestAReopenedSessionIsSetBackWithinItsCeilingBeforeItsPrompt(t *testing.T) {
	agent := fakeAgent(t, "fake")
	h := startHost(t, agent)
	state := h.create("fake", "edits", "")
	if state.Mode != "default" {
		t.Fatalf("mode=%s", state.Mode)
	}
	h.host.heldSession(state.SessionID).stopProcess()
	path := reopenAbove(t, agent, state)
	turn := h.send(state.SessionID, "reply:within")
	done := h.waitFor(state.SessionID, func(s State) bool { return s.LastTurnID == turn })
	if done.Mode != "default" {
		t.Fatalf("the reopened session ran in %s", done.Mode)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), `"default"`) {
		t.Fatalf("the agent was not set back: %s", data)
	}
}

func TestOneContestedSessionDoesNotHoldUpOpeningAnother(t *testing.T) {
	previous := handoverWait
	handoverWait = time.Second
	t.Cleanup(func() { handoverWait = previous })
	agent := fakeAgent(t, "fake")
	h := startHost(t, agent)
	contested := h.create("fake", "", "")
	free := h.create("fake", "", "")
	if !h.host.closeSession(free.SessionID, false, false) {
		t.Fatal("close")
	}
	next := NewHost(h.home, "test", []Agent{agent}, nil)
	waited := make(chan error, 1)
	go func() {
		_, err := next.open(contested.SessionID)
		waited <- err
	}()
	time.Sleep(100 * time.Millisecond)
	started := time.Now()
	opened, err := next.open(free.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer next.closeSession(opened.state.SessionID, false, false)
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("opening a free session waited %s behind a contested one", elapsed)
	}
	if err := <-waited; !isCode(err, CodeHeld) {
		t.Fatalf("a session another host keeps must report it is held: %v", err)
	}
}

func reopenAbove(t *testing.T, agent Agent, state State) string {
	t.Helper()
	return editSaved(t, agent, state, func(saved map[string]any) { saved["mode"] = "bypass" })
}

func editSaved(t *testing.T, agent Agent, state State, edit func(map[string]any)) string {
	t.Helper()
	path := filepath.Join(agent.Env["AGENTHAIL_ACPTEST_STATE"], state.AgentSessionID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	edit(saved)
	if data, err = json.Marshal(saved); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAPersonsModeChoiceRepairsASessionThatReopenedTooHigh(t *testing.T) {
	agent := fakeAgent(t, "fake")
	agent.Modes["retired"] = "plan"
	h := startHost(t, agent)
	state := h.create("fake", "edits", "")
	s := h.host.heldSession(state.SessionID)
	s.stopProcess()
	s.mu.Lock()
	s.state.Mode = "retired"
	s.mu.Unlock()
	reopenAbove(t, agent, state)
	changed, err := h.client.SetOption(context.Background(), OptionParams{SessionID: state.SessionID, Category: "mode", Value: "ask"})
	if err != nil || changed.Mode != "ask" {
		t.Fatalf("state=%+v err=%v", changed, err)
	}
}

func TestRecoveryPicksAModeTheAgentStillOffers(t *testing.T) {
	agent := fakeAgent(t, "fake")
	agent.Modes["retired"] = "plan"
	h := startHost(t, agent)
	state := h.create("fake", "edits", "")
	s := h.host.heldSession(state.SessionID)
	s.stopProcess()
	s.mu.Lock()
	s.state.Mode = "retired"
	s.mu.Unlock()
	reopenAbove(t, agent, state)
	turn := h.send(state.SessionID, "reply:recovered")
	done := h.waitFor(state.SessionID, func(s State) bool { return s.LastTurnID == turn })
	if done.Mode != "default" || done.LastStop != "end_turn" {
		t.Fatalf("state=%+v", done)
	}
}

func TestALateCancelForAnEndedTurnLeavesTheNextTurnRunning(t *testing.T) {
	h := startHost(t, fakeAgent(t, "fake"))
	state := h.create("fake", "", "")
	first := h.send(state.SessionID, "reply:first")
	h.waitFor(state.SessionID, func(s State) bool { return s.LastTurnID == first })
	second := h.send(state.SessionID, "slow")
	h.waitFor(state.SessionID, func(s State) bool { return s.Status == StatusBusy && s.TurnID == second })
	s := h.host.heldSession(state.SessionID)
	if err := s.cancelTurn(first, false); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if now := s.snapshot(); now.Status != StatusBusy || now.TurnID != second {
		t.Fatalf("a cancel for an ended turn stopped the next one: %+v", now)
	}
	if err := s.cancelTurn(second, false); err != nil {
		t.Fatal(err)
	}
	if done := h.waitFor(state.SessionID, func(s State) bool { return s.LastTurnID == second }); done.LastStop != "cancelled" {
		t.Fatalf("done=%+v", done)
	}
}

func TestCancellingAnIdleSessionAnswersItsOpenRequests(t *testing.T) {
	h := startHost(t, fakeAgent(t, "fake"))
	state := h.create("fake", "", "")
	s := h.host.heldSession(state.SessionID)
	pending := &pendingRequest{request: Request{ID: "req-idle"}, answer: make(chan permissionAnswer, 1)}
	s.mu.Lock()
	s.pending["req-idle"] = pending
	s.mu.Unlock()
	if err := s.cancel(); err != nil {
		t.Fatal(err)
	}
	select {
	case answer := <-pending.answer:
		if answer.result.Outcome.Outcome != "cancelled" {
			t.Fatalf("answer=%+v", answer.result)
		}
	default:
		t.Fatal("an open request on an idle session must be answered cancelled")
	}
	if len(s.snapshot().Requests) != 0 {
		t.Fatal("the request must be gone")
	}
}

func TestACancelRacingTheNextPromptNeverEndsIt(t *testing.T) {
	h := startHost(t, fakeAgent(t, "fake"))
	state := h.create("fake", "", "")
	s := h.host.heldSession(state.SessionID)
	for i := range 30 {
		turn := h.send(state.SessionID, fmt.Sprintf("reply:race-%d", i))
		go func() { _ = s.cancelTurn(turn, false) }()
		h.waitFor(state.SessionID, func(s State) bool { return s.LastTurnID == turn })
		next := h.send(state.SessionID, "reply:next")
		done := h.waitFor(state.SessionID, func(s State) bool { return s.LastTurnID == next })
		if done.LastStop != "end_turn" {
			t.Fatalf("iteration %d: a cancel for the previous turn ended the next one (%s)", i, done.LastStop)
		}
	}
}

func TestAnInterruptDuringALoadAnswersRequestsAndStopsEverySendAlreadyStarting(t *testing.T) {
	agent := fakeAgent(t, "fake")
	h := startHost(t, agent)
	state := h.create("fake", "", "")
	s := h.host.heldSession(state.SessionID)
	s.stopProcess()
	path := editSaved(t, agent, state, func(saved map[string]any) { saved["loadMs"] = 1500 })
	type sent struct {
		result SendResult
		err    error
	}
	results := make(chan sent, 2)
	for _, text := range []string{"reply:first-before", "reply:second-before"} {
		go func() {
			result, err := h.client.Send(context.Background(), SendParams{SessionID: state.SessionID, Text: text})
			results <- sent{result, err}
		}()
		time.Sleep(150 * time.Millisecond)
	}
	pending := &pendingRequest{request: Request{ID: "req-load"}, answer: make(chan permissionAnswer, 1)}
	s.mu.Lock()
	s.pending["req-load"] = pending
	s.mu.Unlock()
	started := time.Now()
	if err := s.cancel(); err != nil {
		t.Fatal(err)
	}
	if waited := time.Since(started); waited > 500*time.Millisecond {
		t.Fatalf("the interrupt waited %s behind the load", waited)
	}
	select {
	case answer := <-pending.answer:
		if answer.result.Outcome.Outcome != "cancelled" {
			t.Fatalf("answer=%+v", answer.result)
		}
	default:
		t.Fatal("the open request must be answered cancelled")
	}
	for range 2 {
		if got := <-results; !isCode(got.err, CodeInterrupted) {
			t.Fatalf("a message stopped before it reached the agent must be refused as interrupted, not sent: %+v", got)
		}
	}
	for id, item := range h.items("fake", state.SessionID) {
		if strings.Contains(item.Text, "before") {
			t.Fatalf("a message that was never sent must not appear in the transcript: %s %+v", id, item)
		}
	}
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "before") {
		t.Fatalf("a message sent before the interrupt reached the agent: %s", data)
	}
	after := h.send(state.SessionID, "reply:after")
	if done := h.waitFor(state.SessionID, func(s State) bool { return s.LastTurnID == after }); done.LastStop != "end_turn" {
		t.Fatalf("a message after the interrupt must run: %+v", done)
	}
}

func TestAnAgentReopenedWithoutATurnStillStopsWhenIdle(t *testing.T) {
	previous := idleStop
	t.Cleanup(func() { idleStop = previous })
	h := startHost(t, fakeAgent(t, "fake"))
	state := h.create("fake", "", "")
	s := h.host.heldSession(state.SessionID)
	s.stopProcess()
	idleStop = 200 * time.Millisecond
	if _, err := h.client.SetOption(context.Background(), OptionParams{SessionID: state.SessionID, Category: "mode", Value: "ask"}); err != nil {
		t.Fatal(err)
	}
	pid := s.snapshot().PID
	if pid == 0 {
		t.Fatal("the mode change must reopen the agent")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("an agent reopened without a turn never stopped when idle")
}
