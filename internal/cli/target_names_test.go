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
