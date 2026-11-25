package codexsurface

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/surface"
)

func searchEntry(id, name, cwd string) map[string]any {
	return map[string]any{"thread": map[string]any{"id": id, "name": name, "cwd": cwd, "source": "vscode"}, "snippet": name}
}

func TestCodexSearchReportsStreamedMatchesBeforeTheFinalPage(t *testing.T) {
	early := searchEntry("019f004a-0000-7000-8000-0000000000e1", "early", "/work")
	late := searchEntry("019f004a-0000-7000-8000-0000000000e2", "late", "/work")
	reported := make(chan struct{})
	var daemon *codextest.Daemon
	daemon = startManagedCodex(t, func(method string, _ map[string]any) map[string]any {
		if method != "thread/search/stream" {
			return nil
		}
		daemon.Notify("thread/search/partial", map[string]any{"requestId": 1, "data": []any{early}})
		daemon.Notify("thread/search/partial", map[string]any{"requestId": 1, "data": []any{early}})
		select {
		case <-reported:
		case <-time.After(5 * time.Second):
			return map[string]any{"error": map[string]any{"code": -32603, "message": "partial was not reported before the final page"}}
		}
		return map[string]any{"data": []any{early, late}}
	})
	var found []surface.SessionSearchResult
	results, err := isolatedManagedRuntime(t).SearchSessions(context.Background(), "match", 20, func(result surface.SessionSearchResult) {
		found = append(found, result)
		if len(found) == 1 {
			close(reported)
		}
	})
	if err != nil || len(results) != 2 || len(found) != 1 || found[0].Session.Name != "early" || found[0].Snippet != "early" {
		t.Fatalf("results=%+v found=%+v err=%v", results, found, err)
	}
	if len(daemon.Calls("thread/search")) != 0 {
		t.Fatal("streaming search also ran the blocking search")
	}
}

func TestCodexSearchFallsBackWhenStreamingIsMissing(t *testing.T) {
	daemon := startManagedCodex(t, func(method string, _ map[string]any) map[string]any {
		switch method {
		case "thread/search/stream":
			return map[string]any{"error": map[string]any{"code": -32601, "message": "method not found"}}
		case "thread/search":
			return map[string]any{"data": []any{searchEntry("019f004a-0000-7000-8000-0000000000e3", "older codex", "/work")}}
		}
		return nil
	})
	results, err := isolatedManagedRuntime(t).SearchSessions(context.Background(), "older", 20, nil)
	if err != nil || len(results) != 1 || results[0].Session.Name != "older codex" || len(daemon.Calls("thread/search")) != 1 {
		t.Fatalf("results=%+v err=%v", results, err)
	}
}

func TestCodexSearchStopsAndClosesItsConnectionWhenTheCallerGivesUp(t *testing.T) {
	release := make(chan struct{})
	daemon := startManagedCodex(t, func(method string, _ map[string]any) map[string]any {
		if method == "thread/search/stream" {
			<-release
		}
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := isolatedManagedRuntime(t).SearchSessions(ctx, "slow", 20, nil)
	close(release)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "did not finish in time") || time.Since(start) > 2*time.Second {
		t.Fatalf("err=%v after %v", err, time.Since(start))
	}
	deadline := time.Now().Add(2 * time.Second)
	for daemon.Connections() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("search left its app-server connection open, so Codex keeps scanning")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCodexSearchReportsALostConnection(t *testing.T) {
	startManagedCodex(t, func(method string, _ map[string]any) map[string]any {
		if method == "thread/search/stream" {
			return map[string]any{"drop": true}
		}
		return nil
	})
	if _, err := isolatedManagedRuntime(t).SearchSessions(context.Background(), "gone", 20, nil); err == nil || !strings.Contains(err.Error(), "connection closed") {
		t.Fatalf("err=%v", err)
	}
}

func titlePages(pages map[string][]any, search func() map[string]any) codextest.Responder {
	return func(method string, params map[string]any) map[string]any {
		switch method {
		case "thread/list":
			cursor, _ := params["cursor"].(string)
			next := ""
			if cursor == "" && pages["p2"] != nil {
				next = "p2"
			}
			return map[string]any{"data": pages[cursor], "nextCursor": next}
		case "thread/search/stream":
			if search != nil {
				return search()
			}
			return map[string]any{"data": []any{}}
		}
		return nil
	}
}

func titled(id, name, cwd string) map[string]any {
	return map[string]any{"id": id, "name": name, "cwd": cwd, "source": "vscode"}
}

func TestCodexResolvePrefersAnExactTitleOnALaterPage(t *testing.T) {
	daemon := startManagedCodex(t, titlePages(map[string][]any{
		"":   {titled("019f004a-0000-7000-8000-0000000000f1", "release notes draft", "/a")},
		"p2": {titled("019f004a-0000-7000-8000-0000000000f2", "release notes", "/b")},
	}, nil))
	session, err := isolatedManagedRuntime(t).Resolve(context.Background(), "release notes")
	if err != nil || session == nil || session.ID != "019f004a-0000-7000-8000-0000000000f2" {
		t.Fatalf("session=%+v err=%v", session, err)
	}
	if len(daemon.Calls("thread/search/stream"))+len(daemon.Calls("thread/search")) != 0 {
		t.Fatal("an exact title still ran a history search")
	}
}

func TestCodexResolveRejectsAnExactTitleRepeatedOnALaterPage(t *testing.T) {
	startManagedCodex(t, titlePages(map[string][]any{
		"":   {titled("019f004a-0000-7000-8000-0000000000f3", "duplicate", "/one")},
		"p2": {titled("019f004a-0000-7000-8000-0000000000f4", "DUPLICATE", "/two")},
	}, nil))
	session, err := isolatedManagedRuntime(t).Resolve(context.Background(), "duplicate")
	if err == nil || session != nil || !strings.Contains(err.Error(), "0000000000f3") || !strings.Contains(err.Error(), "0000000000f4") {
		t.Fatalf("session=%+v err=%v", session, err)
	}
}

func TestCodexResolveStillFindsAConversationByCwdOrIDPrefix(t *testing.T) {
	const id = "019f004a-0000-7000-8000-0000000000f5"
	search := func() map[string]any {
		return map[string]any{"data": []any{searchEntry(id, "unrelated title", "/work/fieldnotes")}}
	}
	for _, target := range []string{"fieldnotes", "019f004a-0000-7000-8000-0000000000f"} {
		startManagedCodex(t, titlePages(map[string][]any{"": {}}, search))
		session, err := isolatedManagedRuntime(t).Resolve(context.Background(), target)
		if err != nil || session == nil || session.ID != id {
			t.Fatalf("target=%q session=%+v err=%v", target, session, err)
		}
	}
}
