package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/zm2231/agenthail/internal/broadcast"
	"github.com/zm2231/agenthail/internal/coordinator"
	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type cliBroadcastDelivery struct {
	app    *App
	sender coordinator.Sender
}

func (c cliBroadcastDelivery) Session(ctx context.Context, sessionID, message, source string) (*delivery.Receipt, error) {
	session, adapter, err := c.app.resolveTarget(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return c.sender.DeliverWithOptions(ctx, adapter, session, message, "", surface.SendOptions{SourceSessionID: source})
}

func (c cliBroadcastDelivery) PeerCoordinator(ctx context.Context, spec registry.CoordinatorSpec, message, source string) (*delivery.Receipt, error) {
	answer, err := c.app.peerCoordinatorSend(ctx, spec, message, source, "")
	if err != nil {
		return nil, err
	}
	return &answer.Result, nil
}

func (a *App) channelSend(ctx context.Context, name, message, fromLabel string) error {
	target, err := broadcast.Resolve(a.registry(), name)
	if err != nil {
		return err
	}
	sourceID, err := a.sourceSessionID(ctx, fromLabel)
	if err != nil {
		return err
	}
	service := a.coordinator()
	if sourceID != "" {
		sourceID = service.Canonical(sourceID)
	}
	if fromLabel == "" && sourceID != "" {
		fromLabel = a.resolveDisplay(sourceID)
	}
	if fromLabel == "" {
		fromLabel = "hail"
	}
	broadcaster := broadcast.Broadcaster{Registry: a.registry(), Coordinators: service, Delivery: cliBroadcastDelivery{app: a, sender: coordinator.Sender{Service: service}}}
	result, err := broadcaster.Send(ctx, target, message, fromLabel, sourceID)
	if err != nil {
		return err
	}
	for _, recipient := range result.Recipients {
		label := recipient.Coordinator
		if label == "" {
			label = a.sessionDisplay(recipient.SessionID)
		}
		tag := map[string]string{broadcast.OutcomeSent: "[ OK ]", broadcast.OutcomeQueued: "[QUEUE]", broadcast.OutcomeSubmitted: "[SUBMIT]", broadcast.OutcomeFailed: "[FAIL]"}[recipient.Outcome]
		if recipient.Detail != "" {
			fmt.Printf("  %s %s: %s\n", tag, label, recipient.Detail)
		} else {
			fmt.Printf("  %s %s\n", tag, label)
		}
	}
	queued, failed := result.Count(broadcast.OutcomeQueued), result.Count(broadcast.OutcomeFailed)
	fmt.Printf("#%s: %d sent, %d queued, %d submitted, %d failed\n", target.Label(), result.Count(broadcast.OutcomeSent), queued, result.Count(broadcast.OutcomeSubmitted), failed)
	if queued > 0 {
		if _, running := daemon.IsRunning(); !running {
			fmt.Fprintln(os.Stderr, "warning: daemon is not running; queued channel deliveries will wait until 'agenthail daemon start'")
		}
	}
	if failed > 0 {
		return fmt.Errorf("#%s had %d failed delivery(s)", target.Label(), failed)
	}
	return nil
}
