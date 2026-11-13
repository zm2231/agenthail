package registry

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func presentationVersion(t *testing.T, r *Registry) uint64 {
	t.Helper()
	key, err := r.ReadCatalogPresentationKey(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return key.Version
}

func shownMove(t *testing.T, r *Registry) *MoveShown {
	t.Helper()
	move, err := r.UnfinishedMoveOut(moveConversation)
	if err != nil {
		t.Fatal(err)
	}
	all, err := r.UnfinishedMovesOut()
	if err != nil {
		t.Fatal(err)
	}
	if listed, ok := all[moveConversation]; ok != (move != nil) || move != nil && listed != *move {
		t.Fatalf("one conversation's move=%+v, all moves=%+v", move, all)
	}
	read, err := r.ReadCatalogPresentation(context.Background(), func(string, uint64) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range read.Rows {
		if row.ID == moveConversation && (row.Move == nil) != (move == nil) || row.Move != nil && *row.Move != *move {
			t.Fatalf("presentation move=%+v, read move=%+v", row.Move, move)
		}
	}
	return move
}

func TestARowShowsItsUnfinishedMoveOutAndEveryChangeMovesThePresentation(t *testing.T) {
	r := moveRegistry(t)
	if _, _, err := r.RecordCatalogSession(CatalogSessionState{Session: surface.Session{ID: moveConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Status: surface.StatusIdle}, Project: json.RawMessage(`{}`), Checkout: json.RawMessage(`{}`), ObservedAt: time.Now().UTC(), ProjectionFingerprint: `{}`}, CatalogEvent{DedupeKey: "moving", Type: "session.upserted", EntityID: moveConversation, Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if move := shownMove(t, r); move != nil {
		t.Fatalf("a session never moved shows %+v", move)
	}
	version := presentationVersion(t, r)
	step := func(name string, want *MoveShown) {
		t.Helper()
		next := presentationVersion(t, r)
		if next == version {
			t.Fatalf("%s did not move the presentation version", name)
		}
		version = next
		move := shownMove(t, r)
		if (move == nil) != (want == nil) || move != nil && *move != *want {
			t.Fatalf("%s: move=%+v, want %+v", name, move, want)
		}
	}
	if err := r.BeginMoveOut("move-1", moveConversation, moveTarget, []byte(`{"root":"work"}`), MoveCarry{}, ""); err != nil {
		t.Fatal(err)
	}
	step("beginning", &MoveShown{ID: "move-1", State: MoveShownHolding, PeerHost: moveTarget, HostName: "server", Root: "work"})
	if err := r.MarkMoveOutcomeUnknown("move-1", "no answer"); err != nil {
		t.Fatal(err)
	}
	if move := shownMove(t, r); move.State != MoveShownHolding || move.Detail != "" {
		t.Fatalf("a move not sent yet became %+v", move)
	}
	if err := r.SetMoveState("move-1", MoveSent, "", MovePreparing); err != nil {
		t.Fatal(err)
	}
	step("sending", &MoveShown{ID: "move-1", State: MoveShownCopying, PeerHost: moveTarget, HostName: "server", Root: "work"})
	if err := r.MarkMoveOutcomeUnknown("move-1", "no answer"); err != nil {
		t.Fatal(err)
	}
	step("losing the answer", &MoveShown{ID: "move-1", State: MoveShownSettling, PeerHost: moveTarget, HostName: "server", Root: "work", Detail: "no answer"})
	if err := r.MarkMoveOutcomeUnknown("move-1", "no answer"); err != nil {
		t.Fatal(err)
	}
	if next := presentationVersion(t, r); next != version {
		t.Fatal("losing the same answer again moved the presentation version")
	}
	if err := r.SetMoveState("move-1", MoveFailed, "refused", MoveSent); err != nil {
		t.Fatal(err)
	}
	step("failing", nil)
	var unknownAt string
	if err := r.read.QueryRow(`SELECT outcome_unknown_at FROM session_moves WHERE id='move-1'`).Scan(&unknownAt); err != nil || unknownAt != "" {
		t.Fatalf("a finished move kept outcome_unknown_at=%q err=%v", unknownAt, err)
	}
}

func TestAMoveWhoseOutcomeIsUnknownStillKeepsQueuedDeliveriesOffTheSession(t *testing.T) {
	r := moveRegistry(t)
	if _, err := r.QueueMessageWithOptions(moveConversation, "waits for the move", "key-1", surface.SendOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := r.BeginMoveOut("move-1", moveConversation, moveTarget, []byte(`{}`), MoveCarry{}, ""); err != nil {
		t.Fatal(err)
	}
	if err := r.SetMoveState("move-1", MoveSent, "", MovePreparing); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkMoveOutcomeUnknown("move-1", "the host did not answer"); err != nil {
		t.Fatal(err)
	}
	if move := shownMove(t, r); move == nil || move.State != MoveShownSettling {
		t.Fatalf("move=%+v", move)
	}
	for name, claim := range map[string]func(string, time.Time) (*QueuedMessage, error){"message": r.ClaimNextMessage, "steer": r.ClaimNextSteerMessage, "peer": r.ClaimNextPeerMessage} {
		if item, err := claim(moveConversation, time.Now()); err != nil || item != nil {
			t.Fatalf("a %s claim reached a session whose move is unsettled: %+v err=%v", name, item, err)
		}
	}
	if reason, err := r.SessionMoveRefusal(moveConversation, true); err != nil || reason == "" {
		t.Fatalf("a second move of a session whose move is unsettled was not refused: %q err=%v", reason, err)
	}
}

func TestRemovingTheTargetHostOfAnUnfinishedMoveMovesThePresentation(t *testing.T) {
	r := moveRegistry(t)
	if err := r.BeginMoveOut("move-1", moveConversation, moveTarget, []byte(`{"root":"work"}`), MoveCarry{}, ""); err != nil {
		t.Fatal(err)
	}
	if move := shownMove(t, r); move == nil || move.HostName != "server" {
		t.Fatalf("move=%+v", move)
	}
	version := presentationVersion(t, r)
	if _, err := r.RemoveHost(moveTarget); err != nil {
		t.Fatal(err)
	}
	if presentationVersion(t, r) == version {
		t.Fatal("removing the target host of an unfinished move did not move the presentation version")
	}
	if move := shownMove(t, r); move == nil || move.HostName != "" {
		t.Fatalf("after removing its target host the move shows %+v", move)
	}
}
