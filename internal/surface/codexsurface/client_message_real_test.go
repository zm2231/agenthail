package codexsurface

import (
	"context"
	"testing"
	"time"

	"github.com/tidwall/gjson"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestEachMessageAgenthailGivesCodexCarriesItsDeliveryIDOnItsTurn(t *testing.T) {
	codex, follower := followingRuntime(t)
	ctx := context.Background()
	session, first, err := codex.StartSession(surface.WithMessageUUID(ctx, "delivery-first"), surface.SessionStartOptions{Message: "first", Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if end := rolloutTurnEnd(t, session.ID, first.UUID); end != "task_complete" {
		t.Fatalf("the first turn ended with %s", end)
	}
	awaitNoActiveTurn(t, codex, follower, session.ID)
	session.Status = surface.StatusIdle
	sent, err := codex.Send(surface.WithMessageUUID(ctx, "delivery-sent"), session, "second")
	if err != nil || !sent.Accepted {
		t.Fatalf("send to the idle thread: %+v %v", sent, err)
	}
	if err := codex.Steer(surface.WithMessageUUID(ctx, "delivery-steer"), session, "also this"); err != nil {
		t.Fatalf("steer the running turn: %v", err)
	}
	if end := rolloutTurnEnd(t, session.ID, sent.UUID); end != "task_complete" {
		t.Fatalf("the sent turn ended with %s", end)
	}
	response, err := follower.Request(ctx, "thread/turns/list", map[string]any{"threadId": session.ID, "limit": 5, "sortDirection": "desc", "itemsView": "full"}, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	turnOf := map[string]string{}
	for _, turn := range gjson.Get(codexJSON(response), "result.data").Array() {
		for _, item := range turn.Get("items").Array() {
			if item.Get("type").String() == "userMessage" {
				turnOf[item.Get("clientId").String()] = turn.Get("id").String()
			}
		}
	}
	for clientID, turnID := range map[string]string{"delivery-first": first.UUID, "delivery-sent": sent.UUID, "delivery-steer": sent.UUID} {
		if turnOf[clientID] != turnID {
			t.Errorf("the message delivered as %s is on turn %q, want %q (user messages by client id: %v)", clientID, turnOf[clientID], turnID, turnOf)
		}
	}
}
