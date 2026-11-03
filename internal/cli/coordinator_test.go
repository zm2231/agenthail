package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

const (
	goneConversation     = "0d1e2f3a-4b5c-4d6e-8f70-81a2b3c4d5e6"
	replacedConversation = "5f4e3d2c-1b0a-4f9e-8d7c-6b5a4f3e2d1c"
	startedConversation  = "9e8d7c6b-5a4f-4e3d-8c2b-1a0f9e8d7c6b"
)

type coordinatorCLI struct {
	app    *App
	r      *registry.Registry
	claude *claudetest.Fixture
	cwd    string
	api    *fakeDaemonAPI
}

// newCoordinatorCLI runs the CLI against the real Claude adapter and a fake
// daemon, which is where every coordinator send now goes.
func newCoordinatorCLI(t *testing.T) *coordinatorCLI {
	t.Helper()
	c := &coordinatorCLI{claude: claudetest.New(t), cwd: t.TempDir()}
	posts := &claudePosts{}
	c.app, c.r = cliFixture(t, &cliSurface{kind: surface.KindClaude})
	lazyRegistry(c.app)
	c.app.DefaultTimeout = claudeFixtureTimeout
	c.app.Surfaces[0].Surface = surfaces.NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, c.claude.Home, posts.request)
	for _, key := range []string{"AGENTHAIL_SESSION_ID", "CODEX_THREAD_ID", "CLAUDE_CODE_SESSION_ID"} {
		t.Setenv(key, "")
	}
	c.api = &fakeDaemonAPI{responses: map[string]string{}}
	server := httptest.NewServer(c.api)
	t.Cleanup(server.Close)
	c.app.catalogDaemonRunning = func() bool { return true }
	c.app.daemonAPIOverride = func() (*daemonAPIClient, error) {
		return &daemonAPIClient{baseURL: server.URL, token: "dashboard-synthetic", httpClient: server.Client()}, nil
	}
	c.app.daemonSessionPageReader = func() (sessionPageReader, error) { return testSessionPageReader{}, nil }
	c.app.daemonSessionStreamReader = func() (sessionStreamReader, error) { return testSessionStreamReader{blocked: true}, nil }
	c.project(t, "project-fixture", c.cwd, "fixture")
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
	c.register(t, surface.Session{ID: goneConversation, Bridge: "session_gone", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: exitedPID, Status: surface.StatusIdle, Cwd: c.cwd})
	c.define(t, registry.CoordinatorSpec{ProjectID: "project-fixture", Name: "fixture-lead", Cwd: c.cwd, Instructions: "You coordinate fixture workers."}, goneConversation)
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

// answers makes the fake daemon answer every coordinator send with body.
func (c *coordinatorCLI) answers(body string) {
	c.api.responses["coordinator-send"] = body
}

func (c *coordinatorCLI) sends(t *testing.T) []map[string]any {
	t.Helper()
	actions, _ := c.api.recorded()
	var sends []map[string]any
	for _, action := range actions {
		if action["action"] == "coordinator-send" {
			sends = append(sends, action)
		}
	}
	return sends
}

const startedAnswer = `{"ok":true,"started":true,"result":{"evidence":"delivered","status":"sent","sessionId":"` + startedConversation + `","started":true,"detail":"Started a new conversation for coordinator @fixture-lead."}}`

func TestASendToALeadGoesToTheDaemonWithItsOptions(t *testing.T) {
	c := newCoordinatorCLI(t).withGoneCoordinator(t)
	c.answers(startedAnswer)
	output, err := c.run(t, "send", "@fixture-lead", "plan the fixture release", "--model", "fixture-model", "--no-queue")
	if err != nil || !strings.Contains(output, "Started a new conversation for coordinator @fixture-lead") || !strings.Contains(output, "agenthail last @fixture-lead") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	sends := c.sends(t)
	if len(sends) != 1 || sends[0]["name"] != "fixture-lead" || sends[0]["message"] != "plan the fixture release" || sends[0]["model"] != "fixture-model" || sends[0]["noQueue"] != true {
		t.Fatalf("sends=%v", sends)
	}
	if _, hasHost := sends[0]["host"]; hasHost {
		t.Fatalf("a local lead's send named a host: %v", sends[0])
	}
	if spawned := c.claude.Invocations(); spawned != 0 {
		t.Fatalf("the CLI ran Claude %d times itself", spawned)
	}
}

func TestALeadWithNoConversationYetIsStartedByTheDaemon(t *testing.T) {
	c := newCoordinatorCLI(t)
	c.define(t, registry.CoordinatorSpec{ProjectID: "project-fixture", Name: "fixture-lead", Cwd: c.cwd}, "")
	c.answers(startedAnswer)
	output, err := c.run(t, "send", "@fixture-lead", "first task")
	if err != nil || !strings.Contains(output, "agenthail last @fixture-lead") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	if sends := c.sends(t); len(sends) != 1 || sends[0]["message"] != "first task" {
		t.Fatalf("sends=%v", sends)
	}
	if _, err := c.run(t, "send", "@fixture-lead", "first task", "--reply"); err == nil || !strings.Contains(err.Error(), "has no conversation yet") {
		t.Fatalf("a reply was awaited from a conversation that does not exist yet: err=%v", err)
	}
}

func TestALeadIsNotReachedWithoutTheDaemon(t *testing.T) {
	c := newCoordinatorCLI(t).withGoneCoordinator(t)
	c.app.catalogDaemonRunning = func() bool { return false }
	if _, err := c.run(t, "send", "@fixture-lead", "anyone?"); err == nil || !strings.Contains(err.Error(), "through the daemon") {
		t.Fatalf("err=%v", err)
	}
	if _, err := c.run(t, "send", goneConversation, "anyone?"); err == nil || !strings.Contains(err.Error(), "through the daemon") {
		t.Fatalf("a send by session id went around the daemon: err=%v", err)
	}
	if spawned := c.claude.Invocations(); spawned != 0 {
		t.Fatalf("the CLI ran Claude %d times itself", spawned)
	}
}

func TestAChannelReachesItsCoordinatorThroughTheDaemon(t *testing.T) {
	c := newCoordinatorCLI(t).withGoneCoordinator(t)
	if _, err := c.r.CreateChannel("team"); err != nil {
		t.Fatal(err)
	}
	if err := c.r.AddToChannel("team", goneConversation); err != nil {
		t.Fatal(err)
	}
	c.answers(startedAnswer)
	output, err := c.run(t, "channel", "send", "team", "standup")
	if err != nil || !strings.Contains(output, "#team: 1 sent") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	sends := c.sends(t)
	if len(sends) != 1 || sends[0]["name"] != "fixture-lead" || !strings.HasSuffix(sends[0]["message"].(string), "via #team] standup") {
		t.Fatalf("sends=%v", sends)
	}
	if spawned := c.claude.Invocations(); spawned != 0 {
		t.Fatalf("the CLI ran Claude %d times itself", spawned)
	}
}

func TestBareCoordinatorNameIsAFragmentNotTheCoordinator(t *testing.T) {
	c := newCoordinatorCLI(t)
	c.define(t, registry.CoordinatorSpec{ProjectID: "project-fixture", Name: "fixture-lead", Cwd: c.cwd}, "")
	c.answers(startedAnswer)
	if _, err := c.run(t, "send", "fixture-lead", "not for the coordinator"); err == nil || len(c.sends(t)) != 0 {
		t.Fatalf("a bare name reached the coordinator: err=%v sends=%v", err, c.sends(t))
	}
	if _, err := c.run(t, "send", "@fixture-lead", "for the coordinator"); err != nil || len(c.sends(t)) != 1 {
		t.Fatalf("err=%v sends=%v", err, c.sends(t))
	}
}

func TestQueuedSendToAReplacedCoordinatorConversationNamesTheCurrentOne(t *testing.T) {
	c := newCoordinatorCLI(t).withGoneCoordinator(t)
	c.register(t, surface.Session{ID: replacedConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: c.claude.PID, Status: surface.StatusBusy, Cwd: c.cwd, Transcript: c.claude.Transcript(replacedConversation, c.cwd)})
	c.claude.RecordStatus(replacedConversation, c.cwd, "", "busy", "", time.Now())
	if _, err := c.r.HandOverCoordinator(c.spec(t), goneConversation, replacedConversation, registry.CoordinatorHandover{}); err != nil {
		t.Fatal(err)
	}
	c.answers(`{"ok":true,"result":{"evidence":"queued","status":"queued","sessionId":"` + replacedConversation + `","queueId":4}}`)
	output, err := c.run(t, "send", goneConversation, "second")
	if err != nil || !strings.Contains(output, "queued #4") || !strings.Contains(output, "agenthail steer "+replacedConversation) {
		t.Fatalf("output=%q err=%v", output, err)
	}
	if sends := c.sends(t); len(sends) != 1 || sends[0]["name"] != "fixture-lead" {
		t.Fatalf("sends=%v", sends)
	}
}

func captureStderr(t *testing.T, run func() error) (string, error) {
	t.Helper()
	old := os.Stderr
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = writer
	runErr := run()
	writer.Close()
	os.Stderr = old
	data, readErr := io.ReadAll(reader)
	reader.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	return string(data), runErr
}

// followedStreams answers turn started-turn on the stream of conversation
// replies and nothing on any other, and logs each open and cancel in order.
type followedStreams struct {
	replies string
	mu      sync.Mutex
	log     []string
}

func (f *followedStreams) record(entry string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, entry)
}

func (f *followedStreams) entries() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.log...)
}

func (f *followedStreams) OpenSessionStream(_ context.Context, session *surface.Session, after uint64) (sessionstream.Subscription, error) {
	name := fmt.Sprintf("%s@%d", session.ID, after)
	f.record("open " + name)
	reader := testSessionStreamReader{blocked: true}
	if session.ID == f.replies {
		reader = testSessionStreamReader{events: []sessionstream.Event{
			{Kind: "message", Role: "assistant", TurnID: "started-turn", ItemID: "reply", Body: "the new lead's reply", Version: 1},
			{Kind: "done", Status: "completed", TurnID: "started-turn"},
		}}
	}
	subscription, err := reader.OpenSessionStream(context.Background(), session, after)
	subscription.Cancel = func() { f.record("cancel " + name) }
	return subscription, err
}

const startedWithTurn = `{"ok":true,"started":true,"result":{"evidence":"delivered","status":"sent","sessionId":"` + startedConversation + `","turnId":"started-turn","started":true,"detail":"Started a new conversation for coordinator @fixture-lead (conversation ` + goneConversation + ` no longer exists)."}}`

func TestASendThatStartsANewLeadFollowsTheNewConversation(t *testing.T) {
	for _, flags := range [][]string{{"--reply"}, {"--stream"}, {"--reply", "--json"}} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			c := newCoordinatorCLI(t).withGoneCoordinator(t)
			c.register(t, surface.Session{ID: startedConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Transport: "uds", Status: surface.StatusBusy, Cwd: c.cwd})
			streams := &followedStreams{replies: startedConversation}
			c.app.daemonSessionStreamReader = func() (sessionStreamReader, error) { return streams, nil }
			c.answers(startedWithTurn)
			var output string
			stderr, err := captureStderr(t, func() error {
				var runErr error
				output, runErr = c.run(t, append([]string{"send", goneConversation, "task", "--timeout", "3s"}, flags...)...)
				return runErr
			})
			if err != nil || !strings.Contains(output, "the new lead's reply") || strings.Contains(stderr, "cannot follow") {
				t.Fatalf("output=%q stderr=%q err=%v, want the replacement's reply", output, stderr, err)
			}
			if flags[len(flags)-1] == "--json" {
				var answer struct {
					Delivery struct {
						SessionID string `json:"sessionId"`
						Evidence  string `json:"evidence"`
						Started   bool   `json:"started"`
					} `json:"delivery"`
					Reply struct {
						Text string `json:"text"`
					} `json:"reply"`
				}
				if err := json.Unmarshal([]byte(output), &answer); err != nil || answer.Delivery.SessionID != startedConversation || !answer.Delivery.Started || answer.Delivery.Evidence != string(surface.EvidenceReplyObserved) || answer.Reply.Text != "the new lead's reply" {
					t.Fatalf("json=%q err=%v, want the delivery to the new conversation and its reply", output, err)
				}
			} else if !strings.Contains(output, "Started a new conversation for coordinator @fixture-lead") {
				t.Fatalf("output=%q, want word that the lead was replaced", output)
			}
			want := []string{"open " + goneConversation + "@10", "cancel " + goneConversation + "@10", "open " + startedConversation + "@0", "cancel " + startedConversation + "@0"}
			if got := streams.entries(); !slices.Equal(got, want) {
				t.Fatalf("streams=%v, want %v: the addressed stream released before the new conversation is followed from its start", got, want)
			}
			if flags[0] == "--reply" {
				history, err := c.r.ListHistory(10, startedConversation)
				if err != nil || len(history) != 1 || history[0].Kind != "reply" || history[0].Result != "the new lead's reply" {
					t.Fatalf("history=%+v err=%v, want the reply recorded on the new conversation", history, err)
				}
			}
		})
	}
}

func TestASendThatStartsANewLeadWithoutFollowingOpensNoStream(t *testing.T) {
	c := newCoordinatorCLI(t).withGoneCoordinator(t)
	streams := &followedStreams{replies: startedConversation}
	c.app.daemonSessionStreamReader = func() (sessionStreamReader, error) { return streams, nil }
	c.answers(startedWithTurn)
	output, err := c.run(t, "send", goneConversation, "task")
	if err != nil || !strings.Contains(output, "Started a new conversation for coordinator @fixture-lead") || !strings.Contains(output, "agenthail last "+startedConversation) {
		t.Fatalf("output=%q err=%v", output, err)
	}
	if got := streams.entries(); len(got) != 0 {
		t.Fatalf("streams=%v, want none for a send that does not follow", got)
	}
}

func TestASendThatStartsANewLeadWithoutAConfirmedTurnSaysItCannotFollowIt(t *testing.T) {
	c := newCoordinatorCLI(t).withGoneCoordinator(t)
	c.answers(`{"ok":true,"started":true,"result":{"evidence":"submitted","status":"submitted","sessionId":"` + startedConversation + `","started":true,"detail":"Started a new conversation for coordinator @fixture-lead."}}`)
	var output string
	stderr, err := captureStderr(t, func() error {
		var runErr error
		output, runErr = c.run(t, "send", goneConversation, "task", "--reply", "--timeout", "3s")
		return runErr
	})
	if err != nil || !strings.Contains(output, "agenthail last "+startedConversation) || !strings.Contains(stderr, "cannot follow it") {
		t.Fatalf("output=%q stderr=%q err=%v", output, stderr, err)
	}
}

func TestAReplyToAReplacedLeadConversationGoesToTheCurrentOne(t *testing.T) {
	c := newCoordinatorCLI(t).withGoneCoordinator(t)
	c.register(t, surface.Session{ID: replacedConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: c.claude.PID, Status: surface.StatusBusy, Cwd: c.cwd, Transcript: c.claude.Transcript(replacedConversation, c.cwd)})
	c.claude.RecordStatus(replacedConversation, c.cwd, "", "busy", "", time.Now())
	if _, err := c.r.HandOverCoordinator(c.spec(t), goneConversation, replacedConversation, registry.CoordinatorHandover{}); err != nil {
		t.Fatal(err)
	}
	c.answers(`{"ok":true,"result":{"evidence":"queued","status":"queued","sessionId":"` + replacedConversation + `","queueId":5}}`)
	output, err := c.run(t, "send", goneConversation, "follow up", "--reply", "--timeout", "3s")
	if err != nil || !strings.Contains(output, "queued #5") || !strings.Contains(output, "agenthail steer "+replacedConversation) || strings.Contains(output, goneConversation) {
		t.Fatalf("output=%q err=%v, want the reply addressed to the current conversation", output, err)
	}
	if sends := c.sends(t); len(sends) != 1 || sends[0]["name"] != "fixture-lead" {
		t.Fatalf("sends=%v", sends)
	}
}

func TestStartFromAReplacedCoordinatorConversationNamesTheNewOne(t *testing.T) {
	c := newCoordinatorCLI(t).withGoneCoordinator(t)
	c.register(t, surface.Session{ID: replacedConversation, Bridge: "session_replacement", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: exitedPID, Status: surface.StatusIdle, Cwd: c.cwd})
	if _, err := c.r.HandOverCoordinator(c.spec(t), goneConversation, replacedConversation, registry.CoordinatorHandover{}); err != nil {
		t.Fatal(err)
	}
	c.answers(startedAnswer)
	output, err := c.run(t, "send", goneConversation, "task")
	if err != nil || !strings.Contains(output, "agenthail last "+startedConversation) {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestLeadIsTheCoordinatorOfTheSendersProject(t *testing.T) {
	c := newCoordinatorCLI(t)
	alpha, beta := filepath.Join(c.cwd, "alpha"), filepath.Join(t.TempDir(), "beta")
	for _, dir := range []string{alpha, beta} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	c.project(t, "project-alpha", alpha, "alpha")
	c.project(t, "project-beta", beta, "beta")
	c.define(t, registry.CoordinatorSpec{ProjectID: "project-alpha", Name: "alpha-lead", Cwd: alpha}, "")
	c.define(t, registry.CoordinatorSpec{ProjectID: "project-beta", Name: "beta-lead", Cwd: beta}, "")
	c.answers(startedAnswer)
	const worker = "7b8c9d0e-1f2a-4b3c-9d4e-5f6a7b8c9d0e"
	c.register(t, surface.Session{ID: worker, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: exitedPID, Status: surface.StatusIdle, Cwd: beta})
	const removed = "8c9d0e1f-2a3b-4c4d-8e5f-6a7b8c9d0e1f"
	c.register(t, surface.Session{ID: removed, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: exitedPID, Status: surface.StatusIdle, Cwd: filepath.Join(beta, "removed-worktree")})

	for _, sender := range []string{worker, removed} {
		t.Setenv("CLAUDE_CODE_SESSION_ID", sender)
		if _, err := c.run(t, "send", "@lead", "beta report"); err != nil {
			t.Fatalf("%s could not reach its project's coordinator: %v", sender, err)
		}
	}
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Chdir(alpha)
	if _, err := c.run(t, "send", "@lead", "alpha report"); err != nil {
		t.Fatal(err)
	}
	sends := c.sends(t)
	if len(sends) != 3 || sends[0]["name"] != "beta-lead" || sends[1]["name"] != "beta-lead" || sends[2]["name"] != "alpha-lead" {
		t.Fatalf("sends=%v, want two reports for beta's coordinator and one for alpha's", sends)
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
	if sends := c.sends(t); len(sends) != 0 {
		t.Fatalf("sends=%v", sends)
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
