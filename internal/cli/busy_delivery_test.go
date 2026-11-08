package cli

import (
	"errors"
	"os"
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
