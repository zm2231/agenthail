package surfaces

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestSidecarPythonRejectsConfiguredUnsupportedRuntime(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_PYTHON", executable)
	if _, err := sidecarPython(); err == nil || !strings.Contains(err.Error(), "Python 3.10+") {
		t.Fatalf("err=%v", err)
	}
}

func TestSidecarPythonUsesConfiguredSupportedRuntime(t *testing.T) {
	for _, candidate := range []string{"python3.14", "python3.13", "python3.12", "python3.11", "python3.10", "python3"} {
		t.Setenv("AGENTHAIL_PYTHON", candidate)
		if path, err := sidecarPython(); err == nil {
			if path == "" {
				t.Fatal("empty interpreter path")
			}
			return
		}
	}
	t.Skip("no Python 3.10+ interpreter available")
}

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

func TestClaudeCompactPostsRemoteSlashCommandWithoutTranscriptConfirmation(t *testing.T) {
	original := claudeSendRequest
	t.Cleanup(func() { claudeSendRequest = original })
	var body string
	claudeSendRequest = func(_ context.Context, method, url string, _ map[string]string, requestBody, _ string, _ string, _ time.Duration) (int, string, error) {
		if method != "POST" || !strings.Contains(url, "/v1/code/sessions/") {
			t.Fatalf("method=%s url=%s", method, url)
		}
		body = requestBody
		return 200, `{}`, nil
	}
	if err := (&Claude{}).Compact(context.Background(), &surface.Session{ID: "session_test"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `"content":"/compact"`) {
		t.Fatalf("body=%s", body)
	}
}
