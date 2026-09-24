package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func resetCookieHeaderCache(t *testing.T) {
	t.Helper()
	cookieHeaderCache.Lock()
	cookieHeaderCache.entries = map[string]cookieHeaderCacheEntry{}
	cookieHeaderCache.Unlock()
	t.Cleanup(func() {
		cookieHeaderCache.Lock()
		cookieHeaderCache.entries = map[string]cookieHeaderCacheEntry{}
		cookieHeaderCache.Unlock()
	})
}

func TestLoadCookieHeaderCachesAndInvalidates(t *testing.T) {
	resetCookieHeaderCache(t)
	root := t.TempDir()
	countPath := filepath.Join(root, "count")
	node := filepath.Join(root, "node")
	if err := os.WriteFile(node, []byte("#!/bin/sh\ncount=0\n[ -f \"$AGENTHAIL_TEST_COOKIE_COUNT\" ] && count=$(cat \"$AGENTHAIL_TEST_COOKIE_COUNT\")\ncount=$((count + 1))\nprintf '%s' \"$count\" > \"$AGENTHAIL_TEST_COOKIE_COUNT\"\nprintf 'session=cached'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+":"+os.Getenv("PATH"))
	t.Setenv("AGENTHAIL_TEST_COOKIE_COUNT", countPath)
	bridge := filepath.Join(root, "cookie.mjs")
	if err := os.WriteFile(bridge, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		header, err := loadCookieHeader(context.Background(), bridge, "https://claude.ai/")
		if err != nil || header != "session=cached" {
			t.Fatalf("header=%q err=%v", header, err)
		}
	}
	data, err := os.ReadFile(countPath)
	if err != nil || string(data) != "1" {
		t.Fatalf("calls=%q err=%v", data, err)
	}
	invalidateCookieHeader(bridge, "https://claude.ai/")
	if _, err := loadCookieHeader(context.Background(), bridge, "https://claude.ai/"); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(countPath)
	if err != nil || string(data) != "2" {
		t.Fatalf("calls=%q err=%v", data, err)
	}
}

func TestLoadCookieHeaderCachesBridgeFailure(t *testing.T) {
	resetCookieHeaderCache(t)
	root := t.TempDir()
	countPath := filepath.Join(root, "count")
	node := filepath.Join(root, "node")
	if err := os.WriteFile(node, []byte("#!/bin/sh\ncount=0\n[ -f \"$AGENTHAIL_TEST_COOKIE_COUNT\" ] && count=$(cat \"$AGENTHAIL_TEST_COOKIE_COUNT\")\ncount=$((count + 1))\nprintf '%s' \"$count\" > \"$AGENTHAIL_TEST_COOKIE_COUNT\"\nexit 2\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+":"+os.Getenv("PATH"))
	t.Setenv("AGENTHAIL_TEST_COOKIE_COUNT", countPath)
	bridge := filepath.Join(root, "cookie.mjs")
	if err := os.WriteFile(bridge, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := loadCookieHeader(context.Background(), bridge, "https://claude.ai/"); err == nil {
			t.Fatal("expected cookie bridge failure")
		}
	}
	data, err := os.ReadFile(countPath)
	if err != nil || string(data) != "1" {
		t.Fatalf("calls=%q err=%v", data, err)
	}
}

func TestSidecarRequestUsesCachedCookieWithoutBridgeFallback(t *testing.T) {
	resetCookieHeaderCache(t)
	root := t.TempDir()
	countPath := filepath.Join(root, "count")
	node := filepath.Join(root, "node")
	if err := os.WriteFile(node, []byte("#!/bin/sh\ncount=0\n[ -f \"$AGENTHAIL_TEST_COOKIE_COUNT\" ] && count=$(cat \"$AGENTHAIL_TEST_COOKIE_COUNT\")\ncount=$((count + 1))\nprintf '%s' \"$count\" > \"$AGENTHAIL_TEST_COOKIE_COUNT\"\nprintf 'session=cached'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	python := filepath.Join(root, "python")
	if err := os.WriteFile(python, []byte("#!/bin/sh\nif [ \"$1\" = \"-c\" ]; then exit 0; fi\ncat >/dev/null\nprintf '{\"status\":200,\"body\":\"ok\",\"error\":\"\"}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	bridge := filepath.Join(root, "cookie.mjs")
	worker := filepath.Join(root, "sidecar.py")
	if err := os.WriteFile(bridge, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(worker, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+":"+os.Getenv("PATH"))
	t.Setenv("AGENTHAIL_TEST_COOKIE_COUNT", countPath)
	t.Setenv("AGENTHAIL_PYTHON", python)
	t.Setenv("AGENTHAIL_SIDECAR", worker)
	t.Setenv("AGENTHAIL_COOKIE_BRIDGE", bridge)
	for range 2 {
		status, body, err := sidecarRequestWithCookies(context.Background(), "GET", "https://claude.ai/api/organizations", nil, "", bridge, "https://claude.ai/", time.Second)
		if err != nil || status != 200 || body != "ok" {
			t.Fatalf("status=%d body=%q err=%v", status, body, err)
		}
	}
	data, err := os.ReadFile(countPath)
	if err != nil || string(data) != "1" {
		t.Fatalf("cookie bridge calls=%q err=%v", data, err)
	}
}

func TestProcessGroupCommandKillsDescendantsOnTimeout(t *testing.T) {
	root := t.TempDir()
	childPath := filepath.Join(root, "child.pid")
	script := filepath.Join(root, "worker")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30 &\nprintf '%s' \"$!\" > \"$AGENTHAIL_TEST_CHILD_PID\"\nwait\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_TEST_CHILD_PID", childPath)
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if err := processGroupCommand(ctx, script).Run(); err == nil {
		t.Fatal("expected timeout")
	}
	data, err := os.ReadFile(childPath)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		err = syscall.Kill(pid, 0)
		if err == syscall.ESRCH {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("child %d survived timeout: %v", pid, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

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
