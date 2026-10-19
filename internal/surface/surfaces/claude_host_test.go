package surfaces

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/surface"
)

func hostedClaude(t *testing.T) (*Claude, *claudehosttest.Fixture) {
	t.Helper()
	fixture := claudehosttest.New(t, "")
	return NewClaude("Default", fixture.Home), fixture
}

func listedSession(t *testing.T, c *Claude, id string) *surface.Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var found *surface.Session
	claudehosttest.WaitFor(t, "the session to be listed on the host transport", func() bool {
		c.host.listed = time.Time{}
		sessions, err := c.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for i := range sessions {
			if sessions[i].ID == id && sessions[i].Transport == "host" {
				found = &sessions[i]
				return true
			}
		}
		return false
	})
	return found
}

func origin(t *testing.T, turn claudehosttest.Turn) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(turn.Origin, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAHostSessionIsListedOnTheHostTransport(t *testing.T) {
	c, fixture := hostedClaude(t)
	client := fixture.Start(t)
	id := uuid.NewString()
	opened := fixture.Open(t, client, id)
	sess := listedSession(t, c, id)
	if sess.PID != opened.PID || sess.Bridge != "" {
		t.Fatalf("listed %+v, want pid %d and no bridge", sess, opened.PID)
	}
	if got := c.Route(sess, surface.AuthorPerson); got != surface.RouteHumanTurn {
		t.Fatalf("person route = %v, want a human turn", got)
	}
	if got := c.Route(sess, ""); got != surface.RoutePeer {
		t.Fatalf("agent route = %v, want the native socket", got)
	}
	caps := surface.EffectiveCapabilities(c, sess)
	if !caps.Steer || !caps.Interrupt || caps.Compact || caps.Model {
		t.Fatalf("capabilities = %+v, want steer and interrupt only among controls", caps.Capabilities)
	}
}

func TestAPersonsTurnToAHostSessionCarriesTheIntentUUIDAndRunsOnce(t *testing.T) {
	c, fixture := hostedClaude(t)
	c.localOnly = true
	client := fixture.Start(t)
	id := uuid.NewString()
	fixture.Open(t, client, id)
	sess := listedSession(t, c, id)
	message := uuid.NewString()
	ctx := surface.WithMessageUUID(context.Background(), message)
	for range 2 {
		result, err := c.SendHumanTurn(ctx, sess, "hello from a person")
		if err != nil {
			t.Fatal(err)
		}
		if result.UUID != message || !result.Accepted {
			t.Fatalf("result = %+v, want accepted with the intent uuid", result)
		}
	}
	claudehosttest.WaitFor(t, "the turn", func() bool { return len(fixture.Turns(t, id)) == 1 })
	time.Sleep(200 * time.Millisecond)
	turns := fixture.Turns(t, id)
	if len(turns) != 1 || turns[0].UUID != message || turns[0].Text != "hello from a person" || origin(t, turns[0])["kind"] != "human" {
		t.Fatalf("turns = %+v, want one human turn with the intent uuid", turns)
	}
}

func TestAnAgentMessageToAHostSessionArrivesOverItsNativeSocket(t *testing.T) {
	c, fixture := hostedClaude(t)
	client := fixture.Start(t)
	sender, target := uuid.NewString(), uuid.NewString()
	fixture.Open(t, client, sender)
	fixture.Open(t, client, target)
	senderSession := listedSession(t, c, sender)
	sess := listedSession(t, c, target)
	ctx := surface.WithSourceSessionID(context.Background(), sender)
	if _, err := c.Send(ctx, sess, "/compact"); !surface.IsDeliveryTerminal(err) {
		t.Fatalf("slash command over the socket = %v, want refused", err)
	}
	if _, err := c.Send(ctx, sess, "from another session"); err != nil {
		t.Fatal(err)
	}
	claudehosttest.WaitFor(t, "the peer turn", func() bool { return len(fixture.Turns(t, target)) == 1 })
	turn := fixture.Turns(t, target)[0]
	senderSocket := "uds:/tmp/cc-socks/" + strconv.Itoa(senderSession.PID) + ".sock"
	if origin(t, turn)["kind"] != "peer" || origin(t, turn)["from"] != senderSocket || !strings.Contains(turn.Text, `from="`+senderSocket+`"`) || !strings.Contains(turn.Text, "from another session") {
		t.Fatalf("turn = %+v, want a peer message from %s", turn, senderSocket)
	}
}

func TestSteerAndInterruptAHostSession(t *testing.T) {
	c, fixture := hostedClaude(t)
	client := fixture.Start(t)
	id := uuid.NewString()
	fixture.Open(t, client, id)
	sess := listedSession(t, c, id)
	ctx := context.Background()
	if _, err := c.SendHumanTurn(surface.WithMessageUUID(ctx, uuid.NewString()), sess, "SLOW 30000"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := c.Steer(ctx, sess, "steered"); err != nil {
		t.Fatal(err)
	}
	if err := c.Interrupt(ctx, sess); err != nil {
		t.Fatal(err)
	}
	claudehosttest.WaitFor(t, "both turns", func() bool { return len(fixture.Turns(t, id)) == 2 })
	turns := fixture.Turns(t, id)
	if turns[0].Reply != "interrupted" || turns[1].Text != "steered" || turns[1].Priority != "now" {
		t.Fatalf("turns = %+v, want the slow turn interrupted and the steer sent with priority now", turns)
	}
}

func TestAPersonsTurnIsHeldWhileTheHostIsDownAndItsSessionRuns(t *testing.T) {
	c, fixture := hostedClaude(t)
	client := fixture.Start(t)
	id := uuid.NewString()
	opened := fixture.Open(t, client, id)
	sess := listedSession(t, c, id)
	if _, err := c.SendHumanTurn(surface.WithMessageUUID(context.Background(), uuid.NewString()), sess, "SLOW 3000"); err != nil {
		t.Fatal(err)
	}
	fixture.KillHost(t)
	if !claudehosttest.Alive(opened.PID) {
		t.Fatal("the session's CLI outlives the host while its turn runs")
	}
	_, err := c.SendHumanTurn(surface.WithMessageUUID(context.Background(), uuid.NewString()), sess, "while the host is down")
	if !surface.IsDeliveryHeld(err) {
		t.Fatalf("send while the host is down = %v, want held", err)
	}
	if turns := fixture.Turns(t, id); len(turns) > 1 {
		t.Fatalf("turns = %+v, want nothing delivered while held", turns)
	}
}

func TestHostErrorsMapToDeliveryOutcomes(t *testing.T) {
	host := func(code string) error { return &claudehost.Error{Code: code, Message: code} }
	for _, tc := range []struct {
		name string
		err  error
		want func(error) bool
	}{
		{"not running", claudehost.ErrNotRunning, surface.IsDeliveryHeld},
		{"held", host("held"), surface.IsDeliveryHeld},
		{"draining", host("draining"), surface.IsDeliveryHeld},
		{"not open", host("not_open"), func(err error) bool { return surface.IsDeliveryUnavailable(err) && !surface.IsDeliveryHeld(err) }},
		{"unknown", host("unknown"), surface.IsDeliveryOutcomeUnknown},
		{"no answer", &claudehost.OutcomeUnknownError{Err: context.DeadlineExceeded}, surface.IsDeliveryOutcomeUnknown},
		{"invalid", host("invalid"), func(err error) bool {
			return surface.DeliveryTerminalReason(err) == surface.DeliveryInvalidRequest
		}},
	} {
		if got := claudeHostDeliveryError(tc.err); !tc.want(got) {
			t.Errorf("%s: %v", tc.name, got)
		}
	}
}
