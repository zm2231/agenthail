package delivery

import (
	"context"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

type humanTurnFake struct {
	surface.Surface
	bridged bool
}

func (f humanTurnFake) Name() surface.SurfaceKind              { return surface.KindClaude }
func (f humanTurnFake) CanSendHumanTurn(*surface.Session) bool { return f.bridged }
func (f humanTurnFake) SendHumanTurn(context.Context, *surface.Session, string) (*surface.SendResult, error) {
	return &surface.SendResult{Accepted: true}, nil
}

func TestRouteForDecidesByAuthorAndBridge(t *testing.T) {
	peer := &surface.Session{ID: "session_bridge", Surface: surface.KindClaude, Transport: "uds"}
	for _, tc := range []struct {
		name    string
		bridged bool
		author  surface.Author
		want    Route
	}{
		{"person with bridge", true, surface.AuthorPerson, RouteHumanTurn},
		{"unattributed with bridge", true, "", RoutePeer},
		{"person without bridge", false, surface.AuthorPerson, RoutePeer},
	} {
		if got := RouteFor(humanTurnFake{bridged: tc.bridged}, peer, tc.author); got != tc.want {
			t.Fatalf("%s: route=%v want %v", tc.name, got, tc.want)
		}
	}
}

func TestHumanTurnRouteRejectsEveryPerMessageOption(t *testing.T) {
	for _, options := range []surface.SendOptions{
		{Model: "claude-haiku-5-5"},
		{TurnOptions: surface.TurnOptions{Effort: "high"}},
	} {
		err := RouteHumanTurn.Validate(humanTurnFake{bridged: true}, options)
		if !surface.IsDeliveryTerminal(err) {
			t.Fatalf("options=%+v err=%v, want a terminal invalid request", options, err)
		}
	}
	if err := RouteHumanTurn.Validate(humanTurnFake{bridged: true}, surface.SendOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestRouteForAHostSessionTakesAPersonsTurnThroughTheHostAndAgentsThroughTheSocket(t *testing.T) {
	hosted := &surface.Session{ID: "0b8f5d4e-3a51-4c2e-9a0e-2f3c4d5e6f70", Surface: surface.KindClaude, Transport: "host"}
	adapter := humanTurnFake{bridged: true}
	if got := RouteFor(adapter, hosted, surface.AuthorPerson); got != RouteHumanTurn || got.TransportOnly() {
		t.Fatalf("person route = %v, want a followed human turn", got)
	}
	if got := RouteFor(adapter, hosted, ""); got != RoutePeer || !got.TransportOnly() {
		t.Fatalf("agent route = %v, want the native socket", got)
	}
}
