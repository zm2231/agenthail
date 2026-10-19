package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/codextest"
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
	home, err := filepath.EvalSymlinks(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	if started, err := r.Session(freshConversation); err != nil || started.ConfiguredModel != "fixture-model" || mustEvalSymlinks(t, started.Cwd) != home {
		t.Fatalf("started=%+v err=%v", started, err)
	}
	if sid, err := r.LookupAlias("coordinator"); err != nil || sid != freshConversation {
		t.Fatalf("alias=%q err=%v", sid, err)
	}
}

func goneCoordinatorApp(t *testing.T) (*App, *registry.Registry, *claudetest.Fixture) {
	t.Helper()
	app, r, fixture, _ := coordinatorApp(t)
	gone := surface.Session{ID: goneConversation, Bridge: "session_gone", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: exitedPID, Status: surface.StatusIdle, Cwd: fixture.Home}
	if err := r.RegisterSession(gone); err != nil {
		t.Fatal(err)
	}
	if err := r.SetCoordinator(registry.CoordinatorSpec{Name: "lead", Surface: string(surface.KindClaude), Cwd: fixture.Home, Instructions: "You coordinate fixture workers."}, goneConversation); err != nil {
		t.Fatal(err)
	}
	return app, r, fixture
}

func TestNoQueueSendToAGoneCoordinatorStillStartsIt(t *testing.T) {
	app, r, fixture := goneCoordinatorApp(t)
	output, err := captureStdout(t, func() error { return app.Run([]string{"send", "@lead", "urgent task", "--no-queue"}) })
	if err != nil || !strings.Contains(output, "Started a new conversation for coordinator @lead") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	if fixture.Starts() != 1 || fixture.Invocations("--bg", "--resume", goneConversation) != 0 || !strings.HasSuffix(fixture.Prompt(freshConversation), "urgent task") {
		t.Fatalf("starts=%d resumes=%d", fixture.Starts(), fixture.Invocations("--bg", "--resume", goneConversation))
	}
	if sid, err := r.LookupAlias("lead"); err != nil || sid != freshConversation {
		t.Fatalf("alias=%q err=%v", sid, err)
	}
}

func TestSendToACoordinatorClaudeCannotReportOnWaitsInTheQueue(t *testing.T) {
	app, r, fixture := goneCoordinatorApp(t)
	fixture.AgentsUnreadable()
	output, err := captureStdout(t, func() error { return app.Run([]string{"send", "@lead", "keep this"}) })
	if err != nil || !strings.Contains(output, "queued") || r.QueueCount(goneConversation) != 1 {
		t.Fatalf("output=%q err=%v queued=%d", output, err, r.QueueCount(goneConversation))
	}
	if fixture.Starts() != 0 || fixture.Invocations("--bg", "--resume", goneConversation) != 0 {
		t.Fatalf("starts=%d resumes=%d", fixture.Starts(), fixture.Invocations("--bg", "--resume", goneConversation))
	}
}

func TestCodexCoordinatorStartsOnItsFirstMessage(t *testing.T) {
	for _, key := range []string{"AGENTHAIL_SESSION_ID", "CODEX_THREAD_ID", "CLAUDE_CODE_SESSION_ID"} {
		t.Setenv(key, "")
	}
	lifecycle := codextest.InstallLifecycle(t, codextest.Home(t), nil)
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	app := &App{Registry: r, Surfaces: []SurfaceEntry{{Name: "codex", Surface: surfaces.NewCodex("1")}}, DefaultTimeout: 10 * time.Second, catalogDaemonRunning: func() bool { return false }}
	cwd := t.TempDir()
	if err := app.Run([]string{"coordinator", "new", "codex", "--cwd", cwd, "--permission-mode", "plan"}); err == nil {
		t.Fatal("a codex coordinator accepted a Claude permission mode")
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"coordinator", "new", "codex", "--cwd", cwd}) }); err != nil {
		t.Fatal(err)
	}
	output, err := captureStdout(t, func() error { return app.Run([]string{"send", "@coordinator", "first task"}) })
	if err != nil || !strings.Contains(output, "Started coordinator @coordinator") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	sid, err := r.LookupAlias("coordinator")
	if err != nil {
		t.Fatal(err)
	}
	thread, ok := lifecycle.Daemon().Thread(sid)
	if !ok || len(thread.Turns) != 1 || !strings.HasSuffix(thread.Turns[0].User, "first task") || len(lifecycle.Daemon().Calls("thread/start")) != 1 {
		t.Fatalf("thread=%+v ok=%v", thread, ok)
	}
}

func TestBareCoordinatorNameIsAFragmentNotTheCoordinator(t *testing.T) {
	app, _, fixture, _ := coordinatorApp(t)
	if _, err := captureStdout(t, func() error {
		return app.Run([]string{"coordinator", "new", "claude", "--cwd", fixture.Home, "--name", "lead"})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"send", "lead", "not for the coordinator"}) }); err == nil || fixture.Starts() != 0 {
		t.Fatalf("a bare name started the coordinator: err=%v starts=%d", err, fixture.Starts())
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"send", "@lead", "for the coordinator"}) }); err != nil || fixture.Starts() != 1 {
		t.Fatalf("err=%v starts=%d", err, fixture.Starts())
	}
}

func mustEvalSymlinks(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestReplyToAReplacedCoordinatorConversationFollowsTheCurrentOne(t *testing.T) {
	app, _, fixture := goneCoordinatorApp(t)
	if _, err := captureStdout(t, func() error { return app.Run([]string{"send", "@lead", "first"}) }); err != nil {
		t.Fatal(err)
	}
	transcript := fixture.Transcript(freshConversation, fixture.Home)
	go func() {
		time.Sleep(500 * time.Millisecond)
		at := time.Now().UTC().Format(time.RFC3339Nano)
		user, _ := json.Marshal(map[string]any{"type": "user", "uuid": "fixture-follow-up", "sessionId": freshConversation, "cwd": fixture.Home, "timestamp": at, "message": map[string]any{"role": "user", "content": "follow up"}})
		answer, _ := json.Marshal(map[string]any{"type": "assistant", "uuid": "fixture-follow-up-answer", "sessionId": freshConversation, "cwd": fixture.Home, "timestamp": at, "message": map[string]any{"id": "fixture-follow-up-message", "stop_reason": "end_turn", "content": []map[string]any{{"type": "text", "text": "answer from the current coordinator"}}}})
		duration, _ := json.Marshal(map[string]any{"type": "system", "subtype": "turn_duration", "sessionId": freshConversation, "cwd": fixture.Home, "timestamp": at})
		file, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			return
		}
		defer file.Close()
		_, _ = file.WriteString(string(user) + "\n" + string(answer) + "\n" + string(duration) + "\n")
	}()
	output, err := captureStdout(t, func() error {
		return app.Run([]string{"send", goneConversation, "follow up", "--reply", "--timeout", "5s"})
	})
	if err != nil || !strings.Contains(output, "answer from the current coordinator") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	if fixture.Starts() != 1 || fixture.Invocations("--bg", "--resume", goneConversation) != 0 {
		t.Fatalf("starts=%d resumes=%d", fixture.Starts(), fixture.Invocations("--bg", "--resume", goneConversation))
	}
}

func TestQueuedSendToAReplacedCoordinatorConversationNamesTheCurrentOne(t *testing.T) {
	app, r, _ := goneCoordinatorApp(t)
	if _, err := captureStdout(t, func() error { return app.Run([]string{"send", "@lead", "first"}) }); err != nil {
		t.Fatal(err)
	}
	output, err := captureStdout(t, func() error { return app.Run([]string{"send", goneConversation, "second"}) })
	if err != nil || r.QueueCount(freshConversation) != 1 || !strings.Contains(output, "agenthail steer "+freshConversation) {
		t.Fatalf("output=%q err=%v queued=%d", output, err, r.QueueCount(freshConversation))
	}
}

func TestStartFromAReplacedCoordinatorConversationNamesTheNewOne(t *testing.T) {
	app, r, fixture := goneCoordinatorApp(t)
	const replacement = "5f4e3d2c-1b0a-4f9e-8d7c-6b5a4f3e2d1c"
	if err := r.RegisterSession(surface.Session{ID: replacement, Bridge: "session_replacement", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: exitedPID, Status: surface.StatusIdle, Cwd: fixture.Home}); err != nil {
		t.Fatal(err)
	}
	if err := r.HandOverCoordinator("lead", goneConversation, replacement); err != nil {
		t.Fatal(err)
	}
	output, err := captureStdout(t, func() error { return app.Run([]string{"send", goneConversation, "task"}) })
	if err != nil || fixture.Starts() != 1 || !strings.Contains(output, "agenthail last "+freshConversation) {
		t.Fatalf("output=%q err=%v starts=%d", output, err, fixture.Starts())
	}
}
