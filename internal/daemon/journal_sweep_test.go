package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

func journalSweepRow(t *testing.T, r *registry.Registry, session, key string, at time.Time) {
	t.Helper()
	payload, err := json.Marshal(sessionJournalPayload{ItemID: key, ProviderKey: key, Op: "upsert", Version: 1, Kind: "toolCall", TS: at.UTC().Format(time.RFC3339), Body: strings.Repeat("the same output ", 200)})
	if err != nil {
		t.Fatal(err)
	}
	entry := registry.SessionJournalEntry{SessionID: session, Kind: "toolCall", ProviderKey: key, Payload: payload, ObservedAt: at}
	if _, _, err := r.AppendSessionJournalEntry(entry, registry.SessionJournalRetention{Count: sessionJournalRetentionCount, Bytes: sessionJournalRetentionBytes}); err != nil {
		t.Fatal(err)
	}
}

func TestASweepCompressesIdleSessionsLogsItAndChangesNoRead(t *testing.T) {
	d, r, _, idle, recent := daemonFixture(t)
	now := time.Now()
	journalSweepRow(t, r, idle.ID, "idle-1", now.Add(-8*24*time.Hour))
	journalSweepRow(t, r, recent.ID, "recent-1", now.Add(-6*24*time.Hour))
	before, err := d.readJournalPage(idle.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	var logged bytes.Buffer
	d.log = log.New(&logged, "", 0)

	d.sweepJournals(context.Background(), now)

	if !strings.Contains(logged.String(), "journal sweep: 1 rows in 1 sessions") {
		t.Fatalf("log:\n%s", logged.String())
	}
	after, err := d.readJournalPage(idle.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.Items, after.Items) {
		t.Fatalf("the page changed:\n%+v\n%+v", before.Items, after.Items)
	}
	ids, err := r.IdleJournalSessions(now.Add(time.Hour))
	if err != nil || len(ids) != 1 || ids[0] != recent.ID {
		t.Fatalf("the recent session should still be unswept: %v %v", ids, err)
	}

	logged.Reset()
	d.sweepJournals(context.Background(), now)
	if strings.Contains(logged.String(), "journal sweep:") {
		t.Fatalf("a second sweep had work:\n%s", logged.String())
	}
}

func TestACancelledSweepStops(t *testing.T) {
	d, r, _, idle, other := daemonFixture(t)
	old := time.Now().Add(-30 * 24 * time.Hour)
	journalSweepRow(t, r, idle.ID, "a", old)
	journalSweepRow(t, r, other.ID, "b", old)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d.sweepJournals(ctx, time.Now())
	ids, err := r.IdleJournalSessions(time.Now())
	if err != nil || len(ids) != 1 {
		t.Fatalf("one session is swept before the pause notices: %v %v", ids, err)
	}
}
