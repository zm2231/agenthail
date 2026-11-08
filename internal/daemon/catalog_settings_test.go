package daemon

import (
	"context"
	"sync"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

type settingsStatusSurface struct {
	*localStatusSurface
	mu       sync.Mutex
	settings *surface.ObservedSettings
}

func (s *settingsStatusSurface) observe(settings *surface.ObservedSettings) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings = settings
}

func (s *settingsStatusSurface) LocalStatus(ctx context.Context, session surface.Session) (surface.Session, error) {
	session, err := s.localStatusSurface.LocalStatus(ctx, session)
	s.mu.Lock()
	defer s.mu.Unlock()
	session.Settings = nil
	if session.ID == "from" && s.settings != nil {
		settings := *s.settings
		session.Settings = &settings
	}
	return session, err
}

func (s *settingsStatusSurface) List(ctx context.Context) ([]surface.Session, error) {
	sessions, err := s.localStatusSurface.List(ctx)
	for index := range sessions {
		sessions[index], _ = s.LocalStatus(ctx, sessions[index])
	}
	return sessions, err
}

func TestARowShowsTheSettingsItsSessionReportsAfterTheyChange(t *testing.T) {
	d, _, local := localStatusFixture(t)
	source := &settingsStatusSurface{localStatusSurface: local}
	d.Surfaces = []surface.Surface{source}
	source.observe(&surface.ObservedSettings{Effort: "high", Permission: "default"})
	d.discoverCatalog(context.Background())
	row := func() *surface.ObservedSettings {
		t.Helper()
		state, err := d.dashboardState(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, session := range state.Sessions {
			if session.ID == "from" {
				return session.Settings
			}
		}
		t.Fatal("no catalog row for the session")
		return nil
	}
	if got := row(); got == nil || *got != (surface.ObservedSettings{Effort: "high", Permission: "default"}) {
		t.Fatalf("settings after discovery = %+v", got)
	}

	source.observe(&surface.ObservedSettings{Effort: "xhigh", Permission: "plan"})
	local.writeStatus(t, "from", surface.StatusIdle)
	d.refreshCatalogStatus(context.Background())
	if got := row(); got == nil || *got != (surface.ObservedSettings{Effort: "xhigh", Permission: "plan"}) {
		t.Fatalf("settings after they changed = %+v", got)
	}

	source.observe(nil)
	local.writeStatus(t, "from", surface.StatusIdle)
	d.refreshCatalogStatus(context.Background())
	if got := row(); got != nil {
		t.Fatalf("settings %+v kept once the session stopped reporting them", got)
	}
}
