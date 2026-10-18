package surfaces

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

func fakeBackgroundClaude(t *testing.T) (string, *Claude) {
	t.Helper()
	home := t.TempDir()
	script := `#!/bin/sh
env >> "$HOME/env"
if [ "$1" = agents ]; then
 printf '[{"id":"job12345","sessionId":"fixture-session","kind":"background","name":"fixture","cwd":"%s","state":"working"}]\n' "$HOME"
elif [ "$1" = --bg ]; then
 printf 'backgrounded · job12345\n'
fi
`
	binary := filepath.Join(home, "claude")
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_CLAUDE_BIN", binary)
	return home, NewClaude("", home)
}

func TestClaudeBackgroundCommandsDropCmuxTerminalState(t *testing.T) {
	t.Setenv("CMUX_SURFACE_ID", "fixture-surface")
	t.Setenv("CMUX_SOCKET_PATH", "/nonexistent/fixture.sock")
	t.Setenv("CMUX_CLAUDE_HOOKS_DISABLED", "0")
	t.Setenv("AGENTHAIL_FIXTURE_KEEP", "kept")
	home, c := fakeBackgroundClaude(t)
	if _, _, err := c.StartSession(context.Background(), surface.SessionStartOptions{Message: "hello", Cwd: home}); err != nil {
		t.Fatal(err)
	}
	env, err := os.ReadFile(filepath.Join(home, "env"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(env), "\n") {
		if strings.HasPrefix(line, "CMUX_") {
			t.Fatalf("Claude inherited terminal state %q", line)
		}
	}
	if !strings.Contains(string(env), "AGENTHAIL_FIXTURE_KEEP=kept\n") || !strings.Contains(string(env), "HOME="+home+"\n") {
		t.Fatalf("env=%s", env)
	}
}

const fixtureConversation = "fe758937-0caa-4912-823f-927677775b2b"

func endedBackgroundJob(t *testing.T) (*claudetest.Fixture, *Claude) {
	t.Helper()
	fixture := claudetest.New(t)
	fixture.Transcript(fixtureConversation, fixture.Home)
	fixture.Job("fe758937", fixtureConversation, fixture.Home, "done", 0)
	fixture.ResumeBridge("session_fixture")
	return fixture, NewClaude("", fixture.Home)
}

func shortResumeSettle(t *testing.T) {
	timeout := claudeResumeSettleTimeout
	claudeResumeSettleTimeout = 500 * time.Millisecond
	t.Cleanup(func() { claudeResumeSettleTimeout = timeout })
}

func TestClaudeResumeSettlesOnReachableProcessWhileJobStillSaysDone(t *testing.T) {
	fixture, c := endedBackgroundJob(t)
	fixture.JobState("fe758937", "done", "", time.Now().Add(time.Hour))
	result, err := c.SessionAction(context.Background(), &surface.Session{ID: fixtureConversation}, "resume")
	if err != nil || result["id"] != "fe758937" || result["unchanged"] != false || result["state"] != string(surface.StatusIdle) {
		t.Fatalf("result=%v err=%v", result, err)
	}
	if count := fixture.Invocations("--bg", "--resume", fixtureConversation); count != 1 {
		t.Fatalf("resumes=%d", count)
	}
}

func TestClaudeResumeLeavesReachableProcessAlone(t *testing.T) {
	fixture, c := endedBackgroundJob(t)
	fixture.Record(fixtureConversation, fixture.Home, "session_fixture")
	result, err := c.SessionAction(context.Background(), &surface.Session{ID: fixtureConversation}, "resume")
	if err != nil || result["unchanged"] != true || fixture.Invocations("--bg", "--resume", fixtureConversation) != 0 {
		t.Fatalf("result=%v err=%v", result, err)
	}
}

func TestClaudeResumeReportsJobThatFailsToStart(t *testing.T) {
	detail := "exit 1 before init: Error: Settings file not found: /fixture/settings.json"
	fixture, c := endedBackgroundJob(t)
	fixture.ResumeNeverReady()
	fixture.JobState("fe758937", "failed", detail, time.Now().Add(time.Hour))
	result, err := c.SessionAction(context.Background(), &surface.Session{ID: fixtureConversation}, "resume")
	if err == nil || !strings.Contains(err.Error(), "is failed after resume") || !strings.Contains(err.Error(), detail) || surface.IsDeliveryOutcomeUnknown(err) {
		t.Fatalf("result=%v err=%v", result, err)
	}
}

func TestClaudeResumeIgnoresFailureLeftFromBeforeResume(t *testing.T) {
	shortResumeSettle(t)
	fixture, c := endedBackgroundJob(t)
	fixture.ResumeNeverReady()
	fixture.JobState("fe758937", "failed", "earlier failure", time.Now().Add(-time.Hour))
	result, err := c.SessionAction(context.Background(), &surface.Session{ID: fixtureConversation}, "resume")
	if err == nil || !surface.IsDeliveryOutcomeUnknown(err) || strings.Contains(err.Error(), "earlier failure") {
		t.Fatalf("result=%v err=%v", result, err)
	}
}

func TestClaudeResumeWithUnreadableJobRecordIsUnknown(t *testing.T) {
	shortResumeSettle(t)
	fixture, c := endedBackgroundJob(t)
	fixture.ResumeNeverReady()
	if err := os.MkdirAll(filepath.Join(fixture.Home, ".claude", "jobs", "fe758937"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.Home, ".claude", "jobs", "fe758937", "state.json"), []byte(`{"state":"fai`), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := c.SessionAction(context.Background(), &surface.Session{ID: fixtureConversation}, "resume")
	if err == nil || !surface.IsDeliveryOutcomeUnknown(err) {
		t.Fatalf("result=%v err=%v", result, err)
	}
}

func TestClaudeResumeCountsFailureInTheSameMillisecond(t *testing.T) {
	fixture, c := endedBackgroundJob(t)
	started := time.Date(2026, 1, 2, 3, 4, 5, 123_600_000, time.UTC)
	fixture.JobState("fe758937", "failed", "fixture failure", started.Truncate(time.Millisecond))
	_, err := c.awaitReachable(context.Background(), fixtureConversation, "fe758937", started)
	if err == nil || !strings.Contains(err.Error(), "fixture failure") || surface.IsDeliveryOutcomeUnknown(err) {
		t.Fatalf("err=%v", err)
	}
}
