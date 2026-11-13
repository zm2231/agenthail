package daemon

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestAThirdHostReachesAMovedSessionOnItsNewHostOnceTheHostItLeftSaysWhere(t *testing.T) {
	source, target, third := moveMesh(t, nil)
	session := startMovable(t, source)
	feeder := startMovable(t, third)
	sourceID, targetID := mustHostID(t, source.federationNode), mustHostID(t, target.federationNode)
	mirroredOn(t, third, source, sourceID, session.ID)
	relayFrom(t, third, feeder, session.ID)
	if _, err := source.daemon.moveSession(context.Background(), sessionMoveRequest{SessionID: session.ID, Host: "b"}); err != nil {
		t.Fatal(err)
	}
	ownedBy := func(want string) func() bool {
		return func() bool {
			host, err := third.reg.SessionHost(session.ID)
			return err == nil && host == want
		}
	}
	reachesTarget := func(t *testing.T, text string) {
		t.Helper()
		reply := takeTurn(t, third, feeder.ID, text)
		testkit.Eventually(t, "the relay to reach the session on its new host", 15*time.Second, func() bool {
			third.daemon.syncRelays(context.Background(), third.claude, feeder)
			return relayed(t, target, session.ID, reply) == 1
		})
	}

	t.Run("from the host it left announcing the move, with that host then gone", func(t *testing.T) {
		testkit.Eventually(t, "the third host to record the session on its new host", 15*time.Second, ownedBy(targetID))
		testkit.Eventually(t, "the third host to list the session's new row", 15*time.Second, func() bool {
			target.daemon.discoverCatalog(context.Background())
			ids, err := third.reg.HostCatalogRowIDs(targetID)
			return err == nil && slices.Contains(ids, session.ID)
		})
		source.gate.proxyDown.Store(true)
		t.Cleanup(func() { source.gate.proxyDown.Store(false) })
		reachesTarget(t, "with the old host gone")
		if got := source.gate.proxyRefusals.Load(); got != 0 {
			t.Fatalf("the third host still asked the host the session left %d times", got)
		}
	})

	t.Run("from the host it left answering moved", func(t *testing.T) {
		source.gate.proxyDown.Store(false)
		if _, err := openSweepDB(t, third.reg.Path()).Exec(`UPDATE sessions SET host_id=? WHERE id=?`, sourceID, session.ID); err != nil {
			t.Fatal(err)
		}
		reachesTarget(t, "after a moved answer")
		testkit.Eventually(t, "the moved answer to record the session on its new host", 15*time.Second, ownedBy(targetID))
	})

	t.Run("from a row of the new host that arrived before the move was announced", func(t *testing.T) {
		db := openSweepDB(t, third.reg.Path())
		if _, err := db.Exec(`UPDATE sessions SET host_id=? WHERE id=?`, sourceID, session.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`DELETE FROM catalog_sessions WHERE session_id=?`, session.ID); err != nil {
			t.Fatal(err)
		}
		from, _ := third.daemon.pairedHost(sourceID)
		to, _ := third.daemon.pairedHost(targetID)
		early := dashboardSession{ID: session.ID, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Name: "arrived early", Status: surface.StatusIdle}
		var owned registry.SessionOwnedError
		if err := third.daemon.mirrorOrHide(to, early, true); !errors.As(err, &owned) || owned.HostID != sourceID {
			t.Fatalf("the new host's row before the handover answered %v, want it refused as the old host's", err)
		}
		if !third.daemon.handOverHostSession(from, session.ID, targetID) || !ownedBy(targetID)() {
			t.Fatal("the old host's word did not hand the session over")
		}
		if ids, err := third.reg.HostCatalogRowIDs(targetID); err != nil || !slices.Contains(ids, session.ID) {
			t.Fatalf("the new host's rows after the handover are %v err=%v, want the row it sent early", ids, err)
		}
	})

	t.Run("from the snapshot of the host it left, when the third host reconnects", func(t *testing.T) {
		if _, err := openSweepDB(t, third.reg.Path()).Exec(`UPDATE sessions SET host_id=? WHERE id=?`, sourceID, session.ID); err != nil {
			t.Fatal(err)
		}
		snapshots := source.gate.snapshotsServed.Load()
		source.server.CloseClientConnections()
		testkit.Eventually(t, "the snapshot to record the session on its new host", 15*time.Second, ownedBy(targetID))
		if source.gate.snapshotsServed.Load() == snapshots {
			t.Fatal("the session was handed over without a snapshot of the host it left")
		}
	})
}
