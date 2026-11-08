package surfaces

import (
	"context"
	"testing"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestACodexRequestAnsweredElsewhereIsNotNamedAfterTheNextOneOpens(t *testing.T) {
	fake := startManagedCodex(t, nil)
	fake.AddThread(codextest.Thread{ID: managedThreadID, Originator: "agenthail", Source: "agenthail", Turns: []codextest.Turn{{ID: "turn-1", Status: "inProgress"}}})
	fake.Load(managedThreadID)
	approval := func(item string) map[string]any {
		return map[string]any{"threadId": managedThreadID, "turnId": "turn-1", "itemId": item, "command": "make " + item, "startedAtMs": float64(1)}
	}
	fake.OpenRequest(1, codexCommandApproval, approval("call_1"))
	codex := isolatedManagedRuntime(t)
	ctx := context.Background()
	session, err := codex.Resolve(ctx, managedThreadID)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := codex.Observe(ctx, session)
	if err != nil || observation.WaitingOn == nil || observation.WaitingOn.ID != managedThreadID+":call_1" {
		t.Fatalf("first wait %+v %v", observation, err)
	}
	fake.Answer(1)
	fake.OpenRequest(2, codexCommandApproval, approval("call_2"))
	sessions, err := codex.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, listed := range sessions {
		if listed.ID == managedThreadID && (listed.WaitingOn == nil || listed.WaitingOn.ID == managedThreadID+":call_1") {
			t.Fatalf("the listing waits on %+v after call_1 was answered", listed.WaitingOn)
		}
	}
	observation, err = codex.Observe(ctx, session)
	if err != nil || observation.WaitingOn == nil || observation.WaitingOn.ID != managedThreadID+":call_2" || !observation.WaitingOn.Answerable {
		t.Fatalf("second wait %+v %v", observation, err)
	}
	if _, err := codex.Respond(ctx, session, managedThreadID+":call_1", surface.Decision{Behavior: "allow"}); err == nil {
		t.Fatal("the answered request took a second answer")
	}
}

func TestACodexRequestRefusesADenialItDoesNotOffer(t *testing.T) {
	fake := startManagedCodex(t, nil)
	fake.AddThread(codextest.Thread{ID: managedThreadID, Originator: "agenthail", Source: "agenthail", Turns: []codextest.Turn{{ID: "turn-1", Status: "inProgress"}}})
	fake.Load(managedThreadID)
	fake.OpenRequest(1, codexCommandApproval, map[string]any{"threadId": managedThreadID, "turnId": "turn-1", "itemId": "call_1", "command": "make", "startedAtMs": float64(1), "availableDecisions": []any{"accept", "decline"}})
	fake.OpenRequest(2, codexFileChangeApproval, map[string]any{"threadId": managedThreadID, "turnId": "turn-1", "itemId": "patch_1", "startedAtMs": float64(2)})
	fake.OpenRequest(3, codexPermissionApproval, map[string]any{"threadId": managedThreadID, "turnId": "turn-1", "itemId": "perm_1", "startedAtMs": float64(3), "permissions": map[string]any{}})
	codex := isolatedManagedRuntime(t)
	ctx := context.Background()
	session, err := codex.Resolve(ctx, managedThreadID)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		item     string
		decision surface.Decision
	}{
		{"call_1", surface.Decision{Behavior: "deny", Stop: true}},
		{"call_1", surface.Decision{Behavior: "deny", Always: true}},
		{"call_1", surface.Decision{Behavior: "allow", Always: true}},
		{"patch_1", surface.Decision{Behavior: "deny", Always: true}},
		{"perm_1", surface.Decision{Behavior: "deny", Stop: true}},
		{"perm_1", surface.Decision{Behavior: "deny", Always: true}},
	} {
		if _, err := codex.Respond(ctx, session, managedThreadID+":"+c.item, c.decision); err == nil {
			t.Fatalf("%s took %+v, which it does not offer", c.item, c.decision)
		}
	}
	if responses := fake.Responses(); len(responses) != 0 {
		t.Fatalf("Codex was answered %v", responses)
	}
}
