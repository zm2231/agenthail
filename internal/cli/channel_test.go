package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestChannelSendGoesThroughTheDaemonAsTheCaller(t *testing.T) {
	app, _, api := fanoutFixture(t)
	t.Setenv("CODEX_THREAD_ID", "s-scout")
	if _, err := captureStdout(t, func() error { return app.Run([]string{"channel", "send", "crew", "standup"}) }); err == nil {
		t.Fatal("the fixture's receipt has a failed member, so the command fails")
	}
	actions, keys := api.recorded()
	if len(actions) != 1 || actions[0]["action"] != "channel-send" || actions[0]["channel"] != "crew" || actions[0]["sourceSessionId"] != "s-scout" || !strings.HasPrefix(keys[0], "cli-") {
		t.Fatalf("actions=%v keys=%v, want one channel-send naming the calling session", actions, keys)
	}
}

func TestChannelSendRefusesWithTheDaemonStopped(t *testing.T) {
	app, _, _, api := federatedCLIFixture(t, false)
	if _, err := captureStdout(t, func() error { return app.Run([]string{"channel", "send", "#widget", "standup"}) }); err == nil || !strings.Contains(err.Error(), "agenthail daemon start") {
		t.Fatalf("err=%v, want a refusal that names how to start the daemon", err)
	}
	if actions, _ := api.recorded(); len(actions) != 0 {
		t.Fatalf("actions=%v", actions)
	}
}

func TestAChannelCannotTakeAProjectsName(t *testing.T) {
	now := time.Now()
	session := surface.Session{ID: "alpha", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, LastActive: now}
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"alpha": session}, caps: surface.Capabilities{Send: true}}
	app, store := cliFixture(t, fake)
	if err := store.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	project := json.RawMessage(`{"id":"repo:github.com/example/widget","localId":"implicit:/work/widget","displayName":"widget"}`)
	if _, _, err := store.RecordCatalogSession(registry.CatalogSessionState{Session: session, Project: project, Checkout: json.RawMessage(`{}`), ObservedAt: now.UTC(), ProjectionFingerprint: "alpha"}, registry.CatalogEvent{DedupeKey: "session:alpha", Type: "session.upserted", EntityID: "alpha", Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"channel", "create", "widget"}) }); err == nil || !strings.Contains(err.Error(), "#widget already names project widget") {
		t.Fatalf("create with a project's name: err=%v", err)
	}
}
