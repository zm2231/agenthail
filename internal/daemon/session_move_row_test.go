package daemon

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestAMirroredRowShowsThePeersMoveUntilThePeerDropsIt(t *testing.T) {
	_, store := openPresentationRegistry(t)
	record, err := store.AddHost(registry.PairedHost{ID: "h0123456789ab", Name: "peer", Endpoint: "https://peer.example", Address: "100.64.0.2", Token: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	d := newTestDaemon(t, store, nil)
	peer := &pairedHost{record: record}
	row := dashboardSession{ID: "moving", Name: "moving", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Status: surface.StatusIdle, Move: &moveView{State: registry.MoveShownSettling, Host: "h00000000000c", HostName: "laptop", Detail: "no answer"}}
	if err := d.mirrorRemoteRow(peer, row, false); err != nil {
		t.Fatal(err)
	}
	if move := viewSession(t, d, record.ID+"::moving").Move; move == nil || *move != *row.Move {
		t.Fatalf("the mirrored row's move=%+v, want %+v", move, row.Move)
	}
	row.Move = nil
	if err := d.mirrorRemoteRow(peer, row, false); err != nil {
		t.Fatal(err)
	}
	if move := viewSession(t, d, record.ID+"::moving").Move; move != nil {
		t.Fatalf("the mirrored row kept a move its host dropped: %+v", move)
	}
}

func TestRecoveryDropsTheMoveFromTheRowOfAMoveNoAdapterCanSettle(t *testing.T) {
	source, _, record, _ := movePair(t)
	session := startMovable(t, source)
	source.daemon.discoverCatalog(context.Background())
	if err := source.reg.BeginMoveOut("move-1", session.ID, record.ID, []byte(`{}`), registry.MoveCarry{}, source.daemon.hostIdentity().ID); err != nil {
		t.Fatal(err)
	}
	source.daemon.publishMove(session.ID)
	waitForPublishedMove(t, source, session.ID, record, registry.MoveShownHolding)

	source.daemon.failInterruptedMoves()
	if move, err := source.reg.SessionMove("move-1"); err != nil || move.State != registry.MoveFailed {
		t.Fatalf("move=%+v err=%v", move, err)
	}
	testkit.Eventually(t, "the row to drop the failed move", 10*time.Second, func() bool {
		fingerprint, _, err := source.reg.CatalogProjectionFingerprint(session.ID)
		var row dashboardSession
		return err == nil && json.Unmarshal([]byte(fingerprint), &row) == nil && row.Move == nil
	})
}
