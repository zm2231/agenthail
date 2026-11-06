package surfaces

import (
	"context"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestAnEndedHostedConversationIsResumedWithoutATurn(t *testing.T) {
	host := claudehosttest.New(t, "")
	claudetest.New(t)
	host.Start(t)
	c := NewClaude(nil, host.Home)
	session, _, err := c.StartSession(context.Background(), surface.SessionStartOptions{Message: "build it", Cwd: host.Cwd})
	if err != nil {
		t.Fatal(err)
	}
	listedSession(t, c, session.ID)
	if c.Ended(context.Background(), session) {
		t.Fatal("a running conversation reads as ended")
	}
	if _, err := c.SessionAction(context.Background(), session, "stop"); err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, "the session to close", 15*time.Second, func() bool { return !claudehosttest.Alive(session.PID) })
	if !c.Ended(context.Background(), session) {
		t.Fatal("a closed conversation does not read as ended")
	}
	if err := c.Wake(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if spawns := host.Spawns(t, session.ID); len(spawns) != 2 {
		t.Fatalf("spawns=%+v, want the start and the wake", spawns)
	}
	if turns := host.Turns(t, session.ID); len(turns) != 1 {
		t.Fatalf("turns=%+v, want only the first brief", turns)
	}
	if c.Ended(context.Background(), session) {
		t.Fatal("a resumed conversation still reads as ended")
	}
}
