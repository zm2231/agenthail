package surfaces

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func refusedCodexMove(t *testing.T, children, queued []any) surface.MoveRefusal {
	t.Helper()
	startManagedCodex(t, func(method string, params map[string]any) map[string]any {
		switch method {
		case "thread/loaded/list":
			return map[string]any{"data": []any{managedThreadID}}
		case "thread/read":
			return map[string]any{"thread": map[string]any{"id": managedThreadID, "source": "agenthail", "status": map[string]any{"type": "idle"}}}
		case "thread/turns/list":
			return map[string]any{"data": []any{}}
		case "thread/list":
			if params["ancestorThreadId"] != managedThreadID {
				t.Errorf("thread/list asked for %v", params)
			}
			return map[string]any{"data": children}
		case "thread/queue/list":
			return map[string]any{"data": queued}
		}
		return nil
	})
	codex := isolatedManagedRuntime(t)
	session := &surface.Session{ID: managedThreadID, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Source: "agenthail", Transport: codexTransportManaged, Cwd: t.TempDir()}
	_, err := codex.PrepareMove(context.Background(), session, surface.AdoptRequest{})
	var refusal surface.MoveRefusal
	if !errors.As(err, &refusal) {
		t.Fatalf("PrepareMove returned %v, want a refusal", err)
	}
	release, err := holdCodexThread(context.Background(), managedThreadID)
	if err != nil {
		t.Fatalf("a refused move kept the thread held: %v", err)
	}
	release()
	return refusal
}

func TestACodexThreadWithSubagentsIsRefusedNamingThem(t *testing.T) {
	refusal := refusedCodexMove(t, []any{map[string]any{"id": "child-one"}, map[string]any{"id": "child-two"}}, nil)
	if !strings.Contains(refusal.Reason, "child-one") || !strings.Contains(refusal.Reason, "child-two") {
		t.Fatalf("the refusal does not name the subagent threads: %s", refusal.Reason)
	}
}

func TestACodexThreadWithQueuedMessagesIsRefused(t *testing.T) {
	refusal := refusedCodexMove(t, nil, []any{map[string]any{"id": "q", "clientUserMessageId": "c", "input": []any{map[string]any{"type": "text", "text": "later"}}}})
	if !strings.Contains(refusal.Reason, "queued") {
		t.Fatalf("the refusal does not name the queue: %s", refusal.Reason)
	}
}
