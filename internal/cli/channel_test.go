package cli

import (
	"strings"
	"testing"

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
