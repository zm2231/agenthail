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

func (f humanTurnFake) Name() surface.SurfaceKind { return surface.KindClaude }
func (f humanTurnFake) RuntimeKinds() []surface.RuntimeKind {
	return []surface.RuntimeKind{surface.RuntimeClaudeCLI}
}
func (f humanTurnFake) CanSendHumanTurn(*surface.Session) bool { return f.bridged }
func (f humanTurnFake) SendHumanTurn(context.Context, *surface.Session, string) (*surface.SendResult, error) {
	return &surface.SendResult{Accepted: true}, nil
}

func TestRouteForDecidesByAuthorAndBridge(t *testing.T) {
	peer := &surface.Session{ID: "session_bridge", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Transport: "uds"}
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
