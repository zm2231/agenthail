package daemon

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/testkit"
)

// assertRowsAvailableUntil checks the host's rows on every pass until done
// reports true, so the outage it spans is the one done waits for.
func assertRowsAvailableUntil(t *testing.T, local *federationNode, hostID, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		for _, row := range sessionsOf(local.snapshot(t), hostID) {
			if row.UnavailableReason != "" || row.Host.State != hostStateConnected {
				t.Fatalf("a host outage inside the grace marked %s %s: %q", row.ID, row.Host.State, row.UnavailableReason)
			}
		}
		if done() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func awaitMirroredName(t *testing.T, local, remote *federationNode, hostID, name string) {
	t.Helper()
	remote.fake.mu.Lock()
	session := remote.fake.sessions["remote-a"]
	session.Name, session.LastActive = name, time.Now()
	remote.fake.sessions["remote-a"] = session
	remote.fake.mu.Unlock()
	remote.daemon.discoverCatalog(context.Background())
	testkit.Eventually(t, "the mirror to follow the host again", 10*time.Second, func() bool {
		for _, row := range sessionsOf(local.snapshot(t), hostID) {
			if row.Name == name {
				return true
			}
		}
		return false
	})
}

func TestABriefHostOutageKeepsItsRowsAvailable(t *testing.T) {
	local, remote, record := pairedFederationWith(t, func(f *federation) {
		f.downGrace, f.retryMin = time.Hour, 50*time.Millisecond
	})
	refused := remote.gate.proxyRefusals.Load()
	remote.gate.proxyDown.Store(true)
	remote.server.CloseClientConnections()
	assertRowsAvailableUntil(t, local, record.ID, "three failed reconnects", func() bool { return remote.gate.proxyRefusals.Load()-refused >= 3 })
	streams := remote.gate.streams.Load()
	remote.gate.proxyDown.Store(false)
	awaitNewStream(t, remote, streams)
	awaitMirroredName(t, local, remote, record.ID, "after the first drop")
	streams = remote.gate.streams.Load()
	remote.server.CloseClientConnections()
	assertRowsAvailableUntil(t, local, record.ID, "the mirror to reconnect", func() bool { return remote.gate.streams.Load() > streams })
	awaitMirroredName(t, local, remote, record.ID, "after the second drop")
	assertRowsAvailableUntil(t, local, record.ID, "a last look", func() bool { return true })
}

func TestAHostWhoseSnapshotsFailIsShownDownThoughItsVersionAnswers(t *testing.T) {
	local, remote, record := pairedFederationWith(t, func(f *federation) {
		f.downGrace, f.retryMin = 300*time.Millisecond, 50*time.Millisecond
	})
	remote.gate.snapshotsDown.Store(true)
	remote.server.CloseClientConnections()
	testkit.Eventually(t, "the host to be shown down while its snapshots fail", 10*time.Second, func() bool {
		rows := sessionsOf(local.snapshot(t), record.ID)
		return len(rows) > 0 && rows[0].Host.State != hostStateConnected && rows[0].UnavailableReason != ""
	})
	remote.gate.snapshotsDown.Store(false)
	testkit.Eventually(t, "the host to be connected once its snapshots answer", 10*time.Second, func() bool {
		rows := sessionsOf(local.snapshot(t), record.ID)
		return len(rows) > 0 && rows[0].Host.State == hostStateConnected && rows[0].UnavailableReason == ""
	})
}

func awaitNewStream(t *testing.T, remote *federationNode, after int64) {
	t.Helper()
	testkit.Eventually(t, "the mirror to open a new catalog stream", 10*time.Second, func() bool {
		return remote.gate.streams.Load() > after
	})
}

func TestARevokedTokenIsShownWithoutWaitingForTheGrace(t *testing.T) {
	local, remote, record := pairedFederationWith(t, func(f *federation) { f.downGrace = time.Hour })
	devices, err := remote.reg.ListDevices(false)
	if err != nil || len(devices) != 1 {
		t.Fatalf("devices=%+v err=%v", devices, err)
	}
	if err := remote.reg.RevokeDevice(devices[0].ID); err != nil {
		t.Fatal(err)
	}
	remote.server.CloseClientConnections()
	testkit.Eventually(t, "unauthorized host", 10*time.Second, func() bool {
		rows := sessionsOf(local.snapshot(t), record.ID)
		return len(rows) == 2 && rows[0].Host.State == hostStateUnauthorized && strings.Contains(rows[0].UnavailableReason, "pair it again")
	})
}

func TestALocalRegistryErrorNeverMarksTheHostDown(t *testing.T) {
	local, _, record := pairedFederationWith(t, func(f *federation) { f.downGrace = 0 })
	host, _ := local.daemon.pairedHost(record.ID)
	var outage hostOutage
	local.daemon.hostFailed(host, &outage, localMirror(errors.New("database is locked")))
	if !outage.since.IsZero() || outage.down {
		t.Fatalf("a local error started an outage: %+v", outage)
	}
	for _, row := range sessionsOf(local.snapshot(t), record.ID) {
		if row.UnavailableReason != "" || row.Host.State != hostStateConnected {
			t.Fatalf("a local error marked %s %s: %q", row.ID, row.Host.State, row.UnavailableReason)
		}
	}
}

// A payload shape this build cannot read fails again as soon as the follower
// resumes; the snapshots that repair it must settle at one per ceiling rather
// than start over at once.
func TestRepairSnapshotsForEventsThatKeepFailingSettleAtTheCeiling(t *testing.T) {
	var backoff repairBackoff
	now := time.Unix(0, 0)
	var waits []time.Duration
	for range 8 {
		wait := backoff.next(now, time.Second, 8*time.Second)
		waits = append(waits, wait)
		now = now.Add(wait + 100*time.Millisecond)
	}
	want := []time.Duration{0, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second, 8 * time.Second, 8 * time.Second}
	if !slices.Equal(waits, want) {
		t.Fatalf("waits = %v, want %v", waits, want)
	}
	if wait := backoff.next(now.Add(9*time.Second), time.Second, 8*time.Second); wait != 0 {
		t.Fatalf("a failure long after the follower resumed waits %s, want a snapshot at once", wait)
	}
}
