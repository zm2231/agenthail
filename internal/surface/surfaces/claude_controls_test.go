package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

// fakeRemoteControl serves the claude.ai Remote Control endpoints a control
// uses: the session state, the event log read by after_id, and event posts.
// onPost answers a posted payload by appending events or writing the transcript.
type fakeRemoteControl struct {
	t         *testing.T
	mu        sync.Mutex
	connected bool
	events    []map[string]any
	posts     []map[string]any
	onPost    func(payload map[string]any) []map[string]any
}

func newFakeRemoteControl(t *testing.T) *fakeRemoteControl {
	return &fakeRemoteControl{t: t, connected: true}
}

func (f *fakeRemoteControl) request(_ context.Context, method, url string, _ map[string]string, body string, _ CookieSource, _ time.Duration) (int, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case method == "GET" && strings.HasSuffix(url, "/v1/code/sessions/cse_test"):
		status := "disconnected"
		if f.connected {
			status = "connected"
		}
		return 200, fmt.Sprintf(`{"response_shape":{"id":"cse_test","connection_status":%q,"external_metadata":{"model":"model-live"}}}`, status), nil
	case method == "GET" && strings.Contains(url, "/v1/sessions/session_test/events?"):
		after := url[strings.Index(url, "after_id=")+len("after_id="):]
		start := 0
		for i, event := range f.events {
			if event["uuid"] == after {
				start = i + 1
			}
		}
		page := f.events[start:]
		last := ""
		if len(page) > 0 {
			last, _ = page[len(page)-1]["uuid"].(string)
		}
		encoded, _ := json.Marshal(map[string]any{"data": page, "last_id": last, "has_more": false})
		return 200, string(encoded), nil
	case method == "POST" && strings.HasSuffix(url, "/v1/code/sessions/cse_test/events"):
		var envelope struct {
			Events []struct {
				Payload map[string]any `json:"payload"`
			} `json:"events"`
		}
		if err := json.Unmarshal([]byte(body), &envelope); err != nil || len(envelope.Events) != 1 {
			f.t.Fatalf("post body=%s err=%v", body, err)
		}
		payload := envelope.Events[0].Payload
		f.posts = append(f.posts, payload)
		f.events = append(f.events, payload)
		if f.onPost != nil {
			f.events = append(f.events, f.onPost(payload)...)
		}
		return 200, `{}`, nil
	}
	f.t.Fatalf("unexpected %s %s", method, url)
	return 0, "", nil
}

func (f *fakeRemoteControl) postCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.posts)
}

func controlResponse(payload map[string]any, subtype, failure string) map[string]any {
	return map[string]any{"type": "control_response", "uuid": newUUID(), "response": map[string]any{"request_id": payload["request_id"], "subtype": subtype, "error": failure}}
}

func shortControlPoll(t *testing.T) {
	previous := claudeControlPoll
	claudeControlPoll = 5 * time.Millisecond
	t.Cleanup(func() { claudeControlPoll = previous })
}

func appendTranscript(t *testing.T, path, lines string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(lines); err != nil {
		t.Fatal(err)
	}
}

func busyControlSession(t *testing.T) (*claudetest.Fixture, *surface.Session) {
	fixture := claudetest.New(t)
	fixture.RecordStatus("conversation-test", fixture.Home, "session_test", "busy", "", time.Time{})
	path := writeTranscript(t, `{"type":"user","uuid":"turn-active","message":{"content":"working"}}`)
	return fixture, &surface.Session{ID: "conversation-test", Surface: surface.KindClaude, Bridge: "session_test", PID: fixture.PID, Status: surface.StatusBusy, Transcript: path}
}

func TestClaudeModelSwitchWaitsForItsOwnControlResponse(t *testing.T) {
	shortControlPoll(t)
	fixture, session := busyControlSession(t)
	remote := newFakeRemoteControl(t)
	remote.onPost = func(payload map[string]any) []map[string]any {
		return []map[string]any{
			{"type": "control_response", "uuid": newUUID(), "response": map[string]any{"request_id": "someone-else", "subtype": "error", "error": "unrelated"}},
			controlResponse(payload, "success", ""),
		}
	}
	claude := NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, fixture.Home, remote.request)
	result, err := claude.Control(context.Background(), session, surface.ControlRequest{Action: "model", Value: "model-next"})
	if err != nil || result.Evidence != surface.EvidenceDelivered || result.Output != "model-next" || result.RequestID == "" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	request, _ := remote.posts[0]["request"].(map[string]any)
	if remote.posts[0]["type"] != "control_request" || remote.posts[0]["request_id"] != result.RequestID || request["subtype"] != "set_model" || request["model"] != "model-next" {
		t.Fatalf("posted=%v", remote.posts[0])
	}
}

func TestClaudeModelSwitchReportsRejectionAndSilenceDistinctly(t *testing.T) {
	shortControlPoll(t)
	fixture, session := busyControlSession(t)
	remote := newFakeRemoteControl(t)
	remote.onPost = func(payload map[string]any) []map[string]any {
		return []map[string]any{controlResponse(payload, "error", "unknown model")}
	}
	claude := NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, fixture.Home, remote.request)
	result, err := claude.Control(context.Background(), session, surface.ControlRequest{Action: "model", Value: "nope"})
	if err == nil || result.Evidence != surface.EvidenceFailed || !strings.Contains(err.Error(), "unknown model") {
		t.Fatalf("rejected result=%+v err=%v", result, err)
	}

	remote.onPost = nil
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	result, err = claude.Control(ctx, session, surface.ControlRequest{Action: "model", Value: "model-next"})
	if !surface.IsDeliveryOutcomeUnknown(err) || result.Evidence != surface.EvidenceUnknown || result.RequestID == "" {
		t.Fatalf("silent result=%+v err=%v", result, err)
	}
}

func TestClaudeControlsRefuseADisconnectedRemoteControlBeforePosting(t *testing.T) {
	fixture, session := busyControlSession(t)
	remote := newFakeRemoteControl(t)
	remote.connected = false
	claude := NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, fixture.Home, remote.request)
	for _, control := range []surface.ControlRequest{{Action: "interrupt"}, {Action: "steer", Value: "focus"}, {Action: "model", Value: "model-next"}, {Action: "context"}} {
		if _, err := claude.Control(context.Background(), session, control); err == nil || !surface.IsDeliveryUnavailable(err) {
			t.Fatalf("%s err=%v", control.Action, err)
		}
	}
	if remote.postCount() != 0 {
		t.Fatalf("posted %d events to a disconnected session", remote.postCount())
	}
}

func TestClaudeInterruptConfirmsTheActiveTurnEnded(t *testing.T) {
	shortControlPoll(t)
	fixture, session := busyControlSession(t)
	remote := newFakeRemoteControl(t)
	remote.onPost = func(payload map[string]any) []map[string]any {
		fixture.RecordStatus("conversation-test", fixture.Home, "session_test", "idle", "", time.Time{})
		appendTranscript(t, session.Transcript, `{"type":"user","uuid":"marker","message":{"content":"[Request interrupted by user]"}}`+"\n")
		return []map[string]any{controlResponse(payload, "success", "")}
	}
	claude := NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, fixture.Home, remote.request)
	result, err := claude.Control(context.Background(), session, surface.ControlRequest{Action: "interrupt"})
	if err != nil || result.Evidence != surface.EvidenceDelivered || result.TurnID != "turn-active" || result.RequestID != remote.posts[0]["request_id"] {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestClaudeInterruptAcknowledgedWithoutTheTurnEndingIsUnknown(t *testing.T) {
	shortControlPoll(t)
	fixture, session := busyControlSession(t)
	remote := newFakeRemoteControl(t)
	remote.onPost = func(payload map[string]any) []map[string]any {
		return []map[string]any{controlResponse(payload, "success", "")}
	}
	claude := NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, fixture.Home, remote.request)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	result, err := claude.Control(ctx, session, surface.ControlRequest{Action: "interrupt"})
	if !surface.IsDeliveryOutcomeUnknown(err) || result.Evidence != surface.EvidenceUnknown || result.RequestID == "" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestClaudeSteerOverRemoteControlIsOnlyTransportAccepted(t *testing.T) {
	fixture, session := busyControlSession(t)
	remote := newFakeRemoteControl(t)
	claude := NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, fixture.Home, remote.request)
	result, err := surface.SteerWithEvidence(context.Background(), claude, session, "focus on the parser")
	if err != nil || result.Evidence != surface.EvidenceTransportAccepted || result.RequestID == "" || result.RequestID != remote.posts[0]["uuid"] {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestClaudeCommandOutputIsJoinedToItsRequest(t *testing.T) {
	shortControlPoll(t)
	fixture, session := busyControlSession(t)
	remote := newFakeRemoteControl(t)
	remote.onPost = func(payload map[string]any) []map[string]any {
		appendTranscript(t, session.Transcript,
			`{"type":"system","subtype":"local_command","uuid":"stale","parentUuid":"earlier","commandRun":{"command":"context","args":""},"content":"<local-command-stdout>stale usage</local-command-stdout>"}`+"\n"+
				fmt.Sprintf(`{"type":"system","subtype":"local_command","uuid":"fresh","parentUuid":%q,"commandRun":{"command":"context","args":""},"content":"<local-command-stdout>\u001b[1mContext Usage\u001b[22m 12%%</local-command-stdout>"}`, payload["uuid"])+"\n")
		return nil
	}
	claude := NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, fixture.Home, remote.request)
	result, err := claude.Control(context.Background(), session, surface.ControlRequest{Action: "context"})
	if err != nil || result.Evidence != surface.EvidenceReplyObserved || result.Output != "Context Usage 12%" || result.RequestID != remote.posts[0]["uuid"] {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestClaudeCommandAfterACompetingPromptIsUnknown(t *testing.T) {
	shortControlPoll(t)
	fixture, session := busyControlSession(t)
	remote := newFakeRemoteControl(t)
	remote.onPost = func(map[string]any) []map[string]any {
		return []map[string]any{{"type": "user", "uuid": "another-prompt"}}
	}
	claude := NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, fixture.Home, remote.request)
	result, err := claude.Control(context.Background(), session, surface.ControlRequest{Action: "usage"})
	if !surface.IsDeliveryOutcomeUnknown(err) || result.Evidence != surface.EvidenceUnknown {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestClaudeControlValuesAreValidatedBeforeAnyRequest(t *testing.T) {
	claude := NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, t.TempDir(), func(context.Context, string, string, map[string]string, string, CookieSource, time.Duration) (int, string, error) {
		t.Fatal("invalid control reached claude.ai")
		return 0, "", nil
	})
	session := &surface.Session{ID: "conversation-test", Bridge: "session_test"}
	for _, control := range []surface.ControlRequest{{Action: "effort", Value: "extreme"}, {Action: "rename"}, {Action: "rename", Value: "two\nlines"}, {Action: "context", Value: "extra"}, {Action: "steer", Value: "/clear"}, {Action: "model", Value: "two words"}, {Action: "clear"}} {
		if _, err := claude.Control(context.Background(), session, control); err == nil {
			t.Fatalf("%+v accepted", control)
		}
	}
}

// connectedRemoteControl answers the Remote Control state read for
// session_test as connected and passes every other request to next.
func connectedRemoteControl(next ClaudeRequest) ClaudeRequest {
	return func(ctx context.Context, method, url string, headers map[string]string, body string, cookies CookieSource, timeout time.Duration) (int, string, error) {
		if method == "GET" && strings.HasSuffix(url, "/v1/code/sessions/cse_test") {
			return 200, `{"response_shape":{"id":"cse_test","connection_status":"connected","external_metadata":{}}}`, nil
		}
		return next(ctx, method, url, headers, body, cookies, timeout)
	}
}

func TestClaudeInterruptWithoutItsControlResponseIsNotReportedAsDone(t *testing.T) {
	shortControlPoll(t)
	fixture, session := busyControlSession(t)
	remote := newFakeRemoteControl(t)
	claude := NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, fixture.Home, remote.request)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := claude.Interrupt(ctx, session); !surface.IsDeliveryOutcomeUnknown(err) {
		t.Fatalf("an unanswered interrupt returned %v", err)
	}
	if remote.postCount() != 1 {
		t.Fatalf("posted %d interrupts", remote.postCount())
	}
}
