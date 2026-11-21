package surfaces

import (
	"context"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestAClaudeSteerOrPeerMessageThatStartsWithAPathIsAMessageNotASlashCommand(t *testing.T) {
	adapter := NewClaude(nil, t.TempDir())
	ctx := context.Background()
	hosted := &surface.Session{ID: "hosted", Surface: surface.KindClaude}
	peer := &surface.Session{ID: "peer", Transport: "uds", Surface: surface.KindClaude}
	refusedAsCommand := func(err error) bool {
		return err != nil && strings.Contains(err.Error(), "slash command")
	}
	for _, text := range []string{"/compact", "/compact now", " /my-skill:run fast"} {
		if _, err := adapter.Control(ctx, hosted, surface.ControlRequest{Action: "steer", Value: text}); !refusedAsCommand(err) {
			t.Errorf("steer %q: %v, want refused as a slash command", text, err)
		}
		if _, err := adapter.Send(ctx, peer, text); !refusedAsCommand(err) {
			t.Errorf("peer send %q: %v, want refused as a slash command", text, err)
		}
	}
	for _, text := range []string{"/Volumes/4/CF holds 680 GiB of footage", "/tmp/build.log failed at line 3", "/ is the root"} {
		if _, err := adapter.Control(ctx, hosted, surface.ControlRequest{Action: "steer", Value: text}); refusedAsCommand(err) {
			t.Errorf("steer %q: %v, want it sent as a message", text, err)
		}
		if _, err := adapter.Send(ctx, peer, text); refusedAsCommand(err) {
			t.Errorf("peer send %q: %v, want it sent as a message", text, err)
		}
	}
}
