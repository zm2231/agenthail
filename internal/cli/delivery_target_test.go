package cli

import (
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestDeliveryCommandsRefuseANameFragmentAndListWhatItMatched(t *testing.T) {
	const hotfix = "019fa001-0000-7000-8000-000000000001"
	const source = "019fa002-0000-7000-8000-000000000002"
	sessions := map[string]surface.Session{
		hotfix: {ID: hotfix, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Name: "voice hotfix show proof", Cwd: "/work/voice", Status: surface.StatusIdle, Transport: "managed"},
		source: {ID: source, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Name: "relay source", Cwd: "/work/source", Status: surface.StatusIdle, Transport: "managed"},
	}
	fake := &cliSurface{kind: surface.KindCodex, sessions: sessions, caps: surface.Capabilities{Send: true, Steer: true}}
	app, r := cliFixture(t, fake)
	for _, session := range sessions {
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.SetAlias("hotfix", hotfix); err != nil {
		t.Fatal(err)
	}
	if err := app.Run([]string{"channel", "create", "launch"}); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		{"queue", "show", "1774"},
		{"send", "show", "1774"},
		{"steer", "show", "1774"},
		{"send", "codex:show", "1774"},
		{"relay", "add", source, "show"},
		{"channel", "add", "launch", "show"},
	} {
		_, err := captureStdout(t, func() error { return app.Run(args) })
		if err == nil || !strings.Contains(err.Error(), "nothing was delivered") || !strings.Contains(err.Error(), "codex:"+hotfix+"  @hotfix") {
			t.Fatalf("%v err=%v, want a refusal naming %s and @hotfix", args, err, hotfix)
		}
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"queue", "show", "1774"}) }); err == nil || !strings.Contains(err.Error(), "agenthail queue list") {
		t.Fatalf("queue refusal err=%v, want the queue usage", err)
	}
	if len(fake.sent) != 0 || len(fake.steered) != 0 || r.QueueCount(hotfix) != 0 {
		t.Fatalf("a fragment delivered: sent=%v steered=%v queued=%d", fake.sent, fake.steered, r.QueueCount(hotfix))
	}
	if routes, err := r.ListRoutes(); err != nil || len(routes) != 0 {
		t.Fatalf("a fragment created a relay: routes=%v err=%v", routes, err)
	}
	if channels, err := r.ListChannels(); err != nil || len(channels) != 1 || len(channels[0].Members) != 0 {
		t.Fatalf("a fragment joined a channel: %+v err=%v", channels, err)
	}

	for _, args := range [][]string{
		{"queue", "@hotfix", "by alias"},
		{"queue", "hotfix", "by bare alias"},
		{"queue", hotfix, "by id"},
		{"queue", hotfix[:12], "by unique id prefix"},
	} {
		if _, err := captureStdout(t, func() error { return app.Run(args) }); err != nil {
			t.Fatalf("%v err=%v", args, err)
		}
	}
	if count := r.QueueCount(hotfix); count != 4 {
		t.Fatalf("exact targets queued %d messages, want 4", count)
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"channel", "add", "launch", "@hotfix"}) }); err != nil {
		t.Fatalf("channel add by alias err=%v", err)
	}
}
