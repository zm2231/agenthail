package surfaces

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

const goneConversation = "conversation-gone"

func goneRemoteControl(posts *int) ClaudeRequest {
	return func(context.Context, string, string, map[string]string, string, CookieSource, time.Duration) (int, string, error) {
		*posts++
		return http.StatusNotFound, `{"error":{"message":"Session cse_gone not found"}}`, nil
	}
}

func goneListedSession(t *testing.T, c *Claude, id string) surface.Session {
	t.Helper()
	sessions, err := c.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range sessions {
		if session.ID == id {
			return session
		}
	}
	t.Fatalf("sessions=%+v, want %s listed", sessions, id)
	return surface.Session{}
}

func TestAPersonsMessageReachesTheTerminalWhenItsRemoteControlIsGone(t *testing.T) {
	fixture := claudetest.New(t)
	fixture.Listen()
	fixture.Transcript(goneConversation, fixture.Home)
	fixture.Record(goneConversation, fixture.Home, "session_gone")
	peers := claudetest.ListenPeerManager(t, fixture.Home)
	posts := 0
	c := NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, fixture.Home, goneRemoteControl(&posts))

	session := goneListedSession(t, c, goneConversation)
	if session.Bridge != "session_gone" || c.Route(&session, surface.AuthorPerson) != surface.RouteHumanTurn {
		t.Fatalf("session=%+v, want it listed with the Remote Control its record names", session)
	}
	result, err := c.SendHumanTurn(context.Background(), &session, "are you there", nil)
	if err != nil || result == nil || !result.Accepted {
		t.Fatalf("result=%+v err=%v, want the message delivered over the messaging socket", result, err)
	}
	if posts != 1 || peers.Count("are you there") != 1 {
		t.Fatalf("posts=%d peer messages=%v, want one refused post and one socket delivery", posts, peers.Messages())
	}
	if session.Bridge != "" || c.Route(&session, surface.AuthorPerson) != surface.RoutePeer {
		t.Fatalf("session=%+v, want the sent session left on its socket", session)
	}

	relisted := goneListedSession(t, c, goneConversation)
	if relisted.Bridge != "" || c.Route(&relisted, surface.AuthorPerson) != surface.RoutePeer {
		t.Fatalf("relisted=%+v, want the gone Remote Control dropped though the record still names it", relisted)
	}
	if notice := surface.EffectiveCapabilities(c, &relisted).TurnNotice; notice == "" {
		t.Fatal("the relisted session shows no notice that a message reaches it as a note")
	}
}

func TestAPersonsMessageFailsClearlyWhenRemoteControlIsGoneAndNoSocketListens(t *testing.T) {
	fixture := claudetest.New(t)
	fixture.Transcript(goneConversation, fixture.Home)
	fixture.Record(goneConversation, fixture.Home, "session_gone")
	posts := 0
	c := NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, fixture.Home, goneRemoteControl(&posts))

	session := goneListedSession(t, c, goneConversation)
	_, err := c.SendHumanTurn(context.Background(), &session, "are you there", nil)
	if !surface.IsDeliveryTerminal(err) || surface.DeliveryTerminalReason(err) != surface.DeliveryTargetMissing || !errors.Is(err, errRemoteControlGone) || !strings.Contains(err.Error(), "no messaging socket") {
		t.Fatalf("err=%v, want a final target-missing failure naming the gone Remote Control and the missing socket", err)
	}
	if posts != 1 {
		t.Fatalf("posts=%d, want one", posts)
	}
}
