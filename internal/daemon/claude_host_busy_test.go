package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/testkit"
)

// busyHostSession opens a host session and starts a turn that runs until it
// is interrupted.
func busyHostSession(t *testing.T) (*claudehosttest.Fixture, claudehost.Client, *surfaces.Claude, *Daemon, surface.Session, string) {
	t.Helper()
	fixture := claudehosttest.New(t, "")
	client := fixture.Start(t)
	id := uuid.NewString()
	fixture.Open(t, client, id)
	claude := surfaces.NewClaude(nil, fixture.Home)
	session := hostedSession(t, claude, id)
	r := hostRegistry(t)
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	d := newTestDaemon(t, r, []surface.Surface{claude})
	running := uuid.NewString()
	if _, err := client.Send(context.Background(), claudehost.SendRequest{SessionID: id, UUID: running, Text: "SLOW 30000"}); err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, "the turn to run", 15*time.Second, func() bool { return hostState(t, client, id) == "busy" })
	return fixture, client, claude, d, session, running
}

func hostState(t *testing.T, client claudehost.Client, id string) string {
	t.Helper()
	listing, err := client.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, listed := range listing.Sessions {
		if listed.SessionID == id {
			return listed.State
		}
	}
	return ""
}

func TestAPersonsMessageToABusyHostSessionWaitsInTheQueueForItsTurnToEnd(t *testing.T) {
	fixture, client, claude, d, session, running := busyHostSession(t)
	ctx := context.Background()
	receipt, err := d.dispatcher().DeliverWithOptions(ctx, claude, &session, "after your turn", "", surface.SendOptions{Author: surface.AuthorPerson})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Evidence != surface.EvidenceQueued || receipt.QueueID == 0 {
		t.Fatalf("receipt = %+v, want the message queued behind the running turn", receipt)
	}
	if err := client.Interrupt(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, "the turn to end", 15*time.Second, func() bool { return hostState(t, client, session.ID) == "idle" })
	idle := hostedSession(t, claude, session.ID)
	d.drainMessageQueue(ctx, claude, &idle)
	testkit.Eventually(t, "the queued turn", 15*time.Second, func() bool { return len(fixture.Turns(t, session.ID)) == 2 })
	turns := fixture.Turns(t, session.ID)
	if turns[0].UUID != running || turns[0].Reply != "interrupted" || turns[1].Text != "after your turn" || turnOrigin(t, turns[1]) != "human" {
		t.Fatalf("turns = %+v, want the queued message as its own turn after the interrupted one", turns)
	}
}

func TestAPersonsSteerToABusyHostSessionGoesIntoTheRunningTurn(t *testing.T) {
	fixture, client, claude, d, session, _ := busyHostSession(t)
	ctx := context.Background()
	receipt, err := d.dispatcher().DeliverWithOptions(ctx, claude, &session, "change course", "", surface.SendOptions{Author: surface.AuthorPerson, BusyDelivery: "steer"})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Evidence == surface.EvidenceQueued || receipt.QueueID != 0 {
		t.Fatalf("receipt = %+v, want the steer sent now", receipt)
	}
	if err := client.Interrupt(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, "the steer to reach Claude", 15*time.Second, func() bool { return len(fixture.Turns(t, session.ID)) == 2 })
	if steer := fixture.Turns(t, session.ID)[1]; steer.Text != "change course" || steer.Priority != "now" {
		t.Fatalf("steer = %+v, want it given to Claude at once", steer)
	}
}
