package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

const (
	goneConversation  = "0d1e2f3a-4b5c-4d6e-8f70-81a2b3c4d5e6"
	freshConversation = "9a8b7c6d-5e4f-4a3b-9c2d-1e0f9a8b7c6d"
)

func coordinatorApp(t *testing.T) (*App, *registry.Registry, *claudetest.Fixture, *claudePosts) {
	t.Helper()
	fixture := claudetest.New(t)
	fixture.StartAs(freshConversation)
	fixture.ResumeBridge("session_fresh")
	posts := &claudePosts{}
	app, r := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	app.Surfaces[0].Surface = surfaces.NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, fixture.Home, posts.request)
	return app, r, fixture, posts
}

func TestSendToCoordinatorWhoseConversationIsGoneStartsOneFromItsInstructions(t *testing.T) {
	app, r, fixture, _ := coordinatorApp(t)
	gone := surface.Session{ID: goneConversation, Bridge: "session_gone", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: exitedPID, Status: surface.StatusIdle, Cwd: fixture.Home}
	if err := r.RegisterSession(gone); err != nil {
		t.Fatal(err)
	}
	instructions := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(instructions, []byte("You coordinate fixture workers."), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"identify", goneConversation, "lead"}) }); err != nil {
		t.Fatal(err)
	}
	if _, err := captureStdout(t, func() error {
		return app.Run([]string{"coordinator", "set", "@lead", "--instructions", instructions})
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.QueueMessage(goneConversation, "queued before the restart"); err != nil {
		t.Fatal(err)
	}

	output, err := captureStdout(t, func() error { return app.Run([]string{"send", "@lead", "plan the fixture release"}) })
	if err != nil || !strings.Contains(output, "Started a new conversation for coordinator @lead") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	prompt := fixture.Prompt(freshConversation)
	for _, part := range []string{"You coordinate fixture workers.", "conversation " + goneConversation + " no longer exists", "plan the fixture release"} {
		if !strings.Contains(prompt, part) {
			t.Fatalf("prompt %q lacks %q", prompt, part)
		}
	}
	if fixture.Invocations("--bg", "--resume", goneConversation) != 0 || fixture.Starts() != 1 {
		t.Fatalf("resumes=%d starts=%d", fixture.Invocations("--bg", "--resume", goneConversation), fixture.Starts())
	}
	if sid, err := r.LookupAlias("lead"); err != nil || sid != freshConversation {
		t.Fatalf("alias=%q err=%v", sid, err)
	}
	if r.QueueCount(freshConversation) != 1 || r.QueueCount(goneConversation) != 0 {
		t.Fatalf("queue fresh=%d gone=%d", r.QueueCount(freshConversation), r.QueueCount(goneConversation))
	}

	output, err = captureStdout(t, func() error { return app.Run([]string{"send", "@lead", "next task"}) })
	if err != nil || !strings.Contains(output, "for @lead and will deliver") || fixture.Starts() != 1 || r.QueueCount(freshConversation) != 2 {
		t.Fatalf("output=%q err=%v starts=%d queued=%d", output, err, fixture.Starts(), r.QueueCount(freshConversation))
	}
}

func TestDefinedCoordinatorStartsOnItsFirstMessage(t *testing.T) {
	app, r, fixture, _ := coordinatorApp(t)
	if _, err := captureStdout(t, func() error {
		return app.Run([]string{"coordinator", "new", "claude", "--cwd", fixture.Home, "--model", "fixture-model"})
	}); err != nil {
		t.Fatal(err)
	}
	output, err := captureStdout(t, func() error { return app.Run([]string{"send", "@coordinator", "first task"}) })
	if err != nil || !strings.Contains(output, "Started coordinator @coordinator") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	if !strings.HasSuffix(fixture.Prompt(freshConversation), "first task") {
		t.Fatalf("prompt=%q", fixture.Prompt(freshConversation))
	}
	if started, err := r.Session(freshConversation); err != nil || started.ConfiguredModel != "fixture-model" || started.Cwd != fixture.Home {
		t.Fatalf("started=%+v err=%v", started, err)
	}
	if sid, err := r.LookupAlias("coordinator"); err != nil || sid != freshConversation {
		t.Fatalf("alias=%q err=%v", sid, err)
	}
}
