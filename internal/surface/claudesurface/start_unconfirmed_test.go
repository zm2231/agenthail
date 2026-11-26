package claudesurface

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfacetest"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestASlowClaudeStartOpensAndTakesItsFirstTurnPastItsCallersDeadline(t *testing.T) {
	t.Setenv("FAKE_CLAUDE_INITIALIZE_MS", "2000")
	host := claudehosttest.New(t, "")
	host.Start(t)
	c := surfacetest.InDaemon(NewClaude(nil, host.Home))
	turn := uuid.NewString()
	var readied []string
	ctx := surface.WithSessionReady(surface.WithMessageUUID(context.Background(), turn), func(started surface.Session) error {
		readied = append(readied, started.ID)
		return nil
	})
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	session, sent, err := c.StartSession(ctx, surface.SessionStartOptions{Message: "build it", Cwd: host.Cwd})
	if err != nil || session == nil || sent == nil || !sent.Accepted || len(readied) != 1 || readied[0] != session.ID {
		t.Fatalf("session=%+v sent=%+v readied=%v err=%v, want the slow start opened, recorded and sent", session, sent, readied, err)
	}
	testkit.Eventually(t, "the first turn", 15*time.Second, func() bool {
		turns := host.Turns(t, session.ID)
		return len(turns) == 1 && turns[0].UUID == turn
	})
}

func TestAnOpenTheHostDoesNotConfirmIsClosedThereAndStartsNothing(t *testing.T) {
	t.Setenv("FAKE_CLAUDE_INITIALIZE_MS", "30000")
	restore := claudeStartTimeout
	claudeStartTimeout = time.Second
	t.Cleanup(func() { claudeStartTimeout = restore })
	host := claudehosttest.New(t, "")
	client := host.Start(t)
	c := surfacetest.InDaemon(NewClaude(nil, host.Home))
	readied := 0
	ctx := surface.WithSessionReady(context.Background(), func(surface.Session) error {
		readied++
		return nil
	})
	session, sent, err := c.StartSession(ctx, surface.SessionStartOptions{Message: "build it", Cwd: host.Cwd})
	if err == nil || session != nil || sent != nil || readied != 0 || surface.IsDeliveryOutcomeUnknown(err) {
		t.Fatalf("session=%+v sent=%+v readied=%d err=%v, want no session and a definite failure", session, sent, readied, err)
	}
	spawned := spawnedSessions(t, host)
	if len(spawned) != 1 {
		t.Fatalf("spawned=%v, want the one open the host began", spawned)
	}
	testkit.Eventually(t, "the unconfirmed conversation to be gone from the host", 15*time.Second, func() bool {
		listing, err := client.List(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, listed := range listing.Sessions {
			if listed.SessionID == spawned[0] {
				return false
			}
		}
		for _, orphan := range listing.Orphans {
			if orphan.SessionID == spawned[0] {
				return false
			}
		}
		return true
	})
	if turns := host.Turns(t, spawned[0]); len(turns) != 0 {
		t.Fatalf("turns=%+v, want no first turn", turns)
	}
}

func spawnedSessions(t *testing.T, host *claudehosttest.Fixture) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(host.ClaudeDir, "spawns.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, line := range claudehosttest.Lines(data) {
		var spawn claudehosttest.Spawn
		if err := json.Unmarshal(line, &spawn); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, spawn.SessionID)
	}
	return ids
}
