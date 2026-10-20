package surfaces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const pickAColor = `{"questions":[{"question":"Pick a color","header":"Color","options":[{"label":"Red","description":"Warm"},{"label":"Blue","description":"Cool"}],"multiSelect":false}]}`

type heldPrompt struct {
	decision *ClaudePromptDecision
	err      error
}

func holdInBackground(claude *Claude, prompt ClaudePermissionPrompt) (<-chan heldPrompt, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan heldPrompt, 1)
	go func() {
		decision, err := claude.HoldPrompt(ctx, prompt)
		done <- heldPrompt{decision, err}
	}()
	return done, cancel
}

func awaitRequests(t *testing.T, claude *Claude, session *surface.Session) []surface.Request {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, err := claude.Requests(context.Background(), session)
		if err != nil {
			t.Fatal(err)
		}
		if len(state.Requests) > 0 {
			return state.Requests
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the prompt was never held")
	return nil
}

func terminalSession(t *testing.T) *surface.Session {
	t.Helper()
	transcript := writeTranscript(t, `{"type":"user","uuid":"prompt","timestamp":"2026-10-18T23:00:27Z","message":{"content":"Ask me which color I prefer"}}`)
	return &surface.Session{ID: "conversation", Bridge: "bridge", Surface: surface.KindClaude, PID: os.Getpid(), Status: surface.StatusBusy, Transcript: transcript}
}

func TestATerminalQuestionHeldByTheHookIsAnsweredThroughAgenthail(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	asked := time.Now().Add(-time.Second).Truncate(time.Millisecond).UTC()
	writeClaudeRecord(t, home, map[string]any{"kind": "interactive", "entrypoint": "cli", "status": "waiting", "waitingFor": "input needed", "statusUpdatedAt": asked.UnixMilli()})
	claude := NewClaude(nil, home)
	var mu sync.Mutex
	var changes []ClaudePromptChange
	claude.OnPromptChange(func(change ClaudePromptChange) {
		mu.Lock()
		changes = append(changes, change)
		mu.Unlock()
	})
	session := terminalSession(t)
	if claude.SessionCapabilities(session).Respond {
		t.Fatal("a session with no held prompt offered answers")
	}
	done, cancel := holdInBackground(claude, ClaudePermissionPrompt{SessionID: "conversation", ToolName: "AskUserQuestion", ToolInput: json.RawMessage(pickAColor), PID: os.Getpid()})
	defer cancel()
	requests := awaitRequests(t, claude, session)
	request := requests[0]
	if request.Kind != surface.WaitingQuestion || len(request.Questions) != 1 || request.Questions[0].Question != "Pick a color" || !request.CreatedAt.Equal(asked) {
		t.Fatalf("request=%+v", request)
	}
	if !claude.SessionCapabilities(session).Respond {
		t.Fatal("a held prompt was not answerable")
	}
	observation, err := claude.Observe(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if observation.WaitingOn == nil || observation.WaitingOn.ID != request.ID || !strings.Contains(request.ID, ":held:") {
		t.Fatalf("the session waits on %+v, not the held request %s", observation.WaitingOn, request.ID)
	}
	if _, err := claude.Respond(context.Background(), session, request.ID, surface.Decision{Behavior: "allow", Answers: map[string]string{"Pick a color": "Blue"}}); err != nil {
		t.Fatal(err)
	}
	held := <-done
	if held.err != nil || held.decision == nil || held.decision.Behavior != "allow" {
		t.Fatalf("held=%+v", held)
	}
	answers, _ := held.decision.UpdatedInput["answers"].(map[string]string)
	if answers["Pick a color"] != "Blue" || held.decision.UpdatedInput["questions"] == nil {
		t.Fatalf("updatedInput=%+v", held.decision.UpdatedInput)
	}
	observation, err = claude.Observe(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if observation.WaitingOn != nil {
		t.Fatalf("the record still showing the answered question made it wait again: %+v", observation.WaitingOn)
	}
	if _, err := claude.Respond(context.Background(), session, request.ID, surface.Decision{Behavior: "deny"}); !errors.Is(err, surface.ErrRequestStale) {
		t.Fatalf("a second answer err=%v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(changes) != 2 || !changes[0].Open || changes[1].Open || !changes[1].Settled || changes[1].RequestID != request.ID {
		t.Fatalf("changes=%+v", changes)
	}
}

func TestAPromptAnsweredInTheTerminalStopsBeingHeld(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	previous := claudePromptSettleEvery
	claudePromptSettleEvery = 10 * time.Millisecond
	t.Cleanup(func() { claudePromptSettleEvery = previous })
	home := t.TempDir()
	asked := time.Now().Add(-time.Second).UnixMilli()
	writeClaudeRecord(t, home, map[string]any{"kind": "interactive", "entrypoint": "cli", "status": "waiting", "waitingFor": "permission prompt", "statusUpdatedAt": asked})
	claude := NewClaude(nil, home)
	session := terminalSession(t)
	done, cancel := holdInBackground(claude, ClaudePermissionPrompt{SessionID: "conversation", ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"ls"}`), PID: os.Getpid()})
	defer cancel()
	request := awaitRequests(t, claude, session)[0]
	writeClaudeRecord(t, home, map[string]any{"kind": "interactive", "entrypoint": "cli", "status": "busy", "statusUpdatedAt": asked + 500})
	select {
	case held := <-done:
		if held.err != nil || held.decision != nil {
			t.Fatalf("held=%+v", held)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the prompt stayed held after the terminal answered it")
	}
	if _, err := claude.Respond(context.Background(), session, request.ID, surface.Decision{Behavior: "allow"}); !errors.Is(err, surface.ErrRequestStale) {
		t.Fatalf("a late answer err=%v", err)
	}
}

func TestAlwaysAllowAppliesTheRulesClaudeSuggested(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	writeClaudeRecord(t, home, map[string]any{"kind": "interactive", "entrypoint": "cli", "status": "waiting", "waitingFor": "permission prompt", "statusUpdatedAt": time.Now().UnixMilli()})
	claude := NewClaude(nil, home)
	session := terminalSession(t)
	suggestions := json.RawMessage(`[{"type":"addRules","rules":[{"toolName":"Bash","ruleContent":"ls"}],"behavior":"allow","destination":"localSettings"}]`)
	done, cancel := holdInBackground(claude, ClaudePermissionPrompt{SessionID: "conversation", ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"ls"}`), Suggestions: suggestions, PID: os.Getpid()})
	defer cancel()
	request := awaitRequests(t, claude, session)[0]
	if request.Kind != surface.WaitingApproval || !request.AlwaysAllow || !strings.Contains(string(request.Input), `"ls"`) {
		t.Fatalf("request=%+v", request)
	}
	if outcome, err := claude.Respond(context.Background(), session, request.ID, surface.Decision{Behavior: "allow", Always: true}); err != nil || outcome != "allowed_always" {
		t.Fatalf("outcome=%q err=%v", outcome, err)
	}
	held := <-done
	if held.decision == nil || string(held.decision.UpdatedPermissions) != string(suggestions) || held.decision.UpdatedInput["command"] != "ls" {
		t.Fatalf("decision=%+v", held.decision)
	}
}

func TestOnlyATerminalSessionsOwnProcessHasItsPromptHeld(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	for _, test := range []struct {
		name   string
		fields map[string]any
		pid    int
	}{
		{name: "background job", fields: map[string]any{"kind": "bg", "entrypoint": "cli"}, pid: os.Getpid()},
		{name: "SDK host", fields: map[string]any{"kind": "interactive", "entrypoint": "sdk-ts"}, pid: os.Getpid()},
		{name: "another process", fields: map[string]any{"kind": "interactive", "entrypoint": "cli"}, pid: os.Getpid() + 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			test.fields["status"] = "waiting"
			test.fields["waitingFor"] = "permission prompt"
			writeClaudeRecord(t, home, test.fields)
			claude := NewClaude(nil, home)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := claude.HoldPrompt(ctx, ClaudePermissionPrompt{SessionID: "conversation", ToolName: "Bash", PID: test.pid}); !errors.Is(err, ErrPromptNotHeld) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestTheSamePromptPassedByTwoHooksIsOneRequestAndBothGetTheAnswer(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	writeClaudeRecord(t, home, map[string]any{"kind": "interactive", "entrypoint": "cli", "status": "waiting", "waitingFor": "input needed", "statusUpdatedAt": time.Now().UnixMilli()})
	claude := NewClaude(nil, home)
	var mu sync.Mutex
	var changes []ClaudePromptChange
	claude.OnPromptChange(func(change ClaudePromptChange) {
		mu.Lock()
		changes = append(changes, change)
		mu.Unlock()
	})
	session := terminalSession(t)
	prompt := ClaudePermissionPrompt{SessionID: "conversation", ToolName: "AskUserQuestion", ToolInput: json.RawMessage(pickAColor), PID: os.Getpid()}
	first, cancelFirst := holdInBackground(claude, prompt)
	defer cancelFirst()
	second, cancelSecond := holdInBackground(claude, prompt)
	defer cancelSecond()
	requests := awaitRequests(t, claude, session)
	deadline := time.Now().Add(5 * time.Second)
	for {
		claude.prompts.mu.Lock()
		holders := 0
		if open := claude.prompts.open["conversation"]; len(open) == 1 {
			holders = open[0].holders
		}
		claude.prompts.mu.Unlock()
		if holders == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the second hook call did not join the first hold")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if state, _ := claude.Requests(context.Background(), session); len(state.Requests) != 1 {
		t.Fatalf("requests=%+v", state.Requests)
	}
	if _, err := claude.Respond(context.Background(), session, requests[0].ID, surface.Decision{Behavior: "allow", Answers: map[string]string{"Pick a color": "Red"}}); err != nil {
		t.Fatal(err)
	}
	for _, done := range []<-chan heldPrompt{first, second} {
		held := <-done
		if held.err != nil || held.decision == nil || held.decision.UpdatedInput["answers"].(map[string]string)["Pick a color"] != "Red" {
			t.Fatalf("held=%+v", held)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(changes) != 2 || !changes[0].Open || !changes[1].Settled {
		t.Fatalf("changes=%+v", changes)
	}
}

func TestAHookThatStopsWaitingLeavesTheHoldToTheOtherCall(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	writeClaudeRecord(t, home, map[string]any{"kind": "interactive", "entrypoint": "cli", "status": "waiting", "waitingFor": "permission prompt", "statusUpdatedAt": time.Now().UnixMilli()})
	claude := NewClaude(nil, home)
	session := terminalSession(t)
	prompt := ClaudePermissionPrompt{SessionID: "conversation", ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"ls"}`), PID: os.Getpid()}
	first, cancelFirst := holdInBackground(claude, prompt)
	second, cancelSecond := holdInBackground(claude, prompt)
	defer cancelSecond()
	request := awaitRequests(t, claude, session)[0]
	time.Sleep(50 * time.Millisecond)
	cancelFirst()
	if held := <-first; !errors.Is(held.err, context.Canceled) {
		t.Fatalf("first=%+v", held)
	}
	if state, _ := claude.Requests(context.Background(), session); len(state.Requests) != 1 {
		t.Fatal("the hold ended while another call still held it")
	}
	if _, err := claude.Respond(context.Background(), session, request.ID, surface.Decision{Behavior: "deny"}); err != nil {
		t.Fatal(err)
	}
	if held := <-second; held.decision == nil || held.decision.Behavior != "deny" {
		t.Fatalf("second=%+v", held)
	}
}

func TestALateDuplicateTakesTheAnswerButTheSamePromptAskedAgainIsHeldAgain(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	asked := time.Now().Add(-time.Second).UnixMilli()
	writeClaudeRecord(t, home, map[string]any{"kind": "interactive", "entrypoint": "cli", "status": "waiting", "waitingFor": "permission prompt", "statusUpdatedAt": asked})
	claude := NewClaude(nil, home)
	var mu sync.Mutex
	opened := 0
	claude.OnPromptChange(func(change ClaudePromptChange) {
		mu.Lock()
		if change.Open {
			opened++
		}
		mu.Unlock()
	})
	session := terminalSession(t)
	prompt := ClaudePermissionPrompt{SessionID: "conversation", ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"rm -rf build"}`), PID: os.Getpid()}
	first, cancel := holdInBackground(claude, prompt)
	defer cancel()
	request := awaitRequests(t, claude, session)[0]
	if _, err := claude.Respond(context.Background(), session, request.ID, surface.Decision{Behavior: "allow"}); err != nil {
		t.Fatal(err)
	}
	if held := <-first; held.decision == nil || held.decision.Behavior != "allow" {
		t.Fatalf("first=%+v", held)
	}

	late, err := claude.HoldPrompt(context.Background(), prompt)
	if err != nil || late == nil || late.Behavior != "allow" {
		t.Fatalf("a late duplicate of the answered prompt got %+v, %v", late, err)
	}
	mu.Lock()
	if opened != 1 {
		t.Fatalf("the late duplicate opened the prompt again: %d opens", opened)
	}
	mu.Unlock()

	writeClaudeRecord(t, home, map[string]any{"kind": "interactive", "entrypoint": "cli", "status": "waiting", "waitingFor": "permission prompt", "statusUpdatedAt": asked + 3000})
	again, cancelAgain := holdInBackground(claude, prompt)
	defer cancelAgain()
	if requests := awaitRequests(t, claude, session); requests[0].ID == request.ID {
		t.Fatal("the prompt asked again reused the answered request")
	}
	select {
	case held := <-again:
		t.Fatalf("the prompt asked again was answered without a person: %+v", held)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestAPromptTheRecordNeverShowsIsNotHeld(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	writeClaudeRecord(t, home, map[string]any{"kind": "interactive", "entrypoint": "cli", "status": "busy", "statusUpdatedAt": time.Now().UnixMilli()})
	claude := NewClaude(nil, home)
	opened := false
	claude.OnPromptChange(func(change ClaudePromptChange) { opened = opened || change.Open })
	decision, err := claude.HoldPrompt(context.Background(), ClaudePermissionPrompt{SessionID: "conversation", ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"ls"}`), PID: os.Getpid()})
	if decision != nil || err != nil || opened || claude.SessionCapabilities(terminalSession(t)).Respond {
		t.Fatalf("decision=%+v err=%v opened=%v", decision, err, opened)
	}
}

func TestAnEndedHoldIsForgottenAfterTheDuplicateWindow(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	writeClaudeRecord(t, home, map[string]any{"kind": "interactive", "entrypoint": "cli", "status": "waiting", "waitingFor": "permission prompt", "statusUpdatedAt": time.Now().UnixMilli()})
	claude := NewClaude(nil, home)
	claude.prompts.forgetAfter = 50 * time.Millisecond
	session := terminalSession(t)
	done, cancel := holdInBackground(claude, ClaudePermissionPrompt{SessionID: "conversation", ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"ls"}`), PID: os.Getpid()})
	defer cancel()
	request := awaitRequests(t, claude, session)[0]
	if _, err := claude.Respond(context.Background(), session, request.ID, surface.Decision{Behavior: "deny"}); err != nil {
		t.Fatal(err)
	}
	<-done
	deadline := time.Now().Add(5 * time.Second)
	for {
		claude.prompts.mu.Lock()
		remembered := len(claude.prompts.ended)
		claude.prompts.mu.Unlock()
		if remembered == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("an ended hold is still remembered with no later hook call: %d sessions", remembered)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAnAnswerIsForgottenWhenItsSessionExits(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	writeClaudeRecord(t, home, map[string]any{"kind": "interactive", "entrypoint": "cli", "status": "waiting", "waitingFor": "permission prompt", "statusUpdatedAt": time.Now().UnixMilli()})
	claude := NewClaude(nil, home)
	session := terminalSession(t)
	done, cancel := holdInBackground(claude, ClaudePermissionPrompt{SessionID: "conversation", ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"ls"}`), PID: os.Getpid()})
	defer cancel()
	request := awaitRequests(t, claude, session)[0]
	if _, err := claude.Respond(context.Background(), session, request.ID, surface.Decision{Behavior: "deny"}); err != nil {
		t.Fatal(err)
	}
	<-done
	claude.prompts.mu.Lock()
	_, remembered := claude.prompts.answered["conversation"]
	claude.prompts.mu.Unlock()
	if !remembered {
		t.Fatal("the answer is not remembered while the record still shows the prompt")
	}
	if err := os.Remove(filepath.Join(home, ".claude", "sessions", fmt.Sprint(os.Getpid())+".json")); err != nil {
		t.Fatal(err)
	}
	if _, err := claude.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	claude.prompts.mu.Lock()
	defer claude.prompts.mu.Unlock()
	if len(claude.prompts.answered) != 0 {
		t.Fatalf("an exited session's answer is still remembered: %v", claude.prompts.answered)
	}
}
