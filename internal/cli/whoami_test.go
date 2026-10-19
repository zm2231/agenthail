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
		{ID: "codex-one", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Cwd: sharedCwd},
		{ID: "codex-two", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Cwd: sharedCwd},
	} {
		if err := store.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetAlias("builder", "codex-two"); err != nil {
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
	if err := store.RegisterSession(surface.Session{ID: "other", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Cwd: "/work/agenthail"}); err != nil {
		t.Fatal(err)
	}

	output, err := captureStdout(t, func() error { return app.Run([]string{"whoami", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	var got whoamiResult
	if err := json.Unmarshal([]byte(output), &got); err != nil {
		t.Fatal(err)
	}
	if got.Resolved || got.Session != "" || got.Reason == "" || strings.Contains(output, "other") {
		t.Fatalf("whoami=%+v output=%q", got, output)
	}
}

func TestSessionFromAnotherAgenthailIsRefusedWithRemedies(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	worker := surface.Session{ID: "worker", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle}
	fake := &cliSurface{kind: surface.KindClaude, caps: surface.Capabilities{Send: true}, sessions: map[string]surface.Session{"worker": worker}}
	app, store := cliFixture(t, fake)
	if err := store.RegisterSession(worker); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CODE_SESSION_ID", "foreign-conversation")
	want := []string{"CLAUDE_CODE_SESSION_ID=foreign-conversation", store.Path(), "--from", "unset CLAUDE_CODE_SESSION_ID"}

	err := app.Run([]string{"send", "worker", "hello"})
	for _, part := range want {
		if err == nil || !strings.Contains(err.Error(), part) {
			t.Fatalf("send error %v does not contain %q", err, part)
		}
	}
	if len(fake.sent) != 0 {
		t.Fatalf("a refused sender still sent: %v", fake.sent)
	}

	output, err := captureStdout(t, func() error { return app.Run([]string{"whoami", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	var got whoamiResult
	if err := json.Unmarshal([]byte(output), &got); err != nil {
		t.Fatal(err)
	}
	for _, part := range want {
		if got.Resolved || !strings.Contains(got.Reason, part) {
			t.Fatalf("whoami reason %q does not contain %q", got.Reason, part)
		}
	}

	t.Setenv("CODEX_THREAD_ID", "inherited-thread")
	if err := app.Run([]string{"send", "worker", "hello"}); err == nil || !strings.Contains(err.Error(), "unset CLAUDE_CODE_SESSION_ID CODEX_THREAD_ID to send") {
		t.Fatalf("send error %v does not name every caller variable still set", err)
	}
}
