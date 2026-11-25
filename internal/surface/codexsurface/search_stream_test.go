package codexsurface

import (
	"context"
	"testing"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestCodexSearchReportsStreamedMatchesBeforeTheFinalPage(t *testing.T) {
	early := map[string]any{"thread": map[string]any{"id": "019f004a-0000-7000-8000-0000000000e1", "name": "early", "source": "vscode"}, "snippet": "first"}
	late := map[string]any{"thread": map[string]any{"id": "019f004a-0000-7000-8000-0000000000e2", "name": "late", "source": "vscode"}, "snippet": "second"}
	var daemon *codextest.Daemon
	daemon = startManagedCodex(t, func(method string, _ map[string]any) map[string]any {
		if method == "thread/search/stream" {
			daemon.Notify("thread/search/partial", map[string]any{"requestId": 1, "data": []any{early}})
			daemon.Notify("thread/search/partial", map[string]any{"requestId": 1, "data": []any{early}})
			return map[string]any{"data": []any{early, late}}
		}
		return nil
	})
	var found []surface.SessionSearchResult
	results, err := isolatedManagedRuntime(t).SearchSessions(context.Background(), "match", 20, func(result surface.SessionSearchResult) {
		found = append(found, result)
	})
	if err != nil || len(results) != 2 || len(found) != 1 || found[0].Session.Name != "early" || found[0].Snippet != "first" {
		t.Fatalf("results=%+v found=%+v err=%v", results, found, err)
	}
	if len(daemon.Calls("thread/search")) != 0 {
		t.Fatal("streaming search also ran the blocking search")
	}
}

func TestCodexSearchFallsBackWhenStreamingIsMissing(t *testing.T) {
	thread := map[string]any{"thread": map[string]any{"id": "019f004a-0000-7000-8000-0000000000e3", "name": "older codex", "source": "vscode"}}
	daemon := startManagedCodex(t, func(method string, _ map[string]any) map[string]any {
		switch method {
		case "thread/search/stream":
			return map[string]any{"error": map[string]any{"code": -32601, "message": "method not found"}}
		case "thread/search":
			return map[string]any{"data": []any{thread}}
		}
		return nil
	})
	results, err := isolatedManagedRuntime(t).SearchSessions(context.Background(), "older", 20, nil)
	if err != nil || len(results) != 1 || results[0].Session.Name != "older codex" || len(daemon.Calls("thread/search")) != 1 {
		t.Fatalf("results=%+v err=%v", results, err)
	}
}
