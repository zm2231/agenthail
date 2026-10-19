package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestActiveLastUsesDaemonPageAndDoesNotReadProvider(t *testing.T) {
	session := surface.Session{ID: "s", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop}
	provider := &cliReadSurface{cliSurface: &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"s": session}}, result: &surface.SessionReadResult{Source: "provider"}}
	app, _ := cliFixture(t, provider.cliSurface)
	app.Surfaces[0].Surface = provider
	app.catalogDaemonRunning = func() bool { return true }
	app.daemonSessionPageReader = func() (sessionPageReader, error) {
		return sessionPageFixture{result: &surface.SessionReadResult{Source: "journal", Exchanges: []surface.Exchange{{User: "q", Assistant: "a"}}, NextBefore: 42}}, nil
	}

	output, err := captureStdout(t, func() error { return app.Run([]string{"last", "codex:s", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 0 || !strings.Contains(output, `"source":"journal"`) || !strings.Contains(output, `"nextBefore":42`) {
		t.Fatalf("provider requests=%d output=%s", len(provider.requests), output)
	}
}

func TestActiveDaemonFailureDoesNotFallBackToProvider(t *testing.T) {
	session := surface.Session{ID: "s", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop}
	provider := &cliReadSurface{cliSurface: &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"s": session}}, result: &surface.SessionReadResult{Source: "provider"}}
	app, _ := cliFixture(t, provider.cliSurface)
	app.Surfaces[0].Surface = provider
	app.catalogDaemonRunning = func() bool { return true }
	app.daemonSessionPageReader = func() (sessionPageReader, error) { return nil, errors.New("daemon unavailable") }

	if err := app.Run([]string{"last", "codex:s"}); err == nil || !strings.Contains(err.Error(), "active daemon session page") {
		t.Fatalf("err=%v", err)
	}
	if len(provider.requests) != 0 {
		t.Fatalf("provider was read %d times", len(provider.requests))
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
			session := surface.Session{ID: "s", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: test.status}
			provider := &cliReadSurface{cliSurface: &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"s": session}, caps: surface.Capabilities{Reply: true}}}
			app, _ := cliFixture(t, provider.cliSurface)
			app.Surfaces[0].Surface = provider
			app.catalogDaemonRunning = func() bool { return true }
			app.daemonSessionPageReader = func() (sessionPageReader, error) {
				return sessionPageFixture{result: &surface.SessionReadResult{Source: "journal", Exchanges: []surface.Exchange{{Assistant: "answer"}}}}, nil
			}
			output, err := captureStdout(t, func() error { return app.Run([]string{"reply", "codex:s", "--json"}) })
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

type pagedSessionPages struct {
	pages    map[int64]*surface.SessionReadResult
	requests *[]surface.SessionReadRequest
}

func (f pagedSessionPages) ReadSession(_ context.Context, _ *surface.Session, request surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	*f.requests = append(*f.requests, request)
	return f.pages[request.Before], nil
}

func TestLastCountReadsOlderPagesUntilItHasThatManyExchanges(t *testing.T) {
	session := surface.Session{ID: "s", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop}
	provider := &cliReadSurface{cliSurface: &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"s": session}}}
	app, _ := cliFixture(t, provider.cliSurface)
	app.Surfaces[0].Surface = provider
	app.catalogDaemonRunning = func() bool { return true }
	var requests []surface.SessionReadRequest
	app.daemonSessionPageReader = func() (sessionPageReader, error) {
		return pagedSessionPages{requests: &requests, pages: map[int64]*surface.SessionReadResult{
			0:  {Source: "journal", Exchanges: []surface.Exchange{{User: "newest question", Assistant: "newest answer"}}, NextBefore: 17},
			17: {Source: "journal", Exchanges: []surface.Exchange{{User: "older question", Assistant: "older answer"}}, NextBefore: 9},
		}}, nil
	}

	output, err := captureStdout(t, func() error { return app.Run([]string{"last", "codex:s", "2", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	older, newest := strings.Index(output, "older question"), strings.Index(output, "newest question")
	if older < 0 || newest < older || !strings.Contains(output, `"nextBefore":9`) {
		t.Fatalf("output=%s", output)
	}
	if len(requests) != 2 || requests[0].Limit != 2 || requests[1].Limit != 1 || requests[1].Before != 17 {
		t.Fatalf("requests=%+v", requests)
	}
}

func TestLastResolvesAnExitedSessionByItsName(t *testing.T) {
	provider := &cliReadSurface{cliSurface: &cliSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{}, resolveErr: surface.ErrSessionNotFound}}
	app, r := cliFixture(t, provider.cliSurface)
	app.Surfaces[0].Surface = provider
	if err := r.RegisterSession(surface.Session{ID: "11111111-2222-3333-4444-555555555555", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "pairing-fixture", Status: surface.StatusIdle}); err != nil {
		t.Fatal(err)
	}
	app.catalogDaemonRunning = func() bool { return true }
	app.daemonSessionPageReader = func() (sessionPageReader, error) {
		return sessionPageFixture{result: &surface.SessionReadResult{Source: "journal", Exchanges: []surface.Exchange{{User: "fixture question", Assistant: "fixture answer"}}}}, nil
	}

	output, err := captureStdout(t, func() error { return app.Run([]string{"last", "pairing-fixture", "--json"}) })
	if err != nil || !strings.Contains(output, `"session":"11111111-2222-3333-4444-555555555555"`) || !strings.Contains(output, "fixture answer") {
		t.Fatalf("output=%s err=%v", output, err)
	}
}
