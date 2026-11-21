package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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

func TestARemovedIdentityCannotSendAsItself(t *testing.T) {
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
	app.scopeOverride = func(daemon.ScopeRequest) (*daemon.ScopeReply, error) {
		return nil, errors.New("the daemon scope check ran")
	}
	identity, err := r.AddAgentIdentity("kat")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.RemoveAgentIdentity("kat"); err != nil {
		t.Fatal(err)
	}
	for _, sender := range []string{"kat", identity.SessionID} {
		t.Setenv("AGENTHAIL_SESSION_ID", sender)
		_, err := captureStdout(t, func() error { return app.Run([]string{"send", "target", "hello from " + sender}) })
		if err == nil || !strings.Contains(err.Error(), "removed") {
			t.Fatalf("send with AGENTHAIL_SESSION_ID=%s: err=%v", sender, err)
		}
	}
	for _, caller := range []string{"", "target"} {
		t.Setenv("AGENTHAIL_SESSION_ID", caller)
		for _, sender := range []string{"kat", identity.SessionID} {
			if _, err := captureStdout(t, func() error { return app.Run([]string{"send", "--from", sender, "target", "from a flag"}) }); err == nil || !strings.Contains(err.Error(), "removed") {
				t.Fatalf("send --from %s as %q: err=%v", sender, caller, err)
			}
		}
	}
	if len(fake.sent) != 0 {
		t.Fatalf("a removed identity sent %v", fake.sent)
	}
}

func TestIdentityRemoteShowsItsTokenOnceAndDoctorReportsTheMode(t *testing.T) {
	app, r := cliFixture(t, &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{}})
	run := func(args ...string) string {
		t.Helper()
		out, err := captureStdout(t, func() error { return app.Run(args) })
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return out
	}
	run("identity", "add", "scout")
	if health, err := remoteMCPHealthOf(r, time.Now()); err != nil || health.Mode != "off" {
		t.Fatalf("health=%+v err=%v", health, err)
	}
	out := run("identity", "remote", "scout")
	token := ""
	for field := range strings.FieldsSeq(out) {
		if strings.HasPrefix(field, registry.RemoteTokenPrefix) {
			token = field
		}
	}
	if token == "" || !strings.Contains(out, "token alone gets in") {
		t.Fatalf("identity remote: %q", out)
	}
	if remote, err := r.RemoteIdentityByToken(token); err != nil || remote.Name != "scout" {
		t.Fatalf("remote=%+v err=%v", remote, err)
	}
	if listed := run("identity", "list"); strings.Contains(listed, token) || !strings.Contains(listed, "remote: token only") {
		t.Fatalf("identity list: %q", listed)
	}
	if health, _ := remoteMCPHealthOf(r, time.Now()); health.Mode != "token only" || !strings.Contains(health.String(), "@scout: token only") {
		t.Fatalf("health=%s", health)
	}

	run("identity", "access", "set", "--team", "https://Example-Team.cloudflareaccess.com/", "--aud", "tag")
	if team, aud, ok, err := r.AccessSettings(); err != nil || !ok || team != "example-team.cloudflareaccess.com" || aud != "tag" {
		t.Fatalf("team=%q aud=%q ok=%v err=%v", team, aud, ok, err)
	}
	health, _ := remoteMCPHealthOf(r, time.Now())
	if health.Mode != "access+token" || len(health.Problems) != 1 || !strings.Contains(health.Problems[0], "no Access binding") {
		t.Fatalf("health=%+v", health)
	}
	out = run("identity", "remote", "scout", "--access-client-id", "ID-1.access")
	if strings.Contains(out, token) || !strings.Contains(out, "service token id-1") {
		t.Fatalf("rebind: %q", out)
	}
	if _, err := r.RemoteIdentityByToken(token); !errors.Is(err, registry.ErrRemoteUnknown) {
		t.Fatalf("the first token still works after a rebind: %v", err)
	}
	if health, _ := remoteMCPHealthOf(r, time.Now()); len(health.Problems) != 0 {
		t.Fatalf("problems=%v", health.Problems)
	}
	if _, err := captureStdout(t, func() error {
		return app.Run([]string{"identity", "remote", "scout", "--access-client-id", "a", "--access-email", "b@example.com"})
	}); err == nil {
		t.Fatal("bound both a client id and an email")
	}
	run("identity", "remote", "scout", "--off")
	if _, err := r.RemoteIdentityNamed("scout"); err == nil {
		t.Fatal("remote access still on after --off")
	}
	run("identity", "access", "clear")
	if _, _, ok, _ := r.AccessSettings(); ok {
		t.Fatal("Access settings still set after clear")
	}
}

func TestInboxPruneTakesItsAgeAsTheFlagsValue(t *testing.T) {
	app, r := cliFixture(t, &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{}})
	identity, err := r.AddAgentIdentity("scout")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.StoreInboxMessage(registry.InboxMessage{Identity: identity.SessionID, UUID: "old", Message: "old", ReceivedAt: time.Now().Add(-48 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ReadInbox("scout", registry.InboxQuery{}); err != nil {
		t.Fatal(err)
	}
	if out, err := captureStdout(t, func() error { return app.Run([]string{"inbox", "scout", "--prune", "1d"}) }); err != nil {
		t.Fatalf("inbox scout --prune 1d: out=%q err=%v", out, err)
	}
	t.Setenv("AGENTHAIL_SESSION_ID", "scout")
	if out, err := captureStdout(t, func() error { return app.Run([]string{"inbox", "--prune", "1d"}) }); err != nil {
		t.Fatalf("inbox --prune 1d: out=%q err=%v", out, err)
	}
	if all, err := r.ReadInbox("scout", registry.InboxQuery{All: true, Peek: true}); err != nil || len(all) != 0 {
		t.Fatalf("inbox after prune=%v err=%v", all, err)
	}
}
