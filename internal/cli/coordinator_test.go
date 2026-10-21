package cli

import (
	"os"
	"os/exec"
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
	lazyRegistry(c.app)
	// These commands start and resume real host and Claude processes, which
	// a loaded runner can take well over a second to do.
	c.app.DefaultTimeout = 15 * time.Second
	c.app.Surfaces[0].Surface = surfaces.NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, c.claude.Home, posts.request)
	for _, key := range []string{"AGENTHAIL_SESSION_ID", "CODEX_THREAD_ID", "CLAUDE_CODE_SESSION_ID"} {
		t.Setenv(key, "")
	}
	c.project(t, "project-fixture", c.host.Cwd, "fixture")
	return c
}

// project registers dir as a folder project, so its coordinator is
// @<name>-lead.
func (c *coordinatorCLI) project(t *testing.T, id, dir, name string) {
	t.Helper()
	if err := c.r.AddProject(registry.ProjectRecord{ID: id, WorkspaceRoot: mustEvalSymlinks(t, dir), Title: name, Added: true}); err != nil {
		t.Fatal(err)
	}
}

// define records a coordinator of a project as the daemon's set and new
// actions do, so these tests start from a defined coordinator.
func (c *coordinatorCLI) define(t *testing.T, spec registry.CoordinatorSpec, sessionID string) {
	t.Helper()
	if spec.Surface == "" {
		spec.Surface = string(surface.KindClaude)
	}
	spec.ProjectRoot = mustEvalSymlinks(t, spec.Cwd)
	if _, err := c.r.SetCoordinator(spec, registry.CoordinatorChange{SessionID: sessionID}); err != nil {
		t.Fatal(err)
	}
}

func (c *coordinatorCLI) spec(t *testing.T) registry.CoordinatorSpec {
	t.Helper()
	spec, err := c.r.Coordinator("project-fixture")
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func (c *coordinatorCLI) withGoneCoordinator(t *testing.T) *coordinatorCLI {
	t.Helper()
	c.register(t, surface.Session{ID: goneConversation, Bridge: "session_gone", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: exitedPID, Status: surface.StatusIdle, Cwd: c.host.Cwd})
	if _, err := c.r.SetCoordinator(registry.CoordinatorSpec{ProjectID: "project-fixture", ProjectRoot: mustEvalSymlinks(t, c.host.Cwd), Name: "fixture-lead", Surface: string(surface.KindClaude), Cwd: c.host.Cwd, Instructions: "You coordinate fixture workers."}, registry.CoordinatorChange{SessionID: goneConversation}); err != nil {
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

// heldReplacement hands @fixture-lead from the gone conversation to a live process
// that has neither a messaging socket nor Remote Control, so a send to it waits
// in the queue.
func (c *coordinatorCLI) heldReplacement(t *testing.T) {
	t.Helper()
	c.register(t, surface.Session{ID: replacedConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: c.claude.PID, Status: surface.StatusBusy, Cwd: c.host.Cwd, Transcript: c.claude.Transcript(replacedConversation, c.host.Cwd)})
	c.claude.RecordStatus(replacedConversation, c.host.Cwd, "", "busy", "", time.Now())
	if err := c.r.HandOverCoordinator(c.spec(t), goneConversation, replacedConversation); err != nil {
		t.Fatal(err)
	}
}

func TestSendToCoordinatorWhoseConversationIsGoneStartsOneFromItsInstructions(t *testing.T) {
	c := newCoordinatorCLI(t)
	c.register(t, surface.Session{ID: goneConversation, Bridge: "session_gone", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: exitedPID, Status: surface.StatusIdle, Cwd: c.host.Cwd})
	c.define(t, registry.CoordinatorSpec{ProjectID: "project-fixture", Name: "fixture-lead", Cwd: c.host.Cwd, Instructions: "You coordinate fixture workers."}, goneConversation)
	if err := c.r.QueueMessage(goneConversation, "queued before the restart"); err != nil {
		t.Fatal(err)
	}
	const worker = "6a7b8c9d-0e1f-4a2b-8c3d-4e5f6a7b8c9d"
	c.register(t, surface.Session{ID: worker, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: exitedPID, Status: surface.StatusIdle, Cwd: c.host.Cwd})
	for _, args := range [][]string{{"channel", "create", "team"}, {"channel", "add", "team", goneConversation}} {
		if _, err := c.run(t, args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.r.AddRoute(goneConversation, worker, ".*"); err != nil {
		t.Fatal(err)
	}

	output, err := c.run(t, "send", "@fixture-lead", "plan the fixture release")
	if err != nil || !strings.Contains(output, "Started a new conversation for coordinator @fixture-lead") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	current := c.current(t, "fixture-lead")
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
	if members, err := c.r.ChannelMembers("team"); err != nil || len(members) != 1 || members[0] != current {
		t.Fatalf("channel members=%v err=%v", members, err)
	}
	if routes, err := c.r.ListRoutes(); err != nil || len(routes) != 1 || routes[0].FromSession != current {
		t.Fatalf("routes=%+v err=%v", routes, err)
	}

	if _, err := c.run(t, "send", "@fixture-lead", "next task"); err != nil || c.started(t) != 1 || c.current(t, "fixture-lead") != current {
		t.Fatalf("err=%v started=%d current=%s", err, c.started(t), c.current(t, "fixture-lead"))
	}
}

func TestDefinedCoordinatorStartsOnItsFirstMessage(t *testing.T) {
	c := newCoordinatorCLI(t)
	c.define(t, registry.CoordinatorSpec{ProjectID: "project-fixture", Name: "fixture-lead", Cwd: c.host.Cwd, Model: "fixture-model"}, "")
	output, err := c.run(t, "send", "@fixture-lead", "first task")
	if err != nil || !strings.Contains(output, "Started coordinator @fixture-lead") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	current := c.current(t, "fixture-lead")
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
	output, err := c.run(t, "send", "@fixture-lead", "urgent task", "--no-queue")
	if err != nil || !strings.Contains(output, "Started a new conversation for coordinator @fixture-lead") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	current := c.current(t, "fixture-lead")
	if c.started(t) != 1 || c.resumed(t, goneConversation) || !strings.HasSuffix(c.firstTurn(t, current), "urgent task") {
		t.Fatalf("started=%d resumed gone=%v", c.started(t), c.resumed(t, goneConversation))
	}
}

func TestSendToACoordinatorClaudeCannotReportOnWaitsInTheQueue(t *testing.T) {
	c := newCoordinatorCLI(t).withGoneCoordinator(t)
	c.claude.AgentsUnreadable()
	output, err := c.run(t, "send", "@fixture-lead", "keep this")
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
	app := &App{OpenRegistry: func() (*registry.Registry, error) { return r, nil }, Surfaces: []SurfaceEntry{{Name: "codex", Surface: surfaces.NewCodex("1")}}, DefaultTimeout: 10 * time.Second, catalogDaemonRunning: func() bool { return false }}
	cwd := t.TempDir()
	if err := r.AddProject(registry.ProjectRecord{ID: "project-codex", WorkspaceRoot: mustEvalSymlinks(t, cwd), Title: "fixture", Added: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetCoordinator(registry.CoordinatorSpec{ProjectID: "project-codex", ProjectRoot: mustEvalSymlinks(t, cwd), Name: "fixture-lead", Surface: string(surface.KindCodex), Cwd: cwd}, registry.CoordinatorChange{}); err != nil {
		t.Fatal(err)
	}
	output, err := captureStdout(t, func() error { return app.Run([]string{"send", "@fixture-lead", "first task"}) })
	if err != nil || !strings.Contains(output, "Started coordinator @fixture-lead") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	sid, err := r.LookupAlias("fixture-lead")
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
	c.define(t, registry.CoordinatorSpec{ProjectID: "project-fixture", Name: "fixture-lead", Cwd: c.host.Cwd}, "")
	if _, err := c.run(t, "send", "fixture-lead", "not for the coordinator"); err == nil || c.started(t) != 0 {
		t.Fatalf("a bare name started the coordinator: err=%v started=%d", err, c.started(t))
	}
	if _, err := c.run(t, "send", "@fixture-lead", "for the coordinator"); err != nil || c.started(t) != 1 {
		t.Fatalf("err=%v started=%d", err, c.started(t))
	}
}

// lazyRegistry makes app open its registry on first use, as the agenthail
// binary does.
func lazyRegistry(app *App) {
	r := app.Registry
	app.Registry = nil
	app.OpenRegistry = func() (*registry.Registry, error) { return r, nil }
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
	if _, err := c.run(t, "send", "@fixture-lead", "first"); err != nil {
		t.Fatal(err)
	}
	current := c.current(t, "fixture-lead")
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
	if err := c.r.HandOverCoordinator(c.spec(t), goneConversation, replacedConversation); err != nil {
		t.Fatal(err)
	}
	output, err := c.run(t, "send", goneConversation, "task")
	current := c.current(t, "fixture-lead")
	if err != nil || c.started(t) != 1 || current == replacedConversation || !strings.Contains(output, "agenthail last "+current) {
		t.Fatalf("output=%q err=%v started=%d current=%s", output, err, c.started(t), current)
	}
}

func TestLeadIsTheCoordinatorOfTheSendersProject(t *testing.T) {
	c := newCoordinatorCLI(t)
	alpha, beta := filepath.Join(c.host.Cwd, "alpha"), filepath.Join(t.TempDir(), "beta")
	for _, dir := range []string{alpha, beta} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	c.project(t, "project-alpha", alpha, "alpha")
	c.project(t, "project-beta", beta, "beta")
	c.define(t, registry.CoordinatorSpec{ProjectID: "project-alpha", Name: "alpha-lead", Cwd: alpha}, "")
	c.define(t, registry.CoordinatorSpec{ProjectID: "project-beta", Name: "beta-lead", Cwd: beta}, "")
	const worker = "7b8c9d0e-1f2a-4b3c-9d4e-5f6a7b8c9d0e"
	c.register(t, surface.Session{ID: worker, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: exitedPID, Status: surface.StatusIdle, Cwd: beta})

	t.Setenv("CLAUDE_CODE_SESSION_ID", worker)
	if output, err := c.run(t, "send", "@lead", "beta report"); err != nil || !strings.Contains(output, "Started coordinator @beta-lead") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	if _, err := c.r.LookupAlias("alpha-lead"); err == nil {
		t.Fatal("a beta worker's report started alpha's coordinator")
	}
	const removed = "8c9d0e1f-2a3b-4c4d-8e5f-6a7b8c9d0e1f"
	c.register(t, surface.Session{ID: removed, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: exitedPID, Status: surface.StatusIdle, Cwd: filepath.Join(beta, "removed-worktree")})
	t.Setenv("CLAUDE_CODE_SESSION_ID", removed)
	if _, err := c.run(t, "send", "@lead", "report from a removed folder"); err != nil {
		t.Fatalf("a worker whose folder was removed could not reach its project's coordinator: %v", err)
	}
	betaLead := c.current(t, "beta-lead")
	claudehosttest.WaitFor(t, "the report at beta's coordinator", func() bool {
		return len(c.host.Turns(t, betaLead)) == 2 || c.r.QueueCount(betaLead) == 1
	})

	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Chdir(alpha)
	if output, err := c.run(t, "send", "@lead", "alpha report"); err != nil || !strings.Contains(output, "Started coordinator @alpha-lead") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	if !strings.HasSuffix(c.firstTurn(t, c.current(t, "alpha-lead")), "alpha report") || !strings.HasSuffix(c.firstTurn(t, c.current(t, "beta-lead")), "beta report") {
		t.Fatal("a report reached the wrong coordinator")
	}
}

func TestAFolderOutsideEveryProjectHasNoLead(t *testing.T) {
	c := newCoordinatorCLI(t)
	t.Chdir(t.TempDir())
	if _, err := c.run(t, "send", "@lead", "anyone?"); err == nil || !strings.Contains(err.Error(), "is not in a project") {
		t.Fatalf("err=%v", err)
	}
	if _, err := c.run(t, "coordinator"); err == nil || !strings.Contains(err.Error(), "is not in a project") {
		t.Fatalf("err=%v", err)
	}
	if _, err := c.run(t, "identify", goneConversation, "lead"); err == nil {
		t.Fatal("a session took the reserved name @lead")
	}
	if c.started(t) != 0 {
		t.Fatalf("started=%d", c.started(t))
	}
}

func TestCoordinatorSetOfAPairedHostsSessionAsksThatHost(t *testing.T) {
	app, _, _, api := federatedCLIFixture(t, true)
	api.responses = map[string]string{"coordinator-set": `{"ok":true,"coordinator":{"projectId":"repo:github.com/example/widget","projectRoot":"/home/remote/src","name":"builder-lead","surface":"codex","sessionId":"remote-1"},"replaced":[{"projectId":"repo:github.com/example/widget","name":"old-lead","hostId":"` + vpsHostID + `"}]}`}
	output, err := captureStdout(t, func() error {
		return app.Run([]string{"coordinator", "set", "vps::@builder", "--name", "builder-lead", "--replace"})
	})
	if err != nil || !strings.Contains(output, "replaced vps::@old-lead") || !strings.Contains(output, "@builder-lead coordinates /home/remote/src") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	actions, _ := api.recorded()
	if len(actions) != 1 || actions[0]["action"] != "coordinator-set" || actions[0]["sessionId"] != vpsBuilder.ID || actions[0]["name"] != "builder-lead" || actions[0]["replace"] != true {
		t.Fatalf("actions=%v", actions)
	}
}

func TestLeadOnAPairedHostIsReachedThroughThatHost(t *testing.T) {
	app, reg, fake, api := federatedCLIFixture(t, true)
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"remote", "add", "origin", "https://github.com/example/widget.git"}} {
		if output, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, output)
		}
	}
	t.Chdir(repo)
	for _, key := range []string{"AGENTHAIL_SESSION_ID", "CODEX_THREAD_ID", "CLAUDE_CODE_SESSION_ID"} {
		t.Setenv(key, "")
	}
	if err := reg.ReplaceHostCoordinators(vpsHostID, []registry.CoordinatorEntry{{ProjectID: "repo:github.com/example/widget", ProjectRoot: "/home/remote/widget", Name: "widget-lead", Surface: "codex"}}); err != nil {
		t.Fatal(err)
	}
	api.responses = map[string]string{"coordinator-send": `{"ok":true,"started":true,"result":{"evidence":"delivered","status":"sent","detail":"Started coordinator @widget-lead."}}`}
	for _, target := range []string{"@lead", "@widget-lead"} {
		output, err := captureStdout(t, func() error { return app.Run([]string{"send", target, "status?"}) })
		if err != nil || !strings.Contains(output, "Started coordinator @widget-lead") || !strings.Contains(output, "agenthail last vps::@widget-lead") {
			t.Fatalf("%s: output=%q err=%v", target, output, err)
		}
	}
	actions, _ := api.recorded()
	if len(actions) != 2 || actions[0]["action"] != "coordinator-send" || actions[0]["host"] != vpsHostID || actions[0]["name"] != "widget-lead" || actions[0]["message"] != "status?" {
		t.Fatalf("actions=%v", actions)
	}
	if len(fake.sent) != 0 {
		t.Fatalf("local surface received %v", fake.sent)
	}
}
