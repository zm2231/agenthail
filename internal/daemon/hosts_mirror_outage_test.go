package daemon

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/testkit"
)

func TestABriefHostOutageKeepsItsRowsAvailable(t *testing.T) {
	local, remote, record := pairedFederationWithGrace(t, time.Hour)
	remote.gate.proxyDown.Store(true)
	remote.server.CloseClientConnections()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, row := range sessionsOf(local.snapshot(t), record.ID) {
			if row.UnavailableReason != "" || row.Host.State != hostStateConnected {
				t.Fatalf("a host outage inside the grace marked %s %s: %q", row.ID, row.Host.State, row.UnavailableReason)
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	before := remote.gate.beyondVersion.Load()
	remote.gate.proxyDown.Store(false)
	testkit.Eventually(t, "the mirror to reconnect", 10*time.Second, func() bool {
		return remote.gate.beyondVersion.Load() > before
	})
	for _, row := range sessionsOf(local.snapshot(t), record.ID) {
		if row.UnavailableReason != "" || row.Host.State != hostStateConnected {
			t.Fatalf("after recovery %s is %s: %q", row.ID, row.Host.State, row.UnavailableReason)
		}
	}
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
