package delivery

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type sourceCheckingSurface struct {
	fakeSurface
	source      string
	replyTarget string
}

func (*sourceCheckingSurface) EnsureWritable(context.Context, *surface.Session) error { return nil }

func (s *sourceCheckingSurface) Send(ctx context.Context, session *surface.Session, message string) (*surface.SendResult, error) {
	s.source = surface.SourceSessionID(ctx)
	s.replyTarget = surface.ReplyTarget(ctx)
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
	session := &surface.Session{ID: "target", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI}
	if err := r.RegisterSession(*session); err != nil {
		t.Fatal(err)
	}
	for _, sourceID := range []string{"source-immediate", "source-deferred"} {
		if err := r.RegisterSession(surface.Session{ID: sourceID, Surface: surface.SurfaceKind("agenthail"), RuntimeKind: surface.RuntimeOperator}); err != nil {
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

func TestTheSurfaceIsToldHowTheTargetAddressesTheSender(t *testing.T) {
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	target := &surface.Session{ID: "target", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Transport: "uds"}
	for _, session := range []surface.Session{*target, {ID: "aliased", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "aliased title"}, {ID: "titled", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "only a title"}} {
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.SetAlias("reviewer", "aliased"); err != nil {
		t.Fatal(err)
	}
	hostID := "h0123456789ab"
	if _, err := r.AddHost(registry.PairedHost{ID: hostID, Name: "vps", Endpoint: "https://vps.example.ts.net:7412", Address: "100.64.0.2", Token: "ahd_synthetic"}); err != nil {
		t.Fatal(err)
	}
	for id, projection := range map[string]map[string]string{"remote-aliased": {"alias": "builder"}, "remote-titled": {}} {
		projection["id"] = hostID + "::" + id
		raw, _ := json.Marshal(projection)
		if _, _, err := r.ApplyHostCatalogRow(hostID, registry.HostCatalogRow{Session: surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: id + " title"}, EntityID: hostID + "::" + id, Projection: raw, ObservedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	for alias, id := range map[string]string{"local-builder": "remote-aliased", "local-only": "remote-titled"} {
		if err := r.SetAlias(alias, id); err != nil {
			t.Fatal(err)
		}
	}
	for source, want := range map[string]string{"aliased": "@reviewer", "remote-aliased": "vps::@builder", "titled": "", "remote-titled": ""} {
		adapter := &sourceCheckingSurface{fakeSurface: fakeSurface{kind: surface.KindClaude, result: &surface.SendResult{UUID: "peer-" + source, Accepted: true}}}
		if _, err := (Dispatcher{Registry: r}).DeliverWithOptions(context.Background(), adapter, target, "hello", "", surface.SendOptions{SourceSessionID: source}); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		if adapter.source != source || adapter.replyTarget != want {
			t.Fatalf("%s: source=%q replyTarget=%q, want %q", source, adapter.source, adapter.replyTarget, want)
		}
	}
}
