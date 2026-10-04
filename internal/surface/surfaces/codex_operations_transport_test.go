package surfaces

import (
	"context"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestCodexOperationsUseNativeSocketAndReturnedIdentity(t *testing.T) {
	fake := startManagedCodex(t, func(method string, _ map[string]any) map[string]any {
		switch method {
		case "thread/resume":
			return map[string]any{"thread": map[string]any{"canAcceptDirectInput": true, "status": "busy"}}
		case "thread/fork":
			return map[string]any{"thread": map[string]any{"id": "fork-id", "cwd": "/fork", "source": "appServer", "name": "fork"}}
		}
		return map[string]any{"data": []any{}, "nextCursor": "page-two"}
	})
	codex := isolatedManagedRuntime(t)
	session := &surface.Session{ID: "source", Surface: surface.KindCodex, Transport: codexTransportManaged, Source: "agenthail", Cwd: "/source"}
	fork, err := codex.ForkSession(context.Background(), session, surface.ForkOptions{BeforeTurnID: "cutoff"})
	if err != nil || fork.ID != "fork-id" || fork.Cwd != "/fork" || fork.Source != "appServer" || fork.Transport != codexTransportManaged {
		t.Fatalf("fork=%+v err=%v", fork, err)
	}
	for _, action := range []string{"list", "add", "update", "delete", "reorder", "start"} {
		result, err := codex.NativeQueue(context.Background(), session, surface.NativeQueueRequest{Action: action, Message: "next", ID: "q1", ClientID: "stable", IDs: []string{"q1"}, Cursor: "page-one"})
		if err != nil || result["nextCursor"] != "page-two" {
			t.Fatal(action, result, err)
		}
		if calls := fake.Calls("thread/queue/" + action); len(calls) != 1 || calls[0]["threadId"] != "source" {
			t.Fatalf("%s calls=%v", action, calls)
		}
	}
	forkParams := fake.Calls("thread/fork")[0]
	if forkParams["beforeTurnId"] != "cutoff" || forkParams["deferGoalContinuation"] != true || forkParams["threadSource"] != "agenthail" {
		t.Fatal(forkParams)
	}
	if fake.Calls("thread/queue/add")[0]["clientUserMessageId"] != "stable" || fake.Calls("thread/queue/list")[0]["cursor"] != "page-one" {
		t.Fatal(fake.Methods())
	}
	before := len(fake.Methods())
	for _, request := range []surface.NativeQueueRequest{{Action: "add", Message: "hello"}, {Action: "update", Message: "hello"}, {Action: "delete"}, {Action: "reorder"}, {Action: "bogus"}} {
		if _, err := codex.NativeQueue(context.Background(), session, request); err == nil {
			t.Fatalf("invalid request accepted: %+v", request)
		}
	}
	if after := len(fake.Methods()); after != before {
		t.Fatalf("invalid queue requests reached the app-server: %v", fake.Methods()[before:])
	}
	session.Transport = codexTransportReadOnly
	if _, err := codex.NativeQueue(context.Background(), session, surface.NativeQueueRequest{Action: "start"}); !surface.IsDeliveryUnavailable(err) || surface.IsDeliveryOutcomeUnknown(err) {
		t.Fatal(err)
	}
}
