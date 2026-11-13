package claudehost_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestARetiringHostKeepsAnIdleSessionWhosePersonTypesUntilItIsReleased(t *testing.T) {
	f := claudehosttest.New(t, "")
	ctx := testContext(t)
	client, err := f.Manager(t, "1").Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	old, _ := client.Hello(ctx)
	sessionID := uuid.NewString()
	opened := f.Open(t, client, sessionID)
	if _, err := client.Send(ctx, claudehost.SendRequest{SessionID: sessionID, UUID: uuid.NewString(), Text: "before the update"}); err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, "the first turn", 15*time.Second, func() bool { return len(f.Turns(t, sessionID)) == 1 })
	if client, err = f.Manager(t, "2").Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	if !claudehosttest.Alive(old.PID) || !claudehosttest.Alive(opened.PID) {
		t.Fatal("the retiring host closed an idle session nobody released")
	}

	if _, err := client.Send(ctx, claudehost.SendRequest{SessionID: sessionID, UUID: uuid.NewString(), Text: "while kept", Origin: &claudehost.Origin{Kind: "human"}}); err != nil {
		t.Fatalf("a person's turn to the kept session = %v", err)
	}
	testkit.Eventually(t, "the person's turn", 15*time.Second, func() bool { return len(f.Turns(t, sessionID)) == 2 })
	if turn := f.Turns(t, sessionID)[1]; turn.PID != opened.PID || turn.Reply != "echo:while kept" {
		t.Fatalf("the person's turn = %+v, want it run by the kept Claude %d", turn, opened.PID)
	}
	held, err := client.Open(ctx, claudehost.OpenRequest{Resume: sessionID, Cwd: f.Cwd})
	if err != nil || held.PID != opened.PID {
		t.Fatalf("open while the retiring host keeps it = %+v, %v; want the kept Claude %d", held, err, opened.PID)
	}
	if spawns := f.Spawns(t, sessionID); len(spawns) != 1 {
		t.Fatalf("Claude started %d times for the session, want once", len(spawns))
	}

	releaseOnRetiringHost(t, client, sessionID)
	testkit.Eventually(t, "the retiring host to leave once its session is released", 15*time.Second, func() bool {
		return !claudehosttest.Alive(old.PID) && !claudehosttest.Alive(opened.PID)
	})
	resumed, err := client.Open(ctx, claudehost.OpenRequest{Resume: sessionID, Cwd: f.Cwd})
	if err != nil || resumed.PID == opened.PID {
		t.Fatalf("resume on the new host = %+v, %v", resumed, err)
	}
}

func TestARetiringHostClosesItsIdleSessionsItselfOnceTheDrainGraceIsOver(t *testing.T) {
	f := claudehosttest.New(t, "")
	ctx := testContext(t)
	client, err := f.Manager(t, "1", "AGENTHAIL_CLAUDE_HOST_DRAIN_GRACE_MS=1500").Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	old, _ := client.Hello(ctx)
	sessionID := uuid.NewString()
	opened := f.Open(t, client, sessionID)
	if _, err := client.Send(ctx, claudehost.SendRequest{SessionID: sessionID, UUID: uuid.NewString(), Text: "x"}); err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, "the turn", 15*time.Second, func() bool { return len(f.Turns(t, sessionID)) == 1 })
	if _, err := f.Manager(t, "2").Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, "the retiring host to close its session and leave after the grace", 15*time.Second, func() bool {
		return !claudehosttest.Alive(old.PID) && !claudehosttest.Alive(opened.PID)
	})
}
