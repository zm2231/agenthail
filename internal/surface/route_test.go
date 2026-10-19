package surface

import (
	"context"
	"testing"
)

type routeFake struct {
	Surface
	route Route
}

func (f routeFake) Name() SurfaceKind            { return KindClaude }
func (f routeFake) Route(*Session, Author) Route { return f.route }
func (f routeFake) SendHumanTurn(context.Context, *Session, string) (*SendResult, error) {
	return &SendResult{Accepted: true}, nil
}

type sendOnlyFake struct{ Surface }

func TestRouteForAsksTheAdapter(t *testing.T) {
	session := &Session{ID: "session_bridge", Surface: KindClaude, RuntimeKind: RuntimeClaudeCLI, Transport: "uds"}
	for _, want := range []Route{RouteSend, RoutePeer, RouteHumanTurn} {
		if got := RouteFor(routeFake{route: want}, session, AuthorPerson); got != want {
			t.Fatalf("route=%v want %v", got, want)
		}
	}
	if got := RouteFor(sendOnlyFake{}, session, AuthorPerson); got != RouteSend {
		t.Fatalf("an adapter without routes took %v", got)
	}
}

func TestHumanTurnRouteRejectsEveryPerMessageOption(t *testing.T) {
	for _, options := range []SendOptions{
		{Model: "claude-haiku-5-5"},
		{TurnOptions: TurnOptions{Effort: "high"}},
	} {
		err := RouteHumanTurn.Validate(routeFake{}, options)
		if !IsDeliveryTerminal(err) {
			t.Fatalf("options=%+v err=%v, want a terminal invalid request", options, err)
		}
	}
	if err := RouteHumanTurn.Validate(routeFake{}, SendOptions{}); err != nil {
		t.Fatal(err)
	}
}
