package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/zm2231/agenthail/internal/signin"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

// sendToReadyClaude delivers one message through the public Send to a session
// whose transcript proves the previous turn completed, with the HTTP exchange
// answered by respond.
func sendToReadyClaude(t *testing.T, respond ClaudeRequest) error {
	t.Helper()
	path := writeTranscript(t, `
{"type":"user","uuid":"u1","message":{"content":"one"}}
{"type":"assistant","uuid":"a1","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"answer"}]}}
{"type":"system","subtype":"stop_hook_summary"}`)
	home, pid := liveBridgeProcess(t)
	claude := NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, home, respond)
	_, err := claude.Send(context.Background(), &surface.Session{ID: "conversation-test", Bridge: "session_test", PID: pid, Status: surface.StatusIdle, Transcript: path}, "message")
	return err
}

// liveBridgeProcess records a live process that owns session_test through
// Remote Control.
func liveBridgeProcess(t *testing.T) (string, int) {
	t.Helper()
	fixture := claudetest.New(t)
	fixture.Record("conversation-test", fixture.Home, "session_test")
	return fixture.Home, fixture.PID
}

func TestClaudeSendClassifiesDeliveryOutcome(t *testing.T) {
	for _, test := range []struct {
		name     string
		status   int
		body     string
		err      error
		terminal surface.DeliveryTerminalKind
	}{
		{name: "transport failure after dispatch", err: context.DeadlineExceeded},
		{name: "server error", status: http.StatusInternalServerError, body: "upstream failed"},
		{name: "challenge page", status: http.StatusOK, body: "Just a moment"},
		{name: "rate limited", status: http.StatusTooManyRequests, body: "try later"},
		{name: "bad request", status: http.StatusBadRequest, body: "rejected", terminal: surface.DeliveryInvalidRequest},
		{name: "unauthorized", status: http.StatusUnauthorized, body: "rejected", terminal: surface.DeliveryAuthenticationNeeded},
		{name: "forbidden", status: http.StatusForbidden, body: "rejected", terminal: surface.DeliveryAccessDenied},
		{name: "not found", status: http.StatusNotFound, body: "rejected", terminal: surface.DeliveryTargetMissing},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := sendToReadyClaude(t, func(context.Context, string, string, map[string]string, string, CookieSource, time.Duration) (int, string, error) {
				return test.status, test.body, test.err
			})
			if test.terminal == "" {
				if !surface.IsDeliveryOutcomeUnknown(err) || surface.IsDeliveryTerminal(err) {
					t.Fatalf("err=%v, want unknown outcome", err)
				}
				return
			}
			if !surface.IsDeliveryTerminal(err) || surface.IsDeliveryOutcomeUnknown(err) || surface.DeliveryTerminalReason(err) != test.terminal {
				t.Fatalf("err=%v reason=%q, want terminal %q", err, surface.DeliveryTerminalReason(err), test.terminal)
			}
		})
	}
}

func TestClaudeSendUsesTranscriptReadiness(t *testing.T) {
	calls := 0
	home, pid := liveBridgeProcess(t)
	claude := NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, home, func(context.Context, string, string, map[string]string, string, CookieSource, time.Duration) (int, string, error) {
		calls++
		return 200, `{}`, nil
	})
	recent := writeTranscript(t, `
{"type":"user","uuid":"u0","timestamp":"2026-07-19T01:00:00Z","message":{"content":"previous"}}
{"type":"assistant","uuid":"a0","timestamp":"2026-07-19T01:00:01Z","message":{"id":"m0","stop_reason":"end_turn","content":[{"type":"text","text":"previous answer"}]}}
{"type":"system","subtype":"stop_hook_summary"}`)
	newActivity := time.Date(2026, 7, 19, 1, 5, 0, 0, time.UTC)
	result, err := claude.Send(context.Background(), &surface.Session{ID: "conversation-test", Bridge: "session_test", PID: pid, Status: surface.StatusBusy, Transcript: recent, LastActive: newActivity}, "racing")
	if err != nil || result.Accepted || calls != 0 {
		t.Fatalf("recent result=%+v calls=%d err=%v", result, calls, err)
	}

	completed := writeTranscript(t, `
{"type":"user","uuid":"u1","timestamp":"2026-07-19T01:00:00Z","message":{"content":"one"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-07-19T01:00:01Z","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"answer"}]}}
{"type":"system","subtype":"stop_hook_summary"}`)
	writeJitter := time.Date(2026, 7, 19, 1, 0, 1, 9_000_000, time.UTC)
	result, err = claude.Send(context.Background(), &surface.Session{ID: "conversation-test", Bridge: "session_test", PID: pid, Status: surface.StatusBusy, Transcript: completed, LastActive: writeJitter}, "next")
	if err != nil || !result.Accepted || calls != 1 {
		t.Fatalf("completed result=%+v calls=%d err=%v", result, calls, err)
	}

	active := writeTranscript(t, `{"type":"user","uuid":"u2","message":{"content":"running"}}`)
	result, err = claude.Send(context.Background(), &surface.Session{ID: "conversation-test", Bridge: "session_test", PID: pid, Status: surface.StatusIdle, Transcript: active}, "too soon")
	if err != nil || result.Accepted || calls != 1 {
		t.Fatalf("active result=%+v calls=%d err=%v", result, calls, err)
	}

	result, err = claude.Send(context.Background(), &surface.Session{ID: "conversation-test", Bridge: "session_test", PID: pid, Status: surface.StatusIdle}, "unproven")
	if err != nil || result.Accepted || calls != 1 {
		t.Fatalf("missing transcript result=%+v calls=%d err=%v", result, calls, err)
	}
}

func TestClaudeSendHumanTurnUsesAuthenticatedBridgeForUDSSession(t *testing.T) {
	transcript := writeTranscript(t, `
{"type":"user","uuid":"u1","timestamp":"2026-07-19T01:00:00Z","message":{"content":"previous"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-07-19T01:00:01Z","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"answer"}]}}
{"type":"system","subtype":"stop_hook_summary"}`)
	var requestBody, requestURL string
	home, pid := liveHumanTurnProcess(t)
	claude := NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, home, func(_ context.Context, method, url string, _ map[string]string, body string, _ CookieSource, _ time.Duration) (int, string, error) {
		if method != http.MethodPost {
			t.Fatalf("method=%s", method)
		}
		requestURL, requestBody = url, body
		return http.StatusOK, `{}`, nil
	})
	session := &surface.Session{ID: "conversation-bridge", Bridge: "session_bridge", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: pid, Transport: "uds", Status: surface.StatusIdle, Transcript: transcript}
	result, err := claude.SendHumanTurn(context.Background(), session, "Please inspect the failure.")
	if err != nil || result == nil || !result.Accepted {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if !strings.Contains(requestURL, "/v1/code/sessions/cse_bridge/events") {
		t.Fatalf("url=%q", requestURL)
	}
	var envelope struct {
		Events []struct {
			Payload struct {
				Type    string `json:"type"`
				Session string `json:"session_id"`
				Message struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"message"`
			} `json:"payload"`
		} `json:"events"`
	}
	if err := json.Unmarshal([]byte(requestBody), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Events) != 1 || envelope.Events[0].Payload.Type != "user" || envelope.Events[0].Payload.Session != "session_bridge" || envelope.Events[0].Payload.Message.Role != "user" || envelope.Events[0].Payload.Message.Content != "Please inspect the failure." {
		t.Fatalf("envelope=%s", requestBody)
	}
}

func TestClaudeSendHumanTurnRejectsNativeOnlySession(t *testing.T) {
	claude := NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, t.TempDir(), func(context.Context, string, string, map[string]string, string, CookieSource, time.Duration) (int, string, error) {
		t.Fatal("human turn attempted a web request without a bridge identity")
		return 0, "", nil
	})
	_, err := claude.SendHumanTurn(context.Background(), &surface.Session{ID: "native-session", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Transport: "uds", Status: surface.StatusIdle}, "hello")
	if !surface.IsDeliveryUnavailable(err) {
		t.Fatalf("err=%v", err)
	}
}

func TestClaudeSendHumanTurnUsesBridgeOnlySessionID(t *testing.T) {
	transcript := writeTranscript(t, `
{"type":"user","uuid":"u1","timestamp":"2026-07-19T01:00:00Z","message":{"content":"previous"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-07-19T01:00:01Z","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"answer"}]}}
{"type":"system","subtype":"stop_hook_summary"}`)
	var requestURL string
	home, pid := liveHumanTurnProcess(t)
	claude := NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, home, func(_ context.Context, _ string, url string, _ map[string]string, _ string, _ CookieSource, _ time.Duration) (int, string, error) {
		requestURL = url
		return http.StatusOK, `{}`, nil
	})
	result, err := claude.SendHumanTurn(context.Background(), &surface.Session{ID: "conversation-bridge", Bridge: "session_bridge", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: pid, Transport: "remote-control", Status: surface.StatusIdle, Transcript: transcript}, "continue")
	if err != nil || result == nil || !result.Accepted {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if !strings.Contains(requestURL, "/v1/code/sessions/cse_bridge/events") {
		t.Fatalf("url=%q", requestURL)
	}
}

func TestClaudeSendHumanTurnRetainsIdleGate(t *testing.T) {
	home, pid := liveHumanTurnProcess(t)
	claude := NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, home, func(context.Context, string, string, map[string]string, string, CookieSource, time.Duration) (int, string, error) {
		t.Fatal("human turn bypassed the active-turn gate")
		return 0, "", nil
	})
	active := writeTranscript(t, `{"type":"user","uuid":"u2","message":{"content":"running"}}`)
	result, err := claude.SendHumanTurn(context.Background(), &surface.Session{ID: "conversation-bridge", Bridge: "session_bridge", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: pid, Transport: "uds", Status: surface.StatusIdle, Transcript: active}, "wait")
	if err != nil || result == nil || result.Accepted {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

// liveHumanTurnProcess records a live process that owns session_bridge through
// Remote Control and a messaging socket.
func liveHumanTurnProcess(t *testing.T) (string, int) {
	t.Helper()
	fixture := claudetest.New(t)
	fixture.Listen()
	fixture.Record("conversation-bridge", fixture.Home, "session_bridge")
	return fixture.Home, fixture.PID
}

func TestClaudeCompactPostsRemoteSlashCommandAndConfirmsBoundary(t *testing.T) {
	path := writeTranscript(t, `{"type":"user","message":{"content":"ready"}}`)
	var body string
	home, pid := liveBridgeProcess(t)
	claude := NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, home, func(_ context.Context, method, url string, _ map[string]string, requestBody string, _ CookieSource, _ time.Duration) (int, string, error) {
		if method != "POST" || !strings.Contains(url, "/v1/code/sessions/") {
			t.Fatalf("method=%s url=%s", method, url)
		}
		body = requestBody
		var envelope struct {
			Events []struct {
				Payload struct {
					UUID string `json:"uuid"`
				} `json:"payload"`
			} `json:"events"`
		}
		if err := json.Unmarshal([]byte(requestBody), &envelope); err != nil || len(envelope.Events) != 1 || envelope.Events[0].Payload.UUID == "" {
			t.Fatalf("request body=%s err=%v", requestBody, err)
		}
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Fprintf(f, "\n{\"type\":\"user\",\"uuid\":%q,\"message\":{\"content\":\"/compact\"}}\n{\"type\":\"system\",\"subtype\":\"compact_boundary\",\"content\":\"done\"}\n", envelope.Events[0].Payload.UUID); err != nil {
			f.Close()
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		return 200, `{}`, nil
	})
	if err := claude.Compact(context.Background(), &surface.Session{ID: "conversation-test", Bridge: "session_test", PID: pid, Transcript: path}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `"content":"/compact"`) {
		t.Fatalf("body=%s", body)
	}
}

func TestClaudeCompactDoesNotAcceptBoundaryBeforeRequestRecord(t *testing.T) {
	path := writeTranscript(t, `{"type":"user","message":{"content":"ready"}}`)
	home, pid := liveBridgeProcess(t)
	claude := NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, home, func(context.Context, string, string, map[string]string, string, CookieSource, time.Duration) (int, string, error) {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString("\n{\"type\":\"system\",\"subtype\":\"compact_boundary\",\"content\":\"other compact\"}\n"); err != nil {
			f.Close()
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		return 200, `{}`, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := claude.Compact(ctx, &surface.Session{ID: "conversation-test", Bridge: "session_test", PID: pid, Transcript: path})
	if !surface.IsDeliveryOutcomeUnknown(err) {
		t.Fatalf("err=%v", err)
	}
}

func TestClaudeCompactReportsUnknownAfterAcceptedCommandLosesConfirmation(t *testing.T) {
	path := writeTranscript(t, `{"type":"user","message":{"content":"ready"}}`)
	home, pid := liveBridgeProcess(t)
	claude := NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, home, func(context.Context, string, string, map[string]string, string, CookieSource, time.Duration) (int, string, error) {
		return 200, `{}`, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := claude.Compact(ctx, &surface.Session{ID: "conversation-test", Bridge: "session_test", PID: pid, Transcript: path})
	if !surface.IsDeliveryOutcomeUnknown(err) {
		t.Fatalf("err=%v", err)
	}
}
