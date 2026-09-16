package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestWhoamiUsesBoundSessionInsteadOfSharedCWD(t *testing.T) {
	fake := &cliSurface{kind: surface.KindCodex}
	app, store := cliFixture(t, fake)
	sharedCwd := "/work/agenthail"
	for _, session := range []surface.Session{
		{ID: "codex-one", Surface: surface.KindCodex, Cwd: sharedCwd},
		{ID: "codex-two", Surface: surface.KindCodex, Cwd: sharedCwd},
	} {
		if err := store.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.ReplaceAlias("builder", "codex-two"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_THREAD_ID", "codex-two")

	output, err := captureStdout(t, func() error { return app.Run([]string{"whoami", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	var got whoamiResult
	if err := json.Unmarshal([]byte(output), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Resolved || got.Session != "codex-two" || got.Alias != "builder" || got.Project != "agenthail" || got.Cwd != sharedCwd {
		t.Fatalf("whoami=%+v", got)
	}
}

func TestWhoamiReportsUnboundShellWithoutGuessingFromCWD(t *testing.T) {
	fake := &cliSurface{kind: surface.KindCodex}
	app, store := cliFixture(t, fake)
	if err := store.RegisterSession(surface.Session{ID: "other", Surface: surface.KindCodex, Cwd: "/work/agenthail"}); err != nil {
		t.Fatal(err)
	}

	output, err := captureStdout(t, func() error { return app.Run([]string{"whoami"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "unresolved: no caller session binding") || strings.Contains(output, "other") {
		t.Fatalf("output=%q", output)
	}
}

func TestWhoamiResolvesClaudeBinding(t *testing.T) {
	fake := &cliSurface{kind: surface.KindClaude}
	app, store := cliFixture(t, fake)
	if err := store.RegisterSession(surface.Session{ID: "claude-session", Surface: surface.KindClaude, Cwd: "/work/claude-project"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_SESSION_ID", "claude-session")

	output, err := captureStdout(t, func() error { return app.Run([]string{"whoami", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	var got whoamiResult
	if err := json.Unmarshal([]byte(output), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Resolved || got.Session != "claude-session" || got.Surface != "claude" || got.Project != "claude-project" {
		t.Fatalf("whoami=%+v", got)
	}
}

func TestWhoamiReportsUnregisteredBoundSession(t *testing.T) {
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{}}
	app, _ := cliFixture(t, fake)
	t.Setenv("CLAUDE_SESSION_ID", "missing")

	output, err := captureStdout(t, func() error { return app.Run([]string{"whoami", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	var got whoamiResult
	if err := json.Unmarshal([]byte(output), &got); err != nil {
		t.Fatal(err)
	}
	if got.Resolved || !strings.Contains(got.Reason, "resolve sender") {
		t.Fatalf("whoami=%+v", got)
	}
}
