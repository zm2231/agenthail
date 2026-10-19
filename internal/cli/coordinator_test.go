package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

const (
	goneConversation     = "0d1e2f3a-4b5c-4d6e-8f70-81a2b3c4d5e6"
	replacedConversation = "5f4e3d2c-1b0a-4f9e-8d7c-6b5a4f3e2d1c"
)

type coordinatorCLI struct {
	app    *App
	r      *registry.Registry
	claude *claudetest.Fixture
	host   *claudehosttest.Fixture
}

// newCoordinatorCLI runs the CLI against the real Claude adapter and a running
// Claude host, which starts new conversations.
func newCoordinatorCLI(t *testing.T) *coordinatorCLI {
	t.Helper()
	c := &coordinatorCLI{claude: claudetest.New(t)}
	c.host = claudehosttest.New(t, c.claude.Home)
	c.host.Start(t)
	posts := &claudePosts{}
	c.app, c.r = cliFixture(t, &cliSurface{kind: surface.KindClaude})
	c.app.Surfaces[0].Surface = surfaces.NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, c.claude.Home, posts.request)
	return c
}

func (c *coordinatorCLI) withGoneCoordinator(t *testing.T) *coordinatorCLI {
	t.Helper()
	c.register(t, surface.Session{ID: goneConversation, Bridge: "session_gone", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: exitedPID, Status: surface.StatusIdle, Cwd: c.host.Cwd})
	if err := c.r.SetCoordinator(registry.CoordinatorSpec{Name: "lead", Surface: string(surface.KindClaude), Cwd: c.host.Cwd, Instructions: "You coordinate fixture workers."}, goneConversation); err != nil {
		t.Fatal(err)
	}
	return c
}

func (c *coordinatorCLI) register(t *testing.T, session surface.Session) {
	t.Helper()
	if err := c.r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
}

func (c *coordinatorCLI) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return captureStdout(t, func() error { return c.app.Run(args) })
}

func (c *coordinatorCLI) current(t *testing.T, name string) string {
	t.Helper()
	sid, err := c.r.LookupAlias(name)
	if err != nil {
		t.Fatal(err)
	}
	return sid
}

func (c *coordinatorCLI) started(t *testing.T) int {
	t.Helper()
	sessions, err := c.r.ListSessions(1000)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, session := range sessions {
		if session.RuntimeKind == surface.RuntimeClaudeHost {
			count++
		}
	}
	return count
}

func (c *coordinatorCLI) resumed(t *testing.T, conversation string) bool {
	t.Helper()
	return len(c.host.Spawns(t, conversation)) != 0
}

func (c *coordinatorCLI) firstTurn(t *testing.T, conversation string) string {
	t.Helper()
	claudehosttest.WaitFor(t, "the first turn", func() bool { return len(c.host.Turns(t, conversation)) > 0 })
	return c.host.Turns(t, conversation)[0].Text
}

// heldReplacement hands @lead from the gone conversation to a live process
// that has neither a messaging socket nor Remote Control, so a send to it waits
// in the queue.
func (c *coordinatorCLI) heldReplacement(t *testing.T) {
	t.Helper()
	c.register(t, surface.Session{ID: replacedConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: c.claude.PID, Status: surface.StatusBusy, Cwd: c.host.Cwd, Transcript: c.claude.Transcript(replacedConversation, c.host.Cwd)})
	c.claude.RecordStatus(replacedConversation, c.host.Cwd, "", "busy", "", time.Now())
	if err := c.r.HandOverCoordinator("lead", goneConversation, replacedConversation); err != nil {
		t.Fatal(err)
	}
}

func TestSendToCoordinatorWhoseConversationIsGoneStartsOneFromItsInstructions(t *testing.T) {
	c := newCoordinatorCLI(t)
	c.register(t, surface.Session{ID: goneConversation, Bridge: "session_gone", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: exitedPID, Status: surface.StatusIdle, Cwd: c.host.Cwd})
	instructions := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(instructions, []byte("You coordinate fixture workers."), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.run(t, "identify", goneConversation, "lead"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.run(t, "coordinator", "set", "@lead", "--instructions", instructions); err != nil {
		t.Fatal(err)
	}
	if err := c.r.QueueMessage(goneConversation, "queued before the restart"); err != nil {
		t.Fatal(err)
	}

	output, err := c.run(t, "send", "@lead", "plan the fixture release")
	if err != nil || !strings.Contains(output, "Started a new conversation for coordinator @lead") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	current := c.current(t, "lead")
	if current == goneConversation || c.started(t) != 1 || c.resumed(t, goneConversation) {
		t.Fatalf("current=%s started=%d resumed gone=%v", current, c.started(t), c.resumed(t, goneConversation))
	}
	first := c.firstTurn(t, current)
	for _, part := range []string{"You coordinate fixture workers.", "conversation " + goneConversation + " no longer exists", "plan the fixture release"} {
		if !strings.Contains(first, part) {
			t.Fatalf("first turn %q lacks %q", first, part)
		}
	}
	if c.r.QueueCount(current) != 1 || c.r.QueueCount(goneConversation) != 0 {
		t.Fatalf("queue current=%d gone=%d", c.r.QueueCount(current), c.r.QueueCount(goneConversation))
	}

	if _, err := c.run(t, "send", "@lead", "next task"); err != nil || c.started(t) != 1 || c.current(t, "lead") != current {
		t.Fatalf("err=%v started=%d current=%s", err, c.started(t), c.current(t, "lead"))
	}
}

func TestDefinedCoordinatorStartsOnItsFirstMessage(t *testing.T) {
	c := newCoordinatorCLI(t)
	if _, err := c.run(t, "coordinator", "new", "claude", "--cwd", c.host.Cwd, "--model", "fixture-model"); err != nil {
		t.Fatal(err)
	}
	output, err := c.run(t, "send", "@coordinator", "first task")
	if err != nil || !strings.Contains(output, "Started coordinator @coordinator") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	current := c.current(t, "coordinator")
	if first := c.firstTurn(t, current); !strings.HasSuffix(first, "first task") {
		t.Fatalf("first turn=%q", first)
	}
	if started, err := c.r.Session(current); err != nil || started.ConfiguredModel != "fixture-model" || mustEvalSymlinks(t, started.Cwd) != mustEvalSymlinks(t, c.host.Cwd) {
		t.Fatalf("started=%+v err=%v", started, err)
	}
	spawns := c.host.Spawns(t, current)
	if len(spawns) != 1 || !strings.Contains(strings.Join(spawns[0].Argv, " "), "fixture-model") {
		t.Fatalf("spawns=%+v", spawns)
	}
}

func TestNoQueueSendToAGoneCoordinatorStillStartsIt(t *testing.T) {
	c := newCoordinatorCLI(t).withGoneCoordinator(t)
	output, err := c.run(t, "send", "@lead", "urgent task", "--no-queue")
	if err != nil || !strings.Contains(output, "Started a new conversation for coordinator @lead") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	current := c.current(t, "lead")
	if c.started(t) != 1 || c.resumed(t, goneConversation) || !strings.HasSuffix(c.firstTurn(t, current), "urgent task") {
		t.Fatalf("started=%d resumed gone=%v", c.started(t), c.resumed(t, goneConversation))
	}
}

func TestSendToACoordinatorClaudeCannotReportOnWaitsInTheQueue(t *testing.T) {
	c := newCoordinatorCLI(t).withGoneCoordinator(t)
	c.claude.AgentsUnreadable()
	output, err := c.run(t, "send", "@lead", "keep this")
	if err != nil || !strings.Contains(output, "queued") || c.r.QueueCount(goneConversation) != 1 {
		t.Fatalf("output=%q err=%v queued=%d", output, err, c.r.QueueCount(goneConversation))
	}
	if c.started(t) != 0 || c.resumed(t, goneConversation) {
		t.Fatalf("started=%d resumed gone=%v", c.started(t), c.resumed(t, goneConversation))
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
	c := newCoordinatorCLI(t)
	if _, err := c.run(t, "coordinator", "new", "claude", "--cwd", c.host.Cwd, "--name", "lead"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.run(t, "send", "lead", "not for the coordinator"); err == nil || c.started(t) != 0 {
		t.Fatalf("a bare name started the coordinator: err=%v started=%d", err, c.started(t))
	}
	if _, err := c.run(t, "send", "@lead", "for the coordinator"); err != nil || c.started(t) != 1 {
		t.Fatalf("err=%v started=%d", err, c.started(t))
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
	c := newCoordinatorCLI(t).withGoneCoordinator(t)
	if _, err := c.run(t, "send", "@lead", "first"); err != nil {
		t.Fatal(err)
	}
	current := c.current(t, "lead")
	c.firstTurn(t, current)
	_, err := c.run(t, "send", goneConversation, "follow up", "--reply", "--timeout", "3s")
	if err == nil || !strings.Contains(err.Error(), "cannot be correlated with transcript turns") {
		t.Fatalf("a reply was awaited on a conversation other than the coordinator's: err=%v", err)
	}
	if c.started(t) != 1 || c.resumed(t, goneConversation) {
		t.Fatalf("started=%d resumed gone=%v", c.started(t), c.resumed(t, goneConversation))
	}
}

func TestQueuedSendToAReplacedCoordinatorConversationNamesTheCurrentOne(t *testing.T) {
	c := newCoordinatorCLI(t).withGoneCoordinator(t)
	c.heldReplacement(t)
	output, err := c.run(t, "send", goneConversation, "second")
	if err != nil || c.r.QueueCount(replacedConversation) != 1 || !strings.Contains(output, "agenthail steer "+replacedConversation) {
		t.Fatalf("output=%q err=%v queued=%d", output, err, c.r.QueueCount(replacedConversation))
	}
}

func TestStartFromAReplacedCoordinatorConversationNamesTheNewOne(t *testing.T) {
	c := newCoordinatorCLI(t).withGoneCoordinator(t)
	c.register(t, surface.Session{ID: replacedConversation, Bridge: "session_replacement", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: exitedPID, Status: surface.StatusIdle, Cwd: c.host.Cwd})
	if err := c.r.HandOverCoordinator("lead", goneConversation, replacedConversation); err != nil {
		t.Fatal(err)
	}
	output, err := c.run(t, "send", goneConversation, "task")
	current := c.current(t, "lead")
	if err != nil || c.started(t) != 1 || current == replacedConversation || !strings.Contains(output, "agenthail last "+current) {
		t.Fatalf("output=%q err=%v started=%d current=%s", output, err, c.started(t), current)
	}
}
