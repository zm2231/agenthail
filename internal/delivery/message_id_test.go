package delivery

import (
	"context"
	"errors"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestEveryIntentBackedReceiptCarriesItsIntentsUUID(t *testing.T) {
	busy := &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "turn"}
	waiting := &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "turn", WaitingOn: &surface.Waiting{Kind: surface.WaitingQuestion, ID: "question"}}
	unknown := surface.DeliveryOutcomeUnknown(errors.New("steer acknowledgement lost"))
	cases := []struct {
		name    string
		want    surface.DeliveryEvidence
		adapter *fakeSurface
		deliver func(Dispatcher, surface.Surface, *surface.Session) (*Receipt, error)
	}{
		{"queued behind a busy turn", surface.EvidenceQueued, &fakeSurface{result: &surface.SendResult{Accepted: false}}, func(d Dispatcher, a surface.Surface, s *surface.Session) (*Receipt, error) {
			return d.Deliver(context.Background(), a, s, "queued", "key")
		}},
		{"steer with an unknown outcome", surface.EvidenceSubmitted, &fakeSurface{observe: busy, steerErr: unknown, capabilities: surface.Capabilities{Steer: true}}, func(d Dispatcher, a surface.Surface, s *surface.Session) (*Receipt, error) {
			return d.Steer(context.Background(), a, s, "steer", "")
		}},
		{"steer held for a waiting turn", surface.EvidenceQueued, &fakeSurface{observe: waiting, capabilities: surface.Capabilities{Steer: true}}, func(d Dispatcher, a surface.Surface, s *surface.Session) (*Receipt, error) {
			return d.Steer(context.Background(), a, s, "after the answer", "")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := registry.Open(registrytest.Path(t))
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			session := &surface.Session{ID: "target", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusBusy}
			if err := r.RegisterSession(*session); err != nil {
				t.Fatal(err)
			}
			receipt, err := tc.deliver(Dispatcher{Registry: r}, tc.adapter, session)
			if err != nil || receipt == nil || receipt.DeliveryID == 0 || receipt.Evidence != tc.want {
				t.Fatalf("receipt=%+v err=%v", receipt, err)
			}
			intent, err := r.DeliveryIntent(receipt.DeliveryID)
			if err != nil || intent.UUID == "" || receipt.MessageID != intent.UUID {
				t.Fatalf("receipt message id %q, intent=%+v err=%v", receipt.MessageID, intent, err)
			}
		})
	}
}
