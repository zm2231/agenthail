package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestChannelSendFromAMemberSkipsItAndNamesIt(t *testing.T) {
	sessions := map[string]surface.Session{}
	for _, id := range []string{"alpha", "beta", "gamma"} {
		sessions[id] = surface.Session{ID: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle}
	}
	fake := &cliSurface{kind: surface.KindCodex, sessions: sessions, caps: surface.Capabilities{Send: true}}
	app, _ := cliFixture(t, fake)
	for _, args := range [][]string{{"channel", "create", "crew"}, {"channel", "add", "crew", "codex:alpha"}, {"channel", "add", "crew", "codex:beta"}, {"channel", "add", "crew", "codex:gamma"}, {"identify", "codex:alpha", "scout"}} {
		if _, err := captureStdout(t, func() error { return app.Run(args) }); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	t.Setenv("CODEX_THREAD_ID", "alpha")
	output, err := captureStdout(t, func() error { return app.Run([]string{"channel", "send", "crew", "standup"}) })
	if err != nil || !strings.Contains(output, "2 sent") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	if len(fake.sent) != 2 || fake.sent[0] != "[from @scout via #crew] standup" || fake.sent[1] != fake.sent[0] {
		t.Fatalf("sent=%q", fake.sent)
	}
}

func TestChannelSendToAProjectReachesItsActiveConversations(t *testing.T) {
	now := time.Now()
	sessions := map[string]surface.Session{
		"alpha": {ID: "alpha", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, LastActive: now},
		"beta":  {ID: "beta", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, LastActive: now},
		"gamma": {ID: "gamma", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, LastActive: now.Add(-48 * time.Hour)},
	}
	fake := &cliSurface{kind: surface.KindCodex, sessions: sessions, caps: surface.Capabilities{Send: true}}
	app, store := cliFixture(t, fake)
	for _, id := range []string{"alpha", "beta", "gamma"} {
		session := sessions[id]
		if err := store.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
		project := json.RawMessage(`{"id":"repo:github.com/example/widget","localId":"implicit:/work/widget","displayName":"widget"}`)
		if _, _, err := store.RecordCatalogSession(registry.CatalogSessionState{Session: session, Project: project, Checkout: json.RawMessage(`{}`), ObservedAt: now.UTC(), ProjectionFingerprint: id}, registry.CatalogEvent{DedupeKey: "session:" + id, Type: "session.upserted", EntityID: id, Payload: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"channel", "create", "widget"}) }); err == nil || !strings.Contains(err.Error(), "#widget already names project widget") {
		t.Fatalf("create with a project's name: err=%v", err)
	}
	t.Setenv("CODEX_THREAD_ID", "alpha")
	output, err := captureStdout(t, func() error { return app.Run([]string{"channel", "send", "#widget", "standup"}) })
	if err != nil || !strings.Contains(output, "#widget: 1 sent, 0 queued, 0 submitted, 0 failed") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	if len(fake.sent) != 1 || !strings.HasSuffix(fake.sent[0], " via #widget] standup") {
		t.Fatalf("sent=%q", fake.sent)
	}
}
