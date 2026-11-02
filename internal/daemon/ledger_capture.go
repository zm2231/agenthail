package daemon

import (
	"context"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) personCapture(ctx delivery.PersonCaptureContext) {
	if ctx.TargetCwd == "" {
		return
	}
	project, err := ResolvePathProject(context.Background(), d.Registry, ctx.TargetCwd)
	if err != nil {
		d.log.Printf("ledger capture: resolve project for %s: %s", ctx.TargetCwd, err)
		return
	}
	if project == nil || project.Key == "" {
		return
	}
	store := ledger.NewStore(d.Registry.WriteDB(), d.Registry.ReadDB())
	capture := ledger.PersonCapture(store)
	captureErr := capture(ledger.CaptureInput{
		ProjectKey: project.Key,
		SessionID:  surface.PersonSessionID,
		TargetID:   ctx.TargetSessionID,
		Surface:    ctx.TargetSurface,
		MessageID:  captureMessageID(ctx.DeliveryKey),
		Text:       ctx.Message,
	})
	if captureErr != nil {
		d.log.Printf("ledger capture: record utterance for project %s: %s", project.Key, captureErr)
	}
}

func captureMessageID(deliveryKey string) string {
	if deliveryKey != "" {
		return deliveryKey
	}
	return uuid.NewString()
}
