package surfaces

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
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
	return home, NewClaude(nil, home)
}

func TestClaudeBackgroundCommandsDropCmuxTerminalState(t *testing.T) {
	t.Setenv("CMUX_SURFACE_ID", "fixture-surface")
	t.Setenv("CMUX_SOCKET_PATH", "/nonexistent/fixture.sock")
	t.Setenv("CMUX_CLAUDE_HOOKS_DISABLED", "0")
	t.Setenv("AGENTHAIL_FIXTURE_KEEP", "kept")
	home, c := fakeBackgroundClaude(t)
	if _, _, err := c.BackgroundStarter().StartSession(context.Background(), surface.SessionStartOptions{Message: "hello", Cwd: home}); err != nil {
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

func TestClaudeStartsASessionInTheHostWithItsFirstTurn(t *testing.T) {
	host := claudehosttest.New(t, "")
	host.Start(t)
	c := NewClaude(nil, host.Home)
	turn := uuid.NewString()
	session, sent, err := c.StartSession(surface.WithMessageUUID(context.Background(), turn), surface.SessionStartOptions{Message: "build it", Cwd: host.Cwd, Name: "builder", PermissionMode: "acceptEdits", TurnOptions: surface.TurnOptions{Effort: "high"}})
	if err != nil {
		t.Fatal(err)
	}
	if session.RuntimeKind != surface.RuntimeClaudeHost || session.PID <= 0 || session.Name != "builder" || sent == nil || sent.UUID != turn || !sent.Accepted {
		t.Fatalf("session=%+v sent=%+v", session, sent)
	}
	spawns := host.Spawns(t, session.ID)
	if len(spawns) != 1 || !slices.Contains(spawns[0].Argv, "--permission-mode=acceptEdits") && !slices.Contains(spawns[0].Argv, "acceptEdits") {
		t.Fatalf("spawns=%+v", spawns)
	}
	claudehosttest.WaitFor(t, "the first turn", func() bool {
		turns := host.Turns(t, session.ID)
		return len(turns) == 1 && turns[0].UUID == turn && turns[0].Text == "build it" && string(turns[0].Origin) == `{"kind":"human"}`
	})
	listed := listedSession(t, c, session.ID)
	if listed.PID != session.PID {
		t.Fatalf("listed=%+v, want pid %d", listed, session.PID)
	}
}

func TestClaudeStartWithoutTheHostCreatesNothing(t *testing.T) {
	home := t.TempDir()
	session, sent, err := NewClaude(nil, home).StartSession(context.Background(), surface.SessionStartOptions{Message: "build it", Cwd: home})
	if err == nil || session != nil || sent != nil || surface.IsDeliveryOutcomeUnknown(err) || !strings.Contains(err.Error(), "Claude host is not running") {
		t.Fatalf("session=%+v sent=%+v err=%v", session, sent, err)
	}
}

func endedBackgroundJob(t *testing.T) (*claudetest.Fixture, *claudehosttest.Fixture, *Claude) {
	t.Helper()
	fixture, host, c := hostResume(t)
	fixture.Transcript(fixtureConversation, fixture.Home)
	fixture.Job("fe758937", fixtureConversation, fixture.Home, "done", 0)
	return fixture, host, c
}

func TestClaudeResumeActionOpensAnEndedJobInTheHost(t *testing.T) {
	_, host, c := endedBackgroundJob(t)
	result, err := c.SessionAction(context.Background(), &surface.Session{ID: fixtureConversation}, "resume")
	if err != nil || result["id"] != "fe758937" || result["unchanged"] != false {
		t.Fatalf("result=%v err=%v", result, err)
	}
	if spawns := host.Spawns(t, fixtureConversation); len(spawns) != 1 {
		t.Fatalf("spawns=%+v", spawns)
	}
}

func TestClaudeResumeLeavesReachableProcessAlone(t *testing.T) {
	fixture, host, c := endedBackgroundJob(t)
	fixture.Record(fixtureConversation, fixture.Home, "session_fixture")
	result, err := c.SessionAction(context.Background(), &surface.Session{ID: fixtureConversation}, "resume")
	if err != nil || result["unchanged"] != true || len(host.Spawns(t, fixtureConversation)) != 0 {
		t.Fatalf("result=%v err=%v", result, err)
	}
}

func TestClaudeHostSessionLifecycle(t *testing.T) {
	host := claudehosttest.New(t, "")
	claudetest.New(t)
	host.Start(t)
	c := NewClaude(nil, host.Home)
	session, _, err := c.StartSession(context.Background(), surface.SessionStartOptions{Message: "build it", Cwd: host.Cwd})
	if err != nil {
		t.Fatal(err)
	}
	listedSession(t, c, session.ID)
	if status, err := c.SessionAction(context.Background(), session, "status"); err != nil || status["pid"] != session.PID {
		t.Fatalf("status=%v err=%v", status, err)
	}
	if _, err := c.SessionAction(context.Background(), session, "logs"); err == nil || !strings.Contains(err.Error(), "agenthail last") {
		t.Fatalf("logs err=%v", err)
	}
	if _, err := c.SessionAction(context.Background(), session, "stop"); err != nil {
		t.Fatal(err)
	}
	claudehosttest.WaitFor(t, "the session to close", func() bool { return !claudehosttest.Alive(session.PID) })
	if status, err := c.SessionAction(context.Background(), session, "status"); err != nil || status["state"] != "closed" {
		t.Fatalf("status after stop=%v err=%v", status, err)
	}
	if result, err := c.SessionAction(context.Background(), session, "resume"); err != nil || result["unchanged"] != false {
		t.Fatalf("resume=%v err=%v", result, err)
	}
	if spawns := host.Spawns(t, session.ID); len(spawns) != 2 {
		t.Fatalf("spawns=%+v, want the open and the resume", spawns)
	}
}

func TestClaudeStopThatCannotRunIsAFailureNotUnknown(t *testing.T) {
	home := t.TempDir()
	script := `#!/bin/sh
if [ "$1" = agents ]; then
 printf '[{"id":"job12345","sessionId":"fixture-session","kind":"background","cwd":"%s/removed","state":"working"}]\n' "$HOME"
fi
`
	binary := filepath.Join(home, "claude")
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_CLAUDE_BIN", binary)
	_, err := NewClaude(nil, home).SessionAction(context.Background(), &surface.Session{ID: "fixture-session"}, "stop")
	if !surface.IsDeliveryUnavailable(err) || surface.IsDeliveryOutcomeUnknown(err) {
		t.Fatalf("err=%v", err)
	}
}
