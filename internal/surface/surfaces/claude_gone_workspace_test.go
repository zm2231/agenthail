package surfaces

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestASendToASessionWhoseWorkspaceIsGoneSaysSo(t *testing.T) {
	fixture, host, c := hostResume(t)
	gone := filepath.Join(t.TempDir(), "removed-worktree")
	if err := os.Mkdir(gone, 0700); err != nil {
		t.Fatal(err)
	}
	session := exitedSession(fixture)
	session.Cwd = gone
	session.Transcript = fixture.Transcript(fixtureConversation, gone)
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	_, err := c.SendHumanTurn(surface.WithMessageUUID(context.Background(), uuid.NewString()), &session, "are you there", nil)
	if surface.DeliveryTerminalReason(err) != surface.DeliveryInvalidRequest || !strings.Contains(err.Error(), gone) {
		t.Fatalf("err=%v, want a terminal error that names the missing folder", err)
	}
	if spawns := host.Spawns(t, fixtureConversation); len(spawns) != 0 {
		t.Fatalf("spawns=%+v", spawns)
	}
}
