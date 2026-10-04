package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestClaudePostDispatchFailuresHaveUnknownOutcome(t *testing.T) {
	original := claudeSendRequest
	t.Cleanup(func() { claudeSendRequest = original })
	for _, test := range []struct {
		name   string
		status int
		body   string
		err    error
	}{
		{name: "transport", err: context.DeadlineExceeded},
		{name: "http", status: 500, body: "upstream failed"},
		{name: "challenge", status: 200, body: "Just a moment"},
	} {
		t.Run(test.name, func(t *testing.T) {
			claudeSendRequest = func(context.Context, string, string, map[string]string, string, string, string, time.Duration) (int, string, error) {
				return test.status, test.body, test.err
			}
			_, err := (&Claude{}).postMessage(context.Background(), &surface.Session{ID: "session_test"}, "message")
			if !surface.IsDeliveryOutcomeUnknown(err) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestClaudePostClientFailuresAreTerminal(t *testing.T) {
	original := claudeSendRequest
	t.Cleanup(func() { claudeSendRequest = original })
	for _, test := range []struct {
		status int
		reason surface.DeliveryTerminalKind
	}{
		{http.StatusBadRequest, surface.DeliveryInvalidRequest},
		{http.StatusUnauthorized, surface.DeliveryAuthenticationNeeded},
		{http.StatusForbidden, surface.DeliveryAccessDenied},
		{http.StatusNotFound, surface.DeliveryTargetMissing},
	} {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			claudeSendRequest = func(context.Context, string, string, map[string]string, string, string, string, time.Duration) (int, string, error) {
				return test.status, "rejected", nil
			}
			_, err := (&Claude{}).postMessage(context.Background(), &surface.Session{ID: "session_test"}, "message")
			if !surface.IsDeliveryTerminal(err) || surface.IsDeliveryOutcomeUnknown(err) {
				t.Fatalf("status=%d err=%v", test.status, err)
			}
			if surface.DeliveryTerminalReason(err) != test.reason {
				t.Fatalf("status=%d reason=%q", test.status, surface.DeliveryTerminalReason(err))
			}
		})
	}
}

func TestClaudePostRateLimitOutcomeRemainsUnknown(t *testing.T) {
	original := claudeSendRequest
	t.Cleanup(func() { claudeSendRequest = original })
	claudeSendRequest = func(context.Context, string, string, map[string]string, string, string, string, time.Duration) (int, string, error) {
		return http.StatusTooManyRequests, "try later", nil
	}
	_, err := (&Claude{}).postMessage(context.Background(), &surface.Session{ID: "session_test"}, "message")
	if !surface.IsDeliveryOutcomeUnknown(err) || surface.IsDeliveryTerminal(err) {
		t.Fatalf("err=%v", err)
	}
}

func TestClaudeSendUsesTranscriptReadiness(t *testing.T) {
	original := claudeSendRequest
	t.Cleanup(func() { claudeSendRequest = original })
	calls := 0
	claudeSendRequest = func(context.Context, string, string, map[string]string, string, string, string, time.Duration) (int, string, error) {
		calls++
		return 200, `{}`, nil
	}
	claude := NewClaude("Default", t.TempDir())
	recent := writeTranscript(t, `
{"type":"user","uuid":"u0","timestamp":"2026-07-19T01:00:00Z","message":{"content":"previous"}}
{"type":"assistant","uuid":"a0","timestamp":"2026-07-19T01:00:01Z","message":{"id":"m0","stop_reason":"end_turn","content":[{"type":"text","text":"previous answer"}]}}`)
	newActivity := time.Date(2026, 7, 19, 1, 5, 0, 0, time.UTC)
	result, err := claude.Send(context.Background(), &surface.Session{ID: "session_test", Status: surface.StatusBusy, Transcript: recent, LastActive: newActivity}, "racing")
	if err != nil || result.Accepted || calls != 0 {
		t.Fatalf("recent result=%+v calls=%d err=%v", result, calls, err)
	}

	completed := writeTranscript(t, `
{"type":"user","uuid":"u1","timestamp":"2026-07-19T01:00:00Z","message":{"content":"one"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-07-19T01:00:01Z","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"answer"}]}}`)
	writeJitter := time.Date(2026, 7, 19, 1, 0, 1, 9_000_000, time.UTC)
	result, err = claude.Send(context.Background(), &surface.Session{ID: "session_test", Status: surface.StatusBusy, Transcript: completed, LastActive: writeJitter}, "next")
	if err != nil || !result.Accepted || calls != 1 {
		t.Fatalf("completed result=%+v calls=%d err=%v", result, calls, err)
	}

	active := writeTranscript(t, `{"type":"user","uuid":"u2","message":{"content":"running"}}`)
	result, err = claude.Send(context.Background(), &surface.Session{ID: "session_test", Status: surface.StatusIdle, Transcript: active}, "too soon")
	if err != nil || result.Accepted || calls != 1 {
		t.Fatalf("active result=%+v calls=%d err=%v", result, calls, err)
	}

	result, err = claude.Send(context.Background(), &surface.Session{ID: "session_test", Status: surface.StatusIdle}, "unproven")
	if err != nil || result.Accepted || calls != 1 {
		t.Fatalf("missing transcript result=%+v calls=%d err=%v", result, calls, err)
	}
}

func TestClaudeCompactPostsRemoteSlashCommandAndConfirmsBoundary(t *testing.T) {
	original := claudeSendRequest
	t.Cleanup(func() { claudeSendRequest = original })
	path := writeTranscript(t, `{"type":"user","message":{"content":"ready"}}`)
	var body string
	claudeSendRequest = func(_ context.Context, method, url string, _ map[string]string, requestBody, _ string, _ string, _ time.Duration) (int, string, error) {
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
	}
	if err := (&Claude{}).Compact(context.Background(), &surface.Session{ID: "session_test", Transcript: path}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `"content":"/compact"`) {
		t.Fatalf("body=%s", body)
	}
}

func TestClaudeCompactDoesNotAcceptBoundaryBeforeRequestRecord(t *testing.T) {
	path := writeTranscript(t, `{"type":"user","message":{"content":"ready"}}`)
	claude := NewClaudeWithRequest("", t.TempDir(), func(context.Context, string, string, map[string]string, string, string, string, time.Duration) (int, string, error) {
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
	err := claude.Compact(ctx, &surface.Session{ID: "session_test", Transcript: path})
	if !surface.IsDeliveryOutcomeUnknown(err) {
		t.Fatalf("err=%v", err)
	}
}

func TestClaudeCompactReportsUnknownAfterAcceptedCommandLosesConfirmation(t *testing.T) {
	original := claudeSendRequest
	t.Cleanup(func() { claudeSendRequest = original })
	path := writeTranscript(t, `{"type":"user","message":{"content":"ready"}}`)
	claudeSendRequest = func(context.Context, string, string, map[string]string, string, string, string, time.Duration) (int, string, error) {
		return 200, `{}`, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := (&Claude{}).Compact(ctx, &surface.Session{ID: "session_test", Transcript: path})
	if !surface.IsDeliveryOutcomeUnknown(err) {
		t.Fatalf("err=%v", err)
	}
}
