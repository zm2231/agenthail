package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/dashconfig"
	"github.com/zm2231/agenthail/internal/projectresolve"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestASettledTranscriptKeepsTheRowsLastPlanAndTurnStop(t *testing.T) {
	d, r, fake, session, _ := daemonFixture(t)
	view, err := d.loadCatalogViewState()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.RecordObservedSession(session); err != nil {
		t.Fatal(err)
	}
	publish := func(session surface.Session) catalogStoredListing {
		t.Helper()
		if !d.publishCatalogSession(context.Background(), fake, session, nil, projectresolve.CatalogIdentity{}, "", 0, nil, false, dashconfig.Config{}, view, time.Now().UTC(), nil, nil) {
			t.Fatalf("%s was not published", session.ID)
		}
		return d.storedListing(session.ID)
	}
	plan := &surface.Plan{Done: 2, Total: 3, Current: "Run gates"}
	stop := &surface.TurnStop{TurnID: "turn", At: time.Now().Add(-8 * 24 * time.Hour).UTC().Truncate(time.Second), Reason: "failed"}
	read := session
	read.Plan, read.TurnStop = plan, stop
	publish(read)
	settled := session
	settled.SettledTranscript = true
	stored := publish(settled)
	if stored.Plan == nil || *stored.Plan != *plan || stored.TurnStop == nil || *stored.TurnStop != *stop {
		t.Fatalf("settled republish stored plan=%+v stop=%+v, want the last read %+v %+v", stored.Plan, stored.TurnStop, plan, stop)
	}
	stored = publish(session)
	if stored.Plan != nil || stored.TurnStop != nil {
		t.Fatalf("a read that found no plan or stop stored plan=%+v stop=%+v, want both cleared", stored.Plan, stored.TurnStop)
	}
}
