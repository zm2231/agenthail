package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/surface"
)

func stubDaemonRunning(t *testing.T, running bool) {
	t.Helper()
	was := daemonRunning
	daemonRunning = func() bool { return running }
	t.Cleanup(func() { daemonRunning = was })
}

func TestABusyNoQueueSendIsRefusedWithItsNextStepAndNothingSent(t *testing.T) {
	busy := surface.Session{ID: "target", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusBusy}
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"target": busy}, caps: surface.Capabilities{Send: true}, sendResult: &surface.SendResult{UUID: "turn", Accepted: false}}
	app, r := cliFixture(t, fake)
	_, err := captureStdout(t, func() error { return app.Run([]string{"send", "codex:target", "now please", "--no-queue"}) })
	if !errors.Is(err, delivery.ErrTargetBusy) {
		t.Fatalf("a busy --no-queue send must stay a busy refusal: %v", err)
	}
	for _, next := range []string{`agenthail queue codex:target "message"`, `agenthail steer codex:target "message"`} {
		if !strings.Contains(err.Error(), next) {
			t.Fatalf("the refusal does not name %q: %v", next, err)
		}
	}
	queued, err := r.ListQueue(true, nil)
	if err != nil || len(queued) != 0 {
		t.Fatalf("a refused --no-queue send was queued: %+v err=%v", queued, err)
	}
}

func TestTheSteerSettingSteersABusyNoQueueSendAndNeverQueuesIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".agenthail"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".agenthail", "dashboard.json"), []byte(`{"busyDelivery":"steer"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	busy := surface.Session{ID: "target", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Transport: "app-server", Status: surface.StatusBusy}
	for _, steers := range []bool{true, false} {
		fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"target": busy}, caps: surface.Capabilities{Send: true, Steer: steers}, sendResult: &surface.SendResult{UUID: "turn", Accepted: false}}
		app, r := cliFixture(t, fake)
		_, err := captureStdout(t, func() error { return app.Run([]string{"send", "codex:target", "change course", "--no-queue"}) })
		queued, listErr := r.ListQueue(true, nil)
		if listErr != nil || len(queued) != 0 {
			t.Fatalf("steers=%v: --no-queue queued %+v (err %v)", steers, queued, listErr)
		}
		if steers && (err != nil || len(fake.steered) != 1) {
			t.Fatalf("with the steer setting a busy target that takes steers is steered: steered=%q err=%v", fake.steered, err)
		}
		if !steers && (!errors.Is(err, delivery.ErrTargetBusy) || len(fake.steered) != 0) {
			t.Fatalf("a busy target that takes no steer is refused: steered=%q err=%v", fake.steered, err)
		}
	}
}

func TestADaemonRefusalOfABusyCoordinatorIsStillRecognised(t *testing.T) {
	if !isTargetBusy(errors.New(delivery.ErrTargetBusy.Error())) {
		t.Fatal("the daemon's busy refusal, which arrives as text, is not recognised")
	}
	if isTargetBusy(errors.New("session is not running")) {
		t.Fatal("another refusal is taken for busy")
	}
}

func TestASteerHeldForAnAnswerWarnsWhenTheDaemonIsDown(t *testing.T) {
	waiting := &surface.Waiting{Kind: surface.WaitingQuestion, ID: "toolu_synthetic"}
	asking := surface.Session{ID: "asking", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Transport: "app-server", Status: surface.StatusBusy}
	for _, running := range []bool{false, true} {
		fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"asking": asking}, caps: surface.Capabilities{Send: true, Steer: true}, observation: &surface.TurnObservation{WaitingOn: waiting}}
		app, _ := cliFixture(t, fake)
		stubDaemonRunning(t, running)
		var stdout string
		stderr, err := captureStderr(t, func() error {
			var runErr error
			stdout, runErr = captureStdout(t, func() error { return app.Run([]string{"steer", "codex:asking", "use the cache"}) })
			return runErr
		})
		if err != nil || !strings.Contains(stdout, "waiting on you") || len(fake.steered) != 0 {
			t.Fatalf("running=%v: a steer to a session waiting on its user is held: stdout=%q steered=%q err=%v", running, stdout, fake.steered, err)
		}
		if warned := strings.Contains(stderr, queuedWithoutDaemon); warned == running {
			t.Fatalf("running=%v: warning shown=%v, stderr=%q", running, warned, stderr)
		}
	}
}

func TestOnlyAQueuedSendWarnsThatTheDaemonIsDown(t *testing.T) {
	for _, busy := range []bool{false, true} {
		target := surface.Session{ID: "target", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Transport: "app-server", Status: surface.StatusIdle}
		fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"target": target}, caps: surface.Capabilities{Send: true}}
		if busy {
			fake.sendResult = &surface.SendResult{UUID: "turn", Accepted: false}
		}
		app, _ := cliFixture(t, fake)
		stubDaemonRunning(t, false)
		stderr, err := captureStderr(t, func() error {
			_, runErr := captureStdout(t, func() error { return app.Run([]string{"send", "codex:target", "hello"}) })
			return runErr
		})
		if err != nil {
			t.Fatalf("busy=%v: %v", busy, err)
		}
		if warned := strings.Contains(stderr, queuedWithoutDaemon); warned != busy {
			t.Fatalf("busy=%v: warned=%v, want a warning only for the queued send; stderr=%q", busy, warned, stderr)
		}
	}
}

func TestSteeringAnIdleSessionSaysToSend(t *testing.T) {
	idle := surface.Session{ID: "idle", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Transport: "app-server", Status: surface.StatusIdle}
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"idle": idle}, caps: surface.Capabilities{Send: true, Steer: true}, steerErr: surface.ErrNothingToSteer}
	app, _ := cliFixture(t, fake)
	_, err := captureStdout(t, func() error { return app.Run([]string{"steer", "codex:idle", "use the cache"}) })
	if !errors.Is(err, surface.ErrNothingToSteer) || !strings.Contains(err.Error(), `agenthail send codex:idle "message"`) {
		t.Fatalf("an idle steer does not name send: %v", err)
	}
}

func TestARemoteIdleSteerSaysToSend(t *testing.T) {
	app, _, _, api := federatedCLIFixture(t, true)
	api.responses = map[string]string{"steer": `{"error":{"code":"invalid","message":"` + surface.ErrNothingToSteer.Error() + `"}}`}
	api.statuses = map[string]int{"steer": 409}
	_, err := captureStdout(t, func() error { return app.Run([]string{"steer", "vps::@builder", "use the cache"}) })
	if !errors.Is(err, surface.ErrNothingToSteer) || !strings.Contains(err.Error(), `agenthail send vps::@builder "message"`) {
		t.Fatalf("a remote idle steer does not name send: %v", err)
	}
}

func TestARemoteSteerCarriesTheAgentThatSentIt(t *testing.T) {
	for _, identity := range surface.CallerIdentity {
		t.Setenv(identity.Variable, "")
	}
	app, r, _, api := federatedCLIFixture(t, true)
	if _, err := captureStdout(t, func() error { return app.Run([]string{"steer", "vps::@builder", "from the CLI"}) }); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterSession(surface.Session{ID: "worker", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_SESSION_ID", "worker")
	if _, err := captureStdout(t, func() error { return app.Run([]string{"steer", "vps::@builder", "from the worker"}) }); err != nil {
		t.Fatal(err)
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"steer", "vps::@builder", "  "}) }); err == nil {
		t.Fatal("an empty remote steer was posted")
	}
	actions, _ := api.recorded()
	if len(actions) != 2 || actions[0]["action"] != "steer" || actions[1]["action"] != "steer" {
		t.Fatalf("actions=%v, want two steers", actions)
	}
	if _, hasSource := actions[0]["sourceSessionId"]; hasSource {
		t.Fatalf("a steer run outside any agent session carried a source: %v", actions[0])
	}
	if actions[1]["sourceSessionId"] != "worker" || actions[1]["message"] != "from the worker" {
		t.Fatalf("the daemon cannot gate a remote steer it does not know the sender of: %v", actions[1])
	}
}

func TestUncertainOutcomesSayToReadTheSessionFirst(t *testing.T) {
	remote, _, _, api := federatedCLIFixture(t, true)
	api.response = `{"ok":true,"status":"submitted"}`
	output, err := captureStdout(t, func() error { return remote.Run([]string{"send", "vps::@builder", "hello vps"}) })
	if err != nil || !strings.Contains(output, "agenthail last vps::@builder") {
		t.Fatalf("an unknown remote send does not name last: %q err=%v", output, err)
	}
	output, err = captureStdout(t, func() error { return remote.Run([]string{"steer", "vps::@builder", "use the cache"}) })
	if err != nil || !strings.Contains(output, "agenthail last vps::@builder") {
		t.Fatalf("an unknown remote steer does not name last: %q err=%v", output, err)
	}

	lost := surface.DeliveryOutcomeUnknown(errors.New("the acknowledgement was lost"))
	busy := surface.Session{ID: "target", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Transport: "app-server", Status: surface.StatusBusy}
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"target": busy}, caps: surface.Capabilities{Send: true, Steer: true}, sendErr: lost, steerErr: lost}
	local, _ := cliFixture(t, fake)
	for _, command := range [][]string{{"send", "codex:target", "hello"}, {"steer", "codex:target", "use the cache"}} {
		output, err := captureStdout(t, func() error { return local.Run(command) })
		if err != nil || !strings.Contains(output, "agenthail last") {
			t.Fatalf("%s with an unknown outcome does not name last: %q err=%v", command[0], output, err)
		}
	}
}

func TestASteerFromAnAgentThatMayNotSteerGoesAsASend(t *testing.T) {
	idle := surface.Session{ID: "target", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Transport: "app-server", Status: surface.StatusIdle}
	worker := surface.Session{ID: "worker", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Transport: "app-server", Status: surface.StatusBusy}
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"target": idle, "worker": worker}, caps: surface.Capabilities{Send: true, Steer: true}}
	app, r := cliFixture(t, fake)
	for _, session := range []surface.Session{idle, worker} {
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("AGENTHAIL_SESSION_ID", "worker")
	output, err := captureStdout(t, func() error { return app.Run([]string{"steer", "codex:target", "change course"}) })
	if err != nil || len(fake.steered) != 0 || len(fake.sent) != 1 || !strings.Contains(output, "not a steer") {
		t.Fatalf("an unauthorised steer to an idle target: output=%q sent=%q steered=%q err=%v", output, fake.sent, fake.steered, err)
	}
}

func TestASteerRunOutsideAnyAgentSessionReachesTheBusyTurn(t *testing.T) {
	for _, identity := range surface.CallerIdentity {
		t.Setenv(identity.Variable, "")
	}
	busy := surface.Session{ID: "target", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Transport: "app-server", Status: surface.StatusBusy}
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"target": busy}, caps: surface.Capabilities{Send: true, Steer: true}}
	app, r := cliFixture(t, fake)
	output, err := captureStdout(t, func() error { return app.Run([]string{"steer", "codex:target", "change course"}) })
	queued, listErr := r.ListQueue(true, nil)
	if err != nil || len(fake.steered) != 1 || len(fake.sent) != 0 || listErr != nil || len(queued) != 0 || strings.Contains(output, "not a steer") {
		t.Fatalf("a plain CLI steer is steered: output=%q steered=%q sent=%q queued=%+v err=%v", output, fake.steered, fake.sent, queued, err)
	}
}

func TestHelpAndTheSkillNameTheSameDeliveryNextSteps(t *testing.T) {
	help, err := captureStdout(t, func() error { return (&App{}).Run([]string{"help"}) })
	if err != nil {
		t.Fatal(err)
	}
	skill, err := os.ReadFile("../../skills/agenthail-operations/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, next := range []string{
		`agenthail queue <target> "msg"`,
		`agenthail steer <target> "msg"`,
		`agenthail send <target> "msg"`,
		"agenthail requests <target>",
		"agenthail daemon start",
		"agenthail last <target>",
	} {
		if !strings.Contains(help, next) || !strings.Contains(string(skill), next) {
			t.Errorf("help has %q: %v; the skill has it: %v", next, strings.Contains(help, next), strings.Contains(string(skill), next))
		}
	}
	for _, outcome := range []string{"--no-queue", "read-only", "submitted or unknown", "daemon not running", "waiting on you"} {
		if !strings.Contains(help, outcome) {
			t.Errorf("help does not cover %q", outcome)
		}
	}
}
