package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestActiveLastUsesDaemonPageAndDoesNotReadProvider(t *testing.T) {
	session := surface.Session{ID: "s", Surface: surface.KindCodex}
	provider := &cliReadSurface{cliSurface: &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"s": session}}, result: &surface.SessionReadResult{Source: "provider"}}
	app, _ := cliFixture(t, provider.cliSurface)
	app.Surfaces[0].Surface = provider
	app.catalogDaemonRunning = func() bool { return true }
	app.daemonSessionPageReader = func() (sessionPageReader, error) {
		return sessionPageFixture{result: &surface.SessionReadResult{Source: "journal", Exchanges: []surface.Exchange{{User: "q", Assistant: "a"}}, NextBefore: 42}}, nil
	}

	output, err := captureStdout(t, func() error { return app.cmdLast([]string{"codex:s", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 0 || !strings.Contains(output, `"source":"journal"`) || !strings.Contains(output, `"nextBefore":42`) {
		t.Fatalf("provider requests=%d output=%s", len(provider.requests), output)
	}
}

func TestActiveDaemonFailureDoesNotFallBackToProvider(t *testing.T) {
	session := surface.Session{ID: "s", Surface: surface.KindCodex}
	provider := &cliReadSurface{cliSurface: &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"s": session}}, result: &surface.SessionReadResult{Source: "provider"}}
	app, _ := cliFixture(t, provider.cliSurface)
	app.Surfaces[0].Surface = provider
	app.catalogDaemonRunning = func() bool { return true }
	app.daemonSessionPageReader = func() (sessionPageReader, error) { return nil, errors.New("daemon unavailable") }

	if err := app.cmdLast([]string{"codex:s"}); err == nil || !strings.Contains(err.Error(), "active daemon session page") {
		t.Fatalf("err=%v", err)
	}
	if len(provider.requests) != 0 {
		t.Fatalf("provider was read %d times", len(provider.requests))
	}
}

func TestDaemonSessionPageClientUsesAuthenticatedJournalCursor(t *testing.T) {
	var gotPath, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		gotAuth = r.Header.Get("Authorization")
		if gotAuth != "Bearer fixture-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"readSource": "journal", "journalSeq": 9,
			"exchanges": []surface.Exchange{{User: "q", Assistant: "a"}},
			"timeline":  surface.SessionTimeline{Items: []surface.TimelineItem{{ID: "item", Kind: "message", Role: "assistant", Text: "a"}}, NextBefore: 7, Source: "journal"},
		})
	}))
	defer server.Close()
	client := &daemonSessionPageClient{baseURL: server.URL, token: "fixture-token", httpClient: server.Client()}
	read, err := client.ReadSession(context.Background(), &surface.Session{ID: "s"}, surface.SessionReadRequest{Limit: 1, Before: 123})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer fixture-token" || !strings.Contains(gotPath, "id=s") || !strings.Contains(gotPath, "timelineBefore=123") || !strings.Contains(gotPath, "limit=4") {
		t.Fatalf("path=%q auth=%q", gotPath, gotAuth)
	}
	if read.Source != "journal" || read.JournalSeq != 9 || read.NextBefore != 7 || len(read.Items) != 1 {
		t.Fatalf("read=%+v", read)
	}
}

func TestDaemonSessionPageClientReturnsTypedHistoryGap(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "history_gap", "message": "older history is gone"}, "earliestSeq": 8, "latestSeq": 20})
	}))
	defer server.Close()
	client := &daemonSessionPageClient{baseURL: server.URL, token: "fixture-token", httpClient: server.Client()}
	_, err := client.ReadSession(context.Background(), &surface.Session{ID: "s"}, surface.SessionReadRequest{Limit: 4, Before: 2})
	var pageErr *daemonSessionPageError
	if !errors.As(err, &pageErr) || pageErr.Code != "history_gap" || pageErr.EarliestSeq != 8 || pageErr.LatestSeq != 20 {
		t.Fatalf("err=%v typed=%+v", err, pageErr)
	}
}

func TestDaemonSessionPageClientHonorsDeadlineWithoutRetry(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		<-r.Context().Done()
	}))
	defer server.Close()
	client := &daemonSessionPageClient{baseURL: server.URL, token: "fixture-token", httpClient: server.Client()}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := client.ReadSession(ctx, &surface.Session{ID: "s"}, surface.SessionReadRequest{Limit: 4})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) || requests.Load() != 1 {
		t.Fatalf("err=%v requests=%d", err, requests.Load())
	}
}

func TestActiveReplyDerivesDoneFromResolvedSessionStatus(t *testing.T) {
	for _, test := range []struct {
		name   string
		status surface.SessionStatus
		done   bool
	}{
		{name: "idle", status: surface.StatusIdle, done: true},
		{name: "busy", status: surface.StatusBusy, done: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := surface.Session{ID: "s", Surface: surface.KindCodex, Status: test.status}
			provider := &cliReadSurface{cliSurface: &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"s": session}, caps: surface.Capabilities{Reply: true}}}
			app, _ := cliFixture(t, provider.cliSurface)
			app.Surfaces[0].Surface = provider
			app.catalogDaemonRunning = func() bool { return true }
			app.daemonSessionPageReader = func() (sessionPageReader, error) {
				return sessionPageFixture{result: &surface.SessionReadResult{Source: "journal", Exchanges: []surface.Exchange{{Assistant: "answer"}}}}, nil
			}
			output, err := captureStdout(t, func() error { return app.cmdReply([]string{"codex:s", "--json"}) })
			if err != nil || !strings.Contains(output, `"done":`+map[bool]string{true: "true", false: "false"}[test.done]) {
				t.Fatalf("output=%s err=%v", output, err)
			}
		})
	}
}

type sessionPageFixture struct {
	result *surface.SessionReadResult
	err    error
}

func (f sessionPageFixture) ReadSession(context.Context, *surface.Session, surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	return f.result, f.err
}
