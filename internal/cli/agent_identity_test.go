package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/agentinbox"
	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestAnAgentIdentitySendsWithTheCLIsReachAndReadsItsInbox(t *testing.T) {
	fake := &cliSurface{
		kind: surface.KindCodex,
		sessions: map[string]surface.Session{
			"target": {ID: "target", Name: "Target", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Source: "vscode", Transport: "desktop", Status: surface.StatusIdle},
		},
		caps: surface.Capabilities{Send: true},
	}
	app, r := cliFixture(t, fake)
	app.Surfaces = append(app.Surfaces, SurfaceEntry{Name: "agent", Surface: agentinbox.New(func() *registry.Registry { return r })})
	if err := r.RecordObservedSession(fake.sessions["target"]); err != nil {
		t.Fatal(err)
	}
	scoped := 0
	app.scopeOverride = func(daemon.ScopeRequest) (*daemon.ScopeReply, error) {
		scoped++
		return nil, errors.New("an identity's send was checked as an agent session's")
	}
	if out, err := captureStdout(t, func() error { return app.Run([]string{"identity", "add", "scout"}) }); err != nil || !strings.Contains(out, "added identity @scout") {
		t.Fatalf("identity add: out=%q err=%v", out, err)
	}

	t.Setenv("AGENTHAIL_SESSION_ID", "scout")
	if app.agentEnvironment() {
		t.Fatal("an identity in AGENTHAIL_SESSION_ID was treated as an agent session")
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"send", "target", "hello from scout"}) }); err != nil || scoped != 0 {
		t.Fatalf("send as scout: err=%v scope checks=%d", err, scoped)
	}
	if len(fake.sent) != 1 || !strings.Contains(fake.sent[0], "hello from scout") {
		t.Fatalf("target received %v", fake.sent)
	}

	t.Setenv("AGENTHAIL_SESSION_ID", "")
	out, err := captureStdout(t, func() error { return app.Run([]string{"send", "@scout", "a note for scout"}) })
	if err != nil || !strings.Contains(out, "Delivered to the inbox of @scout") {
		t.Fatalf("send to scout: out=%q err=%v", out, err)
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"send", "@scout", "--reply", "answer me"}) }); err == nil || !strings.Contains(err.Error(), "nothing replies") {
		t.Fatalf("send --reply to an identity: %v", err)
	}

	t.Setenv("AGENTHAIL_SESSION_ID", "scout")
	out, err = captureStdout(t, func() error { return app.Run([]string{"inbox"}) })
	if err != nil || !strings.Contains(out, "a note for scout") || !strings.Contains(out, "from Agenthail CLI") || strings.Contains(out, "answer me") {
		t.Fatalf("inbox: out=%q err=%v", out, err)
	}
	if out, err = captureStdout(t, func() error { return app.Run([]string{"inbox", "--wait", "50ms"}) }); err == nil || !strings.Contains(err.Error(), "no message for @scout") {
		t.Fatalf("a second read waits and times out: out=%q err=%v", out, err)
	}

	t.Setenv("AGENTHAIL_SESSION_ID", "target")
	if !app.agentEnvironment() {
		t.Fatal("a session in AGENTHAIL_SESSION_ID lost its agent reach checks")
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"inbox"}) }); err == nil || !strings.Contains(err.Error(), "not an agent identity") {
		t.Fatalf("inbox for a session: %v", err)
	}
}
