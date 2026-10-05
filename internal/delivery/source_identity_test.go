package delivery

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type sourceCheckingSurface struct {
	fakeSurface
	source string
}

func (*sourceCheckingSurface) EnsureWritable(context.Context, *surface.Session) error { return nil }

func (s *sourceCheckingSurface) Send(ctx context.Context, session *surface.Session, message string) (*surface.SendResult, error) {
	s.source = surface.SourceSessionID(ctx)
	return s.fakeSurface.Send(ctx, session, message)
}

func (s *sourceCheckingSurface) SendWithOptions(ctx context.Context, session *surface.Session, message string, options surface.SendOptions) (*surface.SendResult, error) {
	s.source = surface.SourceSessionID(ctx)
	s.fakeSurface.sent = append(s.fakeSurface.sent, message)
	return s.fakeSurface.result, s.fakeSurface.err
}

func TestDispatcherAppliesSourceSessionIDToImmediateAndDeferredSend(t *testing.T) {
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	session := &surface.Session{ID: "target", Surface: surface.KindClaude}
	if err := r.RegisterSession(*session); err != nil {
		t.Fatal(err)
	}
	for _, sourceID := range []string{"source-immediate", "source-deferred"} {
		if err := r.RegisterSession(surface.Session{ID: sourceID, Surface: surface.SurfaceKind("agenthail")}); err != nil {
			t.Fatal(err)
		}
	}
	adapter := &sourceCheckingSurface{fakeSurface: fakeSurface{kind: surface.KindClaude, result: &surface.SendResult{UUID: "turn", Accepted: true}}}
	options := surface.SendOptions{Model: "sonnet", SourceSessionID: "source-immediate"}
	if _, err := (Dispatcher{Registry: r}).DeliverWithOptions(context.Background(), adapter, session, "now", "", options); err != nil {
		t.Fatal(err)
	}
	if adapter.source != "source-immediate" {
		t.Fatalf("immediate source=%q", adapter.source)
	}

	adapter.result = &surface.SendResult{Accepted: false}
	if _, err := (Dispatcher{Registry: r}).DeliverWithOptions(context.Background(), adapter, session, "later", "", surface.SendOptions{SourceSessionID: "source-deferred"}); err != nil {
		t.Fatal(err)
	}
	item, err := r.ClaimNextMessage(session.ID, time.Now())
	if err != nil || item == nil || item.SourceSessionID != "source-deferred" {
		t.Fatalf("queued item=%+v err=%v", item, err)
	}
}
