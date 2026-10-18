package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func (f *cliSurface) ReadCompletions(context.Context, *surface.Session, string) (surface.CompletionBatch, error) {
	return surface.CompletionBatch{Cursor: `"baseline"`}, nil
}

func TestRelayCLIConditionPersistsAndAppearsInBothOutputs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	from := surface.Session{ID: "from", Surface: surface.KindCodex, Transport: "desktop", Source: "vscode"}
	to := from
	to.ID = "to"
	fake := &cliSurface{kind: surface.KindCodex, caps: surface.Capabilities{Send: true, Goal: true}, sessions: map[string]surface.Session{"from": from, "to": to}}
	app, r := cliFixture(t, fake)
	for _, s := range []surface.Session{from, to} {
		if err := r.RegisterSession(s); err != nil {
			t.Fatal(err)
		}
	}
	output, err := captureStdout(t, func() error {
		return app.Run([]string{"relay", "add", "from", "to", "READY", "--once", "--when", "goal_completed"})
	})
	if err != nil || !strings.Contains(output, "goal_completed") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	for _, args := range [][]string{{"relay", "add", "from", "to", "--when", "wrong"}, {"relay", "add", "from", "to", "--when"}} {
		if err := app.Run(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	output, err = captureStdout(t, func() error { return app.Run([]string{"relay", "list", "--json"}) })
	var decoded struct {
		Relays []registry.RouteRow `json:"relays"`
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(output), &decoded); err != nil || len(decoded.Relays) != 1 || decoded.Relays[0].Handoff != "goal_completed" || !decoded.Relays[0].Once {
		t.Fatalf("output=%s err=%v", output, err)
	}
	output, err = captureStdout(t, func() error { return app.Run([]string{"relay", "list"}) })
	if err != nil || !strings.Contains(output, "when=goal_completed pending=0") {
		t.Fatalf("output=%q err=%v", output, err)
	}
}
