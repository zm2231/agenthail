package cli

import (
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestAFailedTurnEndReportsWhyItFailed(t *testing.T) {
	limit := "the Claude account is out of usage (You've hit your session limit); move the session to another account with: agenthail thread move <session> --account <name>"
	if err := directStreamTerminalError(surface.StreamEvent{Kind: "done", Status: "failed", Text: limit}, "u1"); err == nil || !strings.Contains(err.Error(), "out of usage") {
		t.Fatalf("direct err=%v", err)
	}
	if err := directStreamTerminalError(surface.StreamEvent{Kind: "done"}, "u1"); err != nil {
		t.Fatalf("a completed turn failed: %v", err)
	}
	if got := daemonStreamFailure(sessionstream.Event{Kind: "done", Status: "failed", Body: limit}); got != limit {
		t.Fatalf("daemon reason=%q", got)
	}
	if got := daemonStreamFailure(sessionstream.Event{Kind: "done", Status: "cancelled"}); got != "cancelled" {
		t.Fatalf("daemon reason=%q", got)
	}
}
