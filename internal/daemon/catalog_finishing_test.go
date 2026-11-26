package daemon

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// finishingStatusSurface reports a reply that ended at endedAt from a process
// still busy, as Claude does while Stop hooks run, read at its own clock.
type finishingStatusSurface struct {
	*localStatusSurface
	endedAt time.Time
	now     atomic.Pointer[time.Time]
}

func (s *finishingStatusSurface) LocalStatus(ctx context.Context, session surface.Session) (surface.Session, error) {
	session, err := s.localStatusSurface.LocalStatus(ctx, session)
	session.Activity = nil
	if session.ID == "from" {
		activity := &surface.Activity{BusySince: s.endedAt.Add(-time.Minute), LastProgressAt: s.endedAt, EndedAt: s.endedAt}
		activity.Finishing(*s.now.Load())
		session.Activity = activity
	}
	return session, err
}

func (s *finishingStatusSurface) List(ctx context.Context) ([]surface.Session, error) {
	sessions, err := s.localStatusSurface.List(ctx)
	for index := range sessions {
		sessions[index], _ = s.LocalStatus(ctx, sessions[index])
	}
	return sessions, err
}

func TestTheStatusPassMarksAReplyFinishingOnceItsGraceEndsWithoutAFileChange(t *testing.T) {
	d, _, local := localStatusFixture(t)
	source := &finishingStatusSurface{localStatusSurface: local, endedAt: time.Now().Add(-surface.FinishingGrace)}
	before := source.endedAt.Add(surface.FinishingGrace - time.Millisecond)
	source.now.Store(&before)
	d.Surfaces = []surface.Surface{source}
	local.writeStatus(t, "from", surface.StatusBusy)
	d.discoverCatalog(context.Background())
	activity := func() *surface.Activity {
		t.Helper()
		state, err := d.dashboardState(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, session := range state.Sessions {
			if session.ID == "from" {
				return session.Activity
			}
		}
		t.Fatal("no catalog row for the finishing session")
		return nil
	}
	d.refreshCatalogStatus(context.Background())
	if got := activity(); got == nil || !got.FinishingSince.IsZero() {
		t.Fatalf("activity %+v before the grace ended, want one not yet finishing", got)
	}
	after := source.endedAt.Add(surface.FinishingGrace)
	source.now.Store(&after)
	d.refreshCatalogStatus(context.Background())
	if got := activity(); got == nil || !got.FinishingSince.Equal(source.endedAt) {
		t.Fatalf("activity %+v after the grace, want finishing since %s", got, source.endedAt)
	}
}
