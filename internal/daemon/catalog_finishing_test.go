package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// finishingStatusSurface reports a reply that ended at endedAt from a process
// still busy, as Claude does while Stop hooks run.
type finishingStatusSurface struct {
	*localStatusSurface
	endedAt time.Time
}

func (s *finishingStatusSurface) LocalStatus(ctx context.Context, session surface.Session) (surface.Session, error) {
	session, err := s.localStatusSurface.LocalStatus(ctx, session)
	session.Activity = nil
	if session.ID == "from" {
		activity := &surface.Activity{BusySince: s.endedAt.Add(-time.Minute), LastProgressAt: s.endedAt, EndedAt: s.endedAt}
		activity.Finishing(time.Now())
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
	source := &finishingStatusSurface{localStatusSurface: local, endedAt: time.Now().Add(300*time.Millisecond - surface.FinishingGrace)}
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
	time.Sleep(time.Until(source.endedAt.Add(surface.FinishingGrace)) + 50*time.Millisecond)
	d.refreshCatalogStatus(context.Background())
	if got := activity(); got == nil || !got.FinishingSince.Equal(source.endedAt) {
		t.Fatalf("activity %+v after the grace, want finishing since %s", got, source.endedAt)
	}
}
