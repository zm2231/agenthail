package registry

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const (
	moveConversation = "6f1c9d2e-0b0a-4a51-9f3e-6f9a1d2c3b4e"
	moveTarget       = "h0123456789ab"
)

var testPlaceholder = MovePlaceholder{EntityID: moveTarget + "::" + moveConversation, Projection: json.RawMessage(`{"id":"` + moveTarget + "::" + moveConversation + `","surface":"claude"}`), UnavailableReason: "moved to server"}

func moveRegistry(t *testing.T) *Registry {
	t.Helper()
	r := openTestRegistry(t)
	if _, err := r.AddHost(PairedHost{ID: moveTarget, Name: "server", Endpoint: "https://server.example.ts.net:7412", Address: "100.64.0.2", Token: "ahd_secret"}); err != nil {
		t.Fatal(err)
	}
	session := surface.Session{ID: moveConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Name: "moving", PID: 41, Transcript: "/tmp/t.jsonl", HasLocal: true}
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestAMoveOutHoldsItsConversationsQueueAndHandsItToTheTarget(t *testing.T) {
	r := moveRegistry(t)
	if err := r.SetAlias("builder", moveConversation); err != nil {
		t.Fatal(err)
	}
	queued, err := r.QueueMessageWithOptions(moveConversation, "while moving", "key-1", surface.SendOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.BeginMoveOut("move-1", moveConversation, moveTarget, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := r.BeginMoveOut("move-2", moveConversation, moveTarget, []byte(`{}`)); err == nil {
		t.Fatal("a second move of the same conversation began")
	}
	if item, err := r.ClaimNextMessage(moveConversation, time.Now()); err != nil || item != nil {
		t.Fatalf("a moving conversation's queue was claimed here: %+v err=%v", item, err)
	}
	if reason, _ := r.SessionMoveRefusal(moveConversation); !strings.Contains(reason, "move") {
		t.Fatalf("refusal=%q", reason)
	}
	if _, err := r.CompleteMoveOut("move-1", testPlaceholder); !errors.As(err, &MoveConflictError{}) {
		t.Fatalf("a move that was never sent completed: %v", err)
	}
	if err := r.SetMoveState("move-1", MoveSent, "", MovePreparing); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CompleteMoveOut("move-1", testPlaceholder); err != nil {
		t.Fatal(err)
	}
	if owner, err := r.SessionHost(moveConversation); err != nil || owner != moveTarget {
		t.Fatalf("owner=%q err=%v", owner, err)
	}
	stored, err := r.Session(moveConversation)
	if err != nil || stored.PID != 0 || stored.Transcript != "" || stored.HasLocal {
		t.Fatalf("a moved session kept local state: %+v err=%v", stored, err)
	}
	if _, err := r.LookupAlias("builder"); err == nil {
		t.Fatal("the source kept the moved session's alias")
	}
	held, err := r.HostDeliveries(moveTarget)
	if err != nil || len(held) != 1 || held[0].ID != queued || held[0].Key != "key-1" || held[0].Status != "pending" {
		t.Fatalf("held=%+v err=%v", held, err)
	}
	var request map[string]any
	if err := json.Unmarshal(held[0].Request, &request); err != nil || request["action"] != "send" || request["sessionId"] != moveConversation || request["message"] != "while moving" {
		t.Fatalf("held request=%s err=%v", held[0].Request, err)
	}
	move, err := r.SessionMove("move-1")
	if err != nil || move.State != MoveDone {
		t.Fatalf("move=%+v err=%v", move, err)
	}
	if _, err := r.CompleteMoveOut("move-1", testPlaceholder); err != nil {
		t.Fatalf("completing a done move again: %v", err)
	}
	if err := r.RegisterSession(surface.Session{ID: moveConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost}); !errors.As(err, &SessionOwnedError{}) {
		t.Fatalf("a local registration took back a moved session the target serves: %v", err)
	}
	rows, err := r.HostCatalogRows(moveTarget)
	if err != nil || len(rows) != 1 || rows[0].Session.ID != moveConversation || rows[0].UnavailableReason != "moved to server" {
		t.Fatalf("the moved row is not mirrored as the target's: %+v err=%v", rows, err)
	}
	if _, _, err := r.ApplyHostCatalogRow(moveTarget, HostCatalogRow{Session: surface.Session{ID: moveConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Name: "moving"}, EntityID: testPlaceholder.EntityID, Projection: json.RawMessage(`{"id":"` + testPlaceholder.EntityID + `","surface":"claude","name":"moving"}`)}); err != nil {
		t.Fatalf("the target's own row did not replace the placeholder: %v", err)
	}
}

func TestAFailedMoveOutReleasesItsConversation(t *testing.T) {
	r := moveRegistry(t)
	if _, err := r.QueueMessageWithOptions(moveConversation, "waits for the move", "key-1", surface.SendOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := r.BeginMoveOut("move-1", moveConversation, moveTarget, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := r.SetMoveState("move-1", MoveFailed, "refused", MovePreparing, MoveSent); err != nil {
		t.Fatal(err)
	}
	item, err := r.ClaimNextMessage(moveConversation, time.Now())
	if err != nil || item == nil || item.Message != "waits for the move" {
		t.Fatalf("the queue stayed held after the move failed: %+v err=%v", item, err)
	}
	if owner, _ := r.SessionHost(moveConversation); owner != "" {
		t.Fatalf("a failed move changed the owner to %q", owner)
	}
}

func TestAMoveInIsPreparedOnceAbortedForGoodAndTakesTheSourcesRow(t *testing.T) {
	r := openTestRegistry(t)
	source := "h00000000000a"
	if _, err := r.AddHost(PairedHost{ID: source, Name: "laptop", Endpoint: "https://laptop.example.ts.net:7412", Address: "100.64.0.3", Token: "ahd_secret"}); err != nil {
		t.Fatal(err)
	}
	entity := source + "::" + moveConversation
	if _, _, err := r.ApplyHostCatalogRow(source, HostCatalogRow{Session: surface.Session{ID: moveConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost}, EntityID: entity, Projection: json.RawMessage(`{"id":"` + entity + `"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.PrepareMoveIn("move-1", moveConversation, source, []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.PrepareMoveIn("move-1", moveConversation, source, []byte(`{"a":1}`)); err != nil {
		t.Fatalf("preparing the same move again: %v", err)
	}
	if _, err := r.PrepareMoveIn("move-1", moveConversation, source, []byte(`{"a":2}`)); err == nil {
		t.Fatal("a move was prepared again with another manifest")
	}
	if move, err := r.PrepareMoveIn("move-2", moveConversation, source, []byte(`{"a":1}`)); err != nil || move.State != MovePrepared {
		t.Fatalf("a later move from the same host: move=%+v err=%v", move, err)
	}
	if superseded, _ := r.SessionMove("move-1"); superseded.State != MoveAborted {
		t.Fatalf("the earlier prepared move is %s", superseded.State)
	}
	if aborted, err := r.AbortMoveIn("move-unseen", moveConversation, source); err != nil || aborted.State != MoveAborted {
		t.Fatalf("aborting an unseen move: %+v err=%v", aborted, err)
	}
	if _, err := r.PrepareMoveIn("move-unseen", moveConversation, source, []byte(`{"a":1}`)); err == nil {
		t.Fatal("a move aborted before it arrived was prepared")
	}
	session := surface.Session{ID: moveConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, PID: 77, Transcript: "/home/user/.claude/projects/x/t.jsonl", HasLocal: true}
	if _, err := r.TakeMovedSession("move-2", session, "builder", nil); !errors.As(err, &MoveConflictError{}) {
		t.Fatalf("a move that never received its files took the session: %v", err)
	}
	if err := r.SetMoveState("move-2", MoveReceived, "", MovePrepared); err != nil {
		t.Fatal(err)
	}
	events, err := r.TakeMovedSession("move-2", session, "builder", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != "session.removed" || events[0].EntityID != entity {
		t.Fatalf("events=%+v", events)
	}
	if owner, _ := r.SessionHost(moveConversation); owner != "" {
		t.Fatalf("owner=%q", owner)
	}
	if rows, _ := r.HostCatalogRows(source); len(rows) != 0 {
		t.Fatalf("the source's mirrored row survived: %+v", rows)
	}
	if id, err := r.LookupAlias("builder"); err != nil || id != moveConversation {
		t.Fatalf("alias=%q err=%v", id, err)
	}
	if _, err := r.AbortMoveIn("move-2", moveConversation, source); !errors.As(err, &MoveConflictError{}) {
		t.Fatalf("a resumed move was aborted: %v", err)
	}
	if _, _, err := r.ApplyHostCatalogRow(source, HostCatalogRow{Session: surface.Session{ID: moveConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost}, EntityID: entity, Projection: json.RawMessage(`{"id":"` + entity + `"}`)}); !errors.As(err, &SessionOwnedError{}) {
		t.Fatalf("the source's late row took the session back: %v", err)
	}
}

func TestAMoveInTakesASessionAThirdHostNoLongerServes(t *testing.T) {
	r := openTestRegistry(t)
	source, third := "h00000000000a", "h00000000000b"
	for id, name := range map[string]string{source: "laptop", third: "vps"} {
		if _, err := r.AddHost(PairedHost{ID: id, Name: name, Endpoint: "https://" + name + ".example.ts.net:7412", Address: "100.64.0.3", Token: "ahd_secret"}); err != nil {
			t.Fatal(err)
		}
	}
	entity := third + "::" + moveConversation
	if _, _, err := r.ApplyHostCatalogRow(third, HostCatalogRow{Session: surface.Session{ID: moveConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost}, EntityID: entity, Projection: json.RawMessage(`{"id":"` + entity + `"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.PrepareMoveIn("move-1", moveConversation, source, []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := r.SetMoveState("move-1", MoveReceived, "", MovePrepared); err != nil {
		t.Fatal(err)
	}
	session := surface.Session{ID: moveConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, HasLocal: true}
	if _, err := r.TakeMovedSession("move-1", session, "", nil); !errors.As(err, &SessionOwnedError{}) {
		t.Fatalf("a move took a session another host still serves: %v", err)
	}
	if _, _, err := r.RemoveHostCatalogRow(third, moveConversation, entity, "gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.TakeMovedSession("move-1", session, "", nil); err != nil {
		t.Fatalf("a move could not take a session its last host stopped serving: %v", err)
	}
	if owner, _ := r.SessionHost(moveConversation); owner != "" {
		t.Fatalf("owner=%q", owner)
	}
}
