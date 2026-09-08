package daemon

import (
	"context"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

type sourceCaptureSurface struct {
	*daemonSurface
	source string
}

func (s *sourceCaptureSurface) SendWithOptions(ctx context.Context, session *surface.Session, message string, options surface.SendOptions) (*surface.SendResult, error) {
	s.source = surface.SourceSessionID(ctx)
	return s.daemonSurface.SendWithOptions(ctx, session, message, options)
}

func TestOutboxRestoresQueuedSourceSessionIDAlongsideModel(t *testing.T) {
	d, r, fake, _, to := daemonFixture(t)
	queueID, err := r.QueueMessageWithOptions(to.ID, "deferred", "", surface.SendOptions{Model: "sonnet", SourceSessionID: "source"})
	if err != nil {
		t.Fatal(err)
	}
	adapter := &sourceCaptureSurface{daemonSurface: fake}
	d.drainMessageQueue(context.Background(), adapter, &to)
	if adapter.source != "source" {
		t.Fatalf("source=%q", adapter.source)
	}
	item, err := r.QueueItem(queueID)
	if err != nil {
		t.Fatal(err)
	}
	if item.Status != "delivered" {
		t.Fatalf("queue item=%+v", item)
	}
	if len(fake.models) != 1 || fake.models[0] != "sonnet" {
		t.Fatalf("models=%v", fake.models)
	}
}
