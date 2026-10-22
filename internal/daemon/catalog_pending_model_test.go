package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

type pendingModelStatusSurface struct {
	*localStatusSurface
}

func (s *pendingModelStatusSurface) pendingPath(id string) string {
	return filepath.Join(s.dir, id+".pending")
}

func (s *pendingModelStatusSurface) LocalStatusFiles(session surface.Session) []string {
	return []string{s.statusPath(session.ID), s.pendingPath(session.ID)}
}

func (s *pendingModelStatusSurface) LocalStatus(ctx context.Context, session surface.Session) (surface.Session, error) {
	session, err := s.localStatusSurface.LocalStatus(ctx, session)
	raw, _ := os.ReadFile(s.pendingPath(session.ID))
	session.PendingModel = strings.TrimSpace(string(raw))
	return session, err
}

func (s *pendingModelStatusSurface) List(ctx context.Context) ([]surface.Session, error) {
	sessions, err := s.daemonSurface.List(ctx)
	for index := range sessions {
		if local, localErr := s.LocalStatus(ctx, sessions[index]); localErr == nil {
			sessions[index] = local
		}
	}
	return sessions, err
}

func catalogPendingModels(t *testing.T, d *Daemon, after uint64, sessionID string) []string {
	t.Helper()
	window, err := d.Registry.CatalogEventsAfter(after, 100)
	if err != nil {
		t.Fatal(err)
	}
	var models []string
	for _, event := range window.Events {
		if event.Type != "session.upserted" || event.EntityID != sessionID {
			continue
		}
		var envelope struct {
			Session struct {
				PendingModel string `json:"pendingModel"`
			} `json:"session"`
		}
		if err := json.Unmarshal(event.Payload, &envelope); err != nil {
			t.Fatal(err)
		}
		models = append(models, envelope.Session.PendingModel)
	}
	return models
}

func TestStatusPassPublishesAPendingModelWithoutDiscovery(t *testing.T) {
	d, store, local := localStatusFixture(t)
	source := &pendingModelStatusSurface{localStatusSurface: local}
	d.Surfaces = []surface.Surface{source}
	d.discoverCatalog(context.Background())
	d.refreshCatalogStatus(context.Background())
	after := latestCatalogSeq(t, store)

	if err := os.WriteFile(source.pendingPath("from"), []byte("gpt-sol\n"), 0600); err != nil {
		t.Fatal(err)
	}
	d.refreshCatalogStatus(context.Background())
	if models := catalogPendingModels(t, d, after, "from"); len(models) != 1 || models[0] != "gpt-sol" {
		t.Fatalf("pending model events=%v", models)
	}

	if err := os.Remove(source.pendingPath("from")); err != nil {
		t.Fatal(err)
	}
	d.refreshCatalogStatus(context.Background())
	if models := catalogPendingModels(t, d, after, "from"); len(models) != 2 || models[1] != "" {
		t.Fatalf("cleared pending model events=%v", models)
	}
}
