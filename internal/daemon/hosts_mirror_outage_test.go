package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/testkit"
)

func assertRowsAvailableFor(t *testing.T, local *federationNode, hostID string, span time.Duration) {
	t.Helper()
	deadline := time.Now().Add(span)
	for {
		for _, row := range sessionsOf(local.snapshot(t), hostID) {
			if row.UnavailableReason != "" || row.Host.State != hostStateConnected {
				t.Fatalf("a host outage inside the grace marked %s %s: %q", row.ID, row.Host.State, row.UnavailableReason)
			}
		}
		if !time.Now().Before(deadline) {
			return
		}
		time.Sleep(100 * time.Millisecond)
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
	local, remote, record := pairedFederationWithGrace(t, time.Hour)
	remote.gate.proxyDown.Store(true)
	remote.server.CloseClientConnections()
	assertRowsAvailableFor(t, local, record.ID, 3*time.Second)
	remote.gate.proxyDown.Store(false)
	awaitMirroredName(t, local, remote, record.ID, "after the first drop")
	remote.server.CloseClientConnections()
	assertRowsAvailableFor(t, local, record.ID, 2*time.Second)
	awaitMirroredName(t, local, remote, record.ID, "after the second drop")
	assertRowsAvailableFor(t, local, record.ID, 0)
}

func TestARevokedTokenIsShownWithoutWaitingForTheGrace(t *testing.T) {
	local, remote, record := pairedFederationWithGrace(t, time.Hour)
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
	local, _, record := pairedFederationWithGrace(t, 0)
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
