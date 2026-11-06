package surfaces

import (
	"context"
	"slices"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestANewCodexThreadIsRecordedBeforeItsFirstTurnStarts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	bridge := startDesktopBridge(t, func(method string) string {
		switch method {
		case "thread/start":
			return `{"result":{"cwd":"/tmp/project","thread":{"id":"desktop-new","name":"","source":"vscode"}}}`
		case "turn/start":
			return `{"result":{"turn":{"id":"desktop-turn"}}}`
		}
		return ""
	})
	var readyID string
	var sentBeforeReady []string
	ctx := surface.WithSessionReady(context.Background(), func(session surface.Session) error {
		readyID = session.ID
		sentBeforeReady = bridge.Methods()
		return nil
	})
	if _, _, err := NewCodex(bridge.URL).StartSession(ctx, surface.SessionStartOptions{Message: "Build the release", Cwd: "/tmp/project"}); err != nil {
		t.Fatal(err)
	}
	if readyID != "desktop-new" || !slices.Contains(sentBeforeReady, "thread/start") || slices.Contains(sentBeforeReady, "turn/start") {
		t.Fatalf("ready for %q after %v", readyID, sentBeforeReady)
	}
	if !slices.Contains(bridge.Methods(), "turn/start") {
		t.Fatalf("the first turn never started: %v", bridge.Methods())
	}
}
