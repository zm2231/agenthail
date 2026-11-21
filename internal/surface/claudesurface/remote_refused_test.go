package claudesurface

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/browsercookie"
	"github.com/zm2231/agenthail/internal/surface/claudesurface/claudetest"
)

func TestRemoteControlWithNoProfileForTheAccountSendsAPersonToTheSocketUntilTheCheckExpires(t *testing.T) {
	fixture := newClaudeRootFixture(t, "c", "c@example.com")
	claude := NewClaudeWithRequest(twoProfiles, fixture.home, fixture.request)
	session := fixture.session
	session.Transport, session.PID = "uds", os.Getpid()
	if route := claude.Route(session, surface.AuthorPerson); route != surface.RouteHumanTurn {
		t.Fatalf("route=%v, want the person's own turn while Remote Control is untried", route)
	}

	_, err := claude.postMessage(context.Background(), session, "hello")
	if !errors.Is(err, surface.ErrRouteChanged) || surface.DeliveryTerminalReason(err) != surface.DeliverySigninUnreadable || !strings.Contains(err.Error(), "no sign-in profile is signed into claude.ai as c@example.com") {
		t.Fatalf("err=%v, want the sign-in failure reported as a changed route", err)
	}
	for _, call := range fixture.callList() {
		if strings.HasPrefix(call, "POST") {
			t.Fatalf("posted with another account's cookies: %q", fixture.callList())
		}
	}
	if route := claude.Route(session, surface.AuthorPerson); route != surface.RoutePeer {
		t.Fatalf("route=%v, want the socket while Remote Control cannot sign in", route)
	}
	capabilities := surface.EffectiveCapabilities(claude, session)
	if capabilities.PersonMessage != surface.PersonNote || capabilities.Steer || capabilities.Interrupt {
		t.Fatalf("capabilities=%+v, want a note and no Remote Control controls", capabilities)
	}

	claude.refusedMu.Lock()
	claude.refused[session.ID] = time.Now().Add(-time.Second)
	claude.refusedMu.Unlock()
	if route := claude.Route(session, surface.AuthorPerson); route != surface.RouteHumanTurn {
		t.Fatalf("route=%v, want Remote Control tried again once the check expires", route)
	}
}

func TestRemoteControlWithNoProfileForTheAccountAndNoSocketFailsAsBefore(t *testing.T) {
	fixture := newClaudeRootFixture(t, "c", "c@example.com")
	claude := NewClaudeWithRequest(twoProfiles, fixture.home, fixture.request)
	session := fixture.session
	session.PID = os.Getpid()

	_, err := claude.postMessage(context.Background(), session, "hello")
	if errors.Is(err, surface.ErrRouteChanged) || surface.DeliveryTerminalReason(err) != surface.DeliverySigninUnreadable {
		t.Fatalf("err=%v, want the sign-in failure to stand when no socket can take the message", err)
	}
	if route := claude.Route(session, surface.AuthorPerson); route != surface.RouteHumanTurn {
		t.Fatalf("route=%v, want the route unchanged", route)
	}
}

type unreadableClaudeSignin struct{}

func (unreadableClaudeSignin) ResolveSignin(signin.Target) (signin.Source, error) {
	return signin.Source{}, signin.ProblemError{Consumer: signin.Claude, Err: errors.New("no sign-in profile is signed into claude.ai as the session's account")}
}

func (unreadableClaudeSignin) SigninCandidates(signin.Consumer) ([]signin.Source, error) {
	return nil, signin.ProblemError{Consumer: signin.Claude, Err: errors.New("no sign-in profile is signed into claude.ai as the session's account")}
}

func TestAPersonsMessageRoutedToTheSocketForSigninStillComesFromThePerson(t *testing.T) {
	fixture := claudetest.New(t)
	fixture.Listen()
	fixture.Transcript(goneConversation, fixture.Home)
	fixture.Record(goneConversation, fixture.Home, "session_unsigned")
	peers := claudetest.ListenPeerManager(t, fixture.Home)
	c := NewClaudeWithRequest(unreadableClaudeSignin{}, fixture.Home, func(context.Context, string, string, map[string]string, string, browsercookie.Source, time.Duration) (int, string, error) {
		t.Fatal("a request went to claude.ai without a sign-in")
		return 0, "", nil
	})
	ctx := surface.WithPersonName(surface.WithAuthor(surface.WithSourceSessionID(context.Background(), surface.PersonSessionID), surface.AuthorPerson), "Zain")
	session := goneListedSession(t, c, goneConversation)
	options := surface.SendOptions{Author: surface.AuthorPerson, SourceSessionID: surface.PersonSessionID}

	route := surface.RouteFor(c, &session, surface.AuthorPerson)
	if _, err := route.Send(ctx, c, &session, "from the phone", options); route != surface.RouteHumanTurn || !errors.Is(err, surface.ErrRouteChanged) {
		t.Fatalf("route=%v err=%v, want the person's turn refused for sign-in as a changed route", route, err)
	}
	rerouted := surface.RouteFor(c, &session, surface.AuthorPerson)
	if _, err := rerouted.Send(ctx, c, &session, "from the phone", options); rerouted != surface.RoutePeer || err != nil {
		t.Fatalf("route=%v err=%v, want the socket to take it", rerouted, err)
	}
	sends := peers.Sends()
	if len(sends) != 1 || sends[0].Source != surface.PersonSessionID || !strings.Contains(sends[0].Message, "from the phone") {
		t.Fatalf("sends=%+v, want one socket message sent as the person", sends)
	}
}
