package cli

import (
	"context"
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

	identity, err := r.AgentIdentityNamed("scout")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := captureStdout(t, func() error { return app.Run([]string{"send", identity.SessionID, "by its id"}) }); err != nil || !strings.Contains(out, "inbox of @scout") {
		t.Fatalf("send to the identity's id %s: out=%q err=%v", identity.SessionID, out, err)
	}
	if _, err := captureStdout(t, func() error {
		return app.Run([]string{"send", "--from", identity.SessionID, "target", "from its id"})
	}); err != nil || len(fake.sent) != 2 || !strings.Contains(fake.sent[1], "from its id") {
		t.Fatalf("send from the identity's id: err=%v sent=%v", err, fake.sent)
	}
	t.Setenv("AGENTHAIL_SESSION_ID", identity.SessionID)
	if app.agentEnvironment() {
		t.Fatal("the identity's id in AGENTHAIL_SESSION_ID was treated as an agent session")
	}
	if id, err := app.sourceSessionID(context.Background(), ""); err != nil || id != identity.SessionID {
		t.Fatalf("sender from the identity's id=%q err=%v", id, err)
	}

	t.Setenv("AGENTHAIL_SESSION_ID", "scout")
	out, err = captureStdout(t, func() error { return app.Run([]string{"inbox", "--all", "--peek"}) })
	if err != nil || !strings.Contains(out, "by its id") {
		t.Fatalf("inbox after a send by id: out=%q err=%v", out, err)
	}
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
