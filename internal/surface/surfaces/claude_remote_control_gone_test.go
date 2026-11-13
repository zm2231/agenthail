package surfaces

import (
	"context"
	"errors"
	"net/http"
	"os"
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

func TestAPersonsTurnThatMeetsAGoneRemoteControlIsRoutedAgainToTheTerminalsSocket(t *testing.T) {
	fixture := claudetest.New(t)
	fixture.Listen()
	fixture.Transcript(goneConversation, fixture.Home)
	fixture.Record(goneConversation, fixture.Home, "session_gone")
	peers := claudetest.ListenPeerManager(t, fixture.Home)
	posts := 0
	c := NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, fixture.Home, goneRemoteControl(&posts))
	ctx := context.Background()

	session := goneListedSession(t, c, goneConversation)
	route := surface.RouteFor(c, &session, surface.AuthorPerson)
	if session.Bridge != "session_gone" || route != surface.RouteHumanTurn {
		t.Fatalf("session=%+v route=%v, want it listed with the Remote Control its record names", session, route)
	}
	_, err := route.Send(ctx, c, &session, "are you there", surface.SendOptions{Author: surface.AuthorPerson})
	if !errors.Is(err, surface.ErrRouteChanged) || !surface.IsDeliveryUnavailable(err) || posts != 1 || len(peers.Messages()) != 0 {
		t.Fatalf("err=%v posts=%d peer messages=%v, want one refused post, nothing delivered and the route reported changed", err, posts, peers.Messages())
	}
	rerouted := surface.RouteFor(c, &session, surface.AuthorPerson)
	if session.Bridge != "" || rerouted != surface.RoutePeer {
		t.Fatalf("session=%+v route=%v, want the session left on its socket", session, rerouted)
	}
	if _, err := rerouted.Send(ctx, c, &session, "are you there", surface.SendOptions{Author: surface.AuthorPerson}); err != nil || peers.Count("are you there") != 1 || posts != 1 {
		t.Fatalf("err=%v posts=%d peer messages=%v, want the message on the socket once", err, posts, peers.Messages())
	}

	relisted := goneListedSession(t, c, goneConversation)
	if relisted.Bridge != "" || c.Route(&relisted, surface.AuthorPerson) != surface.RoutePeer {
		t.Fatalf("relisted=%+v, want the gone Remote Control dropped though the record still names it", relisted)
	}
	if got := surface.EffectiveCapabilities(c, &relisted).PersonMessage; got != surface.PersonNote {
		t.Fatalf("the relisted session's personMessage=%q, want a note", got)
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

func TestAStreamsCommandsFollowRemoteControlStartingAndStoppingWithoutATranscriptWrite(t *testing.T) {
	fixture := claudetest.New(t)
	fixture.Listen()
	transcript := fixture.Transcript(goneConversation, fixture.Home)
	fixture.Record(goneConversation, fixture.Home, "")
	c := NewClaude(nil, fixture.Home)
	session := &surface.Session{ID: goneConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Transport: "uds", PID: fixture.PID, HasLocal: true, Transcript: transcript}

	ctx, cancel := context.WithCancel(context.Background())
	commands := make(chan string, 8)
	streamed := make(chan error, 1)
	go func() {
		streamed <- c.Stream(ctx, session, "", func(event surface.StreamEvent) {
			if event.Kind == "commands" {
				commands <- event.Commands.Unavailable
			}
		}, time.Minute)
	}()
	defer func() {
		cancel()
		<-streamed
	}()
	next := func(want string) {
		t.Helper()
		select {
		case got := <-commands:
			if got != want {
				t.Fatalf("commands unavailable=%q, want %q", got, want)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("no commands item followed, want unavailable=%q", want)
		}
	}

	next(claudeCommandsUnavailable)
	fixture.Record(goneConversation, fixture.Home, "session_started")
	next("")
	fixture.Record(goneConversation, fixture.Home, "")
	next(claudeCommandsUnavailable)
}

func TestAStreamRouteFollowsABridgeRewrittenAtTheSameSizeInOneTick(t *testing.T) {
	fixture := claudetest.New(t)
	fixture.Listen()
	fixture.Record(goneConversation, fixture.Home, "session_aaaaaaaa")
	c := NewClaude(nil, fixture.Home)
	session := &surface.Session{ID: goneConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Transport: "uds", PID: fixture.PID, HasLocal: true}
	record := c.peerRecordPath(fixture.PID)
	tick := time.Now().Add(time.Minute)
	if err := os.Chtimes(record, tick, tick); err != nil {
		t.Fatal(err)
	}
	route := c.newStreamRoute(context.Background(), session)
	if bridge := route.current().Bridge; bridge != "session_aaaaaaaa" {
		t.Fatalf("bridge=%q before the rewrite", bridge)
	}
	before, err := os.Stat(record)
	if err != nil {
		t.Fatal(err)
	}
	fixture.Record(goneConversation, fixture.Home, "session_bbbbbbbb")
	if err := os.Chtimes(record, tick, tick); err != nil {
		t.Fatal(err)
	}
	if after, err := os.Stat(record); err != nil || after.Size() != before.Size() {
		t.Fatalf("rewrite changed the record's size: before=%d after=%v err=%v", before.Size(), after, err)
	}
	if !route.reread(context.Background()) || route.current().Bridge != "session_bbbbbbbb" {
		t.Fatalf("bridge=%q after a same-size rewrite in one tick", route.current().Bridge)
	}
}
