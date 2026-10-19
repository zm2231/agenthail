package cli

import (
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestNamedTargetDoesNotMatchAnotherSessionsName(t *testing.T) {
	e := exitedClaudeApp(t)
	if err := e.r.RegisterSession(surface.Session{ID: "session_named", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "claudeprobe-a3", Status: surface.StatusIdle}); err != nil {
		t.Fatal(err)
	}
	err := e.app.Run([]string{"send", "@probe-a", "hello"})
	if err == nil || !strings.Contains(err.Error(), "no session matched '@probe-a'") || e.delivered(t, "hello") {
		t.Fatalf("err=%v delivered=%v", err, e.delivered(t, "hello"))
	}
}

func TestNamedTargetIsExactForEveryCommandThatTakesOne(t *testing.T) {
	e := exitedClaudeApp(t)
	if err := e.r.RegisterSession(surface.Session{ID: "session_named", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "claudeprobe-a3", Status: surface.StatusIdle}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"send", e.session.ID, "hello", "--from", "@probe-a"},
		{"thread", "status", "@probe-a"},
		{"identify", "@probe-a", "named"},
	} {
		if _, err := captureStdout(t, func() error { return e.app.Run(args) }); err == nil {
			t.Fatalf("%v resolved @probe-a to another session's name", args)
		}
	}
	if e.delivered(t, "hello") {
		t.Fatal("a send went out with an unresolved sender")
	}
	if sid, err := e.r.ResolveTarget("probe-a"); err != nil || sid != "session_named" {
		t.Fatalf("an unprefixed fragment no longer matches by name: sid=%q err=%v", sid, err)
	}
}

func (e exitedClaude) delivered(t *testing.T, text string) bool {
	t.Helper()
	for _, turn := range e.host.Turns(t, e.session.ID) {
		if turn.Text == text {
			return true
		}
	}
	return e.peers.Count(text) != 0
}
