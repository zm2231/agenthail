package cli

import (
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestNamedTargetDoesNotMatchAnotherSessionsName(t *testing.T) {
	app, r, _, posts, _ := exitedClaudeApp(t)
	if err := r.RegisterSession(surface.Session{ID: "session_named", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "claudeprobe-a3", Status: surface.StatusIdle}); err != nil {
		t.Fatal(err)
	}
	err := app.Run([]string{"send", "@probe-a", "hello"})
	if err == nil || !strings.Contains(err.Error(), "no session matched '@probe-a'") || posts.count("hello") != 0 {
		t.Fatalf("err=%v posts=%d", err, posts.count("hello"))
	}
}

func TestNamedTargetIsExactForEveryCommandThatTakesOne(t *testing.T) {
	app, r, _, posts, exited := exitedClaudeApp(t)
	if err := r.RegisterSession(surface.Session{ID: "session_named", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "claudeprobe-a3", Status: surface.StatusIdle}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"send", exited.ID, "hello", "--from", "@probe-a"},
		{"thread", "status", "@probe-a"},
		{"identify", "@probe-a", "named"},
	} {
		if _, err := captureStdout(t, func() error { return app.Run(args) }); err == nil {
			t.Fatalf("%v resolved @probe-a to another session's name", args)
		}
	}
	if posts.count("hello") != 0 {
		t.Fatal("a send went out with an unresolved sender")
	}
	if sid, err := r.ResolveTarget("probe-a"); err != nil || sid != "session_named" {
		t.Fatalf("an unprefixed fragment no longer matches by name: sid=%q err=%v", sid, err)
	}
}

