package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type adopterSurface struct {
	*cliSurface
	busy    map[string]string
	adopted []string
	waits   []time.Duration
}

func (a *adopterSurface) AdoptSession(_ context.Context, sess *surface.Session, request surface.AdoptRequest) (*surface.AdoptResult, error) {
	a.waits = append(a.waits, request.Wait)
	if reason := a.busy[sess.ID]; reason != "" {
		return nil, surface.DeliveryTerminal(&surface.AdoptBusyError{Session: sess.ID, Reason: reason}, surface.DeliveryInvalidRequest)
	}
	if !request.StopTerminal {
		return nil, surface.DeliveryTerminal(&surface.AdoptConsentError{Session: sess.ID, PID: 42}, surface.DeliveryInvalidRequest)
	}
	a.adopted = append(a.adopted, sess.ID)
	adopted := *sess
	adopted.RuntimeKind = surface.RuntimeClaudeHost
	return &surface.AdoptResult{Session: &adopted, Stopped: "terminal process 42"}, nil
}

func TestThreadAdoptOpensASessionInTheHostAndReRegistersIt(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	fake := &cliSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{}}
	adopter := &adopterSurface{cliSurface: fake, busy: map[string]string{"busy1": "a turn is running"}}
	app, store := cliFixture(t, fake)
	app.Surfaces = []SurfaceEntry{{Name: "claude", Surface: adopter}}
	for _, id := range []string{"c1", "busy1"} {
		session := surface.Session{ID: id, Name: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI}
		fake.sessions[id] = session
		if err := store.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	app.catalogDaemonRunning = func() bool { return false }
	if err := app.Run([]string{"thread", "adopt", "c1", "--stop-terminal"}); err == nil || !strings.Contains(err.Error(), "agenthail daemon start") || len(adopter.adopted) != 0 {
		t.Fatalf("err=%v adopted=%v, want the terminal left running and the daemon-start hint", err, adopter.adopted)
	}
	serveAppDaemon(t, app, store, []surface.Surface{adopter})
	if err := app.Run([]string{"thread", "adopt", "c1"}); err == nil || !strings.Contains(err.Error(), "--stop-terminal") {
		t.Fatalf("a terminal session was adopted without consent: %v", err)
	}
	output, err := captureStdout(t, func() error { return app.Run([]string{"thread", "adopt", "c1", "--stop-terminal"}) })
	if err != nil || !strings.Contains(output, "opened c1") || !strings.Contains(output, "terminal process 42") {
		t.Fatalf("output=%s err=%v", output, err)
	}
	stored, err := store.Session("c1")
	if err != nil || stored.RuntimeKind != surface.RuntimeClaudeHost {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
	if err := app.Run([]string{"thread", "adopt", "busy1", "--stop-terminal", "--wait", "90s"}); err == nil || !strings.Contains(err.Error(), "a turn is running") || adopter.waits[len(adopter.waits)-1] != 90*time.Second {
		t.Fatalf("busy adoption: err=%v waits=%v, want the refusal after waiting 90s", err, adopter.waits)
	}
	for _, args := range [][]string{{"thread", "adopt"}, {"thread", "adopt", "c1", "--root", "spare"}, {"thread", "adopt", "c1", "--wait", "soon"}, {"thread", "adopt", "c1", "--stop-terminal", "--wait"}, {"thread", "adopt", "c1", "--stop-terminal", "--timeout"}, {"thread", "adopt", "c1", "--wait", "--stop-terminal"}} {
		if err := app.Run(args); err == nil {
			t.Fatalf("%v was accepted", args)
		}
	}
	for _, args := range [][]string{{"thread", "adopt", "c1", "--json", "--root", "spare"}, {"thread", "adopt", "missing", "--json"}, {"thread", "adopt", "busy1", "--stop-terminal", "--json"}} {
		output, err := captureStdout(t, func() error { return app.Run(args) })
		var reply struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		}
		if err == nil || json.Unmarshal([]byte(output), &reply) != nil || reply.OK || reply.Error == "" {
			t.Fatalf("%v: output=%q err=%v", args, output, err)
		}
	}
	if len(adopter.adopted) != 1 {
		t.Fatalf("adopted=%v", adopter.adopted)
	}
}
