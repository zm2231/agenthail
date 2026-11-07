package cli

import (
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestIdentifyListNamesTheSessionEachNamePointsAt(t *testing.T) {
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{
		"alpha": {ID: "alpha", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "Builder", Status: surface.StatusIdle},
	}, caps: surface.Capabilities{Send: true}}
	app, _ := cliFixture(t, fake)
	if _, err := captureStdout(t, func() error { return app.Run([]string{"identify", "codex:alpha", "a1"}) }); err != nil {
		t.Fatal(err)
	}
	output, err := captureStdout(t, func() error { return app.Run([]string{"identify", "list"}) })
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(output)
	if len(fields) != 2 || fields[0] != "@a1" || fields[1] != "codex/Builder" {
		t.Fatalf("identify list = %q, want @a1 and the session it names", output)
	}
}
